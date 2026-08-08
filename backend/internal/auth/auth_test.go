package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func proxyConfig() Config {
	return Config{
		Mode:        ModeProxy,
		UserHeader:  "X-Forwarded-User",
		NameHeader:  "X-Forwarded-Name",
		RolesHeader: "X-Forwarded-Groups",
	}
}

// captured runs a request through the middleware and reports the identity the
// handler saw.
func captured(t *testing.T, cfg Config, headers map[string]string) (Identity, bool) {
	t.Helper()

	var got Identity
	var ok bool
	handler := Middleware(cfg, discardLogger())(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			got, ok = FromContext(r.Context())
		}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return got, ok
}

func TestParseMode(t *testing.T) {
	cases := []struct {
		in    string
		want  Mode
		valid bool
	}{
		{"", ModeNone, true},
		{"none", ModeNone, true},
		{"NONE", ModeNone, true},
		{" proxy ", ModeProxy, true},
		{"oidc", ModeNone, false},
		{"nonsense", ModeNone, false},
	}
	for _, c := range cases {
		got, ok := ParseMode(c.in)
		if got != c.want || ok != c.valid {
			t.Errorf("ParseMode(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.valid)
		}
	}
}

func TestModeNoneLeavesRequestsAnonymous(t *testing.T) {
	// Even a request carrying the header is anonymous when the mode is off, so
	// turning authentication on is a deliberate act rather than something a
	// caller can trigger by sending a header.
	_, ok := captured(t, Config{Mode: ModeNone}, map[string]string{
		"X-Forwarded-User": "someone",
	})
	if ok {
		t.Error("mode none must not authenticate anyone")
	}
}

func TestProxyModeReadsTheIdentity(t *testing.T) {
	id, ok := captured(t, proxyConfig(), map[string]string{
		"X-Forwarded-User":   "sovanna.hang",
		"X-Forwarded-Name":   "Hang Sovanna",
		"X-Forwarded-Groups": "planners, finance ,",
	})
	if !ok {
		t.Fatal("expected the request to be authenticated")
	}
	if id.Subject != "sovanna.hang" {
		t.Errorf("subject = %q", id.Subject)
	}
	if id.Name != "Hang Sovanna" {
		t.Errorf("name = %q", id.Name)
	}
	if len(id.Roles) != 2 || id.Roles[0] != "planners" || id.Roles[1] != "finance" {
		t.Errorf("roles = %v, want [planners finance] with blanks dropped", id.Roles)
	}
}

func TestProxyModeWithoutTheHeaderStaysAnonymous(t *testing.T) {
	// A request that skipped the proxy must not be silently promoted.
	if _, ok := captured(t, proxyConfig(), nil); ok {
		t.Error("a request with no identity header must not be authenticated")
	}
}

func TestActorPrefersTheAuthenticatedIdentity(t *testing.T) {
	ctx := WithIdentity(context.Background(), Identity{Subject: "sovanna.hang"})

	// The whole point: a caller claiming to be somebody else is ignored.
	if got := Actor(ctx, "someone-else"); got != "sovanna.hang" {
		t.Errorf("Actor = %q, want the authenticated subject to win", got)
	}
}

func TestActorFallsBackToTheDeclaredValue(t *testing.T) {
	// Under mode none there is no identity, so the declared value is all there
	// is. This is the behaviour documented as a gap in docs/security.md.
	if got := Actor(context.Background(), "planner"); got != "planner" {
		t.Errorf("Actor = %q, want planner", got)
	}
	if got := Actor(context.Background(), "  "); got != "SYSTEM" {
		t.Errorf("Actor = %q, want SYSTEM when nothing is declared", got)
	}
}

func TestIdentityString(t *testing.T) {
	if got := (Identity{Subject: "a.user", Name: "A User"}).String(); got != "a.user (A User)" {
		t.Errorf("String = %q", got)
	}
	if got := (Identity{Subject: "a.user"}).String(); got != "a.user" {
		t.Errorf("String = %q", got)
	}
	if got := (Identity{Subject: "a.user", Name: "a.user"}).String(); got != "a.user" {
		t.Errorf("String = %q, want the name not repeated", got)
	}
}

package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeSessions resolves one token and fails on demand.
type fakeSessions struct {
	token string
	id    Identity
	err   error
	calls int
}

func (f *fakeSessions) ResolveSession(_ context.Context, token string) (Identity, bool, error) {
	f.calls++
	if f.err != nil {
		return Identity{}, false, f.err
	}
	if token != f.token {
		return Identity{}, false, nil
	}
	return f.id, true, nil
}

// identityOf runs a request through the local-mode middleware and reports the
// identity it attached.
func identityOf(t *testing.T, sessions SessionResolver, prepare func(*http.Request)) (Identity, bool) {
	t.Helper()

	cfg := Config{Mode: ModeLocal, Sessions: sessions, CookieName: "spp_session"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	var got Identity
	var ok bool
	handler := Middleware(cfg, log)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = FromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/storage-capacity", nil)
	if prepare != nil {
		prepare(req)
	}
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return got, ok
}

func TestLocalModeReadsTheSessionCookie(t *testing.T) {
	sessions := &fakeSessions{
		token: "good-token",
		id:    Identity{Subject: "warehouse", Name: "Warehouse Supervisor", Roles: []string{RoleWarehouse}},
	}

	id, ok := identityOf(t, sessions, func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "spp_session", Value: "good-token"})
	})
	if !ok {
		t.Fatal("a valid session cookie did not authenticate the request")
	}
	if id.Subject != "warehouse" || !id.HasRole(RoleWarehouse) {
		t.Fatalf("wrong identity attached: %+v", id)
	}
}

func TestLocalModeReadsABearerToken(t *testing.T) {
	// The header form is what makes the API usable from curl and from a test,
	// without a cookie jar.
	sessions := &fakeSessions{token: "good-token", id: Identity{Subject: "admin"}}

	id, ok := identityOf(t, sessions, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer good-token")
	})
	if !ok || id.Subject != "admin" {
		t.Fatalf("bearer token did not authenticate: %+v ok=%v", id, ok)
	}
}

func TestLocalModeLeavesBadTokensAnonymous(t *testing.T) {
	sessions := &fakeSessions{token: "good-token", id: Identity{Subject: "admin"}}

	if _, ok := identityOf(t, sessions, func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "spp_session", Value: "stale-token"})
	}); ok {
		t.Fatal("an unknown token authenticated the request")
	}
	if _, ok := identityOf(t, sessions, nil); ok {
		t.Fatal("a request with no token authenticated")
	}
	if sessions.calls != 1 {
		t.Fatalf("the resolver was called %d times; a request with no token should not reach the database", sessions.calls)
	}
}

func TestLocalModeTreatsAResolverFailureAsAnonymous(t *testing.T) {
	// If the database is unreachable the request must not be waved through,
	// and it must not be answered with a 500 that tells an unauthenticated
	// caller about the state of the deployment. It becomes anonymous, and the
	// guard turns that into a 401.
	sessions := &fakeSessions{token: "good-token", err: errors.New("connection refused")}

	if _, ok := identityOf(t, sessions, func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "spp_session", Value: "good-token"})
	}); ok {
		t.Fatal("a failed session lookup authenticated the request")
	}
}

func TestTokenFromRequestPrefersTheCookie(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "spp_session", Value: "from-cookie"})
	r.Header.Set("Authorization", "Bearer from-header")

	if got := TokenFromRequest(r, "spp_session"); got != "from-cookie" {
		t.Fatalf("got %q, want the cookie to win", got)
	}
}

func TestTokenFromRequestIgnoresOtherSchemes(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Basic c29tZTpib2R5")

	if got := TokenFromRequest(r, "spp_session"); got != "" {
		t.Fatalf("got %q from a Basic header; only Bearer carries a session token", got)
	}
}

func TestSessionCookieNameHasADefault(t *testing.T) {
	if got := (Config{}).SessionCookieName(); got == "" {
		t.Fatal("an unconfigured cookie name would set a cookie with no name")
	}
}

func TestLoginRequiredOnlyForLocal(t *testing.T) {
	// Proxy mode rejects nothing: a request arriving without the header has
	// bypassed the proxy, which is a deployment fault to fix rather than a
	// login screen to show.
	if (Config{Mode: ModeProxy}).LoginRequired() {
		t.Fatal("proxy mode asked for a login screen it has no way to serve")
	}
	if (Config{Mode: ModeNone}).LoginRequired() {
		t.Fatal("mode none asked for a login")
	}
	if !(Config{Mode: ModeLocal}).LoginRequired() {
		t.Fatal("local mode did not ask for a login")
	}
}

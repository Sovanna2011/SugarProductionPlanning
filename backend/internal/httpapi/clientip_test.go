package httpapi

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func request(remote, forwarded string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r.RemoteAddr = remote
	if forwarded != "" {
		r.Header.Set("X-Forwarded-For", forwarded)
	}
	return r
}

func TestClientIPIgnoresTheHeaderFromAnUntrustedPeer(t *testing.T) {
	// Anyone can send this header. Believing it unconditionally would let a
	// caller pick their own identity for the rate limiter — a fresh one per
	// request — and write whatever they liked into the session log.
	trusted, err := ParseTrustedProxies("10.0.0.7")
	if err != nil {
		t.Fatal(err)
	}

	got := ClientIP(request("203.0.113.9:51000", "1.2.3.4"), trusted)
	if got != "203.0.113.9" {
		t.Fatalf("got %q; the header was believed from an address that is not a configured proxy", got)
	}

	// With nothing configured the header is ignored entirely.
	if got := ClientIP(request("203.0.113.9:51000", "1.2.3.4"), nil); got != "203.0.113.9" {
		t.Fatalf("got %q with no trusted proxies configured", got)
	}
}

func TestClientIPBelievesATrustedProxy(t *testing.T) {
	// Without this, every request behind a reverse proxy looks like it came
	// from the proxy, and a per-client limit becomes one global limit that the
	// first attacker closes for everybody.
	trusted, err := ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}

	got := ClientIP(request("10.0.0.7:44000", "198.51.100.23"), trusted)
	if got != "198.51.100.23" {
		t.Fatalf("got %q, want the address the proxy reported", got)
	}
}

func TestClientIPUsesTheLastUntrustedHop(t *testing.T) {
	// Entries to the left of the trusted chain were supplied by the caller and
	// mean nothing; the rightmost untrusted one is the furthest the deployment
	// can actually vouch for.
	trusted, err := ParseTrustedProxies("10.0.0.0/8,172.16.0.0/12")
	if err != nil {
		t.Fatal(err)
	}

	got := ClientIP(request("10.0.0.7:44000", "1.2.3.4, 198.51.100.23, 172.16.5.5"), trusted)
	if got != "198.51.100.23" {
		t.Fatalf("got %q, want the last hop the trusted chain saw", got)
	}
}

func TestClientIPFallsBackWhenTheHeaderIsRubbish(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.7")
	if err != nil {
		t.Fatal(err)
	}

	for _, header := range []string{"", "not-an-address", "   "} {
		if got := ClientIP(request("10.0.0.7:44000", header), trusted); got != "10.0.0.7" {
			t.Fatalf("header %q gave %q; want the peer address", header, got)
		}
	}
}

func TestParseTrustedProxies(t *testing.T) {
	nets, err := ParseTrustedProxies(" 10.0.0.7 , 172.16.0.0/12 ,, 2001:db8::1 ")
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 3 {
		t.Fatalf("parsed %d entries, want 3", len(nets))
	}

	// A bare address means that one host, not everything sharing its prefix.
	if nets[0].Contains(mustIP(t, "10.0.0.8")) {
		t.Fatal("a bare address was widened into a block")
	}
	if !nets[0].Contains(mustIP(t, "10.0.0.7")) {
		t.Fatal("a bare address does not match itself")
	}

	if _, err := ParseTrustedProxies("10.0.0.7, nonsense"); err == nil {
		t.Fatal("a value that is neither an address nor a block was accepted, which would " +
			"silently leave the proxy untrusted")
	}
	if nets, err := ParseTrustedProxies(""); err != nil || nets != nil {
		t.Fatalf("empty gave %v, %v; want no entries and no error", nets, err)
	}
}

func mustIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("%q is not an address", s)
	}
	return ip
}

package httpapi

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the address to attribute a request to.
//
// X-Forwarded-For is believed only when the request actually arrived from a
// configured proxy. Anyone can send that header, so trusting it unconditionally
// would let a caller pick their own identity for the rate limiter and write
// whatever they liked into the session log. Trusting it never is not an option
// either: behind a reverse proxy every request appears to come from the proxy,
// which would turn a per-client limit into one global limit that the first
// attacker closes for everybody.
//
// Only the last entry of the header is used — the address the trusted proxy
// itself saw. Earlier entries were supplied by the caller and mean nothing.
func ClientIP(r *http.Request, trusted []*net.IPNet) string {
	peer := peerIP(r)
	if peer == nil {
		// An address net/http gave us that does not parse: record it as it
		// came rather than inventing one.
		return r.RemoteAddr
	}
	if len(trusted) == 0 || !inAny(peer, trusted) {
		return peer.String()
	}

	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded == "" {
		return peer.String()
	}

	parts := strings.Split(forwarded, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if ip := net.ParseIP(candidate); ip != nil {
			// Walk back past any further trusted hops in the chain, so a
			// proxy behind a load balancer still resolves to the person.
			if inAny(ip, trusted) && i > 0 {
				continue
			}
			return ip.String()
		}
	}
	return peer.String()
}

// peerIP is the address the connection actually came from.
func peerIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

func inAny(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ParseTrustedProxies reads a comma-separated list of addresses or CIDR blocks.
//
// A bare address is treated as a single-host block, because "10.0.0.7" is what
// somebody writes when they mean that one proxy.
func ParseTrustedProxies(raw string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, block, err := net.ParseCIDR(part); err == nil {
			out = append(out, block)
			continue
		}
		ip := net.ParseIP(part)
		if ip == nil {
			return nil, &net.ParseError{Type: "IP address or CIDR block", Text: part}
		}
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	return out, nil
}

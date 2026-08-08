// Package auth establishes who is making a request.
//
// The mechanism is deliberately pluggable, because which one is right depends
// on what the factory already runs. What is not negotiable is where the rest
// of the system reads the actor from: once a request is authenticated, the
// audit trail takes the identity from here and never from the request body.
//
// Mode "none" is the default and preserves the system's original behaviour, so
// nothing changes until a deployment opts in. It is not safe outside a trusted
// network; see docs/security.md.
package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
)

// Mode selects how a request is authenticated.
type Mode string

const (
	// ModeNone performs no authentication. Every request is anonymous, and the
	// actor recorded in the audit trail falls back to whatever the caller
	// declared. Suitable only on a trusted network.
	ModeNone Mode = "none"

	// ModeProxy trusts an upstream reverse proxy to have authenticated the
	// user and to pass the identity in a header. The application must then be
	// reachable only through that proxy, or the header can simply be forged.
	ModeProxy Mode = "proxy"
)

// ParseMode reads a configured mode, defaulting to none.
func ParseMode(s string) (Mode, bool) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case "", ModeNone:
		return ModeNone, true
	case ModeProxy:
		return ModeProxy, true
	default:
		return ModeNone, false
	}
}

// Identity is the authenticated user.
type Identity struct {
	// Subject is the stable identifier recorded in the audit trail.
	Subject string
	// Name is a display name when the mechanism supplies one.
	Name string
	// Roles carries any group membership the mechanism supplies. Nothing
	// enforces roles yet; they are carried so authorisation can be added
	// without changing this plumbing again.
	Roles []string
}

// String renders the identity for an audit column.
func (i Identity) String() string {
	if i.Name != "" && i.Name != i.Subject {
		return i.Subject + " (" + i.Name + ")"
	}
	return i.Subject
}

type contextKey struct{}

// WithIdentity returns a context carrying the authenticated user.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// FromContext returns the authenticated user, if the request was
// authenticated at all.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(contextKey{}).(Identity)
	return id, ok && id.Subject != ""
}

// Actor returns the name to record in an audit column.
//
// An authenticated identity always wins. `declared` is the value the caller
// put in the request body, and is used only when the request carries no
// identity — which is to say, only under ModeNone.
func Actor(ctx context.Context, declared string) string {
	if id, ok := FromContext(ctx); ok {
		return id.Subject
	}
	if declared = strings.TrimSpace(declared); declared != "" {
		return declared
	}
	return "SYSTEM"
}

// Config describes how to authenticate.
type Config struct {
	Mode Mode
	// UserHeader carries the subject under ModeProxy.
	UserHeader string
	// NameHeader and RolesHeader are optional extras under ModeProxy.
	NameHeader  string
	RolesHeader string
}

// Middleware authenticates each request and attaches the identity.
//
// It does not reject unauthenticated requests: under ModeNone there is nothing
// to reject, and under ModeProxy a request without the header has bypassed the
// proxy, which is a deployment fault worth surfacing loudly rather than a
// per-request 401. Rejecting is the next step once a mechanism is chosen.
func Middleware(cfg Config, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if cfg.Mode == ModeNone {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			subject := strings.TrimSpace(r.Header.Get(cfg.UserHeader))
			if subject == "" {
				log.Warn("request carried no identity header; is it reaching the proxy?",
					"header", cfg.UserHeader, "path", r.URL.Path)
				next.ServeHTTP(w, r)
				return
			}

			id := Identity{
				Subject: subject,
				Name:    strings.TrimSpace(r.Header.Get(cfg.NameHeader)),
			}
			if raw := strings.TrimSpace(r.Header.Get(cfg.RolesHeader)); raw != "" {
				for _, role := range strings.Split(raw, ",") {
					if role = strings.TrimSpace(role); role != "" {
						id.Roles = append(id.Roles, role)
					}
				}
			}
			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
		})
	}
}

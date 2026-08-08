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
	"time"
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

	// ModeLocal authenticates against the application's own user table: a
	// user signs in with a name and password and receives a session. It needs
	// no infrastructure beyond the database the system already has, which
	// makes it the mode to use for evaluation and testing, and a reasonable
	// one for a small site with no directory to integrate with.
	ModeLocal Mode = "local"
)

// ParseMode reads a configured mode, defaulting to none.
func ParseMode(s string) (Mode, bool) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case "", ModeNone:
		return ModeNone, true
	case ModeProxy:
		return ModeProxy, true
	case ModeLocal:
		return ModeLocal, true
	default:
		return ModeNone, false
	}
}

// Roles recognised by the system.
//
// They describe jobs on the site rather than screens in the application, so a
// new screen inherits an answer to "who may use this" instead of needing a new
// role invented for it.
const (
	// RoleAdmin administers the system itself: users, master data, and any
	// override the other roles are refused.
	RoleAdmin = "ADMIN"
	// RolePlanner maintains master data and the daily storage plan.
	RolePlanner = "PLANNER"
	// RoleWarehouse posts receipts, issues, transfers and reservations.
	RoleWarehouse = "WAREHOUSE"
	// RoleViewer may read everything and change nothing.
	RoleViewer = "VIEWER"
)

// KnownRoles lists every role the system enforces, in descending order of
// reach. Anything else is carried but grants nothing.
var KnownRoles = []string{RoleAdmin, RolePlanner, RoleWarehouse, RoleViewer}

// IsKnownRole reports whether a role is one the system acts on.
func IsKnownRole(role string) bool {
	for _, known := range KnownRoles {
		if strings.EqualFold(known, role) {
			return true
		}
	}
	return false
}

// HasAnyRole reports whether the identity holds at least one of the roles.
// An empty list means the operation is open to any authenticated user.
func (i Identity) HasAnyRole(roles ...string) bool {
	if len(roles) == 0 {
		return true
	}
	for _, role := range roles {
		if i.HasRole(role) {
			return true
		}
	}
	return false
}

// Identity is the authenticated user.
type Identity struct {
	// Subject is the stable identifier recorded in the audit trail.
	Subject string
	// Name is a display name when the mechanism supplies one.
	Name string
	// Roles carries the group membership the mechanism supplies. See
	// KnownRoles for the ones the system acts on.
	Roles []string
	// MustChangePassword is set when the account is signed in on a password
	// somebody else chose. The session is real, but it is only good for
	// reading and for changing the password.
	MustChangePassword bool
}

// HasRole reports whether the identity carries a role, case-insensitively
// because identity providers disagree about casing.
func (i Identity) HasRole(role string) bool {
	if role == "" {
		return true
	}
	for _, held := range i.Roles {
		if strings.EqualFold(held, role) {
			return true
		}
	}
	return false
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

// SessionResolver turns an opaque session token into the identity it belongs
// to. It reports ok=false when the token is unknown, expired, revoked, or
// belongs to an account that has since been deactivated — the caller cannot
// tell those apart, and does not need to.
//
// It is an interface so the auth package stays free of the database.
type SessionResolver interface {
	ResolveSession(ctx context.Context, token string) (Identity, bool, error)
}

// Config describes how to authenticate.
type Config struct {
	Mode Mode
	// UserHeader carries the subject under ModeProxy.
	UserHeader string
	// NameHeader and RolesHeader are optional extras under ModeProxy.
	NameHeader  string
	RolesHeader string

	// Sessions resolves session tokens under ModeLocal.
	Sessions SessionResolver
	// CookieName is the session cookie under ModeLocal.
	CookieName string
	// SessionTTL is how long a session issued by a login remains valid.
	SessionTTL time.Duration
	// CookieSecure marks the session cookie Secure. It must be on wherever
	// the application is reached over HTTPS, and off for a plain-HTTP test
	// deployment, or the browser will discard the cookie and no one can sign
	// in.
	CookieSecure bool
}

// SessionCookieName returns the configured cookie name, or the default.
func (c Config) SessionCookieName() string {
	if c.CookieName != "" {
		return c.CookieName
	}
	return "spp_session"
}

// LoginRequired reports whether requests must carry an identity.
//
// Only ModeLocal can answer a rejection usefully — it has a login screen to
// send the caller to. ModeProxy rejects nothing, because a request arriving
// without the header has bypassed the proxy entirely, which is a deployment
// fault to surface loudly rather than a per-request 401.
func (c Config) LoginRequired() bool { return c.Mode == ModeLocal }

// TokenFromRequest returns the session token a request carries, from the
// session cookie or an Authorization: Bearer header.
//
// The header form exists so the API can be exercised with curl and from test
// code without a cookie jar. A browser uses the cookie.
func TokenFromRequest(r *http.Request, cookieName string) string {
	if c, err := r.Cookie(cookieName); err == nil {
		if token := strings.TrimSpace(c.Value); token != "" {
			return token
		}
	}
	const prefix = "Bearer "
	if h := r.Header.Get("Authorization"); len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// Middleware authenticates each request and attaches the identity.
//
// It establishes identity but never rejects: whether an anonymous request is
// allowed to proceed is a question about the route, which Require answers.
// Keeping the two apart is what lets the login endpoint and the static files
// stay reachable while everything else needs a session.
func Middleware(cfg Config, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		switch cfg.Mode {
		case ModeNone:
			return next

		case ModeLocal:
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				token := TokenFromRequest(r, cfg.SessionCookieName())
				if token == "" || cfg.Sessions == nil {
					next.ServeHTTP(w, r)
					return
				}
				id, ok, err := cfg.Sessions.ResolveSession(r.Context(), token)
				if err != nil {
					// The database is unreachable or misbehaving. Treat the
					// request as anonymous and let Require turn that into a
					// 401; answering 500 here would tell an unauthenticated
					// caller more about the deployment than it should.
					log.Error("resolve session", "error", err, "path", r.URL.Path)
					next.ServeHTTP(w, r)
					return
				}
				if !ok {
					next.ServeHTTP(w, r)
					return
				}
				next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
			})

		default:
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
}

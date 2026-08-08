package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/auth"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/service"
)

// --- who may do what --------------------------------------------------------

// rule grants a group of endpoints to a set of roles.
//
// The table is deliberately short and stated in one place. Scattering the same
// decision across thirty handlers is how a new endpoint ends up quietly open.
type rule struct {
	method string // empty matches any method
	prefix string
	roles  []string
}

// routeRules is checked in order; the first matching rule decides. An endpoint
// matching no rule is open to any authenticated user, which is right because
// every such endpoint is a read.
var routeRules = []rule{
	// Administering the system, including other people's accounts.
	{prefix: "/api/v1/admin/", roles: []string{auth.RoleAdmin}},

	// Master data and the plan are the planner's to maintain.
	{method: http.MethodPost, prefix: "/api/v1/master/", roles: []string{auth.RoleAdmin, auth.RolePlanner}},
	{method: http.MethodPut, prefix: "/api/v1/master/", roles: []string{auth.RoleAdmin, auth.RolePlanner}},
	{method: http.MethodPost, prefix: "/api/v1/planning/", roles: []string{auth.RoleAdmin, auth.RolePlanner}},

	// Stock is the warehouse's to move. Validating a movement is not moving
	// it, so it stays open to anyone who may read — that is how a planner
	// checks whether a receipt would fit.
	{method: http.MethodPost, prefix: "/api/v1/inventory/movements/validate", roles: nil},
	{method: http.MethodPost, prefix: "/api/v1/inventory/", roles: []string{auth.RoleAdmin, auth.RoleWarehouse}},
}

// publicPaths never require an identity: the health probe, and the two calls a
// browser must make before anyone has signed in.
var publicPaths = map[string]bool{
	"/api/v1/health":      true,
	"/api/v1/auth/config": true,
	"/api/v1/auth/login":  true,
	// me answers "nobody, yet" rather than refusing, so the browser can ask
	// one question on start instead of interpreting a 401.
	"/api/v1/auth/me": true,
	// Logging out when the session has already gone is not a failure.
	"/api/v1/auth/logout": true,
}

// rolesFor returns the roles a request needs, and whether any rule matched.
func rolesFor(method, path string) ([]string, bool) {
	for _, r := range routeRules {
		if r.method != "" && r.method != method {
			continue
		}
		if strings.HasPrefix(path, r.prefix) {
			return r.roles, true
		}
	}
	return nil, false
}

// guard rejects requests that carry no identity, or an identity without the
// role the endpoint needs.
//
// Under ModeNone it does nothing at all, so a deployment that has not chosen
// an authentication mechanism behaves exactly as it did before this existed.
func (a *API) guard(next http.Handler) http.Handler {
	if a.auth.Mode == auth.ModeNone {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Anything outside the API is the UI5 application itself. It has to
		// load before anyone can sign in, so it is never gated.
		if !strings.HasPrefix(path, "/api/") || publicPaths[path] {
			next.ServeHTTP(w, r)
			return
		}

		id, ok := auth.FromContext(r.Context())
		if !ok {
			a.fail(w, r, http.StatusUnauthorized, errors.New("sign in to use this"))
			return
		}

		// A password somebody else chose is good enough to look around with,
		// and to change itself. It is not good enough to post stock against
		// this person's name.
		if id.MustChangePassword && r.Method != http.MethodGet && r.Method != http.MethodHead &&
			path != "/api/v1/auth/change-password" && path != "/api/v1/auth/logout" {
			a.fail(w, r, http.StatusForbidden,
				errors.New("change your password before making changes: it was set by somebody else"))
			return
		}

		if roles, matched := rolesFor(r.Method, path); matched && !id.HasAnyRole(roles...) {
			a.fail(w, r, http.StatusForbidden, errors.New(
				"this needs the "+strings.Join(roles, " or ")+" role, and "+id.Subject+" does not have it"))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// --- handlers ---------------------------------------------------------------

// authConfig tells an unauthenticated browser what it is dealing with, and
// nothing more: whether to show a login screen, and what the roles are called.
type authConfig struct {
	Mode          string   `json:"mode"`
	LoginRequired bool     `json:"loginRequired"`
	CanChangeOwn  bool     `json:"canChangePassword"`
	Roles         []string `json:"roles"`

	// DemoAccounts is filled in only when this database actually holds the
	// fixture accounts, and then it carries their shared password too.
	//
	// Publishing a password from an endpoint that needs no authentication is
	// indefensible for a real account and unavoidable for a fixture: these
	// accounts exist to be signed in as, by whoever has the page open. The
	// list empties itself the moment `spp-seed-users -remove-demo` runs, and
	// the server warns on every start until it does.
	DemoAccounts []auth.DemoAccount `json:"demoAccounts,omitempty"`
	DemoPassword string             `json:"demoPassword,omitempty"`
}

func (a *API) authConfigHandler(w http.ResponseWriter, r *http.Request) {
	cfg := authConfig{
		Mode:          string(a.auth.Mode),
		LoginRequired: a.auth.LoginRequired(),
		CanChangeOwn:  a.auth.Mode == auth.ModeLocal,
		Roles:         auth.KnownRoles,
	}

	if a.auth.Mode == auth.ModeLocal {
		present, err := a.svc.DemoAccounts(r.Context())
		if err != nil {
			// The login screen still works without the shortcut, so a failure
			// here is logged rather than served as an error.
			a.log.Error("read demo accounts", "error", err)
		} else if len(present) > 0 {
			cfg.DemoAccounts = present
			cfg.DemoPassword = auth.DemoPassword
		}
	}

	a.writeJSON(w, http.StatusOK, cfg)
}

// login verifies the credentials and, on success, sets the session cookie.
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	if a.auth.Mode != auth.ModeLocal {
		a.fail(w, r, http.StatusNotFound,
			errors.New("this deployment does not sign people in; identity comes from "+string(a.auth.Mode)))
		return
	}

	client := ClientIP(r, a.trustedProxies)

	// The per-account hold in the service stops guessing at one password. This
	// stops one guess each against every account, which no single account's
	// counter would ever notice.
	if a.loginLimit != nil {
		if ok, retryAfter := a.loginLimit.Allow(client); !ok {
			seconds := int(retryAfter.Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			a.log.Warn("sign-in attempts throttled", "client", client, "retryAfterSeconds", seconds)
			a.fail(w, r, http.StatusTooManyRequests, fmt.Errorf(
				"too many failed sign-in attempts from this address. Try again in %d minute(s)",
				(seconds+59)/60))
			return
		}
	}

	req, err := decodeBody[service.LoginRequest](w, r)
	if err != nil {
		a.fail(w, r, http.StatusBadRequest, err)
		return
	}
	req.UserAgent = r.UserAgent()
	req.ClientIP = client

	res, err := a.svc.Login(r.Context(), req)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, service.ErrUnauthorized):
			status = http.StatusUnauthorized
			// Only a rejected credential counts. A malformed body or a
			// database failure is not somebody guessing.
			if a.loginLimit != nil {
				a.loginLimit.Record(client)
			}
		case errors.Is(err, service.ErrValidation):
			status = http.StatusBadRequest
		}
		a.fail(w, r, status, withoutSentinel(err))
		return
	}

	// Getting it right clears the slate, so two fumbled attempts before a
	// correct one are not carried for the rest of the window.
	if a.loginLimit != nil {
		a.loginLimit.Forget(client)
	}

	http.SetCookie(w, a.sessionCookie(res.Token, res.ExpiresAt))
	a.log.Info("signed in", "user", res.User.Username, "roles", res.User.Roles)
	a.writeJSON(w, http.StatusOK, res)
}

// logout ends the session and clears the cookie.
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	token := auth.TokenFromRequest(r, a.auth.SessionCookieName())
	if err := a.svc.Logout(r.Context(), token); err != nil {
		a.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	// An expired cookie with an empty value is how a cookie is deleted; the
	// attributes must match the ones it was set with or the browser keeps it.
	http.SetCookie(w, a.sessionCookie("", time.Unix(0, 0)))
	a.writeJSON(w, http.StatusOK, map[string]bool{"signedOut": true})
}

// me describes the caller.
type meResponse struct {
	Authenticated bool     `json:"authenticated"`
	Username      string   `json:"username,omitempty"`
	DisplayName   string   `json:"displayName,omitempty"`
	Roles         []string `json:"roles"`
	// Permissions are the answers the UI actually needs, worked out here so
	// the screens do not each re-implement the role table and drift from it.
	Permissions        permissions `json:"permissions"`
	MustChangePassword bool        `json:"mustChangePassword"`
	Mode               string      `json:"mode"`
	// LoginRequired lets the browser decide whether to show a login screen
	// from this one answer, rather than combining two.
	LoginRequired bool `json:"loginRequired"`
}

// permissions is what this caller may do, in the terms the screens use.
type permissions struct {
	ViewOnly       bool `json:"viewOnly"`
	EditMasterData bool `json:"editMasterData"`
	EditPlan       bool `json:"editPlan"`
	PostMovements  bool `json:"postMovements"`
	ManageUsers    bool `json:"manageUsers"`
	OverrideBlocks bool `json:"overrideCapacityBlocks"`
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		// Not an error: the browser asks this on start precisely to find out.
		a.writeJSON(w, http.StatusOK, meResponse{
			Roles:         []string{},
			Mode:          string(a.auth.Mode),
			LoginRequired: a.auth.LoginRequired(),
			Permissions:   a.permissionsFor(auth.Identity{}, false),
		})
		return
	}

	roles := id.Roles
	if roles == nil {
		roles = []string{}
	}
	a.writeJSON(w, http.StatusOK, meResponse{
		Authenticated:      true,
		Username:           id.Subject,
		DisplayName:        id.Name,
		Roles:              roles,
		Permissions:        a.permissionsFor(id, true),
		MustChangePassword: id.MustChangePassword,
		Mode:               string(a.auth.Mode),
		LoginRequired:      a.auth.LoginRequired(),
	})
}

// permissionsFor answers the role table for one identity.
func (a *API) permissionsFor(id auth.Identity, authenticated bool) permissions {
	// With no authentication configured, everybody may do everything — which
	// is exactly what ModeNone means, and the UI should show it as such
	// rather than hiding buttons that would in fact work.
	if a.auth.Mode == auth.ModeNone {
		return permissions{
			EditMasterData: true, EditPlan: true, PostMovements: true,
			ManageUsers: true, OverrideBlocks: true,
		}
	}
	if !authenticated {
		return permissions{ViewOnly: true}
	}

	p := permissions{
		EditMasterData: id.HasAnyRole(auth.RoleAdmin, auth.RolePlanner),
		EditPlan:       id.HasAnyRole(auth.RoleAdmin, auth.RolePlanner),
		PostMovements:  id.HasAnyRole(auth.RoleAdmin, auth.RoleWarehouse),
		ManageUsers:    id.HasRole(auth.RoleAdmin),
		OverrideBlocks: id.HasRole(a.overrideRole),
	}
	if id.MustChangePassword {
		p = permissions{}
	}
	p.ViewOnly = !p.EditMasterData && !p.EditPlan && !p.PostMovements && !p.ManageUsers
	return p
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		in, err := decodeBody[service.ChangePasswordRequest](w, r)
		if err != nil {
			return nil, err
		}
		if err := a.svc.ChangePassword(r.Context(), in); err != nil {
			return nil, err
		}
		// Every session was revoked, this one included, so the cookie is
		// stale. Clear it rather than leave the browser holding a dead token.
		http.SetCookie(w, a.sessionCookie("", time.Unix(0, 0)))
		return map[string]any{
			"changed": true,
			"message": "Password changed. Sign in again with the new one.",
		}, nil
	})
}

// --- user administration ----------------------------------------------------

func (a *API) users(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		return a.svc.ListUsers(r.Context(), r.URL.Query().Get("includeInactive") == "true")
	})
}

func (a *API) saveUser(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		in, err := decodeBody[service.UserInput](w, r)
		if err != nil {
			return nil, err
		}
		return a.svc.SaveUser(r.Context(), in)
	})
}

func (a *API) resetPassword(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		in, err := decodeBody[service.ResetPasswordRequest](w, r)
		if err != nil {
			return nil, err
		}
		return a.svc.ResetPassword(r.Context(), in)
	})
}

func (a *API) signOutUser(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		in, err := decodeBody[struct {
			UserID int64 `json:"userId"`
		}](w, r)
		if err != nil {
			return nil, err
		}
		n, err := a.svc.SignOutUser(r.Context(), in.UserID)
		if err != nil {
			return nil, err
		}
		return map[string]int64{"sessionsEnded": n}, nil
	})
}

// withoutSentinel strips the classifying error's own text from a message.
//
// The service wraps its errors in a sentinel so the HTTP layer can pick a
// status code, which leaves the sentinel's name on the front of the message:
// "not authenticated: the user name or password is not correct". That reads
// fine in a log and badly on a login form, which is the most-seen screen in
// the system and the one a person is staring at when something has gone wrong.
//
// Only the sign-in path uses this. Elsewhere the prefix is a useful hint about
// what kind of answer an API client just got.
func withoutSentinel(err error) error {
	for _, sentinel := range []error{service.ErrUnauthorized, service.ErrValidation} {
		prefix := sentinel.Error() + ": "
		if errors.Is(err, sentinel) && strings.HasPrefix(err.Error(), prefix) {
			return errors.New(strings.TrimPrefix(err.Error(), prefix))
		}
	}
	return err
}

// --- cookie -----------------------------------------------------------------

// sessionCookie builds the session cookie.
//
// HttpOnly keeps it out of reach of any script on the page, so a cross-site
// scripting bug cannot walk off with the session. SameSite=Lax means another
// site cannot cause the browser to send it on a form post, which is what would
// otherwise make every state-changing endpoint here forgeable.
func (a *API) sessionCookie(token string, expires time.Time) *http.Cookie {
	c := &http.Cookie{
		Name:     a.auth.SessionCookieName(),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.auth.CookieSecure,
		Expires:  expires,
	}
	if token == "" {
		c.MaxAge = -1
	} else if d := time.Until(expires); d > 0 {
		c.MaxAge = int(d.Seconds())
	}
	return c
}

// The address a request is attributed to is resolved by ClientIP, in
// clientip.go, which believes X-Forwarded-For only from a configured proxy.

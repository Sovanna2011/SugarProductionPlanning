package httpapi

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/auth"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/service"
)

// guarded runs one request through the guard and reports the status, plus
// whether the request reached the handler behind it.
func guarded(t *testing.T, mode auth.Mode, id *auth.Identity, method, path string) (int, bool) {
	t.Helper()

	api := &API{
		log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		auth:         auth.Config{Mode: mode},
		overrideRole: auth.RoleAdmin,
	}

	var reached bool
	handler := api.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(method, path, nil)
	if id != nil {
		req = req.WithContext(auth.WithIdentity(req.Context(), *id))
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, reached
}

func TestGuardIsInertWhenAuthenticationIsOff(t *testing.T) {
	// Turning the login system on must be a deliberate act. With the mode
	// left at none, every endpoint behaves exactly as it did before any of
	// this existed.
	for _, path := range []string{
		"/api/v1/dashboard/storage-capacity",
		"/api/v1/inventory/movements",
		"/api/v1/master/storage-locations",
		"/api/v1/admin/users",
	} {
		if code, reached := guarded(t, auth.ModeNone, nil, http.MethodPost, path); !reached {
			t.Fatalf("%s was blocked with authentication off (status %d)", path, code)
		}
	}
}

func TestGuardRefusesAnonymousCallers(t *testing.T) {
	code, reached := guarded(t, auth.ModeLocal, nil, http.MethodGet, "/api/v1/dashboard/storage-capacity")
	if code != http.StatusUnauthorized || reached {
		t.Fatalf("anonymous read: status %d reached %v, want 401 and not reached", code, reached)
	}
}

func TestGuardLetsTheLoginAndTheAppThrough(t *testing.T) {
	// Without these three, a browser could never reach the login screen: the
	// app would not load, and the call that submits the password would be
	// refused for want of the session it is trying to create.
	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodGet, "/api/v1/auth/config"},
		{http.MethodGet, "/api/v1/auth/me"},
		{http.MethodGet, "/api/v1/health"},
		{http.MethodGet, "/index.html"},
		{http.MethodGet, "/resources/sap-ui-core.js"},
	}
	for _, c := range cases {
		if code, reached := guarded(t, auth.ModeLocal, nil, c.method, c.path); !reached {
			t.Fatalf("%s %s was blocked before sign-in (status %d)", c.method, c.path, code)
		}
	}
}

func TestGuardEnforcesRoles(t *testing.T) {
	planner := &auth.Identity{Subject: "planner", Roles: []string{auth.RolePlanner}}
	warehouse := &auth.Identity{Subject: "warehouse", Roles: []string{auth.RoleWarehouse}}
	viewer := &auth.Identity{Subject: "viewer", Roles: []string{auth.RoleViewer}}
	admin := &auth.Identity{Subject: "admin", Roles: []string{auth.RoleAdmin}}

	cases := []struct {
		name    string
		id      *auth.Identity
		method  string
		path    string
		allowed bool
	}{
		{"a planner maintains master data", planner, http.MethodPost, "/api/v1/master/storage-locations", true},
		{"a planner maintains the plan", planner, http.MethodPost, "/api/v1/planning/storage", true},
		{"a planner does not move stock", planner, http.MethodPost, "/api/v1/inventory/movements", false},
		{"a warehouse hand moves stock", warehouse, http.MethodPost, "/api/v1/inventory/movements", true},
		{"a warehouse hand reserves stock", warehouse, http.MethodPost, "/api/v1/inventory/reservations", true},
		{"a warehouse hand does not edit capacities", warehouse, http.MethodPut, "/api/v1/master/threshold-levels", false},
		{"a viewer reads", viewer, http.MethodGet, "/api/v1/dashboard/storage-capacity", true},
		{"a viewer writes nothing", viewer, http.MethodPost, "/api/v1/inventory/movements", false},
		{"only an administrator sees the users", viewer, http.MethodGet, "/api/v1/admin/users", false},
		{"an administrator sees the users", admin, http.MethodGet, "/api/v1/admin/users", true},
		{"an administrator may do the rest too", admin, http.MethodPost, "/api/v1/inventory/movements", true},

		// Asking whether a receipt would fit is a read dressed as a POST, so
		// a planner checking a plan is not refused.
		{"anyone signed in may validate a movement", planner, http.MethodPost, "/api/v1/inventory/movements/validate", true},
		{"a viewer may validate a movement", viewer, http.MethodPost, "/api/v1/inventory/movements/validate", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, reached := guarded(t, auth.ModeLocal, c.id, c.method, c.path)
			if c.allowed && !reached {
				t.Fatalf("%s %s was refused with %d", c.method, c.path, code)
			}
			if !c.allowed {
				if reached {
					t.Fatalf("%s %s was allowed", c.method, c.path)
				}
				if code != http.StatusForbidden {
					t.Fatalf("status %d, want 403 — the caller is known, the answer is no", code)
				}
			}
		})
	}
}

func TestGuardHoldsBackAnIssuedPassword(t *testing.T) {
	// Someone signed in on a password an administrator chose may look around,
	// and may change that password. Nothing else, because until they do, the
	// audit trail cannot honestly say the action was theirs.
	id := &auth.Identity{
		Subject:            "planner",
		Roles:              []string{auth.RolePlanner},
		MustChangePassword: true,
	}

	if code, reached := guarded(t, auth.ModeLocal, id, http.MethodGet, "/api/v1/dashboard/storage-capacity"); !reached {
		t.Fatalf("reading was refused with %d", code)
	}
	if code, reached := guarded(t, auth.ModeLocal, id, http.MethodPost, "/api/v1/master/storage-locations"); reached {
		t.Fatalf("a write was allowed on an issued password (status %d)", code)
	}
	if code, reached := guarded(t, auth.ModeLocal, id, http.MethodPost, "/api/v1/auth/change-password"); !reached {
		t.Fatalf("changing the password was refused with %d, which leaves the account stuck", code)
	}
}

func TestPermissionsMatchTheRouteTable(t *testing.T) {
	// The UI hides what it may not do. If these answers drift from the guard's
	// the user sees a button that then fails, or no button for something they
	// are entitled to.
	api := &API{auth: auth.Config{Mode: auth.ModeLocal}, overrideRole: auth.RoleAdmin}

	viewer := api.permissionsFor(auth.Identity{Subject: "v", Roles: []string{auth.RoleViewer}}, true)
	if !viewer.ViewOnly || viewer.EditMasterData || viewer.PostMovements || viewer.ManageUsers {
		t.Fatalf("a viewer was granted more than reading: %+v", viewer)
	}
	if viewer.OverrideBlocks {
		t.Fatal("a viewer may force a blocked posting")
	}

	admin := api.permissionsFor(auth.Identity{Subject: "a", Roles: []string{auth.RoleAdmin}}, true)
	if !admin.EditMasterData || !admin.PostMovements || !admin.ManageUsers || !admin.OverrideBlocks || admin.ViewOnly {
		t.Fatalf("an administrator was refused something: %+v", admin)
	}

	// With authentication off the UI must not hide buttons that would work.
	open := (&API{auth: auth.Config{Mode: auth.ModeNone}}).permissionsFor(auth.Identity{}, false)
	if !open.EditMasterData || !open.PostMovements || !open.ManageUsers {
		t.Fatalf("with authentication off the UI would hide working buttons: %+v", open)
	}
}

func TestWithoutSentinel(t *testing.T) {
	// A login form is the most-seen screen in the system and the one somebody
	// is staring at when something has gone wrong. "not authenticated:" in
	// front of the explanation is a log line, not a sentence for a person.
	wrapped := fmt.Errorf("%w: the user name or password is not correct", service.ErrUnauthorized)
	if got := withoutSentinel(wrapped).Error(); got != "the user name or password is not correct" {
		t.Fatalf("got %q, want the message without the sentinel", got)
	}

	validation := fmt.Errorf("%w: enter a user name and password", service.ErrValidation)
	if got := withoutSentinel(validation).Error(); got != "enter a user name and password" {
		t.Fatalf("got %q", got)
	}

	// An error that is not one of the classifying sentinels is left alone.
	plain := errors.New("too many failed sign-in attempts from this address")
	if got := withoutSentinel(plain).Error(); got != plain.Error() {
		t.Fatalf("an unwrapped error was rewritten to %q", got)
	}

	// And one that wraps a sentinel without carrying its text keeps its own.
	odd := fmt.Errorf("something else entirely: %w", service.ErrUnauthorized)
	if got := withoutSentinel(odd).Error(); got != odd.Error() {
		t.Fatalf("got %q, want the message untouched when the prefix is not there", got)
	}
}

//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/auth"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/httpapi"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/service"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
)

// newTestAPI builds the real HTTP handler, so these tests exercise the same
// routing, middleware and guard the server does.
func newTestAPI(t *testing.T, svc *service.Service, cfg auth.Config) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httpapi.New(svc, log, "", cfg, auth.RoleAdmin).Routes()
}

// signIn posts credentials and returns the session token.
func signIn(t *testing.T, client *http.Client, base, username, password string) string {
	t.Helper()

	body := `{"username":"` + username + `","password":"` + password + `"}`
	res, err := client.Post(base+"/api/v1/auth/login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("sign in as %s: %v", username, err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("sign in as %s returned %d: %s", username, res.StatusCode, raw)
	}

	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Token == "" {
		t.Fatalf("sign in as %s returned no token", username)
	}

	// The browser path must work too: the response has to set the cookie, or
	// the UI5 app would be signed in only as far as this test is concerned.
	var cookie bool
	for _, c := range res.Cookies() {
		if c.Name == "spp_session" && c.Value != "" {
			cookie = true
			if !c.HttpOnly {
				t.Fatal("the session cookie is readable by scripts on the page")
			}
		}
	}
	if !cookie {
		t.Fatal("signing in did not set a session cookie")
	}
	return payload.Token
}

// status makes one request as the holder of a token and returns the status.
func status(t *testing.T, client *http.Client, method, url, token, body string) int {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode
}

// account creates a throwaway user and returns it, cleaning up afterwards so
// the tests can run repeatedly against the same database.
func account(t *testing.T, store *postgres.Store, ctx context.Context, username, password string, roles ...string) domain.User {
	t.Helper()

	// A previous run may have left it behind.
	if existing, err := store.GetUserByUsername(ctx, username); err == nil {
		if _, err := store.Pool().Exec(ctx, `DELETE FROM app_users WHERE id = $1`, existing.ID); err != nil {
			t.Fatalf("clear %s: %v", username, err)
		}
	}

	digest, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.InsertUser(ctx, domain.User{
		Username:    username,
		DisplayName: strings.ToUpper(username[:1]) + username[1:],
		Roles:       roles,
		Status:      domain.StatusActive,
		Remark:      "created by the integration tests",
	}, digest, "test")
	if err != nil {
		t.Fatalf("create %s: %v", username, err)
	}

	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = store.Pool().Exec(cleanup, `DELETE FROM app_users WHERE id = $1`, user.ID)
	})
	return user
}

func TestLoginIssuesAWorkingSession(t *testing.T) {
	store, ctx := open(t)
	svc := service.New(store)

	const password = "Integration-Test-2027"
	user := account(t, store, ctx, "itest-planner", password, auth.RolePlanner)

	res, err := svc.Login(ctx, service.LoginRequest{
		Username: "ITEST-PLANNER", // the name is folded, so casing must not matter
		Password: password,
		ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	if res.Token == "" {
		t.Fatal("no session token was issued")
	}
	if !res.ExpiresAt.After(time.Now()) {
		t.Fatalf("the session expired at %s, before it was issued", res.ExpiresAt)
	}

	// The token must resolve to the identity the rest of the system uses.
	id, ok, err := svc.ResolveSession(ctx, res.Token)
	if err != nil || !ok {
		t.Fatalf("the issued token did not resolve: ok=%v err=%v", ok, err)
	}
	if id.Subject != user.Username || !id.HasRole(auth.RolePlanner) {
		t.Fatalf("resolved to %+v", id)
	}

	// The token is stored hashed: the raw value must not appear in the table.
	var stored int
	if err := store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM user_sessions WHERE encode(token_hash, 'escape') = $1`,
		res.Token).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatal("the session token itself is in the database; only its hash should be")
	}

	// Logging out must take effect at once, not at expiry.
	if err := svc.Logout(ctx, res.Token); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := svc.ResolveSession(ctx, res.Token); ok || err != nil {
		t.Fatalf("the session survived logout: ok=%v err=%v", ok, err)
	}
}

func TestLoginRejectionsAreIndistinguishable(t *testing.T) {
	store, ctx := open(t)
	svc := service.New(store)

	const password = "Integration-Test-2027"
	account(t, store, ctx, "itest-viewer", password, auth.RoleViewer)

	_, wrongPassword := svc.Login(ctx, service.LoginRequest{Username: "itest-viewer", Password: "not-it-at-all"})
	_, noSuchUser := svc.Login(ctx, service.LoginRequest{Username: "itest-nobody", Password: password})

	if wrongPassword == nil || noSuchUser == nil {
		t.Fatal("a bad sign-in was accepted")
	}
	if !errors.Is(wrongPassword, service.ErrUnauthorized) || !errors.Is(noSuchUser, service.ErrUnauthorized) {
		t.Fatalf("wrong error kinds: %v / %v", wrongPassword, noSuchUser)
	}
	// Different wording would let an attacker enumerate the staff list without
	// ever signing in.
	if wrongPassword.Error() != noSuchUser.Error() {
		t.Fatalf("a wrong password says %q but an unknown user says %q",
			wrongPassword.Error(), noSuchUser.Error())
	}
}

func TestRepeatedFailuresLockTheAccountAndTheLockLifts(t *testing.T) {
	store, ctx := open(t)
	svc := service.New(store)

	const password = "Integration-Test-2027"
	user := account(t, store, ctx, "itest-locked", password, auth.RoleViewer)

	for i := 0; i < 5; i++ {
		if _, err := svc.Login(ctx, service.LoginRequest{Username: user.Username, Password: "wrong"}); err == nil {
			t.Fatal("a wrong password was accepted")
		}
	}

	// The right password is now refused too, and says why.
	_, err := svc.Login(ctx, service.LoginRequest{Username: user.Username, Password: password})
	if err == nil {
		t.Fatal("the account was not held after five wrong passwords")
	}
	if !strings.Contains(err.Error(), "too many failed attempts") {
		t.Fatalf("the hold does not explain itself: %v", err)
	}

	// The hold is a delay, not a permanent lock. Wind it back and the correct
	// password works again, without an administrator having to intervene.
	if _, err := store.Pool().Exec(ctx,
		`UPDATE app_users SET locked_until = now() - INTERVAL '1 minute' WHERE id = $1`, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, service.LoginRequest{Username: user.Username, Password: password}); err != nil {
		t.Fatalf("the account stayed locked after the hold expired: %v", err)
	}

	// A successful sign-in clears the count, so the next mistake starts over.
	refreshed, err := store.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.FailedAttempts != 0 || refreshed.LockedUntil != nil {
		t.Fatalf("the throttle survived a successful sign-in: %+v", refreshed)
	}
}

func TestDeactivatingAnAccountEndsItsSessions(t *testing.T) {
	store, ctx := open(t)
	svc := service.New(store)

	const password = "Integration-Test-2027"
	user := account(t, store, ctx, "itest-leaver", password, auth.RoleWarehouse)

	res, err := svc.Login(ctx, service.LoginRequest{Username: user.Username, Password: password})
	if err != nil {
		t.Fatal(err)
	}

	admin := auth.WithIdentity(ctx, auth.Identity{Subject: "itest-admin", Roles: []string{auth.RoleAdmin}})
	current, err := store.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveUser(admin, service.UserInput{
		ID:          current.ID,
		Username:    current.Username,
		DisplayName: current.DisplayName,
		Roles:       current.Roles,
		Status:      domain.StatusInactive,
		Version:     current.Version,
	}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	// "Deactivated" must mean now, not at the next expiry.
	if _, ok, err := svc.ResolveSession(ctx, res.Token); ok || err != nil {
		t.Fatalf("a deactivated account kept its session: ok=%v err=%v", ok, err)
	}
	if _, err := svc.Login(ctx, service.LoginRequest{Username: user.Username, Password: password}); err == nil {
		t.Fatal("a deactivated account signed back in")
	}
}

func TestChangingThePasswordRevokesEverySession(t *testing.T) {
	store, ctx := open(t)
	svc := service.New(store)

	const password = "Integration-Test-2027"
	user := account(t, store, ctx, "itest-changer", password, auth.RolePlanner)

	first, err := svc.Login(ctx, service.LoginRequest{Username: user.Username, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Login(ctx, service.LoginRequest{Username: user.Username, Password: password})
	if err != nil {
		t.Fatal(err)
	}

	signedIn := auth.WithIdentity(ctx, auth.Identity{Subject: user.Username, Roles: user.Roles})
	if err := svc.ChangePassword(signedIn, service.ChangePasswordRequest{
		CurrentPassword: password,
		NewPassword:     "Integration-Test-2028",
	}); err != nil {
		t.Fatalf("change password: %v", err)
	}

	// A password change that leaves the other browser signed in has not done
	// what the person doing it believes it has done.
	for name, token := range map[string]string{"this session": first.Token, "the other session": second.Token} {
		if _, ok, err := svc.ResolveSession(ctx, token); ok || err != nil {
			t.Fatalf("%s survived the password change: ok=%v err=%v", name, ok, err)
		}
	}

	if _, err := svc.Login(ctx, service.LoginRequest{Username: user.Username, Password: password}); err == nil {
		t.Fatal("the old password still works")
	}
	if _, err := svc.Login(ctx, service.LoginRequest{
		Username: user.Username, Password: "Integration-Test-2028",
	}); err != nil {
		t.Fatalf("the new password does not work: %v", err)
	}
}

func TestTheLastAdministratorCannotBeRemoved(t *testing.T) {
	store, ctx := open(t)
	svc := service.New(store)

	// Clear the field first: any other active administrator would make this
	// test pass for the wrong reason.
	existing, err := store.ListUsers(ctx, postgres.UserFilter{Role: auth.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range existing {
		if _, err := store.Pool().Exec(ctx,
			`UPDATE app_users SET status = 'INACTIVE' WHERE id = $1`, a.ID); err != nil {
			t.Fatal(err)
		}
		id := a.ID
		t.Cleanup(func() {
			restore, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = store.Pool().Exec(restore, `UPDATE app_users SET status = 'ACTIVE' WHERE id = $1`, id)
		})
	}

	only := account(t, store, ctx, "itest-only-admin", "Integration-Test-2027", auth.RoleAdmin)
	actor := auth.WithIdentity(ctx, auth.Identity{Subject: only.Username, Roles: []string{auth.RoleAdmin}})

	// Demoting the last administrator leaves a system nobody can administer.
	_, err = svc.SaveUser(actor, service.UserInput{
		ID: only.ID, Username: only.Username, DisplayName: only.DisplayName,
		Roles: []string{auth.RoleViewer}, Status: domain.StatusActive, Version: only.Version,
	})
	if err == nil {
		t.Fatal("the only administrator demoted themselves")
	}
	if !strings.Contains(err.Error(), "only active administrator") {
		t.Fatalf("the refusal does not explain itself: %v", err)
	}

	// Deactivating them is the same mistake by another route.
	if _, err := svc.SaveUser(actor, service.UserInput{
		ID: only.ID, Username: only.Username, DisplayName: only.DisplayName,
		Roles: []string{auth.RoleAdmin}, Status: domain.StatusInactive, Version: only.Version,
	}); err == nil {
		t.Fatal("the only administrator deactivated themselves")
	}
}

// TestTheApiRefusesTheRightThings drives the real HTTP stack, because the
// guard, the middleware and the service only add up to a login system when
// they are wired together.
func TestTheApiRefusesTheRightThings(t *testing.T) {
	store, ctx := open(t)

	const password = "Integration-Test-2027"
	account(t, store, ctx, "itest-api-viewer", password, auth.RoleViewer)
	account(t, store, ctx, "itest-api-store", password, auth.RoleWarehouse)

	svc := service.New(store, service.WithOverrideRole(auth.RoleAdmin))
	cfg := auth.Config{Mode: auth.ModeLocal, Sessions: svc, CookieName: "spp_session", SessionTTL: time.Hour}
	srv := httptest.NewServer(newTestAPI(t, svc, cfg))
	t.Cleanup(srv.Close)

	client := srv.Client()

	// Before signing in, a read is refused and the login endpoint is not.
	if code := status(t, client, http.MethodGet, srv.URL+"/api/v1/factories", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("an anonymous read returned %d, want 401", code)
	}
	if code := status(t, client, http.MethodGet, srv.URL+"/api/v1/auth/config", "", ""); code != http.StatusOK {
		t.Fatalf("auth/config returned %d before sign-in; the login screen could never load", code)
	}

	viewer := signIn(t, client, srv.URL, "itest-api-viewer", password)
	warehouse := signIn(t, client, srv.URL, "itest-api-store", password)

	if code := status(t, client, http.MethodGet, srv.URL+"/api/v1/factories", viewer, ""); code != http.StatusOK {
		t.Fatalf("a signed-in viewer could not read: %d", code)
	}

	movement := `{"factoryId":1,"movementDate":"2027-02-15","movementType":"WAREHOUSE_RECEIPT",
		"storageCode":"RAW-W01","productCode":"RAW-SUGAR","packagingCode":"PKG-BULK","weightQuantity":1}`

	if code := status(t, client, http.MethodPost, srv.URL+"/api/v1/inventory/movements", viewer, movement); code != http.StatusForbidden {
		t.Fatalf("a viewer posting stock returned %d, want 403", code)
	}
	// The warehouse role reaches the handler. Whether the posting itself is
	// accepted is the capacity engine's business, not the guard's, so any
	// answer other than 403 proves the point.
	if code := status(t, client, http.MethodPost, srv.URL+"/api/v1/inventory/movements", warehouse, movement); code == http.StatusForbidden {
		t.Fatal("the warehouse role was refused a posting")
	}
	if code := status(t, client, http.MethodGet, srv.URL+"/api/v1/admin/users", warehouse, ""); code != http.StatusForbidden {
		t.Fatalf("a warehouse hand listing the users returned %d, want 403", code)
	}

	// A token that has been signed out is a token like any other.
	if code := status(t, client, http.MethodPost, srv.URL+"/api/v1/auth/logout", viewer, ""); code != http.StatusOK {
		t.Fatalf("logout returned %d", code)
	}
	if code := status(t, client, http.MethodGet, srv.URL+"/api/v1/factories", viewer, ""); code != http.StatusUnauthorized {
		t.Fatalf("a read with a signed-out token returned %d, want 401", code)
	}
}

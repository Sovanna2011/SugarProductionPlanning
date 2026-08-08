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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/auth"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/httpapi"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/ratelimit"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/service"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
)

// newTestAPI builds the real HTTP handler, so these tests exercise the same
// routing, middleware and guard the server does.
func newTestAPI(t *testing.T, svc *service.Service, cfg auth.Config, opts httpapi.Options) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httpapi.New(svc, log, "", cfg, opts).Routes()
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
	}, digest)
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
	srv := httptest.NewServer(newTestAPI(t, svc, cfg, httpapi.Options{OverrideRole: auth.RoleAdmin}))
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

// TestDuplicateCodeIsAnAnswerNotAFailure pins a defect the role probe found:
// creating a record whose code is already taken reached the caller as a 500
// and a generic "internal error", which tells somebody who simply reused a
// code nothing about what to do next.
func TestDuplicateCodeIsAnAnswerNotAFailure(t *testing.T) {
	store, ctx := open(t)
	svc := service.New(store)

	in := service.PackagingTypeInput{
		Code:         "PKG-DUPTEST",
		Description:  "Integration test packaging",
		NetWeight:    25,
		NetWeightUOM: "KG",
	}

	if _, err := svc.SavePackagingType(ctx, in); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = store.Pool().Exec(cleanup, `DELETE FROM packaging_types WHERE code = $1`, in.Code)
	})

	// The same call again: no version, so it means "create", and the code is
	// taken. That is the caller's mistake, and the answer has to say so.
	_, err := svc.SavePackagingType(ctx, in)
	if err == nil {
		t.Fatal("creating a second packaging type with the same code was accepted")
	}
	if !errors.Is(err, service.ErrValidation) {
		t.Fatalf("a duplicate code came back as %v, which the API answers with 500 and "+
			"an unhelpful \"internal error\"", err)
	}
	if !strings.Contains(err.Error(), "version") {
		t.Fatalf("the message does not say how to update the existing record instead: %v", err)
	}
}

// TestSprayingAcrossAccountsIsThrottled covers the gap the per-account hold
// leaves open: one guess each against every account. No single account ever
// reaches its own limit, so without a per-client limit nothing notices.
func TestSprayingAcrossAccountsIsThrottled(t *testing.T) {
	store, ctx := open(t)

	const password = "Integration-Test-2027"
	const attempts = 4

	// Enough accounts that each is guessed at only once — well under the five
	// failures that would hold an account on its own.
	for i := 0; i < attempts; i++ {
		account(t, store, ctx, "itest-spray-"+strconv.Itoa(i), password, auth.RoleViewer)
	}

	svc := service.New(store)
	cfg := auth.Config{Mode: auth.ModeLocal, Sessions: svc, CookieName: "spp_session", SessionTTL: time.Hour}
	limit := ratelimit.NewFailures(attempts-1, 15*time.Minute, 100)

	srv := httptest.NewServer(newTestAPI(t, svc, cfg, httpapi.Options{
		OverrideRole: auth.RoleAdmin,
		LoginLimit:   limit,
	}))
	t.Cleanup(srv.Close)

	attempt := func(username, password string) int {
		body := `{"username":"` + username + `","password":"` + password + `"}`
		res, err := srv.Client().Post(srv.URL+"/api/v1/auth/login", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}

	var refused bool
	for i := 0; i < attempts; i++ {
		code := attempt("itest-spray-"+strconv.Itoa(i), "one-guess-each")
		switch code {
		case http.StatusUnauthorized:
			// Still being answered normally.
		case http.StatusTooManyRequests:
			refused = true
		default:
			t.Fatalf("attempt %d returned %d, want 401 or 429", i+1, code)
		}
	}
	if !refused {
		t.Fatalf("%d guesses spread across %d accounts were all answered; "+
			"the per-account hold cannot see this and nothing else did either", attempts, attempts)
	}

	// The correct password is refused too while the hold stands — the limit is
	// on the address, not on any one account.
	if code := attempt("itest-spray-0", password); code != http.StatusTooManyRequests {
		t.Fatalf("a correct password from the throttled address returned %d, want 429", code)
	}

	// And it is a hold, not a lockout: once the window closes, signing in works.
	limit.Forget("127.0.0.1")
	if code := attempt("itest-spray-0", password); code != http.StatusOK {
		t.Fatalf("after the hold lifted, a correct password returned %d, want 200", code)
	}
}

// TestSuccessDoesNotConsumeTheAllowance is the reason only failures are
// counted: a shift change where everybody signs in at once, from one address
// if they are behind a proxy, must not exhaust the limit.
func TestSuccessDoesNotConsumeTheAllowance(t *testing.T) {
	store, ctx := open(t)

	const password = "Integration-Test-2027"
	account(t, store, ctx, "itest-shift", password, auth.RoleViewer)

	svc := service.New(store)
	cfg := auth.Config{Mode: auth.ModeLocal, Sessions: svc, CookieName: "spp_session", SessionTTL: time.Hour}

	srv := httptest.NewServer(newTestAPI(t, svc, cfg, httpapi.Options{
		LoginLimit: ratelimit.NewFailures(2, 15*time.Minute, 100),
	}))
	t.Cleanup(srv.Close)

	for i := 0; i < 10; i++ {
		body := `{"username":"itest-shift","password":"` + password + `"}`
		res, err := srv.Client().Post(srv.URL+"/api/v1/auth/login", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		code := res.StatusCode
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()

		if code != http.StatusOK {
			t.Fatalf("sign-in %d returned %d; successful sign-ins are consuming the failure allowance", i+1, code)
		}
	}
}

// TestUsingASessionKeepsItAlive covers the behaviour a fixed expiry gets
// wrong: somebody working a long shift signed out mid-task, for no reason
// connected to anything they did.
func TestUsingASessionKeepsItAlive(t *testing.T) {
	store, ctx := open(t)

	const password = "Integration-Test-2027"
	user := account(t, store, ctx, "itest-shift-worker", password, auth.RoleWarehouse)

	// A short idle window so the test does not have to wait twelve hours.
	svc := service.New(store,
		service.WithSessionTTL(time.Hour),
		service.WithSessionMaxLifetime(24*time.Hour))

	res, err := svc.Login(ctx, service.LoginRequest{Username: user.Username, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	// Wind the expiry back so the session is inside the last half of its
	// window, which is when using it should push it out.
	if _, err := store.Pool().Exec(ctx,
		`UPDATE user_sessions SET expires_at = now() + INTERVAL '10 minutes'
		 WHERE user_id = $1`, user.ID); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := svc.ResolveSession(ctx, res.Token); !ok || err != nil {
		t.Fatalf("the session did not resolve: ok=%v err=%v", ok, err)
	}

	var extended time.Time
	if err := store.Pool().QueryRow(ctx,
		`SELECT expires_at FROM user_sessions WHERE user_id = $1`, user.ID).Scan(&extended); err != nil {
		t.Fatal(err)
	}
	if !extended.After(time.Now().Add(50 * time.Minute)) {
		t.Fatalf("expiry is %s, barely moved; using the session did not keep it alive", extended)
	}
}

func TestASessionCannotBeKeptAliveForEver(t *testing.T) {
	store, ctx := open(t)

	const password = "Integration-Test-2027"
	user := account(t, store, ctx, "itest-forever", password, auth.RoleViewer)

	// An idle window longer than the absolute lifetime, so every extension is
	// clamped by the cap rather than by the window.
	svc := service.New(store,
		service.WithSessionTTL(time.Hour),
		service.WithSessionMaxLifetime(2*time.Hour))

	res, err := svc.Login(ctx, service.LoginRequest{Username: user.Username, Password: password})
	if err != nil {
		t.Fatal(err)
	}

	// Pretend the session was issued three hours ago — past its cap — while
	// still inside its idle window, which is exactly the state continuous use
	// would produce.
	if _, err := store.Pool().Exec(ctx, `
		UPDATE user_sessions
		SET issued_at  = now() - INTERVAL '3 hours',
		    expires_at = now() + INTERVAL '2 minutes'
		WHERE user_id = $1`, user.ID); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := svc.ResolveSession(ctx, res.Token); !ok || err != nil {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}

	var expires time.Time
	if err := store.Pool().QueryRow(ctx,
		`SELECT expires_at FROM user_sessions WHERE user_id = $1`, user.ID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if expires.After(time.Now().Add(10 * time.Minute)) {
		t.Fatalf("expiry was pushed to %s, past the absolute lifetime; a session in "+
			"continuous use would never end", expires)
	}
}

func TestExtendingNeverShortensASession(t *testing.T) {
	store, ctx := open(t)

	const password = "Integration-Test-2027"
	user := account(t, store, ctx, "itest-nonshorten", password, auth.RoleViewer)

	svc := service.New(store, service.WithSessionTTL(time.Hour), service.WithSessionMaxLifetime(24*time.Hour))
	res, err := svc.Login(ctx, service.LoginRequest{Username: user.Username, Password: password})
	if err != nil {
		t.Fatal(err)
	}

	// A session with far longer left than the idle window would grant. Using
	// it must not pull the expiry back to now+window.
	if _, err := store.Pool().Exec(ctx,
		`UPDATE user_sessions SET expires_at = now() + INTERVAL '20 hours' WHERE user_id = $1`,
		user.ID); err != nil {
		t.Fatal(err)
	}

	if _, ok, _ := svc.ResolveSession(ctx, res.Token); !ok {
		t.Fatal("the session did not resolve")
	}

	var expires time.Time
	if err := store.Pool().QueryRow(ctx,
		`SELECT expires_at FROM user_sessions WHERE user_id = $1`, user.ID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if expires.Before(time.Now().Add(19 * time.Hour)) {
		t.Fatalf("expiry was pulled back to %s; using a session shortened it", expires)
	}
}

package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/auth"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
)

// ErrUnauthorized means the caller has not established who they are, or the
// credentials offered were not accepted.
//
// It is separate from ErrForbidden: one says "I do not know you", the other
// says "I know you and the answer is no". The first is worth a login screen,
// the second is not.
var ErrUnauthorized = errors.New("not authenticated")

// Sign-in throttling.
const (
	// maxLoginAttempts is how many wrong passwords in a row are tolerated.
	maxLoginAttempts = 5
	// loginLockFor is how long the account is held after that. It is a delay,
	// not a lock: guessing becomes hopeless while a person who mistyped their
	// password is not shut out until an administrator rescues them.
	loginLockFor = 15 * time.Minute
	// defaultSessionTTL is how long a session survives without being used.
	defaultSessionTTL = 12 * time.Hour
	// defaultSessionMaxLifetime caps how long a session can be kept alive by
	// use, however continuous. Without it a session extended on every request
	// would never end, and "sign out everywhere" would be the only way to
	// retire one.
	defaultSessionMaxLifetime = 7 * 24 * time.Hour
)

// sessionTTL is how long a session survives without being used.
func (s *Service) sessionTTL() time.Duration {
	if s.authSessionTTL > 0 {
		return s.authSessionTTL
	}
	return defaultSessionTTL
}

// sessionMaxLifetime caps how long use can keep a session alive.
func (s *Service) sessionMaxLifetime() time.Duration {
	if s.authSessionMaxLifetime > 0 {
		return s.authSessionMaxLifetime
	}
	return defaultSessionMaxLifetime
}

// WithSessionTTL sets how long a session survives without being used.
func WithSessionTTL(d time.Duration) Option {
	return func(s *Service) { s.authSessionTTL = d }
}

// WithSessionMaxLifetime caps how long use can keep a session alive.
func WithSessionMaxLifetime(d time.Duration) Option {
	return func(s *Service) { s.authSessionMaxLifetime = d }
}

// LoginRequest is a sign-in attempt.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`

	// Recorded against the session. The handler fills these in from the
	// request; a caller cannot set them through the JSON body.
	UserAgent string `json:"-"`
	ClientIP  string `json:"-"`
}

// LoginResult is what a successful sign-in returns.
type LoginResult struct {
	// Token is the session token. It is what the browser stores in a cookie
	// and what an API client sends as a bearer token.
	Token string `json:"token"`
	// ExpiresAt is when the session stops working if it is not used again.
	// Using it pushes this out; see ResolveSession.
	ExpiresAt time.Time `json:"expiresAt"`
	// User is the account that signed in.
	User domain.User `json:"user"`
	// MustChangePassword repeats the flag on the user for the login screen,
	// which acts on it before anything else has loaded.
	MustChangePassword bool `json:"mustChangePassword"`
}

// Login verifies a name and password and issues a session.
//
// Every rejection returns the same sentence. An attacker who can tell "no such
// user" from "wrong password" from "that account is deactivated" can map the
// staff list without ever signing in; the person who genuinely mistyped
// something is no worse off for the vagueness, because the fix is the same.
func (s *Service) Login(ctx context.Context, req LoginRequest) (LoginResult, error) {
	username := strings.ToLower(strings.TrimSpace(req.Username))
	if username == "" || req.Password == "" {
		return LoginResult{}, fmt.Errorf("%w: enter a user name and password", ErrValidation)
	}

	const rejected = "the user name or password is not correct"

	user, err := s.store.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Spend the time anyway, so a missing account is not visibly
			// faster to reject than a real one.
			auth.VerifyPassword("", req.Password)
			return LoginResult{}, fmt.Errorf("%w: %s", ErrUnauthorized, rejected)
		}
		return LoginResult{}, err
	}

	if user.Locked(time.Now()) {
		return LoginResult{}, fmt.Errorf("%w: too many failed attempts. Try again after %s",
			ErrUnauthorized, user.LockedUntil.Format("15:04"))
	}

	digest, err := s.store.PasswordDigest(ctx, user.ID)
	if err != nil {
		return LoginResult{}, err
	}

	if user.Status != domain.StatusActive || !auth.VerifyPassword(digest, req.Password) {
		// Count the attempt even for a deactivated account: otherwise the
		// throttle can be sidestepped by guessing against one.
		if _, ferr := s.store.RecordLoginFailure(ctx, user.ID, maxLoginAttempts, loginLockFor); ferr != nil {
			return LoginResult{}, ferr
		}
		return LoginResult{}, fmt.Errorf("%w: %s", ErrUnauthorized, rejected)
	}

	token, hash, err := auth.NewSessionToken()
	if err != nil {
		return LoginResult{}, err
	}
	expires := time.Now().Add(s.sessionTTL())
	if _, err := s.store.CreateSession(ctx, user.ID, hash, expires, req.UserAgent, req.ClientIP); err != nil {
		return LoginResult{}, err
	}
	if err := s.store.RecordLoginSuccess(ctx, user.ID); err != nil {
		return LoginResult{}, err
	}

	now := time.Now()
	user.LastLoginAt = &now
	user.FailedAttempts = 0
	user.LockedUntil = nil

	return LoginResult{
		Token:              token,
		ExpiresAt:          expires,
		User:               user,
		MustChangePassword: user.MustChangePassword,
	}, nil
}

// Logout ends the session the token belongs to. Logging out twice, or with a
// token that was never valid, is not an error.
func (s *Service) Logout(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	return s.store.RevokeSession(ctx, auth.HashToken(token))
}

// ResolveSession implements auth.SessionResolver, turning a session token into
// the identity the middleware attaches to the request.
//
// Using a session keeps it alive. A fixed expiry measured from sign-in signs
// somebody out in the middle of a shift, halfway through posting a receipt,
// for no reason connected to anything they did — so the window is an idle one,
// pushed out while the session is in use and bounded by an absolute lifetime
// so it cannot be kept alive for ever.
func (s *Service) ResolveSession(ctx context.Context, token string) (auth.Identity, bool, error) {
	if strings.TrimSpace(token) == "" {
		return auth.Identity{}, false, nil
	}
	user, session, ok, err := s.store.SessionUser(ctx, auth.HashToken(token))
	if err != nil || !ok {
		return auth.Identity{}, false, err
	}

	s.extendSession(ctx, session)

	return auth.Identity{
		UserID:             user.ID,
		Subject:            user.Username,
		Name:               user.DisplayName,
		Roles:              user.Roles,
		MustChangePassword: user.MustChangePassword,
	}, true, nil
}

// extendSession pushes an in-use session's expiry out, at most once per half
// window so this costs one write per session per several hours rather than one
// per request.
//
// A failure here is logged nowhere and returned nowhere: the request is
// already authenticated, and refusing it because the expiry could not be
// refreshed would turn a housekeeping problem into an outage. The worst case
// is that the session expires on time.
func (s *Service) extendSession(ctx context.Context, session domain.UserSession) {
	idle := s.sessionTTL()
	wanted := time.Now().Add(idle)

	// Never past the absolute lifetime, however continuously it is used.
	if limit := session.IssuedAt.Add(s.sessionMaxLifetime()); wanted.After(limit) {
		wanted = limit
	}
	// Only when it buys more than half a window, so this is not a write per
	// request.
	if wanted.Sub(session.ExpiresAt) < idle/2 {
		return
	}
	_ = s.store.ExtendSession(ctx, session.ID, wanted)
}

// CurrentUser returns the signed-in account.
func (s *Service) CurrentUser(ctx context.Context) (domain.User, error) {
	id, ok := auth.FromContext(ctx)
	if !ok {
		return domain.User{}, fmt.Errorf("%w: sign in first", ErrUnauthorized)
	}
	user, err := s.store.GetUserByUsername(ctx, id.Subject)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Proxy mode authenticates people who have no row here, and that
			// is legitimate: report what the proxy said.
			return domain.User{
				Username:    id.Subject,
				DisplayName: id.Name,
				Roles:       id.Roles,
				Status:      domain.StatusActive,
				CanSignIn:   true,
			}, nil
		}
		return domain.User{}, err
	}
	return user, nil
}

// ChangePasswordRequest changes one's own password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// ChangePassword replaces the signed-in user's password.
//
// The current password is required even though the session already proves who
// this is: it is what stops an unattended browser being used to take the
// account over permanently.
func (s *Service) ChangePassword(ctx context.Context, req ChangePasswordRequest) error {
	id, ok := auth.FromContext(ctx)
	if !ok {
		return fmt.Errorf("%w: sign in first", ErrUnauthorized)
	}

	user, err := s.store.GetUserByUsername(ctx, id.Subject)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: this account is managed outside the application, "+
				"so its password cannot be changed here", ErrForbidden)
		}
		return err
	}

	digest, err := s.store.PasswordDigest(ctx, user.ID)
	if err != nil {
		return err
	}
	if !auth.VerifyPassword(digest, req.CurrentPassword) {
		return fmt.Errorf("%w: the current password is not correct", ErrUnauthorized)
	}
	if req.NewPassword == req.CurrentPassword {
		return fmt.Errorf("%w: the new password must differ from the current one", ErrValidation)
	}
	if err := auth.CheckPasswordPolicy(user.Username, req.NewPassword); err != nil {
		return fmt.Errorf("%w: %s", ErrValidation, strings.TrimPrefix(err.Error(), "password rejected: "))
	}

	newDigest, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		return err
	}
	// SetPassword revokes every session, including this one, so the user signs
	// in again with the new password. That is the honest behaviour: it is also
	// what makes a password change effective against somebody who already has
	// a session.
	return s.store.SetPassword(ctx, user.ID, newDigest, false)
}

// --- user administration ----------------------------------------------------

// UserInput creates or updates an account.
type UserInput struct {
	// ID selects the account to update; zero creates one.
	ID          int64    `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Email       string   `json:"email"`
	Roles       []string `json:"roles"`
	Status      string   `json:"status"`
	Remark      string   `json:"remark"`
	// Password sets the initial password on create. Leave it empty to have
	// one generated and returned once.
	Password string `json:"password,omitempty"`
	// Version is required on update (optimistic locking).
	Version int `json:"version"`
}

// Validate checks what can be checked without the database.
func (in *UserInput) Validate() error {
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Email = strings.TrimSpace(in.Email)
	in.Remark = strings.TrimSpace(in.Remark)

	if in.Status == "" {
		in.Status = domain.StatusActive
	}
	if in.Status != domain.StatusActive && in.Status != domain.StatusInactive {
		return fmt.Errorf("%w: status must be ACTIVE or INACTIVE", ErrValidation)
	}

	if in.ID == 0 {
		if l := len(in.Username); l < 3 || l > 64 {
			return fmt.Errorf("%w: the user name must be between 3 and 64 characters", ErrValidation)
		}
		if strings.ContainsAny(in.Username, " \t\r\n") {
			return fmt.Errorf("%w: the user name must not contain spaces", ErrValidation)
		}
	}
	if in.DisplayName == "" {
		return fmt.Errorf("%w: a display name is required, so the audit trail names a person", ErrValidation)
	}
	if in.Email != "" && !strings.Contains(in.Email, "@") {
		return fmt.Errorf("%w: %q is not an email address", ErrValidation, in.Email)
	}

	// Normalise and check the roles. An unknown role is refused rather than
	// stored and ignored: a role that grants nothing but looks like it does is
	// worse than no role at all.
	seen := map[string]bool{}
	roles := make([]string, 0, len(in.Roles))
	for _, r := range in.Roles {
		r = strings.ToUpper(strings.TrimSpace(r))
		if r == "" || seen[r] {
			continue
		}
		if !auth.IsKnownRole(r) {
			return fmt.Errorf("%w: %q is not a role this system knows. Use one of %s",
				ErrValidation, r, strings.Join(auth.KnownRoles, ", "))
		}
		seen[r] = true
		roles = append(roles, r)
	}
	if len(roles) == 0 {
		return fmt.Errorf("%w: give the account at least one role (%s)",
			ErrValidation, strings.Join(auth.KnownRoles, ", "))
	}
	sort.Strings(roles)
	in.Roles = roles

	if in.Password != "" {
		if err := auth.CheckPasswordPolicy(in.Username, in.Password); err != nil {
			return fmt.Errorf("%w: %s", ErrValidation, strings.TrimPrefix(err.Error(), "password rejected: "))
		}
	}
	return nil
}

// UserSaveResult reports what a user write did.
type UserSaveResult struct {
	User    domain.User `json:"user"`
	Created bool        `json:"created"`
	// InitialPassword is set only when one was generated, and is the only
	// time it can be read. It is not stored anywhere in this form.
	InitialPassword string    `json:"initialPassword,omitempty"`
	Warnings        []Warning `json:"warnings,omitempty"`
}

// ListUsers returns the accounts.
func (s *Service) ListUsers(ctx context.Context, includeInactive bool) ([]domain.User, error) {
	return s.store.ListUsers(ctx, postgres.UserFilter{IncludeInactive: includeInactive})
}

// DemoAccounts returns the fixture accounts this database actually holds, in
// the order the login screen should offer them.
//
// It returns the ones that exist rather than the ones that could exist, so a
// login screen never offers an account that would then refuse the password.
func (s *Service) DemoAccounts(ctx context.Context) ([]auth.DemoAccount, error) {
	present, err := s.store.ListUsers(ctx, postgres.UserFilter{DemoOnly: true})
	if err != nil {
		return nil, err
	}
	if len(present) == 0 {
		return nil, nil
	}

	byName := make(map[string]domain.User, len(present))
	for _, u := range present {
		byName[u.Username] = u
	}

	out := make([]auth.DemoAccount, 0, len(present))
	for _, fixture := range auth.DemoAccounts {
		u, ok := byName[fixture.Username]
		if !ok {
			continue
		}
		// The roles come from the database, not the fixture: an administrator
		// may have changed them, and the screen should say what is true.
		out = append(out, auth.DemoAccount{
			Username: u.Username,
			Name:     u.DisplayName,
			Roles:    u.Roles,
			Remark:   fixture.Remark,
		})
	}
	return out, nil
}

// SaveUser creates or updates an account.
func (s *Service) SaveUser(ctx context.Context, in UserInput) (UserSaveResult, error) {
	if err := in.Validate(); err != nil {
		return UserSaveResult{}, err
	}
	actor := auth.Actor(ctx, "")

	if in.ID == 0 {
		return s.createUser(ctx, in, actor)
	}

	existing, err := s.store.GetUser(ctx, in.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserSaveResult{}, fmt.Errorf("%w: no user with id %d", ErrNotFound, in.ID)
		}
		return UserSaveResult{}, err
	}
	if in.Version == 0 {
		return UserSaveResult{}, fmt.Errorf("%w: send the version you read, so a concurrent change is not overwritten", ErrValidation)
	}

	// Nobody may leave the system with no way in. Both halves of that are
	// checked here rather than trusted to the caller's screen.
	losingAdmin := hasRole(existing.Roles, auth.RoleAdmin) &&
		(!hasRole(in.Roles, auth.RoleAdmin) || in.Status != domain.StatusActive)
	if losingAdmin {
		others, err := s.otherActiveAdmins(ctx, existing.ID)
		if err != nil {
			return UserSaveResult{}, err
		}
		if others == 0 {
			return UserSaveResult{}, fmt.Errorf(
				"%w: %s is the only active administrator. Give somebody else the %s role first",
				ErrValidation, existing.Username, auth.RoleAdmin)
		}
	}

	updated, err := s.store.UpdateUser(ctx, domain.User{
		ID:          existing.ID,
		DisplayName: in.DisplayName,
		Email:       in.Email,
		Roles:       in.Roles,
		Status:      in.Status,
		Remark:      in.Remark,
		Version:     in.Version,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserSaveResult{}, fmt.Errorf(
				"%w: %s was changed by somebody else. Reload and try again", ErrConflict, existing.Username)
		}
		return UserSaveResult{}, err
	}

	var warnings []Warning
	// A deactivated account keeps working until its sessions end, which is not
	// what "deactivate" means to the person clicking it.
	if in.Status == domain.StatusInactive && existing.Status == domain.StatusActive {
		n, err := s.store.RevokeUserSessions(ctx, existing.ID)
		if err != nil {
			return UserSaveResult{}, err
		}
		if n > 0 {
			warnings = append(warnings, Warning{
				Field:   "status",
				Message: fmt.Sprintf("%s was signed out of %d session(s).", existing.Username, n),
			})
		}
	}

	return UserSaveResult{User: updated, Warnings: warnings}, nil
}

func (s *Service) createUser(ctx context.Context, in UserInput, actor string) (UserSaveResult, error) {
	if _, err := s.store.GetUserByUsername(ctx, in.Username); err == nil {
		return UserSaveResult{}, fmt.Errorf("%w: the user name %q is taken", ErrValidation, in.Username)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return UserSaveResult{}, err
	}

	// An administrator setting somebody else's password knows it, so the
	// account is flagged to change it at first sign-in either way.
	password, generated := in.Password, false
	if password == "" {
		var err error
		if password, err = auth.GeneratePassword(); err != nil {
			return UserSaveResult{}, err
		}
		generated = true
	}
	digest, err := auth.HashPassword(password)
	if err != nil {
		return UserSaveResult{}, err
	}

	created, err := s.store.InsertUser(ctx, domain.User{
		Username:           in.Username,
		DisplayName:        in.DisplayName,
		Email:              in.Email,
		Roles:              in.Roles,
		Status:             in.Status,
		MustChangePassword: true,
		Remark:             in.Remark,
	}, digest)
	if err != nil {
		return UserSaveResult{}, err
	}

	result := UserSaveResult{User: created, Created: true}
	if generated {
		result.InitialPassword = password
	}
	return result, nil
}

// ResetPasswordRequest is an administrator setting somebody else's password.
type ResetPasswordRequest struct {
	UserID int64 `json:"userId"`
	// Password is optional; leave it empty to have one generated.
	Password string `json:"password,omitempty"`
}

// ResetPassword issues a new password for an account and signs it out
// everywhere.
//
// The new password is flagged as needing a change, because a password two
// people know is not a password.
func (s *Service) ResetPassword(ctx context.Context, req ResetPasswordRequest) (UserSaveResult, error) {
	user, err := s.store.GetUser(ctx, req.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserSaveResult{}, fmt.Errorf("%w: no user with id %d", ErrNotFound, req.UserID)
		}
		return UserSaveResult{}, err
	}

	password, generated := req.Password, false
	if password == "" {
		if password, err = auth.GeneratePassword(); err != nil {
			return UserSaveResult{}, err
		}
		generated = true
	}
	if err := auth.CheckPasswordPolicy(user.Username, password); err != nil {
		return UserSaveResult{}, fmt.Errorf("%w: %s", ErrValidation,
			strings.TrimPrefix(err.Error(), "password rejected: "))
	}

	digest, err := auth.HashPassword(password)
	if err != nil {
		return UserSaveResult{}, err
	}
	if err := s.store.SetPassword(ctx, user.ID, digest, true); err != nil {
		return UserSaveResult{}, err
	}

	refreshed, err := s.store.GetUser(ctx, user.ID)
	if err != nil {
		return UserSaveResult{}, err
	}
	result := UserSaveResult{User: refreshed}
	if generated {
		result.InitialPassword = password
	}
	return result, nil
}

// SignOutUser ends every session of an account.
func (s *Service) SignOutUser(ctx context.Context, userID int64) (int64, error) {
	if _, err := s.store.GetUser(ctx, userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("%w: no user with id %d", ErrNotFound, userID)
		}
		return 0, err
	}
	return s.store.RevokeUserSessions(ctx, userID)
}

// otherActiveAdmins counts the active administrators other than the given one.
func (s *Service) otherActiveAdmins(ctx context.Context, exceptID int64) (int, error) {
	admins, err := s.store.ListUsers(ctx, postgres.UserFilter{Role: auth.RoleAdmin})
	if err != nil {
		return 0, err
	}
	var n int
	for _, a := range admins {
		if a.ID != exceptID && a.Status == domain.StatusActive && a.CanSignIn {
			n++
		}
	}
	return n, nil
}

func hasRole(roles []string, want string) bool {
	for _, r := range roles {
		if strings.EqualFold(r, want) {
			return true
		}
	}
	return false
}

package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
)

// userColumns is the projection every user read shares. The password digest is
// never among them; it is selected explicitly, by name, in the one query that
// needs it.
const userColumns = `
	u.id, u.username, u.display_name, u.email, u.roles, u.status,
	u.must_change_password,
	(u.password_hash IS NOT NULL AND u.password_hash <> '') AS can_sign_in,
	u.is_demo,
	u.failed_attempts, u.locked_until, u.last_login_at, u.password_changed_at,
	u.remark, u.created_at, u.version`

// userColumnsBare is the same projection without the alias, for the RETURNING
// clause of an INSERT, which has no table to qualify.
var userColumnsBare = strings.ReplaceAll(userColumns, "u.", "")

func scanUser(r pgx.Rows) (domain.User, error) {
	var v domain.User
	err := r.Scan(&v.ID, &v.Username, &v.DisplayName, &v.Email, &v.Roles, &v.Status,
		&v.MustChangePassword, &v.CanSignIn, &v.IsDemo,
		&v.FailedAttempts, &v.LockedUntil, &v.LastLoginAt, &v.PasswordChangedAt,
		&v.Remark, &v.CreatedAt, &v.Version)
	if v.Roles == nil {
		v.Roles = []string{}
	}
	return v, err
}

// UserFilter narrows a user query.
type UserFilter struct {
	Username        string
	Role            string
	IncludeInactive bool
}

// ListUsers returns accounts matching the filter, in name order.
func (s *Store) ListUsers(ctx context.Context, f UserFilter) ([]domain.User, error) {
	var w filter
	if f.Username != "" {
		w.add("u.username = ?", strings.ToLower(strings.TrimSpace(f.Username)))
	}
	if f.Role != "" {
		w.add("? = ANY (u.roles)", strings.ToUpper(strings.TrimSpace(f.Role)))
	}
	if !f.IncludeInactive {
		w.add("u.status = 'ACTIVE'")
	}

	rows, err := s.pool.Query(ctx, `
		SELECT `+userColumns+`
		FROM app_users u
		WHERE TRUE`+w.where()+`
		ORDER BY u.username`, w.args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanUser)
}

// GetUser looks an account up by id.
func (s *Store) GetUser(ctx context.Context, id int64) (domain.User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userColumns+` FROM app_users u WHERE u.id = $1`, id)
	if err != nil {
		return domain.User{}, err
	}
	users, err := collect(rows, scanUser)
	if err != nil {
		return domain.User{}, err
	}
	if len(users) == 0 {
		return domain.User{}, pgx.ErrNoRows
	}
	return users[0], nil
}

// GetUserByUsername looks an account up by name, whatever its status.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (domain.User, error) {
	users, err := s.ListUsers(ctx, UserFilter{Username: username, IncludeInactive: true})
	if err != nil {
		return domain.User{}, err
	}
	if len(users) == 0 {
		return domain.User{}, pgx.ErrNoRows
	}
	return users[0], nil
}

// CountUsers returns how many accounts exist, and how many of those are demo
// accounts with a published password.
//
// The server uses both on start: to warn about a login-enabled deployment that
// nobody can sign in to, and about one anybody can.
func (s *Store) CountUsers(ctx context.Context) (total, demo int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE is_demo AND status = 'ACTIVE')
		FROM app_users`).Scan(&total, &demo)
	return total, demo, err
}

// PasswordDigest returns the stored bcrypt digest for an account.
//
// It is the only read of that column, and it is separate from the user read so
// the digest cannot travel with a user record by accident.
func (s *Store) PasswordDigest(ctx context.Context, userID int64) (string, error) {
	var hash *string
	if err := s.pool.QueryRow(ctx,
		`SELECT password_hash FROM app_users WHERE id = $1`, userID).Scan(&hash); err != nil {
		return "", err
	}
	if hash == nil {
		return "", nil
	}
	return *hash, nil
}

// InsertUser creates an account. An empty digest creates an account that
// cannot sign in yet.
func (s *Store) InsertUser(ctx context.Context, u domain.User, digest, actor string) (domain.User, error) {
	var hash *string
	if digest != "" {
		hash = &digest
	}
	var changedAt *time.Time
	if digest != "" {
		now := time.Now()
		changedAt = &now
	}

	rows, err := s.pool.Query(ctx, `
		INSERT INTO app_users (username, display_name, email, password_hash, roles,
		                       status, must_change_password, is_demo, password_changed_at,
		                       remark, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
		RETURNING `+userColumnsBare,
		strings.ToLower(strings.TrimSpace(u.Username)), u.DisplayName, u.Email, hash, u.Roles,
		u.Status, u.MustChangePassword, u.IsDemo, changedAt, u.Remark, actor)
	if err != nil {
		return domain.User{}, err
	}
	return firstUser(rows)
}

// UpdateUser changes the editable fields of an account, refusing a stale
// version (optimistic locking, as elsewhere in the master data).
//
// Roles, status and display fields are here; the password is not — it moves
// only through SetPassword, so every change to it is one code path.
func (s *Store) UpdateUser(ctx context.Context, u domain.User, actor string) (domain.User, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE app_users u
		SET display_name = $2,
		    email        = $3,
		    roles        = $4,
		    status       = $5,
		    remark       = $6,
		    updated_at   = now(),
		    updated_by   = $7,
		    version      = version + 1
		WHERE u.id = $1 AND u.version = $8
		RETURNING `+userColumns,
		u.ID, u.DisplayName, u.Email, u.Roles, u.Status, u.Remark, actor, u.Version)
	if err != nil {
		return domain.User{}, err
	}
	return firstUser(rows)
}

// SetPassword replaces an account's password digest and clears any throttle.
//
// Every existing session for the user is revoked in the same transaction: a
// password change that leaves the old sessions alive does not achieve what the
// person changing it believes it achieves.
func (s *Store) SetPassword(ctx context.Context, userID int64, digest string, mustChange bool, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE app_users
		SET password_hash        = $2,
		    must_change_password = $3,
		    password_changed_at  = now(),
		    failed_attempts      = 0,
		    locked_until         = NULL,
		    updated_at           = now(),
		    updated_by           = $4,
		    version              = version + 1
		WHERE id = $1`, userID, digest, mustChange, actor)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}

	if _, err := tx.Exec(ctx, `
		UPDATE user_sessions SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RecordLoginSuccess clears the throttle and stamps the login time.
func (s *Store) RecordLoginSuccess(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE app_users
		SET failed_attempts = 0, locked_until = NULL, last_login_at = now()
		WHERE id = $1`, userID)
	return err
}

// RecordLoginFailure counts a wrong password and, once the count reaches
// maxAttempts, holds the account for lockFor.
//
// The hold is a delay rather than a permanent lock: guessing becomes
// impractical, while someone who simply mistyped their password is not locked
// out of their own account until an administrator intervenes.
func (s *Store) RecordLoginFailure(ctx context.Context, userID int64, maxAttempts int, lockFor time.Duration) (domain.User, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE app_users u
		SET failed_attempts = u.failed_attempts + 1,
		    locked_until    = CASE WHEN u.failed_attempts + 1 >= $2
		                           THEN now() + $3::interval
		                           ELSE u.locked_until END
		WHERE u.id = $1
		RETURNING `+userColumns,
		userID, maxAttempts, lockFor.String())
	if err != nil {
		return domain.User{}, err
	}
	return firstUser(rows)
}

// firstUser reads exactly one user from a RETURNING query.
func firstUser(rows pgx.Rows) (domain.User, error) {
	users, err := collect(rows, scanUser)
	if err != nil {
		return domain.User{}, err
	}
	if len(users) == 0 {
		return domain.User{}, pgx.ErrNoRows
	}
	return users[0], nil
}

// --- sessions ---------------------------------------------------------------

// CreateSession records a new session for a user.
func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash []byte, expiresAt time.Time, userAgent, clientIP string) (domain.UserSession, error) {
	var v domain.UserSession
	err := s.pool.QueryRow(ctx, `
		INSERT INTO user_sessions (user_id, token_hash, expires_at, user_agent, client_ip)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, user_id, issued_at, expires_at, last_seen_at, revoked_at, user_agent, client_ip`,
		userID, tokenHash, expiresAt, truncate(userAgent, 400), truncate(clientIP, 60)).
		Scan(&v.ID, &v.UserID, &v.IssuedAt, &v.ExpiresAt, &v.LastSeenAt, &v.RevokedAt, &v.UserAgent, &v.ClientIP)
	return v, err
}

// SessionUser returns the account a live session belongs to.
//
// A session that is expired, revoked, or attached to a deactivated account is
// reported as absent rather than as an error: the caller acts on all three the
// same way, and telling them apart would leak which is which.
func (s *Store) SessionUser(ctx context.Context, tokenHash []byte) (domain.User, domain.UserSession, bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+userColumns+`,
		       s.id, s.user_id, s.issued_at, s.expires_at, s.last_seen_at,
		       s.revoked_at, s.user_agent, s.client_ip
		FROM user_sessions s
		JOIN app_users u ON u.id = s.user_id
		WHERE s.token_hash = $1
		  AND s.revoked_at IS NULL
		  AND s.expires_at > now()
		  AND u.status = 'ACTIVE'`, tokenHash)
	if err != nil {
		return domain.User{}, domain.UserSession{}, false, err
	}

	type pair struct {
		user    domain.User
		session domain.UserSession
	}
	found, err := collect(rows, func(r pgx.Rows) (pair, error) {
		var p pair
		err := r.Scan(&p.user.ID, &p.user.Username, &p.user.DisplayName, &p.user.Email,
			&p.user.Roles, &p.user.Status, &p.user.MustChangePassword, &p.user.CanSignIn,
			&p.user.IsDemo,
			&p.user.FailedAttempts, &p.user.LockedUntil, &p.user.LastLoginAt,
			&p.user.PasswordChangedAt, &p.user.Remark, &p.user.CreatedAt, &p.user.Version,
			&p.session.ID, &p.session.UserID, &p.session.IssuedAt, &p.session.ExpiresAt,
			&p.session.LastSeenAt, &p.session.RevokedAt, &p.session.UserAgent, &p.session.ClientIP)
		if p.user.Roles == nil {
			p.user.Roles = []string{}
		}
		return p, err
	})
	if err != nil {
		return domain.User{}, domain.UserSession{}, false, err
	}
	if len(found) == 0 {
		return domain.User{}, domain.UserSession{}, false, nil
	}

	// Freshen the activity stamp, but not on every request: it is used to see
	// which sessions are still in use, and a write per API call would be a
	// meaningful cost for a figure nobody reads to the minute.
	p := found[0]
	if time.Since(p.session.LastSeenAt) > time.Minute {
		if _, err := s.pool.Exec(ctx,
			`UPDATE user_sessions SET last_seen_at = now() WHERE id = $1`, p.session.ID); err != nil {
			return p.user, p.session, true, err
		}
		p.session.LastSeenAt = time.Now()
	}
	return p.user, p.session, true, nil
}

// RevokeSession ends one session. Revoking an unknown or already-revoked token
// is not an error: logging out twice should not fail.
func (s *Store) RevokeSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE user_sessions SET revoked_at = now()
		 WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash)
	return err
}

// RevokeUserSessions ends every live session for a user and reports how many.
func (s *Store) RevokeUserSessions(ctx context.Context, userID int64) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE user_sessions SET revoked_at = now()
		 WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListUserSessions returns a user's live sessions, newest first.
func (s *Store) ListUserSessions(ctx context.Context, userID int64) ([]domain.UserSession, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.id, s.user_id, u.username, s.issued_at, s.expires_at, s.last_seen_at,
		       s.revoked_at, s.user_agent, s.client_ip
		FROM user_sessions s
		JOIN app_users u ON u.id = s.user_id
		WHERE s.user_id = $1 AND s.revoked_at IS NULL AND s.expires_at > now()
		ORDER BY s.last_seen_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (domain.UserSession, error) {
		var v domain.UserSession
		err := r.Scan(&v.ID, &v.UserID, &v.Username, &v.IssuedAt, &v.ExpiresAt,
			&v.LastSeenAt, &v.RevokedAt, &v.UserAgent, &v.ClientIP)
		return v, err
	})
}

// PurgeExpiredSessions deletes sessions that ended more than grace ago, so the
// table does not grow without bound. Revoked and expired rows are kept for a
// while because they are the record of who was signed in when.
func (s *Store) PurgeExpiredSessions(ctx context.Context, grace time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM user_sessions
		WHERE (expires_at < now() - $1::interval)
		   OR (revoked_at IS NOT NULL AND revoked_at < now() - $1::interval)`, grace.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// truncate bounds a caller-supplied string before it is stored.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

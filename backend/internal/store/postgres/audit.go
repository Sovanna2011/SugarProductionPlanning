package postgres

import (
	"context"
	"fmt"
	"sync"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/auth"
)

// SystemUsername is the account rows are attributed to when there is no person
// behind the write: migrations, seeders, scheduled work. It is a real row in
// app_users with no password and an INACTIVE status, so the foreign key holds
// and a screen saying "created by System" is telling the truth.
const SystemUsername = "system"

// actorCache remembers the id of the SYSTEM account.
//
// It is looked up rather than hard-coded because the id depends on when the
// migration ran, and it is cached because the answer cannot change while the
// process lives — the row has no delete path.
type actorCache struct {
	once sync.Once
	id   int64
	err  error
}

// ActorID resolves the user id to stamp into created_by and changed_by.
//
// The identity comes from the request context, which the authentication
// middleware put there. Nothing reads it from a request body, and no input
// struct in this package has a field for it, so there is no path by which a
// client can name somebody else as the author of a row — the check is
// structural rather than a validation somebody has to remember to write.
//
// A request with no identity is attributed to SYSTEM. That happens under
// SPP_AUTH_MODE=none, which is the trusted-network development mode; it is the
// honest answer, because in that mode the server genuinely does not know who
// is calling.
func (s *Store) ActorID(ctx context.Context) (int64, error) {
	if id, ok := auth.FromContext(ctx); ok && id.UserID > 0 {
		return id.UserID, nil
	}
	return s.systemActorID(ctx)
}

func (s *Store) systemActorID(ctx context.Context) (int64, error) {
	s.systemActor.once.Do(func() {
		err := s.pool.QueryRow(ctx,
			`SELECT id FROM app_users WHERE username = $1`, SystemUsername).
			Scan(&s.systemActor.id)
		if err != nil {
			s.systemActor.err = fmt.Errorf(
				"the %q account is missing: it is created by migration 0008 and every "+
					"audit column references it: %w", SystemUsername, err)
		}
	})
	return s.systemActor.id, s.systemActor.err
}

// AuditRow is the four mandatory columns, as read back for display.
//
// The names are resolved through the user master rather than stored beside the
// ids, so renaming somebody corrects every screen that ever showed them
// instead of leaving the old name scattered across a million rows.
type AuditRow struct {
	CreatedBy     int64
	CreatedByName string
	CreatedAt     any
	ChangedBy     int64
	ChangedByName string
	ChangedAt     any
}

// AuditSelect is the SELECT list that reads the audit columns of `t` together
// with the display names behind them.
//
// It is a helper rather than copied SQL because the join is easy to get subtly
// wrong — an inner join here silently drops every row whose author has been
// removed, which is precisely the row somebody is looking for when they go
// looking. The foreign keys make that impossible today; the LEFT JOIN means it
// stays impossible if they are ever relaxed.
func AuditSelect(t string) string {
	return fmt.Sprintf(`%[1]s.created_by, COALESCE(NULLIF(cu.display_name,''), cu.username, ''),
	       %[1]s.created_at, %[1]s.changed_by,
	       COALESCE(NULLIF(hu.display_name,''), hu.username, ''), %[1]s.changed_at`, t)
}

// AuditJoin is the matching FROM clause fragment for AuditSelect.
func AuditJoin(t string) string {
	return fmt.Sprintf(`LEFT JOIN app_users cu ON cu.id = %[1]s.created_by
	 LEFT JOIN app_users hu ON hu.id = %[1]s.changed_by`, t)
}

// SystemParameter reads one configuration value.
//
// Configuration lives in a table rather than in the environment because the
// business timezone is a fact about the factory, not about the deployment: a
// second server, a migration to another host, or a container rebuilt without
// the variable must not silently change which day a posting belongs to.
func (s *Store) SystemParameter(ctx context.Context, key string) (string, error) {
	var value string
	err := s.pool.QueryRow(ctx,
		`SELECT param_value FROM system_parameters WHERE param_key = $1`, key).Scan(&value)
	return value, err
}

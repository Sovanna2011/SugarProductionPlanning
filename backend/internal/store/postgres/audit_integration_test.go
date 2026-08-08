//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/auth"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
)

// The audit standard, checked against a real schema rather than against the
// SQL that was meant to produce it.
//
// The static test in the migrations package reads CREATE TABLE statements.
// This one asks the database what it actually has, which is the only thing
// that answers "is the rule true" rather than "was the rule written down".

// exempt matches migrations.auditExempt, and the reason is the same one.
var exempt = map[string]bool{"schema_migrations": true}

func TestSchemaCarriesTheAuditStandard(t *testing.T) {
	store, ctx := open(t)
	pool := store.Pool()
	_ = store

	rows, err := pool.Query(ctx, `
		SELECT t.table_name,
		       count(*) FILTER (WHERE c.column_name = 'created_by' AND c.data_type = 'bigint'),
		       count(*) FILTER (WHERE c.column_name = 'changed_by' AND c.data_type = 'bigint'),
		       count(*) FILTER (WHERE c.column_name = 'created_at'
		                          AND c.data_type = 'timestamp with time zone'),
		       count(*) FILTER (WHERE c.column_name = 'changed_at'
		                          AND c.data_type = 'timestamp with time zone'),
		       count(*) FILTER (WHERE c.column_name IN ('created_by','changed_by','created_at','changed_at')
		                          AND c.is_nullable = 'YES')
		FROM information_schema.tables t
		JOIN information_schema.columns c
		  ON c.table_schema = t.table_schema AND c.table_name = t.table_name
		WHERE t.table_schema = 'public' AND t.table_type = 'BASE TABLE'
		GROUP BY t.table_name ORDER BY t.table_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	checked := 0
	for rows.Next() {
		var table string
		var createdBy, changedBy, createdAt, changedAt, nullable int
		if err := rows.Scan(&table, &createdBy, &changedBy, &createdAt, &changedAt, &nullable); err != nil {
			t.Fatal(err)
		}
		if exempt[table] {
			continue
		}
		checked++
		if createdBy != 1 || changedBy != 1 {
			t.Errorf("%s: created_by/changed_by are not both bigint (%d/%d). "+
				"A username in text cannot be joined to the user master and goes "+
				"stale the moment somebody is renamed.", table, createdBy, changedBy)
		}
		if createdAt != 1 || changedAt != 1 {
			t.Errorf("%s: created_at/changed_at are not both timestamptz (%d/%d). "+
				"A timestamp without a zone is an argument waiting to happen.",
				table, createdAt, changedAt)
		}
		if nullable != 0 {
			t.Errorf("%s: %d audit column(s) are nullable. A row with no author "+
				"is the row somebody will need the author of.", table, nullable)
		}
	}
	if checked < 20 {
		t.Fatalf("only %d tables checked; the query is wrong, not the schema", checked)
	}
	t.Logf("%d tables carry all four audit columns", checked)
}

func TestEveryAuditedTableHasItsForeignKeysAndTrigger(t *testing.T) {
	store, ctx := open(t)
	pool := store.Pool()
	_ = store

	// Foreign keys checked by the columns they constrain, not by name — a
	// constraint that merely looks right in a listing is not a constraint.
	rows, err := pool.Query(ctx, `
		WITH audited AS (
		  SELECT DISTINCT table_name FROM information_schema.columns
		  WHERE table_schema = 'public' AND column_name = 'created_by'
		), fks AS (
		  SELECT cl.relname AS table_name, a.attname AS column_name
		  FROM pg_constraint con
		  JOIN pg_class cl ON cl.oid = con.conrelid
		  JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = ANY (con.conkey)
		  JOIN pg_class ref ON ref.oid = con.confrelid
		  WHERE con.contype = 'f' AND ref.relname = 'app_users'
		), trg AS (
		  SELECT DISTINCT event_object_table AS table_name FROM information_schema.triggers
		  WHERE trigger_schema = 'public' AND trigger_name LIKE '%_audit_stamp'
		)
		SELECT audited.table_name,
		       EXISTS (SELECT 1 FROM fks WHERE fks.table_name = audited.table_name
		                                   AND fks.column_name = 'created_by'),
		       EXISTS (SELECT 1 FROM fks WHERE fks.table_name = audited.table_name
		                                   AND fks.column_name = 'changed_by'),
		       EXISTS (SELECT 1 FROM trg WHERE trg.table_name = audited.table_name)
		FROM audited ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var table string
		var createdFK, changedFK, hasTrigger bool
		if err := rows.Scan(&table, &createdFK, &changedFK, &hasTrigger); err != nil {
			t.Fatal(err)
		}
		if !createdFK || !changedFK {
			t.Errorf("%s: created_by=%v changed_by=%v reference app_users — a user id "+
				"with no foreign key behind it is a number that looks joinable and is not",
				table, createdFK, changedFK)
		}
		if !hasTrigger {
			t.Errorf("%s: no audit_stamp trigger, so created_at and changed_at are "+
				"whatever the writer chose to send", table)
		}
	}
}

// The four guarantees, exercised rather than inspected.
func TestTheDatabaseRefusesToLetAuditFieldsBeFaked(t *testing.T) {
	store, ctx := open(t)
	pool := store.Pool()

	sovanna := mustUser(t, ctx, store, "audit-sovanna", "SOVANNA")
	admin := mustUser(t, ctx, store, "audit-admin", "ADMIN")

	long_ago := time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)

	var id int64
	err := pool.QueryRow(ctx, `
		INSERT INTO system_parameters (param_key, param_value, created_by, changed_by,
		                               created_at, changed_at)
		VALUES ('AUDIT_TEST', 'one', $1, $1, $2, $2) RETURNING id`,
		sovanna.ID, long_ago).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM system_parameters WHERE id = $1`, id)
	})

	var createdBy, changedBy int64
	var createdAt, changedAt time.Time
	read := func() {
		t.Helper()
		if err := pool.QueryRow(ctx, `
			SELECT created_by, created_at, changed_by, changed_at
			FROM system_parameters WHERE id = $1`, id).
			Scan(&createdBy, &createdAt, &changedBy, &changedAt); err != nil {
			t.Fatal(err)
		}
	}

	read()
	if !createdAt.After(time.Now().Add(-time.Minute)) {
		t.Errorf("created_at was taken from the insert (%s) instead of the database "+
			"clock — section 2 says users do not choose it", createdAt)
	}
	if !changedAt.After(time.Now().Add(-time.Minute)) {
		t.Errorf("changed_at was taken from the insert (%s)", changedAt)
	}
	firstCreatedAt := createdAt

	// An update that tries to rewrite who created the row, the way a client
	// posting a whole object back would.
	if _, err := pool.Exec(ctx, `
		UPDATE system_parameters
		SET param_value = 'two', created_by = $2, created_at = $3, changed_by = $2
		WHERE id = $1`, id, admin.ID, long_ago); err != nil {
		t.Fatal(err)
	}

	read()
	if createdBy != sovanna.ID {
		t.Errorf("created_by was rewritten by an update (%d, wanted %d). Section 7: "+
			"an update touches changed_by and changed_at, nothing else.", createdBy, sovanna.ID)
	}
	if !createdAt.Equal(firstCreatedAt) {
		t.Errorf("created_at moved on update: %s -> %s", firstCreatedAt, createdAt)
	}
	if changedBy != admin.ID {
		t.Errorf("changed_by is %d, wanted %d — an update must record who made it",
			changedBy, admin.ID)
	}
	if !changedAt.After(firstCreatedAt) {
		t.Errorf("changed_at (%s) did not move past created_at (%s) on update",
			changedAt, firstCreatedAt)
	}

	// An author who does not exist.
	if _, err := pool.Exec(ctx, `
		INSERT INTO system_parameters (param_key, param_value, created_by, changed_by)
		VALUES ('AUDIT_TEST_BAD', 'x', 999999999, 999999999)`); err == nil {
		_, _ = pool.Exec(ctx, `DELETE FROM system_parameters WHERE param_key = 'AUDIT_TEST_BAD'`)
		t.Error("a row was written naming a user id that does not exist")
	}

	// An author who cannot be deleted out from under the record.
	if _, err := pool.Exec(ctx, `DELETE FROM app_users WHERE id = $1`, sovanna.ID); err == nil {
		t.Error("a user who created rows was deleted, orphaning the audit trail. " +
			"Accounts that have touched anything are deactivated, not removed.")
	}
}

// The actor comes from the session, and there is no field for a client to
// claim otherwise — this proves the first half, since the second is structural.
func TestTheActorComesFromTheSessionNotTheRequest(t *testing.T) {
	store, ctx := open(t)

	user := mustUser(t, ctx, store, "audit-actor", "Actor")

	anon, err := store.ActorID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	signedIn, err := store.ActorID(auth.WithIdentity(ctx, auth.Identity{
		UserID: user.ID, Subject: user.Username,
	}))
	if err != nil {
		t.Fatal(err)
	}

	if signedIn != user.ID {
		t.Errorf("a signed-in request was attributed to %d, not to %d", signedIn, user.ID)
	}
	if anon == signedIn {
		t.Error("an unauthenticated request was attributed to the same account as a " +
			"signed-in one")
	}

	var username string
	if err := store.Pool().QueryRow(ctx,
		`SELECT username FROM app_users WHERE id = $1`, anon).Scan(&username); err != nil {
		t.Fatal(err)
	}
	if username != postgres.SystemUsername {
		t.Errorf("an unauthenticated write was attributed to %q, not to the SYSTEM "+
			"account. Guessing a person is worse than naming the system.", username)
	}
}

func mustUser(t *testing.T, ctx context.Context, store *postgres.Store, username, display string) domain.User {
	t.Helper()
	if existing, err := store.GetUserByUsername(ctx, username); err == nil {
		return existing
	}
	u, err := store.InsertUser(ctx, domain.User{
		Username: username, DisplayName: display, Roles: []string{auth.RoleViewer},
		Status: domain.StatusActive,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

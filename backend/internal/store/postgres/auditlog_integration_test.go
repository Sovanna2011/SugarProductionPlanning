//go:build integration

package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
)

// The central audit log, exercised through real writes.
//
// It is written by a trigger, so every test here changes something the
// ordinary way and then asks what the log says about it — never by inserting a
// log row directly, which would prove only that the table accepts inserts.

func TestAChangeIsRecordedWithItsOldAndNewValue(t *testing.T) {
	store, ctx := open(t)
	pool := store.Pool()

	sovanna := mustUser(t, ctx, store, "log-sovanna", "SOVANNA")

	var before float64
	if err := pool.QueryRow(ctx,
		`SELECT physical_capacity FROM storage_locations WHERE storage_code = 'FG-WH01'`).
		Scan(&before); err != nil {
		t.Fatal(err)
	}
	after := before + 2000

	if _, err := pool.Exec(ctx, `
		UPDATE storage_locations SET physical_capacity = $1, changed_by = $2
		WHERE storage_code = 'FG-WH01'`, after, sovanna.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			UPDATE storage_locations SET physical_capacity = $1, changed_by = 1
			WHERE storage_code = 'FG-WH01'`, before)
	})

	entries, err := store.ListAuditLog(ctx, postgres.AuditLogFilter{
		TableName: "storage_locations", RecordKey: "FG-WH01", Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("changing a warehouse's capacity left nothing in the audit log")
	}
	e := entries[0]

	if e.Action != "CHANGE" {
		t.Errorf("action is %q, wanted CHANGE", e.Action)
	}
	if e.ActedBy != sovanna.ID {
		t.Errorf("the change is attributed to %d, not to %d", e.ActedBy, sovanna.ID)
	}
	if e.ActedByName != "SOVANNA" {
		t.Errorf("the name is %q — the log resolves it through the user master", e.ActedByName)
	}
	if e.RecordKey != "FG-WH01" {
		t.Errorf("record key is %q; a log nobody can read without joining back to a row "+
			"that may since have been deleted gets read once and never again", e.RecordKey)
	}

	diff, ok := e.Changes["physical_capacity"]
	if !ok {
		t.Fatalf("the capacity change is not in the diff: %v", e.Changes)
	}
	// The values arrive as JSON numbers.
	if got := toFloat(diff.Old); got != before {
		t.Errorf("old capacity recorded as %v, wanted %v", diff.Old, before)
	}
	if got := toFloat(diff.New); got != after {
		t.Errorf("new capacity recorded as %v, wanted %v", diff.New, after)
	}

	// Only the field that moved.
	for _, f := range e.ChangedFields {
		if f == "changed_at" || f == "changed_by" || f == "version" {
			t.Errorf("%q is in the changed fields. It moves on every update, so it is "+
				"noise on every row — and the log records both in its own columns.", f)
		}
	}
}

func TestSavingWithoutChangingAnythingIsNotRecorded(t *testing.T) {
	store, ctx := open(t)
	pool := store.Pool()

	countEntries := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM audit_logs WHERE table_name = 'storage_locations'`).
			Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	before := countEntries()
	// The shape of a form saved with nothing touched.
	if _, err := pool.Exec(ctx, `
		UPDATE storage_locations SET storage_name = storage_name, changed_by = 1
		WHERE storage_code = 'FG-WH01'`); err != nil {
		t.Fatal(err)
	}
	if after := countEntries(); after != before {
		t.Errorf("a save that changed nothing wrote %d log entries. They say a record "+
			"was changed and cannot say how, which is worse than silence.", after-before)
	}
}

func TestASecretIsRecordedAsHavingChangedButNeverAsItsValue(t *testing.T) {
	store, ctx := open(t)
	pool := store.Pool()

	user := mustUser(t, ctx, store, "log-secret", "Secret Holder")
	const secret = "argon2id$v=19$m=65536$THIS-MUST-NOT-REACH-THE-LOG"

	if _, err := pool.Exec(ctx,
		`UPDATE app_users SET password_hash = $2, changed_by = 1 WHERE id = $1`,
		user.ID, secret); err != nil {
		t.Fatal(err)
	}

	var leaked int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_logs WHERE changes::text LIKE '%THIS-MUST-NOT-REACH%'`).
		Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked > 0 {
		t.Errorf("%d log entries contain the password hash. Hashing it in app_users "+
			"achieves nothing if it is copied into a table that gets exported to "+
			"settle a capacity dispute.", leaked)
	}

	entries, err := store.ListAuditLog(ctx, postgres.AuditLogFilter{
		TableName: "app_users", RecordID: user.ID, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("changing a password left nothing in the log at all")
	}
	if _, ok := entries[0].Changes["password_hash"]; !ok {
		t.Error("the password change is not recorded. That it changed, by whom and " +
			"when is exactly what an audit trail is for; only the value is not.")
	}
}

func TestTheLogRecordsAChangeMadeOutsideTheApplication(t *testing.T) {
	store, ctx := open(t)
	pool := store.Pool()

	// The UPDATE typed by hand at three in the morning to unstick something.
	// No Go code involved, no repository, no service — the case an
	// application-maintained log misses, and the case most worth catching.
	if _, err := pool.Exec(ctx, `
		INSERT INTO system_parameters (param_key, param_value, created_by, changed_by)
		VALUES ('AUDIT_LOG_TEST', 'first', 1, 1)
		ON CONFLICT (param_key) DO UPDATE SET param_value = 'first'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM system_parameters WHERE param_key = 'AUDIT_LOG_TEST'`)
	})
	if _, err := pool.Exec(ctx, `
		UPDATE system_parameters SET param_value = 'second', changed_by = 1
		WHERE param_key = 'AUDIT_LOG_TEST'`); err != nil {
		t.Fatal(err)
	}

	entries, err := store.ListAuditLog(ctx, postgres.AuditLogFilter{
		TableName: "system_parameters", RecordKey: "AUDIT_LOG_TEST",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Fatalf("a hand-typed insert and update produced %d log entries, wanted 2. "+
			"A log the application maintains records exactly the writes the "+
			"application remembered, which is the set least likely to need auditing.",
			len(entries))
	}
	if entries[0].Action != "CHANGE" || entries[1].Action != "CREATE" {
		t.Errorf("newest first should be CHANGE then CREATE, got %s then %s",
			entries[0].Action, entries[1].Action)
	}
	if d, ok := entries[0].Changes["param_value"]; !ok {
		t.Error("the value change is not in the diff")
	} else if d.Old != "first" || d.New != "second" {
		t.Errorf("recorded %v -> %v, wanted first -> second", d.Old, d.New)
	}
}

func TestADeleteIsRecordedWithWhatWasThere(t *testing.T) {
	store, ctx := open(t)
	pool := store.Pool()

	if _, err := pool.Exec(ctx, `
		INSERT INTO system_parameters (param_key, param_value, created_by, changed_by)
		VALUES ('AUDIT_DELETE_TEST', 'gone soon', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM system_parameters WHERE param_key = 'AUDIT_DELETE_TEST'`); err != nil {
		t.Fatal(err)
	}

	entries, err := store.ListAuditLog(ctx, postgres.AuditLogFilter{
		TableName: "system_parameters", RecordKey: "AUDIT_DELETE_TEST", Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].Action != "DELETE" {
		t.Fatal("deleting a row left no DELETE entry — the one case where the row " +
			"itself can no longer answer any questions")
	}
	if d, ok := entries[0].Changes["param_value"]; !ok || d.Old != "gone soon" {
		t.Errorf("the deleted value is not recorded: %v", entries[0].Changes)
	}
}

func TestWhatTheLogDoesNotCoverIsStatedRatherThanDiscovered(t *testing.T) {
	store, ctx := open(t)

	exclusions, err := store.ListAuditLogExclusions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(exclusions) == 0 {
		t.Fatal("no exclusions listed, but three tables are excluded")
	}

	named := map[string]string{}
	for _, e := range exclusions {
		named[e.TableName] = e.Reason
		if len(strings.Fields(e.Reason)) < 8 {
			t.Errorf("%s is excluded with no real reason (%q). Somebody reading history "+
				"and finding nothing needs to know whether nothing happened or nothing "+
				"was recorded.", e.TableName, e.Reason)
		}
	}
	for _, want := range []string{"audit_logs", "inventory_balances"} {
		if _, ok := named[want]; !ok {
			t.Errorf("%s is not covered by the log and not listed as excluded", want)
		}
	}

	// And the exclusions are real: the balance table is written on every
	// posting, so if it were logged the log would be mostly balances.
	pool := store.Pool()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_logs WHERE table_name = 'inventory_balances'`).
		Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n > 0 {
		t.Errorf("%d balance entries in the log. It is derived from movements that are "+
			"themselves logged, so this is volume without information — and it buries "+
			"the movements somebody is actually looking for.", n)
	}
}

func TestPostingAMovementIsRecorded(t *testing.T) {
	store, ctx := open(t)

	entries, err := store.ListAuditLog(ctx, postgres.AuditLogFilter{
		TableName: "inventory_movements", Limit: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Skip("no movements posted in this database yet")
	}
	for _, e := range entries {
		if e.Action != "CREATE" {
			t.Errorf("movement %d recorded as %s. The ledger is append-only; a movement "+
				"that changed after posting is the thing this log exists to surface.",
				e.ID, e.Action)
		}
	}
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

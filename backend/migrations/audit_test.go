package migrations

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The mandatory audit standard, checked against the migrations themselves.
//
// This test needs no database, which is the point: it runs in the fast CI job
// and fails on the pull request that adds a non-compliant table, rather than
// three weeks later when somebody notices a screen has nothing to show in its
// Administrative Information panel. The live schema is checked separately, in
// TestSchemaCarriesTheAuditStandard, which needs a real Postgres.
//
// It applies to tables created by 0008 and later. Everything before it was
// brought into line by 0008's backfill, which alters tables rather than
// declaring them, and a parser reading CREATE TABLE cannot see that.

// auditColumns are required on every table.
var auditColumns = []string{"created_by", "created_at", "changed_by", "changed_at"}

// auditExempt names the tables that do not carry them, with the reason.
//
// An exemption list is a liability — it is where a standard goes to die — so
// it holds exactly one entry, the reason is written down, and this test fails
// if an entry is added without one.
var auditExempt = map[string]string{
	"schema_migrations": "the migration ledger. It records which migration has run, " +
		"including the one that creates app_users, so a foreign key from it to the " +
		"user table cannot be satisfied at the moment it is first written. Its " +
		"applied_at column carries the same information.",
}

var (
	reCreateTable = regexp.MustCompile(`(?is)CREATE TABLE (?:IF NOT EXISTS )?(\w+)\s*\(`)
	reLineComment = regexp.MustCompile(`--[^\n]*`)
)

// tableBody returns the text between the parentheses of each CREATE TABLE.
func tablesIn(sql string) map[string]string {
	sql = reLineComment.ReplaceAllString(sql, "")
	out := map[string]string{}
	for _, m := range reCreateTable.FindAllStringSubmatchIndex(sql, -1) {
		name := sql[m[2]:m[3]]
		depth, i := 1, m[1]
		for i < len(sql) && depth > 0 {
			switch sql[i] {
			case '(':
				depth++
			case ')':
				depth--
			}
			i++
		}
		out[name] = sql[m[1]:i]
	}
	return out
}

func TestEveryNewTableDeclaresTheAuditColumns(t *testing.T) {
	files, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	checked := 0
	for _, name := range names {
		// Tables older than the standard were migrated into it by 0008.
		if name < "0008" {
			continue
		}
		for table, body := range tablesIn(files[name]) {
			if _, ok := auditExempt[table]; ok {
				continue
			}
			checked++
			for _, col := range auditColumns {
				if !regexp.MustCompile(`(?im)^\s*` + col + `\s`).MatchString(body) {
					t.Errorf("%s: table %q has no %s column.\n\n%s",
						name, table, col, auditRuleReminder(table))
				}
			}
			if !strings.Contains(body, "REFERENCES app_users (id)") {
				t.Errorf("%s: table %q does not reference app_users from its audit "+
					"columns. Storing a user id that points at nothing is worse than "+
					"storing the name, because it looks joinable.", name, table)
			}
			// The trigger is what makes the timestamps authoritative rather
			// than advisory, so a table without one is only half compliant.
			trigger := table + "_audit_stamp"
			if !strings.Contains(files[name], trigger) {
				t.Errorf("%s: table %q has no %s trigger, so its created_at and "+
					"changed_at are whatever the caller supplied.", name, table, trigger)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no tables were checked — the scan is broken, not the schema")
	}
	t.Logf("%d table(s) created at or after the standard, all compliant", checked)
}

func TestTheExemptionListExplainsItself(t *testing.T) {
	for table, why := range auditExempt {
		if len(strings.Fields(why)) < 10 {
			t.Errorf("table %q is exempt from the audit standard with no real reason "+
				"given (%q). An exemption nobody can argue with is an exemption "+
				"nobody will remove.", table, why)
		}
	}
	if len(auditExempt) > 2 {
		t.Errorf("%d tables are exempt from the audit standard. Past one or two "+
			"this is not an exception any more, it is a second convention.",
			len(auditExempt))
	}
}

func auditRuleReminder(table string) string {
	return fmt.Sprintf(`Every table carries the same four columns:

    created_by  BIGINT      NOT NULL REFERENCES app_users (id) ON DELETE RESTRICT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    changed_by  BIGINT      NOT NULL REFERENCES app_users (id) ON DELETE RESTRICT,
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

and the trigger that makes the timestamps authoritative:

    CREATE TRIGGER %[1]s_audit_stamp
        BEFORE INSERT OR UPDATE ON %[1]s
        FOR EACH ROW EXECUTE FUNCTION audit_stamp();

See docs/audit-fields.md. If this table genuinely cannot carry them, add it to
auditExempt with the reason.`, table)
}

package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// AuditLogEntry is one recorded change.
//
// The log is written by a database trigger, so this type is read-only by
// construction: there is no insert path in Go, and adding one would create a
// second way for entries to appear that the trigger does not know about.
type AuditLogEntry struct {
	ID            int64                `json:"id"`
	TableName     string               `json:"tableName"`
	RecordID      *int64               `json:"recordId,omitempty"`
	RecordKey     string               `json:"recordKey"`
	Action        string               `json:"action"`
	Changes       map[string]FieldDiff `json:"changes"`
	ChangedFields []string             `json:"changedFields"`
	ActedBy       int64                `json:"actedBy"`
	ActedByName   string               `json:"actedByName"`
	ActedAt       time.Time            `json:"actedAt"`
}

// FieldDiff is one field's before and after.
//
// `any` because a column can be a number, a string, a boolean, an array or
// null, and coercing all of them to text would lose the distinction between
// the string "0" and the number 0 — which is exactly the sort of thing a
// dispute about a capacity figure turns on.
type FieldDiff struct {
	Old any `json:"old"`
	New any `json:"new"`
}

// AuditLogFilter narrows the log to the question being asked.
//
// The three that get asked, in the order they get asked: what happened lately,
// what happened to this record, and what has this person been doing. Each has
// an index behind it.
type AuditLogFilter struct {
	TableName string
	RecordID  int64
	RecordKey string
	ActedBy   int64
	Field     string
	From      *time.Time
	To        *time.Time
	Limit     int
}

// ListAuditLog reads recorded changes, newest first.
func (s *Store) ListAuditLog(ctx context.Context, f AuditLogFilter) ([]AuditLogEntry, error) {
	var where []string
	var args []any
	add := func(clause string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if f.TableName != "" {
		add("l.table_name = $%d", f.TableName)
	}
	if f.RecordID > 0 {
		add("l.record_id = $%d", f.RecordID)
	}
	if f.RecordKey != "" {
		add("l.record_key = $%d", f.RecordKey)
	}
	if f.ActedBy > 0 {
		add("l.created_by = $%d", f.ActedBy)
	}
	if f.Field != "" {
		add("l.changed_fields @> ARRAY[$%d]::text[]", f.Field)
	}
	if f.From != nil {
		add("l.created_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("l.created_at < $%d", *f.To)
	}

	// A default rather than unlimited: the log is the one table that only ever
	// grows, and a screen that asks for all of it works fine for a month and
	// then stops working, at the point where somebody most needs it.
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	args = append(args, limit)

	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	rows, err := s.pool.Query(ctx, `
		SELECT l.id, l.table_name, l.record_id, l.record_key, l.action,
		       l.changes, l.changed_fields, l.created_by,
		       COALESCE(NULLIF(u.display_name, ''), u.username, ''), l.created_at
		FROM audit_logs l
		LEFT JOIN app_users u ON u.id = l.created_by`+clause+`
		ORDER BY l.created_at DESC, l.id DESC
		LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("read audit log: %w", err)
	}
	defer rows.Close()

	out := []AuditLogEntry{}
	for rows.Next() {
		var e AuditLogEntry
		if err := rows.Scan(&e.ID, &e.TableName, &e.RecordID, &e.RecordKey, &e.Action,
			&e.Changes, &e.ChangedFields, &e.ActedBy, &e.ActedByName, &e.ActedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AuditLogExclusion is a table the log deliberately does not cover.
type AuditLogExclusion struct {
	TableName string `json:"tableName"`
	Reason    string `json:"reason"`
}

// ListAuditLogExclusions returns what the log does not cover, and why.
//
// Served alongside the log rather than kept in a comment somewhere. Somebody
// reading history and finding nothing about a table needs to know whether that
// means nothing happened or nothing was recorded, and those are very different
// answers to be guessing between.
func (s *Store) ListAuditLogExclusions(ctx context.Context) ([]AuditLogExclusion, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT table_name, reason FROM audit_log_exclusions ORDER BY table_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AuditLogExclusion{}
	for rows.Next() {
		var e AuditLogExclusion
		if err := rows.Scan(&e.TableName, &e.Reason); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

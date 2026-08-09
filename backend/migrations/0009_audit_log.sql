-- ---------------------------------------------------------------------------
-- 0009 — the central audit log
-- ---------------------------------------------------------------------------
-- The four audit fields on every table say who last changed a record and when.
-- They do not say what changed. That is the difference between
--
--     ADMIN changed Finished Sugar Warehouse 1 on 9 August
--
-- and
--
--     ADMIN changed its capacity from 20,000 t to 22,000 t on 9 August
--
-- and for a capacity figure somebody will eventually dispute, the second is
-- the half that settles it.
--
-- **A trigger writes it, not the application.** The same argument as 0008,
-- with more force: an audit log the application maintains is an audit log that
-- records exactly the writes the application remembered to record, which is
-- the set of writes least likely to need auditing. A trigger sees the UPDATE
-- typed by hand at three in the morning, the data fix run from a psql session,
-- and the second application nobody mentioned.
--
-- **It diffs generically.** to_jsonb(OLD) against to_jsonb(NEW), one entry per
-- field that actually changed. No table needs its own logging code, so a table
-- added next year is logged the day it is created rather than the day somebody
-- remembers to add it.

-- ---------------------------------------------------------------------------
-- The table
-- ---------------------------------------------------------------------------
-- It carries the four mandatory fields like everything else, and here they do
-- double duty: a log row is written once and never updated, so created_by and
-- created_at *are* the who and the when of the action. Separate acted_by and
-- acted_at columns would be the same two values under different names, and two
-- names for one fact is how they drift apart.
CREATE TABLE audit_logs (
    id           BIGSERIAL PRIMARY KEY,

    -- What was touched.
    table_name   TEXT   NOT NULL,
    record_id    BIGINT,
    -- The business key, when the row has one: FG-WH01 rather than 7. A log
    -- nobody can read without joining back to the row it describes — which
    -- may since have been deleted — is a log that gets read once and then
    -- never again.
    record_key   TEXT   NOT NULL DEFAULT '',

    action       TEXT   NOT NULL CHECK (action IN ('CREATE', 'CHANGE', 'DELETE')),

    -- {"physical_capacity": {"old": 20000, "new": 22000}, ...}
    --
    -- JSONB rather than a row per field: one action is one row, which is how
    -- somebody reads it, and the shape is the same whether one field moved or
    -- twenty. The field list is also kept as an array so "everything that ever
    -- touched physical_capacity" is an index lookup rather than a JSON scan.
    changes         JSONB  NOT NULL DEFAULT '{}'::jsonb,
    changed_fields  TEXT[] NOT NULL DEFAULT '{}',

    created_by   BIGINT      NOT NULL REFERENCES app_users (id) ON DELETE RESTRICT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    changed_by   BIGINT      NOT NULL REFERENCES app_users (id) ON DELETE RESTRICT,
    changed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    version      INTEGER     NOT NULL DEFAULT 1
);

CREATE INDEX audit_logs_created_by_idx ON audit_logs (created_by);
CREATE INDEX audit_logs_changed_by_idx ON audit_logs (changed_by);

-- The three questions this table gets asked, in the order it gets asked them:
-- "what happened lately", "what happened to this record", "who did what".
CREATE INDEX audit_logs_recent_idx ON audit_logs (created_at DESC);
CREATE INDEX audit_logs_record_idx ON audit_logs (table_name, record_id, created_at DESC);
CREATE INDEX audit_logs_actor_idx  ON audit_logs (created_by, created_at DESC);
CREATE INDEX audit_logs_fields_idx ON audit_logs USING GIN (changed_fields);

CREATE TRIGGER audit_logs_audit_stamp
    BEFORE INSERT OR UPDATE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_stamp();

COMMENT ON TABLE audit_logs IS
    'Who changed what, on which record, from what value to what value. Written '
    'by the audit_log_change() trigger on every audited table, so a change made '
    'outside the application is recorded too.';

-- ---------------------------------------------------------------------------
-- What is not logged, and why
-- ---------------------------------------------------------------------------
-- Two tables, both for reasons that are about the log being useful rather than
-- about it being inconvenient.
--
-- audit_logs itself: a trigger logging its own writes does not terminate.
--
-- inventory_balances: it is derived, not entered. Every row of it is the sum
-- of posted movements, and every movement is logged. Logging the balance as
-- well would double the volume of the busiest table in the system to record a
-- number that can be recomputed from entries already in the log — and would
-- bury the movements, which are what somebody is actually looking for, under
-- the balance updates they caused.
--
-- schema_migrations has no audit columns at all, so there is no actor to
-- attribute a log entry to. It keeps applied_at.
CREATE TABLE audit_log_exclusions (
    table_name TEXT PRIMARY KEY,
    reason     TEXT NOT NULL CHECK (length(reason) > 40),

    created_by BIGINT      NOT NULL REFERENCES app_users (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    changed_by BIGINT      NOT NULL REFERENCES app_users (id) ON DELETE RESTRICT,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    version    INTEGER     NOT NULL DEFAULT 1
);

CREATE INDEX audit_log_exclusions_created_by_idx ON audit_log_exclusions (created_by);
CREATE INDEX audit_log_exclusions_changed_by_idx ON audit_log_exclusions (changed_by);

CREATE TRIGGER audit_log_exclusions_audit_stamp
    BEFORE INSERT OR UPDATE ON audit_log_exclusions
    FOR EACH ROW EXECUTE FUNCTION audit_stamp();

-- The reason is a NOT NULL column with a minimum length rather than a comment,
-- because an exclusion list is where an audit trail goes to die and the cost
-- of adding to it should be having to write down why.
INSERT INTO audit_log_exclusions (table_name, reason, created_by, changed_by)
SELECT t.table_name, t.reason, u.id, u.id
FROM app_users u, (VALUES
    ('audit_logs',
     'The log itself. A trigger that logs its own writes does not terminate.'),
    ('audit_log_exclusions',
     'The exclusion list itself, for the same reason as the log. Changes to it '
     || 'are visible as a change to this table, which is read directly.'),
    ('inventory_balances',
     'Derived, never entered: every row is the sum of posted movements, and '
     || 'every movement is logged. Logging it as well would double the volume '
     || 'of the busiest table to record a number already recoverable from the '
     || 'log, and would bury the movements under the balance updates they caused.')
) AS t(table_name, reason)
WHERE u.username = 'system';

-- ---------------------------------------------------------------------------
-- The trigger
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION audit_log_change() RETURNS TRIGGER AS $$
DECLARE
    old_row  JSONB;
    new_row  JSONB;
    diff     JSONB;
    fields   TEXT[];
    act      TEXT;
    actor    BIGINT;
    rec_id   BIGINT;
    rec_key  TEXT;
    subject  JSONB;
BEGIN
    IF TG_OP = 'INSERT' THEN
        act := 'CREATE'; new_row := to_jsonb(NEW); old_row := '{}'::jsonb;
        subject := new_row;
    ELSIF TG_OP = 'DELETE' THEN
        act := 'DELETE'; old_row := to_jsonb(OLD); new_row := '{}'::jsonb;
        subject := old_row;
    ELSE
        act := 'CHANGE'; old_row := to_jsonb(OLD); new_row := to_jsonb(NEW);
        subject := new_row;
    END IF;

    -- Fields that carry no information about the change itself.
    --
    -- changed_at and changed_by move on every single update, so including them
    -- would put two entries of pure noise on every row of the log — and the
    -- log already records both, in its own created_at and created_by. Same for
    -- created_at and created_by, which by 0008 cannot change after the insert.
    -- version is the optimistic-locking counter and says nothing a reader
    -- wants. The _legacy columns are the pre-0008 usernames, kept for history
    -- and never written again.
    old_row := old_row - 'changed_at' - 'changed_by' - 'created_at' - 'created_by'
                       - 'version' - 'created_by_legacy' - 'changed_by_legacy';
    new_row := new_row - 'changed_at' - 'changed_by' - 'created_at' - 'created_by'
                       - 'version' - 'created_by_legacy' - 'changed_by_legacy';

    SELECT COALESCE(jsonb_object_agg(key, jsonb_build_object('old', o, 'new', n)), '{}'::jsonb),
           COALESCE(array_agg(key), '{}'::text[])
      INTO diff, fields
      FROM (
        SELECT COALESCE(o.key, n.key) AS key, o.value AS o, n.value AS n
        FROM jsonb_each(old_row) o
        FULL JOIN jsonb_each(new_row) n ON n.key = o.key
        WHERE o.value IS DISTINCT FROM n.value
      ) d;

    -- An update that changed nothing but the audit columns is not a change.
    -- Saving a form without touching a field would otherwise fill the log with
    -- entries that say a record was changed and cannot say how.
    IF act = 'CHANGE' AND diff = '{}'::jsonb THEN
        RETURN NULL;
    END IF;

    -- Secrets are recorded as having changed, never as what they changed to.
    --
    -- A password hash in the audit log is a password hash in every backup of
    -- the audit log, in every export somebody takes to look at a capacity
    -- dispute, and on the screen of anybody allowed to read history. The whole
    -- point of hashing it in app_users is defeated by copying it here. That a
    -- password was changed, by whom and when, is the part an audit trail is
    -- for; the value is not.
    IF diff ?| ARRAY['password_hash', 'token_hash'] THEN
        SELECT jsonb_object_agg(key,
                 CASE WHEN key IN ('password_hash', 'token_hash')
                      THEN jsonb_build_object('old', '(not recorded)', 'new', '(not recorded)')
                      ELSE value END)
          INTO diff FROM jsonb_each(diff);
    END IF;

    actor  := COALESCE((subject ->> 'changed_by')::BIGINT,
                       (subject ->> 'created_by')::BIGINT);
    rec_id := (subject ->> 'id')::BIGINT;

    -- The business key, if the row has anything that reads like one. Tried in
    -- the order a person would recognise them.
    rec_key := COALESCE(
        subject ->> 'storage_code', subject ->> 'code', subject ->> 'username',
        subject ->> 'param_key',    subject ->> 'group_code',
        subject ->> 'table_name',   subject ->> 'plan_date',
        subject ->> 'movement_date', '');

    INSERT INTO audit_logs (table_name, record_id, record_key, action,
                            changes, changed_fields, created_by, changed_by)
    VALUES (TG_TABLE_NAME, rec_id, rec_key, act, diff, fields, actor, actor);

    RETURN NULL;   -- AFTER trigger: the return value is ignored
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION audit_log_change() IS
    'Writes one audit_logs row per create, change or delete, diffing to_jsonb(OLD) '
    'against to_jsonb(NEW). Attached AFTER the fact to every audited table that is '
    'not in audit_log_exclusions.';

-- ---------------------------------------------------------------------------
-- Attach it
-- ---------------------------------------------------------------------------
-- AFTER rather than BEFORE: the log should record what was committed, not what
-- was attempted. A row refused by a check constraint or a foreign key must not
-- leave an entry saying it was written.
DO $$
DECLARE
    t TEXT;
BEGIN
    FOR t IN
        SELECT c.table_name FROM information_schema.columns c
        WHERE c.table_schema = 'public' AND c.column_name = 'changed_by'
          AND c.table_name NOT IN (SELECT table_name FROM audit_log_exclusions)
    LOOP
        EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I', t || '_audit_log', t);
        EXECUTE format(
            'CREATE TRIGGER %I AFTER INSERT OR UPDATE OR DELETE ON %I '
            'FOR EACH ROW EXECUTE FUNCTION audit_log_change()', t || '_audit_log', t);
    END LOOP;
END $$;

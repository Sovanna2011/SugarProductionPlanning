-- ---------------------------------------------------------------------------
-- 0008 — mandatory audit fields on every table
-- ---------------------------------------------------------------------------
-- Every business, master-data, configuration and transaction table carries the
-- same four columns:
--
--     created_by  BIGINT      who first wrote the row
--     created_at  TIMESTAMPTZ when, by the database clock
--     changed_by  BIGINT      who last changed it
--     changed_at  TIMESTAMPTZ when, by the database clock
--
-- Three things about this migration are worth reading before the SQL.
--
-- **The columns were nearly there already.** Eighteen of the twenty-one tables
-- had created_at/created_by/updated_at/updated_by, so most of this is a rename
-- (updated_* -> changed_*) and a type change. The rename is not cosmetic: two
-- names for the same idea is how a system ends up with half its tables audited
-- one way and half the other, and the standard is worth nothing if it is not
-- one standard.
--
-- **_by becomes a foreign key.** It held a username as text. Text goes stale
-- the moment somebody is renamed, and it cannot be joined to anything, so
-- "show me everything this person touched" meant matching strings. It is now
-- app_users(id), which is the relationship the user master already implies.
--
-- **The database enforces it, not the application.** A BEFORE trigger stamps
-- created_at and changed_at from the server clock on every write and puts back
-- the original created_by and created_at on every update. That is a deliberate
-- choice about where to put the rule. An application-layer convention holds
-- until somebody writes a repository that forgets it, or runs an UPDATE by
-- hand at three in the morning to fix a stuck posting; a trigger holds for
-- both. It also means the guarantee survives a second application — a report
-- writer, a data fix, an integration — that never read this file.
--
-- One table is exempt, and the exemption is named rather than left to be
-- discovered: schema_migrations. It records which migration has run, including
-- this one, and it has to be writable before app_users exists at all — the
-- first migration it records is the one that creates the user table. A
-- foreign key from the migration ledger to a table three migrations later
-- cannot be satisfied. It carries applied_at, which is the same information.

-- ---------------------------------------------------------------------------
-- 1. The SYSTEM account
-- ---------------------------------------------------------------------------
-- created_by is NOT NULL and references a user, but rows do get written with
-- no human behind them: seed data, migrations, the nightly job that has not
-- been written yet. Those actions have an actor, and pretending otherwise
-- means either a nullable column that is null on a third of the table or an
-- invented id that points at nothing.
--
-- So SYSTEM is a real account with a real row. It cannot be signed in to —
-- password_hash is null and the status is INACTIVE — but it can be joined to,
-- and a screen showing "Created by SYSTEM" is telling the truth.
INSERT INTO app_users (username, display_name, status, roles, remark)
VALUES ('system', 'System', 'INACTIVE', '{}',
        'Audit actor for rows written by migrations, seeds and scheduled work. '
        || 'Cannot sign in: no password hash, and the status is INACTIVE.')
ON CONFLICT (username) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 2. updated_* becomes changed_*
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOR t IN
        SELECT table_name FROM information_schema.columns
        WHERE table_schema = 'public' AND column_name = 'updated_at'
    LOOP
        EXECUTE format('ALTER TABLE %I RENAME COLUMN updated_at TO changed_at', t);
    END LOOP;

    FOR t IN
        SELECT table_name FROM information_schema.columns
        WHERE table_schema = 'public' AND column_name = 'updated_by'
    LOOP
        EXECUTE format('ALTER TABLE %I RENAME COLUMN updated_by TO changed_by', t);
    END LOOP;
END $$;

-- ---------------------------------------------------------------------------
-- 3. created_by / changed_by become user ids
-- ---------------------------------------------------------------------------
-- The existing values are usernames, plus a handful of names that were never
-- users at all: 'SYSTEM' from the column default, 'seed-demo' from the seeder,
-- 'integration-test' from the tests. A name that matches an account maps to
-- it; everything else maps to SYSTEM, which is what those actions were.
--
-- The old text is not thrown away. created_by_legacy keeps it, because it is
-- the only record of *which* system actor wrote a row — that a figure came
-- from the seeder rather than from a migration is worth knowing when the
-- number turns out to be wrong, and no foreign key can carry it.
DO $$
DECLARE
    t TEXT;
    system_id BIGINT;
BEGIN
    SELECT id INTO STRICT system_id FROM app_users WHERE username = 'system';

    FOR t IN
        SELECT c.table_name FROM information_schema.columns c
        WHERE c.table_schema = 'public' AND c.column_name = 'created_by'
          AND c.data_type = 'text'
    LOOP
        EXECUTE format('ALTER TABLE %I RENAME COLUMN created_by TO created_by_legacy', t);
        EXECUTE format('ALTER TABLE %I RENAME COLUMN changed_by TO changed_by_legacy', t);

        EXECUTE format('ALTER TABLE %I ADD COLUMN created_by BIGINT', t);
        EXECUTE format('ALTER TABLE %I ADD COLUMN changed_by BIGINT', t);

        EXECUTE format($f$
            UPDATE %I SET
              created_by = COALESCE((SELECT u.id FROM app_users u
                                     WHERE u.username = lower(created_by_legacy)), %s),
              changed_by = COALESCE((SELECT u.id FROM app_users u
                                     WHERE u.username = lower(changed_by_legacy)), %s)
        $f$, t, system_id, system_id);

        EXECUTE format('ALTER TABLE %I ALTER COLUMN created_by SET NOT NULL', t);
        EXECUTE format('ALTER TABLE %I ALTER COLUMN changed_by SET NOT NULL', t);
        EXECUTE format('ALTER TABLE %I ALTER COLUMN created_by SET DEFAULT %s', t, system_id);
        EXECUTE format('ALTER TABLE %I ALTER COLUMN changed_by SET DEFAULT %s', t, system_id);
    END LOOP;
END $$;

-- ---------------------------------------------------------------------------
-- 4. The tables that had none
-- ---------------------------------------------------------------------------
-- storage_group_members is a join table, and join tables are usually where a
-- standard like this is quietly skipped. It is skipped exactly where it is
-- most useful: "who added Warehouse 2 to the finished sugar pool, and when"
-- is a question about capacity that somebody will ask the first time a
-- projection looks wrong.
--
-- user_sessions is written by the sign-in path rather than by a person, so
-- created_by is the account that signed in — which is also user_id. Recording
-- it twice is not redundant: user_id is who the session is *for*, created_by
-- is who *made* it, and an administrator impersonating a user would make those
-- differ. Nothing does that today; the column is there so that when something
-- does, the record already distinguishes them.
DO $$
DECLARE
    t TEXT;
    system_id BIGINT;
BEGIN
    SELECT id INTO STRICT system_id FROM app_users WHERE username = 'system';

    FOREACH t IN ARRAY ARRAY['storage_group_members', 'user_sessions'] LOOP
        EXECUTE format($f$
            ALTER TABLE %I
              ADD COLUMN created_by BIGINT      NOT NULL DEFAULT %s,
              ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
              ADD COLUMN changed_by BIGINT      NOT NULL DEFAULT %s,
              ADD COLUMN changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
        $f$, t, system_id, system_id);
    END LOOP;
END $$;

-- user_sessions rows are made by the account signing in, not by SYSTEM.
UPDATE user_sessions SET created_by = user_id, changed_by = user_id;

-- ---------------------------------------------------------------------------
-- 5. Foreign keys to the user master
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOR t IN
        SELECT table_name FROM information_schema.columns
        WHERE table_schema = 'public' AND column_name = 'created_by'
    LOOP
        -- ON DELETE RESTRICT, not CASCADE and not SET NULL. Deleting a user
        -- must not delete the rows they created, and must not quietly detach
        -- their name from them either. An account that has touched anything
        -- is deactivated rather than removed, and this is the constraint that
        -- makes that the only option.
        EXECUTE format($f$
            ALTER TABLE %I
              ADD CONSTRAINT fk_%s_created_by FOREIGN KEY (created_by)
                  REFERENCES app_users (id) ON DELETE RESTRICT,
              ADD CONSTRAINT fk_%s_changed_by FOREIGN KEY (changed_by)
                  REFERENCES app_users (id) ON DELETE RESTRICT
        $f$, t, t, t);

        EXECUTE format('CREATE INDEX %I ON %I (created_by)', t || '_created_by_idx', t);
        EXECUTE format('CREATE INDEX %I ON %I (changed_by)', t || '_changed_by_idx', t);
    END LOOP;
END $$;

-- ---------------------------------------------------------------------------
-- 6. The trigger that makes it true rather than intended
-- ---------------------------------------------------------------------------
-- On INSERT: both timestamps come from the transaction clock, whatever the
-- caller supplied. On UPDATE: changed_at is refreshed, and created_by and
-- created_at are put back to what they were — so an update cannot rewrite who
-- created a record, whether it comes from the application, from a report
-- writer, or from a hand-typed UPDATE.
--
-- now() is the start of the transaction, not the instant of the statement.
-- That is the right clock here: two rows written by one posting share a
-- timestamp, which is what somebody reading the audit trail expects of one
-- action.
CREATE OR REPLACE FUNCTION audit_stamp() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        NEW.created_at := now();
        NEW.changed_at := now();
    ELSE
        NEW.created_by := OLD.created_by;
        NEW.created_at := OLD.created_at;
        NEW.changed_at := now();
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION audit_stamp() IS
    'Stamps created_at/changed_at from the database clock and protects '
    'created_by/created_at from being rewritten by an update. Attached to '
    'every table carrying the four audit columns.';

DO $$
DECLARE
    t TEXT;
BEGIN
    FOR t IN
        SELECT table_name FROM information_schema.columns
        WHERE table_schema = 'public' AND column_name = 'changed_at'
    LOOP
        EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I', t || '_audit_stamp', t);
        EXECUTE format(
            'CREATE TRIGGER %I BEFORE INSERT OR UPDATE ON %I '
            'FOR EACH ROW EXECUTE FUNCTION audit_stamp()', t || '_audit_stamp', t);
    END LOOP;
END $$;

-- ---------------------------------------------------------------------------
-- 7. The business timezone
-- ---------------------------------------------------------------------------
-- Audit timestamps are TIMESTAMPTZ, so they are unambiguous instants and the
-- database stores no timezone of its own. But "which day did that happen on"
-- has to be answered somewhere, and answering it in the reader's browser means
-- a shift posted at 23:49 in Kampong Speu lands on the previous day for anyone
-- reading from Europe.
--
-- So the factory's timezone is configuration, not a client guess. One row,
-- read by the API and handed to the front end.
CREATE TABLE system_parameters (
    id          BIGSERIAL PRIMARY KEY,
    param_key   TEXT NOT NULL UNIQUE,
    param_value TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',

    created_by  BIGINT      NOT NULL REFERENCES app_users (id) ON DELETE RESTRICT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    changed_by  BIGINT      NOT NULL REFERENCES app_users (id) ON DELETE RESTRICT,
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    version     INTEGER     NOT NULL DEFAULT 1
);

CREATE INDEX system_parameters_created_by_idx ON system_parameters (created_by);
CREATE INDEX system_parameters_changed_by_idx ON system_parameters (changed_by);

CREATE TRIGGER system_parameters_audit_stamp
    BEFORE INSERT OR UPDATE ON system_parameters
    FOR EACH ROW EXECUTE FUNCTION audit_stamp();

INSERT INTO system_parameters (param_key, param_value, description, created_by, changed_by)
SELECT 'BUSINESS_TIMEZONE', 'Asia/Phnom_Penh',
       'IANA timezone the factory works in. Audit timestamps are stored as '
       || 'instants and rendered in this zone, so a posting belongs to the '
       || 'day it happened on at the site rather than in the reader''s browser.',
       u.id, u.id
FROM app_users u WHERE u.username = 'system';

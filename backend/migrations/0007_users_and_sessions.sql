-- 0007_users_and_sessions.sql
-- Application users, their roles, and the server-side sessions a login issues.
--
-- Until now the system had a pluggable identity (see internal/auth) but no way
-- of establishing one on its own: mode "none" trusted the caller and mode
-- "proxy" trusted an upstream. This adds mode "local", so the application can
-- be tested and demonstrated with real logins and real role separation without
-- standing up an identity provider first.
--
-- No user is created here. Seeding accounts from a migration would give every
-- deployment the same known passwords; accounts come from cmd/seed-users,
-- which has to be run deliberately.

BEGIN;

-- ---------------------------------------------------------------------------
-- Users
-- ---------------------------------------------------------------------------
-- password_hash holds a bcrypt digest, never a password and never a reversible
-- encoding of one. It is nullable so an account can exist with no way to log
-- in — that is how an administrator disables sign-in without deleting the user
-- and losing the audit trail that points at them.
CREATE TABLE app_users (
    id                  BIGSERIAL PRIMARY KEY,
    -- The subject recorded in the audit trail. Stored lower-case so
    -- "Sovanna" and "sovanna" cannot become two accounts.
    username            TEXT        NOT NULL UNIQUE
                                    CHECK (username = lower(username) AND length(username) BETWEEN 3 AND 64),
    display_name        TEXT        NOT NULL DEFAULT '',
    email               TEXT        NOT NULL DEFAULT '',
    password_hash       TEXT,

    -- Roles are an array rather than a join table because they are a short,
    -- closed list checked on every request; a join would buy nothing.
    roles               TEXT[]      NOT NULL DEFAULT '{}',

    status              TEXT        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),

    -- Set when a password was issued by an administrator rather than chosen by
    -- the user. The user may sign in, but the UI asks them to change it.
    must_change_password BOOLEAN    NOT NULL DEFAULT FALSE,

    -- Marks an account created by cmd/seed-users -demo, whose password is
    -- written down in the documentation. The server counts these on start and
    -- says so, loudly, so a demonstration database cannot quietly become a
    -- production one with the passwords still public.
    is_demo             BOOLEAN     NOT NULL DEFAULT FALSE,

    -- Throttling state. locked_until is advisory: it delays guessing, it does
    -- not lock an account permanently, so a mistyped password cannot be used
    -- to deny someone access to their own account for long.
    failed_attempts     INTEGER     NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    locked_until        TIMESTAMPTZ,
    last_login_at       TIMESTAMPTZ,
    password_changed_at TIMESTAMPTZ,

    remark              TEXT        NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by          TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by          TEXT        NOT NULL DEFAULT 'SYSTEM',
    version             INTEGER     NOT NULL DEFAULT 1
);

-- Email is optional, but where it is given it identifies one account.
CREATE UNIQUE INDEX app_users_email_key ON app_users (lower(email)) WHERE email <> '';

-- ---------------------------------------------------------------------------
-- Sessions
-- ---------------------------------------------------------------------------
-- Only the SHA-256 of the session token is stored. A leaked database therefore
-- yields no usable session, the same reasoning that keeps the password itself
-- out of app_users.
--
-- Sessions are server-side rather than a signed cookie so that "log out
-- everywhere" and "disable this account now" take effect immediately instead
-- of at the next expiry.
CREATE TABLE user_sessions (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT      NOT NULL REFERENCES app_users (id) ON DELETE CASCADE,
    token_hash      BYTEA       NOT NULL UNIQUE,
    issued_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at      TIMESTAMPTZ,
    -- Recorded for the "where am I signed in" list and for incident review.
    -- Both are attacker-supplied strings; they are never interpreted.
    user_agent      TEXT        NOT NULL DEFAULT '',
    client_ip       TEXT        NOT NULL DEFAULT '',
    CHECK (expires_at > issued_at)
);

CREATE INDEX user_sessions_user_idx ON user_sessions (user_id);
CREATE INDEX user_sessions_expiry_idx ON user_sessions (expires_at) WHERE revoked_at IS NULL;

COMMIT;

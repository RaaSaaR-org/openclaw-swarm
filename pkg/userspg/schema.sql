-- pkg/userspg schema, v1. Apply with `userspg.Migrate(ctx, pool)`.
--
-- Single users table per PROP-001. The lower(email) index makes login
-- lookups O(log n); the unique constraint on lower(email) prevents duplicate
-- accounts via casing tricks. Soft-delete keeps a 30-day GDPR grace window —
-- the actual hard-delete cron lives in TASK-021 and reads `deleted_at`.

CREATE TABLE IF NOT EXISTS users (
    id                  TEXT        PRIMARY KEY,                 -- u_<ulid>
    email               TEXT        NOT NULL,                    -- canonicalized lower-case
    password_hash       TEXT        NOT NULL,                    -- argon2id PHC string
    tier                TEXT        NOT NULL DEFAULT 'free',
    stripe_customer_id  TEXT,
    language            TEXT        NOT NULL DEFAULT 'de',
    app                 TEXT        NOT NULL DEFAULT 'personal-assistant',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    email_verified_at   TIMESTAMPTZ,
    deleted_at          TIMESTAMPTZ,
    last_login_at       TIMESTAMPTZ
);

-- Idempotent `app` column add for installations that ran the v1 schema
-- before this column existed. Postgres 9.6+ supports IF NOT EXISTS on
-- ALTER TABLE ADD COLUMN.
ALTER TABLE users ADD COLUMN IF NOT EXISTS app TEXT NOT NULL DEFAULT 'personal-assistant';

-- Idempotent `email_bounced_at` column add (TASK-020 Phase 4 — Resend
-- webhook receiver). Non-nil = provider reported the address as bounced
-- or complained; future sends should skip these users to protect sender
-- reputation. Nullable + no default so existing rows aren't touched.
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_bounced_at TIMESTAMPTZ;

-- deletion_audit (TASK-021 Phase 5): records every soft-delete + the
-- subsequent hard-purge timestamp without storing PII. Lets us answer
-- "did we delete user X?" — required for GDPR legal audits — without
-- holding onto the user ID itself.
--
-- The id_hash is SHA-256 of the original user ID. The original ID is
-- gone after PurgeDeletedBefore runs; the hash survives forever.
CREATE TABLE IF NOT EXISTS deletion_audit (
    id_hash     TEXT        PRIMARY KEY,
    deleted_at  TIMESTAMPTZ NOT NULL,
    purged_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS deletion_audit_deleted_at_idx ON deletion_audit (deleted_at);

-- Unique on lower(email) for active rows only — soft-deleted users free up
-- their email address so a new signup can reuse it during the grace window.
CREATE UNIQUE INDEX IF NOT EXISTS users_email_active_idx
    ON users (lower(email))
    WHERE deleted_at IS NULL;

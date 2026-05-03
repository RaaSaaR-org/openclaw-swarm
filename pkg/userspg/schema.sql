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
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    email_verified_at   TIMESTAMPTZ,
    deleted_at          TIMESTAMPTZ,
    last_login_at       TIMESTAMPTZ
);

-- Unique on lower(email) for active rows only — soft-deleted users free up
-- their email address so a new signup can reuse it during the grace window.
CREATE UNIQUE INDEX IF NOT EXISTS users_email_active_idx
    ON users (lower(email))
    WHERE deleted_at IS NULL;

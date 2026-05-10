// Package userspg implements users.Store on top of Postgres via pgx/v5.
// Split out of pkg/users so the core types + MemoryStore stay dep-free —
// callers that only need the interface (or that mock against it in tests)
// don't pull pgx into their build graph.
package userspg

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emai-ai/swarm/pkg/users"
)

//go:embed schema.sql
var schemaFS embed.FS

// PoolStore satisfies users.Store. The caller owns the pgxpool.Pool — we
// don't open or close it. Same pattern as the K8s clients in pkg/authk8s:
// dependency-injected so tests can swap in fake / pooled / real connections.
type PoolStore struct {
	Pool *pgxpool.Pool
	Now  func() time.Time // injected for tests; defaults to time.Now
}

// Compile-time assertion the API contract is satisfied.
var _ users.Store = (*PoolStore)(nil)

// New constructs a PoolStore. Empty pool is rejected so callers fail at
// startup rather than at first query.
func New(pool *pgxpool.Pool) (*PoolStore, error) {
	if pool == nil {
		return nil, errors.New("userspg: nil pool")
	}
	return &PoolStore{Pool: pool}, nil
}

func (s *PoolStore) clock() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Migrate applies the embedded schema.sql against the pool. Idempotent —
// uses CREATE TABLE/INDEX IF NOT EXISTS so re-running is safe. For real
// migrations (alter column, drop index) layer a proper migration tool on
// top; this is the v1 bootstrap.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("userspg: nil pool")
	}
	body, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("read embedded schema: %w", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

func (s *PoolStore) Create(ctx context.Context, p users.CreateParams) (*users.User, error) {
	email := users.NormalizeEmail(p.Email)
	if email == "" {
		return nil, users.ErrInvalidEmail
	}
	if p.PasswordHash == "" {
		return nil, users.ErrEmptyHash
	}
	if !users.ValidTier(p.Tier) {
		return nil, users.ErrInvalidTier
	}
	if !users.ValidLang(p.Language) {
		return nil, users.ErrInvalidLang
	}
	app := p.App
	if app == "" {
		app = users.DefaultApp
	}
	if !users.ValidApp(app) {
		return nil, users.ErrInvalidApp
	}
	id, err := users.NewID()
	if err != nil {
		return nil, err
	}
	now := s.clock().UTC()
	const q = `
		INSERT INTO users (id, email, password_hash, tier, language, app, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, email, password_hash, tier, COALESCE(stripe_customer_id, ''),
		          language, app, created_at, email_verified_at, email_bounced_at, deleted_at, last_login_at`
	row := s.Pool.QueryRow(ctx, q, id, email, p.PasswordHash, string(p.Tier), string(p.Language), app, now)
	u, err := scanUser(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return nil, users.ErrEmailTaken
		}
		return nil, err
	}
	return u, nil
}

func (s *PoolStore) GetByID(ctx context.Context, id string) (*users.User, error) {
	const q = `
		SELECT id, email, password_hash, tier, COALESCE(stripe_customer_id, ''),
		       language, app, created_at, email_verified_at, email_bounced_at, deleted_at, last_login_at
		FROM users
		WHERE id = $1 AND deleted_at IS NULL`
	row := s.Pool.QueryRow(ctx, q, id)
	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, users.ErrNotFound
	}
	return u, err
}

func (s *PoolStore) GetByEmail(ctx context.Context, email string) (*users.User, error) {
	email = users.NormalizeEmail(email)
	const q = `
		SELECT id, email, password_hash, tier, COALESCE(stripe_customer_id, ''),
		       language, app, created_at, email_verified_at, email_bounced_at, deleted_at, last_login_at
		FROM users
		WHERE lower(email) = $1 AND deleted_at IS NULL`
	row := s.Pool.QueryRow(ctx, q, email)
	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, users.ErrNotFound
	}
	return u, err
}

func (s *PoolStore) UpdateTier(ctx context.Context, id string, tier users.Tier) error {
	if !users.ValidTier(tier) {
		return users.ErrInvalidTier
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE users SET tier = $1 WHERE id = $2 AND deleted_at IS NULL`, string(tier), id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return users.ErrNotFound
	}
	return nil
}

func (s *PoolStore) UpdateStripeCustomerID(ctx context.Context, id, stripeID string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE users SET stripe_customer_id = $1 WHERE id = $2 AND deleted_at IS NULL`, stripeID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return users.ErrNotFound
	}
	return nil
}

func (s *PoolStore) MarkEmailVerified(ctx context.Context, id string, at time.Time) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE users SET email_verified_at = $1 WHERE id = $2 AND deleted_at IS NULL`, at.UTC(), id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return users.ErrNotFound
	}
	return nil
}

// MarkEmailBounced records the most recent provider-reported bounce or
// complaint. Unlike MarkEmailVerified, this WHERE clause omits the
// `deleted_at IS NULL` guard — bouncing during the GDPR grace window is
// still useful info for ops; we shouldn't silently ignore the webhook.
func (s *PoolStore) MarkEmailBounced(ctx context.Context, id string, at time.Time) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE users SET email_bounced_at = $1 WHERE id = $2`, at.UTC(), id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return users.ErrNotFound
	}
	return nil
}

func (s *PoolStore) RecordLogin(ctx context.Context, id string, at time.Time) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE users SET last_login_at = $1 WHERE id = $2 AND deleted_at IS NULL`, at.UTC(), id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return users.ErrNotFound
	}
	return nil
}

func (s *PoolStore) SoftDelete(ctx context.Context, id string, at time.Time) error {
	// Two-step: confirm the row exists at all (for ErrNotFound vs
	// ErrAlreadyDeleted distinction), then conditionally update.
	var exists bool
	var alreadyDeleted bool
	row := s.Pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM users WHERE id = $1`, id)
	switch err := row.Scan(&alreadyDeleted); {
	case errors.Is(err, pgx.ErrNoRows):
		return users.ErrNotFound
	case err != nil:
		return err
	}
	exists = true
	if alreadyDeleted {
		return users.ErrAlreadyDeleted
	}
	if !exists {
		return users.ErrNotFound
	}
	// Audit log (TASK-021 Phase 5): record the deletion timestamp keyed
	// by sha256(id) so we can answer "did we delete user X?" later
	// without storing the user ID. Same transaction as the UPDATE so the
	// audit row and the soft-delete are atomic — never one without the
	// other. ON CONFLICT covers the soft-delete-after-purge re-creation
	// edge case (user signs up again with a fresh ID that hashes the same
	// — astronomically unlikely but defended against).
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE users SET deleted_at = $1 WHERE id = $2 AND deleted_at IS NULL`, at.UTC(), id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO deletion_audit (id_hash, deleted_at) VALUES ($1, $2)
		 ON CONFLICT (id_hash) DO UPDATE SET deleted_at = EXCLUDED.deleted_at`,
		hashUserID(id), at.UTC(),
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PurgeDeletedBefore hard-deletes every soft-deleted user older than the
// cutoff. The GDPR cron (TASK-021 Phase 2) calls this with
// `time.Now().Add(-users.GracePeriod)` once a day. Atomically updates the
// audit log's `purged_at` for the affected rows so the audit row survives
// the hard-delete and a future "when did we purge user X?" query can be
// answered.
func (s *PoolStore) PurgeDeletedBefore(ctx context.Context, before time.Time) (int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	// Capture the IDs about to be purged so we can hash them for the
	// audit-log update. After DELETE the IDs are gone.
	rows, err := tx.Query(ctx, `SELECT id FROM users WHERE deleted_at IS NOT NULL AND deleted_at < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, tx.Commit(ctx)
	}
	now := time.Now().UTC()
	for _, id := range ids {
		if _, err := tx.Exec(ctx,
			`UPDATE deletion_audit SET purged_at = $1 WHERE id_hash = $2`,
			now, hashUserID(id),
		); err != nil {
			return 0, err
		}
	}
	tag, err := tx.Exec(ctx, `DELETE FROM users WHERE deleted_at IS NOT NULL AND deleted_at < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// DeletionAudit is the audit-log shape returned by LookupDeletion. PurgedAt
// is nil when the row has been soft-deleted but not yet hard-purged (still
// within the grace window).
type DeletionAudit struct {
	IDHash    string
	DeletedAt time.Time
	PurgedAt  *time.Time
}

// LookupDeletion answers "was this user ID ever deleted?" without holding
// onto the user ID itself in the audit table. Pass the original `u_<ulid>`
// — we hash inside. Returns nil + nil when no record exists. Used by legal
// audits ("court asks: did you delete user X on date Y?").
func (s *PoolStore) LookupDeletion(ctx context.Context, userID string) (*DeletionAudit, error) {
	const q = `SELECT id_hash, deleted_at, purged_at FROM deletion_audit WHERE id_hash = $1`
	var a DeletionAudit
	err := s.Pool.QueryRow(ctx, q, hashUserID(userID)).Scan(&a.IDHash, &a.DeletedAt, &a.PurgedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// hashUserID returns the SHA-256 hex digest of a user ID. Stable across
// process restarts (no salt) — the audit-log lookup needs to find the same
// hash for the same ID later, so a salt would defeat the purpose. The
// information leak is "the audit log can be enumerated by anyone with a
// candidate user ID", which is the trade-off for being able to answer
// legal-audit queries at all.
func hashUserID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

// scanUser knows the exact column order returned by every Get/Insert query
// in this package — keep the SELECT lists in the queries above in sync.
func scanUser(row pgx.Row) (*users.User, error) {
	var u users.User
	var stripeID string
	var tier, lang, app string
	var verifiedAt, bouncedAt, deletedAt, lastLoginAt *time.Time
	err := row.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &tier, &stripeID,
		&lang, &app, &u.CreatedAt, &verifiedAt, &bouncedAt, &deletedAt, &lastLoginAt,
	)
	if err != nil {
		return nil, err
	}
	u.Tier = users.Tier(tier)
	u.Language = users.Lang(lang)
	u.App = app
	u.StripeCustomerID = stripeID
	u.EmailVerifiedAt = verifiedAt
	u.EmailBouncedAt = bouncedAt
	u.DeletedAt = deletedAt
	u.LastLoginAt = lastLoginAt
	return &u, nil
}

// Package userspg implements users.Store on top of Postgres via pgx/v5.
// Split out of pkg/users so the core types + MemoryStore stay dep-free —
// callers that only need the interface (or that mock against it in tests)
// don't pull pgx into their build graph.
package userspg

import (
	"context"
	"embed"
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
		          language, app, created_at, email_verified_at, deleted_at, last_login_at`
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
		       language, app, created_at, email_verified_at, deleted_at, last_login_at
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
		       language, app, created_at, email_verified_at, deleted_at, last_login_at
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
	_, err := s.Pool.Exec(ctx, `UPDATE users SET deleted_at = $1 WHERE id = $2 AND deleted_at IS NULL`, at.UTC(), id)
	return err
}

// PurgeDeletedBefore hard-deletes every soft-deleted user older than the
// cutoff. The GDPR cron (TASK-021 Phase 2) calls this with
// `time.Now().Add(-users.GracePeriod)` once a day.
func (s *PoolStore) PurgeDeletedBefore(ctx context.Context, before time.Time) (int, error) {
	tag, err := s.Pool.Exec(ctx,
		`DELETE FROM users WHERE deleted_at IS NOT NULL AND deleted_at < $1`,
		before.UTC(),
	)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// scanUser knows the exact column order returned by every Get/Insert query
// in this package — keep the SELECT lists in the queries above in sync.
func scanUser(row pgx.Row) (*users.User, error) {
	var u users.User
	var stripeID string
	var tier, lang, app string
	var verifiedAt, deletedAt, lastLoginAt *time.Time
	err := row.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &tier, &stripeID,
		&lang, &app, &u.CreatedAt, &verifiedAt, &deletedAt, &lastLoginAt,
	)
	if err != nil {
		return nil, err
	}
	u.Tier = users.Tier(tier)
	u.Language = users.Lang(lang)
	u.App = app
	u.StripeCustomerID = stripeID
	u.EmailVerifiedAt = verifiedAt
	u.DeletedAt = deletedAt
	u.LastLoginAt = lastLoginAt
	return &u, nil
}

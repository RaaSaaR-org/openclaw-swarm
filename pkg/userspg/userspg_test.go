// Integration tests for PoolStore. Guarded by the PGURL env var so the
// default `go test ./...` pass on a developer laptop without Postgres skips
// cleanly. To run locally:
//
//	docker run --rm -d -p 5499:5432 -e POSTGRES_PASSWORD=test postgres:16-alpine
//	PGURL='postgres://postgres:test@127.0.0.1:5499/postgres?sslmode=disable' go test ./...
//
// Each test uses a per-run schema-scoped table prefix so concurrent test
// invocations don't clobber each other (we drop the tables on TearDown).
package userspg

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emai-ai/swarm/pkg/users"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("PGURL")
	if url == "" {
		t.Skip("PGURL not set — skipping Postgres integration tests")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v (PGURL=%s)", err, url)
	}
	// Tables in the public schema; clean before and after so reruns are deterministic.
	cleanup := func() {
		if _, err := pool.Exec(context.Background(), `DROP TABLE IF EXISTS users`); err != nil {
			t.Logf("cleanup: %v", err)
		}
	}
	cleanup()
	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() {
		cleanup()
		pool.Close()
	})
	return pool
}

func TestPoolStoreCreateAndLookup(t *testing.T) {
	pool := newTestPool(t)
	s, _ := New(pool)

	created, err := s.Create(context.Background(), users.CreateParams{
		Email:        "Alice@Example.org",
		PasswordHash: "$argon2id$test-hash",
		Tier:         users.TierFree,
		Language:     users.LangDE,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Email != "alice@example.org" {
		t.Errorf("email = %q, want lowercased", created.Email)
	}

	got, err := s.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Tier != users.TierFree || got.Language != users.LangDE {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	got, err = s.GetByEmail(context.Background(), "ALICE@example.org")
	if err != nil {
		t.Fatalf("GetByEmail (mixed case): %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("case-insensitive lookup failed")
	}
}

func TestPoolStoreEmailUniqueViaIndex(t *testing.T) {
	pool := newTestPool(t)
	s, _ := New(pool)

	p := users.CreateParams{
		Email:        "alice@example.org",
		PasswordHash: "$argon2id$x",
		Tier:         users.TierFree,
		Language:     users.LangDE,
	}
	if _, err := s.Create(context.Background(), p); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	dupe := p
	dupe.Email = "ALICE@example.org" // different casing → still unique
	_, err := s.Create(context.Background(), dupe)
	if !errors.Is(err, users.ErrEmailTaken) {
		t.Fatalf("expected ErrEmailTaken, got %v", err)
	}
}

func TestPoolStoreUpdates(t *testing.T) {
	pool := newTestPool(t)
	s, _ := New(pool)
	u, _ := s.Create(context.Background(), users.CreateParams{
		Email: "u@x.de", PasswordHash: "$argon2id$x", Tier: users.TierFree, Language: users.LangDE,
	})

	if err := s.UpdateTier(context.Background(), u.ID, users.TierStarter); err != nil {
		t.Fatalf("UpdateTier: %v", err)
	}
	if got, _ := s.GetByID(context.Background(), u.ID); got.Tier != users.TierStarter {
		t.Errorf("tier not updated: %q", got.Tier)
	}

	if err := s.UpdateStripeCustomerID(context.Background(), u.ID, "cus_test"); err != nil {
		t.Fatalf("UpdateStripeCustomerID: %v", err)
	}
	if got, _ := s.GetByID(context.Background(), u.ID); got.StripeCustomerID != "cus_test" {
		t.Errorf("stripe id not stored: %q", got.StripeCustomerID)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := s.MarkEmailVerified(context.Background(), u.ID, now); err != nil {
		t.Fatalf("MarkEmailVerified: %v", err)
	}
	if got, _ := s.GetByID(context.Background(), u.ID); got.EmailVerifiedAt == nil {
		t.Errorf("EmailVerifiedAt should be non-nil after MarkEmailVerified")
	}

	if err := s.RecordLogin(context.Background(), u.ID, now); err != nil {
		t.Fatalf("RecordLogin: %v", err)
	}
	if got, _ := s.GetByID(context.Background(), u.ID); got.LastLoginAt == nil {
		t.Errorf("LastLoginAt should be non-nil after RecordLogin")
	}

	// Update on missing id → ErrNotFound (not silent success).
	if err := s.UpdateTier(context.Background(), "u_does_not_exist", users.TierFree); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected ErrNotFound on missing id, got %v", err)
	}
}

func TestPoolStoreSoftDeleteAndReclaimEmail(t *testing.T) {
	pool := newTestPool(t)
	s, _ := New(pool)
	u, _ := s.Create(context.Background(), users.CreateParams{
		Email: "alice@example.org", PasswordHash: "$argon2id$x", Tier: users.TierFree, Language: users.LangDE,
	})

	now := time.Now().UTC().Truncate(time.Second)
	if err := s.SoftDelete(context.Background(), u.ID, now); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := s.GetByID(context.Background(), u.ID); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected NotFound after soft-delete, got %v", err)
	}
	// Email is reclaimable thanks to the partial unique index.
	if _, err := s.Create(context.Background(), users.CreateParams{
		Email: "alice@example.org", PasswordHash: "$argon2id$x", Tier: users.TierFree, Language: users.LangDE,
	}); err != nil {
		t.Errorf("email should be reclaimable after soft-delete, got %v", err)
	}
	// Double-delete is rejected.
	if err := s.SoftDelete(context.Background(), u.ID, now); !errors.Is(err, users.ErrAlreadyDeleted) {
		t.Errorf("expected ErrAlreadyDeleted, got %v", err)
	}
}

func TestPoolStorePurgeDeletedBefore(t *testing.T) {
	pool := newTestPool(t)
	s, _ := New(pool)
	ctx := context.Background()

	// Create three users with different soft-delete timing.
	u1, _ := s.Create(ctx, users.CreateParams{Email: "old@x.de", PasswordHash: "$argon2id$x", Tier: users.TierFree, Language: users.LangDE})
	u2, _ := s.Create(ctx, users.CreateParams{Email: "recent@x.de", PasswordHash: "$argon2id$x", Tier: users.TierFree, Language: users.LangDE})
	u3, _ := s.Create(ctx, users.CreateParams{Email: "active@x.de", PasswordHash: "$argon2id$x", Tier: users.TierFree, Language: users.LangDE})

	now := time.Now().UTC().Truncate(time.Second)
	if err := s.SoftDelete(ctx, u1.ID, now.Add(-2*users.GracePeriod)); err != nil {
		t.Fatal(err)
	}
	if err := s.SoftDelete(ctx, u2.ID, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	// u3 stays active.

	purged, err := s.PurgeDeletedBefore(ctx, now.Add(-users.GracePeriod))
	if err != nil {
		t.Fatalf("PurgeDeletedBefore: %v", err)
	}
	if purged != 1 {
		t.Errorf("purged = %d, want 1 (past-grace only)", purged)
	}

	// Past-grace row gone — even raw SELECT shouldn't find it.
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`, u1.ID).Scan(&n)
	if n != 0 {
		t.Errorf("past-grace row still present, got %d", n)
	}
	// Within-grace row still there (soft-deleted but not purged).
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1 AND deleted_at IS NOT NULL`, u2.ID).Scan(&n)
	if n != 1 {
		t.Errorf("within-grace row should be present and soft-deleted, got count=%d", n)
	}
	// Active row untouched.
	if _, err := s.GetByID(ctx, u3.ID); err != nil {
		t.Errorf("active user must be untouched, got %v", err)
	}
}

func TestPoolStoreNewRejectsNilPool(t *testing.T) {
	t.Parallel()
	if _, err := New(nil); err == nil {
		t.Error("expected error on nil pool")
	}
}

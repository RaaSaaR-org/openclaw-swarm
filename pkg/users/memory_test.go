package users

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func newCreate() CreateParams {
	return CreateParams{
		Email:        "Alice@Example.org",
		PasswordHash: "$argon2id$test-hash",
		Tier:         TierFree,
		Language:     LangDE,
	}
}

func TestMemoryStoreCreateLowercasesEmail(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	u, err := s.Create(context.Background(), newCreate())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.Email != "alice@example.org" {
		t.Errorf("email = %q, want lowercased", u.Email)
	}
	if !strings.HasPrefix(u.ID, IDPrefix) {
		t.Errorf("ID = %q, missing prefix", u.ID)
	}
	if u.Tier != TierFree || u.Language != LangDE {
		t.Errorf("tier/lang round-trip wrong: %+v", u)
	}
	if u.CreatedAt.IsZero() {
		t.Error("CreatedAt not set")
	}
}

func TestMemoryStoreCreateValidations(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	cases := []struct {
		name string
		mut  func(*CreateParams)
		want error
	}{
		{"bad email", func(p *CreateParams) { p.Email = "not-an-email" }, ErrInvalidEmail},
		{"empty hash", func(p *CreateParams) { p.PasswordHash = "" }, ErrEmptyHash},
		{"bad tier", func(p *CreateParams) { p.Tier = "vip" }, ErrInvalidTier},
		{"bad lang", func(p *CreateParams) { p.Language = "fr" }, ErrInvalidLang},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newCreate()
			c.mut(&p)
			_, err := s.Create(context.Background(), p)
			if !errors.Is(err, c.want) {
				t.Errorf("expected %v, got %v", c.want, err)
			}
		})
	}
}

func TestMemoryStoreEmailUniqueAcrossCasing(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	if _, err := s.Create(context.Background(), newCreate()); err != nil {
		t.Fatal(err)
	}
	dupe := newCreate()
	dupe.Email = "ALICE@example.org"
	_, err := s.Create(context.Background(), dupe)
	if !errors.Is(err, ErrEmailTaken) {
		t.Errorf("expected ErrEmailTaken, got %v", err)
	}
}

func TestMemoryStoreLookups(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	created, _ := s.Create(context.Background(), newCreate())

	got, err := s.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != created.ID || got.Email != created.Email {
		t.Errorf("GetByID round-trip mismatch: %+v vs %+v", got, created)
	}

	got, err = s.GetByEmail(context.Background(), "ALICE@example.org")
	if err != nil {
		t.Fatalf("GetByEmail (mixed case): %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("case-insensitive lookup failed")
	}

	if _, err := s.GetByID(context.Background(), "u_nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if _, err := s.GetByEmail(context.Background(), "ghost@example.org"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound on missing email, got %v", err)
	}
}

func TestMemoryStoreUpdates(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	u, _ := s.Create(context.Background(), newCreate())

	if err := s.UpdateTier(context.Background(), u.ID, TierStarter); err != nil {
		t.Fatalf("UpdateTier: %v", err)
	}
	if got, _ := s.GetByID(context.Background(), u.ID); got.Tier != TierStarter {
		t.Errorf("tier after update = %q, want starter", got.Tier)
	}
	if err := s.UpdateTier(context.Background(), u.ID, "vip"); !errors.Is(err, ErrInvalidTier) {
		t.Errorf("expected ErrInvalidTier, got %v", err)
	}

	if err := s.UpdateStripeCustomerID(context.Background(), u.ID, "cus_test"); err != nil {
		t.Fatalf("UpdateStripeCustomerID: %v", err)
	}
	if got, _ := s.GetByID(context.Background(), u.ID); got.StripeCustomerID != "cus_test" {
		t.Errorf("stripe id not stored")
	}

	now := time.Unix(1_700_000_000, 0)
	if err := s.MarkEmailVerified(context.Background(), u.ID, now); err != nil {
		t.Fatalf("MarkEmailVerified: %v", err)
	}
	if got, _ := s.GetByID(context.Background(), u.ID); got.EmailVerifiedAt == nil || !got.EmailVerifiedAt.Equal(now.UTC()) {
		t.Errorf("EmailVerifiedAt not stored: %v", got.EmailVerifiedAt)
	}

	if err := s.RecordLogin(context.Background(), u.ID, now); err != nil {
		t.Fatalf("RecordLogin: %v", err)
	}
	if got, _ := s.GetByID(context.Background(), u.ID); got.LastLoginAt == nil {
		t.Errorf("LastLoginAt not stored")
	}
}

func TestMemoryStoreSoftDelete(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	u, _ := s.Create(context.Background(), newCreate())

	now := time.Unix(1_700_000_000, 0)
	if err := s.SoftDelete(context.Background(), u.ID, now); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	// Subsequent lookups treat the user as gone.
	if _, err := s.GetByID(context.Background(), u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected NotFound after soft-delete, got %v", err)
	}
	if _, err := s.GetByEmail(context.Background(), u.Email); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected NotFound by email after soft-delete, got %v", err)
	}
	// Email is freed up — a new account can claim it during the grace window.
	if _, err := s.Create(context.Background(), newCreate()); err != nil {
		t.Errorf("email should be reclaimable after soft-delete, got %v", err)
	}
	// Double-delete is rejected.
	if err := s.SoftDelete(context.Background(), u.ID, now); !errors.Is(err, ErrAlreadyDeleted) {
		t.Errorf("expected ErrAlreadyDeleted, got %v", err)
	}
}

func TestMemoryStoreCreateAppDefaultAndOverride(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	// Empty App falls back to DefaultApp.
	u, err := s.Create(context.Background(), newCreate())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.App != DefaultApp {
		t.Errorf("default App = %q, want %q", u.App, DefaultApp)
	}

	// Explicit App is honored when valid.
	p := newCreate()
	p.Email = "bob@example.org"
	p.App = "coding-helper"
	u2, err := s.Create(context.Background(), p)
	if err != nil {
		t.Fatalf("Create with App: %v", err)
	}
	if u2.App != "coding-helper" {
		t.Errorf("explicit App = %q, want coding-helper", u2.App)
	}

	// Bad slug is rejected.
	p3 := newCreate()
	p3.Email = "carol@example.org"
	p3.App = "Bad App!"
	_, err = s.Create(context.Background(), p3)
	if !errors.Is(err, ErrInvalidApp) {
		t.Errorf("expected ErrInvalidApp, got %v", err)
	}
}

func TestValidApp(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"a", "personal-assistant", "coding-helper", "x-y-z", "abc123"} {
		if !ValidApp(ok) {
			t.Errorf("%q must be valid", ok)
		}
	}
	for _, bad := range []string{"", "Personal-Assistant", "-leading", "trailing-", "has space", "with_underscore", "Hello"} {
		if ValidApp(bad) {
			t.Errorf("%q must NOT be valid", bad)
		}
	}
	// 64 chars is over the cap.
	if ValidApp(strings.Repeat("a", 64)) {
		t.Error("64-char slug must be rejected")
	}
	// 63 chars is fine.
	if !ValidApp(strings.Repeat("a", 63)) {
		t.Error("63-char slug must be accepted")
	}
}

func TestMemoryStorePurgeDeletedBefore(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()

	// Three users, varying soft-delete ages.
	cutoff := time.Unix(1_700_000_000, 0)
	old := newCreate()
	old.Email = "old@example.org"
	uOld, _ := s.Create(ctx, old)
	_ = s.SoftDelete(ctx, uOld.ID, cutoff.Add(-2*GracePeriod)) // way past grace

	recent := newCreate()
	recent.Email = "recent@example.org"
	uRecent, _ := s.Create(ctx, recent)
	_ = s.SoftDelete(ctx, uRecent.ID, cutoff.Add(-1*time.Hour)) // 1h ago — within grace

	active := newCreate()
	active.Email = "active@example.org"
	uActive, _ := s.Create(ctx, active)
	// Active not soft-deleted at all.

	purged, err := s.PurgeDeletedBefore(ctx, cutoff.Add(-GracePeriod))
	if err != nil {
		t.Fatalf("PurgeDeletedBefore: %v", err)
	}
	if purged != 1 {
		t.Errorf("purged = %d, want 1 (only the past-grace one)", purged)
	}
	// Past-grace row gone.
	if _, err := s.GetByID(ctx, uOld.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("past-grace user must be hard-deleted, got %v", err)
	}
	// Recent (within grace) row still present (soft-deleted but recoverable
	// in principle — GetByID returns ErrNotFound for soft-deleted users
	// already, but the row data is still in the map).
	if _, ok := s.byID[uRecent.ID]; !ok {
		t.Error("within-grace user must NOT be hard-deleted")
	}
	// Active user untouched.
	if _, err := s.GetByID(ctx, uActive.ID); err != nil {
		t.Errorf("active user must be untouched, got %v", err)
	}
}

func TestGracePeriodIs30Days(t *testing.T) {
	t.Parallel()
	if GracePeriod != 30*24*time.Hour {
		t.Errorf("GracePeriod = %v, want 30 days (privacy-page commitment)", GracePeriod)
	}
}

func TestMemoryStoreCloneIsolation(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	u, _ := s.Create(context.Background(), newCreate())

	got1, _ := s.GetByID(context.Background(), u.ID)
	got1.Email = "mutated@example.org"
	got2, _ := s.GetByID(context.Background(), u.ID)
	if got2.Email == "mutated@example.org" {
		t.Error("Store handed out aliased pointer; mutation leaked")
	}
}

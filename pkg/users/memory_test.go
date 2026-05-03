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

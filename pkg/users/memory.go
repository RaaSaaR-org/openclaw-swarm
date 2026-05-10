package users

import (
	"context"
	"net/mail"
	"sort"
	"sync"
	"time"
)

// MemoryStore is a Store backed by a sync.Mutex-guarded map. Used in tests
// (so test packages don't have to spin up Postgres) and in the
// KAI_INSECURE_DEV_AUTH path where there's no real database. Not durable;
// every restart loses everything.
type MemoryStore struct {
	mu     sync.Mutex
	byID   map[string]*User
	now    func() time.Time // injectable for tests; defaults to time.Now
}

// NewMemoryStore returns a ready-to-use in-memory Store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		byID: map[string]*User{},
		now:  time.Now,
	}
}

func (s *MemoryStore) clock() time.Time { return s.now() }

func (s *MemoryStore) Create(_ context.Context, p CreateParams) (*User, error) {
	email := NormalizeEmail(p.Email)
	if !validEmail(email) {
		return nil, ErrInvalidEmail
	}
	if p.PasswordHash == "" {
		return nil, ErrEmptyHash
	}
	if !ValidTier(p.Tier) {
		return nil, ErrInvalidTier
	}
	if !ValidLang(p.Language) {
		return nil, ErrInvalidLang
	}
	app := p.App
	if app == "" {
		app = DefaultApp
	}
	if !ValidApp(app) {
		return nil, ErrInvalidApp
	}
	id, err := NewID()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.byID {
		if u.Email == email && u.DeletedAt == nil {
			return nil, ErrEmailTaken
		}
	}
	u := &User{
		ID:           id,
		Email:        email,
		PasswordHash: p.PasswordHash,
		Tier:         p.Tier,
		Language:     p.Language,
		App:          app,
		CreatedAt:    s.clock().UTC(),
	}
	s.byID[id] = u
	return cloneUser(u), nil
}

func (s *MemoryStore) GetByID(_ context.Context, id string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.byID[id]
	if !ok || u.DeletedAt != nil {
		return nil, ErrNotFound
	}
	return cloneUser(u), nil
}

func (s *MemoryStore) GetByEmail(_ context.Context, email string) (*User, error) {
	email = NormalizeEmail(email)
	s.mu.Lock()
	defer s.mu.Unlock()
	// Stable iteration order so duplicates (which Create prevents anyway)
	// resolve to the lowest ID — keeps tests deterministic.
	ids := make([]string, 0, len(s.byID))
	for id := range s.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		u := s.byID[id]
		if u.Email == email && u.DeletedAt == nil {
			return cloneUser(u), nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) UpdateTier(_ context.Context, id string, tier Tier) error {
	if !ValidTier(tier) {
		return ErrInvalidTier
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.byID[id]
	if !ok || u.DeletedAt != nil {
		return ErrNotFound
	}
	u.Tier = tier
	return nil
}

func (s *MemoryStore) UpdateStripeCustomerID(_ context.Context, id, stripeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.byID[id]
	if !ok || u.DeletedAt != nil {
		return ErrNotFound
	}
	u.StripeCustomerID = stripeID
	return nil
}

func (s *MemoryStore) MarkEmailVerified(_ context.Context, id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.byID[id]
	if !ok || u.DeletedAt != nil {
		return ErrNotFound
	}
	t := at.UTC()
	u.EmailVerifiedAt = &t
	return nil
}

// MarkEmailBounced records the most recent provider-reported bounce or
// complaint for a user. Soft-deleted users are still updated — bouncing
// during the GDPR grace window is information ops still wants to record.
func (s *MemoryStore) MarkEmailBounced(_ context.Context, id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.byID[id]
	if !ok {
		return ErrNotFound
	}
	t := at.UTC()
	u.EmailBouncedAt = &t
	return nil
}

func (s *MemoryStore) RecordLogin(_ context.Context, id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.byID[id]
	if !ok || u.DeletedAt != nil {
		return ErrNotFound
	}
	t := at.UTC()
	u.LastLoginAt = &t
	return nil
}

func (s *MemoryStore) SoftDelete(_ context.Context, id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.byID[id]
	if !ok {
		return ErrNotFound
	}
	if u.DeletedAt != nil {
		return ErrAlreadyDeleted
	}
	t := at.UTC()
	u.DeletedAt = &t
	return nil
}

func (s *MemoryStore) PurgeDeletedBefore(_ context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := before.UTC()
	purged := 0
	for id, u := range s.byID {
		if u.DeletedAt != nil && u.DeletedAt.Before(cutoff) {
			delete(s.byID, id)
			purged++
		}
	}
	return purged, nil
}

// validEmail keeps the rule simple: parseable per RFC 5322 and contains '@'.
// Stricter validation (MX lookup, plus-addressing rules) belongs at the
// signup-form layer, not here.
func validEmail(s string) bool {
	if s == "" || len(s) > 254 {
		return false
	}
	if _, err := mail.ParseAddress(s); err != nil {
		return false
	}
	return true
}

// cloneUser hands out a copy so callers can't mutate the in-memory map.
// Pointer fields are deep-copied so DeletedAt etc. don't alias.
func cloneUser(u *User) *User {
	out := *u
	if u.EmailVerifiedAt != nil {
		t := *u.EmailVerifiedAt
		out.EmailVerifiedAt = &t
	}
	if u.DeletedAt != nil {
		t := *u.DeletedAt
		out.DeletedAt = &t
	}
	if u.LastLoginAt != nil {
		t := *u.LastLoginAt
		out.LastLoginAt = &t
	}
	return &out
}

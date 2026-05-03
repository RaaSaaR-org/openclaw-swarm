// Package users is the user-account data layer for EmAI swarm — the User
// entity that sits above KaiInstance (PROP-001). It exposes a Store interface
// and an in-memory implementation usable in tests + the dev path. The real
// Postgres-backed Store lives in the sibling pkg/userspg/ module so this
// package stays dep-light (no pgx, no driver pull) for callers that only need
// types or want to mock against the interface.
package users

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Tier enumerates the SaaS plans. Names match PROP-002's pricing tiers; the
// strings are durable contract — they land in Postgres `users.tier`, in the
// KaiInstance CRD `spec.tier`, and in webhook payloads, so renames are a
// migration not a refactor.
type Tier string

const (
	TierFree       Tier = "free"
	TierStarter    Tier = "starter"
	TierGrowth     Tier = "growth"
	TierEnterprise Tier = "enterprise"
)

// Lang is the user-preference language code on the User row. Matches
// pkg/email's Lang enum at the string level so callers can hand a User.Language
// straight to the email package without translation.
type Lang string

const (
	LangDE Lang = "de"
	LangEN Lang = "en"
)

// User is the canonical row shape — what gets stored in Postgres and what
// every Store method returns. Keep field order matching the schema in
// pkg/userspg/schema.sql so reviewing diffs is straightforward.
type User struct {
	ID               string     // u_<ulid>
	Email            string     // lower-cased on Create; unique across non-deleted rows
	PasswordHash     string     // argon2id PHC string from pkg/auth.HashPassword
	Tier             Tier
	StripeCustomerID string     // empty until first checkout
	Language         Lang
	App              string     // catalog persona slug picked at signup; default DefaultApp
	CreatedAt        time.Time
	EmailVerifiedAt  *time.Time // nil until verify-email link clicked
	DeletedAt        *time.Time // nil = active; non-nil = within GDPR grace window
	LastLoginAt      *time.Time
}

// CreateParams is the input shape for Store.Create — keep the API surface
// small (no positional args, no exposing ID generation to callers).
type CreateParams struct {
	Email        string
	PasswordHash string
	Tier         Tier
	Language     Lang
	App          string // optional; falls back to DefaultApp when empty
}

// DefaultApp is the persona a brand-new SaaS workspace ships with when the
// signup form didn't carry an `app` field. Matches one of the slugs in
// `agents/catalog/<slug>/`.
const DefaultApp = "personal-assistant"

// Store is what callers depend on. Implementations must be safe for
// concurrent use. Email lookups are case-insensitive (we store lower-cased).
//
// The interface intentionally stops at lifecycle CRUD — token issuance for
// email verification / password reset belongs in a separate package
// (pkg/email + per-flow tokens), and quota enforcement belongs in the operator
// (TASK-019). Keeping Store narrow keeps the Postgres impl boring.
type Store interface {
	Create(ctx context.Context, p CreateParams) (*User, error)
	GetByID(ctx context.Context, id string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	UpdateTier(ctx context.Context, id string, tier Tier) error
	UpdateStripeCustomerID(ctx context.Context, id, stripeID string) error
	MarkEmailVerified(ctx context.Context, id string, at time.Time) error
	RecordLogin(ctx context.Context, id string, at time.Time) error
	SoftDelete(ctx context.Context, id string, at time.Time) error
	// PurgeDeletedBefore hard-deletes every User with `deleted_at` strictly
	// older than `before`. Returns the count purged. The GDPR cron
	// (TASK-021 Phase 2) calls this with `time.Now() - GracePeriod` once a
	// day. Hard-deleted rows are gone — the audit table records that the
	// deletion happened (id hash + timestamp, no PII).
	PurgeDeletedBefore(ctx context.Context, before time.Time) (int, error)
}

// GracePeriod is the soft-delete window: how long a SoftDeleted user can
// reverse course (a future "undelete" UI) before the cron hard-deletes the
// row. 30 days matches the privacy-page commitment ("hard deletion within
// 30 days").
const GracePeriod = 30 * 24 * time.Hour

// Sentinel errors so callers branch on the failure mode without parsing
// strings. Keep this list short — every entry is a contract that
// implementations must honor.
var (
	ErrNotFound      = errors.New("users: not found")
	ErrEmailTaken    = errors.New("users: email already in use")
	ErrInvalidEmail  = errors.New("users: invalid email")
	ErrInvalidTier   = errors.New("users: invalid tier")
	ErrInvalidLang   = errors.New("users: invalid language")
	ErrInvalidApp    = errors.New("users: invalid app slug")
	ErrEmptyHash     = errors.New("users: password hash required")
	ErrAlreadyDeleted = errors.New("users: already soft-deleted")
)

// NormalizeEmail lower-cases and trims; the canonical form for storage and
// lookup. Implementations call this before writing or querying so callers
// don't have to care about casing.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ValidTier reports whether t is one of the four enum values.
func ValidTier(t Tier) bool {
	switch t {
	case TierFree, TierStarter, TierGrowth, TierEnterprise:
		return true
	}
	return false
}

// ValidLang reports whether l is one of the supported preference codes.
func ValidLang(l Lang) bool {
	return l == LangDE || l == LangEN
}

// ValidApp reports whether s is a syntactically-valid catalog app slug:
// DNS-safe, 1-63 chars, lowercase letters / digits / hyphens, starts and
// ends alphanumeric. Empty strings are NOT valid here — callers either
// pass a slug or pass DefaultApp; pkg/users doesn't substitute.
//
// Existence-of-app validation (does `agents/catalog/<slug>` actually exist?)
// stays at the consumer layer — the catalog is shipped by the swarm repo,
// not pkg/users.
func ValidApp(s string) bool {
	if len(s) == 0 || len(s) > 63 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' && i != 0 && i != len(s)-1:
		default:
			return false
		}
	}
	return true
}

# pkg/users

User-account data layer (PROP-001). Tiny interface, in-memory implementation
for tests + dev mode, ULID-shaped IDs.

## API

```go
import "github.com/emai-ai/swarm/pkg/users"

s := users.NewMemoryStore()  // or userspg.New(pool) for production
u, err := s.Create(ctx, users.CreateParams{
    Email:        "alice@example.org",
    PasswordHash: argonPHC,        // from pkg/auth.HashPassword
    Tier:         users.TierFree,
    Language:     users.LangDE,
})
```

| Operation | Notes |
|---|---|
| `Create(p)` | Lower-cases email; rejects duplicates; mints `u_<ulid>` ID; returns the materialised User |
| `GetByID(id)` / `GetByEmail(email)` | Case-insensitive on email; returns `ErrNotFound` for soft-deleted users |
| `UpdateTier(id, tier)` | Whitelisted tiers only |
| `UpdateStripeCustomerID(id, stripe)` | Set after first checkout |
| `MarkEmailVerified(id, t)` / `RecordLogin(id, t)` | Audit timestamps |
| `SoftDelete(id, t)` | Sets `deleted_at`; subsequent lookups return `ErrNotFound`; email is reclaimable so a new signup can use it during the GDPR grace window |
| `PurgeDeletedBefore(before)` | Hard-deletes every soft-deleted user older than the cutoff. Returns the count purged. The GDPR cron (TASK-021 Phase 2) calls this with `time.Now() - users.GracePeriod` once a day. |

## Storage backends

| Module | Use when | Deps |
|---|---|---|
| `pkg/users.NewMemoryStore()` | Tests, `SWARM_INSECURE_DEV_AUTH` | None — pure stdlib |
| `pkg/userspg.New(pgxpool)` | Production | `github.com/jackc/pgx/v5` |

`pkg/users` carries zero external deps so callers that only need types or
mocks against the interface don't pull pgx into the build graph. Same
multi-module pattern as `pkg/auth` + `pkg/authk8s`.

## ID format

Every User ID is `u_<26-char Crockford Base32 ULID>`. ULIDs are
timestamp-prefixed and lexicographically sortable, so `ORDER BY id` in
Postgres gives free time ordering. ID generation is hand-rolled in
`pkg/users/id.go` (zero deps); collision-tested in `id_test.go` over 1000
concurrent mints.

## Schema

`pkg/userspg/schema.sql` is the canonical Postgres DDL — single `users`
table, partial unique index on `lower(email) WHERE deleted_at IS NULL`
(soft-deleted rows free up the email). Apply with `userspg.Migrate(ctx, pool)`
on startup; idempotent via `CREATE ... IF NOT EXISTS`.

## Running integration tests

```sh
docker run --rm -d -p 5499:5432 -e POSTGRES_PASSWORD=test postgres:16-alpine
PGURL='postgres://postgres:test@127.0.0.1:5499/postgres?sslmode=disable' \
  go test ./pkg/userspg/... -v
```

Tests skip cleanly if `PGURL` is unset, so the default `go test ./...` pass
on a developer laptop without Postgres still works.

## What's not in scope

This package does CRUD on the User row. Other concerns live elsewhere:

- **Email verification / password reset tokens** → not yet (TASK-013 + TASK-020).
- **Quota enforcement** → operator (TASK-019).
- **Stripe webhooks** → web/billing (TASK-016) writes back via `UpdateTier` + `UpdateStripeCustomerID`.
- **GDPR hard-delete** → cron (TASK-021 Phase 2) reads `deleted_at` and purges past `users.GracePeriod` (30 days). The primitive (`PurgeDeletedBefore`) is implemented in pkg/users + pkg/userspg; the cron workload itself lives in `swarm-cloud` deploy overlay (it's a CronJob with RBAC scoped to one namespace).
- **Per-user workspace listing** → operator label `swarm.io/user-id=<id>` plus a `kubectl get kaiinstance` selector. Lives at the consumer layer, not in this package.

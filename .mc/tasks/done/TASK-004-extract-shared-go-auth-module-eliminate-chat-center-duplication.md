---
id: TASK-004
aliases:
- TASK-004
title: Extract shared Go auth module (eliminate chat/center duplication)
slug: extract-shared-go-auth-module-eliminate-chat-center-duplication
status: done
priority: 2
owner: ''
projects: []
customers: []
tags:
- refactor
- auth
- web
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---




# Extract shared Go auth module (eliminate chat/center duplication)

## Why
The same JWT issue/verify + argon2id password hashing code lives in **two** server bundles (`web/customer-chat/server/auth.go` and `web/customer-center/server/auth.go`), and the differences between them are accidental rather than intentional. Bug fixes (e.g. TASK-002 demo-secret removal) must be applied twice and stay in sync. As soon as `admin-console` or `onboarding` need user-level auth (rather than just `ADMIN_TOKEN`), it'll be three or four copies. A SaaS product cannot have its auth code forked across services.

## What
- Create a new package at top-level **`pkg/auth/`** — covering: `IssueJWT`, `VerifyJWT`, `HashPassword`, `VerifyPassword`, cookie helpers, user-record load/save against the K8s Secret. (`pkg/` is the agreed home for code shared across the 5 web apps; `web/internal/...` is reserved for code internal to a single web app.)
- Migrate `customer-chat` and `customer-center` to import it. Keep public APIs minimal — no leaking of `*http.Request` into the lib.
- Decide on `go.mod` topology: collapse the 5 modules under `web/*/server/` into one module rooted at the repo (with `pkg/` as a sibling of `web/`), or keep 5 per-server modules and use a `replace` directive into `pkg/`. Single root module is simpler if all 5 servers are released together.
- Add tests in the new package (table-driven, covers expired tokens, wrong slug, wrong password, secret rotation).

## References
- `/Users/heussers/develop/emai/swarm/web/customer-chat/server/auth.go` (~276 lines)
- `/Users/heussers/develop/emai/swarm/web/customer-center/server/auth.go` (~337 lines)
- `/Users/heussers/develop/emai/swarm/web/customer-chat/server/users.go` (Secret reader)
- `/Users/heussers/develop/emai/swarm/web/customer-chat/go.mod`, `customer-center/go.mod`
- `golang.org/x/crypto/argon2` — current hashing lib
- `github.com/golang-jwt/jwt/v5` (or whichever JWT lib is in use)

## Acceptance Criteria
- [ ] Zero duplicated auth functions across `web/*/server/` — verified by grep
- [ ] Both `customer-chat` and `customer-center` build, test, and pass smoke against the new shared module
- [ ] Shared module has its own tests with >80% coverage
- [ ] When TASK-002 (demo-secret removal) lands, the fix is in **one** file

## Notes
Coordinate with TASK-009 (JWT revocation) — if revocation is added later, having one module makes it trivial.

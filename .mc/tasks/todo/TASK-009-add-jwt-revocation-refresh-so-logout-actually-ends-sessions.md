---
id: TASK-009
aliases:
- TASK-009
title: Add JWT revocation/refresh so logout actually ends sessions
slug: add-jwt-revocation-refresh-so-logout-actually-ends-sessions
status: backlog
priority: 3
owner: ''
projects: []
customers: []
tags:
- security
- auth
- saas
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---


# Add JWT revocation/refresh so logout actually ends sessions

## Why
Current auth issues a 24h JWT in cookie `kai-session`. Logout deletes the cookie client-side but the JWT remains valid server-side until expiry — so a stolen JWT (XSS, leaked logs, copied cookie) is good for up to a day. There is no per-customer rotation procedure either: if `kai-<slug>-chat-bridge` JWT secret is compromised, the only fix is "delete the Secret and pray no one is logged in". A SaaS platform needs real session control.

## Decided
- **Option B — server-side revocation list** (locked in 2026-05-03). Add a `jti` claim to JWTs in `pkg/auth.SignJWT` + `IssueSession`. Revoked `jti`s persist in the existing `kai-<slug>-chat-bridge` Secret under a new key `revoked-jtis` (JSON array of `{jti, exp}`). Per-request lookup checks the list; entries TTL out at the original exp. Logout-everywhere = clear the list + bump the JWT secret (atomic). Option A (refresh tokens) deferred — overkill for the v1 surface; Option C (kid-bump) too coarse for multi-user tenants.

## What
- Extend `pkg/auth`:
  - `SignJWT` adds a random `jti` (16 hex chars) to claims.
  - New `Revoke(ctx, jti, exp)` and `IsRevoked(ctx, jti)` against a pluggable store.
  - `Authenticate` calls `IsRevoked` before returning success.
- Default store implementation: K8s Secret backed (`kai-<slug>-chat-bridge.revoked-jtis`). Cleanup runs on every read — drop entries past their `exp`. List bounded in practice (max 1k entries per slug = ~32 KB; sized to fit in a Secret).
- Customer-center + customer-chat:
  - `handleLogout` reads the cookie's `jti`, calls `pkg/auth.Revoke`, then clears the cookie.
  - New endpoint `POST /api/center/{slug}/users/{email}/sessions:revoke` (admin-only) → revoke all of one user's tokens.
  - Document the "log out everyone for tenant X" procedure: bump `kai-<slug>-chat-bridge.jwt-secret` (1-line Secret patch); all old JWTs fail signature check immediately.
- Tests in `pkg/auth/auth_test.go`: `Revoke`, `IsRevoked` happy path, expired-jti cleanup, large-list pruning, `Authenticate` rejects revoked tokens.

## References
- `/Users/heussers/develop/emai/swarm/pkg/auth/auth.go` (TASK-004; `Authenticate`/`IssueSession` are the surface to extend)
- `/Users/heussers/develop/emai/swarm/web/customer-center/server/auth.go` (`handleLogout` adds the revoke call)
- `/Users/heussers/develop/emai/swarm/web/customer-chat/server/auth.go` (mirror)
- Per-customer JWT secret + revoked-jti list both in K8s Secret `kai-<slug>-chat-bridge`
- OWASP JWT cheat sheet: https://cheatsheetseries.owasp.org/cheatsheets/JSON_Web_Token_for_Java_Cheat_Sheet.html
- RFC 7009 (Token revocation): https://datatracker.ietf.org/doc/html/rfc7009

## References
- `/Users/heussers/develop/emai/swarm/web/customer-center/server/auth.go` (lines 139–150 — logout just deletes cookie, no server-side state change)
- `/Users/heussers/develop/emai/swarm/web/customer-chat/server/auth.go` (mirror code)
- Per-customer JWT secret in K8s Secret `kai-<slug>-chat-bridge`
- OWASP JWT cheat sheet: https://cheatsheetseries.owasp.org/cheatsheets/JSON_Web_Token_for_Java_Cheat_Sheet.html
- RFC 7009 (Token revocation): https://datatracker.ietf.org/doc/html/rfc7009

## Acceptance Criteria
- [ ] Logout invalidates the user's session within the chosen TTL window (and is documented as such)
- [ ] Documented procedure for emergency "log out everyone for tenant X" within 5 minutes
- [ ] Tests cover: revoked token rejected, refresh rotation, expired refresh
- [ ] Approach written up in `docs/architecture.md` auth section

## Notes
Coordinate with TASK-004 (shared auth module) — implement once in the shared module, both apps benefit.

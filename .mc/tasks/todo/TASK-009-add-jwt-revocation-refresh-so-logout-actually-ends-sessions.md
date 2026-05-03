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

## What
Pick one of:
- **Option A — short access + refresh tokens:** access token TTL drops to ~15min, long-lived refresh token issued separately, refresh endpoint rotates both. Logout invalidates refresh.
- **Option B — server-side revocation list:** JWT `jti` claim, list of revoked `jti`s in a K8s Secret or in-memory cache (acceptable for single-pod customer-center), TTL-bounded. Logout adds `jti` to list.
- **Option C — bump-on-logout `kid`:** version the JWT secret per customer; logout-everywhere just bumps the version, invalidating all outstanding tokens.

Recommend Option A for SaaS scalability + security; Option C as the simplest first step (no storage required).

Also document the rotation procedure for the `kai-<slug>-chat-bridge` Secret: how to bump it, what gets invalidated, expected user impact.

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

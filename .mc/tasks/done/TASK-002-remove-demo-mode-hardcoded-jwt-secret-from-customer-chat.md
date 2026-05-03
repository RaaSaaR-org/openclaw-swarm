---
id: TASK-002
aliases:
- TASK-002
title: Remove DEMO_MODE hardcoded JWT secret from customer-chat
slug: remove-demo-mode-hardcoded-jwt-secret-from-customer-chat
status: done
priority: 2
owner: ''
projects: []
customers: []
tags:
- security
- auth
- web
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---




# Remove DEMO_MODE hardcoded JWT secret from customer-chat

## Why
`customer-chat/server/auth.go` contains a hardcoded fallback secret `"demo-secret-do-not-use-in-prod"` at **two** call sites. If a `DEMO_MODE`-style code path is ever hit in a real deployment — by accident or via a misconfigured chart — every JWT becomes forgeable by anyone who reads the open-source repo. This is a textbook "demo flag in production" footgun and must not ship to a SaaS platform.

## What
- Audit both occurrences (lines **58** and **158** of `web/customer-chat/server/auth.go`) and the conditions that trigger them.
- Replace with a hard error at startup: if the per-customer JWT secret is missing or empty, refuse to serve auth endpoints (return 503 + log a clear message), do not fall back to a known string.
- Verify no other web server (`customer-center`, `admin-console`, `onboarding`, `status-page`) has the same pattern. `customer-center` duplicates much of this auth code — check it too.
- If a "demo" mode is genuinely needed for local dev, gate it on a loud env var (`KAI_INSECURE_DEV_AUTH=1`), log a banner on every request, and refuse to start if the env looks production (e.g. listening on non-loopback).

## References
- `/Users/heussers/develop/emai/swarm/web/customer-chat/server/auth.go:58` — `s.issueSession(w, slug, email, []byte("demo-secret-do-not-use-in-prod"))`
- `/Users/heussers/develop/emai/swarm/web/customer-chat/server/auth.go:158` — `secret = []byte("demo-secret-do-not-use-in-prod")`
- `/Users/heussers/develop/emai/swarm/web/customer-center/server/auth.go` (duplicate auth logic — verify same pattern not present)
- Per-customer JWT secret: K8s Secret `kai-<slug>-chat-bridge` (created by operator)
- OWASP A02 Cryptographic Failures: https://owasp.org/www-project-top-ten/2021/A02_2021-Cryptographic_Failures/

## Acceptance Criteria
- [ ] No string `demo-secret-do-not-use-in-prod` (or any other hardcoded secret) anywhere in `web/*/server/`
- [ ] Missing/empty JWT secret causes startup failure or 503, never silent fallback
- [ ] `grep -r "demo-secret\|do-not-use\|insecure" web/` returns nothing in non-test code
- [ ] If a dev-mode auth bypass exists, it requires explicit env var and emits a per-request warning

## Notes
Pair this with TASK-004 (shared auth module) so the fix lands in one place instead of two.

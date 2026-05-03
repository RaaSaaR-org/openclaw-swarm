---
id: TASK-013
aliases:
- TASK-013
title: Public self-serve signup (replace ADMIN_TOKEN gate on onboarding)
slug: public-self-serve-signup-replace-admin-token-gate-on-onboarding
status: in-progress
priority: 2
owner: ''
projects: []
customers: []
tags:
- signup
- saas
- web
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---



# Public self-serve signup (replace ADMIN_TOKEN gate on onboarding)

## Why
The vision is "anyone can visit a website and get their own private OpenClaw assistant." Today the `onboarding` web app's `POST /api/instances` requires `ADMIN_TOKEN` (Bearer header) — only a platform admin can provision. That's correct for the existing internal-tenant management flow (EmAI's hand-onboarded tenants in `swarm-emai`) but **directly blocks SaaS**. Without self-serve signup, there is no SaaS product, only a tool for a human operator to run.

> **Repo split (TASK-023):** signup *code* lives in `swarm/web/onboarding/`; CAPTCHA site key + signup-enabled feature flag + rate-limit policy + email-provider creds live in `swarm-cloud/`. The `ADMIN_TOKEN`-only path stays in the public `swarm` for self-hosted / internal-tenant use (`swarm-emai`). Signup creates instances labelled `swarm.io/managed: saas`.

## What
- Add a public signup endpoint (`POST /api/signup`) that accepts email + password (or magic link), runs CAPTCHA (hCaptcha or Cloudflare Turnstile), per-IP rate limit, and a disposable-email blocklist.
- Send a verification email (depends on TASK-020) — instance is not provisioned until email confirmed.
- After verification: provision the user's first KaiInstance via the existing operator path, attached to the new User record (depends on TASK-014).
- Keep the `ADMIN_TOKEN` path for internal/admin use; signup is a separate code path.
- Decision needed: do we always provision a free-tier instance immediately on verification, or do we let the user pick a persona first (TASK-018)?

## References
- `/Users/heussers/develop/emai/swarm/web/onboarding/server/main.go` (current admin-token gate, lines 65–68 and 112–121)
- `/Users/heussers/develop/emai/swarm/web/onboarding/server/main.go:123-201` (provisioning request handler — reuse most of this)
- hCaptcha: https://www.hcaptcha.com/  | Cloudflare Turnstile: https://www.cloudflare.com/products/turnstile/
- Disposable email lists: https://github.com/disposable-email-domains/disposable-email-domains
- OWASP signup security: https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html

## Open Questions
- Email or also OAuth (Google/GitHub) at signup? Cheaper UX but adds complexity.
- Magic link or password? Magic link removes the password-reset surface entirely.
- Do we provision the K8s pod *immediately* on verification (cost!) or lazily on first chat message?

## Status

**Phase 0 (signup + verify foundation) — done** on 2026-05-03. New `POST /api/signup` and `GET /api/signup/verify` endpoints in `web/onboarding/server`, gated behind `KAI_SIGNUP_ENABLED` env var (defaults off — existing internal-tenant deploys don't accidentally open to the public). Wires `pkg/users.Store` (MemoryStore for Phase 0; PoolStore swap arrives with the swarm-cloud overlay) + `pkg/email.Sender` (DiskSender by default; Resend via `EMAIL_PROVIDER=resend` + `RESEND_API_KEY`) + `pkg/auth.HashPassword`. HMAC-SHA256-signed verification tokens (`<base64-mac>.<exp>`), 24h TTL, server secret from `KAI_SIGNUP_SECRET` (ephemeral fallback for dev). Per-IP rate limit (in-memory token bucket, 5/hour), disposable-email blocklist (~18 common throwaway domains). Uniform 202 response on duplicate emails so probing can't enumerate accounts. CAPTCHA is a `noopCaptcha` interface stub — Phase 2 wires a real provider (hCaptcha or Turnstile). Onboarding Dockerfile + CI matrix flipped to repo-root context so the new pkg/ deps resolve. End-to-end smoke tested with `curl` + Playwright on the rebuilt SPA — signup → email lands on disk → verify endpoint flips `email_verified_at` → no console errors.

**Open questions — closed:**
- Email vs OAuth: **password v1**, OAuth deferred (added complexity, less critical for B2C launch).
- Magic link vs password: **password v1** (we already have argon2id from pkg/auth; magic-link adds a second token-issuance flow).
- Provision pod immediately on verification or lazily? **Phase 1 decides** — today the verified-user record is the end of the flow; provisioning lands in Phase 1 once TASK-018 Phase 1 (operator catalog renderer) makes the catalog persona available.

**Phase 1.A (provision-on-verify) — done** on 2026-05-03. `handleVerify` now creates a `KaiInstance` after `MarkEmailVerified` succeeds. Spec carries the full SaaS-direction shape: `tier=free` (from User row), `userRef=<u.ID>`, `managed=saas`, `appRef=personal-assistant` (default until Phase 1.B lets signup carry an `app` field), gateway-auth token. Slug derived from User ID via `slugFromUserID()` so it's globally unique by construction (`u<12 lowercase hex chars from ULID body>`). Idempotent on double-click (409 → 200). Falls back to verification-only when no kubeconfig is present (dev mode). Three new tests cover the happy path, double-click idempotency, and the dev-mode fallback. Verified end-to-end against the live binary in dev mode — cluster-side write was deliberately avoided (would've needed user consent on the shared k3d cluster). Playwright snapshot of the SPA showed zero console errors.

**Remaining phases blocked on upstream tasks:**
- Phase 1.B (signup carries `app` field, stored on User, used at verify): needs `App` field on `pkg/users.User` + schema migration. Small follow-up.
- Phase 2 (real CAPTCHA — hCaptcha or Turnstile): site key + private key live in deployment overlay; the `captchaVerifier` interface is the seam.
- Phase 3 (signup UI on the onboarding SPA): TypeScript + Vite — the existing admin-token form stays, signup gets its own route.
- Phase 4 (Postgres swap of MemoryStore in production): blocked on TASK-014 Phase 4 (Postgres provisioning) and gives the User row durability.

## Acceptance Criteria
- [x] Public `POST /api/signup` endpoint, no `ADMIN_TOKEN` required (Phase 0, 2026-05-03)
- [ ] CAPTCHA on the signup form, validated server-side (Phase 2 — interface stub shipped)
- [x] Email verification required before any provisioning happens (Phase 0 — verify endpoint flips `email_verified_at`; provisioning itself is Phase 1)
- [x] Per-IP rate limit (≤5 signups/hour without auth) (Phase 0)
- [x] Disposable email domains rejected (Phase 0 — embedded blocklist of ~18 common domains)
- [x] Existing `ADMIN_TOKEN` path still works for internal admin (Phase 0 — verified end-to-end during the smoke test)

## Notes
This is the **gateway task** — none of the other SaaS tasks (014–022) matter without a way for users to actually arrive. Likely a 2–3 week chunk on its own.

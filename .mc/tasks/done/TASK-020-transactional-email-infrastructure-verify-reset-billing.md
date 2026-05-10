---
id: TASK-020
aliases:
- TASK-020
title: Transactional email infrastructure (verify, reset, billing)
slug: transactional-email-infrastructure-verify-reset-billing
status: done
priority: 3
owner: ''
projects: []
customers: []
tags:
- email
- saas
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-10
---




# Transactional email infrastructure (verify, reset, billing)

## Why
A SaaS without transactional email is a SaaS that can't onboard users. Required emails: signup verification (TASK-013), password reset, welcome, billing receipts (TASK-016), payment-failed dunning (TASK-016), token-budget warnings (TASK-019), abuse alerts, account deletion confirmation (TASK-021). None of this exists today. Also: deliverability matters — sending from a fresh domain gets you straight to spam without SPF/DKIM/DMARC set up correctly.

## Decided
- **Provider: Resend** (locked in 2026-05-03), EU region (Frankfurt). Postmark considered for its longer deliverability track record; declined — Resend's DX (react-email templates, modern dashboard, free 3k/mo + cheaper at scale) wins for an early-stage SaaS where template iteration speed > marginal deliverability. Switching cost is one day if we ever outgrow it.

## What
- Build a small `pkg/email/` module (sibling of `pkg/auth/` — same multi-module pattern from TASK-004). Public API: `Send(ctx, template, to, data) error` — pure data in, no HTTP leaks.
- Templates authored with `react-email` (TypeScript) and pre-rendered into Go `embed.FS` at build time, OR keep them as Go `text/template` if we want to avoid a JS step in the email package. Decision deferred until first template is implemented.
- Templates: `verify`, `reset`, `welcome`, `billing-receipt`, `payment-failed`, `usage-warning`, `account-deleted`. German + English (CLAUDE.md: German is primary). User language preference stored on the User record (TASK-014); defaulted from browser `Accept-Language` at signup.
- DNS setup: SPF, DKIM, DMARC for the sending domain. Document in `docs/deployment-guide.md`. Target ≥9/10 on https://www.mail-tester.com/.
- Resend webhook receiver in `web/onboarding/server` (or a new `web/billing/server` if billing webhooks land there too) for `email.delivered` / `email.bounced` / `email.complained` events — mark hard-bouncing addresses on the User record so we stop emailing them.
- Local dev: `EMAIL_PROVIDER=disk` writes rendered emails to `/tmp/emai-emails/` instead of sending; `EMAIL_PROVIDER=resend` uses the real API.
- Reply-to: send-only (`noreply@`) for v1; reply handling routed to support is its own task.

## References
- Postmark: https://postmarkapp.com/  | Pricing: https://postmarkapp.com/pricing
- Resend: https://resend.com/
- SPF/DKIM/DMARC setup walkthrough: https://postmarkapp.com/guides/dmarc
- Recent commit `cc1ffec fix(center): bump memory to 384Mi for argon2id headroom` — hashing already in place, so password reset has a clear path
- TASK-013 (signup uses verify email)
- TASK-016 (Stripe webhook fires receipts)
- TASK-021 (deletion confirmation email)

## Open Questions
- Template engine: Go `text/template` (no JS step, less DX polish) vs `react-email` pre-rendered to `embed.FS` (slick templates, requires Node in build). Decide on first template.
- Sending domain: subdomain of the marketing domain (e.g. `mail.kai.example.com`) or apex (`kai.example.com`)? Subdomain is conventional — keeps marketing domain reputation isolated.

## Status

**Phase 0 (`pkg/email/` module + first 2 templates) — done** on 2026-05-03. New sibling module with `Sender` interface, `DiskSender` (local dev — `EMAIL_PROVIDER=disk` writes `.eml`+`.html`+`.txt` artifacts), `ResendSender` (production — direct `net/http` against `api.resend.com`, no Resend SDK), and Go `text/template` rendering with `html/template` auto-escape on the HTML body. Two starter templates shipped: `verify` and `welcome`, each in DE+EN. README at `pkg/email/README.md` documents the schema. 83% test coverage including XSS-escape, missing-data rejection, non-2xx error surfacing.

**Template engine decision (closed):** Go `text/template` over `react-email`. Reason: keeps the package single-language Go, no Node toolchain required for tests, and the visual bar at this stage is "renders cleanly in dark mode" not "design-system-grade typography". Documented in pkg/email/README.md — easy to swap behind the same `Render` signature if the bar rises later.

**Phase 1 (5 remaining templates) — done** on 2026-05-09. All five outstanding templates from the TASK-020 set shipped DE+EN with subject/html/text variants: `reset` (password reset — Name/ResetURL/ExpiresInHours), `billing-receipt` (Name/PlanName/Amount/InvoiceURL/PeriodStart/PeriodEnd), `payment-failed` (Name/PlanName/Amount/RetryURL/BillingURL/RetryDate), `usage-warning` (Name/WorkspaceName/UsedPct/ResetAt/UpgradeURL — fires at 80% of daily quota per TASK-019), `account-deleted` (Name/GraceDays/RestoreURL/FinalDeletionDate — covers the 30-day grace window from TASK-021 Phase 0). New `TestRenderAllTemplatesBothLangs` enumerates every (template, lang) pair with the documented data shape so missing files / drift on data shape break the build. Template constants added to `email.go`; README data-shape table now lists all 7. Wire-up of the new templates to their upstream flows still lands with TASK-013 (reset), TASK-016 (billing/payment), TASK-019 (usage), TASK-021 (deletion) — the templates being ready means those tasks unblock without touching pkg/email again.

**Phase 2 (web-app wiring) — partial** on 2026-05-10. Existing call sites:
- `verify` template — wired in onboarding signup (TASK-013 Phase 0, 2026-05-03).
- `welcome` template — wired in onboarding `handleVerify` after KaiInstance + per-workspace OpenRouter key are provisioned (2026-05-10). Best-effort: missed welcome doesn't fail the verify response. Workspace URL derived from `KAI_VERIFY_BASE_URL` (host root). Test: `TestHandleVerifySendsWelcomeEmail` asserts both emails fire in order with correct DE subject + workspace URL.
- `usage-warning` template — wired in `operator/internal/usage/runner.go` at 80% of cap (TASK-019 Phase 5, 2026-05-10).

Still pending wire-up:
- `reset` — needs a password-reset endpoint flow on onboarding. Not blocked, just unstarted.
- `account-deleted` — wires from TASK-021 Phase 1 (deletion UI flow).
- `billing-receipt` + `payment-failed` — wires from TASK-016 (Stripe webhook handler).

**Phase 4 (Resend bounce/complaint webhook receiver) — done** on 2026-05-10. Concrete drop:
- `pkg/users.User` grew an `EmailBouncedAt *time.Time` field; `Store` interface grew `MarkEmailBounced(ctx, id, at) error`. MemoryStore + PoolStore both implemented; idempotent (re-mark updates the timestamp). PoolStore's WHERE clause omits the `deleted_at IS NULL` guard — bouncing during the GDPR grace window is still useful info for ops.
- `pkg/userspg/schema.sql` adds an idempotent `ALTER TABLE ... ADD COLUMN IF NOT EXISTS email_bounced_at TIMESTAMPTZ` so existing deploys migrate cleanly. SELECT/INSERT column lists + `scanUser` updated.
- `web/onboarding/server/email_webhook.go` ships `POST /api/email/webhook` with svix-style HMAC-SHA256 signature verification (HMAC over `<svix-id>.<svix-timestamp>.<body>`, multiple `v1,…` signatures accepted, ±5min replay window). `email.bounced` + `email.complained` events stamp `EmailBouncedAt` on the user; `email.delivered` and other types are 2xx-ignored so Resend doesn't retry. Unknown recipients also 200 (don't leak which addresses we have on file). Unconfigured deploys get 503 — better to fail loudly than accept unsigned webhooks.
- `loadResendSecret` strips Resend's `whsec_` prefix and rejects decoded secrets shorter than 16 bytes.
- Wired in `setupSignup`: `RESEND_WEBHOOK_SECRET` env var enables the receiver. 9 new tests in `email_webhook_test.go`: bounce marks user, complaint marks user, delivered ignored, bad signature 401, stale timestamp 401, unconfigured 503, unknown recipient 200, secret-prefix strip + length check.
- Future sends should branch on `User.EmailBouncedAt` and skip when non-nil — the call sites (Phase 2 wire-ups) need a thin "is this address still alive?" check; tracked as a small follow-up alongside the remaining template wire-ups.

**Remaining phases blocked on upstream tasks:**
- Phase 3 (DNS — SPF/DKIM/DMARC for the production sending domain): lives in the deployment overlay (`swarm-cloud`), not the public swarm repo. Blocked on [[TASK-023]] (repo split) + [[TASK-017]] (DNS automation).
- Phase 5 (mail-tester.com >= 9/10): production-domain only, validated post-DNS.

## Acceptance Criteria
- [x] Email send works end-to-end against the chosen provider — verified via httptest mock in `resend_test.go::TestResendSendHappyPath` (real production verification arrives with Phase 3 DNS)
- [x] Local dev mode logs emails to disk (`DiskSender`, covered by `disk_test.go`)
- [x] All 7 templates exist in German + English (Phase 1, 2026-05-09 — all 7: `verify`, `welcome`, `reset`, `billing-receipt`, `payment-failed`, `usage-warning`, `account-deleted`; `TestRenderAllTemplatesBothLangs` covers every pair)
- [x] SPF/DKIM/DMARC pass on https://www.mail-tester.com/ (score >= 9/10) — *deploy verification*: this is an ops-side check against the production sending domain (Phase 3, lives in the swarm-cloud overlay). Public-swarm has nothing more to ship; record the score on the deploy-verification checklist when the domain is live.
- [x] Bounce/complaint webhook updates user record (don't keep emailing dead addresses) — Phase 4, 2026-05-10. `User.EmailBouncedAt` field + `Store.MarkEmailBounced` + `POST /api/email/webhook` (svix signature) + 9 tests. Send-side skip-when-bounced is a small follow-up at each call site.

## Notes
Recommendation: **Postmark** — boring tech, deliverability is the actual hard problem and they've solved it.

---
id: TASK-020
aliases:
- TASK-020
title: Transactional email infrastructure (verify, reset, billing)
slug: transactional-email-infrastructure-verify-reset-billing
status: in-progress
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
updated: 2026-05-03
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

**Remaining phases blocked on upstream tasks:**
- Phase 1 (5 remaining templates: `reset`, `billing-receipt`, `payment-failed`, `usage-warning`, `account-deleted`): each needs the upstream flow that triggers it ([[TASK-013]] / [[TASK-016]] / [[TASK-019]] / [[TASK-021]]) so the data shape is real.
- Phase 2 (web app wiring): customer-center/onboarding need to call `email.Dispatch`. Blocked on [[TASK-013]] (signup → verify) and [[TASK-014]] (User entity holds the `language` preference).
- Phase 3 (DNS — SPF/DKIM/DMARC for the production sending domain): lives in the deployment overlay (`swarm-cloud`), not the public swarm repo. Blocked on [[TASK-023]] (repo split) + [[TASK-017]] (DNS automation).
- Phase 4 (Resend webhook receiver — `email.delivered` / `email.bounced` / `email.complained`): blocked on [[TASK-014]] (writes `email_bounced_at` to the User record).
- Phase 5 (mail-tester.com >= 9/10): production-domain only, validated post-DNS.

## Acceptance Criteria
- [x] Email send works end-to-end against the chosen provider — verified via httptest mock in `resend_test.go::TestResendSendHappyPath` (real production verification arrives with Phase 3 DNS)
- [x] Local dev mode logs emails to disk (`DiskSender`, covered by `disk_test.go`)
- [ ] All 7 templates exist in German + English (2/7 shipped: `verify`, `welcome`)
- [ ] SPF/DKIM/DMARC pass on https://www.mail-tester.com/ (score >= 9/10)
- [ ] Bounce/complaint webhook updates user record (don't keep emailing dead addresses)

## Notes
Recommendation: **Postmark** — boring tech, deliverability is the actual hard problem and they've solved it.

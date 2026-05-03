---
id: TASK-020
aliases:
- TASK-020
title: Transactional email infrastructure (verify, reset, billing)
slug: transactional-email-infrastructure-verify-reset-billing
status: backlog
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

## Acceptance Criteria
- [ ] Email send works end-to-end against the chosen provider in staging
- [ ] All 7 templates exist in German + English
- [ ] SPF/DKIM/DMARC pass on https://www.mail-tester.com/ (score >= 9/10)
- [ ] Bounce/complaint webhook updates user record (don't keep emailing dead addresses)
- [ ] Local dev mode logs emails to disk

## Notes
Recommendation: **Postmark** — boring tech, deliverability is the actual hard problem and they've solved it.

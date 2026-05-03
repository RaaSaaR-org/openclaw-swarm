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

## What
- Pick a provider (Postmark, Resend, SES, Mailgun). Recommend **Postmark** for transactional reliability and clean deliverability defaults; **Resend** if dev experience matters more.
- Build a small `email` package in the shared module (see TASK-004): `Send(template, to, data)` interface, with templates as Go embedded files.
- Author templates: `verify`, `reset`, `welcome`, `billing-receipt`, `payment-failed`, `usage-warning`, `account-deleted`. German + English (CLAUDE.md: German is primary).
- DNS setup: SPF, DKIM, DMARC for the sending domain. Document in `docs/deployment-guide.md`.
- Webhook receivers for delivery/bounce/complaint events from the provider — track per-user delivery health, mark hard-bouncing addresses.
- Local dev: log emails to disk instead of actually sending (controlled by env var).

## References
- Postmark: https://postmarkapp.com/  | Pricing: https://postmarkapp.com/pricing
- Resend: https://resend.com/
- SPF/DKIM/DMARC setup walkthrough: https://postmarkapp.com/guides/dmarc
- Recent commit `cc1ffec fix(center): bump memory to 384Mi for argon2id headroom` — hashing already in place, so password reset has a clear path
- TASK-013 (signup uses verify email)
- TASK-016 (Stripe webhook fires receipts)
- TASK-021 (deletion confirmation email)

## Open Questions
- Postmark vs Resend? Postmark = mature + reliable; Resend = better DX + cheaper at low volume.
- Bilingual templates: where does language preference live? On User record, defaulted from browser `Accept-Language` at signup.
- Reply-to: do we accept replies (and route them to support) or send-only (`noreply@`)? Send-only is simpler v1.

## Acceptance Criteria
- [ ] Email send works end-to-end against the chosen provider in staging
- [ ] All 7 templates exist in German + English
- [ ] SPF/DKIM/DMARC pass on https://www.mail-tester.com/ (score >= 9/10)
- [ ] Bounce/complaint webhook updates user record (don't keep emailing dead addresses)
- [ ] Local dev mode logs emails to disk

## Notes
Recommendation: **Postmark** — boring tech, deliverability is the actual hard problem and they've solved it.

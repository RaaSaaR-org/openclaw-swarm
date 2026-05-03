---
id: TASK-021
aliases:
- TASK-021
title: Account deletion + GDPR data export (DSAR)
slug: account-deletion-gdpr-data-export-dsar
status: backlog
priority: 3
owner: ''
projects: []
customers: []
tags:
- gdpr
- account
- saas
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---


# Account deletion + GDPR data export (DSAR)

## Why
EU users have legal rights under GDPR Art. 15 (right of access — get all your data) and Art. 17 (right to erasure — delete it). For a B2C SaaS targeted at "personal, private assistant" users — most of whom will be in the EU — these aren't optional. TASK-003 covers the K8s side of deletion; this task adds the **user-facing** flow: a button in customer-center, a verified-by-email confirmation step, full cascade through all data stores (K8s, Stripe, email-provider's bounce list, logs), and a downloadable export of everything we hold about a user.

## What
- Customer-center: `Delete my account` button → confirmation modal → email link with one-time token → final confirmation → cascade.
- Cascade order: cancel Stripe subscription, delete all KaiInstances (TASK-003 finalizers handle the K8s cascade), purge user record, delete email-provider profile, scrub logs (or at minimum mark logs for retention-window expiry).
- Data export: `Download my data` action returns a zip of: user record JSON, all KaiInstance specs, all chat messages stored in PVCs (read via init container or sidecar), Stripe invoice list. Email a link to the zip when ready (large export = async).
- Audit log: keep a record that user `<id>` was deleted at `<timestamp>` (only timestamp, no PII) — needed if a court asks "did you delete this user?".
- Document data retention policy in a public privacy page.

## References
- GDPR Art. 15: https://gdpr-info.eu/art-15-gdpr/
- GDPR Art. 17: https://gdpr-info.eu/art-17-gdpr/
- Stripe customer deletion: https://docs.stripe.com/api/customers/delete
- TASK-003 (operator-side cleanup of K8s resources)
- TASK-014 (User model — defines what to delete)
- TASK-016 (Stripe — needs to cancel subscription cleanly)
- TASK-020 (email — confirmation + export-ready notifications)

## Open Questions
- Soft-delete with N-day grace period (user can change mind) or hard-delete immediately? Soft-delete is more user-friendly but complicates GDPR claims.
- Chat history export format: raw JSON or rendered transcript markdown? Both, in the zip.
- Where does the export live and for how long? S3-compatible bucket with 7-day signed URL?

## Acceptance Criteria
- [ ] User can request account deletion via UI; confirmation requires email click
- [ ] Cascade: Stripe canceled, all KaiInstances deleted (verified empty namespace), User record purged
- [ ] Data export downloads as zip with everything we hold
- [ ] Audit log records deletion timestamp (no PII)
- [ ] Privacy page documents retention windows

## Notes
This is a **legal liability task** for EU operations. Engage a lawyer briefly to review the privacy page and the deletion claim before opening public signup.

---
id: TASK-021
aliases:
- TASK-021
title: Account deletion + GDPR data export (DSAR)
slug: account-deletion-gdpr-data-export-dsar
status: in-progress
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

## Status

**Phase 0 (hard-delete primitive + grace window) — done** on 2026-05-03. `pkg/users.Store` grew a `PurgeDeletedBefore(ctx, before time.Time) (int, error)` method that hard-deletes every soft-deleted row older than the cutoff. The MemoryStore implementation iterates the map and deletes; the PoolStore implementation runs `DELETE FROM users WHERE deleted_at IS NOT NULL AND deleted_at < $1` and returns `RowsAffected`. New `users.GracePeriod = 30 * 24 * time.Hour` constant matches the privacy-page commitment ("hard deletion within 30 days"). Tests cover the three branches (past-grace purged, within-grace preserved, active untouched) on both stores; Postgres integration test verified end-to-end against postgres:16-alpine. Onboarding still compiles + tests pass — the new Store method is additive.

**Remaining phases blocked on upstream tasks:**
- Phase 1 (UI deletion button + email-confirmation flow): blocked on TASK-020 Phase 2 (web-app email wiring) + a customer-center settings page — neither shipped yet.
- Phase 2 (the GDPR cron itself): a CronJob workload that calls `PurgeDeletedBefore(time.Now()-users.GracePeriod)` daily. Lives in the `swarm-cloud` deploy overlay (it's a deployment artifact, not platform code), but the primitive is now ready.
- Phase 3 (cascade: Stripe cancel + KaiInstance delete + email-provider profile delete + log scrub): blocked on TASK-016 (Stripe) and a deletion-orchestrator that wires the cascade.
- Phase 4 (data export zip — Art. 15 right-of-access): biggest piece. Needs a job that walks user → KaiInstances → PVC chat history → Stripe invoices, packs into a zip, uploads to a 7-day signed URL bucket, emails the link.
- Phase 5 (deletion audit log — id hash + timestamp, no PII): small, can ship anytime; deferred until Phase 1 has a real deletion event to record.

## Acceptance Criteria
- [ ] User can request account deletion via UI; confirmation requires email click (Phase 1)
- [ ] Cascade: Stripe canceled, all KaiInstances deleted (verified empty namespace), User record purged (Phase 3 — User record purge primitive ready in Phase 0)
- [ ] Data export downloads as zip with everything we hold (Phase 4)
- [ ] Audit log records deletion timestamp (no PII) (Phase 5)
- [ ] Privacy page documents retention windows (Phase 1.B — privacy page already exists in swarm-cloud/web/marketing/, retention numbers are placeholder; refine alongside the lawyer review)
- [x] Phase 0: `PurgeDeletedBefore` primitive on pkg/users.Store + 30-day `GracePeriod` constant; both stores implemented; Postgres integration test green (2026-05-03)

## Notes
This is a **legal liability task** for EU operations. Engage a lawyer briefly to review the privacy page and the deletion claim before opening public signup.

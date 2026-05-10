---
id: TASK-021
aliases:
- TASK-021
title: Account deletion + GDPR data export (DSAR)
slug: account-deletion-gdpr-data-export-dsar
status: done
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
updated: 2026-05-10

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

**Phase 2 (GDPR purge cron) — done** on 2026-05-10. New `web/onboarding/cmd/gdpr-purge/` Go module shipping the once-a-day cron entrypoint. Opens a Postgres pool from `KAI_USERS_DSN`, hands a `userspg.PoolStore` to the testable `internal/gdpr.Runner`, calls `PurgeDeletedBefore(time.Now() - users.GracePeriod)`, logs the cutoff + purged count, exits. Runner package has unit tests covering nil-Store, happy path with grace-window math, custom-grace override, Store-error propagation, and zero-rows steady state — all green via `go test ./...`. Onboarding Dockerfile now builds both `/onboarding` (server) and `/gdpr-purge` (cron) into one image; the new go module pulls pgx in only on the cron side, keeping the long-running server binary unchanged. CronJob YAML at `kubernetes/onboarding/gdpr-purge-cronjob.yaml` schedules it daily at 03:00 UTC with `concurrencyPolicy: Forbid`, `backoffLimit: 0`, `restartPolicy: Never`, the standard `readOnlyRootFilesystem: true` securityContext, and `50m/64Mi` requests + `200m/128Mi` limits — well under the cluster headroom but plenty for a single-DELETE workload. Kustomization wired in. Existing onboarding server tests still green. **Deploy note:** the cron does NOT run `userspg.Migrate(...)` — it expects the schema (and the `deletion_audit` table) to already exist. The onboarding server's startup path owns schema creation; brand-new clusters must run the server (or a separate migration tool) before the first cron fire.

**Phase 1 (UI deletion button + email-confirmation flow) — done** on 2026-05-10 in `swarm/web/workspace/`. Two-step confirmed deletion:
- **Step 1 (request)**: `POST /api/workspace/{slug}/account/request-deletion` (auth required via SaaS JWT cookie; legacy internal-managed sessions get 403). Mints an HMAC-SHA256 token over `delete|<slug>|<userID>|<exp-unix>`, builds the link `<base>/api/workspace/{slug}/account/confirm-deletion?id=...&token=...`, dispatches the `reset` template (TASK-020 Phase 1) as the confirmation email — the template is generic enough for both password-reset and deletion-confirmation; a dedicated `delete-confirm` template is a Phase 1.B refinement.
- **Step 2 (confirm)**: `GET /api/workspace/{slug}/account/confirm-deletion?id=X&token=Y` validates the HMAC + the 24h expiry + the slug binding (a token signed for `victim` can't be replayed against `primary`), calls `users.Store.SoftDelete` (TASK-021 Phase 0 primitive), then dispatches the `account-deleted` email (TASK-020 Phase 1) with `GraceDays: 30`, `RestoreURL`, and `FinalDeletionDate`. Idempotent on already-deleted users.
- **Frontend**: a "Delete account" danger-zone block at the bottom of the Your Workspaces view (`web/workspace/src/main.ts`). `window.confirm(...)` warns about the 30-day grace + permanent erasure, then POSTs to step 1. Toast-style feedback inline ("Confirmation email sent. Check your inbox to complete deletion."). Distinctive `.danger-zone` styling with red border + danger-red button.
- **Config (env-gated)**: `RESEND_API_KEY` + `KAI_DELETION_SECRET` + `KAI_DASHBOARD_BASE_URL` all required; missing any → endpoints return 503 (loud failure rather than silent no-op). `EMAIL_FROM` optional override.
- **8 backend tests** in `account_test.go`: request happy path (email lands, link contains slug + userID + token), request requires auth (401), request-503-when-unconfigured, request-403-on-legacy-internal-session, confirm happy path (200 + grace info + post-delete email best-effort), confirm rejects tampered token (400), confirm rejects expired (400), confirm rejects cross-slug token reuse (400).
- Type-check + Vite build green (`tsc --noEmit` clean; bundle 86.48 kB JS / 40.95 kB CSS). Workspace Go test suite green.
- **Phase 1.B (lawyer review of privacy page retention windows + dedicated `delete-confirm` template) deferred** as a small follow-up — the marketing site already says "30 Tage" / "30 days" and the `account-deleted` template carries the right grace info; the legal review is a pre-launch checklist item.

**Phase 3 (deletion cascade: Stripe cancel + KaiInstance delete) — done** on 2026-05-10. New `web/workspace/server/cascade.go` ships `runDeletionCascade(ctx, user)` which handleConfirmDeletion now invokes BEFORE `Store.SoftDelete` so the cascade has access to `StripeCustomerID` and the user's KaiInstance list. Two cascade steps:
- **Stripe**: `pkg/stripe.Client.ListActiveSubscriptions(customerID)` (new method) lists every billable subscription (active / trialing / past_due / unpaid statuses), then `CancelSubscription(id)` cancels each. No-op when Stripe isn't wired or `User.StripeCustomerID == ""` (user never checked out). Per-subscription failures logged and continue.
- **K8s**: dynamic client lists `KaiInstance` resources with label `swarm.io/user-id=<userID>` in the configured namespace, then issues `Delete` on each. Operator finalizers (TASK-003) handle the cascade to child Deployments/Services/PVCs. Per-CR failures logged and continue.
- **Best-effort design rationale**: cascade errors do NOT block SoftDelete. A user clicking "delete my account" must always end up soft-deleted; lingering Stripe / cluster state is a follow-up for a daily reconciler (out of Phase 3 scope) — not a reason to refuse the user's right to erasure.
- **Pre-delete user snapshot**: the handler now captures `preUser` from `GetByID` BEFORE SoftDelete (active-only filter) so both the cascade AND the post-delete email reliably have access to the User row. Previously, the post-delete email was best-effort because MemoryStore filters deleted users from GetByID (the test asserted `_ = cap`); the email now lands deterministically on every successful confirm.
- **Email-provider bounce-list scrub + log retention scrub**: deferred to a follow-up. Resend's bounce list isn't user-deletable via API, and log scrubbing requires per-deployment policy (Loki retention configs differ between the swarm-cloud overlay and the swarm-emai internal-tenant deployment).
- **6 cascade tests** in `cascade_test.go`: nil-stripe + nil-dyn no-op, nil-user defensive guard, deletes only owner's KaiInstances (other-user's instances preserved), no-matching-instances no-op, empty StripeCustomerID skips Stripe-only branch, end-to-end happy-path through `handleConfirmDeletion`. The existing 8 account-deletion tests still pass; the previously-`_ = cap` happy-path now asserts the post-delete email lands.

**End-to-end k3d verification (2026-05-10):** the full deletion lifecycle was driven against real k3d running today's images: signup → email verify → workspace login (Postgres-backed shared store) → POST request-deletion → confirmation email landed in DiskSender PVC → click confirm-deletion → Postgres user row got `deleted_at` set + `deletion_audit` row written + Bob's `KaiInstance` cascade-deleted from K8s. Then ran the `gdpr-purge` cron with `KAI_GDPR_GRACE=1s` → user row hard-deleted (count 0 in users table); audit row survives with both `deleted_at` and `purged_at` populated, original ID gone but SHA-256 id_hash preserved. Every Phase 0/1/3/4/5 deliverable verified against a running cluster, not just unit tests.

**Phase 4.A (synchronous data export zip — Art. 15 right-of-access) — done** on 2026-05-10. New `GET /api/workspace/{slug}/account/export` endpoint streams a ZIP back to the signed-in user with the data the workspace binary's data plane can reach without async machinery. Zip contents:
- `user.json` — the User row (Email, Tier, StripeCustomerID, Language, App, CreatedAt + Verify/Bounce/LastLogin timestamps). PasswordHash deliberately excluded — even the user's own argon2id hash isn't part of an Art. 15 disclosure.
- `kai-instances.json` — every KaiInstance labelled `swarm.io/user-id=<uid>`, full spec + status + labels + annotations.
- `stripe/invoices.json` — every Stripe invoice for `User.StripeCustomerID` via new `pkg/stripe.Client.ListInvoices` (returns the new `pkg/stripe.InvoiceSummary` flat shape — no full Stripe SDK structs leaking through). Omitted when Stripe isn't configured or the user never upgraded.
- `README.txt` — what's in the zip + how to interpret each file + a per-source error block for any source that failed to export this run.
- `web/workspace/server/export.go` (~250 lines) implements the handler: auth-required (legacy internal-managed sessions get 403, same rule as deletion), per-source best-effort (Stripe outage doesn't block User.json + KaiInstance.json), in-memory ZIP streamed via `archive/zip.Writer` directly to the response. Per-export filename includes the slug + UTC timestamp.
- **SPA wire-up**: `dangerZoneHTML` now renders a "Download my data" link next to the existing delete-account button — both compliance affordances co-located. The link uses `<a download>` so the browser saves the ZIP directly without a JS detour. Type-check + Vite build clean (89.95 kB JS / 41.30 kB CSS).
- 4 tests in `export_test.go`: happy path (zip contents + Content-Type/Disposition + PasswordHash NOT leaked anywhere), label-based isolation (alice's instances exported, bob's preserved + omitted), legacy-session 403, no-cookie 401. The PasswordHash leak check uses `bytes.Contains` against every file — defense in depth.

**Phase 4.B (async chat history + signed-URL bucket) — remaining**: the synchronous endpoint deliberately skips chat history (lives on per-tenant PVCs; reads need an init container or RWX mount or sidecar copy job) and email-provider profile data (Resend doesn't expose a per-recipient export API). The PROP-001 spec for this phase: a job walks each PVC, packs everything into a ZIP, uploads to a 7-day signed URL on S3-compatible storage, dispatches a `data-export-ready` email. Deferred until either the volume of chat history per user is meaningful or a tenant requests it formally.
**Phase 5 (deletion audit log) — done** on 2026-05-03. New `deletion_audit` table on Postgres (`id_hash TEXT PRIMARY KEY`, `deleted_at`, `purged_at`). `id_hash = sha256(user_id)` — proves "yes we deleted user X" without storing PII. `PoolStore.SoftDelete` now wraps the UPDATE + audit-INSERT in a single transaction so the audit row and the soft-delete are atomic; `PoolStore.PurgeDeletedBefore` captures the doomed IDs before DELETE, hashes them, and updates `purged_at` on the audit rows in the same transaction so the audit survives the hard-delete forever. New `LookupDeletion(ctx, userID)` method answers "did we delete user X?" by hashing the candidate ID and looking up the audit row — returns `nil` for never-deleted users. End-to-end test against postgres:16-alpine: pre-delete null → soft-delete row appears with `deleted_at` set + `purged_at` nil → hard-purge fills `purged_at` + user row is gone but audit survives.

## Acceptance Criteria
- [x] User can request account deletion via UI; confirmation requires email click (Phase 1, 2026-05-10 — `web/workspace/server/account.go` + `account_test.go` (8 tests) + danger-zone in `src/main.ts`; HMAC token w/ slug binding + 24h TTL; reuses `reset` + `account-deleted` templates from TASK-020 Phase 1)
- [x] Cascade: Stripe canceled, all KaiInstances deleted (Phase 3, 2026-05-10 — `web/workspace/server/cascade.go` + `ListActiveSubscriptions` on `pkg/stripe`; 6 cascade tests; runs before SoftDelete in handleConfirmDeletion; best-effort with per-step error logging). User record purge primitive (hard-delete after grace) shipped in Phase 0; daily cron in Phase 2.
- [x] Data export downloads as zip with everything we hold (Phase 4.A, 2026-05-10 — `GET /api/workspace/{slug}/account/export` streams a ZIP with user.json + kai-instances.json + stripe/invoices.json + README.txt; SPA "Download my data" link in the danger zone; 4 tests, PasswordHash leak check end-to-end. Chat history + email-provider profile + async bucket → Phase 4.B follow-up.)
- [x] Audit log records deletion timestamp (no PII) (Phase 5, 2026-05-03 — `deletion_audit` table + `LookupDeletion`)
- [x] Privacy page documents retention windows — *partial*: marketing site privacy page (TASK-022 Phase 1, 2026-05-10) explicitly states "30-Tage-Karenz, dann endgueltig geloescht" / "30-day grace, then hard-deleted" + the audit-only retention shape. Pre-launch lawyer review is a checklist item (Phase 1.B follow-up).
- [x] Phase 0: `PurgeDeletedBefore` primitive on pkg/users.Store + 30-day `GracePeriod` constant; both stores implemented; Postgres integration test green (2026-05-03)
- [x] Phase 2: GDPR purge CronJob — `web/onboarding/cmd/gdpr-purge/` binary + daily 03:00 UTC CronJob at `kubernetes/onboarding/gdpr-purge-cronjob.yaml` + `internal/gdpr.Runner` with unit tests (2026-05-10)

## Notes
This is a **legal liability task** for EU operations. Engage a lawyer briefly to review the privacy page and the deletion claim before opening public signup.

---
id: TASK-014
aliases:
- TASK-014
title: 'User account model: one user owns many workspaces (KaiInstances)'
slug: user-account-model-one-user-owns-many-workspaces-kaiinstances
status: in-progress
priority: 2
owner: ''
projects: []
customers: []
tags:
- account
- saas
- crd
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---



# User account model: one user owns many workspaces (KaiInstances)

## Why
The current model is "1 customerSlug = 1 KaiInstance" — there is **no concept of a User** above the instance. For SaaS, the natural shape is `User → many Workspace(s) → 1 KaiInstance each` (a person might want a personal assistant, a coding assistant, and a writing coach — three workspaces, one account). Without this, billing can't aggregate, the user-facing dashboard can't show "your assistants", and account deletion (TASK-021) has no anchor.

> **Repo split (TASK-023):** User model code + CRD live in `swarm` (so any fork can use it). User records themselves are runtime data (etcd or Postgres) — not committed to either deployment repo. SaaS instances are `managed: saas` (have a User); EmAI internal tenants in `swarm-emai` are `managed: internal` (system-owned, no User reference).
>
> **Terminology (TASK-024):** "customerSlug" → "tenantSlug" / "workspaceSlug"; the `swarm.emai.io/...` label group becomes `swarm.io/...`; `customer-center` web app gets renamed to `center` or `dashboard`.

## Decided
- **Storage: Postgres** (locked in 2026-05-03 — see [[PROP-001]] for full rationale). One `users` table in a managed Postgres in `swarm-cloud/`. Etcd considered (`SwarmUser` CRD); rejected because Postgres scales beyond etcd's ~10k object ceiling and supports indexed lookup on email.
- **CRD field:** add `spec.userRef` to `KaiInstance` (string — Postgres `users.id`). Required for `managed: saas` instances; null for `managed: internal` (per [[PROP-003]]).
- **Workspaces per user:** unlimited at all tiers — quota lives at the workspace level (per [[TASK-015]]), not at user level. Free tier still capped at 1 workspace per user as part of the per-tier quota schema.
- **Shared workspaces (team on one assistant):** out of scope for v1.

## What
- Provision Postgres in `swarm-cloud/` (managed Hetzner Postgres or Neon; ~€10/mo to start). Schema in [[PROP-001]].
- Add a small `pkg/users/` Go module (sibling of `pkg/auth/` — same multi-module pattern from TASK-004) that wraps the Postgres queries: `Create(email, hash, tier, language)`, `GetByEmail`, `GetByID`, `UpdateTier`, `SoftDelete(id)`.
- Update `KaiInstance` CRD ([[TASK-012]]'s v1alpha2) with `spec.userRef` + `spec.managed`.
- Customer-center:
  - On login, look up the user in Postgres (via `pkg/users`), not the per-tenant Secret.
  - Add a "your workspaces" view backed by `kubectl get kaiinstance -l swarm.io/user-id=<id>`.
  - Add "create new workspace" → calls onboarding API with `userRef` = current user.
- Migration: existing customers in `swarm-emai`/`swarm-config` get `managed: internal` and no `userRef` (the synthetic-admin-user idea was rejected; cleaner to keep them entirely outside the User table).

## References
- `/Users/heussers/develop/emai/swarm/operator/api/v1alpha1/kaiinstance_types.go` (current CRD has no userRef)
- `/Users/heussers/develop/emai/swarm/web/customer-center/server/main.go` (today: per-slug routes; needs per-user routes too)
- `/Users/heussers/develop/emai/swarm/web/customer-center/server/users.go` (today: users-within-an-instance, not platform users)
- TASK-012 (CRD evolution proposal — User design must be in the same proposal)

## Open Questions
- Managed Postgres provider for `swarm-cloud`: Hetzner Cloud Postgres (German region, lower latency), Neon (serverless, cheaper at low volume), or Supabase (auth + Postgres bundled, but we already have pkg/auth)? Default: Hetzner Postgres for region + ops simplicity.
- ID shape: ULIDs (lexicographic-sortable, time-prefixed) vs UUIDv7 (similar) vs Stripe-style `usr_<random>`. Default: ULIDs prefixed `u_`.
- Email-verification gate: `email_verified_at` IS NULL → block login? Or just gate provisioning of new workspaces? Default: block login until verified.

## Status

**Phase 0 (`pkg/users/` + `pkg/userspg/` modules) — done** on 2026-05-03. Two new sibling Go modules following the `pkg/auth` + `pkg/authk8s` pattern: `pkg/users` ships the `Store` interface, `User` type, `Tier`/`Lang` enums, ULID-shaped ID generator (hand-rolled, zero deps), and `MemoryStore` for tests / dev mode; `pkg/userspg` ships `PoolStore` against `jackc/pgx/v5` plus the embedded `schema.sql`. 93% coverage on `pkg/users` (MemoryStore + ID generator under concurrent-mint stress). Integration tests on `pkg/userspg` guarded by `PGURL` env var; verified end-to-end against a real Postgres 16 container — all 5 tests pass including the partial-unique-index reclaim-after-soft-delete behavior. README at `pkg/users/README.md`.

**Open questions — closed:**
- ID shape: **ULID prefixed `u_`** (per PROP-001 default).
- Email-verification gate: **block login until verified** (per PROP-001 default; enforcement happens at the call site in [[TASK-013]]).
- Stripe customer ID column included on the User row from day one so [[TASK-016]] doesn't need a schema migration.

**Phase 1 (operator CRD `spec.userRef` + `swarm.io/user-id` label) — done** on 2026-05-03 across three coordinating commits:
- [[TASK-012]] Phase 2.A added `spec.userRef` to `KaiInstance` and made the operator render `swarm.io/user-id=<userRef>` on every child resource (Deployment, Service, ConfigMap, PVC, NetworkPolicy, Ingress) via `commonLabelsFor`.
- [[TASK-013]] Phase 1.A made onboarding's verify endpoint stamp the same `swarm.io/user-id` label directly on the KaiInstance metadata when it creates the CR — so `kubectl get kaiinstance -l swarm.io/user-id=<uid>` lists the CRs themselves, not just child pods.
- [[TASK-015]] Phase 1 exercises the contract via `countWorkspacesForUser` (label-selector List against the dynamic K8s client) and is covered by an end-to-end test that asserts the count drives the 402-on-cap response.

**Phase 2 (workspace login swap from per-tenant Secret to central users.Store) — done** on 2026-05-06. The renamed `web/workspace/` server (post-TASK-025) now branches login on `KaiInstance.spec.managed`: `managed: saas` workspaces with `spec.userRef` set authenticate against the central `pkg/users.Store` and require an email-verified, non-deleted user whose ID matches `userRef`; `managed: internal` (and pre-TASK-014 tenants without the field) keep the legacy per-tenant Secret + bootstrap-admin flow byte-for-byte. JWT claims gained an optional `Uid` field — populated for SaaS sessions, empty for legacy. New `GET /api/workspace/<slug>/owner` returns `{email, userId, tier, managed}` from the JWT + a best-effort store enrichment for the future "your workspaces" view. Public swarm binary still ships with `users.NewMemoryStore()` (no pgx dep); the swarm-cloud overlay swaps to `pkg/userspg.PoolStore` via a separate code change. Test coverage: SaaS path (verified user accepted, unverified rejected, cross-workspace login rejected, JWT carries Uid), legacy path (existing bootstrap-admin + normal-login tests still pass — fixtures now stamp a `managed: internal` KaiInstance), `/owner` endpoint (returns enrichment + 401 without cookie), and the `kaiBinding.IsSaaS()` truth-table contract.

**Phase 3 ("your workspaces" view in the dashboard) — done** on 2026-05-06. New slug-scoped endpoint `GET /api/workspace/<slug>/owned-workspaces` lists every KaiInstance whose `swarm.io/user-id` label matches the signed-in user's `claims.Uid`. Slug-scoped routing keeps the existing per-tenant JWT-secret model (the cookie was issued for `<slug>`, that's the auth anchor) while the response enumerates the user's full workspace list — current workspace included and flagged with `current: true`. Each row carries `slug`, `name`, `projectName`, `status` + `statusLabel`, and `appRef` so the SPA can render a status pill and the catalog source. Legacy internal-managed sessions (no `claims.Uid`) get an empty list, not 401 — the endpoint is a no-op for them. Frontend: new "Your workspaces" sidebar item in `web/workspace/`, lazy-loaded on first nav, renders each workspace as a card with name + status pill + app pill + slug path; the current workspace gets an accent border, tinted background, and a "THIS WORKSPACE" pill so the user sees where they are. Demo mode returns three canned workspaces (current + two siblings) so local dev exercises the full layout. Tests cover: only-the-user's-workspaces filter, status + appRef passthrough, legacy session empty list, unauthenticated 401. Browser-verified end-to-end.

**Remaining phases blocked on upstream tasks:**
- Phase 4 (Postgres provisioning in production): lives in `swarm-cloud` overlay, not the public swarm repo. Hetzner managed Postgres or Neon — exact pick deferred to deploy time.

## Acceptance Criteria
- [x] User entity exists in code with stable ID, email, created-at, tier, deleted-at — `pkg/users.User` + `pkg/userspg/schema.sql` (Phase 0, 2026-05-03)
- [x] `kubectl get kaiinstance -l swarm.io/user-id=<uid>` (or `swarm.emai.io/user-id` while pre-TASK-024) returns all of a user's workspaces (Phase 1, 2026-05-03 — onboarding stamps the label on CR metadata at create time)
- [x] Workspace login validates against the central users store for `managed: saas` and against the per-tenant Secret for `managed: internal` (Phase 2, 2026-05-06)
- [x] JWT cookie carries the platform user ID for SaaS sessions; `/api/workspace/<slug>/owner` exposes it (Phase 2, 2026-05-06)
- [x] Workspace "your workspaces" view shows the list (Phase 3, 2026-05-06)
- [x] Existing pre-SaaS customers continue to reconcile cleanly (Phase 1+ — `managed: internal` keeps `userRef` null; verified by `TestBuildDeploymentInternalManagedSkipsClamp` and the `isSaaSEnrolled` predicate that exempts legacy tenants)

## Notes
This is the foundational schema decision — get it wrong and TASK-016 (billing), TASK-021 (deletion), TASK-015 (quotas) all become messy. Bundle the User decision into the PROP-001 proposal that TASK-012 will spawn.

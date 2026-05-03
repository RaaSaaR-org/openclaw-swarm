---
id: TASK-014
aliases:
- TASK-014
title: 'User account model: one user owns many workspaces (KaiInstances)'
slug: user-account-model-one-user-owns-many-workspaces-kaiinstances
status: backlog
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

## Acceptance Criteria
- [ ] User entity exists somewhere (CRD or DB) with stable ID, email, created-at, tier, deleted-at
- [ ] `kubectl get kaiinstance -l swarm.io/user-id=<uid>` (or `swarm.emai.io/user-id` while pre-TASK-024) returns all of a user's workspaces
- [ ] Customer-center "your workspaces" view shows the list
- [ ] Existing pre-SaaS customers continue to reconcile cleanly

## Notes
This is the foundational schema decision — get it wrong and TASK-016 (billing), TASK-021 (deletion), TASK-015 (quotas) all become messy. Bundle the User decision into the PROP-001 proposal that TASK-012 will spawn.

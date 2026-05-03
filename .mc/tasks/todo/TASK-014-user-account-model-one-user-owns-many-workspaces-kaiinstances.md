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

## What
- Decide where User lives: a new CRD (`SwarmUser`), a separate Postgres-backed user service, or just a K8s Secret per user. CRD is the most consistent with the rest of the platform; Postgres is more conventional for user accounts.
- Add `spec.userRef` (or label `swarm.io/user-id` once TASK-024 renames the API group; today's group is `swarm.emai.io`) to `KaiInstance` so we can list "all instances belonging to user X".
- Customer-center grows a "your workspaces" view + a "create new workspace" action.
- Migration: existing customers in `swarm-config/` need to be associated with a synthetic admin user (or kept as untenanted "platform-managed").

## References
- `/Users/heussers/develop/emai/swarm/operator/api/v1alpha1/kaiinstance_types.go` (current CRD has no userRef)
- `/Users/heussers/develop/emai/swarm/web/customer-center/server/main.go` (today: per-slug routes; needs per-user routes too)
- `/Users/heussers/develop/emai/swarm/web/customer-center/server/users.go` (today: users-within-an-instance, not platform users)
- TASK-012 (CRD evolution proposal — User design must be in the same proposal)

## Open Questions
- CRD or Postgres for User? CRD = consistent platform; Postgres = conventional for billing/email/audit.
- One workspace per user (free) and many on paid tiers, or always many?
- How do shared workspaces work (a team of users on one assistant)? Out of scope for v1?

## Acceptance Criteria
- [ ] User entity exists somewhere (CRD or DB) with stable ID, email, created-at, tier, deleted-at
- [ ] `kubectl get kaiinstance -l swarm.io/user-id=<uid>` (or `swarm.emai.io/user-id` while pre-TASK-024) returns all of a user's workspaces
- [ ] Customer-center "your workspaces" view shows the list
- [ ] Existing pre-SaaS customers continue to reconcile cleanly

## Notes
This is the foundational schema decision — get it wrong and TASK-016 (billing), TASK-021 (deletion), TASK-015 (quotas) all become messy. Bundle the User decision into the PROP-001 proposal that TASK-012 will spawn.

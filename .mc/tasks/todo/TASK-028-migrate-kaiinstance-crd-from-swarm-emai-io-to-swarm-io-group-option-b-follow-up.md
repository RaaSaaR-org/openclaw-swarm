---
id: TASK-028
aliases:
- TASK-028
title: Migrate KaiInstance CRD from swarm.emai.io to swarm.io group (Option B follow-up)
slug: migrate-kaiinstance-crd-from-swarm-emai-io-to-swarm-io-group-option-b-follow-up
status: backlog
priority: 3
owner: ''
projects: []
customers: []
tags:
- operator
- rename
- saas
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-10
updated: 2026-05-10
---


# Migrate KaiInstance CRD from swarm.emai.io to swarm.io group (Option B follow-up)

## Why
[[TASK-024]] Phase 5 (2026-05-10) shipped **Option A** of the architectural fork: the field rename `customerName/customerSlug` → `tenantName/tenantSlug` landed in v1alpha2 inside the existing `swarm.emai.io` API group, with a real conversion webhook handling old v1alpha1 manifests. **Option B** (renaming the group itself to `swarm.io`) was deferred because it can't be done in-place — k8s CRDs don't support API-group renames. The only path is a one-time migration: create a new CRD at `kaiinstances.swarm.io`, port every existing CR under the new group, repoint every consumer, drop the old CRD after one release.

The trigger to actually do this is when `swarm-emai` has been quiet for a sprint (no live incidents) AND the SaaS overlay is mature enough to absorb a coordinated overlay re-apply.

## What
1. **New CRD at `swarm.io/v1alpha2`** — duplicate the schema currently at `swarm.emai.io/v1alpha2` under the new group (no schema differences). Generate via `controller-gen` from a new `operator/api/swarm/v1alpha2/` package.
2. **Operator dual-watches** for one cycle. Reconciles both `swarm.emai.io/v1alpha2` and `swarm.io/v1alpha2` — same internal types, both groups feed the same reconcile loop.
3. **Migration script** in `swarm-emai`: walks every `kaiinstances.swarm.emai.io` CR, applies an equivalent `kaiinstances.swarm.io` CR, then deletes the old one. Idempotent — safe to re-run. Uses dry-run + diff before any actual write.
4. **5 web apps** flip their `kaiInstanceGVR` from `swarm.emai.io/v1alpha2` to `swarm.io/v1alpha2` (one-line change per app).
5. **Drop the old CRD** after one release where both groups coexisted and zero CRs remain at `swarm.emai.io`.
6. **Annotation rename**: `swarm.emai.io/tenant-links` → `swarm.io/tenant-links`. Reader stays backwards-compatible for one cycle.

## References
- [[TASK-024]] — original de-EmAI-ify task; Option A status section explains why this was split out.
- [[TASK-012]] Phase 2.B status — context on the conversion webhook + cert-manager wiring already in place; group rename can reuse that infrastructure.
- `/Users/heussers/develop/emai/swarm/operator/api/v1alpha2/` — current location; the swarm.io/v1alpha2 package would be a sibling.
- swarm-emai overlays — the overlay-coordination risk is the main reason this was deferred.

## Acceptance Criteria
- [ ] New CRD `kaiinstances.swarm.io` v1alpha2 served + stored in the cluster.
- [ ] Migration tool re-applies every existing `swarm.emai.io` CR under the new group, idempotent + diffable before write.
- [ ] All 5 web apps point at `swarm.io/v1alpha2`; CI green; no remaining `swarm.emai.io/v1alpha*` strings in the operator/web-app source.
- [ ] One release where both CRDs coexist; downstream tooling (rmsctl, swarm-ctl, kubectl users) had a chance to migrate.
- [ ] Old `kaiinstances.swarm.emai.io` CRD deleted; `swarm-emai` overlays no longer reference it.
- [ ] Annotation `swarm.emai.io/tenant-links` → `swarm.io/tenant-links` migration; legacy read fallback dropped after one release.

## Notes
**Don't open this task until** `swarm-emai` has been incident-free for a sprint and the SaaS overlay is in a stable shape — the "every overlay's manifests change at once" footprint is the main risk. Prefer a dedicated maintenance window over rolling this in alongside other operator changes.

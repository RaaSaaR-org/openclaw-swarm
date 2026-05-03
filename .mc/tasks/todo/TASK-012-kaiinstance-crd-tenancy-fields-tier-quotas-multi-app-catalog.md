---
id: TASK-012
aliases:
- TASK-012
title: 'KaiInstance CRD: tenancy fields (tier, quotas) + multi-app catalog'
slug: kaiinstance-crd-tenancy-fields-tier-quotas-multi-app-catalog
status: in-progress
priority: 3
owner: ''
projects: []
customers: []
tags:
- operator
- saas
- crd
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---



# KaiInstance CRD: tenancy fields (tier, quotas) + multi-app catalog

## Why
This is the **strategic** task that gates the SaaS direction. Today's `KaiInstance` CRD is a fine "one customer = one OpenClaw pod" primitive but lacks the fields a SaaS product needs:
- No `tier` / `plan` field — can't charge differently or apply different defaults
- No quota field beyond `spec.resources` (which is optional and per-instance, not per-tenant)
- No `apps[]` array — one customer is hardwired to one agent persona, no plugin/extension catalog
- No `observedGeneration` in status — operator can't reliably detect stale spec
- No `costCenter` / `org` labels — multi-customer cost attribution is impossible

This is the boundary between "internal customer management tool" and "SaaS platform". Hold this task open as a design discussion before implementing — getting the CRD wrong is expensive (CRD migrations are painful).

## What
**Phase 1 — design (no code):** Write `docs/proposals/PROP-001-saas-crd-evolution.md` (or use `mc new proposal`) covering:
- Should multi-app live on `KaiInstance` (`spec.apps[]`) or as a sibling CRD `KaiApp` referencing `KaiInstance`?
- How does `tier` interact with the `Resources` defaults — server-side default-merging vs. webhook-injected?
- What does deletion semantics look like for multi-app (deleting an app vs. deleting the whole tenant)?
- Migration path from v1alpha1 → v1alpha2 (conversion webhook? hand-edit?).

**Phase 2 — implement (after PROP-001 accepted):**
- Add `spec.tier` (enum: `free|starter|growth|enterprise`)
- Add `spec.org` / `spec.costCenter` (free-form string)
- Add `status.observedGeneration`
- Either `spec.apps []AppRef` or new `KaiApp` CRD
- ValidatingAdmissionWebhook to enforce tier-based quotas

## References
- `/Users/heussers/develop/emai/swarm/operator/api/v1alpha1/kaiinstance_types.go` (current CRD — only ~80 lines of spec)
- `/Users/heussers/develop/emai/swarm/operator/internal/controller/kaiinstance_controller.go` (reconciler that needs updating)
- Recent commit `49e4a68 feat(operator): per-customer OpenRouter key + lean openclaw.json` — example of recent CRD-shape evolution
- Prior conversation context: user asked whether swarm should generalize to a SaaS platform with apps catalog (this task is the architectural answer)
- Kubebuilder CRD conversion webhooks: https://book.kubebuilder.io/multiversion-tutorial/conversion
- Reference SaaS operator patterns: https://operatorframework.io/

## Status

**Phase 1 (PROP-001) — done** on 2026-05-03. See [[PROP-001]] for the full decision: User entity in Postgres (not a CRD), multi-app stays on `KaiInstance` as `spec.appRef` (single app today; `spec.apps[]AppRef` reserved for additive future change), v1alpha1 → v1alpha2 conversion bundled with the customer→tenant rename ([[TASK-024]]).

**Phase 2.A (additive fields on v1alpha1) — done** on 2026-05-03. Five new optional fields landed on `KaiInstanceSpec`: `tier` (enum), `userRef` (Postgres user.id), `appRef` (catalog persona slug), `org` (cost-center label), `managed` (saas|internal). All optional, all default to empty, all skip cleanly when unset so existing tenants in `swarm-emai`/`swarm-config` keep their current rendered output. Operator now renders `swarm.io/{user-id, tier, app, org, managed}` labels on every child resource (Deployment, Service, ConfigMap, PVC, NetworkPolicy, Ingress) when the matching Spec field is set. Generated CRD (`config/crd/bases/swarm.emai.io_kaiinstances.yaml`) regenerated. Four new operator tests cover both branches (empty Spec → no SaaS labels; populated Spec → all labels rendered + propagated to pod template).

**Remaining work:**
- Phase 2.B (v1alpha2 + conversion webhook): the actual rename (`customerSlug` → `tenantSlug`, `customerName` → `tenantName`, API group `swarm.emai.io` → `swarm.io`) bundled with [[TASK-024]] phases 2-5. Deliberately deferred — `git mv`-shaped renames are a coordinated deploy.
- Phase 2.C (ValidatingAdmissionWebhook for tier-based quotas): blocked on TASK-019 Phase 1 (per-tier resource defaults) so the webhook has something concrete to enforce.

## Acceptance Criteria
**Phase 1:**
- [x] PROP-001 written, reviewed, decision recorded (2026-05-03)
- [x] Decision on multi-app shape (sub-resource vs. sibling CRD) is locked in — `spec.appRef` single-app today, `spec.apps[]` reserved for additive future change

**Phase 2:**
- [x] CRD updated with new fields (`tier`, `userRef`, `appRef`, `org`, `managed`), generated manifests committed (Phase 2.A, 2026-05-03)
- [x] Existing customers continue to reconcile cleanly — new fields are optional and default to empty (Phase 2.A)
- [ ] ValidatingWebhook rejects out-of-tier resource requests (Phase 2.C, blocked on TASK-019)
- [x] Test coverage for label rendering with both populated and empty Spec fields (Phase 2.A)

## Notes
**Do not start Phase 2 without Phase 1 sign-off.** This is the highest-leverage CRD change since v1alpha1 was created — getting it right matters more than shipping fast. Hold this task open in `backlog` until the SaaS direction is committed.

---
id: TASK-012
aliases:
- TASK-012
title: 'KaiInstance CRD: tenancy fields (tier, quotas) + multi-app catalog'
slug: kaiinstance-crd-tenancy-fields-tier-quotas-multi-app-catalog
status: backlog
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

## Acceptance Criteria
**Phase 1:**
- [ ] PROP-001 written, reviewed, decision recorded
- [ ] Decision on multi-app shape (sub-resource vs. sibling CRD) is locked in

**Phase 2:**
- [ ] CRD updated with new fields, generated manifests committed
- [ ] Existing customers (in swarm-config) still reconcile cleanly
- [ ] ValidatingWebhook rejects out-of-tier resource requests
- [ ] Test coverage for default-merging and webhook validation

## Notes
**Do not start Phase 2 without Phase 1 sign-off.** This is the highest-leverage CRD change since v1alpha1 was created — getting it right matters more than shipping fast. Hold this task open in `backlog` until the SaaS direction is committed.

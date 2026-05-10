---
id: TASK-012
aliases:
- TASK-012
title: 'KaiInstance CRD: tenancy fields (tier, quotas) + multi-app catalog'
slug: kaiinstance-crd-tenancy-fields-tier-quotas-multi-app-catalog
status: done
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
updated: 2026-05-10
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

**Phase 2.B (v1alpha2 + conversion webhook) — done** on 2026-05-10. Picked **Option A** of the architectural fork (kept the API group `swarm.emai.io`, dropped `customerName`/`customerSlug` from v1alpha2 only) — Option B (rename group to `swarm.io`) requires a one-time migration of every CR in `swarm-emai` and was deferred to a follow-up release for risk reasons. Concrete drop:
- New `operator/api/v1alpha2/` package with the tenant-clean schema (drops `customerName`/`customerSlug` entirely; `tenantName` required, `tenantSlug` optional). v1alpha2 is `+kubebuilder:storageversion`, marks itself as the conversion `Hub` (`Hub()` method).
- `operator/api/v1alpha1/kaiinstance_conversion.go` implements `ConvertTo(Hub)` + `ConvertFrom(Hub)`. Five conversion tests cover tenant-wins-over-customer precedence, legacy-only fold, all nested/optional fields (Telegram, GatewayAuth, Resources, ExternalAccess, Status), v1alpha2→v1alpha1 round-trip populates BOTH name fields.
- Operator reconciler + controller tests + suite mass-renamed v1alpha1 → v1alpha2; reads `kai.Spec.TenantName` / `kai.Spec.TenantSlug` directly (the conversion webhook handles old v1alpha1 manifests at the API-server boundary).
- Webhook server registered in `cmd/main.go` via `mgr.GetWebhookServer().Register("/convert", conversion.NewWebhookHandler(...))`.
- All 5 web apps repointed at `swarm.emai.io/v1alpha2` GVR (operator, chat, workspace, admin-console, onboarding, status-page). Onboarding's `buildSaaSKaiInstance` now writes `spec.tenantName` / `spec.tenantSlug`.
- Generated CRD manifest now serves both versions (`v1alpha1` legacy, `v1alpha2` storage); kustomize chain wires conversion (`config/crd/patches/webhook_in_kaiinstances.yaml`), CA injection (`cainjection_in_kaiinstances.yaml`), webhook Service (`config/webhook/service.yaml`), cert-manager Issuer + Certificate (`config/certmanager/`), and a Deployment patch that mounts the cert + opens port 9443 (`config/default/manager_webhook_patch.yaml`).
- Sample manifests (`operator/config/samples/*.yaml`, `quickstart.yaml`, `templates/{project,research,support}-assistant/kai.yaml.tmpl`) bumped to `apiVersion: swarm.emai.io/v1alpha2` + `tenantName`/`tenantSlug`.
- `kubectl apply -f` of an existing v1alpha1 manifest in `swarm-emai` keeps working — the conversion webhook folds `customerName` → `tenantName` (and `customerSlug` → `tenantSlug`) on the API-server side. RBAC verbs stay the same (`apiGroups: ["swarm.emai.io"]` covers all versions). The `operator-system` namespace must have cert-manager installed before this deploy lands; without cert-manager the CRD's `caBundle` stays empty and the kube-apiserver refuses every operation on KaiInstance CRs.

All operator tests green (api/v1alpha1, internal/controller); all 5 web app test suites green.

**Phase 2.C (ValidatingAdmissionWebhook for tier-based quotas) — done** on 2026-05-10. Bundled with [[TASK-015]] Phase 2 — the same webhook satisfies both tasks. `operator/internal/webhook/v1alpha2/kaiinstance_webhook.go` rejects creates/updates whose `spec.resources` exceed `pkg/quotas.For(tier)` ceilings; SaaS-enrolled tenants only (`spec.tier` set + `spec.managed != "internal"`). Seven webhook tests cover legacy passthrough, internal-managed passthrough, over-tier rejection, within-tier acceptance, update-path check, unknown-tier rejection, delete always allowed. Generated `config/webhook/manifests.yaml` ValidatingWebhookConfiguration uses the same cert-manager-injected CA bundle as the conversion webhook.

**Remaining work (out of scope for this task):**
- Group rename `swarm.emai.io` → `swarm.io`: deferred to a follow-up release. End-state of Option B in the architectural fork — tracked in [[TASK-028]]. swarm-emai overlays would re-apply every manifest under the new group. Not an acceptance criterion of this task; listed for context.

## Acceptance Criteria
**Phase 1:**
- [x] PROP-001 written, reviewed, decision recorded (2026-05-03)
- [x] Decision on multi-app shape (sub-resource vs. sibling CRD) is locked in — `spec.appRef` single-app today, `spec.apps[]` reserved for additive future change

**Phase 2:**
- [x] CRD updated with new fields (`tier`, `userRef`, `appRef`, `org`, `managed`), generated manifests committed (Phase 2.A, 2026-05-03)
- [x] Existing customers continue to reconcile cleanly — new fields are optional and default to empty (Phase 2.A); v1alpha1 manifests still apply via the conversion webhook (Phase 2.B, 2026-05-10)
- [x] ValidatingWebhook rejects out-of-tier resource requests (Phase 2.C, 2026-05-10 — bundled with TASK-015 Phase 2; `operator/internal/webhook/v1alpha2.KaiInstanceValidator` + 7 tests)
- [x] Test coverage for label rendering with both populated and empty Spec fields (Phase 2.A); conversion roundtrip + nested-field tests added (Phase 2.B, 2026-05-10)
- [x] Phase 2.B: v1alpha2 schema + conversion webhook (2026-05-10)

## Notes
**Do not start Phase 2 without Phase 1 sign-off.** This is the highest-leverage CRD change since v1alpha1 was created — getting it right matters more than shipping fast. Hold this task open in `backlog` until the SaaS direction is committed.

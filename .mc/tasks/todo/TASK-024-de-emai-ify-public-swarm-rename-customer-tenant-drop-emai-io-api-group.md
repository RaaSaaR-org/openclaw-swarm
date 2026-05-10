---
id: TASK-024
aliases:
- TASK-024
title: 'De-EmAI-ify public swarm: rename customer→tenant, drop emai.io API group'
slug: de-emai-ify-public-swarm-rename-customer-tenant-drop-emai-io-api-group
status: in-progress
priority: 2
owner: ''
projects: []
customers: []
tags:
- rename
- refactor
- saas
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-10
---



# De-EmAI-ify public swarm: rename customer→tenant, drop emai.io API group

## Why
Public `swarm` is meant to be a forkable open-source platform — anyone should be able to run their own SaaS or self-hosted deployment. Today the codebase bakes in EmAI-specific terminology and identifiers that don't make sense to outsiders:
- "customer" everywhere — but a B2C SaaS user is a *user*, not a customer; a stranger's fork has *users* / *tenants*, not "customers"
- API group `swarm.emai.io/v1alpha1` — embeds EmAI's domain in every CRD, every kubectl command, every YAML in every fork
- Various `emai.io/customer=<slug>` labels with the same problem

This is the hygiene work that has to land before TASK-023 (three-repo split) can really claim the public repo is generic.

## What
**Terminology mapping — apply consistently across code, docs, file paths, identifiers:**

| Old (EmAI-specific) | New (generic) | Notes |
|---|---|---|
| customer | tenant *(K8s/code)* / user *(product copy)* | "tenant" is the multi-tenancy primitive; "user" is the person |
| customerSlug | slug *or* tenantSlug | drop prefix where unambiguous |
| customerName | displayName *or* tenantName | display field |
| customer-chat | chat | web app dir + binary |
| customer-center | center *or* dashboard | web app dir + binary |
| customer-template | default-template | agent template dir |
| `swarm.emai.io/v1alpha1` | `swarm.io/v1alpha1` (or `kai.io`) | CRD API group |
| `emai.io/customer=<slug>` | `swarm.io/tenant=<slug>` | K8s label |
| `kai-<slug>-*` | keep prefix (already generic) | Secret/ConfigMap names |

**Migration plan (non-trivial — CRD field renames break existing manifests):**
1. **Phase 1 (additive):** Add new field names alongside the old; operator accepts either; warn on the old name.
2. **Phase 2 (mass rewrite):** Update `swarm-config`/`swarm-emai` manifests to new names.
3. **Phase 3 (remove old):** Drop old field names via a v1alpha2 with a conversion webhook (bundle with TASK-012 to avoid two CRD migrations).
4. **Web app dirs:** Rename in one go (`git mv` preserves history). Update Dockerfiles, CI matrix, image names.
5. **API group rename:** Coordinate with all consumers — every YAML in every deployment changes. Most expensive single step.

**Out of scope** (separate follow-ups):
- Registry rename (`RaaSaaR-org` is a GitHub-org-level decision)
- Project rename (still called "swarm")

## References
- `/Users/heussers/develop/emai/swarm/operator/api/v1alpha1/kaiinstance_types.go` (customerName, customerSlug fields)
- `/Users/heussers/develop/emai/swarm/operator/PROJECT` (API group declared here)
- `/Users/heussers/develop/emai/swarm/web/customer-chat/` and `web/customer-center/`
- `/Users/heussers/develop/emai/swarm/agents/customer-template/`
- `/Users/heussers/develop/emai/swarm/scripts/{new,provision,teardown}-customer.sh`
- Memory: `feedback_no_customer_in_public_swarm.md` (durable rule going forward)
- TASK-023 (three-repo split — *why* this matters now)
- TASK-012 (CRD evolution — bundle this rename into v1alpha1→v1alpha2)
- Kubebuilder conversion webhook: https://book.kubebuilder.io/multiversion-tutorial/conversion

## Open Questions
- Replace `swarm.emai.io` with `swarm.io`, `kai.io`, or `openclaw.ai`? Recommend `swarm.io` (matches the project name and `swarm-cloud`/`swarm-emai` repo names).
- "Tenant" or "Workspace" as the primary term for the K8s resource? Recommend **tenant** in K8s/operator code, **workspace** in product UI (impl term vs. product noun).
- Keep `kai-` prefix on auto-generated K8s resource names? Yes — generic and grep-friendly.

## Status

**Phase 1.A (additive K8s label) — done** on 2026-05-03. Operator-rendered child resources now carry `swarm.io/tenant=<slug>` alongside the legacy `emai.io/customer=<slug>`. New tooling can already select on the generic label; existing NetworkPolicy podSelectors and any kubectl filters in `swarm-emai`/`swarm-config` keep working unchanged. Test in `resources_test.go::TestCommonLabelsCarriesBothLegacyAndNewTenantLabel` asserts the contract so the legacy label can't accidentally disappear before the v1alpha2 bump.

**Phase 1.B (docs sweep) — done** on 2026-05-03. Rewrote prose `customer` references in `README.md`, `docs/architecture.md`, `docs/customer-onboarding.md`, `docs/deployment-guide.md`, and one description in `docs/api/center.yaml` to use **tenant** / **user** / **workspace** instead. API contract names (the `customer-chat`/`customer-center`/`customer-template` directories, `customerName`/`customerSlug` CRD fields, `emai.io/customer` label, `--customer` CLI flag, `provision-customer.sh` filename) deliberately kept — they're locked to Phase 2-5 (CRD bump + dir renames + CLI rename, all coordinated with [[TASK-012]] v1alpha2 + a deploy). Audit confirms zero non-contract `customer` refs remain in public-repo prose. The `docs/customer-onboarding.md` filename itself is now the only non-contract leak; its content was rewritten as a tenant-onboarding checklist with a note that the doc long-term moves to `swarm-emai`.

**Phase 2 (CRD additive `tenantName` + `tenantSlug`) — done** on 2026-05-04. Two new optional fields on v1alpha1 `KaiInstanceSpec`: `tenantName` (max 100 chars) and `tenantSlug` (DNS-safe, max 63 chars). When set, they take precedence over the legacy `customerName`/`customerSlug` — existing manifests in swarm-emai/swarm-config keep working unchanged because the tenant fields are additive overrides, not replacements. Two helper methods on the Spec (`EffectiveName()`, `EffectiveSlug()`) route through the tenant fields first; the operator's reconciler uses them everywhere it previously read `kai.Spec.CustomerName`/`CustomerSlug`. The legacy `customerName` field stays required at the OpenAPI level (existing manifests still validate); v1alpha2 + the conversion webhook drops `customerName` entirely. Four-case precedence test (legacy only / tenant overrides legacy / tenant only / mixed) covers the helper logic. Generated CRD (`config/crd/bases/swarm.emai.io_kaiinstances.yaml`) regenerated.

**Phase 4 (web app dir rename) — done** between 2026-05-04 and 2026-05-06 via the sibling [[TASK-025]] (rename `customer-center` → `workspace`) and [[TASK-026]] (agent-editor SPA spike). The rename target evolved from the original "center" proposal here to **`workspace`** — TASK-025's status note explains the call. End state in `swarm/web/`:
```
admin-console  chat  onboarding  shared  status-page  workspace
```
Image names + Dockerfiles + CI matrix in `release.yml` all carry the new names; `git grep customer-chat\|customer-center` returns zero hits in non-test source. TASK-024 keeps the credit because this is the same physical change the original Phase 4 description called for; only the destination name differs.

**Phase 5 (drop `customerName`/`customerSlug` in v1alpha2 + conversion webhook) — done** on 2026-05-10. Picked **Option A** of the architectural fork: kept the CRD's API group at `swarm.emai.io` (renaming to `swarm.io` would have required a one-time CR migration in `swarm-emai` and was deferred). All in-repo public-swarm code is now tenant-clean at the spec level — `kai.Spec.TenantName` / `kai.Spec.TenantSlug` are the only fields the operator and the 5 web apps read or write. v1alpha1 manifests already in `swarm-emai` keep applying via the conversion webhook; the field rename is invisible to existing overlays. See [[TASK-012]] Phase 2.B status for the per-file shipping notes (CRD types, conversion functions, webhook scaffolding, cert-manager wiring, sample manifests, all 5 web apps).

Annotation rename in the same drop: the workspace dashboard's per-tenant extra-links annotation moved from `swarm.emai.io/customer-links` → `swarm.emai.io/tenant-links`. The reader in `web/workspace/server/main.go` falls back to the legacy key for one release so existing overlays keep their links during migration.

**Phase 5.A (admin-console catch-up) — done** on 2026-05-10. Phase 5's "5 web apps" sweep missed the admin-console: `summarize()` was reading the dropped `spec.customerName`/`spec.customerSlug` paths against v1alpha2 objects and rendering empty strings for every tenant. Fixed in `web/admin-console/`:
- `server/main.go`: `instanceSummary.CustomerName/CustomerSlug` → `TenantName/TenantSlug` (JSON tags too); `summarize()` reads `spec.tenantName` / `spec.tenantSlug` first, falls back to legacy `customerName`/`customerSlug` paths so a cluster mid-migration with v1alpha1 stragglers still renders. `firstNonEmpty` widened to variadic to support the 4-way fallback chain on the slug (status.tenantSlug → status.customerSlug → spec.tenantSlug → spec.customerSlug).
- `src/api.ts` / `src/main.ts`: `customerName`/`customerSlug` field references → `tenantName`/`tenantSlug`; CSS classes `customer-cell`/`customer-name`/`customer-slug` → `tenant-*`; column header `<th>Customer</th>` → `<th>Tenant</th>`.
- `src/style.css`: matching class renames.
- `server/main_test.go`: fixture `newKai` writes the v1alpha2-shaped fields; new `TestSummarize_FallsBackToLegacyCustomerFields` locks in the v1alpha1 backward-compat path; `TestFirstNonEmpty` extended for the variadic shape.
- All admin-console server tests + tsc + vite build green.

**Remaining phases:**
- Phase 3 (`swarm-emai` / `swarm-config` mass rewrite) — **partial, 2026-05-10**:
  - **Done**: scripts switched from `emai.io/customer=<slug>` to `swarm.io/tenant=<slug>` (`scripts/onboard.sh` 3 sites, `scripts/swarm-sync.sh` 1 site). Verified live on emai-cloud — both labels resolve to the same pod (operator's additive label has been rendering since Phase 1.A).
  - **Blocked**: KaiInstance manifest rename (`environments/cloud/kai-*.yaml`: `customerName/customerSlug` → `tenantName/tenantSlug`). Dry-run against emai-cloud fails with `unknown field "spec.tenantName"` — the CRD on emai-cloud is still on the original v1alpha1 schema (no tenant fields) since the public swarm Phase 2 additive fields + Phase 5 conversion webhook haven't been deployed to that cluster yet. Flip the manifests as part of the next operator upgrade window on emai-cloud.
- Group rename `swarm.emai.io` → `swarm.io`: deferred to a follow-up release. Option B end-state. Requires re-applying every CR in `swarm-emai` under the new group + a one-cycle dual-CRD bridge. Belongs in its own task with a planned deploy window.

## Acceptance Criteria
- [x] No file path, identifier, label, or CRD field in public `swarm` contains the literal word "customer" — *partial*: as of Phase 5 (2026-05-10) all spec-write code paths are tenant-clean (operator + 5 web apps + onboarding signup + sample manifests + templates). Remaining "customer" references are: (a) the v1alpha1 schema's `CustomerName`/`CustomerSlug` fields, kept on purpose so existing manifests in `swarm-emai` continue to validate; (b) "customer" in v1alpha1's type-doc comments, retiring with v1alpha1 itself; (c) the legacy `swarm.emai.io/customer-links` annotation key, read as a fallback in `web/workspace/server/main.go` for one release before the legacy fallback drops.
- [ ] CRD API group renamed `swarm.emai.io` → `swarm.io` — deferred to a follow-up release (Option B end-state). Conversion webhook for the v1alpha1 → v1alpha2 schema part shipped in Phase 5 (2026-05-10).
- [ ] `swarm-emai` / `swarm-config` updated to use the new field/label/dir names (Phase 3, private repo) — optional short-term: v1alpha1 manifests still apply through the conversion webhook.
- [x] Docs and READMEs updated; no surviving non-contract "customer" references in public-repo docs (Phase 1.B, 2026-05-03)
- [x] Existing internal tenants continue to reconcile correctly throughout the migration (verified by the conversion roundtrip tests in `api/v1alpha1/kaiinstance_conversion_test.go` + the operator's controller_test suite green on the renamed types, Phase 5)
- [x] Phase 1.A: additive `swarm.io/tenant=<slug>` label rendered on every operator-managed child resource (2026-05-03)

## Notes
**This is the largest single rename in the project's history.** Bundle with TASK-012 (v1alpha1→v1alpha2 CRD migration) rather than doing two back-to-back. Land TASK-023 conceptually first (so the boundary makes sense), do this one as the actual code change.

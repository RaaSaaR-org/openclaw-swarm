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
updated: 2026-05-03
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

**Remaining phases:**
- Phase 2 (CRD additive — `spec.tenantSlug`/`tenantName` alongside `customerSlug`/`customerName`; operator accepts either): bundle with [[TASK-012]] v1alpha2 work.
- Phase 3 (`swarm-emai` / `swarm-config` mass rewrite): private-repo work, blocked on Phase 2.
- Phase 4 (web app dir rename `customer-chat` → `chat`, `customer-center` → `center`): one-shot `git mv` + Dockerfile + CI matrix + image-name + K8s manifest update. Coordinated with a deploy because image names change.
- Phase 5 (CRD API group `swarm.emai.io` → `swarm.io`): conversion webhook for one minor version, every YAML in every overlay updated. Bundle with [[TASK-012]].

## Acceptance Criteria
- [ ] No file path, identifier, label, or CRD field in public `swarm` contains the literal word "customer" (verified by `grep -rn "customer" --exclude-dir=node_modules`) — Phase 5 end state; today only API-contract names remain
- [ ] CRD API group renamed; conversion webhook handles old-style manifests for one minor version (Phase 5)
- [ ] `swarm-emai` / `swarm-config` updated to use the new field/label/dir names (Phase 3, private repo)
- [x] Docs and READMEs updated; no surviving non-contract "customer" references in public-repo docs (Phase 1.B, 2026-05-03)
- [ ] Existing internal tenants continue to reconcile correctly throughout the migration (verified at each phase boundary)
- [x] Phase 1.A: additive `swarm.io/tenant=<slug>` label rendered on every operator-managed child resource (2026-05-03)

## Notes
**This is the largest single rename in the project's history.** Bundle with TASK-012 (v1alpha1→v1alpha2 CRD migration) rather than doing two back-to-back. Land TASK-023 conceptually first (so the boundary makes sense), do this one as the actual code change.

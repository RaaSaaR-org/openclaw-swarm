---
id: TASK-023
aliases:
- TASK-023
title: 'Three-repo split: swarm (public) / swarm-cloud (SaaS) / swarm-emai (internal)'
slug: three-repo-split-swarm-public-swarm-cloud-saas-swarm-emai-internal
status: in-progress
priority: 2
owner: ''
projects: []
customers: []
tags:
- structure
- saas
- split
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-10
---


# Three-repo split: swarm (public) / swarm-cloud (SaaS) / swarm-emai (internal)

## Why
Today `swarm` (public) intermingles generic platform code with EmAI-specific assumptions, and `swarm-config` carries everything that isn't checked into swarm. As we add a public SaaS layer (TASK-013..022), this becomes untenable: SaaS-specific config (Stripe webhook secret, Postmark API key, pooled OpenRouter key, CAPTCHA secrets, marketing site, public DNS topology) doesn't belong in either repo as currently scoped. The right shape is three repos with clear responsibilities — matches the GitLab CE/EE/.com, Sentry self-hosted/Cloud, Plausible CE/Cloud pattern.

## What
**Three repositories, three responsibilities:**

| Repo | Visibility | Purpose | Holds |
|---|---|---|---|
| **`swarm`** | public OSS | Generic platform | Operator, 5 web apps, `pkg/` libs, `agents/catalog/`, `agents/default-template/` (post-TASK-024), K8s base manifests, docs. **Zero EmAI-specific anything.** Anyone can fork and run their own deployment. |
| **`swarm-cloud`** | private | Our SaaS deployment | Production K8s overlay, Stripe webhook secret + price IDs, Postmark API key, pooled OpenRouter key, Cloudflare DNS creds, marketing site (or sibling `swarm-marketing` repo), pricing/abuse policies, Terraform for the SaaS cluster. Deploys to e.g. `kai.example.com`. |
| **`swarm-emai`** | private (renamed from `swarm-config`) | EmAI's internal-tenant deployment | KaiInstance manifests for hand-onboarded EmAI customers, per-customer SOUL.md/USER.md overrides, internal cluster deploy scripts, OpenRouter keys for those tenants, the swarm-ctl playbook. Deploys to existing `emai-cloud` cluster. |

**Coexistence rule:** every KaiInstance gets a label `swarm.io/managed: {internal|saas}` (or under whichever group survives TASK-024). Operator treats them identically; downstream branches on the label:
- billing webhooks (TASK-016) skip `managed: internal`
- quota webhook (TASK-015) skips `managed: internal`
- public signup (TASK-013) only ever creates `managed: saas`
- admin-console can filter/group by label

One operator codebase, two deployment shapes, no forking.

## Decided
- **Naming: rename `swarm-config` → `swarm-emai`** (locked in 2026-05-03 — see [[PROP-003]]). One-shot `gh repo rename` + clones / CI / deploy-script updates. Worth the half-hour for years of cleaner symmetry with `swarm-cloud`.
- **Cluster topology: two clusters** (locked in 2026-05-03 — see [[PROP-003]]). New `kai-cloud` cluster for SaaS; existing `emai-cloud` keeps EmAI internal tenants. ~€15/mo extra; SaaS abuse never affects internal customers.
- **Trigger to actually split:** wait until [[TASK-016]] (Stripe billing) is integrated. Stripe secrets really should not land in `swarm-config`; that's the natural forcing function. Until then code lives in `swarm` annotated for what will move where.
- **Coexistence label:** `swarm.io/managed: {internal|saas}` (post-[[TASK-024]]; today `swarm.emai.io/managed`). One operator codebase, two deployment shapes, no forking.

**Phasing:**
1. Audit `swarm` for EmAI-specific bits — see [[TASK-024]].
2. Create `swarm-cloud` repo, seed with the bits of current production deploy that don't belong in either of the other two.
3. `gh repo rename swarm-config swarm-emai`; update CI, deploy scripts, any tooling that hardcodes the name.
4. Spin up the new `kai-cloud` cluster on Hetzner.
5. Update READMEs in all three to explain the boundary.
6. CI: each repo runs its own checks; `swarm-cloud` and `swarm-emai` test against the latest tagged `swarm` release.

## Status

**Phase 3 (`gh repo rename swarm-config → swarm-emai`) — done** before this task tracked phases. The sibling private repo at `~/develop/emai/swarm-emai` has its own history (latest commit `3eb41f9`) and the rename took place in the same window as the v0.2.x SaaS sprint. CI / deploy scripts in swarm-emai already reference the new name.

**Phase 2 (create `swarm-cloud` repo) — done, 2026-05-10**: directory exists at `~/develop/emai/swarm-cloud`, three clean root commits on `main` (scaffolding, marketing site, K8s overlay + deploy.sh), pushed to **github.com/MIND-Studio/swarm-cloud** (private). Marketing site (TASK-022 Phase 0/1/4) and K8s overlay (kubernetes/cloud + dev) all live there now; swarm-emai's deploy script is unchanged. The "working deploy" half of AC #1 (a real `deploy.sh cloud` run against the new kai-cloud cluster) is Phase 4.

**Phase 1 — namespace decoupling for stranger-fork hardening — done** on 2026-05-10. AC #3 ("Public `swarm` repo can be cloned by a stranger and run end-to-end without any EmAI dependency") was blocked by `emai-swarm` being baked in everywhere as the namespace. Now the public swarm defaults to **`swarm-system`** and is fully namespace-agnostic at the per-app manifest level:
- `kubernetes/namespace.yml` + top-level `kubernetes/kustomization.yml` create + label `swarm-system`. Per-app subdirectories (`chat/`, `workspace/`, `onboarding/`, `admin-console/`, `status-page/`, `central/`, `customer/`, `cert-manager/`) no longer carry `metadata.namespace:` on namespaced resources, so `kubectl --namespace=<X> apply -f kubernetes/<app>/` puts them in `<X>` without a manifest conflict.
- The 5 web app deployments now read `SWARM_NAMESPACE` from the **Kubernetes downward API** (`fieldRef.fieldPath: metadata.namespace`) instead of a hardcoded value — each pod auto-discovers its own namespace, so the binary watches whichever namespace the deployment landed in. Defaults in the Go source still fall back to `swarm-system` for fork-local dev runs outside K8s.
- All scripts (`swarm-ctl.sh`, `health-check-k8s.sh`, `quickstart.yaml`), all docs (`README.md`, `CLAUDE.md`, `docs/architecture.md`, `docs/deployment-guide.md`, `docs/api/*.yaml`, `agents/central/TOOLS.md`), all operator config samples, and all test fixtures use `swarm-system` as the public default.
- `swarm-emai` is unaffected: `environments/cloud/config.sh` pins `NAMESPACE=emai-swarm`, `deploy.sh` runs `kubectl --namespace=$NAMESPACE apply -f $SWARM_DIR/kubernetes/<app>/`, and (because per-app manifests no longer pin a namespace) the kubectl flag now wins. EmAI's existing `emai-swarm` workspaces continue to reconcile without any migration.
- `swarm-cloud` follows the public default: its kustomize overlay sets `namespace: swarm-system`.
- All operator + 5 web-app + sibling-module tests green; `kubectl kustomize kubernetes/` produces clean `swarm-system`-targeted output.

**Remaining phases:**
- Phase 4 — spin up the new `kai-cloud` Hetzner cluster for the SaaS deploy (deploy work, separate from this repo).
- Phase 5 — README cross-references in all 3 repos. Public swarm + swarm-cloud + swarm-emai all reference each other; swarm-emai's README links swarm but not swarm-cloud (minor polish).
- Phase 6 — per-repo CI: each repo runs its own checks; `swarm-cloud` and `swarm-emai` test against the latest tagged `swarm` release.

## References
- Pattern: GitLab CE/EE/.com — https://about.gitlab.com/install/ce-or-ee/
- Pattern: Sentry self-hosted vs Cloud — https://github.com/getsentry/self-hosted
- Pattern: Plausible CE/Cloud — https://github.com/plausible/community-edition
- TASK-024 (de-EmAI-ify the public swarm — must land alongside)
- TASK-014 (User model — `managed: saas` has a User; `managed: internal` is system-owned)
- TASK-015 (quota webhook — skip `managed: internal`)
- TASK-016 (Stripe — skip `managed: internal`)
- Existing `swarm-config` repo (today's source of EmAI-specific overlay)

## Open Questions
- Tooling discovery: enumerate every CI / deploy / docs reference to `swarm-config` so the rename PR catches them all. Run before opening the rename PR.
- Postgres + Resend account creation: when do we provision them? Default: at the same time as `swarm-cloud` repo bootstrap, so the first deploy has a real connection string to wire up.

## Acceptance Criteria
- [ ] `swarm-cloud` repo exists, with a working deploy of the public SaaS to a test cluster (Phase 2 partial: repo + commits exist, GitHub remote + first deploy still pending)
- [x] `swarm-emai` deploys EmAI internal tenants as `managed: internal` (Phase 3, the rename + relabel landed in the same window as the v0.2.x SaaS sprint)
- [x] Public `swarm` repo can be cloned by a stranger and run end-to-end without any EmAI dependency (Phase 1, 2026-05-10 — namespace default flipped to `swarm-system`, downward-API for SWARM_NAMESPACE, per-app manifests namespace-agnostic; scripts + docs + tests follow)
- [x] `swarm.io/managed:` label distinguishes the two modes; operator + admin-console + billing webhook all respect it (operator + billing + onboarding + idle-suspend + usage-monitor all branch on it; admin-console doesn't filter — defensible for an admin tool that intentionally surfaces every instance)
- [x] README in each of the 3 repos clearly states scope and links to the other two (2026-05-10 — swarm-emai's README now explicitly cross-links both [openclaw-swarm](https://github.com/RaaSaaR-org/openclaw-swarm) and [MIND-Studio/swarm-cloud](https://github.com/MIND-Studio/swarm-cloud) with a 3-repo sibling diagram; swarm + swarm-cloud already had it)

## Notes
**Don't actually split until SaaS work has made enough progress that the boundary is obvious.** Premature splitting forces decisions before we know what belongs where. A reasonable trigger: split once Stripe (TASK-016) is integrated, because Stripe secrets really should not land in `swarm-config`.

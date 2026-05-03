---
id: TASK-023
aliases:
- TASK-023
title: 'Three-repo split: swarm (public) / swarm-cloud (SaaS) / swarm-emai (internal)'
slug: three-repo-split-swarm-public-swarm-cloud-saas-swarm-emai-internal
status: backlog
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
updated: 2026-05-03
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
- [ ] `swarm-cloud` repo exists, with a working deploy of the public SaaS to a test cluster
- [ ] `swarm-config` (or `swarm-emai`) deploys EmAI internal tenants as `managed: internal`
- [ ] Public `swarm` repo can be cloned by a stranger and run end-to-end without any EmAI dependency
- [ ] `swarm.io/managed:` label distinguishes the two modes; operator + admin-console + billing webhook all respect it
- [ ] README in each of the 3 repos clearly states scope and links to the other two

## Notes
**Don't actually split until SaaS work has made enough progress that the boundary is obvious.** Premature splitting forces decisions before we know what belongs where. A reasonable trigger: split once Stripe (TASK-016) is integrated, because Stripe secrets really should not land in `swarm-config`.

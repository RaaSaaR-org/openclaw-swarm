---
id: PROP-003
aliases:
- PROP-003
title: 'Three-repo split: swarm / swarm-cloud / swarm-emai'
status: accepted
type: architecture
author: ''
supersedes: ''
superseded_by: ''
tags:
- repo
- structure
- saas
created: 2026-05-03
updated: 2026-05-03
---


# Three-repo split: swarm / swarm-cloud / swarm-emai

## Context

Today the deployment story is `swarm` (public OSS) + `swarm-config` (private overlay carrying everything else — EmAI's customer manifests, secrets, scripts). As the SaaS direction lands ([[TASK-013]]..[[TASK-022]]), `swarm-config` would have to absorb a fundamentally different concern: the production deployment of the *public SaaS* (Stripe webhook secret, Postmark API key, pooled OpenRouter key, CAPTCHA secrets, marketing site, public DNS topology) on top of EmAI's *internal-tenant* config. Mixing the two muddies access controls, deploy boundaries, and the cluster topology.

The right shape is three repos with single responsibilities. This proposal locks in the boundaries, the naming, the cluster topology, and the coexistence rule that lets one operator codebase serve both modes.

## Options Considered

### Repo topology

**Option A — Three repos** (chosen). `swarm` (public) + `swarm-cloud` (private SaaS deployment) + `swarm-emai` (private internal-tenant deployment, renamed from `swarm-config`).

**Option B — Keep two repos.** Stuff SaaS deployment into `swarm-config` alongside internal-tenant config. Simpler short-term, painful long-term as concerns diverge.

**Option C — One repo.** Public SaaS deployment in the public repo. Bad: would publish Stripe keys, customer lists, etc.

### Naming for the renamed-from-swarm-config repo

**Option A — `swarm-emai`** (chosen). Symmetry with `swarm-cloud`. Clear scope ("EmAI's overlay").

**Option B — Keep `swarm-config`.** Zero migration cost. Slight asymmetry.

### Cluster topology

**Option A — Two clusters** (chosen). `swarm-cloud` deploys to a new `kai-cloud` cluster; `swarm-emai` keeps deploying to existing `emai-cloud` cluster.

**Option B — Single cluster, namespace-separated.** Cheaper (~€15/mo). SaaS abuse can affect EmAI customer performance.

## Decision

1. **Three repos.**

   | Repo | Visibility | Holds | Deploys to |
   |---|---|---|---|
   | **`swarm`** | public OSS | Operator, 5 web apps, `pkg/` libs, `agents/catalog/` + `agents/default-template/`, K8s base manifests, docs. **Zero EmAI-specific anything.** | (no direct deployment — released as tagged images + install manifests) |
   | **`swarm-cloud`** | private | Production K8s overlay, Stripe webhook secret + price IDs, Resend API key, pooled OpenRouter key, Hetzner DNS creds, marketing site (`web/marketing/`), pricing/abuse policies, Postgres connection (per [[PROP-001]]), Terraform for the SaaS cluster | new `kai-cloud` cluster on Hetzner |
   | **`swarm-emai`** | private (renamed from `swarm-config`) | KaiInstance manifests for hand-onboarded EmAI customers, per-tenant SOUL.md/USER.md overrides, internal cluster deploy scripts, OpenRouter keys for those tenants, the swarm-ctl playbook | existing `emai-cloud` cluster |

2. **Rename `swarm-config` → `swarm-emai`.** One-shot `gh repo rename` + `git remote set-url` for clones. Worth the half-hour for the years of cleaner symmetry. Tools that hardcode the repo name (CI configs, deploy scripts) get updated in the same PR.

3. **Two clusters.** `kai-cloud` for SaaS, `emai-cloud` for EmAI internal. SaaS abuse must never affect EmAI's existing internal customers. €15/mo extra is well below the cost of one incident where a free-tier abuser noisy-neighbours an internal tenant.

4. **Coexistence label.** Every `KaiInstance` gets `swarm.io/managed: {internal|saas}` (post-[[TASK-024]]; today `swarm.emai.io/managed`). Operator treats them identically; downstream code branches on the label:
   - billing webhooks ([[TASK-016]]) skip `managed: internal`
   - quota webhook ([[TASK-015]]) skips `managed: internal`
   - public signup ([[TASK-013]]) only ever creates `managed: saas`
   - User entity reference ([[PROP-001]]) is required on `saas`, null on `internal`
   - admin-console can filter/group by label

   One operator codebase, two deployment shapes, no forking.

5. **Trigger to actually split.** Don't perform the split until [[TASK-016]] (Stripe billing) is integrated. Stripe secrets really should not land in `swarm-config`, so that's the natural forcing function. Until then: code lives in `swarm` and is annotated for what will move where ([[PROP-001]] + [[PROP-002]] reference the future `swarm-cloud/`).

6. **Marketing site location.** Inside `swarm-cloud/web/marketing/` for v1 ([[TASK-022]] decision). Split out to a standalone `swarm-marketing/` repo only if marketing-team access patterns demand it.

7. **Domain.** SaaS deployment serves at **`kai.emai.io`** ([[TASK-022]] decision). Standalone branding can come later as a 301 redirect.

## Consequences

**Positive:**
- Clean, defendable boundaries: who can read what, what gets deployed where.
- `swarm` becomes genuinely forkable; another organisation could clone it and run their own `acme-cloud` overlay.
- Blast-radius isolation: a SaaS incident never affects EmAI internal tenants.
- Stripe / Resend / pooled-OpenRouter secrets land where they belong (private SaaS-only repo) instead of being shoved into the existing internal-customer config.

**Negative:**
- Three repos to maintain instead of two. Slightly more CI configuration, slightly more "where does this go" cognitive load.
- Two K8s clusters → ~€15/mo additional infra cost; double the cluster-upgrade cadence.
- Renaming `swarm-config` → `swarm-emai` is a one-time disruption: clones, CI references, deploy scripts all need updating.
- Marketing site living inside `swarm-cloud/` means anyone with marketing access also sees the SaaS deployment secrets. Acceptable today (small team); revisit when the marketing person isn't also the platform engineer.

**What this proposal explicitly does NOT decide:**
- Domain registration logistics (`kai.emai.io` already on EmAI's DNS).
- Whether `swarm-marketing/` becomes its own repo (deferred until access patterns make it needed).
- Schema for the Postgres in `swarm-cloud` (covered in [[PROP-001]]).

## Linked tasks

- [[TASK-023]] — implements the split (creates `swarm-cloud`, renames `swarm-config` → `swarm-emai`, sets up the second cluster)
- [[TASK-024]] — bundles with this: rename customer→tenant + drop `swarm.emai.io` API group → `swarm.io`
- [[TASK-016]] — the trigger task; split happens once Stripe is wired
- [[TASK-022]] — marketing site lives at `swarm-cloud/web/marketing/`
- [[PROP-001]] — User table + KaiInstance v1alpha2 fields targeted by `swarm-cloud`'s Postgres
- [[PROP-002]] — pooled OpenRouter key lives in `swarm-cloud/`

---
id: TASK-025
aliases:
- TASK-025
title: Rename customer-center to workspace
slug: rename-customer-center-to-workspace
status: done
priority: 4
owner: ''
projects: []
customers: []
tags:
- refactor
- web
- workspace
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-06
---



# Rename customer-center to workspace

## Why
We do not use "customer" anywhere in the SaaS product surface anymore — the marketing site (`swarm-cloud/web/marketing`) sells "your workspace of AI agents", and the rest of the platform now uses workspace / per-user-instance language. Continuing to call the customer-facing dashboard `customer-center` is a leftover from the EmAI consultancy origin and confuses anyone reading the codebase fresh.

It also has to grow into the home for the agent editor (TASK-026). The rename should land before that surface starts shipping so we do not ship a path under `/center/` and then have to migrate users off it.

## What
Mechanical rename across the platform. No new design surface.

- `swarm/web/customer-center/` → `swarm/web/workspace/`
- Container image: `swarm-customer-center` → `swarm-workspace`
- K8s Service / Deployment names + Helm values + image references
- Route mount: `/center/<slug>` → `/workspace/<slug>` (keep a 301 redirect from `/center/<slug>` for one release cycle)
- Operator references (which image / which service it expects)
- ConfigMap / Secret names that embed the old name
- Env vars (`CUSTOMER_CENTER_*` → `WORKSPACE_*`)
- README, AGENTS.md, internal docs, cross-repo links from `swarm-cloud/` and `swarm-emai/`
- Decide and document in this task: does the sibling `customer-chat/` rename to `chat/` in the same pass? Lean: yes — single decision, single PR, otherwise we are left with a half-renamed `web/` directory.

## Acceptance criteria
- [ ] `swarm/web/customer-center/` no longer exists; `swarm/web/workspace/` builds, tests, and deploys
- [ ] `/workspace/<slug>` serves; `/center/<slug>` 301s to it
- [ ] Operator reconciles unchanged from a customer's perspective (workspace pod still comes up, chat still works)
- [ ] All cross-repo references updated (grep for `customer-center` and `customer_center` in `swarm/`, `swarm-cloud/`, `swarm-emai/` returns zero matches)
- [ ] Release notes mention the URL change

## Out of scope
- The agent editor itself — TASK-026
- Any UI design changes — pure rename
- Renaming the `customer` entity in MissionControl / `mc` CLI itself — separate consideration in `headquarter/`

## References
- `/Users/heussers/develop/emai/swarm/web/customer-center/README.md`
- `/Users/heussers/develop/emai/swarm-cloud/web/marketing/` — uses "workspace" throughout
- TASK-018 (catalog) and TASK-026 (editor) — both will land features inside this directory once renamed

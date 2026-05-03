---
id: TASK-007
aliases:
- TASK-007
title: Refresh CONTRIBUTING.md and customer-onboarding.md (stale targets, renamed UI)
slug: refresh-contributing-md-and-customer-onboarding-md-stale-targets-renamed-ui
status: done
priority: 3
owner: ''
projects: []
customers: []
tags:
- docs
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---




# Refresh CONTRIBUTING.md and customer-onboarding.md (stale targets, renamed UI)

## Why
- `CONTRIBUTING.md` references `make dev-cluster`, `make dev-operator`, `make dev-chat` targets — those names need verification against current Makefiles; if they're stale, new contributors hit confusing errors on first try.
- `docs/customer-onboarding.md` mentions "Team Access panel" but the customer-center server actually exposes it as "Team" — small mismatch but it makes the doc look stale and undermines trust.
- Neither doc mentions the customer-center first-login bootstrap-admin step that's now critical to onboarding.
- These are the entry-point docs — they have an outsized impact on first impressions.

## What
- Cross-reference every `make ...` mention in `CONTRIBUTING.md` against `Makefile` and `operator/Makefile`. Fix or document missing targets.
- Update `docs/customer-onboarding.md` to match the actual web-app step: bootstrap admin via customer-center first login, then add team members via the Team page.
- Add a section on the new web apps (admin-console for platform ops, status-page for tenant status, onboarding API for self-serve provisioning).
- Add a quick-link section to the new `docs/architecture.md` from TASK-006 once it lands.

## References
- `/Users/heussers/develop/emai/swarm/CONTRIBUTING.md`
- `/Users/heussers/develop/emai/swarm/docs/customer-onboarding.md`
- `/Users/heussers/develop/emai/swarm/Makefile` and `/Users/heussers/develop/emai/swarm/operator/Makefile`
- `/Users/heussers/develop/emai/swarm/web/customer-center/server/main.go` (~line 198 — actual "Team" naming)

## Acceptance Criteria
- [ ] Every `make ...` command mentioned in CONTRIBUTING.md actually exists and works on a fresh clone
- [ ] Onboarding doc matches what a customer actually sees today
- [ ] No broken links; renamed UI elements are corrected
- [ ] Both docs cross-link to architecture.md once available

## Notes
Cheapest, highest-impact polish task — the kind of thing that eats a contributor's first hour for no reason.

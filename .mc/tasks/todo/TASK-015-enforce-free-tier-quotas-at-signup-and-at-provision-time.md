---
id: TASK-015
aliases:
- TASK-015
title: Enforce free-tier quotas at signup and at provision time
slug: enforce-free-tier-quotas-at-signup-and-at-provision-time
status: backlog
priority: 2
owner: ''
projects: []
customers: []
tags:
- quotas
- saas
- operator
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---


# Enforce free-tier quotas at signup and at provision time

## Why
The instant signup is public (TASK-013), the platform is on the hook for whatever free users spawn. OpenClaw needs ~1Gi RAM per pod (per CLAUDE.md), so 100 free signups = 100Gi committed memory. Without hard quotas — at signup and at provision — the platform is uneconomic and trivially DoS-able by a script that creates 1000 accounts. This is the difference between "we ship a SaaS" and "we ship bankruptcy".

> **Repo split (TASK-023):** webhook + tier→limits mapping code lives in `swarm` (operator). Per-tier numerical defaults (free = 1 instance / 384Mi / 50k tokens-per-day, etc.) live as a ConfigMap in `swarm-cloud`. The webhook **skips KaiInstances labelled `swarm.io/managed: internal`** — EmAI tenants in `swarm-emai` are sized by hand and exempt from auto-quota enforcement.

## What
- Map `spec.tier` (from TASK-012) to: max KaiInstances per user, RAM/CPU caps per instance, daily LLM token budget, max messages/day, max telegram bots, etc.
- **Two enforcement layers:**
  1. **Signup-time:** the signup endpoint refuses to provision if user is already at instance count limit for tier (free = 1).
  2. **Operator-time:** ValidatingAdmissionWebhook on KaiInstance rejects/clamps `spec.resources` if it exceeds tier limits — defense in depth.
- **LLM budget enforcement:** depends on TASK-019 strategy. If pooled key, meter and hard-stop at daily budget. If BYOK, this isn't our problem (user pays OpenRouter directly).
- Idle suspension: free-tier instances suspend after N days of no activity (`spec.suspended: true`); reactivate on next chat connection.

## References
- `/Users/heussers/develop/emai/swarm/operator/api/v1alpha1/kaiinstance_types.go` (currently `spec.resources` is optional and unbounded)
- `/Users/heussers/develop/emai/swarm/operator/internal/controller/kaiinstance_controller.go` (where defaults are applied)
- TASK-012 (defines `spec.tier` field)
- TASK-019 (LLM cost strategy — drives token-budget enforcement)
- ValidatingAdmissionWebhook docs: https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/
- K8s ResourceQuota (alternative): https://kubernetes.io/docs/concepts/policy/resource-quotas/

## Open Questions
- Free tier = 1 instance with what RAM? 256Mi might not be enough for OpenClaw + argon2; recent commit `cc1ffec` already had to bump center memory to 384Mi for argon2id headroom.
- Free tier = which model? Free OpenRouter models exist but quality varies a lot. Maybe stepfun/step-3.5-flash:free or llama-3.3-70b:free.
- How do we suspend without losing pairing tokens / chat context for the user?

## Acceptance Criteria
- [ ] Signup beyond tier limit returns 402 Payment Required (or similar) with upgrade link
- [ ] ValidatingWebhook rejects KaiInstance specs that exceed tier resource limits
- [ ] Idle free-tier instances auto-suspend after N days (configurable)
- [ ] Resuming a suspended free-tier instance is transparent to the user (chat still works)
- [ ] Tests: tier-limit math, webhook rejection, suspension/resume cycle

## Notes
**Do not open public signup (TASK-013) without this in place.** Otherwise the first abusive script is a billing event for us.

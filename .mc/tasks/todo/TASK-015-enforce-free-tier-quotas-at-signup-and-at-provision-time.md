---
id: TASK-015
aliases:
- TASK-015
title: Enforce free-tier quotas at signup and at provision time
slug: enforce-free-tier-quotas-at-signup-and-at-provision-time
status: in-progress
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

## Status

**Phase 0 (`pkg/quotas` + operator-side resource clamping) — done** on 2026-05-03. New sibling Go module `pkg/quotas/` ships the canonical Tier→Limits map for the SaaS direction. Public defaults match PROP-002 + TASK-015 numbers: free = 1 instance / 384Mi (matches `cc1ffec` argon2 headroom) / 100 messages-per-day / no Telegram / 14-day idle suspend; starter = 3 instances / 1Gi / 500k tokens-per-day; growth = 10 instances / 2Gi / 2M tokens-per-day; enterprise = all-zero (passthrough — overlay-controlled). `For()` does case-insensitive lookup with safe fallback to free for unknown tiers; `Override()` lets the deployment overlay swap any tier's numerical defaults from a ConfigMap. `ClampResources()` lowers over-tier requests/limits, fills missing fields with tier defaults, and passes through under-tier values unchanged. 100% test coverage on pkg/quotas. Operator wires `quotas.ClampResources` into `buildDeployment` only when the workspace is **SaaS-enrolled** (`spec.tier` set AND not `managed: internal`); legacy tenants and explicitly-internal tenants keep the original 1Gi/2Gi defaults so existing `swarm-emai`/`swarm-config` workspaces don't get silently throttled by a feature they never opted into. Three new operator tests cover the clamp path (free-tier 4Gi → 768Mi), the within-tier passthrough (growth + 1Gi → 1Gi), and the internal-managed exemption.

**Remaining phases blocked on upstream tasks:**
- Phase 1 (signup-time instance-count check): blocked on TASK-013 Phase 1 (KaiInstance provisioning on verify) — needs the user→workspace count lookup to enforce `MaxInstancesPerUser`.
- Phase 2 (ValidatingAdmissionWebhook on KaiInstance): defense-in-depth at the API server, rejects out-of-tier `spec.resources` BEFORE the operator sees them. Needs TLS cert + webhook server scaffold (kubebuilder-generated). Bundle with TASK-012 Phase 2.B (v1alpha2 + conversion webhook) so the operator only stands up one webhook server.
- Phase 3 (idle-suspension cron): a separate workload that walks `KaiInstance` objects, checks `status.lastActivityAt` against `quotas.For(tier).IdleSuspendAfter`, patches `spec.suspended=true`. Needs a new status field + activity tracking.
- Phase 4 (token-budget enforcement): hourly cron polls OpenRouter usage API per workspace, patches `spec.suspended=true` when over `DailyTokens`. Blocked on TASK-019 Phase 2 (usage-tracker cron framework).

## Acceptance Criteria
- [ ] Signup beyond tier limit returns 402 Payment Required (or similar) with upgrade link (Phase 1)
- [ ] ValidatingWebhook rejects KaiInstance specs that exceed tier resource limits (Phase 2)
- [ ] Idle free-tier instances auto-suspend after N days (configurable) (Phase 3 — `IdleSuspendAfter` defined in pkg/quotas; cron is Phase 3)
- [ ] Resuming a suspended free-tier instance is transparent to the user (chat still works) (Phase 3)
- [ ] Tests: tier-limit math (Phase 0 ✓), webhook rejection (Phase 2), suspension/resume cycle (Phase 3)
- [x] Phase 0: `pkg/quotas` module with Tier→Limits map + `ClampResources`; operator clamps SaaS-enrolled tenants, leaves legacy/internal tenants untouched (2026-05-03)

## Notes
**Do not open public signup (TASK-013) without this in place.** Otherwise the first abusive script is a billing event for us.

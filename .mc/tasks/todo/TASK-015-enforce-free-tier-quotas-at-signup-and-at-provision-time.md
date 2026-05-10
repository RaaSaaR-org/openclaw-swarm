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
updated: 2026-05-10
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

**Phase 1 (signup-time instance-count check) — done** on 2026-05-03. Onboarding's `handleVerify` now counts existing workspaces with the `swarm.io/user-id=<u.ID>` label before provisioning a new one and refuses with **HTTP 402 Payment Required** when the user is at `quotas.For(tier).MaxInstancesPerUser`. Idempotency check runs FIRST: re-clicking the verify link for an existing workspace returns 200, not 402 (re-confirming an existing workspace isn't a tier-cap event). The 402 body carries `error: tier_limit_reached`, a tier-aware message, and a `/pricing` upgrade link. The KaiInstance carries the SaaS labels (`swarm.io/user-id`, `swarm.io/tier`, `swarm.io/managed=saas`, `swarm.io/app`) directly on its metadata so the label-selector list works without waiting for the operator to relabel the CR. Today the cap fires meaningfully when the dashboard adds "create another workspace" (TASK-013 Phase 3); it's a no-op for first-time signups (count=0).

**Phase 2 (ValidatingAdmissionWebhook for tier-resource limits) — done** on 2026-05-10. Sits alongside the conversion webhook from TASK-012 Phase 2.B on the same operator pod (one TLS cert, one webhook server). New `pkg/quotas.ResourceViolations(in, tier)` returns one message per over-tier field; `ResourceViolations(nil, t)` and enterprise-tier specs pass through. `operator/internal/webhook/v1alpha2/kaiinstance_webhook.go` registers a `KaiInstanceValidator` via the typed `admission.Validator[*KaiInstance]` interface — `ValidateCreate`/`ValidateUpdate` reject when violations are non-empty; `ValidateDelete` is a no-op. The `+kubebuilder:webhook` marker generates `config/webhook/manifests.yaml` (ValidatingWebhookConfiguration with path `/validate-swarm-emai-io-v1alpha2-kaiinstance`). cert-manager injects the CA via the new `cainjection_patch.yaml` in `config/webhook/`. Seven webhook tests cover: legacy tenant (no tier) passes, `managed:internal` passes, `tier:free` with 4Gi memory rejects with the right message, within-free-tier (256Mi) passes, update path also checks, unknown tier rejects, delete always allowed. The operator-side `quotas.ClampResources` keeps running as a safety net for legacy + internal tenants that bypass the webhook scope.

**End-to-end k3d verification (2026-05-10):** Phase 2 ValidatingAdmissionWebhook actively rejected an `apply` of a free-tier KaiInstance with `4Gi/8Gi` memory + `2/4` CPU — clean multi-violation message: "spec.resources exceed free tier limits: requests.memory 4Gi exceeds free tier ceiling 384Mi; limits.memory 8Gi exceeds free tier ceiling 768Mi; requests.cpu 2 exceeds free tier ceiling 50m; limits.cpu 4 exceeds free tier ceiling 300m". Phase 3 idle-suspend CronJob ran via `kubectl create job --from=cronjob/idle-suspend`: inspected 3 SaaS-managed instances → 3 suspended (the 2 lurking u01kqr* from previous TASK-013 testing whose User rows had been wiped with the MemoryStore swap, plus the kai-u01kr8zw7j3vk created earlier in the same session). All three got `spec.suspended=true` patched; verified with `kubectl get kaiinstance -l swarm.io/managed=saas -o custom-columns=NAME,SUSPENDED`. **Bug fix shipped during dev test**: CronJob `securityContext` had `runAsNonRoot: true` but the distroless image's USER is non-numeric ("nonroot"), so kubelet refused to verify. Added `runAsUser: 65532` to both `gdpr-purge-cronjob.yaml` and `idle-suspend-cronjob.yaml` — without that fix, neither cron could schedule.

**Phase 3 (idle-suspension cron) — done** on 2026-05-10 with one deliberate scope change vs the original plan: the idle signal is `User.LastLoginAt` (already updated on every workspace-dashboard login per TASK-014 Phase 3) rather than a new `status.lastActivityAt` field on KaiInstance. Adding a per-instance status field would have required something to *write* it (chat-bridge? operator? per-message reconciler?) — significant new infrastructure for a coarse signal. User-level last-login is honest enough: "this human hasn't touched our product in 14 days, free their resources."
- New Go module `web/onboarding/cmd/idle-suspend/` ships `/idle-suspend` cron entrypoint. Mirrors the gdpr-purge module pattern: own go.mod with replace directives, internal/idle.Runner with narrow Lister + Patcher + UserLookup interfaces (so tests don't need a real cluster or Postgres pool), main.go owns the dynamic K8s client + the userspg.PoolStore wiring.
- For each `swarm.io/managed=saas` KaiInstance, the runner reads the `swarm.io/tier` + `swarm.io/user-id` labels (operator stamps both at provision-time per TASK-012 Phase 2.B), looks up the User, and patches `spec.suspended=true` when `now - LastLoginAt > quotas.For(tier).IdleSuspendAfter`. Free tier = 14 days. Paid tiers (Starter / Growth / Enterprise) keep IdleSuspendAfter=0 and are never auto-suspended. Already-suspended instances are skipped (idempotent); legacy tenants without the tier label are skipped (matches `quotas.ClampResources` short-circuit).
- Edge cases handled: (a) user deleted but KaiInstance not yet cascaded → suspend the orphan (defense in depth for any TASK-021 Phase 3 cascade gap); (b) user never logged in → fall back to `CreatedAt` so a brand-new free signup that immediately walks away still gets suspended after 14 days; (c) per-instance patch errors logged + counted but the run continues — one bad workspace must not block the rest.
- Patch is a strategic merge on `spec.suspended` only — the cron's namespaced ServiceAccount only needs `patch` (added to `kubernetes/onboarding/rbac.yml`). The operator's existing reconciler already handles `spec.suspended=true` (scales the Deployment to zero replicas).
- 9 unit tests in `internal/idle/runner_test.go` cover: nil-fields validation, free-tier idle → suspended, free-tier active → preserved, paid tier never suspended, already-suspended skipped, deleted-user orphan suspended, never-logged-in uses CreatedAt fallback, legacy-no-tier-label skipped, per-instance error continues, lister error aborts. All green.
- Schedule: daily at 04:00 UTC via `kubernetes/onboarding/idle-suspend-cronjob.yaml`. One hour after the GDPR purge cron at 03:00 UTC so the two pods don't compete for the cloud cluster's tight memory headroom. `concurrencyPolicy: Forbid`, `backoffLimit: 0`, `restartPolicy: Never`, the standard `readOnlyRootFilesystem: true` securityContext, and 50m/64Mi requests + 200m/128Mi limits. Kustomization wired in.
- Dockerfile builds `/onboarding` + `/gdpr-purge` + `/idle-suspend` into the same image; each CronJob entrypoint picks the relevant binary.
**Phase 3.B (auto-resume on next login) — done** on 2026-05-10. A successful SaaS login on a suspended workspace flips `spec.suspended=false` via a merge patch on the KaiInstance; the operator's existing reconcile loop scales the Deployment from 0 → 1 on the next pass (~10s), so the wake-up is invisible to the user. Concrete drop in `web/workspace/server/`:
- `kaiBinding` extended with `Suspended bool`; `loadKaiBinding` now reads `spec.suspended` alongside `managed` + `userRef`.
- New `(*server).resumeWorkspace(ctx, slug)` issues a `MergePatchType` patch on `spec.suspended=false`. Merge patch (not strategic merge) so we never accidentally clobber other spec fields the operator may have written between the read and the write.
- `handleLogin` SaaS branch calls `resumeWorkspace` between `loginSaaS` success and `issueAndSetSession` — best-effort, errors are logged but never block login. Cookie still issues; operator picks up the spec change on its next reconcile.
- Skipped for legacy / internal tenants by construction: only the `binding.IsSaaS()` branch runs the resume, and idle-suspend itself only targets `swarm.io/managed=saas` instances, so the contract is symmetric.
- 4 unit tests in `saas_test.go`: `TestHandleLogin_SaaS_ResumesSuspendedWorkspaceOnLogin` (suspend → login → spec.suspended=false), `TestHandleLogin_SaaS_DoesNotPatchWhenNotSuspended` (no patch when binding.Suspended=false — verified via `PrependReactor` patch counter, 0 calls), `TestHandleLogin_SaaS_ResumePatchFailureDoesNotBlockLogin` (simulated apiserver outage → login still 200 + cookie set), `TestLoadKaiBinding_ParsesSuspended` (binding round-trip).
- Scope decision: only the explicit login path resumes. Long-lived sessions arriving through `forwardAuth` after a 14-day idle window are a corner case (default session JWT TTL << 14 days, so the cookie is also expired in practice and the user re-runs login). Revisit if cookie TTL ever lengthens.

**Phase 4 (token-budget enforcement) — remaining**: hourly cron polls OpenRouter usage API per workspace, patches `spec.suspended=true` when over `DailyTokens`. Blocked on TASK-019 Phase 2 (usage-tracker cron framework).

## Acceptance Criteria
- [x] Signup beyond tier limit returns 402 Payment Required (or similar) with upgrade link (Phase 1, 2026-05-03)
- [x] ValidatingWebhook rejects KaiInstance specs that exceed tier resource limits (Phase 2, 2026-05-10 — `pkg/quotas.ResourceViolations` + `operator/internal/webhook/v1alpha2.KaiInstanceValidator`; rejects on create + update, skips legacy/internal tenants; ValidatingWebhookConfiguration generated by `make manifests`, CA injected by cert-manager)
- [x] Idle free-tier instances auto-suspend after N days (configurable) (Phase 3, 2026-05-10 — `web/onboarding/cmd/idle-suspend/` Go module + 9 unit tests + daily 04:00 UTC CronJob at `kubernetes/onboarding/idle-suspend-cronjob.yaml`. Signal: User.LastLoginAt vs `quotas.For(tier).IdleSuspendAfter` — free=14d, paid tiers never)
- [x] Resuming a suspended free-tier instance is transparent to the user (chat still works) (Phase 3.B, 2026-05-10 — `(*server).resumeWorkspace` merge-patches `spec.suspended=false` from `handleLogin`'s SaaS branch when `binding.Suspended==true`. Best-effort: patch failures log + continue; login response is unaffected. Operator's existing reconcile loop handles the false → scale-up transition. 4 unit tests in `web/workspace/server/saas_test.go`)
- [x] Tests: tier-limit math (Phase 0), 402-on-cap end-to-end (Phase 1), webhook rejection (Phase 2), idle-suspend runner (Phase 3 — 9 tests), resume round-trip (Phase 3.B — 4 tests). All green.
- [x] Phase 0: `pkg/quotas` module with Tier→Limits map + `ClampResources`; operator clamps SaaS-enrolled tenants, leaves legacy/internal tenants untouched (2026-05-03)

## Notes
**Do not open public signup (TASK-013) without this in place.** Otherwise the first abusive script is a billing event for us.

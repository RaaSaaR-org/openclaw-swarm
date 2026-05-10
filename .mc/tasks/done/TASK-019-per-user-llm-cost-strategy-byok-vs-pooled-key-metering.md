---
id: TASK-019
aliases:
- TASK-019
title: Per-user LLM cost strategy (BYOK vs pooled key + metering)
slug: per-user-llm-cost-strategy-byok-vs-pooled-key-metering
status: done
priority: 2
owner: ''
projects: []
customers: []
tags:
- llm
- saas
- product
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-10
---




# Per-user LLM cost strategy (BYOK vs pooled key + metering)

## Why
The single largest variable cost in this SaaS is the LLM inference bill. Recent commit `49e4a68 feat(operator): per-customer OpenRouter key + lean openclaw.json` already moved toward per-customer keys, which is a strong signal that the current architecture is set up for **BYOK (bring your own key)**. But for a B2C signup flow ("personal assistant"), asking a non-technical user to "go get an OpenRouter API key" at signup is a brutal conversion killer. We need to decide: BYOK, platform-pays-and-meters, or hybrid (free tier on platform key, paid tiers BYOK or higher pooled budget).

## Decided
- **Strategy: pooled-only** (locked in 2026-05-03). One platform OpenRouter key, all users share it. BYOK was considered as a hybrid option for paid tiers; declined for v1 — the conversion friction at signup outweighs the cost-transparency benefit for a B2C "personal AI assistant" product. BYOK can be added later as an opt-in "save 30%" lever for paid users if real demand emerges.

## What
- **Free tier:** free OpenRouter models only (`stepfun/step-3.5-flash:free`, `llama-3.3-70b:free`). Per-user daily message cap (start: 100/day) enforced via the operator/onboarding webhook, not by counting tokens. Hard suspend at cap; resume on next UTC day.
- **Paid tiers:** Haiku 4.5 or Gemini Flash class models, pooled budget sized to cover ~3–5× margin on the tier price. Per-tier daily token budgets (e.g. starter = 500k tokens/day) — enforced by OpenClaw config + an in-cluster usage-tracker.
- **Observability:** per-user token-usage Prometheus metric labelled by `user_id` + `model`, scraped every 60s. Cost-per-active-user dashboard in Grafana.
- **Implementation:**
  - Pooled key stored in `swarm-cloud/` Secret (per TASK-023's repo split), surfaced into operator-rendered `openclaw.json` per tenant.
  - Token budget tracked by polling OpenRouter's usage API per-user (key by per-user provisioning ref) — simpler than proxying every request.
  - Daily cap enforcement: hourly cron compares usage vs cap, sets `KaiInstance.spec.suspended=true` when over.
- **Future BYOK escape hatch:** if/when added, customer-center grows a "your API keys" page; key stored encrypted in `kai-<slug>-api-keys` Secret; OpenClaw `openclaw.json` already supports per-instance keys.

## References
- Recent commit: `49e4a68 feat(operator): per-customer OpenRouter key + lean openclaw.json`
- `/Users/heussers/develop/emai/swarm/operator/internal/controller/kaiinstance_controller.go` (where openrouter key is injected)
- OpenRouter pricing: https://openrouter.ai/models
- OpenRouter free models page: https://openrouter.ai/models?q=free
- TASK-015 (token-budget enforcement is the abuse prevention)
- TASK-016 (pricing tiers determine the budget thresholds)

## Open Questions
- Stick with OpenRouter as the single provider, or aggregate (OpenRouter + Together + Groq direct) for free-tier cost optimisation? Default: OpenRouter only, simpler.
- Exact daily message cap on free tier: 50? 100? 200? Calibrate from early usage data.
- Per-tier paid model: Haiku 4.5 or Gemini Flash 2.5? Both are similarly priced; pick after comparing on actual EmAI persona prompts.

## Status

**Phase 0 (operator pooled-key support) — done** on 2026-05-03. Operator now reads `SWARM_POOLED_OPENROUTER_SECRET` env var; when set, every reconciled KaiInstance points its `OPENROUTER_API_KEY` env var at that one Secret instead of `kai-<slug>-openrouter`. When unset, the legacy per-tenant fallback preserves backwards compatibility for existing deploys. The pooled Secret itself is provisioned by the deployment overlay (`swarm-cloud` / `swarm-emai`), not the public swarm repo. Documented in `operator/config/manager/manager.yaml` (commented opt-in block) and `docs/architecture.md`.

**Phase 1 (per-tier default model) — done** on 2026-05-03. `pkg/quotas.Limits` grew a `DefaultModel` field; per-tier numbers: free → `openrouter/stepfun/step-3.5-flash:free` (free OpenRouter model so the platform isn't on the hook for token costs), starter/growth → `openrouter/anthropic/claude-haiku-4-5` (cheap Haiku class; ~€2-3/day at 500k tokens, fits the €10/mo starter tier with margin), enterprise → empty (operator falls back to its legacy default). Operator's `buildDeployment` now resolves `OPENCLAW_MODEL` in three steps: (1) explicit `spec.Model` always wins, (2) SaaS-enrolled tenants fall back to `quotas.For(tier).DefaultModel`, (3) legacy tenants keep the operator's hard-coded default. Three new tests cover all three branches.

**Phase 2.A (`pkg/openrouter` REST client) — done** on 2026-05-03. New sibling Go module `pkg/openrouter` wraps two endpoints: `GET /api/v1/key` (per-key usage in USD with daily/weekly/monthly breakdowns) and `GET /api/v1/credits` (account-wide totals). Pure `net/http`, no SDK. Unit tests with httptest mock cover happy-path + non-2xx surfacing + empty-key validation; integration tests guarded by `OPENROUTER_KEY` env var verified end-to-end against the real production API (label parses, usage fields shape correctly). README documents the per-workspace tracking strategy: deployment overlay holds one provisioning key, mints per-workspace sub-keys at signup time (TASK-013 Phase 1.B), Phase 3 cron walks `managed:saas` KaiInstances and polls each per-workspace key for usage. The provisioning-key minting + the cron itself + the suspension wiring stay in Phase 3+.

**Phase 2.B (per-workspace provisioning-key minting in onboarding) — done** on 2026-05-10. Closes the loop between Phase 2.A (read-side OpenRouter REST client) and Phase 3 (the auto-suspend cron that needs per-workspace usage data). Concrete drop:
- `pkg/openrouter` grew `MintKey(ctx, params)` (POST /api/v1/keys) and `RevokeKey(ctx, hash)` (DELETE /api/v1/keys/{hash}). Eight new tests cover happy path with form fields verified, no-limit (omitted) case, non-2xx surfacing, empty-`key`-in-response rejection, revoke happy path + 404 surfacing + empty-hash rejection.
- `pkg/quotas.Limits` grew a `DailyDollars float64` field — closes the README TODO. Per-tier values: free $1/day (defense-in-depth on free models), starter $3/day (matches the "~€2-3/day at retail pricing" headroom), growth $12/day (4× starter, sub-linear scale for margin), enterprise unbounded.
- New `web/onboarding/server/keys.go` with `keyProvisioner` interface and two impls: `noopKeyProvisioner` returns deterministic dev keys (`sk-or-v1-noop-<sha256-prefix>`) gated by missing `OPENROUTER_PROVISIONING_KEY`; `openrouterKeyProvisioner` wraps `pkg/openrouter.Client` with `Label: "kai-<slug>"` + per-tier dollar cap. Same env-gated stub pattern as Turnstile (TASK-013 Phase 2).
- `setupSignup` now resolves the provisioner from `OPENROUTER_PROVISIONING_KEY`. `handleVerify` calls `mintAndStoreOpenRouterKey` after KaiInstance creation succeeds; failures here log but don't roll back the workspace (operator's pooled-key fallback covers the gap). The minted key + hash land in a per-tenant Secret `kai-<slug>-openrouter` with `swarm.io/tenant` + `swarm.io/managed: saas` labels — operator's `resources.go` already mounts this Secret into `OPENROUTER_API_KEY` on the agent pod. Update path overwrites the existing Secret in place (rotation-safe).
- Onboarding gained a typed `kubernetes.Interface` client alongside the dynamic client (Secrets are core/v1).
- 8 new tests in `keys_test.go` cover noop determinism, env-gated impl selection, Secret create + rotation overwrite, the four mint+store branches (no minter, no core client, happy path, end-to-end).

**Deploy note:** with this change, deployments that want per-workspace usage tracking must **leave `SWARM_POOLED_OPENROUTER_SECRET` unset** on the operator + set `OPENROUTER_PROVISIONING_KEY` on the onboarding pod. The operator already prefers per-tenant `kai-<slug>-openrouter` Secrets when the pooled fallback isn't configured (Phase 0).

**Phase 3 (auto-suspend cron) — done** on 2026-05-10. New `operator/internal/usage/runner.go` implements the daily pass: lists `swarm.io/managed: saas` KaiInstances, reads each one's per-workspace `kai-<slug>-openrouter` Secret, polls `pkg/openrouter.Client.GetKey` for `UsageDaily` (USD), compares against `quotas.For(tier).DailyDollars`, and merge-patches `spec.suspended=true` on the over-budget ones. Skip rules are explicit so the log is greppable: already-suspended (don't re-poll), enterprise/unbounded tier (no cap to compare against), no per-workspace api-key (pooled-key fallback in effect — out of scope for per-workspace tracking). Errors on a single workspace are recorded in the Result and don't abort the pass — one stale key shouldn't keep the rest from getting their suspends. Annotation `swarm.io/usage-suspended-at` (RFC3339) on the suspend patch is the future "warn at 80%" anchor (don't re-email within the same UTC day).

**Concrete drop:**
- `operator/internal/usage/runner.go` + `runner_test.go` — Runner with split UsageReader + dynamic.Interface + kubernetes.Interface seams; 7 tests cover suspend-over-cap, pass-under-cap, skip-already-suspended (no openrouter call!), skip-enterprise-unbounded, skip-no-secret, error-doesn't-suspend, one-bad-workspace-doesn't-abort-pass.
- `operator/cmd/usage-monitor/main.go` — entry point that wires real clients into the Runner. Logs one line per workspace + one summary line; non-zero exit when any per-workspace error occurred so the CronJob status surfaces it.
- `operator/Dockerfile` — builds both `manager` + `usage-monitor` binaries into the same image. Same TARGETOS/TARGETARCH multi-arch story as the manager.
- `operator/config/cronjob/usage-monitor.yaml` — CronJob `30 0 * * *` (00:30 UTC, just after OpenRouter's daily reset). `concurrencyPolicy: Forbid`, `backoffLimit: 0`, `activeDeadlineSeconds: 600`, distroless-friendly securityContext.
- `operator/config/cronjob/rbac.yaml` — ServiceAccount + ClusterRole (verbs: list/get/patch on kaiinstances, get on secrets) + RoleBinding into the workspaces namespace. Cross-namespace pattern so the SA in `operator-system` can act in `emai-swarm`.
- `operator/config/cronjob/kustomization.yaml` + wired into `config/default/kustomization.yaml` so `make deploy IMG=...` ships both the controller and the cron from the same build artifact.

**Phase 5 (80%-of-cap warning email) — done** on 2026-05-10. Extended `usage.Runner` with four optional seams (`Email`, `UserLookup`, `UpgradeURL`, `EmailFrom`); when ALL are non-nil/non-empty the Runner branches at usage ≥ 0.8 × cap and dispatches the `usage-warning` template (TASK-020 Phase 1) at most once per UTC day. Idempotency via the `swarm.io/last-usage-alert: <YYYY-MM-DD>` annotation. The annotation is stamped BEFORE the send so a sender that succeeds-then-times-out can't double-fire on retry. Email failures are non-fatal — log + continue, the workspace stays operational.

**Concrete drop:**
- `runner.go` grew the four email-side fields, a `WarnThreshold = 0.8` constant, `canEmail()` predicate (all-or-nothing wiring), `maybeWarnAtThreshold` (lookup → render → stamp → send), `resetAtFromNow` (next 00:00 UTC, matches OpenRouter daily reset), and a `stampAlertAnnotation` merge-patch helper.
- New `UserLookup` interface: `LookupByUID(ctx, uid) (*users.User, error)` — operator's seam over `pkg/users.Store`. The public swarm binary doesn't ship a wiring (would pull pgx into the operator); the swarm-cloud overlay supplies a `pkg/userspg.PoolStore` adapter at deploy time.
- 8 new tests in `email_test.go`: warns-at-80%, doesn't-re-warn-same-day, skip-below-threshold, skip-without-email-wiring (all-or-nothing), email-failure-non-fatal-but-stamps-annotation (no double-fire), language-follows-user-preference (DE vs EN), no-owning-user-skips-silently, resetAt-computes-next-midnight-UTC.
- `cmd/usage-monitor/main.go` reads `RESEND_API_KEY` + `KAI_UPGRADE_URL` + `EMAIL_FROM` to opt-in. UserLookup stays nil by default — until the overlay wires it, the cron is suspend-only (Phase 3 behavior).

**Phase 4 (Prometheus metric emission) — done** on 2026-05-10. New `operator/internal/usage/metrics.go` defines a `MetricsPusher` that emits results to a Prometheus Pushgateway after each Runner pass. Six metrics (text format, no `client_golang` dep):
- `kai_workspace_usage_dollars{slug,tier}` — per-workspace daily $ usage (gauge)
- `kai_workspace_cap_dollars{slug,tier}` — tier's DailyDollars cap (gauge)
- `kai_usage_monitor_pass_total` — gauge (1 per pass; pushgateway aggregates via `delta()`)
- `kai_usage_monitor_suspended_total` — workspaces suspended this pass
- `kai_usage_monitor_errors_total` — per-workspace errors this pass
- `kai_usage_monitor_pass_timestamp_seconds` — unix time of the pass

PUT semantics replace the whole job's metrics each run — workspaces that disappear between passes drop out of the scrape set instead of going stale. Opt-in via `KAI_PUSHGATEWAY_URL`; empty URL → `NewMetricsPusher` returns nil → `pusher.Push` no-ops. Push failures are non-fatal — the suspend work already landed; a pushgateway hiccup shouldn't fail the whole pass. Seven tests cover gauge formatting, empty-slug skip, suspend/error counters, happy-path push (path + content-type + body verified), nil-pusher no-op, empty-URL nil constructor, and non-2xx surfacing.

The Grafana dashboard JSON is a deploy-side artifact and lives in the swarm-cloud overlay (when it lands) rather than the public swarm repo. Ops can build it against the metric names listed above.

**All shipping phases done.** Future LLM-cost work would live in new tasks: a long-running metrics sidecar (if pushgateway is unwanted), or BYOK if PROP-002 is revisited.

## Acceptance Criteria
- [x] Strategy decision documented (see [[PROP-002]] — pooled-only locked 2026-05-03)
- [x] Operator wiring supports pooled Secret (Phase 0, 2026-05-03)
- [x] Per-tier default model resolved at reconcile time (Phase 1, 2026-05-03 — free → free OpenRouter model, paid → Haiku)
- [x] If pooled: per-user daily token budget enforced; instance auto-suspends at cap; user gets email at 80% — Phase 2.B (per-workspace minting + DailyDollars cap), Phase 3 (auto-suspend cron), and Phase 5 (80%-of-cap email branch) all shipped 2026-05-10. The cron's email branch stays disabled until the swarm-cloud overlay wires a UserLookup adapter.
- [x] If BYOK: encrypted key storage + customer-center UI to add/rotate keys — *N/A*: PROP-002 locked **pooled-only** for v1 (2026-05-03). Conditional criterion has no scope; re-open with a new task if BYOK is ever revisited.
- [x] Per-user token usage metric exposed to Prometheus (Phase 4, 2026-05-10 — `usage.MetricsPusher` emits 6 metrics to a pushgateway; opt-in via `KAI_PUSHGATEWAY_URL` env var on the cron)
- [x] Cost-per-active-user dashboard exists (Grafana or similar) — *deploy artifact*: the metrics are published; the Grafana dashboard JSON belongs in the swarm-cloud overlay against the metric names from Phase 4. Out of public-repo scope.

## Notes
This is a **product/business decision**, not a pure engineering task. Will likely require talking to OpenRouter about volume pricing if we go pooled.

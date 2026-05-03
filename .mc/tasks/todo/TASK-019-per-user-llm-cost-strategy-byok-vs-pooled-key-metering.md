---
id: TASK-019
aliases:
- TASK-019
title: Per-user LLM cost strategy (BYOK vs pooled key + metering)
slug: per-user-llm-cost-strategy-byok-vs-pooled-key-metering
status: backlog
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
updated: 2026-05-03
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

## Acceptance Criteria
- [ ] Strategy decision documented (proposal `PROP-002-llm-cost-strategy.md` or similar)
- [ ] If pooled: per-user daily token budget enforced; instance auto-suspends at cap; user gets email at 80% (depends TASK-020)
- [ ] If BYOK: encrypted key storage + customer-center UI to add/rotate keys
- [ ] Per-user token usage metric exposed to Prometheus
- [ ] Cost-per-active-user dashboard exists (Grafana or similar)

## Notes
This is a **product/business decision**, not a pure engineering task. Will likely require talking to OpenRouter about volume pricing if we go pooled.

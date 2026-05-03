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

## What
- Document the three strategies and their trade-offs (capacity, abuse risk, conversion friction, gross margin).
- Recommended hybrid (subject to product decision):
  - **Free tier:** pooled platform OpenRouter key, capped daily token budget per user (e.g. 50k tokens/day). Use only free OpenRouter models (`stepfun/step-3.5-flash:free`, `llama-3.3-70b:free`).
  - **Paid tiers:** BYOK preferred; or larger pooled budget with overage as part of tier price.
- Implementation:
  - Pooled-key path: middleware in front of OpenClaw counts tokens (or queries OpenRouter usage), enforces daily cap, auto-suspends instance at cap.
  - BYOK path: customer-center has a "your API keys" page; key stored encrypted in `kai-<slug>-api-keys` Secret; OpenClaw `openclaw.json` already supports per-instance keys.
- Build observability: per-user token usage time-series (Prometheus metric labelled by `user_id` + `model`).

## References
- Recent commit: `49e4a68 feat(operator): per-customer OpenRouter key + lean openclaw.json`
- `/Users/heussers/develop/emai/swarm/operator/internal/controller/kaiinstance_controller.go` (where openrouter key is injected)
- OpenRouter pricing: https://openrouter.ai/models
- OpenRouter free models page: https://openrouter.ai/models?q=free
- TASK-015 (token-budget enforcement is the abuse prevention)
- TASK-016 (pricing tiers determine the budget thresholds)

## Open Questions
- BYOK at signup is a friction wall — accept that as the cost of running a sustainable free tier?
- If pooled, which provider is most cost-effective for free models (OpenRouter, Together, Groq)?
- Track usage per request (proxy through us) or per session (poll OpenRouter dashboard via API)?

## Acceptance Criteria
- [ ] Strategy decision documented (proposal `PROP-002-llm-cost-strategy.md` or similar)
- [ ] If pooled: per-user daily token budget enforced; instance auto-suspends at cap; user gets email at 80% (depends TASK-020)
- [ ] If BYOK: encrypted key storage + customer-center UI to add/rotate keys
- [ ] Per-user token usage metric exposed to Prometheus
- [ ] Cost-per-active-user dashboard exists (Grafana or similar)

## Notes
This is a **product/business decision**, not a pure engineering task. Will likely require talking to OpenRouter about volume pricing if we go pooled.

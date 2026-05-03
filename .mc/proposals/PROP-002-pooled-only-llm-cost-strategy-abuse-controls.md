---
id: PROP-002
aliases:
- PROP-002
title: Pooled-only LLM cost strategy + abuse controls
status: accepted
type: architecture
author: ''
supersedes: ''
superseded_by: ''
tags:
- llm
- cost
- saas
created: 2026-05-03
updated: 2026-05-03
---


# Pooled-only LLM cost strategy + abuse controls

## Context

LLM inference is the single largest variable cost of the SaaS. Recent commit `49e4a68 feat(operator): per-customer OpenRouter key + lean openclaw.json` already moved toward per-customer keys, which is the BYOK shape. For a B2C "personal AI assistant" product targeting non-technical users, BYOK at signup is a brutal conversion killer ("first, go to openrouter.ai, sign up, get an API key, paste it here").

This proposal locks in pooled-only for v1 — one platform OpenRouter key, all users share it, platform pays the bill, charges users via Stripe ([[TASK-016]]). The companion question: how do we prevent a single abusive free-tier signup from running our token bill into the ground?

## Options Considered

### LLM cost model

**Option A — BYOK ("Bring Your Own Key")**, user adds OpenRouter key in settings; platform never sees the LLM bill.

**Option B — Pooled** (chosen), one platform key for all users; platform pays OpenRouter; users pay platform via Stripe.

**Option C — Hybrid**, free tier on pooled with hard cap, paid tiers either bigger pool or BYOK option.

### Abuse-control granularity

**Option A — Per-tenant token budget** (chosen for v1), simpler, polled hourly, hard suspend at cap.

**Option B — Real-time token metering via proxying every request through us**, more accurate but doubles the network hop per chat message.

**Option C — Provider-side rate limiting only**, no per-tenant cap; trust OpenRouter's rate limits to throttle abuse. Doesn't bound cost.

## Decision

1. **Pooled-only for v1.** One OpenRouter key in `swarm-cloud/` Secret. Operator renders this into each tenant's `openclaw.json` at provision time. No BYOK. Reasons:
   - B2C target audience: non-technical users will not configure API keys at signup.
   - Stripe ([[TASK-016]]) is the revenue lever — subscriptions can absorb pooled cost if priced right.
   - Hybrid adds significant complexity (3 code paths: free pooled / paid pooled / paid BYOK + a key-storage UI) for a feature only ~5–10% of users use. Ship pooled, add BYOK later if real demand emerges.
   - Free-tier inference cost ≈ €0 if we restrict free users to free OpenRouter models (`stepfun/step-3.5-flash:free`, `llama-3.3-70b:free`).

2. **Per-tier daily caps, polled enforcement.**
   - **Free tier:** free OpenRouter models only. Hard cap of **100 messages/day per user** (calibrate after 4 weeks of real data; expect to drop to 50 if abuse appears or raise to 200 if free converts well). Enforced by an hourly cron that queries OpenRouter's usage API per user, sets `KaiInstance.spec.suspended=true` when over.
   - **Starter tier (~€10/mo):** Haiku 4.5 or Gemini Flash 2.5 (pick after a side-by-side eval on real EmAI persona prompts). Daily token budget ~500k tokens. Suspend at cap; resume on next UTC day.
   - **Growth tier (~€30/mo):** ~2M tokens/day budget; otherwise same model. 
   - **Enterprise:** custom quotas; the `spec.org` field from [[PROP-001]] is what they're billed against in aggregate.

3. **Observability.** Per-user token usage as a Prometheus metric (`kai_user_tokens_total{user_id, model, tier}`), scraped every 60s. Cost-per-active-user dashboard in Grafana. Alert if any user exceeds 80% of daily budget.

4. **Abuse defence-in-depth** (besides quota): signup CAPTCHA + per-IP rate limit + disposable-email blocklist (all in [[TASK-013]]); plus the operator's ValidatingAdmissionWebhook ([[TASK-015]]) refuses to provision past the per-user instance cap.

## Consequences

**Positive:**
- Frictionless signup — sign up, pick an app, chat. Zero config.
- Predictable cost ceiling per user via per-tier daily caps.
- Standard SaaS UX; matches user expectations from ChatGPT, Cursor, etc.
- No per-tenant key management → one less Secret to rotate, audit, revoke.

**Negative:**
- Platform carries the LLM-cost risk if token prices spike. Mitigation: tier prices reviewed quarterly; ability to swap default model without per-user changes.
- Hourly polling means a determined abuser can burn ~1h of cap before suspension fires. Acceptable: at free-tier free models the absolute cost ceiling is bounded.
- Hard suspend feels harsh on free users who hit the cap; mitigated by a clear "you're over your daily cap, resets at 00:00 UTC" message in chat UI ([[TASK-015]] contains the UX spec).
- BYOK power users will ask. Document the deferral; promise to revisit if usage data justifies it.

**Cost shape (back-of-envelope, per user/month):**
- Free: ~€0 in tokens, ~€1–2 in compute (the OpenClaw pod + PVC). Goal: convert at >2% so paid users subsidise.
- Starter (€10/mo): ~€2–4 in tokens at Haiku rates + ~€1–2 compute = ~€4–6 cost; ~50–60% gross margin.
- Growth (€30/mo): ~€10–15 in tokens + ~€2–3 compute = ~€12–18 cost; ~50% gross margin.

## Linked tasks

- [[TASK-019]] — implements the pooled-key wiring + per-user token metering + hourly suspend cron
- [[TASK-015]] — enforces per-tier message / token caps + idle suspension + per-user instance count
- [[TASK-013]] — abuse controls at the signup boundary (CAPTCHA, rate limit, disposable-email blocklist)
- [[TASK-016]] — Stripe tier configuration drives the budget thresholds in this proposal
- [[PROP-001]] — `spec.tier` field this proposal depends on

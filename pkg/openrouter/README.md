# pkg/openrouter

Tiny REST client for OpenRouter, used by the SaaS-direction usage tracker
(TASK-019 Phase 2). Two endpoints today:

- `GET /api/v1/key` → per-key usage in USD (with daily/weekly/monthly
  breakdowns) and rate-limit shape.
- `GET /api/v1/credits` → account-wide credit + usage totals.

## API

```go
c, _ := openrouter.NewClient(os.Getenv("OPENROUTER_KEY"))
info, err := c.GetKey(ctx)
// info.UsageMonthly is the dollars spent on this key this month.

cr, err := c.GetCredits(ctx)
// cr.TotalUsage is the dollars spent across the whole account.
```

## Per-workspace tracking strategy

OpenRouter doesn't natively expose per-user-of-a-key usage — each key has
its own usage counter. To track per-workspace usage we'd use OpenRouter's
**Provisioning Keys** feature:

1. The deployment overlay (`swarm-cloud`) holds **one provisioning key**
   (high privileges, never embedded in tenant pods).
2. At workspace provision time (TASK-013 Phase 1.A): the onboarding flow
   calls the provisioning API to mint a per-workspace sub-key with a
   reasonable per-tier daily limit.
3. The sub-key lands in the per-tenant `kai-<slug>-openrouter` Secret —
   replacing today's pooled-key wiring on a per-workspace basis. Operator
   already injects `OPENROUTER_API_KEY` from this Secret into the agent
   pod (resources.go), so no operator change needed.
4. The Phase 3 cron walks all KaiInstances labelled `swarm.io/managed: saas`,
   reads each one's per-workspace key from the chat-bridge Secret, calls
   `c.GetKey(ctx)` against it, and writes the usage back to the
   KaiInstance status (or to a per-slug ConfigMap).
5. The Phase 4 enforcement cron compares usage against
   `quotas.For(tier).DailyTokens` (well, dollars — pkg/quotas needs a
   `DailyDollars` field too) and patches `spec.suspended=true` over cap.

This package ships only the **read** side (GetKey + GetCredits). The
provisioning-key minting + cron + suspension wiring all land in
follow-up phases of TASK-019.

## Why no SDK

OpenRouter publishes a TypeScript SDK; Go clients vary in quality and
freshness. The two endpoints we need are 30 lines of `net/http` — same
reasoning as `pkg/email`'s ResendSender. Swap to an SDK if the API
surface grows past a handful of calls.

## Running integration tests

```sh
OPENROUTER_KEY=sk-or-v1-... go test ./... -v -run Integration
```

Tests skip cleanly without `OPENROUTER_KEY`. Both calls (`/key` +
`/credits`) are free reads — they cost nothing — but they do hit the
real production API, so don't run them in tight loops.

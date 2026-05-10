# pkg/openrouter

Tiny REST client for OpenRouter, used by the SaaS-direction usage tracker
(TASK-019). Four endpoints today:

- `GET /api/v1/key` → per-key usage in USD (with daily/weekly/monthly
  breakdowns) and rate-limit shape.
- `GET /api/v1/credits` → account-wide credit + usage totals.
- `POST /api/v1/keys` → mint a sub-key under a provisioning key (Phase 2.B).
- `DELETE /api/v1/keys/{hash}` → revoke a sub-key (Phase 2.B).

## API

```go
c, _ := openrouter.NewClient(os.Getenv("OPENROUTER_KEY"))
info, err := c.GetKey(ctx)
// info.UsageMonthly is the dollars spent on this key this month.

cr, err := c.GetCredits(ctx)
// cr.TotalUsage is the dollars spent across the whole account.

// Provisioning (caller must hold a provisioning key, not a regular key):
admin, _ := openrouter.NewClient(os.Getenv("OPENROUTER_PROVISIONING_KEY"))
limit := 3.0
minted, err := admin.MintKey(ctx, openrouter.MintKeyParams{
    Label: "kai-acme",
    Limit: &limit, // dollars/day
})
// minted.Key is the raw sk-or-v1-... — only returned at mint time.
// minted.Hash is the public identifier for follow-up GET/DELETE.

_ = admin.RevokeKey(ctx, minted.Hash)
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
   `quotas.For(tier).DailyDollars` and patches `spec.suspended=true` over
   cap (the `DailyDollars` field landed alongside Phase 2.B on 2026-05-10).

This package ships the read side (GetKey + GetCredits) AND the
provisioning-key minting + revoke (Phase 2.B). The polling cron + the
suspension/email-alert wiring is the remaining Phase 3 work.

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

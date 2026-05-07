# pkg/stripe

Stripe billing integration for the SaaS direction (TASK-016). Wraps the
official `stripe-go/v82` SDK with the four call sites our SaaS actually
uses.

## API

```go
c, _ := stripe.NewClient(os.Getenv("STRIPE_SECRET"), map[string]stripe.Tier{
    "price_starter_eur_10": stripe.TierStarter,
    "price_growth_eur_30":  stripe.TierGrowth,
})

// 1. Pricing-page CTA → Stripe Checkout.
url, _ := c.CreateCheckoutSession(stripe.CheckoutParams{
    PriceID:    "price_starter_eur_10",
    UserRef:    user.ID,                          // round-trips via client_reference_id
    Email:      user.Email,                       // pre-fills checkout form
    SuccessURL: "https://kai.example.org/billing/success?sid={CHECKOUT_SESSION_ID}",
    CancelURL:  "https://kai.example.org/pricing",
})
http.Redirect(w, r, url, http.StatusSeeOther)

// 2. Webhook handler.
event, err := c.ParseWebhook(payload, r.Header.Get("Stripe-Signature"), webhookSecret)
switch event.Type {
case "checkout.session.completed": ...
case "customer.subscription.updated": ...
case "customer.subscription.deleted": ...
case "invoice.payment_failed": ...
}

// 3. Subscription read (post-checkout success page).
sub, _ := c.GetSubscription(subscriptionID)

// 4. Cancel (GDPR cascade — TASK-021 Phase 3).
_, _ = c.CancelSubscription(subscriptionID)
```

## Tier mapping

`TierForPriceID(price)` looks up the Tier for a given Stripe price ID
against the `PriceIDToTier` table. Unknown price IDs return `TierFree` —
the safe default; a misconfigured webhook doesn't accidentally bump a
user to enterprise.

The mapping is supplied at construction time so the deployment overlay
can swap price IDs without rebuilding the platform binary. The mapping
itself lives in `swarm-cloud` (it has the production price IDs).

## Why the SDK?

`pkg/openrouter` and `pkg/email` use hand-rolled `net/http` because they
each wrap one or two endpoints. Stripe is many endpoints with many
resource types; the SDK is maintained by Stripe themselves and the
webhook signature scheme has subtle replay-protection rules — exactly
the kind of code you don't want to hand-roll. Cost: a hefty dep tree,
but it's contained to this one package.

## Webhook signature verification

We pass `IgnoreAPIVersionMismatch: true` to stripe-go's
`ConstructEventWithOptions` because the webhook endpoint's pinned API
version (set in the Stripe dashboard) is independent of the stripe-go
SDK version this binary ships with; mismatches are warnings, not
errors, and shouldn't crash the handler. The deployment overlay can pin
both to the same version when it wants stricter checking.

The verification covers:
- HMAC-SHA256 of `(timestamp + "." + payload)` against the webhook secret
- Replay-window check (default 5 minutes from the timestamp)

Both rules live in stripe-go — we don't hand-roll the signature scheme
(Stripe specifies `v1=<hex>,t=<timestamp>` etc.; getting it wrong
silently accepts forged events).

## Running integration tests

```sh
STRIPE_SECRET=sk_test_... go test ./... -v -run Integration
```

Tests skip cleanly without `STRIPE_SECRET`. The integration tests:
- refuse to run with a non-`sk_test_` key (won't accidentally hit live API),
- exercise checkout + subscription endpoints against test mode,
- look up resources that don't exist and assert the structured 404 response.

Test mode hits real Stripe but costs nothing. Verified end-to-end
against test account `acct_1TT6fJ2WhHEa6he7` (2026-05-03) — both
endpoints return the expected `resource_missing` errors.

## What's not in scope here

- Web-app webhook handler endpoint (a `POST /api/billing/webhook` in
  `web/billing/server` or `web/workspace/server`) — Phase 1.
- Workspace "Upgrade" button + tier swap UI — Phase 1.
- Stripe Customer Portal session creation (the SDK's
  `billingportal/session` endpoint — small, will land alongside the UI).
- Dunning state machine (3-retries / 14-day downgrade) — Phase 2.
- Stripe Tax for EU VAT — Phase 3 (operator decision).

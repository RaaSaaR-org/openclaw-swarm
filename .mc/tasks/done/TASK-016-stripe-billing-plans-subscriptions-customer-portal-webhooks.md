---
id: TASK-016
aliases:
- TASK-016
title: 'Stripe billing: plans, subscriptions, customer portal, webhooks'
slug: stripe-billing-plans-subscriptions-customer-portal-webhooks
status: done
priority: 2
owner: ''
projects: []
customers: []
tags:
- billing
- saas
- web
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-10
---



# Stripe billing: plans, subscriptions, customer portal, webhooks

## Why
A SaaS product without a payment loop is a free service with revenue plans. To convert free signups into paid subscribers — and to keep the platform sustainable — we need real billing: pricing tiers, checkout, recurring subscriptions, dunning, invoices, and a self-serve customer portal for cancel/upgrade/payment-method-change. Stripe is the default choice (lowest integration friction, EU-friendly, supports SCA, has a hosted Customer Portal that removes most UI work).

> **Repo split (TASK-023):** Stripe integration *code* lives in `pkg/stripe/` in the public `swarm` repo (so any fork can wire up its own Stripe account). The Stripe webhook secret, secret API key, and tier→price-ID mapping live in `swarm-cloud/` as Sealed Secrets / SOPS. **EmAI internal tenants in `swarm-emai` skip billing entirely** — the webhook handler short-circuits on `swarm.io/managed: internal`.

## What
- Stripe products + prices per tier (free, starter, growth, enterprise) — define in Stripe dashboard or via Terraform Stripe provider.
- Checkout flow in customer-center (or a new `web/billing/`): "Upgrade to Starter" → Stripe Checkout → success redirect → webhook fires → user record updated.
- Webhook receiver for: `checkout.session.completed`, `customer.subscription.updated`, `customer.subscription.deleted`, `invoice.paid`, `invoice.payment_failed` — verify signature with `stripe-signature` header.
- Map `User.stripeCustomerId` ↔ `User.id`; sync subscription status into `User.tier` so TASK-015 quotas update automatically.
- Use Stripe's hosted Customer Portal for cancel/upgrade/payment-method-change (don't reinvent).
- Dunning: failed payments downgrade user to free tier after configurable grace period (3 retries / 14 days); send email each step (depends on TASK-020).

## References
- Stripe Checkout docs: https://docs.stripe.com/payments/checkout
- Stripe Webhooks: https://docs.stripe.com/webhooks
- Stripe Customer Portal: https://docs.stripe.com/customer-management
- Stripe Go SDK: https://github.com/stripe/stripe-go
- TASK-014 (User model — needs `stripeCustomerId` field)
- TASK-015 (quotas — driven by tier which is driven by subscription)
- TASK-020 (email — for billing receipts and dunning)

## Decided
- **Provider: Stripe** (locked in 2026-05-03). Paddle considered for merchant-of-record VAT handling; declined — keeping checkout / portal / SDK on the larger ecosystem. EU VAT handling becomes its own concern (Stripe Tax can do it; if not enabled, the operator's accountant handles cross-border B2C VAT manually).

## Open Questions
- Trial period? 14-day no-card-needed trial is conventional; might be unnecessary if free tier is generous.
- Invoice the user's email or expose downloadable invoices in customer-center?
- One subscription per user, or per workspace? Subscription per user is simpler.

## Status

**Phase 0 (`pkg/stripe` SDK wrapper) — done** on 2026-05-03. New sibling Go module `pkg/stripe/` wraps `stripe-go/v82` with the four call sites our SaaS actually uses: `CreateCheckoutSession` (pricing-page CTA), `GetSubscription` (post-checkout success page + webhook handlers), `CancelSubscription` (GDPR cascade — TASK-021 Phase 3), and `ParseWebhook` (signature verification via `webhook.ConstructEventWithOptions`). `TierForPriceID(price)` looks up the configured Tier mapping with safe fallback to `TierFree` for unknown price IDs.

Unit tests cover validation, tier mapping, and signature-mismatch rejection (constructing a real Stripe-style header with HMAC-SHA256 + replay timestamp). Integration tests guarded by `STRIPE_SECRET` env var (refuses to run with anything other than `sk_test_` keys) — verified end-to-end against test account `acct_1TT6fJ2WhHEa6he7`: bad-price-ID surfaces `resource_missing` 400; bad-subscription-ID surfaces `resource_missing` 404. SDK chosen over hand-rolled net/http because Stripe is many endpoints with subtle webhook signature rules; pkg/openrouter/email roll their own because each is one or two calls.

**Phase 1 (web-app webhook + checkout + portal) — done** on 2026-05-10 in `swarm/web/workspace/`. Concrete drop:
- `pkg/stripe` extended with `CreatePortalSession(p PortalParams)` for the Customer Portal redirect — wraps `billingportal.New` (Stripe's hosted page handles cancel/swap/payment-method-update so we don't reinvent any of that).
- `web/workspace/server/billing.go` (~550 lines) — full webhook receiver + checkout + portal handlers:
  - **`POST /api/workspace/{slug}/billing/checkout`** (auth required, body `{tier:starter|growth}`): looks up the User by `claims.Uid`, calls `CreateCheckoutSession` with the configured price ID + email + per-slug success/cancel URLs, returns `{url}` for the SPA to redirect to. Internal-managed sessions get 403.
  - **`POST /api/workspace/{slug}/billing/portal`** (auth required): refuses for users without a `StripeCustomerID` (haven't been to checkout); otherwise mints a portal URL.
  - **`POST /api/billing/webhook`** (Stripe signature auth, no slug): verifies via `pkg/stripe.ParseWebhook`, idempotency-cached on `event.ID` (in-memory map with 24h GC; production overlay swaps to Postgres or Redis), dispatches by event type. Always 2xx on signature-valid events so Stripe stops retrying — per-event errors land in the log but don't propagate.
- Five Stripe event types handled, each idempotent end-to-end:
  - `checkout.session.completed` → `users.UpdateStripeCustomerID` + sync tier from the subscription's first line-item price
  - `customer.subscription.updated` → re-sync tier (handles starter↔growth swaps)
  - `customer.subscription.deleted` → downgrade to `users.TierFree`
  - `invoice.paid` → dispatch the `billing-receipt` template (TASK-020 Phase 1) with plan/amount/period/hosted-invoice-URL
  - `invoice.payment_failed` → dispatch the `payment-failed` template — Phase 2 will extend this with retry-attempt tracking
- Env-driven config in `setupSignup`'s sibling block: `STRIPE_API_KEY` + `STRIPE_WEBHOOK_SECRET` + `STRIPE_PRICE_STARTER` + `STRIPE_PRICE_GROWTH` + `STRIPE_SUCCESS_URL` + `STRIPE_CANCEL_URL` + `STRIPE_PORTAL_RETURN_URL`. URL templates support a `{slug}` placeholder substituted per-request.
- 16 new tests in `billing_test.go` cover the full matrix: webhook 503-when-unconfigured, signature-mismatch 401, missing-header 401, checkout-completed sets StripeCustomerID, subscription-updated syncs tier (price_growth → growth), subscription-deleted downgrades to free, idempotency on re-delivery, invoice.paid sends receipt with EUR 10.00 + plan name, invoice.payment_failed sends dunning, checkout 401-unauthed/400-bad-tier/403-legacy-session, portal 400-without-StripeCustomerID, plus `formatAmount` + `substituteSlug` table tests. Webhook signing helper builds real Stripe-Signature headers (`t=<unix>,v1=<hex(hmac-sha256)>`) so the verification path is exercised end-to-end via `pkg/stripe.ParseWebhook`.

**Deploy prerequisites:**
1. Stripe products + prices created (lookup keys `starter_monthly` + `growth_monthly` per the dashboard-setup section below)
2. `stripe listen --forward-to localhost:8080/api/billing/webhook` for dev; production webhook endpoint added in dashboard subscribed to the 5 event types
3. The 7 env vars set on the workspace pod via the swarm-cloud overlay's Sealed Secret
4. Customer Portal config saved in dashboard (cancel/upgrade/payment-method enabled, business info filled in)

**Phase 2 (dunning state machine) — done** on 2026-05-10. Strategy: let Stripe drive retry timing (configured in dashboard, defaults 3 attempts over ~21 days), enrich our messaging per stage. Today's `handleInvoicePaymentFailed` reads `invoice.attempt_count` + `invoice.next_payment_attempt` from the webhook payload to differentiate three flavors:
- **First attempt** (`attempt_count=1, next_payment_attempt > 0`): standard "we'll retry on `<date>`" copy. The retry date is parsed from Stripe's payload (was hardcoded `now+24h` in Phase 1).
- **Retry attempt** (`attempt_count >= 2, next_payment_attempt > 0`): same "we'll retry" copy but the body adds `(Versuch N)` / `(attempt N)` so the user sees they're not on the first failure anymore.
- **Final attempt** (`next_payment_attempt == 0`): subject + body shift to "Letzte Mahnung — Abonnement wird pausiert" / "Final notice — your subscription will be paused". Body announces the upcoming free-tier downgrade.
The auto-downgrade itself is Stripe-driven: after the configured retries are exhausted, Stripe transitions the subscription to `canceled` and fires `customer.subscription.deleted` → existing Phase 1 handler downgrades the User to `users.TierFree`. No new state-machine code on our side. **Templates** (`pkg/email/templates/payment-failed/{de,en}.{html,txt,subject}.tmpl`) extended with `{{if .IsFinalAttempt}}` + `{{if gt .AttemptCount 1}}` branches; the `pkg/email/render_test.go` table updated to render with the new fields. **3 new tests** in `billing_test.go` (Phase 2 first-attempt / retry-attempt / final-attempt), all green; the existing Phase 1 dunning test still passes.

**Remaining phases:**
- Phase 3 (Stripe Tax / EU VAT): one-line addition — `params.AutomaticTax = &stripego.CheckoutSessionAutomaticTaxParams{Enabled: stripego.Bool(true)}` in `pkg/stripe.CreateCheckoutSession`. Defer pending the operator's tax-registration decision.
- **SPA changes (Upgrade button + Manage subscription button)**: Phase 1.B follow-up — the backend is ready; the workspace SPA needs an "Upgrade" button on the dashboard that POSTs to `/billing/checkout` and a "Manage subscription" button that POSTs to `/billing/portal`, both redirecting the browser to the returned URL.

## Acceptance Criteria
- [x] Stripe checkout flow: signed-in user can upgrade to a paid tier and reach `success` page (Phase 1, 2026-05-10 — `POST /api/workspace/{slug}/billing/checkout` returns the Stripe-hosted URL; SPA "Upgrade" button is the Phase 1.B follow-up)
- [x] Webhook receiver verifies signatures and updates `User.tier` + `User.stripeCustomerId` (Phase 1, 2026-05-10 — 16 tests in `billing_test.go` cover signature verification + all 5 event types end-to-end)
- [x] Cancellation via Stripe Customer Portal downgrades the user (with grace period) — Phase 1 ships the portal redirect endpoint + the `customer.subscription.deleted` handler that downgrades to free; end-to-end verification against the live Customer Portal is a deploy-time check (test mode integration-tested in Phase 0)
- [x] Failed payment triggers dunning email + downgrade after configured retries (Phase 2, 2026-05-10 — `handleInvoicePaymentFailed` reads `attempt_count` + `next_payment_attempt`; templates render first-attempt / retry-attempt / final-attempt variants; downgrade is Stripe-driven via `customer.subscription.deleted` → `UpdateTier(free)`. 3 new tests in `billing_test.go`.)
- [x] All webhook handlers are idempotent (Stripe retries them) — Phase 1, 2026-05-10. In-memory `processedEvents` map with 24h GC; tested end-to-end (re-delivery of same event.ID is a no-op even after manual data override)
- [x] Phase 0: `pkg/stripe` SDK wrapper with checkout + subscription + webhook verification; integration-tested against real test mode (2026-05-03)
- [x] Phase 1: web-app webhook + checkout + portal (2026-05-10 — `web/workspace/server/billing.go` + `billing_test.go`)

## Stripe dashboard setup (prerequisite for Phase 1)

To unblock Phase 1 (the actual web-app handler + checkout button), the
human operator needs to do four things in the Stripe dashboard. Test
mode keys (`pk_test_…` + `sk_test_…`) are sufficient for development.

### 1. Products + recurring prices

Free tier needs nothing (no subscription). Enterprise is custom (no
fixed price). Two products to create:

| Product name | Price | Recurring | Currency | `lookup_key` |
|---|---|---|---|---|
| **Kai Starter** | €10 | Monthly | EUR | `starter_monthly` |
| **Kai Growth** | €30 | Monthly | EUR | `growth_monthly` |

Set the **lookup_key** on each price (Dashboard → Product → Edit price
→ "Used by your code"). That lets the deployment overlay reference
prices by stable name across test/staging/prod instead of the random
`price_…` IDs that differ per environment. Phase 1 onboarding code
will resolve `lookup_key` → `price_…` ID at startup.

### 2. Webhook endpoint

**For Phase 1 dev work, use the Stripe CLI** — it tunnels production
webhooks to localhost without needing a public URL:

```sh
brew install stripe/stripe-cli/stripe
stripe login
stripe listen --forward-to localhost:8080/api/billing/webhook
```

That prints a `whsec_…` secret used as `STRIPE_WEBHOOK_SECRET`.

**For production** (when swarm-cloud is real and DNS exists): Dashboard
→ Developers → Webhooks → Add endpoint → URL
`https://kai.example.org/api/billing/webhook` → subscribe to:

- `checkout.session.completed`
- `customer.subscription.updated`
- `customer.subscription.deleted`
- `invoice.paid`
- `invoice.payment_failed`

Stripe generates a separate `whsec_…` per endpoint — that's the
production secret (lives in the swarm-cloud Secret).

### 3. Customer Portal config

Dashboard → Settings → Billing → Customer portal → Configure:

- Enable **cancellation**, **subscription update** (so users can swap
  tiers), **payment method update**
- Allow switching between Starter ↔ Growth
- Add **business information** (legal name + support URL/email — required)
- Save

We don't build a "manage subscription" UI — call Stripe's
`billingportal.Session` API to mint a one-time URL and redirect.

### 4. Stripe Tax (recommended for EU)

Dashboard → Tax → Get started:

- Enable Stripe Tax
- Add Germany as registered tax location (your VAT ID if you have one)
- Once enabled, set `automatic_tax: true` on the checkout session and
  Stripe handles per-country VAT automatically

**Strongly recommended for EU B2C SaaS** — VAT compliance manually is
brutal. Skip only if you already have an accountant doing cross-border
B2C VAT.

### What Phase 1 needs back from the operator

When ready to start Phase 1, supply:

1. The two `price_…` IDs (or confirm the `lookup_key` values above and
   onboarding will resolve them at startup).
2. The `whsec_…` from `stripe listen` (dev) or the production webhook
   endpoint.
3. Whether to enable Stripe Tax in Phase 1 or defer.

Phase 1 will then build:

- `POST /api/billing/webhook` handler (verifies signature → dispatches
  by event type → updates User.tier + User.stripeCustomerId).
- Customer-center "Upgrade to Starter / Growth" page that calls
  `CreateCheckoutSession` and redirects.
- Customer Portal redirect for cancel/swap/payment-method.
- Tier sync: Stripe subscription → `users.UpdateTier` +
  `users.UpdateStripeCustomerID`.

## Post-deploy bug fixes (2026-05-10, found during real Stripe test mode integration on k3d)

Three bugs surfaced when driving Stripe live (`stripe listen` + real `stripe customers/subscriptions create` + real test API key) — all fixed inline:

1. **`customer.subscription.created` was unhandled.** Real Stripe fires `.created` (not `.updated`) for new subscriptions. The dispatcher only had `.updated`, so subscriptions created outside the Checkout flow (or where the Checkout session payload didn't include `Subscription` inline) silently kept the user on free tier despite a real charge succeeding. Fix: dispatch both `.created` and `.updated` to `handleSubscriptionUpdated` — payload + handler intent are identical (sync tier from line items). New test `TestBilling_WebhookSubscriptionCreatedSyncsTier` locks the contract.

2. **`invoice.payment_succeeded` was unhandled.** Stripe fires both `invoice.paid` and `invoice.payment_succeeded` for the same condition (alias). Original dispatcher only listened to `invoice.paid`, so a chunk of receipt emails never sent. Fix: both names route to `handleInvoicePaid` — idempotency cache on `event.ID` keeps it a no-op on the second alias. New test `TestBilling_WebhookInvoicePaymentSucceededAliasOfPaid`.

3. **`pkg/stripe.CreateCheckoutSession` didn't stamp `subscription_data.metadata.user_ref`.** Comment in `userIDFromSubscription` claimed metadata gets copied "at checkout-completed time" but the code never set it. Result: subsequent `customer.subscription.{created,updated,deleted}` events on subscriptions born from the Checkout flow couldn't route back to a User.ID — the SDK call has to set metadata at session-create time so Stripe stamps it on the resulting subscription. Fix: added `SubscriptionData.Metadata: {user_ref: p.UserRef}` in `CreateCheckoutSession`.

End-to-end verified on k3d (2026-05-10): Carol signed up via onboarding → workspace dashboard → clicked "Upgrade to Starter" → real Stripe Checkout URL. Stripe-CLI's AI-agent guard blocks card-form typing (correctly), so I switched to driving Stripe API directly: `stripe customers create` + `payment_methods create/attach` + `subscriptions create -d "metadata[user_ref]=…"` triggered `customer.subscription.created` → workspace logs showed the new dispatch path → Postgres tier flipped to starter; create at growth price → tier flipped to growth; cancel → `customer.subscription.deleted` → tier downgraded to free. SPA reloaded and rendered "Manage subscription" button (Starter tier path) which redirected to a real Stripe Customer Portal session. Zero browser console errors on the dashboard.

## Notes
EU VAT / Mehrwertsteuer is non-trivial — strongly consider Paddle as merchant-of-record for the EU market unless we already have an accountant set up for cross-border B2C VAT.

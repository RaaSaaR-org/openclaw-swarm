---
id: TASK-016
aliases:
- TASK-016
title: 'Stripe billing: plans, subscriptions, customer portal, webhooks'
slug: stripe-billing-plans-subscriptions-customer-portal-webhooks
status: in-progress
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
updated: 2026-05-03
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

**Remaining phases blocked on upstream tasks:**
- Phase 1 (web-app webhook handler + customer-center "Upgrade" button): a `POST /api/billing/webhook` endpoint that calls `ParseWebhook`, dispatches by `event.Type`, and updates the User row. Plus a customer-center page that renders a "Choose tier" UI and redirects to `CreateCheckoutSession`. Needs the price IDs to exist in Stripe (the deployment overlay creates them via the Stripe dashboard or Terraform Stripe provider) and a real webhook secret (`whsec_...`) on the deployment.
- Phase 2 (dunning state machine): 3-retries / 14-day downgrade with email each step. Blocked on TASK-020 web-app email wiring.
- Phase 3 (Stripe Tax / EU VAT): operator decision; can ship in Phase 1 by setting `automatic_tax: true` on the checkout session.

## Acceptance Criteria
- [ ] Stripe checkout flow: signed-in user can upgrade to a paid tier and reach `success` page (Phase 1 — `CreateCheckoutSession` is ready in Phase 0)
- [ ] Webhook receiver verifies signatures and updates `User.tier` + `User.stripeCustomerId` (Phase 1 — `ParseWebhook` is ready in Phase 0; the dispatch + DB update is Phase 1)
- [ ] Cancellation via Stripe Customer Portal downgrades the user (with grace period) — verified end-to-end (Phase 1)
- [ ] Failed payment triggers dunning email + downgrade after configured retries (Phase 2)
- [ ] All webhook handlers are idempotent (Stripe retries them) (Phase 1)
- [x] Phase 0: `pkg/stripe` SDK wrapper with checkout + subscription + webhook verification; integration-tested against real test mode (2026-05-03)

## Notes
EU VAT / Mehrwertsteuer is non-trivial — strongly consider Paddle as merchant-of-record for the EU market unless we already have an accountant set up for cross-border B2C VAT.

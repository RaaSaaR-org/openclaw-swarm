---
id: TASK-016
aliases:
- TASK-016
title: 'Stripe billing: plans, subscriptions, customer portal, webhooks'
slug: stripe-billing-plans-subscriptions-customer-portal-webhooks
status: backlog
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

## Open Questions
- Stripe vs. Paddle (Paddle is merchant of record — handles VAT/sales tax for EU automatically; Stripe leaves that to us).
- Trial period? 14-day no-card-needed trial is conventional; might be unnecessary if free tier is generous.
- Invoice the user's email or expose downloadable invoices in customer-center?
- One subscription per user, or per workspace? Subscription per user is simpler.

## Acceptance Criteria
- [ ] Stripe checkout flow: signed-in user can upgrade to a paid tier and reach `success` page
- [ ] Webhook receiver verifies signatures and updates `User.tier` + `User.stripeCustomerId`
- [ ] Cancellation via Stripe Customer Portal downgrades the user (with grace period) — verified end-to-end
- [ ] Failed payment triggers dunning email + downgrade after configured retries
- [ ] All webhook handlers are idempotent (Stripe retries them)

## Notes
EU VAT / Mehrwertsteuer is non-trivial — strongly consider Paddle as merchant-of-record for the EU market unless we already have an accountant set up for cross-border B2C VAT.

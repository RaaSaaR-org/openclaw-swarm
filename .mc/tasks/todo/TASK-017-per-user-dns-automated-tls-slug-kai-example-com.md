---
id: TASK-017
aliases:
- TASK-017
title: Per-user DNS + automated TLS (<slug>.kai.example.com)
slug: per-user-dns-automated-tls-slug-kai-example-com
status: backlog
priority: 2
owner: ''
projects: []
customers: []
tags:
- dns
- tls
- saas
- operator
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---


# Per-user DNS + automated TLS (<slug>.kai.example.com)

## Why
For a public SaaS, each user's chat needs its own URL — typically `<slug>.kai.example.com` — with a valid TLS cert. The operator already creates an Ingress, but DNS-record automation and per-host cert issuance are not wired up. Wildcard certs sidestep some of this but at the cost of cert sprawl and CT-log exposure of every customer slug. This is the boundary between "demo at a port-forward" and "shareable URL we can put on a landing page."

## Decided
- **Topology: wildcard cert + wildcard DNS** (locked in 2026-05-03). One `*.kai.emai.io` Let's Encrypt cert via DNS-01; one wildcard DNS A record `*.kai.emai.io` → ingress controller IP. Per-slug certs rejected because Let's Encrypt's 50 certs/week per registered domain ceiling caps signup throughput at ~7/day, and tenant slugs don't end up in CT logs (privacy win for B2C).
- **DNS provider: Hetzner DNS** (locked in 2026-05-03). Already on Hetzner infra (per CLAUDE.md); free; supported by `external-dns`. Cloudflare considered for global DNS perf; rejected for v1 — Hetzner DNS is the native fit and one fewer vendor.
- **Domain: `kai.emai.io`** (per [[TASK-022]] decision).
- **Custom domains** (tenant brings their own `assistant.acme.de`) deferred to v2 as a paid-tier feature with HTTP-01 — clean separation, no early commitment.

## What
- cert-manager + a `ClusterIssuer` for Let's Encrypt with DNS-01 against Hetzner DNS (uses [hetzner-dns-webhook](https://github.com/vadimkim/cert-manager-webhook-hetzner) or similar).
- One `Certificate` resource for `*.kai.emai.io` in the ingress namespace; secret name `kai-emai-io-tls`.
- `external-dns` deployed cluster-wide with the Hetzner DNS provider; reads Ingress annotations to keep the wildcard A record in sync with the ingress controller IP.
- Operator updates `KaiInstance.status.externalURL` only after the per-slug Ingress is admitted (which it always is once the wildcard cert + DNS are ready). No per-slug cert wait state.
- Document cert renewal monitoring: cert-manager fires events on renewal, surface them in Grafana.
- Cleanup: when a `KaiInstance` is deleted ([[TASK-003]]), the per-slug Ingress goes via ownerRef cascade — wildcard cert + wildcard DNS A record are unaffected (they're shared).

## References
- `/Users/heussers/develop/emai/swarm/operator/internal/controller/kaiinstance_controller.go` (Ingress creation logic)
- `/Users/heussers/develop/emai/swarm/operator/api/v1alpha1/kaiinstance_types.go` (`status.externalURL`)
- cert-manager: https://cert-manager.io/docs/
- external-dns: https://kubernetes-sigs.github.io/external-dns/
- Let's Encrypt rate limits: https://letsencrypt.org/docs/rate-limits/ (50 certs/week per registered domain — wildcard avoids this)
- Hetzner DNS provider for external-dns: https://github.com/kubernetes-sigs/external-dns/blob/master/docs/tutorials/hetzner.md

## Open Questions
- Hetzner DNS webhook provider: pick the maintained one (cert-manager-webhook-hetzner is the de facto, but check its release cadence before committing).
- Apex `kai.emai.io` itself (the marketing site, [[TASK-022]]) gets a separate non-wildcard cert; that's standard, just call it out in the deploy doc.

## Acceptance Criteria
- [ ] After provisioning, `https://<slug>.kai.example.com` serves a valid cert
- [ ] DNS record is created automatically (no manual step)
- [ ] `status.externalURL` is only set when both Ingress and cert are ready
- [ ] Cert renewal is automated and verified (set test cert TTL low and verify renewal happens)
- [ ] Ingress + DNS + cert all clean up on KaiInstance deletion

## Notes
Pair with TASK-003 (deletion cleanup) so removed customers don't leave orphan DNS records and stale cert-manager Certificate resources.

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

## What
- Pick the topology:
  - **(A) Wildcard cert + wildcard DNS** (`*.kai.example.com` → ingress controller IP). Simplest. Cert via DNS-01 challenge. Slugs are still discoverable via CT logs only if specifically issued.
  - **(B) Per-slug cert + per-slug DNS A record.** Most flexible (custom domains later) but requires DNS API automation per signup.
- cert-manager with a `ClusterIssuer` (Let's Encrypt) — DNS-01 if doing wildcards, HTTP-01 if per-slug.
- DNS automation: external-dns (declarative, reads Ingress annotations) is cleanest. Provider depends on where DNS lives (Cloudflare, Hetzner, Route53).
- Operator updates `KaiInstance.status.externalURL` only after Ingress + cert are both ready.
- Optional v2: support custom domains (user maps their own `assistant.example.com` to `<slug>.kai.example.com` via CNAME, we issue an HTTP-01 cert for it).

## References
- `/Users/heussers/develop/emai/swarm/operator/internal/controller/kaiinstance_controller.go` (Ingress creation logic)
- `/Users/heussers/develop/emai/swarm/operator/api/v1alpha1/kaiinstance_types.go` (`status.externalURL`)
- cert-manager: https://cert-manager.io/docs/
- external-dns: https://kubernetes-sigs.github.io/external-dns/
- Let's Encrypt rate limits: https://letsencrypt.org/docs/rate-limits/ (50 certs/week per registered domain — wildcard avoids this)
- Hetzner DNS provider for external-dns: https://github.com/kubernetes-sigs/external-dns/blob/master/docs/tutorials/hetzner.md

## Open Questions
- Wildcard or per-slug? Recommend wildcard for v1 — simpler, no rate-limit risk, custom domains can be HTTP-01 later.
- Which DNS provider? Probably Hetzner since infrastructure is already on Hetzner CAX21.
- What's the SaaS domain? `kai.emai.io`? Decision is platform-branding-level.

## Acceptance Criteria
- [ ] After provisioning, `https://<slug>.kai.example.com` serves a valid cert
- [ ] DNS record is created automatically (no manual step)
- [ ] `status.externalURL` is only set when both Ingress and cert are ready
- [ ] Cert renewal is automated and verified (set test cert TTL low and verify renewal happens)
- [ ] Ingress + DNS + cert all clean up on KaiInstance deletion

## Notes
Pair with TASK-003 (deletion cleanup) so removed customers don't leave orphan DNS records and stale cert-manager Certificate resources.

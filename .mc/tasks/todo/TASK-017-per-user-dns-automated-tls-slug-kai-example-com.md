---
id: TASK-017
aliases:
- TASK-017
title: Per-user DNS + automated TLS (<slug>.kai.example.com)
slug: per-user-dns-automated-tls-slug-kai-example-com
status: in-progress
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

## Status

**Phase 0 (cert-manager + external-dns manifests + docs) — done** on 2026-05-03. Public swarm repo now ships:
- `kubernetes/cert-manager/` — `cluster-issuer.yml` + `cluster-issuer-staging.yml` + `wildcard-certificate.yml` (one wildcard `*.<domain>` Certificate via Let's Encrypt DNS-01 against Hetzner DNS) + README walking through the cert-manager + `cert-manager-webhook-hetzner` install flow with staging-first guidance.
- `kubernetes/external-dns/` — Namespace + RBAC + Deployment with the Hetzner provider (v0.18.0), `--policy=upsert-only` so it never deletes records on its own, and a README clarifying that the wildcard A record is one-time manual.
- Updated `docs/architecture.md` with a new TLS+DNS section explaining the wildcard-cert + per-slug-subdomain plan, the rate-limit rationale, and why today's path-based ingress hasn't flipped yet.

All manifests are inert until applied by the deployment overlay (`swarm-cloud` / `swarm-emai`); the public repo carries placeholder domains (`kai.example.org`) and references a Hetzner DNS API token Secret that the overlay creates from a private value.

**Open questions — closed:**
- Hetzner DNS webhook provider: `cert-manager-webhook-hetzner` (vadimkim) — the de-facto choice, documented as the install command in `kubernetes/cert-manager/README.md`.
- Apex cert: covered alongside the wildcard (`dnsNames` includes both `*.<domain>` and the apex) — one cert, two SANs.

**Remaining phases blocked on coordinated deploy:**
- Phase 1 (operator Ingress shape): flip `buildIngress` from path-based (`<domain>/ws/<slug>`) to host-based (`<slug>.<domain>/ws`). This changes the URL contract for every existing tenant; needs to land with the `swarm-emai` overlay updating each tenant's chat-bridge config + a coordinated cutover. Tracked as a follow-up phase.
- Phase 2 (operator status): `KaiInstance.status.externalURL` populated only after Ingress is admitted. Lands with Phase 1.
- Phase 3 (cleanup verification): with wildcard cert + wildcard DNS shared across all tenants, per-slug deletion already works — Ingress goes via ownerRef cascade ([[TASK-003]] already handled). Phase 3 is just the verification check on the cluster after Phase 1 lands.

## Acceptance Criteria
- [ ] After provisioning, `https://<slug>.kai.example.com` serves a valid cert (Phase 1+)
- [ ] DNS record is created automatically (no manual step) (external-dns wired in Phase 0; activates after the overlay deploy)
- [ ] `status.externalURL` is only set when both Ingress and cert are ready (Phase 2)
- [ ] Cert renewal is automated and verified (set test cert TTL low and verify renewal happens) (post-Phase 0 deploy verification)
- [ ] Ingress + DNS + cert all clean up on KaiInstance deletion (Ingress already cleans via ownerRef per [[TASK-003]]; wildcard cert + wildcard DNS are shared and deliberately survive deletion)
- [x] Phase 0: cert-manager + external-dns manifests + docs shipped in the public swarm repo (2026-05-03)

## Notes
Pair with TASK-003 (deletion cleanup) so removed customers don't leave orphan DNS records and stale cert-manager Certificate resources.

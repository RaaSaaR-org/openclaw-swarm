# external-dns with Hetzner DNS

Watches Ingress resources and keeps Hetzner DNS records in sync. The
operator-rendered Ingress for each tenant doesn't carry an explicit
`external-dns.alpha.kubernetes.io/hostname` annotation today — `external-dns`
discovers the host from `spec.rules[].host` automatically.

## Deployment shape

A single per-cluster Deployment with `--provider=hetzner`. We use the
maintained provider implementation; see
https://kubernetes-sigs.github.io/external-dns/v0.18.0/docs/tutorials/hetzner.

The wildcard A record (`*.<domain>` → ingress controller external IP) is
**not** created by external-dns from a per-tenant Ingress — it's a
cluster-wide one-shot. Create it once via Hetzner's web UI or a one-off
manifest. external-dns then keeps the apex (`<domain>`) and any custom-domain
records (TASK-017 v2 — bring-your-own-domain) in sync.

## Prerequisites

1. Hetzner DNS API token in a Secret in the same namespace as external-dns:
   ```sh
   kubectl -n external-dns create secret generic hetzner-dns-token \
     --from-literal=api-token='<YOUR-HETZNER-DNS-API-TOKEN>'
   ```
2. The DNS zone (`kai.example.org`) already exists in the Hetzner DNS console
   — external-dns syncs records inside the zone, it doesn't create zones.

## Files

| File | Purpose |
|---|---|
| `namespace.yml` | Dedicated `external-dns` namespace |
| `rbac.yml` | ClusterRole/Binding for watching Ingress + Service resources |
| `deployment.yml` | external-dns Deployment with the Hetzner provider |

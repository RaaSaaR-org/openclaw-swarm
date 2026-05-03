# cert-manager wiring for wildcard Let's Encrypt

These manifests configure cert-manager to issue ONE wildcard certificate
(`*.<domain>`) via Let's Encrypt's **DNS-01** challenge against Hetzner DNS,
then attach it to every per-tenant Ingress the operator creates. The
operator's per-tenant Ingress carries `cert-manager.io/cluster-issuer:
letsencrypt-prod` (see `operator/internal/controller/resources.go`); this
ClusterIssuer is what fulfils that annotation.

## Why DNS-01 (not HTTP-01)

- Wildcard certificates require DNS-01 — Let's Encrypt won't issue `*.foo`
  via HTTP-01.
- One wildcard cert dodges Let's Encrypt's 50 certs/week rate limit per
  registered domain — that limit caps signup throughput at ~7/day if every
  signup mints its own cert. Picked per [TASK-017].
- Tenant slugs never appear in CT logs — privacy win for B2C.

## Why Hetzner DNS

CLAUDE.md: the production cluster runs on Hetzner. Hetzner DNS is the native
fit, free, and supported by [`cert-manager-webhook-hetzner`][webhook] (the
de-facto webhook) and by `external-dns`.

[webhook]: https://github.com/vadimkim/cert-manager-webhook-hetzner

## Prerequisites

1. cert-manager installed in the cluster (`kubectl apply -f
   https://github.com/cert-manager/cert-manager/releases/download/v1.16.0/cert-manager.yaml`).
2. The Hetzner DNS webhook installed:
   ```sh
   helm install cert-manager-webhook-hetzner \
     --repo https://vadimkim.github.io/cert-manager-webhook-hetzner \
     --namespace cert-manager cert-manager-webhook-hetzner
   ```
3. A Hetzner DNS API token, stored in a Secret. **Do not commit the token.**
   The deployment overlay (`swarm-cloud` / `swarm-emai`) creates the Secret
   from a private value:
   ```sh
   kubectl -n cert-manager create secret generic hetzner-dns-token \
     --from-literal=api-token='<YOUR-HETZNER-DNS-API-TOKEN>'
   ```

## Files

| File | Purpose |
|---|---|
| `cluster-issuer.yml` | `letsencrypt-prod` ClusterIssuer using DNS-01 + Hetzner webhook |
| `cluster-issuer-staging.yml` | Same, but pointed at Let's Encrypt's staging endpoint (use first to avoid hitting prod rate limits during setup) |
| `wildcard-certificate.yml` | The single `*.<domain>` Certificate; produces the secret the operator's Ingresses reference |

## How to apply

```sh
# 1. Pick staging first to confirm the issuer works.
kubectl apply -f kubernetes/cert-manager/cluster-issuer-staging.yml
# 2. Watch the wildcard cert issue (DNS propagation takes 1-2 minutes).
kubectl -n emai-swarm get certificate -w
# 3. Once green, swap to prod and re-issue.
kubectl apply -f kubernetes/cert-manager/cluster-issuer.yml
kubectl -n emai-swarm delete certificate <name>  # forces re-issue against prod
kubectl apply -f kubernetes/cert-manager/wildcard-certificate.yml
```

## Renewal

cert-manager renews the cert at 2/3 of its lifetime (Let's Encrypt = 90 days
→ renewal at day 60). cert-manager fires `Issuing` and `Renewing` events on
the Certificate resource; surface them to whatever monitoring stack the
deployment overlay wires up.

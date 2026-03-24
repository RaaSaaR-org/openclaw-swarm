# EmAI Swarm Operator

Kubernetes operator that manages per-customer **Kai** agent instances in the EmAI Swarm. Each `KaiInstance` custom resource is reconciled into a full set of Kubernetes workloads: Deployment, Service, PVC, ConfigMap, NetworkPolicy, and (optionally) Ingress.

Kai instances run the OpenClaw agent image (`ghcr.io/openclaw/openclaw:latest`) with customer-specific identity files (SOUL.md, HEARTBEAT.md, openclaw.json) rendered from templates. The central agent **Kira** provisions Kai instances via `swarm-ctl`, which wraps kubectl for KaiInstance CRUD.

## KaiInstance CRD

**Group:** `swarm.emai.io/v1alpha1` &nbsp;|&nbsp; **Kind:** `KaiInstance`

### Spec Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `customerName` | string | yes | Display name (e.g. "Acme GmbH") |
| `projectName` | string | yes | Project context for the agent |
| `customerSlug` | string | no | DNS-safe identifier; auto-derived from `customerName` if omitted. Immutable once set. |
| `model` | string | no | LLM model override (default: `openrouter/stepfun/step-3.5-flash:free`) |
| `telegram.botTokenSecretRef` | string | no | Secret name containing key `bot-token` for Telegram integration |
| `gatewayAuth.mode` | `none` \| `token` | no | Gateway auth mode |
| `gatewayAuth.token` | string | no | Shared auth token (when mode=token) |
| `resources` | ResourceRequirements | no | Container resource overrides (default: 1Gi/100m request, 2Gi/500m limit) |
| `suspended` | bool | no | Scales Deployment to 0 without deleting state |
| `externalAccess` | bool | no | Create Ingress for external access (default: true) |

### Status

Phase lifecycle: `Provisioning` → `Running` → `Suspended` / `Failed`

Status exposes `gatewayURL` (in-cluster), `externalURL` (public), `ready`, `configHash`, and conditions for each child resource.

## Managed Resources

For each KaiInstance with slug `<slug>`, the operator creates:

| Resource | Name | Purpose |
|----------|------|---------|
| ConfigMap | `kai-<slug>-identity` | SOUL.md, HEARTBEAT.md, openclaw.json |
| PVC | `kai-<slug>-state` | 1Gi persistent agent state |
| Deployment | `kai-<slug>` | OpenClaw agent pod (init container copies identity into PVC) |
| Service | `kai-<slug>` | ClusterIP on port 18789 (gateway) |
| NetworkPolicy | `kai-<slug>-isolation` | Ingress/egress isolation (see below) |
| Ingress | `kai-<slug>-ws` | Traefik Ingress with TLS (if `externalAccess` enabled) |

All child resources have `ownerReference` set — deleting a KaiInstance cascades cleanup.

## Customer Isolation

Each Kai pod is network-isolated via a NetworkPolicy:

- **Ingress:** only from pods with label `emai.io/role: central` (Kira)
- **Egress:** DNS (port 53) and HTTPS (port 443) only — sufficient for OpenRouter and Telegram APIs
- **No K8s API access:** `automountServiceAccountToken: false`

Kai pods cannot communicate with each other or with any cluster service besides DNS.

## Quick Start

```bash
# Prerequisites: Go 1.24+, kubectl, access to a K8s cluster (k3d for local dev)

# Install CRDs
make install

# Run controller locally (outside cluster)
make run

# Or deploy to cluster
make docker-build docker-push IMG=ghcr.io/emai-ai/swarm-operator:latest
make deploy IMG=ghcr.io/emai-ai/swarm-operator:latest
```

## Example KaiInstance

```yaml
apiVersion: swarm.emai.io/v1alpha1
kind: KaiInstance
metadata:
  name: acme
  namespace: emai-swarm
spec:
  customerName: "Acme GmbH"
  projectName: "PROJ-001 Website Relaunch"
  telegram:
    botTokenSecretRef: acme-telegram-token
  gatewayAuth:
    mode: token
    token: "secret-gateway-token"
```

```bash
kubectl apply -f acme.yaml
kubectl get kaiinstances
# NAME   CUSTOMER    PHASE     READY   GATEWAY                              EXTERNAL                           AGE
# acme   Acme GmbH   Running   true    kai-acme.emai-swarm.svc:18789       https://kai.emai.dev/ws/acme       2m
```

## Uninstall

```bash
kubectl delete kaiinstances --all -n emai-swarm   # remove instances (cascades child resources)
make undeploy                                       # remove controller
make uninstall                                      # remove CRDs
```

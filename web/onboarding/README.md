# Onboarding

Web form that provisions a new customer Kai instance by creating a `KaiInstance` CR. Designed for non-engineers — type customer name and project, get a running pod a few seconds later.

Same shape as the admin-console: single Go binary serving an embedded Vite/TypeScript SPA, talks to the cluster via the Kubernetes dynamic client.

## What it can do

- Provision a new `KaiInstance` (customer name, project, slug, model, optional Telegram bot secret, external access toggle)
- Auto-derive a DNS-safe slug from the customer name (editable, validated)
- Live YAML preview of the resource that will be created
- Generate a fresh gateway token and surface it once on success, along with the customer chat URL

It cannot list, edit, or delete instances — that stays with the admin-console / `swarm-ctl`. The `Role` only grants `kaiinstances` `get/list/create`.

## Layout

```
onboarding/
├── server/         # Go binary (HTTP API + embedded SPA)
│   ├── main.go     # auth, validation, KaiInstance creation
│   ├── go.mod / go.sum
│   └── web/        # populated at build time from ../dist
├── src/            # Vite/TS frontend
│   ├── main.ts     # form, live YAML, success modal
│   ├── api.ts      # fetch wrapper, slugify, YAML renderer
│   └── style.css
├── index.html
├── package.json
├── vite.config.ts
└── Dockerfile      # multi-stage: node build → go build → distroless
```

## Local development

Requires Node 22+ and Go 1.25+. The frontend dev server proxies `/api` to port 8080.

```bash
# terminal 1 — backend
cd server
ADMIN_TOKEN=dev-token go run .

# terminal 2 — frontend with hot reload
npm install
npm run dev   # http://localhost:3002
```

Without a kubeconfig, validation still runs (so you can iterate on the form), but `POST /api/instances` returns 503.

## Build & deploy

```bash
docker build -t emai-onboarding:latest .
```

```bash
# create the auth token (one-time)
kubectl create secret generic onboarding-auth \
  --from-literal=token="$(openssl rand -hex 32)" \
  -n emai-swarm

# apply manifests (already wired into top-level kustomization)
kubectl apply -k ../../kubernetes/

# port-forward for access
kubectl port-forward -n emai-swarm svc/onboarding 8080:8080
# open http://localhost:8080 and paste the token
```

K8s manifests live at [`../../kubernetes/onboarding/`](../../kubernetes/onboarding/).

## API

All `/api/*` routes require `Authorization: Bearer <ADMIN_TOKEN>`.

| Method | Path                  | Body                                                                                  | Returns                                                |
|--------|-----------------------|---------------------------------------------------------------------------------------|--------------------------------------------------------|
| GET    | `/healthz`            | —                                                                                     | `ok` (no auth)                                         |
| GET    | `/api/auth`           | —                                                                                     | `{namespace}` — verifies token                         |
| POST   | `/api/instances`      | `{customerName, projectName, customerSlug, model?, telegramSecretRef?, externalAccess?}` | `{name, namespace, customerSlug, gatewayToken}` (201)  |

Validation runs before the K8s call, so empty/invalid input always returns 400 regardless of cluster state.

## Env vars

| Var               | Default       | Notes                                          |
|-------------------|---------------|------------------------------------------------|
| `ADMIN_TOKEN`     | (required)    | Bearer token; loaded from `onboarding-auth` Secret in cluster |
| `ADDR`            | `:8080`       | Listen address                                 |
| `SWARM_NAMESPACE` | `emai-swarm`  | Namespace where `KaiInstance`s are created     |
| `KUBECONFIG`      | (auto)        | Used outside cluster; in-cluster config first  |

## What this is not

This is the **technical** provisioning step only — it creates the K8s resource and lets the operator reconcile it into a running pod. It does not handle the broader customer onboarding flow: USER.md contacts, SOUL.md persona overrides, swarm-config customer files, or `.env` secrets. For the full flow see `scripts/new-customer.sh` (writes to the private `swarm-config/` repo).

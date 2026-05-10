# Status Page

Customer-facing status page that answers one question: "Is my Kai online?".

URL pattern mirrors chat — the customer gets a personal link they bookmark:

```
https://status.<host>/status/<slug>?token=<gateway-token>
```

The same token they use for chat. No separate credentials, no admin panel.

## What it shows

A single status card with one of five states:

| State          | When                                                | Color   |
|----------------|-----------------------------------------------------|---------|
| **Online**       | KaiInstance phase=Running, ready=true               | green   |
| **Setting up**   | phase=Provisioning, or Running but not yet ready    | yellow  |
| **Paused**       | phase=Suspended, or `spec.suspended=true`           | blue    |
| **Issue detected** | phase=Failed                                      | red     |
| **Status link not valid** | Wrong token, missing token, or unknown slug | red     |

Plus the customer name, project, last update timestamp, and a "checked X seconds ago" footer. Auto-refreshes every 15s.

The 401 case returns the **same** response for unknown slugs and wrong tokens, so probing slugs reveals nothing about who is or isn't a customer.

## Layout

```
status-page/
├── server/         # Go binary (HTTP API + embedded SPA)
│   ├── main.go     # token validation against KaiInstance.spec.gatewayAuth.token
│   ├── go.mod / go.sum
│   └── web/        # populated at build time from ../dist
├── src/            # Vite/TS frontend
│   ├── main.ts     # status card with auto-refresh
│   └── style.css   # dark theme with pulse animations
├── index.html
├── package.json
├── vite.config.ts
└── Dockerfile      # multi-stage: node build → go build → distroless
```

## Local development

Requires Node 22+ and Go 1.25+.

```bash
# terminal 1 — backend
cd server
go run .

# terminal 2 — frontend with hot reload
npm install
npm run dev   # http://localhost:3003/status/<slug>?token=<token>
```

Without a kubeconfig, validation against the cluster cannot run, so all status fetches return 503 (the page surfaces this as "Status temporarily unavailable" and keeps retrying).

## Build & deploy

```bash
docker build -t emai-status-page:latest .
```

```bash
# no Secret needed — auth happens per-request via the customer's gateway token
kubectl apply -k ../../kubernetes/

# port-forward for local access
kubectl port-forward -n swarm-system svc/status-page 8080:8080
```

K8s manifests live at [`../../kubernetes/status-page/`](../../kubernetes/status-page/).

## API

| Method | Path                         | Auth                           | Returns                                |
|--------|------------------------------|--------------------------------|----------------------------------------|
| GET    | `/healthz`                   | none                           | `ok`                                   |
| GET    | `/api/status/{slug}`         | `?token=` or `Bearer` header   | `{customerName, projectName, status, ready, message, lastUpdate}` |

The token is matched against `spec.gatewayAuth.token` on the corresponding `KaiInstance` (using a constant-time compare). Anything else — wrong token, missing token, missing instance, malformed slug — returns the same 401.

## Env vars

| Var               | Default       | Notes                                          |
|-------------------|---------------|------------------------------------------------|
| `ADDR`            | `:8080`       | Listen address                                 |
| `SWARM_NAMESPACE` | `swarm-system`  | Namespace where `KaiInstance`s live            |
| `KUBECONFIG`      | (auto)        | Used outside cluster; in-cluster config first  |

## Security model

- **No admin token.** The page is meant to be linked to from outside the cluster, often shared in customer welcome emails. Putting an admin token in a customer-facing path would be a leak waiting to happen.
- **Per-customer auth via gateway token.** Same secret as chat, so the customer doesn't need to manage two credentials.
- **Constant-time compare.** Token comparison uses `crypto/subtle` to avoid timing leaks.
- **Uniform 401.** Wrong-slug and wrong-token both produce identical responses, so the page can't be used as an enumeration oracle.
- **Read-only RBAC.** The ServiceAccount has `kaiinstances` `get` only. It cannot list, patch, or delete anything.

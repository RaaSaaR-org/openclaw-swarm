# Admin Console

Operator dashboard for the Swarm platform. Lists all `KaiInstance` CRs in the cluster and lets an admin suspend or resume them.

Single Go binary that serves a Vite/TypeScript SPA from an embedded filesystem and exposes a small JSON API backed by the in-cluster Kubernetes dynamic client.

## What it can do

- List all `KaiInstance` CRs in the namespace, with phase, ready, gateway URL, age
- View detail (spec + status + conditions + raw JSON) for one instance
- Suspend (scale to 0 replicas, data preserved) and resume an instance
- Auto-refresh every 5s

It cannot create or delete instances — that stays with `swarm-ctl` (kira-central) by design. The RBAC `Role` only grants `get/list/watch/patch` on `kaiinstances`.

## Layout

```
admin-console/
├── server/         # Go binary (HTTP API + embedded SPA)
│   ├── main.go
│   ├── go.mod / go.sum
│   └── web/        # populated at build time from ../dist
├── src/            # Vite/TS frontend
│   ├── main.ts     # app shell, login, table, detail, modals
│   ├── api.ts      # fetch wrapper + token storage
│   └── style.css   # dark theme matching chat
├── index.html
├── package.json
├── vite.config.ts
└── Dockerfile      # multi-stage: node build → go build → distroless
```

## Local development

Requires Node 22+ and Go 1.25+. The frontend dev server proxies `/api` to the Go server on port 8080.

```bash
# terminal 1 — backend
cd server
ADMIN_TOKEN=dev-token go run .

# terminal 2 — frontend with hot reload
npm install
npm run dev   # http://localhost:3001
```

Without a kubeconfig (`~/.kube/config` or `KUBECONFIG`), the backend starts but `/api/*` returns 503. Point at a cluster that has the `KaiInstance` CRD installed (the operator's `make install`) to do real work.

## Build & deploy

```bash
docker build -t emai-admin-console:latest .
```

```bash
# create the auth token (one-time)
kubectl create secret generic admin-console-auth \
  --from-literal=token="$(openssl rand -hex 32)" \
  -n swarm-system

# apply manifests (already wired into top-level kustomization)
kubectl apply -k ../../kubernetes/

# port-forward for access
kubectl port-forward -n swarm-system svc/admin-console 8080:8080
# open http://localhost:8080 and paste the token
```

K8s manifests live at [`../../kubernetes/admin-console/`](../../kubernetes/admin-console/).

## API

All `/api/*` routes require `Authorization: Bearer <ADMIN_TOKEN>`.

| Method | Path                                | Returns                                |
|--------|-------------------------------------|----------------------------------------|
| GET    | `/healthz`                          | `ok` (no auth)                         |
| GET    | `/api/instances`                    | summary list of all instances          |
| GET    | `/api/instances/{name}`             | full unstructured CR JSON              |
| POST   | `/api/instances/{name}/suspend`     | `{name, suspended: true}`              |
| POST   | `/api/instances/{name}/resume`      | `{name, suspended: false}`             |

## Env vars

| Var               | Default       | Notes                                          |
|-------------------|---------------|------------------------------------------------|
| `ADMIN_TOKEN`     | (required)    | Bearer token; loaded from `admin-console-auth` Secret in cluster |
| `ADDR`            | `:8080`       | Listen address                                 |
| `SWARM_NAMESPACE` | `swarm-system`  | Namespace to scope `KaiInstance` lookups       |
| `KUBECONFIG`      | (auto)        | Used outside cluster; in-cluster config first  |

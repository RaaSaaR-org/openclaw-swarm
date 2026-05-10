# mc API gateway — design

## Context

`mc api serve` (mission-control v0.2+) exposes the full mc CRUD surface as bearer-authenticated HTTP/JSON, single-tenant. To serve N tenants from one mc instance — which is the swarm's natural shape — we need a thin gateway in front that:

1. Authenticates clients with **per-tenant** bearer tokens (mc only knows about service-internal tokens).
2. **Scopes** every request so a tenant can only read/write its own subtree of the HQ repo.
3. **Forwards** the request upstream to mc with the gateway's own internal token.

Tenant scoping intentionally does **not** live in mc itself. mc is a generic OSS tool; coupling it to swarm-tenant semantics would push customer-shaped concerns into a tool that other users will run for completely different purposes. The same justification motivates keeping the gateway in the public `swarm` repo (rather than `swarm-emai`): the pattern is generic to any multi-tenant deployment of mc, not specific to the EmAI tenant.

This document describes the gateway's contract, request-scoping rules, and deployment shape. It does not specify EmAI's migration plan — see `swarm-emai/docs/api-migration.md` for that.

## Architecture

```
                        ┌───────────────────────────┐
                        │  HQ git working tree      │
                        │  (mounted into Kira pod)  │
                        └──────────────▲────────────┘
                                       │ atomic_write + read
                                       │
            ┌──────────────────────────┴──────────────────────────┐
            │  mc api serve                                       │
            │  127.0.0.1:5100 (loopback only)                     │
            │                                                     │
            │  Auth = single internal token (mounted secret)      │
            │  Scope = entire HQ repo (no tenant logic in mc)     │
            └──────────────▲──────────────────────────────────────┘
                           │ HTTP, Authorization: Bearer <internal>
                           │
            ┌──────────────┴──────────────┐
            │  mc-gateway                 │   ClusterIP service in
            │  :8080 (cluster-internal)   │   the same namespace
            │                             │
            │  Auth = per-tenant tokens   │
            │         (mounted secret)    │
            │  Scope = inject + verify    │
            │          customer filter    │
            └──────────────▲──────────────┘
                           │ HTTP, Authorization: Bearer <tenant>
                           │
        ┌──────────────────┴──────────────────┐
        │  Kai pods (one per customer)        │
        │  Read MC_API_BASE + MC_API_TOKEN    │
        │  from env, drive the API via curl   │
        │  (or the mc-client wrapper)         │
        └─────────────────────────────────────┘
```

The gateway is the only inbound caller of `mc api serve`. mc's `--bind 127.0.0.1` keeps it off the cluster network entirely; the gateway and mc share the Kira pod (gateway as a sidecar) or co-locate on the same node.

## Token model

Both stores live in K8s Secrets and are mounted read-only.

**Upstream token** (`mc-api-tokens`): a single argon2id-hashed token in mc's standard tokens.yml format, with `[read, write]` capabilities. Loaded by `mc api serve --tokens-file`.

**Tenant tokens** (`mc-gateway-tokens`): the gateway's own format. One entry per identity:

```yaml
tokens:
  # Admin token — the central agent (Kira). Pass-through, no scoping.
  - name: kira-admin
    hash: $argon2id$v=19$m=19456,t=2,p=1$...
    role: admin

  # Tenant tokens — one per Kai instance. Scoped to a single customer subtree.
  - name: kai-acme
    hash: $argon2id$v=19$m=19456,t=2,p=1$...
    role: tenant
    slug: acme
    customer_id: CUST-001
```

Token storage is identical to mc's pattern (argon2id PHC hashes, no plaintext on disk). The gateway should reuse the SHA-256 fast-path cache pattern mc uses internally — argon2 verification is intentionally slow, and uncached every-request verification is a DoS surface.

## Request scoping rules

The gateway applies these rules **after** authentication and **before** forwarding upstream. The token's `role` and `customer_id` are constant for the lifetime of the request.

### Admin role (`role: admin`)
Pass through unchanged. Strip the inbound `Authorization` header and replace with `Bearer <upstream-internal-token>`. Log the token name.

### Tenant role (`role: tenant, customer_id: CUST-NNN`)

| Endpoint | Method | Action |
|---|---|---|
| `/healthz`, `/readyz`, `/v1/openapi.json`, `/v1/docs` | GET | Pass through (public on mc anyway). |
| `/v1/config`, `/v1/status` | GET | Pass through. Status counts may leak cross-tenant signal — if that matters, return a tenant-filtered subset (out of scope here). |
| `/v1/tasks` | GET | **Inject** `customer=CUST-NNN` query param. If client supplied a different `customer`, **reject 400**. |
| `/v1/tasks` | POST | **Require** `customer: "CUST-NNN"` in the JSON body; reject with `400` if missing or different. |
| `/v1/tasks/{id}/move` | POST | Forward upstream, then verify the response's `path` contains the tenant's customer dir. If not (cross-tenant move attempt), return `404`. |
| `/v1/entities/task` | GET | Inject `customer` query param (mirrors `/v1/tasks` behaviour). |
| `/v1/entities/task/{id}` | GET | Forward upstream, then check the response's `frontmatter.customer` matches the tenant. If not, return `404` (not `403` — avoids existence leaks). |
| `/v1/entities/{kind}` for `kind ∈ {meeting, research, sprint, proposal, contact}` | GET | Same pattern: inject `customer` filter, post-verify single-entity GETs. |
| `/v1/entities/customer/{id}` | GET | Allow only if `id == CUST-NNN`. Otherwise `404`. |
| `/v1/customers`, `/v1/projects`, `/v1/contacts` | POST | **403** — tenants cannot create top-level customers/projects/contacts; the central agent provisions those. |
| `/v1/index`, `/v1/validate` | POST | **403** — repo-wide maintenance is admin-only. |

The single-entity-GET post-verification pattern (proxy upstream → check returned `customer` field → drop if mismatch) is the safety net for any endpoint where filter injection isn't sufficient.

Returning `404` (not `403`) for cross-tenant access avoids leaking the existence of other tenants' entities. The same response shape covers genuinely-missing entities, so a tenant cannot probe for IDs.

## Implementation shape

Language: **Go**, matching the existing services in `swarm/web/` (`workspace`, `chat`, `admin-console`). Reuses the same auth helpers (`pkg/auth`) and Dockerfile pattern.

Suggested layout:

```
swarm/cmd/mc-gateway/
├── main.go              entry point, env config, server bootstrap
├── auth.go              token-file load, argon2 verify + sha256 cache
├── scope.go             per-method scoping rules (the table above)
├── proxy.go             request rewrite + upstream forward
├── proxy_test.go        unit tests per rule
├── Dockerfile           multi-stage build, scratch runtime
└── go.mod
```

The proxy itself is a `httputil.ReverseProxy` with a custom `Director` that:
1. Strips inbound `Authorization`.
2. Rewrites query string / body per the rule table.
3. Sets `Authorization: Bearer <internal>`.
4. Adds `X-Mc-Gateway-Tenant: <slug>` so upstream logs/audit trails can attribute requests.

Request rewriting on bodies is the trickiest part. Recommended approach: parse JSON, mutate, re-serialise. Cap body size at the same 64 KiB limit mc enforces — anything larger is a misuse.

## K8s deployment shape

```yaml
# Service
apiVersion: v1
kind: Service
metadata:
  name: mc-gateway
  namespace: <your-ns>
spec:
  type: ClusterIP
  selector:
    app: mc-gateway
  ports: [{ port: 8080, targetPort: 8080 }]
---
# Deployment (1 replica — it's stateless but talks to a single mc instance)
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mc-gateway
spec:
  replicas: 1
  selector: { matchLabels: { app: mc-gateway } }
  template:
    metadata: { labels: { app: mc-gateway } }
    spec:
      containers:
        - name: gateway
          image: ghcr.io/<org>/mc-gateway:<tag>
          env:
            - name: MC_UPSTREAM_URL
              value: http://127.0.0.1:5100
            - name: MC_UPSTREAM_TOKEN
              valueFrom: { secretKeyRef: { name: mc-gateway-upstream, key: token } }
            - name: GATEWAY_TOKENS_PATH
              value: /etc/mc-gateway/tokens.yml
          volumeMounts:
            - name: tokens
              mountPath: /etc/mc-gateway
              readOnly: true
          livenessProbe: { httpGet: { path: /healthz, port: 8080 } }
          readinessProbe: { httpGet: { path: /readyz, port: 8080 } }
      volumes:
        - name: tokens
          secret: { secretName: mc-gateway-tokens }
```

Co-locate with mc by either (a) running the gateway as a sidecar in the Kira pod and pointing at `127.0.0.1:5100`, or (b) running it as a separate Deployment with mc behind another loopback-only service. Option (a) is simpler for a single-instance deployment and mirrors how the existing `mc-dashboard` sidecar works.

No K8s RBAC needed — the gateway only talks HTTP to mc upstream and reads its own mounted Secret. `automountServiceAccountToken: false`.

## Why tenant scoping isn't in mc

Three reasons, in priority order:

1. **mc is a generic OSS tool.** Other users (small teams, individual developers) want a single-tenant API, not multi-tenancy semantics. Baking tenant rules into mc means every release has to consider how a feature interacts with tenant scoping, even though most users have one tenant.
2. **Customer/slug semantics are swarm-specific.** The notion of a "tenant" mapping to a `CUST-NNN-<slug>` directory is an EmAI-tenant convention. Other multi-tenant deployments might shard by user, project, or workspace.
3. **Auth is layered.** Adding gateway in front gives a clear separation: mc trusts a single token, gateway maps untrusted tenant tokens to scoped requests. If we ever need MFA, OIDC, IP allowlisting per tenant, or audit-on-cross-tenant-attempt, those go in the gateway and don't touch mc.

## Open items

- **Audit on cross-tenant attempts.** The current spec returns `404` and logs a warning. If your environment has a stricter posture (e.g. tenants are mutually-suspicious), bump that to a structured audit event per attempt.
- **Rate limiting per tenant.** Not specified here. Add a `limits` block per token entry if needed (`rps`, `burst`).
- **Token rotation.** Same gap as mc — restart-to-reload today. SIGHUP-reload should land before any production rollout.
- **Cross-process coordination.** The gateway is stateless; running multiple replicas is fine. But mc itself is single-process per repo (it holds an exclusive flock on `<repo>/.mc-api.lock`). Keep `mc api serve` at one replica.

## Reference

- Upstream API contract: `mission-control/docs/api.md` (especially §7 on the gateway pattern).
- Live OpenAPI spec: `<mc-host>/v1/openapi.json`.
- Existing in-repo Go services that use the same patterns: `swarm/web/workspace/server/` (auth + K8s secret loading), `swarm/web/chat/server/` (proxy patterns).

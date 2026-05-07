# Swarm web-app API specs

OpenAPI 3.1 contracts for every JSON HTTP API the swarm web servers expose.
Hand-authored (not generated from code annotations) — keep them in sync when
handlers change. CI lints them on every PR with Spectral.

| Spec | Server | Auth | Audience |
|---|---|---|---|
| [admin.yaml](admin.yaml) | `web/admin-console/server` | Shared `ADMIN_TOKEN` | Platform operator |
| [center.yaml](center.yaml) | `web/workspace/server` | Per-tenant JWT cookie | Tenant admin |
| [onboarding.yaml](onboarding.yaml) | `web/onboarding/server` | Shared `ADMIN_TOKEN` | Platform operator (today; TASK-013 opens self-serve) |
| [status.yaml](status.yaml) | `web/status-page/server` | Per-tenant gateway token | Anyone with the token |

`web/chat/server` is the WebSocket gateway-bridge — not a JSON API in
the OpenAPI sense — and is documented in `docs/architecture.md` instead.

## Validating locally

```bash
# One-off install
npm install -g @stoplight/spectral-cli

# Lint each spec
spectral lint docs/api/*.yaml
```

## Rendering as HTML

Quick local preview with Redoc:

```bash
npx -y @redocly/cli preview-docs docs/api/center.yaml
# (Redoc opens a browser tab)
```

For a hosted version, point Redoc / Stoplight Elements at the raw YAML on
GitHub. Publishing to GitHub Pages is tracked separately (`.mc/` TASK-022 ties
this into the marketing site).

## When you change a handler

1. Update the handler in `web/<app>/server/`.
2. Update the matching `docs/api/<app>.yaml` to reflect the new shape (route,
   request body, response codes, error paths).
3. Run `spectral lint docs/api/*.yaml`.
4. Add a test in `web/<app>/server/main_test.go` that pins the new behaviour.

CI fails if specs don't lint clean — that's the drift backstop. There's no
auto-generation today; if drift becomes a recurring problem, switch to
`swaggo/swag` annotations on the handlers (see TASK-008 for the trade-off
discussion).

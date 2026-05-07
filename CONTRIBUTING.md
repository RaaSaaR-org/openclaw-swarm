# Contributing to OpenClaw Swarm

Thanks for your interest in contributing! This guide will get you up and running.

## Quick Start

```bash
# Prerequisites: Go 1.25+, Node 22+, Docker, k3d, kubectl

# 1. Clone
git clone https://github.com/RaaSaaR-org/openclaw-swarm.git
cd openclaw-swarm

# 2. Run tests
make test

# 3. One-shot dev cluster setup (creates k3d cluster + installs CRDs)
make dev

# 4. Run the operator locally (terminal 1)
make dev-operator

# 5. Run the chat UI with hot-reload (terminal 2)
make dev-chat

# Run `make help` to see every target.
```

After provisioning a `KaiInstance`, `make smoke-test` walks the full creation
→ ConfigMap → gateway-healthz path and cleans up after itself.

## Project Structure

```
swarm/
├── operator/                # K8s Operator (Go/Kubebuilder) — manages KaiInstance CRDs
├── pkg/                     # Shared Go libs across web/* servers (e.g. pkg/auth)
├── web/
│   ├── chat/       # Tenant chat UI (Vite + TypeScript + Go server)
│   ├── workspace/     # Tenant dashboard + user management
│   ├── admin-console/       # Platform-operator console (lists/manages KaiInstances)
│   ├── onboarding/          # Provisioning API (creates KaiInstances; ADMIN_TOKEN-gated today)
│   └── status-page/         # Per-tenant public status endpoint
├── kubernetes/              # Base K8s manifests (namespace, RBAC, deployments)
├── agents/                  # Agent identity templates (SOUL.md, AGENTS.md, etc.)
├── scripts/                 # swarm-ctl, provisioning, health-check
├── docker/                  # Docker Compose for local dev
├── terraform/               # Hetzner Cloud IaC
└── docs/                    # Deployment + onboarding guides
```

## Making Changes

### Operator (Go)

```bash
cd operator
make test           # Run unit tests
make lint           # Run linter
make fmt            # Format code
make manifests      # Regenerate CRD manifests after changing types
make run            # Run locally against current kubeconfig
```

When changing the CRD types (`api/v1alpha1/kaiinstance_types.go`), always run `make manifests` and `make generate` to update generated code.

### Web Apps (TypeScript + Go)

Each `web/<app>/` is a Vite SPA + a Go server that embeds the built SPA via
`go:embed`. Every app builds the same way:

```bash
cd web/<app>          # one of: chat, workspace, admin-console, onboarding, status-page
npm install
npm run dev           # Vite dev server with hot-reload (localhost:3000)
npm run build         # Production build → web/dist
cd server
go vet ./...
go build .            # Builds the Go binary that serves the embedded SPA on :8080
```

`chat` and `workspace` share `pkg/auth/` (JWT + argon2id
helpers); all 5 apps share `web/shared/branding/` (theme contract — see
[`web/README.md`](web/README.md)). All 5 Dockerfiles build from the swarm
repo root so they can `COPY pkg/` and `COPY web/shared/`. Don't introduce
a new shared lib without reading [`pkg/auth/`](pkg/auth/) first — the
relative-`replace` pattern is documented there.

**Branding rules for app CSS:**
- Use canonical tokens (`var(--accent)`, `var(--bg)`, `var(--text)`, …) or
  the legacy aliases mapped in each app's `style.css` `:root` block. Don't
  introduce new hex literals or `rgba(255, 103, 0, …)` orange tints — those
  bake the brand into the bundle and break the override path.
- Don't inline `<title>EmAI…</title>` or `<title>Kai…</title>` in
  `index.html`. Ship `<title>Loading…</title>` and let `applyBranding()`
  set the real one from `branding.json`.
- Don't import a font directly in app CSS (`@import url(…Space+Grotesk…)`).
  The `web/shared/branding/theme.css` owns the `@font-face` line; apps
  just read `var(--font-family)` and `var(--font-mono)`.

### Agent Templates

Templates in `operator/internal/controller/templates/` use `{{PLACEHOLDER}}` syntax. When adding or modifying templates:

1. Update the template file (`.tmpl` or `.md`)
2. If adding a new file: update `templates.go` (rendering) and `resources.go` (ConfigMap + init container)
3. Run `make test` to verify template rendering

### Tests

All tests should pass before submitting a PR:

```bash
make test           # All tests
make lint           # All linters
```

## Pull Request Process

1. Fork the repo and create a feature branch
2. Make your changes
3. Run `make test` and `make lint`
4. Submit a PR with a clear description of what and why
5. One approval required for merge

Keep PRs focused — one feature or fix per PR.

## Code Style

- **Go:** Standard `gofmt` formatting, no special conventions
- **TypeScript:** Strict mode, ES2023 target
- **Markdown:** Agent identity files use plain ASCII (no special characters) for container compatibility
- **Commits:** Short, descriptive messages. No required format.

## Questions?

Open an issue or start a discussion. We're happy to help.

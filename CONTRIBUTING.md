# Contributing to OpenClaw Swarm

Thanks for your interest in contributing! This guide will get you up and running.

## Quick Start

```bash
# Prerequisites: Go 1.22+, Node 22+, Docker, k3d, kubectl

# 1. Clone
git clone https://github.com/RaaSaaR-org/openclaw-swarm.git
cd openclaw-swarm

# 2. Run tests
make test

# 3. Set up local dev cluster
make dev-cluster

# 4. Run the operator locally
make dev-operator

# 5. In another terminal: run the chat UI
make dev-chat
```

## Project Structure

```
swarm/
├── operator/           # K8s Operator (Go/Kubebuilder) — manages KaiInstance CRDs
├── web/customer-chat/  # Customer chat UI (Vite + TypeScript)
├── kubernetes/         # Base K8s manifests
├── agents/             # Agent identity templates
├── scripts/            # Provisioning and management scripts
├── docker/             # Docker Compose for local dev
└── docs/               # Deployment guides
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

### Customer Chat (TypeScript)

```bash
cd web/customer-chat
npm install
npm run dev         # Dev server with hot-reload at localhost:3000
npm run build       # Production build
```

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

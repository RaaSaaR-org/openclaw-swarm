# CLAUDE.md

Dev instructions for working in the Swarm repo.

## What This Is

**Swarm** is a deployment and orchestration platform for OpenClaw agent instances. It contains Docker Compose configs, Kubernetes manifests + operator, Terraform infrastructure, agent identity files, a customer chat UI, and provisioning scripts.

## Agents

- **Central agent** — internal operations agent (one instance)
- **Customer agent** — customer-facing agent (one isolated instance per customer, same persona everywhere)

## Architecture

**Primary deployment: Kubernetes** (Docker Compose remains for local dev)

Each agent runs as an isolated pod from one image (`ghcr.io/openclaw/openclaw:latest`). Each gets:
- Own `openclaw.json` config (via ConfigMap + init container copy)
- Own SOUL.md workspace files
- Own persistent volume (`/home/node/.openclaw/`)
- Own NetworkPolicy for isolation

Customer instances are managed by the **Swarm Operator** via `KaiInstance` CRDs. The central agent provisions them using `swarm-ctl`.

## Key Concepts

- **OpenClaw**: Self-hosted AI assistant platform. Config at `~/.openclaw/openclaw.json`, workspace files at `~/.openclaw/workspace/`.
- **SOUL.md**: Defines an agent's personality, scope, and operating rules. Loaded at session start.
- **openclaw.json**: Main config file (JSON5). Defines `agents.list[]`, `channels`, `gateway`, `session`, `tools`.
- **OpenRouter**: LLM provider. Configurable model per instance.
- **Swarm Operator**: K8s operator (Go/Kubebuilder) that reconciles `KaiInstance` CRDs into workloads.
- **swarm-ctl**: CLI wrapper for kubectl — the central agent uses it to provision/manage customer instances.
- **Customer Chat UI**: Vite + TypeScript web chat at `web/customer-chat/` — connects via WebSocket with Ed25519 device auth.
- **Private config repo**: Deployments typically have a sibling `swarm-config/` repo with business-specific overlays (secrets, customer identities, K8s manifest overrides). This repo is the generic platform only.

## Directory Layout

```
swarm/
├── docker/                        # Docker Compose deployment (dev/legacy)
│   ├── Dockerfile.agent           # Based on ghcr.io/openclaw/openclaw:latest
│   ├── docker-compose.yml         # Central + customer services
│   ├── docker-compose.dev.yml     # Dev overrides (exposed ports, no isolation)
│   ├── .env                       # Secrets (gitignored)
│   └── .env.example               # Template
├── agents/
│   ├── central/                   # Central agent: openclaw.json, SOUL.md, HEARTBEAT.md, etc.
│   └── customer-template/         # Templates with {{PLACEHOLDERS}} for new customers
├── demo/                          # Demo customer instances with sample data
├── web/
│   └── customer-chat/             # Customer-facing chat UI (Vite + TypeScript)
│       ├── src/gateway.ts         # OpenClaw WebSocket client
│       ├── src/device.ts          # Ed25519 keypair + challenge signing
│       ├── src/main.ts            # Chat app with markdown rendering
│       ├── src/style.css          # Dark mode UI
│       ├── Dockerfile             # nginx static build
│       └── nginx.conf             # SPA routing
├── operator/                      # K8s Operator (Go/Kubebuilder) for KaiInstance CRD
│   ├── api/v1alpha1/              # CRD type definitions
│   ├── internal/controller/       # Reconciliation loop + template rendering
│   ├── config/                    # CRD, RBAC, manager manifests, sample CRs
│   └── Makefile                   # build, install, run, deploy targets
├── scripts/                       # provision, teardown, backup, health-check, swarm-ctl
├── kubernetes/                    # K8s manifests (base platform — central agent, RBAC, NetworkPolicies)
├── terraform/                     # Hetzner Cloud provisioning
└── docs/                          # Deployment guide, onboarding
```

## Swarm Operator (K8s)

The `operator/` directory contains a Kubernetes Operator (Go + Kubebuilder) that manages customer instances via a `KaiInstance` CRD (`swarm.emai.io/v1alpha1`).

- **CRD:** `KaiInstance` — defines customerName, projectName, model, gatewayAuth, suspended, resources
- **Reconciles into:** ConfigMap, Deployment, Service, PVC, NetworkPolicy per customer
- **Central agent integration:** `swarm-ctl` CLI (at `/shared-bin/swarm-ctl` inside the central agent pod) wraps kubectl for KaiInstance CRUD
- **RBAC:** Central agent can only manage KaiInstance CRs; customer pods get no K8s API access (`automountServiceAccountToken: false`)
- **Dev:** `k3d cluster create swarm`, then `cd operator && make install && make run`

## Customer Chat UI

The `web/customer-chat/` directory contains a customer-facing web chat:

- Connects to OpenClaw gateway via WebSocket with Ed25519 device identity (required since protocol v3)
- Device keypair stored in IndexedDB, challenge-response signed with nonce
- Client ID: `"webchat"`, mode: `"webchat"` (other IDs fail or require device pairing approval)
- Dark mode UI with markdown rendering (via `marked`)
- URL pattern: `/chat/<slug>?token=<gateway-token>&host=ws://<host>:<port>`

```bash
# Dev
cd web/customer-chat && npm install && npm run dev
# Opens at http://localhost:3000
```

## OpenClaw Config Structure

Config lives at `/home/node/.openclaw/openclaw.json` inside the container. Key sections:

```json5
{
  "agents": {
    "defaults": { "model": { "primary": "openrouter/stepfun/step-3.5-flash:free" } },
    "list": [{ "id": "...", "default": true, "workspace": "...", "identity": { "name": "..." } }]
  },
  "channels": { "telegram": { "enabled": true, "dmPolicy": "open" } },
  "gateway": { "port": 18789, "bind": "lan", "auth": { "mode": "token", "token": "..." } },
  "session": { "scope": "per-sender", "reset": { "mode": "daily" } },
  "tools": { "profile": "coding" }  // Central: "coding", Customer: "messaging"
}
```

## Workspace Files

Located at `/home/node/.openclaw/workspace/` — loaded at session start:

| File | Purpose | Source |
|------|---------|--------|
| SOUL.md | Persona, tone, boundaries, project context | Operator template, overridden per-customer |
| AGENTS.md | Operating instructions, session startup, memory protocol | Operator template |
| TOOLS.md | Local environment notes (paths, URLs, credentials) | Operator template, overridden per-customer |
| HEARTBEAT.md | Periodic scheduled task checklist | Operator template, overridden per-customer |
| USER.md | Customer contacts, roles, preferences | Per-customer (from onboard.sh) |
| IDENTITY.md | Agent name, emoji, vibe | Auto-created by OpenClaw |
| MEMORY.md | Persistent long-term memory | Auto-managed by agent |
| memory/ | Daily session logs | Auto-managed by agent |
| skills/mc/SKILL.md | MissionControl CLI skill | Operator template |

## Docker Compose Commands (dev)

```bash
cd docker && docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d
docker compose down
```

## OpenClaw Docs

When troubleshooting OpenClaw issues (gateway, config, channels, pairing, CLI commands, etc.), check the official docs: https://docs.openclaw.ai

## Conventions

- Agent image: `ghcr.io/openclaw/openclaw:latest` — shared by all instances
- Agent identity via workspace files (SOUL.md etc.) copied by init container into PVC
- Customer templates use `{{CUSTOMER_NAME}}`, `{{CUSTOMER_SLUG}}`, `{{PROJECT_NAME}}`
- Gateway auth: always token-based (gateway refuses `auth: "none"` with `bind: "lan"`)
- OpenClaw needs ~1Gi+ RAM and `NODE_OPTIONS=--max-old-space-size=1536`
- OpenClaw startup takes 60s+ — liveness probe initialDelaySeconds must be >= 60
- Tools profile: `"coding"` for all agents (central + customer). Allows shell access for CLI tools like `mc`. Do not use `"messaging"` (blocks shell) or `"minimal"` (breaks file reading)

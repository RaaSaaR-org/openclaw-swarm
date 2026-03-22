# Swarm

Multi-instance deployment platform for [OpenClaw](https://docs.openclaw.ai) AI agents.

Swarm provisions, deploys, and manages isolated OpenClaw agent instances — a central operations agent and per-customer AI assistants, each with strict data isolation.

## Architecture

```
                    ┌─────────────────────────────┐
                    │         Internal             │
                    │                              │
                    │  ┌─────────┐                 │
                    │  │ Central │                 │
                    │  │ Agent   │                 │
                    │  └────┬────┘                 │
                    │       │                      │
                    │       │ orchestration         │
                    └───────┼──────────────────────┘
                            │
              ┌─────────────┼─────────────┐
              │             │             │
    ┌─────────▼───┐  ┌─────▼─────┐  ┌────▼──────┐
    │ Customer A  │  │Customer B │  │Customer C │
    │ ┌─────────┐ │  │ ┌───────┐│  │ ┌───────┐ │
    │ │Customer │ │  │ │Custmr ││  │ │Custmr │ │
    │ │ Agent   │ │  │ │ Agent ││  │ │ Agent │ │
    │ │(isolat.)│ │  │ │(isol.)││  │ │(isol.)│ │
    │ └─────────┘ │  │ └───────┘│  │ └───────┘ │
    └─────────────┘  └──────────┘  └───────────┘
```

Each customer gets:
- **Isolated OpenClaw instance** in its own container/pod
- **Own SOUL.md** defining the agent's persona and scope
- **Network isolation** — customer containers cannot reach each other
- **LLM via OpenRouter** — configurable model per instance

## Components

| Component | Description |
|---|---|
| **Central Agent** | Internal operations — PM, customer management, orchestration |
| **Customer Agent** | Per-customer AI assistant (isolated, scoped to one project) |
| **Swarm Operator** | Kubernetes operator — reconciles `KaiInstance` CRDs into workloads |
| **Customer Chat UI** | Web-based chat frontend (Vite + TypeScript, WebSocket + Ed25519 auth) |
| **swarm-ctl** | CLI for provisioning/managing customer agent instances |

## Quickstart

```bash
# 1. Configure
cp docker/.env.example docker/.env
# Edit .env: add OPENROUTER_API_KEY (get free key at openrouter.ai)

# 2. Build and start
cd docker
docker compose build
docker compose up -d

# 3. Verify
curl http://localhost:18789/healthz
# {"ok":true,"status":"live"}

# 4. Open control UI
# http://localhost:18789 (token: see openclaw.json gateway.auth.token)
```

## Directory Layout

```
swarm/
├── docker/                    # Docker Compose deployment (dev)
│   ├── Dockerfile.agent       # Based on ghcr.io/openclaw/openclaw:latest
│   ├── docker-compose.yml     # Central + customer services
│   └── .env.example           # Template for secrets
├── agents/
│   ├── central/               # Central agent: openclaw.json, SOUL.md, HEARTBEAT.md, ...
│   └── customer-template/     # {{PLACEHOLDER}} templates for new customers
├── demo/                      # Demo customer instances (customer-a, customer-b)
├── web/
│   └── customer-chat/         # Customer-facing web chat UI
├── operator/                  # K8s Operator (Go/Kubebuilder) for KaiInstance CRD
├── scripts/                   # Provisioning, teardown, backup, health-check
├── kubernetes/                # K8s manifests (base platform)
├── terraform/                 # Cloud provisioning (Hetzner)
└── docs/                      # Deployment guide, onboarding
```

## Deployment Targets

| Target | Use Case | Instances | Guide |
|--------|----------|-----------|-------|
| Docker Compose | Dev & demo | 1-5 | [Quickstart above](#quickstart) |
| Raspberry Pi 5 | Always-on, low-cost | 1-3 | [docs/deployment-guide.md](docs/deployment-guide.md) |
| Hetzner VPS | Production, EU-hosted | 3-10 | [terraform/](terraform/) |
| Kubernetes + Operator | Scale | 10+ | [docs/deployment-guide.md](docs/deployment-guide.md#option-4) |

## Prerequisites

- Docker & Docker Compose v2
- OpenRouter API key (free tier available at [openrouter.ai](https://openrouter.ai))
- Optional: Telegram bot token (via [@BotFather](https://t.me/botfather))

## License

MIT

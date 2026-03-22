# Deployment Guide

## Configuration: Public vs Private

Swarm separates **platform code** (this repo, public) from **business configuration** (private config repo).

| Repo | Contains | Visibility |
|------|----------|------------|
| `swarm/` | Platform code, operator, templates, chat UI, K8s manifests | Public |
| `swarm-config/` | Agent identities (SOUL.md), customer data, secrets, deploy script | Private |

### Private config repo structure

```
swarm-config/
├── agents/central/          # Central agent identity (SOUL.md, IDENTITY.md, etc.)
├── customers/<slug>/        # Per-customer SOUL.md and config overrides
├── secrets/.env             # API keys, tokens (gitignored)
└── deploy.sh                # Deploys config to K8s cluster
```

The `deploy.sh` script creates ConfigMaps from the private config, falling back to the generic templates in the public `swarm/` repo for any missing files.

### Setup

```bash
# Clone both repos side-by-side
git clone <public-swarm-repo> swarm
git clone <private-config-repo> swarm-config

# Configure secrets
cp swarm-config/secrets/.env.example swarm-config/secrets/.env
# Edit .env with real API keys

# Deploy
cd swarm-config && ./deploy.sh
```

---

## Option 1: Docker Compose (Dev & Demo)

Best for: Local testing, demos, 1-5 customer instances.

### Prerequisites
- Docker & Docker Compose v2
- OpenRouter API key

### Setup

```bash
cd swarm

# Configure secrets
cp docker/.env.example docker/.env
# Edit docker/.env — add OPENROUTER_API_KEY, optionally TELEGRAM_BOT_TOKEN

# Build (single image shared by all agents)
cd docker
docker compose build

# Start all agents
docker compose up -d

# Verify
curl http://localhost:18789/healthz
docker ps
```

### Control UI

Access at `http://localhost:18789` — requires the gateway auth token from `agents/central/openclaw.json`.

### Dev overrides

For debugging with exposed customer ports:
```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d
```

---

## Option 2: Raspberry Pi 5

Best for: Always-on, low-cost, single-location deployment.

### Prerequisites
- Raspberry Pi 5 (8GB RAM recommended, 4GB minimum)
- Raspberry Pi OS (64-bit)
- Docker (`curl -fsSL https://get.docker.com | sh`)

### Setup

```bash
git clone <your-repo-url>
cd swarm

cp docker/.env.example docker/.env
nano docker/.env  # Add OpenRouter key, Telegram token

cd docker
docker compose build
docker compose up -d
```

### Auto-start on boot

```bash
sudo tee /etc/systemd/system/swarm.service <<EOF
[Unit]
Description=Swarm Agents
After=docker.service
Requires=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/home/pi/swarm/docker
ExecStart=/usr/bin/docker compose up -d
ExecStop=/usr/bin/docker compose down

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl enable swarm
sudo systemctl start swarm
```

### Pi notes
- The OpenClaw image is ~1.9GB — allow time for first pull
- 4GB Pi: run 1-2 agents. 8GB Pi: run 3-5 agents.
- Monitor: `docker stats`
- Add swap if needed: `sudo dphys-swapfile swapoff && sudo nano /etc/dphys-swapfile` (CONF_SWAPSIZE=2048)

---

## Option 3: Hetzner VPS (Terraform)

Best for: Production, multi-customer, EU-hosted.

### Prerequisites
- Terraform 1.5+
- Hetzner Cloud account + API token
- SSH key pair

### Setup

```bash
cd terraform

terraform init

# Configure secrets
export TF_VAR_hcloud_token="your-hetzner-token"
export TF_VAR_openrouter_api_key="your-openrouter-key"
export TF_VAR_telegram_bot_token="your-bot-token"

# Plan and apply
terraform plan -var-file=environments/dev.tfvars
terraform apply -var-file=environments/dev.tfvars

# Connect
ssh root@$(terraform output -raw server_ip)
```

Cloud-init automatically installs Docker, clones the repo, and starts all services.

---

## Option 4: Kubernetes + Swarm Operator (Recommended for Production)

Best for: 10+ customer instances, auto-scaling, centrally managed provisioning.

### Prerequisites
- Kubernetes cluster (k3d for dev, k3s/managed K8s for prod)
- kubectl, Go 1.25+ (for building operator)

### Setup

```bash
# 1. Create k3d cluster (dev)
k3d cluster create emai-swarm --agents 1
k3d image import ghcr.io/openclaw/openclaw:latest -c emai-swarm
k3d image import busybox:latest -c emai-swarm

# 2. Create namespace and base resources
kubectl create namespace emai-swarm

# 3. Create secrets
kubectl create secret generic swarm-secrets \
  --namespace emai-swarm \
  --from-literal=openrouter-api-key="your-key" \
  --from-literal=telegram-bot-token="your-bot-token"

# 4. Create central agent identity ConfigMap
kubectl create configmap central-identity \
  --namespace emai-swarm \
  --from-file=SOUL.md=agents/central/SOUL.md \
  --from-file=HEARTBEAT.md=agents/central/HEARTBEAT.md \
  --from-file=TOOLS.md=agents/central/TOOLS.md \
  --from-file=IDENTITY.md=agents/central/IDENTITY.md \
  --from-file=AGENTS.md=agents/central/AGENTS.md \
  --from-file=openclaw.json=agents/central/openclaw.json

# 5. Deploy base manifests (central agent + RBAC)
kubectl apply -f kubernetes/central/

# 6. Install CRDs and run operator
cd operator
make install
make run  # Runs operator locally against cluster
```

### Adding a customer

```bash
# Via swarm-ctl
swarm-ctl provision --customer "Customer Name" --project "Project Name"

# Or via kubectl
kubectl apply -f - <<EOF
apiVersion: swarm.emai.io/v1alpha1
kind: KaiInstance
metadata:
  name: kai-customer-slug
  namespace: swarm
spec:
  customerName: "Customer Name"
  projectName: "Project Name"
  resources:
    requests: { memory: "1Gi", cpu: "100m" }
    limits: { memory: "2Gi", cpu: "500m" }
EOF
```

The operator automatically creates: Deployment, Service, ConfigMap, PVC, NetworkPolicy.

### Managing instances

```bash
swarm-ctl list                    # List all instances
swarm-ctl status <slug>           # Show instance details
swarm-ctl suspend <slug>          # Scale to 0 (keep data)
swarm-ctl resume <slug>           # Scale back to 1
swarm-ctl delete <slug>           # Delete + cascade cleanup
```

### Customer Chat UI

The customer-facing web chat is at `web/customer-chat/`:

```bash
# Dev server
cd web/customer-chat && npm install && npm run dev

# Access:
# http://localhost:3000/chat/<slug>?token=<gateway-token>&host=ws://localhost:<port>
```

For production, build and deploy as a Docker container:
```bash
cd web/customer-chat
docker build -t customer-chat .
kubectl apply -f kubernetes/customer-chat/
```

### Network isolation
The operator creates per-customer NetworkPolicies:
- Customer pods cannot reach each other
- Customer pods can only reach DNS + HTTPS (for LLM API)
- Only the central agent (role=central) can reach customer pods

### Important notes
- OpenClaw needs ~1Gi+ RAM and 60s+ startup time
- Gateway requires token auth when bound to LAN (no `auth: "none"`)
- Tools profile for customer agents: `"messaging"` (not `"minimal"`)

---

## Provisioning a new customer (Docker Compose — legacy)

```bash
./scripts/provision-customer.sh "Customer Name" "Project Name"

# Creates:
# - customers/<slug>/agent/  — SOUL.md, HEARTBEAT.md, openclaw.json
# - customers/<slug>/hq/     — mc-initialized HQ repo
# - docker/docker-compose.kai-<slug>.yml — Docker Compose override

# Then start:
cd docker
docker compose -f docker-compose.yml -f docker-compose.kai-<slug>.yml up -d kai-<slug>
```

## Health checks

```bash
# K8s
kubectl get kaiinstance -n swarm
kubectl get pods -n swarm

# HTTP
curl http://localhost:18789/healthz

# Docker Compose (legacy)
./scripts/health-check.sh
```

# Agent Registry

## Central Agent (this agent)

- **Role:** Central operations, PM, customer management, research coordination, swarm orchestration
- **Instance:** Kubernetes (central deployment in swarm namespace)
- **Workspace:** /home/node/.openclaw/workspace
- **Channels:** Telegram (optional), Web Control UI (port 18789)
- **Schedule:** Configurable via HEARTBEAT.md
- **Special tools:** `swarm-ctl` for provisioning/managing customer agent instances on Kubernetes

## Customer Agents

All customer-facing agents share a common name and persona template. Each customer interacts with their own isolated instance. This ensures:
- Customers always know they're talking to a project assistant
- Clear distinction from the central agent
- Consistent experience across all customers

Each customer instance is managed by the **Swarm Operator** (Kubernetes) and has:
- Own Deployment, Service, PVC, ConfigMap, and NetworkPolicy
- Own SOUL.md scoped to that customer's project data
- No access to other customers or internal data
- Reachable via Web Chat UI, Telegram (optional), or Control UI

### Provisioning

The central agent can create new customer instances using `swarm-ctl`:
```bash
swarm-ctl provision --customer "Customer Name" --project "Project Name"
swarm-ctl list
swarm-ctl suspend <slug>
swarm-ctl delete <slug>
```

### Customer Channels

Each customer instance supports:
- **Web Chat** — Customer-facing chat UI at `/chat/<slug>?token=<token>&host=ws://<host>:<port>`
- **Telegram** — Via dedicated bot per customer (optional)
- **Control UI** — Admin access at the gateway port

### Routing

Messages to the central agent's Telegram bot go to the central agent only. Customer instances are not reachable via the central Telegram — each customer accesses their agent via the Web Chat UI or their own dedicated Telegram bot.

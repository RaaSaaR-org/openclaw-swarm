# Customer Onboarding Checklist

## Before Onboarding

- [ ] Customer contact person identified (name, email)
- [ ] Project name and scope defined
- [ ] Preferred communication channel determined (Web Chat, Telegram, or both)

## Provisioning (Kubernetes — via Swarm Operator)

- [ ] Provision via `swarm-ctl` or ask the central agent:
  ```bash
  swarm-ctl provision --customer "<Customer Name>" --project "<Project Name>"
  ```
- [ ] Verify instance is running:
  ```bash
  swarm-ctl list
  # Should show Phase=Running, Ready=true
  ```
- [ ] Note the gateway token (auto-generated as `kai-<slug>-dev`)
- [ ] If Telegram needed: create dedicated bot via @BotFather, add botTokenSecretRef to KaiInstance spec

## Provisioning (Docker Compose — legacy)

- [ ] Run `./scripts/provision-customer.sh "<Customer Name>" "<Project Name>"`
- [ ] Review generated files:
  - `customers/<slug>/agent/SOUL.md` — adjust persona, scope, context
  - `customers/<slug>/agent/openclaw.json` — verify model and gateway settings
- [ ] Start customer container:
  ```bash
  cd docker
  docker compose -f docker-compose.yml -f docker-compose.kai-<slug>.yml up -d kai-<slug>
  ```

## Initial Setup

- [ ] Access the customer agent via one of:
  - **Customer Center:** `https://<host>/center/<slug>` — first visit triggers
    the bootstrap-admin flow: the email + password the first visitor submits
    becomes the admin account, written into the `kai-<slug>-users` Secret.
    Subsequent visits use the normal login form.
  - **Web Chat:** `https://<host>/chat/<slug>` — same sign-in. Reads from the
    same `kai-<slug>-users` Secret as Customer Center.
  - **Telegram:** Via dedicated bot (if configured).
- [ ] Send test message to verify agent responds correctly
- [ ] Verify isolation: ask about other customers — agent should refuse

## Handoff to Customer

- [ ] Share the Customer Center link: `https://<host>/center/<slug>` — first
      visit creates the customer admin account (bootstrap-admin flow above).
- [ ] Walk the customer through adding their team on the **Team** page; they
      share initial passwords with each user out-of-band.
- [ ] Optionally share Telegram bot link
- [ ] Brief walkthrough: what the agent can do (project tracking, tasks, meetings, status reports)
- [ ] Set expectations: agent scope, available tools, escalation path
- [ ] Confirm customer can interact successfully

## Ongoing

- [ ] Monitor instances: `swarm-ctl list`
- [ ] Monitor LLM usage via OpenRouter dashboard
- [ ] Periodic check-in with customer on agent usefulness
- [ ] Update SOUL.md as project evolves (update ConfigMap, operator reconciles)

## Suspend / Resume

```bash
swarm-ctl suspend <slug>   # Scale to 0, keep data
swarm-ctl resume <slug>    # Scale back to 1
```

## Offboarding

- [ ] Delete instance: `swarm-ctl delete <slug>`
- [ ] All child resources (Deployment, Service, PVC, ConfigMap, NetworkPolicy,
      `kai-<slug>-users` Secret, `kai-<slug>-chat-bridge` Secret) are
      automatically cleaned up via Kubernetes ownerReferences
- [ ] For Docker Compose: `./scripts/teardown-customer.sh <slug>` (archives data before removal)
- [ ] Confirm customer data fully removed from running infrastructure

## Web App Reference

The platform exposes several web surfaces — pick the right one for the task:

| App | URL | Audience | Purpose |
|---|---|---|---|
| **Customer Center** | `https://<host>/center/<slug>` | Tenant admin | Sign in, manage Team, see briefings |
| **Customer Chat** | `https://<host>/chat/<slug>` | Tenant user | Chat with the Kai agent |
| **Admin Console** | `https://<host>/admin/` | Platform operator | List / suspend / resume KaiInstances (ADMIN_TOKEN auth today) |
| **Status Page** | `https://<host>/status/<slug>` | Anyone with the token | Public per-tenant status (Bearer or `?token=` query) |
| **Onboarding API** | `POST https://<host>/api/instances` | Platform operator | Provision a new KaiInstance (ADMIN_TOKEN auth today) |

Onboarding-API + Admin-Console currently run on a single shared `ADMIN_TOKEN`;
self-serve signup with per-user accounts is tracked under the SaaS work in
`.mc/`.

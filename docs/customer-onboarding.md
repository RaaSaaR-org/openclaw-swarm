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
  - **Customer Center:** `https://<host>/center/<slug>` — sign in with email + password (first visit creates the admin account).
  - **Web Chat:** `https://<host>/chat/<slug>` — same sign-in. Backed by the per-customer `kai-<slug>-users` Secret.
  - **Telegram:** Via dedicated bot (if configured)
- [ ] Send test message to verify agent responds correctly
- [ ] Verify isolation: ask about other customers — agent should refuse

## Handoff to Customer

- [ ] Share the Customer Center link: `https://<host>/center/<slug>` — first visit creates the customer admin account.
- [ ] Walk the customer through adding their team in the Team Access panel; they share initial passwords with each user out-of-band.
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
- [ ] All child resources (Deployment, Service, PVC, ConfigMap, NetworkPolicy) are automatically cleaned up via Kubernetes ownerReferences
- [ ] For Docker Compose: `./scripts/teardown-customer.sh <slug>` (archives data before removal)
- [ ] Confirm customer data fully removed from running infrastructure

---
id: TASK-010
aliases:
- TASK-010
title: 'swarm-ctl: emit ingress URL + gateway token after provisioning'
slug: swarm-ctl-emit-ingress-url-gateway-token-after-provisioning
status: done
priority: 3
owner: ''
projects: []
customers: []
tags:
- scripts
- operator
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---




# swarm-ctl: emit ingress URL + gateway token after provisioning

## Why
After `swarm-ctl provision <customer>` succeeds, the operator allocates an ingress and stores `status.externalURL`, but the script just exits silently. The user (or the central agent that calls it) then has to `kubectl get kaiinstance <name> -o yaml` and grep around for the URL and token to actually share with the customer. This is the most-used provisioning command — making it useful saves time on every onboarding.

## What
- After the wait-for-Running succeeds, fetch and print: `status.externalURL` (or `status.gatewayURL` for in-cluster), `spec.gatewayAuth.token`, the chat URL pattern (`<externalURL>/chat/<slug>?token=<gatewayToken>`), and any Telegram bot username if telegram is configured.
- Also support `swarm-ctl info <customer>` as a standalone subcommand for fetching the same info post-hoc.
- Increase the wait timeout (currently 120s, line 71–73) — slow ARM nodes regularly miss it; recent commit `139fad2 fix(operator): raise kai probe timeouts for slow-CPU prep stages` raised probe timeouts but not this script timeout.

## References
- `/Users/heussers/develop/emai/swarm/scripts/swarm-ctl.sh` (lines 38–118)
- `/Users/heussers/develop/emai/swarm/operator/api/v1alpha1/kaiinstance_types.go` (status fields: `gatewayURL`, `externalURL`)
- Recent commit: `139fad2 fix(operator): raise kai probe timeouts for slow-CPU prep stages`
- Customer chat URL format: `web/customer-chat/README.md` documents `/chat/<slug>?token=...&host=...`

## Acceptance Criteria
- [ ] After successful `swarm-ctl provision`, the user sees ready-to-share URL + token
- [ ] `swarm-ctl info <customer>` prints same info on demand
- [ ] Wait timeout raised to 300s (or made configurable via env var)
- [ ] Output is parseable (e.g. `--output json` flag) for the central agent to consume

## Notes
Central agent currently calls `swarm-ctl` via a wrapper at `/shared-bin/swarm-ctl` inside the pod. JSON output makes that integration much cleaner than scraping text.

---
id: TASK-011
aliases:
- TASK-011
title: K8s-aware replacement for health-check.sh (currently Docker-only)
slug: k8s-aware-replacement-for-health-check-sh-currently-docker-only
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




# K8s-aware replacement for health-check.sh (currently Docker-only)

## Why
`scripts/health-check.sh` only checks Docker containers (hardcoded list of `swarm-central`, `swarm-demo-a`, `swarm-demo-b` plus dynamic discovery of `swarm-kai-<slug>` containers in `$CUSTOMERS_DIR/*/`). Production now runs on Kubernetes — this script is dead code on the K8s deployment, which means there's currently **no operator-facing single command** to see which customer instances are unhealthy. Status-page covers the customer-facing view per slug, but not the platform operator's "everything OK?" question.

## What
- New `scripts/health-check-k8s.sh` (or rewrite the existing one to detect docker-vs-k8s).
- Iterate `kubectl get kaiinstance -A -o json`, derive health from `.status.phase`, `.status.ready`, plus `kubectl get pods -l emai.io/customer=<slug>` for pod-level state and recent restart counts.
- Optional: hit each gateway's `/healthz` via `kubectl port-forward` or via service DNS from inside the cluster.
- Output: human-readable summary + machine-readable JSON (for piping into Prometheus/Grafana or alerting).
- Document the difference between the operator's view (CRD status) and the gateway's view (`/healthz` HTTP) — both can lie independently.

## References
- `/Users/heussers/develop/emai/swarm/scripts/health-check.sh` (Docker-only, ~73 lines)
- `/Users/heussers/develop/emai/swarm/operator/api/v1alpha1/kaiinstance_types.go` (status fields)
- `/Users/heussers/develop/emai/swarm/web/status-page/server/main.go` (per-slug status mapping logic — reuse the phase→status mapping)
- OpenClaw gateway healthz: documented at https://docs.openclaw.ai
- Always pass `--context` (multiple clusters in use locally)

## Acceptance Criteria
- [ ] One command shows status of every KaiInstance across the cluster (or filtered by namespace)
- [ ] Returns non-zero exit code if anything is unhealthy (CI/cron-friendly)
- [ ] JSON output mode for automation
- [ ] Honors `--context` arg

## Notes
Long-term, the right answer is Prometheus + alerting, but a shell script is the bridge between "we have monitoring" and "we have nothing".

---
id: TASK-026
aliases:
- TASK-026
title: 'Agent editor Phase A: read-only viewer + spike OpenClaw reload'
slug: agent-editor-phase-a-read-only-viewer-spike-openclaw-reload
status: done
priority: 4
owner: ''
projects: []
customers: []
tags:
- editor
- workspace
- openclaw
- product
sprint: ''
depends_on:
- TASK-025
due_date: ''
created: 2026-05-03
updated: 2026-05-06
---



# Agent editor Phase A: read-only viewer + spike OpenClaw reload

## Why
The marketing site (`swarm-cloud/web/marketing/src/pages/build.astro`) promises *"Build your own agents"* and *"compose them into teams"*. Today there is no UI surface anywhere where a user can see, edit, or create agents — `swarm/web/customer-center/` shows briefings, team access, and a list of channels, nothing about the agents themselves. The catalog (TASK-018) lets a user pick an agent at signup; after that there is no surface for the agent at all.

Phase A is the smallest move that makes the marketing claim partially honest: the user can at least *see* the agents in their workspace, their persona, the model, and the enabled skills. It also forces us to answer the hard infrastructure question — how does an edit propagate to a running OpenClaw — before we ship a write surface and discover the answer the painful way.

## What

### 1. Spike (must land first inside this task)
- **Does OpenClaw support config reload without a restart?** Audit OpenClaw's docs and source for SIGHUP handling, file watchers, an admin API, or any documented "reload agents" path. Document the result in this task. If the answer is "no", file an upstream issue / PR proposal in OpenClaw before Phase B is sized.
- **Where will edited config live?** Decide between:
  - **ConfigMap per workspace** — kubelet remount within ~60 s; capped at 1 MiB. Default lean.
  - **PVC mounted into the pod** — unbounded; needs an operator job (or sidecar) to write into it.
  - **New field on `KaiInstance` CRD** — clean k8s API; awkward for frequent edits.
  
  Confirm or override the lean after the spike and document the decision here.
- **What does OpenClaw actually expose about its loaded agents at runtime?** API endpoint? Filesystem path we can read? This decides whether Phase A reads from OpenClaw directly or from the source-of-truth ConfigMap.

### 2. Phase A surface (read-only)
Inside `swarm/web/workspace/` (post-rename — TASK-025), add an "Agents" page to the sidebar:

- List the agents currently provisioned in this workspace (today: just the one configured at signup, plus whatever Phase D eventually adds).
- For each agent show: name, persona (rendered SOUL.md), recommended model, enabled skills, source (catalog slug if forked from the catalog).
- Read directly from the live OpenClaw container — preferred path is an OpenClaw API endpoint; fallback is reading the mounted ConfigMap from the workspace backend.
- Zero write surface in this phase. No "edit" button. No "new agent" button. The sidebar item carries a small "preview" badge so we are honest about what works.

### Spike result (2026-05-06)

**Reload story — yes, hot reload is supported.** OpenClaw's gateway watches the config file and supports four reload modes (`off`, `hot`, `restart`, `hybrid` — default). Successful reload swaps the active in-memory config snapshot atomically. Source: https://docs.openclaw.ai/gateway. **Workspace-file reload (SOUL.md, AGENTS.md) is undocumented** — flag this as a Phase B spike item before shipping editable persona; the gateway docs only mention `openclaw.json` watching. No upstream OpenClaw issue needed for the reload story itself.

**Persistence — ConfigMap per workspace (the doc lean stands).** Already how `kai-<slug>-identity` works today: operator renders SOUL.md / AGENTS.md / TOOLS.md / HEARTBEAT.md / openclaw.json / SKILL-mc.md into one ConfigMap, the Deployment mounts it at `/identity`, the init container copies it into the PVC, and OpenClaw reads from `~/.openclaw/`. Edits in Phase B+ become ConfigMap patches. PVC-direct rejected (needs a write-back path, no atomic swap). CRD-field rejected (1 MiB etcd cap, awkward for frequent edits, polluted CR diff).

**Runtime visibility — no agents-list API.** OpenClaw's WebSocket gateway exposes `sessions.*` and `chat.*` operator scopes (see `web/chat/server/bridge.go`), but no `agents.list` or equivalent introspection. Conclusion: Phase A reads from the source-of-truth ConfigMap (`kai-<slug>-identity`), not from OpenClaw directly. Eventually-consistent (~60 s kubelet remount) but the CR is the canonical truth anyway and the workspace backend already has RBAC for ConfigMaps in the namespace.

### 3. Acceptance criteria
- [ ] Spike result written into this task body (reload story, persistence decision, runtime visibility)
- [ ] `swarm/web/workspace/` has an "Agents" sidebar item visible to authenticated users
- [ ] The page lists the workspace's agent(s) with persona, model, skills, source
- [ ] Backend route serves the data without exposing other workspaces' configs (per-workspace isolation enforced)
- [ ] Smoke test: a freshly provisioned workspace shows the catalog agent it was provisioned with

## Out of scope (future phases — separate tasks each)
- **Phase B** — Edit persona text only, with restart banner
- **Phase C** — Skill toggles
- **Phase D** — Create new agent (fork from catalog or blank)
- **Phase E** — Hot-reload (removes the restart blip; likely needs upstream OpenClaw work)
- **Phase F+** — Team composition UI (multi-agent routing)

## Depends on
- **TASK-025** (workspace rename) — this task lands inside `swarm/web/workspace/`. Could ship before the rename if needed, but the path math gets messier and we would migrate URLs twice.

## References
- https://openclaw.ai — runtime we need to interrogate for reload semantics
- `/Users/heussers/develop/emai/swarm/agents/catalog/` — the persona format the page needs to render
- `/Users/heussers/develop/emai/swarm/web/customer-center/server/main.go` — pattern for adding a new backend route (post-rename: `swarm/web/workspace/server/main.go`)
- `/Users/heussers/develop/emai/swarm-cloud/web/marketing/src/pages/build.astro` — the marketing promises this is catching up to
- TASK-018 (catalog) — the source-of-truth agents the read-only viewer renders

---
id: TASK-006
aliases:
- TASK-006
title: Document the 5-app SaaS architecture (admin/center/onboarding/status/chat)
slug: document-the-5-app-saas-architecture-admin-center-onboarding-status-chat
status: backlog
priority: 2
owner: ''
projects: []
customers: []
tags:
- docs
- saas
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---


# Document the 5-app SaaS architecture (admin/center/onboarding/status/chat)

## Why
Today, the only architecture diagram is in `README.md` and shows just operator + customer-chat. The repo now contains 5 web apps with overlapping responsibilities, two distinct auth models (per-customer JWT vs. global `ADMIN_TOKEN`), and a non-obvious data flow (onboarding → operator → KaiInstance → chat-bridge Secret → customer-chat reads users). Anyone joining (or anyone deciding whether swarm is the right SaaS base) cannot navigate this without code-reading. Blocks both contributor onboarding and the SaaS-direction discussion.

## What
- New file: `docs/architecture.md` — per-app role, request flow diagrams (Mermaid), what each app reads/writes in K8s, which Secrets it touches.
- Concretely: a sequence diagram for "new customer signs up via onboarding → KaiInstance created → operator provisions → admin gets confirmation → first user logs into customer-center → customer-center creates user record in `kai-<slug>-users` → customer-chat reads it for chat login".
- A clear table of "what's tenant-scoped vs. what's platform-scoped" — admin-console and onboarding are platform-scoped (`ADMIN_TOKEN`); customer-center, customer-chat, status-page are tenant-scoped (per-slug routes).
- Update top-level `README.md` to point to the new architecture doc.
- Document the swarm ↔ swarm-config seam — what lives in each, what an operator-of-the-platform sees vs. what an end-customer sees.

## References
- `/Users/heussers/develop/emai/swarm/README.md` (current architecture section is operator + chat only)
- `/Users/heussers/develop/emai/swarm/CLAUDE.md` (closer to current truth, but for AI agents)
- `/Users/heussers/develop/emai/swarm/web/{admin-console,customer-center,customer-chat,onboarding,status-page}/server/main.go`
- `/Users/heussers/develop/emai/swarm/operator/internal/controller/kaiinstance_controller.go`
- `/Users/heussers/develop/emai/swarm/agents/central/`, `agents/customer-template/`
- Mermaid sequence diagrams: https://mermaid.js.org/syntax/sequenceDiagram.html
- OpenClaw docs (cross-link): https://docs.openclaw.ai

## Acceptance Criteria
- [ ] `docs/architecture.md` exists with: per-app responsibility table, K8s resource map, sequence diagram for new-customer flow
- [ ] Auth model split (platform-token vs. tenant-JWT) is explicit
- [ ] `README.md` links to it from the Architecture section
- [ ] Diagram renders on GitHub (Mermaid native)

## Notes
Don't duplicate per-app deployment instructions — those live in `docs/deployment-guide.md`. Architecture doc is "how the pieces fit", not "how to deploy".

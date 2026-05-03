---
id: TASK-018
aliases:
- TASK-018
title: App / persona catalog with chooser at signup
slug: app-persona-catalog-with-chooser-at-signup
status: in-progress
priority: 2
owner: ''
projects: []
customers: []
tags:
- catalog
- saas
- product
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---



# App / persona catalog with chooser at signup

## Why
Direct quote from the user's product framing: *"users can get some 'kai instances with apps and so on'"*. The current platform spawns a generic agent with the central-agent persona. A SaaS user signing up should pick from a gallery: "personal assistant", "study buddy", "coding helper", "writing coach", "language tutor", etc. — each is a curated SOUL.md + tool config + suggested model. Without this, the product feels like "spin up a blank chatbot" instead of "get a useful assistant". The catalog is also a marketing surface: each app is a landing-page bullet.

> **Naming convention:** "App" is the user-facing product term. In the codebase an app is just an **agent persona** — a SOUL.md plus a tiny `metadata.yaml` for the catalog UI. There is no separate "App" runtime entity; an app *is* an agent.

## What
- Each app lives at **`agents/catalog/<slug>/`**, alongside the existing `agents/central/` and `agents/customer-template/`. Same renderer, same template machinery, same operator path.
- Per-app file layout:
  ```
  agents/catalog/personal-assistant/
  ├── SOUL.md           # persona, tone, scope (with {{PLACEHOLDERS}} as today)
  ├── metadata.yaml     # name, category, description, iconURL, recommendedModel, toolsProfile, tags, tier
  └── icon.svg          # gallery thumbnail
  ```
- Author a starter set of **5–10 apps** under `agents/catalog/` (curated for v1).
- Catalog page in customer-center (and a teaser on the marketing site, TASK-022): browse, filter by category, preview the persona.
- Signup flow (TASK-013): user picks an app → KaiInstance is created with that app's SOUL.md and `toolsProfile`.
- Switch-app action in customer-center: warn about persona change, then re-render workspace files for the new app.
- Decide: catalog is curated (we author all of them) or community (user-submitted with review queue)? Curated for v1.

## References
- `/Users/heussers/develop/emai/swarm/agents/customer-template/` (existing SOUL.md template — base shape for first catalog app)
- `/Users/heussers/develop/emai/swarm/agents/central/SOUL.md` (the current "default" persona)
- `/Users/heussers/develop/emai/swarm/operator/internal/controller/templates_test.go` (template rendering — catalog apps reuse this verbatim)
- OpenClaw SOUL.md docs: https://docs.openclaw.ai
- TASK-012 (multi-app on KaiInstance vs. sibling KaiApp CRD — relevant if users want multiple personas in one workspace)
- TASK-013 (signup flow — chooser lives there)
- TASK-022 (marketing site reads the same `agents/catalog/` for the public gallery — single source of truth)

## Open Questions
- Curated catalog or community submissions? Curated for v1, community v2 with review queue.
- Can a user have N apps as N workspaces (one app per workspace, TASK-014) or N apps inside one workspace (one workspace, multi-persona OpenClaw)? OpenClaw supports multiple agents in `agents.list[]`; both shapes are technically possible.
- Do paid apps exist (e.g. specialized assistants behind a tier)?

## Status

**Phase 0 (catalog content) — done** on 2026-05-03. Six starter apps live under `agents/catalog/` with `SOUL.md.tmpl` + `metadata.yaml` + `icon.svg`, plus a `README.md` documenting the schema. Each app uses `{{WORKSPACE_NAME}}` / `{{USER_NAME}}` placeholders (no `CUSTOMER_*` per public-repo terminology rule). Apps: `personal-assistant`, `coding-helper`, `writing-coach`, `language-tutor`, `study-buddy`, `productivity-companion`.

**Phase 1 (operator catalog renderer) — done** on 2026-05-03. Operator now reads catalog SOUL.md.tmpl files at runtime from a configurable path (`KAI_CATALOG_DIR` env, default `/etc/swarm/catalog`). When a KaiInstance has `spec.appRef` set AND `<dir>/<appRef>/SOUL.md.tmpl` exists, that file becomes the per-tenant SOUL.md instead of the embedded customer-template. Catalog templates use the new placeholder set per `agents/catalog/README.md` schema (`{{WORKSPACE_NAME}}`, `{{USER_NAME}}` — auto-derived from email's local part, `{{APP_NAME}}`); the embedded customer-template keeps its legacy `{{CUSTOMER_*}}` placeholders. Falls back to embedded gracefully when `appRef` is empty (legacy tenant), the catalog dir is missing (no overlay mount yet), or the specific persona file is missing (catalog ConfigMap drifted behind a freshly-curated slug). Five new tests cover all four paths (catalog hit, catalog miss → fallback, no AppRef → fallback, USER_NAME email-localpart derivation). Deployment-overlay wiring documented in `operator/config/manager/manager.yaml` with the kubectl ConfigMap-create command.

**Phase 3 (signup `?app=<slug>` → KaiInstance with persona) — done** on 2026-05-03 via [[TASK-013]] Phase 1.B (signup carries `app` field, stored on User row, used at verify time to set `spec.appRef`). Combined with Phase 1 here: a user picking `writing-coach` at signup gets a workspace whose SOUL.md is the writing-coach persona, end-to-end.

**Remaining phases blocked on upstream tasks:**
- Phase 2 (catalog page in customer-center): blocked on [[TASK-014]] Phase 2 (customer-center swap from per-tenant Secret to Postgres lookup) — needs the dashboard rewrite to know the user's identity before it can render their catalog choice.
- Phase 4 (switch-app action — change `spec.appRef` on an existing workspace, confirm "persona will reset"): blocked on Phase 2 (needs dashboard).
- Phase 5 (one-screen preview of the persona — show sample SOUL excerpt + suggested first prompts): blocked on Phase 2 + a small metadata.yaml read on the dashboard side.

## Acceptance Criteria
- [x] At least 5 starter apps live under `agents/catalog/` with `SOUL.md` + `metadata.yaml` + `icon.svg` (6 shipped 2026-05-03)
- [ ] Catalog page renders all apps with category filter (Phase 2 — marketing site at swarm-cloud/web/marketing/agents.astro shows the catalog; the dashboard equivalent is Phase 2)
- [x] Signup with `?app=<slug>` produces a KaiInstance with that persona (Phase 3 — TASK-013 Phase 1.B + Phase 1 here)
- [ ] User can switch apps from customer-center, with confirmation prompt (Phase 4)
- [ ] Each app has a one-screen "preview" (sample SOUL.md, suggested first prompts) (Phase 5)
- [x] Phase 1: operator reads catalog SOUL.md.tmpl from KAI_CATALOG_DIR when spec.appRef is set; falls back to embedded customer-template otherwise (2026-05-03)

## Notes
The app catalog is the **product story** for SaaS — it's what the marketing landing page (TASK-022) is selling. Don't ship signup without at least 3 working apps in the catalog.

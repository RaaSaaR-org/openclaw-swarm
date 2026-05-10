---
id: TASK-018
aliases:
- TASK-018
title: App / persona catalog with chooser at signup
slug: app-persona-catalog-with-chooser-at-signup
status: done
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
updated: 2026-05-09
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

**Phase 2 (marketing-side catalog page with category filter) — done** on 2026-05-09 in `swarm-cloud/web/marketing/src/pages/agents.astro`. The agents page now renders a radio-button filter bar above the grid (`All` + one pill per category, derived from the catalog at build time), and CSS-only `:has()` rules in `global.css` hide cards whose `data-category` doesn't match the active radio. Verified end-to-end with Playwright against the dev server: clicking `development` leaves only `Coding Helper`; clicking `learning` leaves `Language Tutor` + `Study Buddy`; clicking `All` restores all 6 cards. Zero JS, zero hydration cost, accessible (radio group + visually-hidden legend, focus ring on the active label). The dashboard equivalent (Phase 2 in the workspace UI) is still pending TASK-014 Phase 2.

**Phase 5 (one-screen persona preview) — done** on 2026-05-09 in `swarm-cloud/web/marketing/`. New dynamic Astro route `pages/agents/[slug].astro` with `getStaticPaths` derived from the catalog — renders one preview page per app: app name + tagline + tier + recommendedModel + tools profile + tags + persona excerpt + suggested first prompts + a `Start with <App>` CTA that links to `/signup?app=<slug>`. The persona excerpt comes from a new `extractIdentityExcerpt()` in `src/data/catalog.ts` that pulls the first paragraph under `## Identity` (English) or `## Identitaet`/`## Identität` (German) from the catalog SOUL.md.tmpl, with `{{USER_NAME}}` / `{{WORKSPACE_NAME}}` / `{{APP_NAME}}` placeholder tokens substituted to a language-appropriate literal (English: `the user`, German: `dem Nutzer`/`vom Nutzer` after contracting `von dem` → `vom`). The `/agents` cards now wrap as `<a class="card card-link">` linking to the per-app preview, with a `→` arrow that activates on hover. All 6 routes (`coding-helper`, `language-tutor`, `personal-assistant`, `productivity-companion`, `study-buddy`, `writing-coach`) return HTTP 200, zero console errors, persona excerpt reads natural in both languages.

**Phase 4 (workspace switch-app from the dashboard) — done** on 2026-05-09 in `swarm/web/workspace/`. **Backend** (`server/app.go` + `app_test.go`): two new endpoints — `GET /api/workspace/{slug}/catalog` lists the available apps from `KAI_CATALOG_DIR` (default `/etc/swarm/catalog`, matches the operator's catalog ConfigMap mount), and `PATCH /api/workspace/{slug}/app` validates the requested `appRef` against the catalog, confirms ownership via the `swarm.io/user-id` label vs `claims.Uid`, then merge-patches `spec.appRef` + the `swarm.io/app` label on the KaiInstance. Legacy internal-managed sessions (no `claims.Uid`) get HTTP 403 — switching personas on hand-onboarded EmAI tenants is an admin operation, not a user one. Eight new tests cover: catalog reading sorted-by-slug, fallback NameDe → Name, skip non-DNS-safe dirs, missing dir returns empty, list endpoint authed/unauthed, switch happy-path with patch verification, unknown-app rejection, cross-user 401 (workspace stays unpatched), legacy-session 403. Workspace go.mod promotes `sigs.k8s.io/yaml` to a direct dep. **Frontend** (`src/main.ts` + `src/style.css`): on the **current** workspace card in the "Your workspaces" view, a "Change persona" button expands an inline picker (no modal) with a `<select>` of catalog apps, a Save button, and a Cancel button. Save fires `window.confirm()` with the prompt-required-by-acceptance text ("Switching personas resets your agent's SOUL.md at next session..."), then PATCH on confirm. Toast-style feedback inline on the card after success/failure. Type-check + Vite build green; backend test suite green.

**Phase 4 follow-ups (not blocking the criterion):**
- Live Playwright verification against a running workspace pod — deferred because demoMode doesn't exercise the K8s patch path and a real pod requires the cluster the user is running. Acceptance is closed on backend tests + clean build; visual regression check belongs in the next deploy verification.

## Acceptance Criteria
- [x] At least 5 starter apps live under `agents/catalog/` with `SOUL.md` + `metadata.yaml` + `icon.svg` (6 shipped 2026-05-03)
- [x] Catalog page renders all apps with category filter (Phase 2 — marketing site at `swarm-cloud/web/marketing/src/pages/agents.astro` shipped 2026-05-09; CSS-only `:has()` filter, Playwright-verified. Dashboard equivalent tracked separately — TASK-014 Phase 2/3 unblocked it.)
- [x] Signup with `?app=<slug>` produces a KaiInstance with that persona (Phase 3 — TASK-013 Phase 1.B + Phase 1 here)
- [x] User can switch apps from customer-center, with confirmation prompt (Phase 4, 2026-05-09 — workspace dashboard exposes a "Change persona" picker on the current card; backend `PATCH /api/workspace/{slug}/app` validates ownership + catalog membership and merge-patches `spec.appRef` + `swarm.io/app` label; `window.confirm()` carries the "persona will reset" warning before the PATCH fires; 8 new backend tests cover happy-path + ownership + unknown-app + legacy-session paths)
- [x] Each app has a one-screen "preview" (sample SOUL.md, suggested first prompts) (Phase 5, 2026-05-09 — `swarm-cloud/web/marketing/src/pages/agents/[slug].astro` renders one page per app with the Identity excerpt from SOUL.md.tmpl + suggestedPrompts + recommendedModel + tier + tags + signup CTA)
- [x] Phase 1: operator reads catalog SOUL.md.tmpl from KAI_CATALOG_DIR when spec.appRef is set; falls back to embedded customer-template otherwise (2026-05-03)

## Notes
The app catalog is the **product story** for SaaS — it's what the marketing landing page (TASK-022) is selling. Don't ship signup without at least 3 working apps in the catalog.

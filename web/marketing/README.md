# swarm-marketing

Public-facing marketing + pricing site for the Kai SaaS. Astro 5, static
output, dark theme (`#141414` base, `#FF6700` accent — CLAUDE.md).

## Where this lives long-term

The decided home is **`swarm-cloud/web/marketing/`** (per [[TASK-022]] and
[[PROP-003]]). It lives in the public swarm repo today only because the
three-repo split ([[TASK-023]]) hasn't shipped yet — once it does, this
directory moves wholesale via `git mv` to the swarm-cloud overlay. Nothing
in this site references EmAI-specific tenants or deployment details, so the
move is mechanical (no code changes, just paths in CI).

## Pages

| Route | Purpose |
|---|---|
| `/` | Value prop, three-app teaser, how-it-works |
| `/apps` | Full app catalog grouped by category — reads `agents/catalog/` at build time |
| `/pricing` | Tier table (free / starter / growth / enterprise) — buttons currently link to `/signup?tier=…`; wires to Stripe checkout in TASK-016 Phase 1 |
| `/signup` | Currently a redirect button to the onboarding service. TASK-013 Phase 3 ships an inline form |
| `/privacy`, `/terms`, `/imprint` | Legal scaffolds. `/imprint` is the §&nbsp;5 DDG Impressum required for any German commercial offering |

## Catalog sourcing

The catalog comes from `agents/catalog/<slug>/metadata.yaml` at the repo
root — same source as customer-center. Acceptance criterion: "no
duplication". `src/data/catalog.ts` reads them at build time via Node `fs`,
so the marketing site and the dashboard always show the same list.

When the marketing site moves to `swarm-cloud`, that overlay vendors a
pinned `swarm` release tag and points `catalogRoot` at the vendored copy.
Path math is the only thing that changes.

## Run locally

```sh
npm install
npm run dev   # http://localhost:4321
npm run build # → dist/
```

## What's deferred to later phases

Per the task body, Phase 0 ships the skeleton. Open follow-ups:

- **Bilingual (DE + EN)** — Phase 1. Astro i18n routing + a translation
  layer reading `nameDe` / `shortDescriptionDe` from the same metadata.yaml
  files (the German strings already exist in the catalog).
- **Stripe checkout buttons** — Phase 2 (blocked on [[TASK-016]] Phase 1).
- **Inline signup form** — Phase 3 (blocked on [[TASK-013]] Phase 3).
- **OG tags + structured data + sitemap** — Phase 4 (production polish).
- **Lighthouse ≥ 95** — production tuning.
- **Hosting (Cloudflare Pages)** — deployment-overlay decision; the
  static `dist/` output deploys to anything.

## Conventions

- No emoji unless the user explicitly asks (per top-level CLAUDE.md).
- "Kognitive Roboter" not "humanoide Roboter" (CLAUDE.md) — though this is
  Kai marketing, not robot marketing, so probably moot.
- German is the primary language for production launch (CLAUDE.md). English
  ships first only because Phase 0 is single-language; the German strings
  are already in `agents/catalog/<slug>/metadata.yaml` (`nameDe` /
  `shortDescriptionDe`) waiting for the i18n routing.

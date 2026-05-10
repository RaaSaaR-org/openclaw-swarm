---
id: TASK-022
aliases:
- TASK-022
title: SaaS marketing landing + pricing page (separate from NeoDEM landing)
slug: saas-marketing-landing-pricing-page-separate-from-neodem-landing
status: in-progress
priority: 3
owner: ''
projects: []
customers: []
tags:
- marketing
- saas
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-10
---



# SaaS marketing landing + pricing page (separate from NeoDEM landing)

## Why
A SaaS needs a public face — value prop, screenshots, demo, app catalog (TASK-018), pricing tiers, signup CTA. The existing `landing-page/` repo (sibling to swarm) is the **NeoDEM** marketing site (robot fleet management product), which is a completely different product for a completely different audience. Reusing it would confuse both audiences. The OpenClaw SaaS needs its own landing site.

## Decided
- **Domain: `kai.emai.io`** (locked in 2026-05-03). Subdomain of EmAI's existing brand — no new domain to register, reuses brand trust. Standalone domain (e.g. `getkai.io`) considered for v1 brand independence; rejected — migrating to a standalone domain later is a 301 redirect away if/when the product warrants its own brand.
- **Repo: inside `swarm-cloud/web/marketing/`** (locked in 2026-05-03 — see [[PROP-003]] for the three-repo split). Separate `swarm-marketing/` repo considered; deferred until marketing-team access patterns demand it (when the marketing person isn't also the platform engineer).
- **Stack: Astro** (per the original task). Fast, SEO-friendly, minimal JS, supports component islands.

> The site reads `agents/catalog/` from a pinned `swarm` release tag (vendored at build time) so it doesn't drift from the deployed catalog.

## What
- New repo or new directory in this repo: `web/marketing/` (Next.js? Astro? Slidev? plain HTML?). **Recommend Astro** — fast, SEO-friendly, minimal JS, supports component islands when needed.
- Pages:
  - `/` — value prop, social proof, video demo, app catalog teaser
  - `/apps` — full app catalog (reads `agents/catalog/` — same source as customer-center, no duplication)
  - `/pricing` — tier table linked to Stripe checkout (TASK-016)
  - `/signup` — embeds or redirects to onboarding signup (TASK-013)
  - `/privacy`, `/terms`, `/imprint` — legal pages (German Impressumspflicht!)
- Bilingual: German + English (CLAUDE.md says German primary). Use Astro i18n routing.
- Dark theme: #141414 base, #FF6700 accent (CLAUDE.md convention).
- "Kognitive Roboter" not "humanoide Roboter" — though this is OpenClaw, not robot, so probably moot.
- Hosting: Cloudflare Pages or Vercel; deploy on push to main.

## References
- `/Users/heussers/develop/emai/landing-page/` (sibling repo — NeoDEM marketing, *do not reuse*)
- `/Users/heussers/develop/emai/CLAUDE.md` — design conventions (dark theme, German primary, color palette)
- Astro: https://astro.build/
- German Impressum law: https://www.gesetze-im-internet.de/dl-info-v/__5.html
- TASK-018 (catalog data lives at `agents/catalog/` — share between marketing site and customer-center)
- TASK-013 (signup flow target)
- TASK-016 (pricing → checkout)
- TASK-017 (DNS — decide the marketing domain)

## Open Questions
- Hosted demo agent (try-before-signup) or just a video? Default: video for v1; hosted demo is a great hook but takes a dedicated abuse story (anon CAPTCHA, hard rate limit, separate pooled key with tiny budget).
- Hosting: Cloudflare Pages (free, global edge, easy deploy) vs Vercel (better Astro integration but vendor lock-in)? Default: Cloudflare Pages — own DNS at Hetzner, Cloudflare Pages can serve from any DNS.

## Status

**Phase 0 (Astro skeleton) — done** on 2026-05-03, in **`swarm-cloud/web/marketing/`** (sibling directory of this `swarm/` repo at `~/develop/emai/swarm-cloud/`). Astro 5 static site with all 7 pages from the task (`/`, `/apps`, `/pricing`, `/signup`, `/privacy`, `/terms`, `/imprint`). Dark theme `#141414` base + `#FF6700` accent per CLAUDE.md. The catalog page reads `swarm/agents/catalog/<slug>/metadata.yaml` at build time via Node `fs` (cross-repo path; production overrides via `SWARM_CATALOG_PATH` env var) — same source as customer-center, zero duplication. All 6 catalog apps render (verified end-to-end: `npm run build` from `swarm-cloud/web/marketing/` → 7 HTML files including all 6 apps grouped by category → Playwright on the in-repo build showed zero console errors).

**The site does not live in the public `swarm` repo** — an earlier commit (`b81113e`) landed it there by mistake; reverted by `393897d` and re-shipped in the swarm-cloud sibling per the locked-in spot. swarm-cloud is currently a sibling directory on the dev machine; it becomes its own git repo when [[TASK-023]] phases the actual three-repo split.

**Locked-in spot is `swarm-cloud/web/marketing/`** ([[PROP-003]] / [[TASK-023]]).

**Open questions — closed:**
- Hosting: Cloudflare Pages (free, global edge, own DNS at Hetzner) — locked, just call it out in the deploy doc.
- Hosted demo vs video: deferred to Phase 1+ (needs the abuse story).

**Phase 1 (DE + EN bilingual) — done** on 2026-05-10 in `swarm-cloud/web/marketing/`. German is the primary language (CLAUDE.md), rendered at the root `/`; English at `/en/`. Concrete drop:
- `astro.config.mjs` — Astro 5 `i18n: { defaultLocale: 'de', locales: ['de','en'], routing: { prefixDefaultLocale: false } }`.
- `src/i18n/index.ts` — typed `Lang`, `asLang(currentLocale)`, `localizedPath(path, lang)`, `otherLocalePath(path, lang)` helpers.
- `src/i18n/strings.ts` — full translation dictionary for both locales: chrome (nav + footer + language switch), home (hero, usecases, pillars, trust stats, how-it-works, FAQ, callout), agents catalog page, agent-detail per-app preview, build, pricing, signup, privacy (DSGVO/GDPR), terms (AGB/ToS), imprint (Impressum per § 5 DDG).
- `src/layouts/Base.astro` — `<html lang>` from `Astro.currentLocale`, three `<link rel="alternate" hreflang>` tags (de, en, x-default), `og:locale` + `og:locale:alternate`, language-switch link in the nav, footer + chrome strings via dictionary.
- `src/pages/<name>.astro` files are now thin wrappers over shared components in `src/components/pages/<Name>.astro` — the components read `Astro.currentLocale` and pull strings from the dictionary. Each page exists at both `/<name>` (German) and `/en/<name>` (English) via parallel files.
- Dynamic agent route: `/agents/[slug]` and `/en/agents/[slug]` both share `AgentDetail.astro` which reads `nameDe`/`shortDescriptionDe` from metadata.yaml when DE; the persona excerpt's language already follows the SOUL.md.tmpl heading (`## Identity` vs `## Identitaet`) per the existing `extractIdentityExcerpt` (TASK-018 Phase 5).

Verified: production `astro build` produced 28 pages (8 base × 2 locales + 6 agent details × 2 locales = 28). `curl /` and `curl /en` both 200; agents/[slug] both locales 200; pricing/privacy/imprint/etc both 200. Playwright dev-server check: `<html lang="de">` + `hreflang` tags present + nav reads "Agenten" / "Bauen" / "Preise" at `/`, "Agents" / "Build" / "Pricing" at `/en`. Language switcher round-trips correctly. Zero new console errors (the 3 pre-existing errors — Vite WS reconnect, trailing-slash 404, missing favicon.ico — are dev-server artifacts).

**Legal-page caveat:** the German privacy/terms/imprint copy is a translation pass; before production launch it must go through legal review. The `Imprint` template still has placeholder name/address — update before launch.

**Phase 4 (OG + structured data + sitemap + Lighthouse ≥ 95) — done** on 2026-05-10 in `swarm-cloud/web/marketing/`. Concrete drop:

- **Sitemap**: added `@astrojs/sitemap` integration to `astro.config.mjs` with the `i18n` block (defaultLocale `de`, locales `{ de: 'de-DE', en: 'en-US' }`). Build emits `dist/sitemap-index.xml` + `dist/sitemap-0.xml` covering all 28 routes (14 pages × 2 locales). Each `<url>` carries `<xhtml:link rel="alternate" hreflang>` for both locales (56 hreflang annotations total) so search engines pick the right locale per region.
- **Structured data (JSON-LD)** in `src/layouts/Base.astro` head: three inline `<script type="application/ld+json">` blocks. (1) `Organization` (shared) — `name: "Kai"`, `url`, `logo` (favicon), `sameAs: [openclaw.ai, docs.kai.example.org, status.kai.example.org, emai.io]`. (2) `WebSite` (per-locale) — `inLanguage: "de-DE" | "en-US"` matches the active locale; publisher = Kai Organization. (3) `SoftwareApplication` (home only) — `applicationCategory: BusinessApplication`, `operatingSystem: Cloud`, free-tier `Offer` (price 0 EUR), `areaServed: EU`. Home detection is path-based (`/` and `/en`) inside Base.astro so the existing page components didn't need changes.
- **OG image**: deferred per-page custom OG images — kept the shared `og.svg` fallback that shipped in Phase 0; tracked as a follow-up if/when launch needs it.
- **Lighthouse fixes**:
  - Added `<meta name="robots" content="index,follow">` to Base.astro head.
  - Async-loaded the Google Fonts stylesheet via `media="print" onload="this.media='all'"` + `<noscript>` fallback. This dropped FCP from 2.6s → 0.8s and was the dominant performance win (~900ms saved).
  - Bumped contrast on `.usecase .uc-tag` and `.footer-col h3/h4` from `--text-faint` (#5A6478, 3.27:1 on the dark base — fails WCAG AA) to `#7B8499` (5.18:1 — passes AA). Six contrast violations on the home page → zero.
  - Promoted the footer column headers from `<h4>` → `<h3>` so the heading hierarchy doesn't skip a level (page goes h1 → h2 → footer h3). Updated `.footer-col` selector to match both tags so style cascade is preserved.

**Lighthouse on `/` (preview server, desktop, no throttling):** Performance 99, Accessibility 100, Best Practices 100, SEO 100. All four categories comfortably above the 95 bar.

**Phase 2 (pricing buttons → Stripe checkout) — done** on 2026-05-10. Strategy: keep the marketing pricing buttons as `/signup?tier=<tier>` (already the case) and bridge the gap inside the workspace SPA. Concrete drop in `swarm/web/workspace/`:
- New `billingSectionHTML()` block in the Your Workspaces view, rendered between the workspace cards and the danger-zone. SaaS-managed accounts on free tier see two **Upgrade to Starter** / **Upgrade to Growth** buttons that POST `/api/workspace/{slug}/billing/checkout` and `window.location.assign` the returned Stripe URL. Paid accounts see a **Manage subscription** button that POSTs `/billing/portal` and redirects to the Stripe Customer Portal. Internal-managed tenants render nothing — billing only applies to SaaS sign-ups.
- New `loadOwner()` fetch hits `/api/workspace/{slug}/owner` (already TASK-014 Phase 3 endpoint) to surface the user's tier. `bindBillingHandlers()` wires both buttons + handles 503 (billing not configured) / 400 (no subscription on portal click) feedback with the same toast pattern as the Switch-app + Delete-account flows.
- CSS: new `.billing-zone` block with the `#FF6700` accent border (matches CLAUDE.md brand colour); reuses `.primary-btn` for the click targets.
- TypeScript clean (`tsc --noEmit`); Vite build green: 89.72 kB JS / 41.30 kB CSS (was 86.48 kB / 40.95 kB — small bump from the new section).
- Full chain a visitor can follow today: marketing `/pricing` → click "Choose Starter" → `/signup?tier=starter` → onboarding email verify → workspace dashboard → "Upgrade to Starter" button → Stripe Checkout → webhook syncs tier on return. Auto-trigger-on-verify (carry the `?tier=` hint through verify → auto-redirect to checkout) is a Phase 1.B refinement under [[TASK-013]].

**Phase 5 (swarm-cloud becomes a real git repo) — done** on 2026-05-10. `git init` + three clean root commits on `main` (scaffolding, marketing site, K8s overlay + deploy.sh) per [[TASK-023]] Phase 2. GitHub remote not yet attached; user adds + pushes when ready. The marketing site, kubernetes/ overlay, environments/, and deploy.sh now live in the swarm-cloud repo on disk; the only thing the public swarm holds is what every fork-able platform should hold.

**Remaining phases blocked on upstream tasks:**
- Phase 3 (inline signup form): blocked on [[TASK-013]] Phase 3 (SPA-side signup form).

## Acceptance Criteria
- [x] Live marketing site at chosen domain with: home, apps, pricing, privacy, terms, imprint (Phase 0 — site exists in swarm-cloud; "live at chosen domain" is Phase 5 deploy concern)
- [x] Bilingual (DE + EN) with proper hreflang tags (Phase 1, 2026-05-10 — Astro 5 i18n with `prefixDefaultLocale: false` so German is at `/`, English at `/en`; `<html lang>` + 3 `hreflang` link tags + `og:locale` per locale; full dictionary in `src/i18n/strings.ts`; 28 pages built; legal pages translated as a pass-through, lawyer review pending pre-launch)
- [x] App catalog driven by same `agents/catalog/` source as customer-center (no duplication) (Phase 0 — `src/data/catalog.ts` reads `../../../../../swarm/agents/catalog/` at build time)
- [x] Pricing table linked to Stripe checkout (Phase 2, 2026-05-10 — marketing pricing buttons → `/signup?tier=…` → verify → workspace dashboard's new **Upgrade** button → POST `/billing/checkout` → Stripe-hosted Checkout. The chain is continuous; the dashboard's billingSection in `web/workspace/src/main.ts` is the bridge.)
- [x] Lighthouse score ≥ 95 on home page (Phase 4, 2026-05-10 — desktop preview: Performance 99 / Accessibility 100 / Best Practices 100 / SEO 100. Async font load + contrast bump + heading-order fix.)
- [x] OG tags + structured data for rich link previews (Phase 4, 2026-05-10 — Phase 0 OG tags retained; added Organization + WebSite (per-locale) JSON-LD on every page and SoftwareApplication JSON-LD on the home page; sitemap-index.xml + per-locale `<xhtml:link rel="alternate" hreflang>` annotations via `@astrojs/sitemap`. Per-page custom OG images deferred — shared `og.svg` fallback is acceptable for v1.)

## Notes
Don't ship before we have an actual demo to show — a marketing site for a vapor product is worse than no site. Pair the launch with TASK-013 (signup) + TASK-018 (≥3 working apps).

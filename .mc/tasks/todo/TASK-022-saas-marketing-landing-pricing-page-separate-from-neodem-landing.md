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
updated: 2026-05-03
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

**Phase 0 (Astro skeleton at `web/marketing/`) — done** on 2026-05-03. Astro 5 static site with all 7 pages from the task (`/`, `/apps`, `/pricing`, `/signup`, `/privacy`, `/terms`, `/imprint`). Dark theme `#141414` base + `#FF6700` accent per CLAUDE.md. The catalog page reads `agents/catalog/<slug>/metadata.yaml` at build time via Node `fs` — same source as customer-center, zero duplication. All 6 catalog apps render (verified end-to-end: `npm run build` → 7 HTML files → Playwright snapshot of `/` and `/apps` shows zero console errors). CI matrix gets a new `marketing` job (Astro build + presence/grep smoke check on `dist/`).

**Locked-in spot is `swarm-cloud/web/marketing/`** ([[PROP-003]] / [[TASK-023]]). The site lives in the public swarm repo today only because the three-repo split hasn't shipped — the directory is mechanically `git mv`-able to swarm-cloud once the split lands. README walks through the move plan.

**Open questions — closed:**
- Hosting: Cloudflare Pages (free, global edge, own DNS at Hetzner) — locked, just call it out in the deploy doc when swarm-cloud lands.
- Hosted demo vs video: deferred to Phase 1+ (needs the abuse story).

**Remaining phases blocked on upstream tasks:**
- Phase 1 (DE + EN bilingual via Astro i18n routing + hreflang): the German strings already exist in `agents/catalog/<slug>/metadata.yaml` (`nameDe` / `shortDescriptionDe`); just needs the Astro i18n config + DE pages.
- Phase 2 (pricing buttons → Stripe checkout): blocked on [[TASK-016]] Phase 1 (Stripe webhook + price IDs).
- Phase 3 (inline signup form): blocked on [[TASK-013]] Phase 3 (SPA-side signup form).
- Phase 4 (OG tags + structured data + sitemap.xml + Lighthouse ≥ 95): production polish.
- Phase 5 (move to `swarm-cloud/web/marketing/`): triggered by [[TASK-023]] Phase 2 (the actual repo split).

## Acceptance Criteria
- [x] Live marketing site at chosen domain with: home, apps, pricing, privacy, terms, imprint (Phase 0 — site exists; "live at chosen domain" is Phase 5 deploy concern)
- [ ] Bilingual (DE + EN) with proper hreflang tags (Phase 1)
- [x] App catalog driven by same `agents/catalog/` source as customer-center (no duplication) (Phase 0 — `src/data/catalog.ts` reads `agents/catalog/<slug>/metadata.yaml` at build time)
- [ ] Pricing table linked to Stripe checkout (Phase 2 — buttons currently link to `/signup?tier=…`)
- [ ] Lighthouse score ≥ 95 on home page (Phase 4)
- [ ] OG tags + structured data for rich link previews (Phase 4 — basic OG tags shipped in Phase 0)

## Notes
Don't ship before we have an actual demo to show — a marketing site for a vapor product is worse than no site. Pair the launch with TASK-013 (signup) + TASK-018 (≥3 working apps).

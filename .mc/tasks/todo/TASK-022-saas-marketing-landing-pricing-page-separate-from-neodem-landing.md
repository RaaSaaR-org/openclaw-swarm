---
id: TASK-022
aliases:
- TASK-022
title: SaaS marketing landing + pricing page (separate from NeoDEM landing)
slug: saas-marketing-landing-pricing-page-separate-from-neodem-landing
status: backlog
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

## Acceptance Criteria
- [ ] Live marketing site at chosen domain with: home, apps, pricing, privacy, terms, imprint
- [ ] Bilingual (DE + EN) with proper hreflang tags
- [ ] App catalog driven by same `agents/catalog/` source as customer-center (no duplication)
- [ ] Pricing table linked to Stripe checkout
- [ ] Lighthouse score ≥ 95 on home page
- [ ] OG tags + structured data for rich link previews

## Notes
Don't ship before we have an actual demo to show — a marketing site for a vapor product is worse than no site. Pair the launch with TASK-013 (signup) + TASK-018 (≥3 working apps).

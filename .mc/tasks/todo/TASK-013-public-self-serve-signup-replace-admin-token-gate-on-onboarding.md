---
id: TASK-013
aliases:
- TASK-013
title: Public self-serve signup (replace ADMIN_TOKEN gate on onboarding)
slug: public-self-serve-signup-replace-admin-token-gate-on-onboarding
status: backlog
priority: 2
owner: ''
projects: []
customers: []
tags:
- signup
- saas
- web
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---


# Public self-serve signup (replace ADMIN_TOKEN gate on onboarding)

## Why
The vision is "anyone can visit a website and get their own private OpenClaw assistant." Today the `onboarding` web app's `POST /api/instances` requires `ADMIN_TOKEN` (Bearer header) — only a platform admin can provision. That's correct for the existing internal-tenant management flow (EmAI's hand-onboarded tenants in `swarm-emai`) but **directly blocks SaaS**. Without self-serve signup, there is no SaaS product, only a tool for a human operator to run.

> **Repo split (TASK-023):** signup *code* lives in `swarm/web/onboarding/`; CAPTCHA site key + signup-enabled feature flag + rate-limit policy + email-provider creds live in `swarm-cloud/`. The `ADMIN_TOKEN`-only path stays in the public `swarm` for self-hosted / internal-tenant use (`swarm-emai`). Signup creates instances labelled `swarm.io/managed: saas`.

## What
- Add a public signup endpoint (`POST /api/signup`) that accepts email + password (or magic link), runs CAPTCHA (hCaptcha or Cloudflare Turnstile), per-IP rate limit, and a disposable-email blocklist.
- Send a verification email (depends on TASK-020) — instance is not provisioned until email confirmed.
- After verification: provision the user's first KaiInstance via the existing operator path, attached to the new User record (depends on TASK-014).
- Keep the `ADMIN_TOKEN` path for internal/admin use; signup is a separate code path.
- Decision needed: do we always provision a free-tier instance immediately on verification, or do we let the user pick a persona first (TASK-018)?

## References
- `/Users/heussers/develop/emai/swarm/web/onboarding/server/main.go` (current admin-token gate, lines 65–68 and 112–121)
- `/Users/heussers/develop/emai/swarm/web/onboarding/server/main.go:123-201` (provisioning request handler — reuse most of this)
- hCaptcha: https://www.hcaptcha.com/  | Cloudflare Turnstile: https://www.cloudflare.com/products/turnstile/
- Disposable email lists: https://github.com/disposable-email-domains/disposable-email-domains
- OWASP signup security: https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html

## Open Questions
- Email or also OAuth (Google/GitHub) at signup? Cheaper UX but adds complexity.
- Magic link or password? Magic link removes the password-reset surface entirely.
- Do we provision the K8s pod *immediately* on verification (cost!) or lazily on first chat message?

## Acceptance Criteria
- [ ] Public `POST /api/signup` endpoint, no `ADMIN_TOKEN` required
- [ ] CAPTCHA on the signup form, validated server-side
- [ ] Email verification required before any provisioning happens
- [ ] Per-IP rate limit (≤5 signups/hour without auth)
- [ ] Disposable email domains rejected
- [ ] Existing `ADMIN_TOKEN` path still works for internal admin

## Notes
This is the **gateway task** — none of the other SaaS tasks (014–022) matter without a way for users to actually arrive. Likely a 2–3 week chunk on its own.

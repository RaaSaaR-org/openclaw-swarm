---
id: TASK-008
aliases:
- TASK-008
title: Publish OpenAPI specs for admin, center, onboarding and status APIs
slug: publish-openapi-specs-for-admin-center-onboarding-and-status-apis
status: done
priority: 3
owner: ''
projects: []
customers: []
tags:
- docs
- api
- saas
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---




# Publish OpenAPI specs for admin, center, onboarding and status APIs

## Why
The 4 web servers expose JSON HTTP APIs (`POST /api/instances` for onboarding, full user CRUD on customer-center, instance list/suspend/resume on admin-console, public status on status-page) but no machine-readable contract. This blocks: (1) CI contract testing, (2) external integrators (anyone who wants to provision via Terraform/CI rather than the web UI), (3) generated client SDKs, (4) generated docs. For a SaaS platform pitched as "self-serve", the absence of an API spec is a credibility issue.

## What
- Author OpenAPI 3.1 specs at `docs/api/{admin,center,onboarding,status}.yaml` — minimal hand-written, then keep updated alongside handler changes.
- Optional: generate from Go using `swaggo/swag` annotations on each handler; trade-off is build complexity vs. drift safety.
- Wire a GitHub Actions step that lints specs (`stoplightio/spectral`) on PRs.
- Publish rendered docs (Redoc or Stoplight Elements) — either GitHub Pages or a simple HTML page in `docs/api/`.

## References
- `/Users/heussers/develop/emai/swarm/web/admin-console/server/main.go` (routes)
- `/Users/heussers/develop/emai/swarm/web/customer-center/server/main.go` (routes — most complex, ~14 endpoints)
- `/Users/heussers/develop/emai/swarm/web/onboarding/server/main.go` (`POST /api/instances` provisioning)
- `/Users/heussers/develop/emai/swarm/web/status-page/server/main.go` (`GET /api/status/{slug}`)
- OpenAPI 3.1 spec: https://spec.openapis.org/oas/v3.1.0
- Spectral linter: https://github.com/stoplightio/spectral
- Redoc: https://redocly.com/redoc

## Acceptance Criteria
- [ ] One spec per server under `docs/api/`, all valid against OpenAPI 3.1
- [ ] CI lints specs (Spectral) on every PR
- [ ] Each spec covers every route + every error response (4xx / 5xx)
- [ ] Renderable HTML docs published (GH Pages or static page)

## Notes
Pairs naturally with TASK-006 (architecture doc) — spec answers "how do I call this?", architecture doc answers "what is this?".

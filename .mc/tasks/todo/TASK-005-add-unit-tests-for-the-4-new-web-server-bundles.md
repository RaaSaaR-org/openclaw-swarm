---
id: TASK-005
aliases:
- TASK-005
title: Add unit tests for the 4 new web server bundles
slug: add-unit-tests-for-the-4-new-web-server-bundles
status: backlog
priority: 2
owner: ''
projects: []
customers: []
tags:
- tests
- web
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---


# Add unit tests for the 4 new web server bundles

## Why
The 4 new Go server bundles (`swarm-admin-console`, `swarm-customer-center`, `swarm-onboarding`, `swarm-status-page`) currently have **zero** test files. They handle authentication, KaiInstance CRUD against the K8s API, and customer onboarding — exactly the surface area that breaks silently. They're also still untracked in git (`?? web/admin-console/server/swarm-admin-console`, etc.), so there's a real chance code lands without anyone noticing missing test coverage. A SaaS product cannot ship 4 auth-handling services with no tests.

## What
- For each of the 4 server bundles, add `*_test.go` files covering at minimum:
  - **admin-console:** `ADMIN_TOKEN` constant-time comparison, KaiInstance list filtering, suspend/resume PATCH path
  - **customer-center:** bootstrap-admin flow, login (good/bad credentials), user CRUD, JWT cookie issue/verify, profile loading
  - **onboarding:** token auth, slug validation, KaiInstance creation, gatewayToken generation, error paths (duplicate slug)
  - **status-page:** token auth (header vs query), phase → public-status mapping, slug not found
- Use `controller-runtime/pkg/client/fake` for K8s API mocking (already a dep via the operator).
- Wire all 4 into `.github/workflows/ci.yml` so PRs run them.

## References
- `/Users/heussers/develop/emai/swarm/web/admin-console/server/main.go` (lines 57–117 — auth)
- `/Users/heussers/develop/emai/swarm/web/customer-center/server/main.go`, `auth.go`, `users.go`
- `/Users/heussers/develop/emai/swarm/web/onboarding/server/main.go` (lines 65–201 — auth + provision)
- `/Users/heussers/develop/emai/swarm/web/status-page/server/main.go` (lines 97–137 — auth + status)
- Reference style: `/Users/heussers/develop/emai/swarm/operator/internal/controller/templates_test.go` (table-driven)
- `controller-runtime` fake client: https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client/fake

## Acceptance Criteria
- [ ] Each of the 4 servers has at least one `_test.go` covering happy path + 1 failure mode for every HTTP handler
- [ ] `go test ./...` from each server's directory passes
- [ ] CI runs the new tests on PRs (not just main)
- [ ] Coverage report shows >60% on each server's main.go

## Notes
Tests will be much easier after TASK-004 (shared auth module) — write the auth-related tests against the shared module instead of duplicating them per server.

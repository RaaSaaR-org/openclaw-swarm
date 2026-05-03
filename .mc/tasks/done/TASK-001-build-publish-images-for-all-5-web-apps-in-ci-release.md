---
id: TASK-001
aliases:
- TASK-001
title: Build & publish images for all 5 web apps in CI/release
slug: build-publish-images-for-all-5-web-apps-in-ci-release
status: done
priority: 2
owner: ''
projects: []
customers: []
tags:
- ci
- release
- web
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---




# Build & publish images for all 5 web apps in CI/release

## Why
The repo now ships **5 web apps** (`customer-chat`, `customer-center`, `admin-console`, `onboarding`, `status-page`) but CI and release only build **3 of them** (operator + customer-chat + customer-center). Tagging a release today silently ships an inconsistent platform — onboarding/admin/status images never get published, so production must build them by hand. The `quickstart.yaml` flow assumes all images live at `ghcr.io/RaaSaaR-org/openclaw-swarm/<name>:<tag>`, which is currently a lie.

## What
- Add `admin-console`, `onboarding`, `status-page` to `.github/workflows/ci.yml` (typecheck + go vet + go build for each, parallel matrix if cleaner).
- Add 3 matching `*-image` jobs to `.github/workflows/release.yml` (multi-arch amd64+arm64 like the existing operator/chat/center jobs).
- Cross-check `quickstart.yaml` and `kubernetes/` manifests still reference the right image names and tags after release.
- Consider refactoring the 5 image jobs into a single matrix job — currently each is ~30 lines of duplicated YAML.

## References
- `/Users/heussers/develop/emai/swarm/.github/workflows/release.yml` (3 `docker/build-push-action@v6` calls at lines 44, 82, 119)
- `/Users/heussers/develop/emai/swarm/.github/workflows/ci.yml` (Docker Build job runs only on push to main, missing 3 apps)
- `/Users/heussers/develop/emai/swarm/web/{admin-console,onboarding,status-page}/Dockerfile`
- `/Users/heussers/develop/emai/swarm/quickstart.yaml`
- Recent commit that introduced the new web apps: `a7dddb4 feat: email+password auth across web apps + multi-app platform`
- GitHub release flow: https://docs.github.com/en/actions/use-cases-and-examples/publishing-packages/publishing-docker-images

## Acceptance Criteria
- [ ] All 5 web app images appear under https://github.com/RaaSaaR-org/openclaw-swarm/pkgs/container after the next tag
- [ ] CI fails if any of the 5 web apps fail to build
- [ ] Multi-arch (amd64 + arm64) for every image — Hetzner CAX21 is ARM64
- [ ] `quickstart.yaml` references resolve against the published images on a clean cluster

## Notes
Reuse `IMG=...` make targets where possible to keep image naming consistent across operator and web apps.

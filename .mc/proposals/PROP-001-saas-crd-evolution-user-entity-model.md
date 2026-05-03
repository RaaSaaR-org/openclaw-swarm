---
id: PROP-001
aliases:
- PROP-001
title: SaaS CRD evolution + User entity model
status: accepted
type: architecture
author: ''
supersedes: ''
superseded_by: ''
tags:
- crd
- user
- saas
created: 2026-05-03
updated: 2026-05-03
---


# SaaS CRD evolution + User entity model

## Context

The current `KaiInstance` CRD (`swarm.emai.io/v1alpha1`) is a fine "one customer = one OpenClaw pod" primitive but lacks fields a SaaS product needs: no `tier`/`plan`, no per-tenant quotas, no app-catalog reference, no `observedGeneration` (added in TASK-003), no `costCenter`/`org` labels, no User concept above the instance. To open the platform up to public signup ([[TASK-013]]) with paying tiers ([[TASK-016]]), per-user metering ([[TASK-019]]), and a curated app catalog ([[TASK-018]]), the CRD has to grow. At the same time, "who *owns* a workspace" needs an answer above the K8s layer — that's the User entity question.

This proposal locks both decisions together because they're entangled: the User entity is what carries the tier; the tier is what gates the CRD's quota fields.

## Options Considered

### Where the User entity lives

**Option A — Postgres** (chosen). Standard relational DB. Schema includes `users(id, email, password_hash, tier, stripe_customer_id, language, created_at, deleted_at, …)`. Indexed on email for O(log n) lookup.

**Option B — `SwarmUser` CRD.** Define a User as a Kubernetes Custom Resource. Reuse the operator pattern; etcd as the store.

**Option C — K8s Secret per user.** Lightest, but useless for queries and bad for >100 users.

### CRD evolution shape

**Option A — extend `KaiInstance`** with `spec.tier`, `spec.org`, `spec.userRef`, `status.observedGeneration` (already there post TASK-003). Multi-app added as `spec.apps[]AppRef`.

**Option B — sibling `KaiApp` CRD** referencing `KaiInstance`. Cleaner separation when one tenant has multiple apps.

## Decision

1. **User entity → Postgres.** Reasons:
   - Scales: etcd ceiling is ~10k objects; Postgres handles millions of users with no architecture change.
   - Queryability: `SELECT * FROM users WHERE email = ?` is O(log n) with an index. Listing all SwarmUsers in K8s is O(n).
   - Co-locates naturally with billing ([[TASK-016]]), email-bounce records ([[TASK-020]]), audit log ([[TASK-021]]) — they all want a relational store.
   - CRDs are right for *infrastructure* (KaiInstance — what runs); they're the wrong tool for *application data* (users — who exists). Use the right tool.
   - Local-dev cost is low: `docker compose up postgres` is one line.

2. **Extend `KaiInstance`** in v1alpha2 with the new SaaS fields rather than spawning sibling CRDs. Reasons:
   - Discoverability: `kubectl get kaiinstance kai-acme -o yaml` shows everything about that workspace in one object.
   - Migration story: a single conversion webhook from v1alpha1 → v1alpha2 ([[TASK-024]] bundles the customer→tenant rename in the same migration).
   - Multi-app v1 stays out of scope; if multi-app demand materialises, add `spec.apps[]AppRef` later — additive change, no new CRD needed.

**Concrete v1alpha2 spec additions:**

```yaml
spec:
  tenantName: "Acme GmbH"           # was: customerName (TASK-024 rename)
  tenantSlug: "acme"                # was: customerSlug
  appRef: "personal-assistant"      # NEW — points at agents/catalog/<slug>
  tier: "free"                      # NEW — enum: free | starter | growth | enterprise
  userRef: "u_01HX3ZQ..."           # NEW — Postgres user.id; null for managed:internal
  org: "acme-gmbh"                  # NEW — optional cost-center / billing-group label
  managed: "saas"                   # NEW — enum: saas | internal (PROP-003 coexistence rule)
  # existing fields kept: projectName, model, telegram, gatewayAuth, resources,
  # suspended, externalAccess
status:
  observedGeneration: 1             # already added in TASK-003
  # existing fields kept
```

**Postgres user table (initial v1):**

```sql
CREATE TABLE users (
    id              TEXT PRIMARY KEY,         -- u_<ulid>
    email           TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,            -- argon2id PHC string from pkg/auth
    tier            TEXT NOT NULL DEFAULT 'free',
    stripe_customer_id TEXT,                  -- null until first checkout
    language        TEXT NOT NULL DEFAULT 'de',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    email_verified_at TIMESTAMPTZ,
    deleted_at      TIMESTAMPTZ,              -- soft-delete for GDPR grace window
    last_login_at   TIMESTAMPTZ
);
CREATE INDEX users_email_idx ON users (LOWER(email));
```

## Consequences

**Positive:**
- One coherent multi-tenant model; quotas, billing, deletion, audit all hang off the User row.
- Etcd stays small (just K8s primitives + KaiInstances). Postgres carries human data.
- v1alpha2 conversion webhook bundles the customer→tenant rename ([[TASK-024]]) — one CRD migration, not two.

**Negative:**
- New infra dependency: managed Postgres in `swarm-cloud/` (Hetzner-managed or Neon, ~€10/mo to start). Backups, monitoring, schema migrations become operational concerns.
- The Postgres becomes a single-point-of-failure for login. Mitigate with managed-Postgres SLAs + read-replica for v2.
- Existing internal customers in `swarm-config`/`swarm-emai` need to be associated with a synthetic system-user (or, per [[PROP-003]], stay `managed: internal` and skip the User table entirely).
- CRD migration is the most expensive change since v1alpha1 was created. Conversion webhook required for ≥1 minor version.

**Out of scope for v1 (deliberate):**
- Multi-app per workspace (one app per workspace; future via `spec.apps[]`)
- Shared workspaces (a team of users on one assistant)
- User-uploaded SOUL.md (catalog-only for v1)

## Linked tasks

- [[TASK-012]] — implements the v1alpha2 CRD spec changes (Phase 2 work; this proposal is Phase 1)
- [[TASK-014]] — implements the User entity (Postgres schema, login wiring, customer-center "your workspaces" view)
- [[TASK-015]] — depends on `spec.tier` from this proposal to enforce quotas
- [[TASK-016]] — depends on `users.stripe_customer_id` from this proposal
- [[TASK-018]] — depends on `spec.appRef` from this proposal
- [[TASK-024]] — bundles the customer→tenant rename into the same v1alpha1→v1alpha2 conversion
- [[PROP-003]] — defines the `managed: {saas|internal}` label that gates which instances get a User

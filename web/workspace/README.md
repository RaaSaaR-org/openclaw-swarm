# Workspace

The customer's home base — a multi-page dashboard that ties together every customer-facing surface the EmAI Swarm exposes for them: chat with Kai, status, MissionControl, robot fleet dashboards, and weekly/meeting briefings. Designed so the customer can bookmark one URL and find everything from there.

URL pattern:

```
https://<host>/workspace/<slug>
```

No URL token. Each user signs in with **email + password** — credentials are stored in the per-tenant `kai-<slug>-users` Secret (argon2id-hashed). The same `kai-session` cookie is honored by `chat` when both apps share an origin (single-sign-on).

## What it shows

The dashboard is split into pages, navigable from the sticky left sidebar:

- **Overview** — greeting + four stat cards (Status, Briefings count + unread, Project team size, Chat-access user count), the latest "What's new" briefing teaser, side-by-side "What Kai does for you" / "Recurring rhythms" cards distilled from `SOUL.md` and `HEARTBEAT.md`, and a "Quick actions" panel.
- **Briefings** — collapsible cards with mark-as-read state (per-browser `localStorage`), search when ≥ 5 briefings, "Mark all as read", deep-links via `…#/briefings/<id>`, and relative times. Markdown body rendered with `marked` (default escaping; no inline HTML honored).
- **Project team** — contact cards (name, role, company, timezone, email, phone). Distilled from `customers/<slug>/USER.md` into a JSON file the operator publishes to a ConfigMap.
- **Apps & channels** — built-in tiles for `Chat with Kai` and `Status`, plus any number of custom external tiles via the `swarm.emai.io/customer-links` annotation on the `KaiInstance`. Channels list includes web chat, plus Telegram if `spec.telegram` is set.
- **Team access** — admin panel that manages the `kai-<slug>-users` Secret. Add an email + initial password, generate a strong password, reset, or remove a user. Argon2id hashing happens server-side.

The sidebar shows the customer card (avatar/name/project/status pill), the unread-briefings badge, the signed-in user's email, and a logout button.

## Authentication

- **First visit on a fresh customer** — the `users.json` is empty, so the login form switches to "Set up your workspace". The first email + password to submit becomes the initial admin. After that, the bootstrap path is closed (the second submission goes through normal sign-in).
- **Subsequent visits** — `Sign in` form. Backend verifies against the argon2id hash and issues a 24h `kai-session` HttpOnly cookie (HS256-signed; secret in `kai-<slug>-chat-bridge`).
- **All authenticated users can do everything** in v1, including managing team access. Role layering (admin / member) is a future enhancement and trivial to add — there's a single `userRecord` struct.
- **Identical 401 for unknown email and wrong password** — no user enumeration leak.

## Layout

```
workspace/
├── server/         # Go binary (HTTP API + embedded SPA)
│   ├── main.go     # routes, getCenter, demo data
│   ├── auth.go     # argon2id, JWT, cookies, login/logout/me/auth
│   ├── users.go    # team-access admin endpoints
│   ├── go.mod / go.sum
│   └── web/        # populated at build time from ../dist
├── src/            # Vite/TS frontend
│   ├── main.ts     # router, sidebar, pages, login screen
│   └── style.css
├── index.html
├── package.json
├── vite.config.ts
└── Dockerfile      # multi-stage: node build → go build → distroless
```

## Custom apps per customer

Set the `swarm.emai.io/customer-links` annotation on the customer's `KaiInstance` to a JSON array. Anything in that array is appended to the apps grid:

```yaml
apiVersion: swarm.emai.io/v1alpha1
kind: KaiInstance
metadata:
  name: kai-acme-gmbh
  namespace: swarm-system
  annotations:
    swarm.emai.io/customer-links: |
      [
        {
          "label": "MissionControl",
          "url": "https://mc.emai.dev/acme-gmbh",
          "description": "Project board: tasks, meetings, decisions.",
          "icon": "📋"
        },
        {
          "label": "Robot fleet",
          "url": "https://neodem.emai.dev/acme",
          "description": "NeoDEM dashboard for your robots.",
          "icon": "🤖"
        }
      ]
spec:
  customerName: "Acme GmbH"
  ...
```

Custom links are always rendered with an `External ↗` badge and open in a new tab.

## Profile (team / scope / rhythms)

Per-tenant team list and the tenant-facing distillations of `SOUL.md` / `HEARTBEAT.md` live in a single ConfigMap. The operator's onboarding flow can populate this from the equivalent files in your private overlay's `customers/<slug>/` directory:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: kai-acme-gmbh-profile     # convention: kai-<slug>-profile
  namespace: swarm-system
data:
  team.json: |
    [
      {
        "name": "Anna Schmidt",
        "role": "Project Lead",
        "company": "Acme GmbH",
        "email": "anna.schmidt@acme.de",
        "timezone": "Europe/Berlin"
      }
    ]
  scope.md: |
    ## What Kai handles for you
    - Project tracking
    - Meeting prep
    - Documentation

    ## Out of scope
    Kai does *not* handle billing or contracts.
  heartbeat.md: |
    - **Monday 09:00** — Weekly status briefing posted here.
    - **Wednesday** — Mid-week task triage in chat.
    - **Friday 16:00** — Sprint summary.
```

All three keys are optional. Missing keys hide the corresponding section.

## Briefings

Briefings live in a separate per-customer ConfigMap so any tooling (the agent itself via `mc`, an admin script, a future briefings-service) can publish them without touching this app:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: kai-acme-gmbh-briefings   # convention: kai-<slug>-briefings
  namespace: swarm-system
data:
  briefings.json: |
    [
      {
        "id": "2026-04-28-week-17",
        "title": "Weekly briefing — Week 17",
        "date": "2026-04-28T09:00:00Z",
        "excerpt": "3 tasks completed, 2 in flight, milestone review on Thursday.",
        "body": "## Highlights\n- Migrated CI to ARM64\n..."
      }
    ]
```

If the ConfigMap is absent or the JSON fails to parse, the briefings section shows a friendly empty-state.

## Local development

Requires Node 22+ and Go 1.25+.

```bash
# terminal 1 — backend
cd server
go run .

# terminal 2 — frontend with hot reload
npm install
npm run dev   # http://localhost:3004/workspace/<slug>
```

`DEMO_MODE=1` on the backend serves canned data (no K8s required); login accepts any email + password.

Without a kubeconfig the API returns 503 (the page surfaces "Hub temporarily unavailable").

## Build & deploy

```bash
docker build -t emai-workspace:latest .
```

```bash
kubectl apply -k ../../kubernetes/

# port-forward for local access
kubectl port-forward -n swarm-system svc/workspace 8080:8080
```

K8s manifests live at [`../../kubernetes/workspace/`](../../kubernetes/workspace/).

## API

| Method | Path                                                  | Auth                | Returns                                                                  |
|--------|-------------------------------------------------------|---------------------|--------------------------------------------------------------------------|
| GET    | `/healthz`                                            | none                | `ok`                                                                     |
| GET    | `/api/workspace/{slug}/auth`                             | none                | `{authenticated, email?, slug?, needsSetup}` — drives the login UI        |
| POST   | `/api/workspace/{slug}/login`                            | email + password    | Sets `kai-session` cookie. Bootstraps when `users.json == []`.           |
| POST   | `/api/workspace/{slug}/logout`                           | cookie              | Clears the cookie.                                                       |
| GET    | `/api/workspace/{slug}`                                  | cookie              | `{customerName, projectName, slug, status, statusLabel, links[], channels[], team[], scope, heartbeat, briefings[]}` |
| GET    | `/api/workspace/{slug}/users`                            | cookie              | `[{email, createdAt, passwordUpdatedAt}]` — no hashes ever exposed       |
| POST   | `/api/workspace/{slug}/users`                            | cookie              | Add `{email, password}` — server hashes argon2id                         |
| DELETE | `/api/workspace/{slug}/users/{email}`                    | cookie              | Remove user                                                              |
| POST   | `/api/workspace/{slug}/users/{email}/password`           | cookie              | Reset `{password}`                                                       |

`status` is one of `online | setting-up | paused | issue | unknown`, derived from the `KaiInstance` phase + ready + `spec.suspended`.

## Env vars

| Var                | Default       | Notes                                                                                              |
|--------------------|---------------|----------------------------------------------------------------------------------------------------|
| `ADDR`             | `:8080`       | Listen address                                                                                     |
| `SWARM_NAMESPACE`  | `swarm-system`  | Namespace where `KaiInstance`s and per-customer Secrets/ConfigMaps live                            |
| `CHAT_BASE_URL`    | `""`          | Optional host prefix for the built-in chat link. Empty = same origin (`/chat/<slug>`).             |
| `STATUS_BASE_URL`  | `""`          | Same idea for the status link.                                                                     |
| `DEMO_MODE`        | `""`          | `1`/`true` to bypass K8s and serve canned data                                                     |
| `KUBECONFIG`       | (auto)        | Used outside cluster; in-cluster config first.                                                     |

## Security model

- **Argon2id password hashing** with default Go crypto params (`m=64MiB, t=3, p=4`). Hashes never leave the server.
- **HS256 JWT in HttpOnly + Secure + SameSite=Lax cookie**, 24h TTL. Signing secret is stored in `kai-<slug>-chat-bridge`. No revocation list in v1; password reset rotates the underlying user record but doesn't invalidate live sessions until expiry.
- **Same cookie + same secret as `chat`** — when both apps are served from one origin (production ingress), signing in once works for both.
- **Uniform 401 on bad sign-in** — wrong password, unknown email, missing/invalid slug all return `{"error":"invalid login"}`.
- **Read-only RBAC for the customer-facing surface** — `kaiinstances get`, `configmaps get`. The team-access admin endpoints add `secrets get/update/patch` (the app trusts itself to scope writes to `kai-*-users`).
- **Bootstrap-as-first-user** — when `users.json == []`, the next login creates the user. After that, the bootstrap path is closed automatically. No magic admin URL, no shared password to leak.
- **Briefings are sanitized markdown only** — `marked` with default escaping; no inline HTML.

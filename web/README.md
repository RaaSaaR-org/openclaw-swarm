# swarm/web

Five small Vite + plain-TypeScript SPAs (no React/Vue), each fronted by a
tiny embedded Go server that `//go:embed`s the build output:

| App | Port (dev) | Purpose |
|---|---|---|
| `chat`   | 3000 | Tenant-facing chat UI |
| `admin-console`   | 3001 | Platform-operator console |
| `onboarding`      | 3002 | Signup + provisioning UI |
| `status-page`     | 3003 | Per-tenant public status link |
| `workspace` | 3004 | Tenant project hub |

Plus a sibling contract directory `shared/branding/` consumed by every app.

## Theming — deploy-time brand swap

Each app loads brand tokens, copy strings, logo, and favicon from
`/branding/*` at runtime. The Go server serves that prefix from one of two
places, in order:

1. **Override directory.** When `BRANDING_DIR` env is set (the K8s
   manifests set it to `/branding`) and a file exists there, serve it.
   The K8s `<app>-branding` ConfigMap is mounted at that path with
   `optional: true`, so consumers swap brand by replacing the ConfigMap.
2. **Embedded defaults.** Otherwise, fall back to the files Vite copied
   into `dist/branding/` from `web/shared/branding/` at build time
   (Kai brand per `swarm-cloud/brand.md`).

The browser-side loader at `web/shared/branding/loader.ts` fetches
`/branding/branding.json`, falls back to bundled defaults on 404 / parse
error, and exposes a typed `Branding` object plus `applyBranding()` helper.

### What the contract owns

```
web/shared/branding/
  theme.css         — :root with canonical brand tokens (--bg, --accent, ...)
  branding.json     — typed Branding object (name, agentName, copy, …)
  logo.svg          — wordmark + brand mark
  favicon.svg       — mark only (per-app overrides ship distinct icons)
  loader.ts         — Branding type + loadBranding() + applyBranding()
  vite-plugin.ts    — registers @branding alias, dev middleware,
                      build-time copy into dist/branding/
```

Each app's `vite.config.ts` adds `brandingPlugin()` to its `plugins`
array, and each `tsconfig.json` adds `"@branding/*": ["../shared/branding/*"]`
to its `compilerOptions.paths`.

### CSS cascade

`index.html` carries two stylesheets in this order:

1. `<link rel="stylesheet" href="/branding/theme.css">` — canonical
   brand vars (`--bg`, `--accent`, `--text`, font, radii).
2. The bundled CSS (Vite injects it after) — each app's `style.css`
   `:root` block defines compatibility aliases: `--base: var(--bg)`,
   `--caltrans-orange: var(--accent)`, etc., so existing rules using
   the legacy var names keep working unchanged.

Brand overrides therefore only need to set canonical names; aliases
follow.

### Brand strings

Hardcoded "EmAI" / "Kai" / copy strings in `main.ts` files were replaced
with reads from the `Branding` object:

```ts
import { loadBranding, applyBranding, DEFAULT_BRANDING, type Branding }
  from '@branding/loader';

let b: Branding = DEFAULT_BRANDING;

async function bootstrap() {
  b = await loadBranding();
  applyBranding(b, { docTitle: b.copy.<scope>.docTitle });
  // … render with b.name, b.copy.<scope>.<key>, b.logoUrl, b.domain, …
}
```

Bundled defaults still paint the right surface colours and font on
first byte (so there's no flash-of-unstyled-content); only strings
inside `#app` swap when `loadBranding()` resolves, and `#app` is
empty until the app's `bootstrap()` finishes anyway.

## Override mechanism — operator side

Public swarm K8s manifests ship default `<app>-branding` ConfigMaps with
the Kai brand inline. To deploy with a different brand from your private
overlay repo:

1. Curate brand assets:
   ```
   <overlay-repo>/branding/
     _shared/{branding.json, theme.css, logo.svg}
     <app>/favicon.svg     # per app — may differ to keep tab-strip distinct
   ```
2. In the deploy script, replace the ConfigMaps:
   ```bash
   for app in chat workspace admin-console onboarding status-page; do
     kubectl delete configmap "${app}-branding" --ignore-not-found
     kubectl create configmap "${app}-branding" \
       --from-file=branding.json="$DIR/_shared/branding.json" \
       --from-file=theme.css="$DIR/_shared/theme.css" \
       --from-file=logo.svg="$DIR/_shared/logo.svg" \
       --from-file=favicon.svg="$DIR/$app/favicon.svg"
   done
   ```
3. Pods pick up the new files within ~60 s as the kubelet propagates
   the ConfigMap; no rollout needed unless you want to force one.

## Local dev

Each app:

```sh
cd web/<app>
npm install
npm run dev   # http://localhost:<port>
```

The dev server's branding middleware serves `web/shared/branding/*` at
`/branding/*` with the right `Content-Type` headers, so the app sees
the same shape it'll see in production with the embedded defaults.

To preview an override locally, copy your override files into the same
`web/shared/branding/` paths before `npm run dev`. (For end-to-end
verification with a mounted ConfigMap, use the k3d flow via your private
overlay's `deploy.sh dev`.)

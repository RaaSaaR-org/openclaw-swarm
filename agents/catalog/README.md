# Agent Catalog

Curated personas a tenant can pick at signup. Each subdirectory is one **app** —
the user-facing product term for "an agent persona". An app is *not* a separate
runtime entity; it's a SOUL.md plus a `metadata.yaml` describing how to surface
it in the chooser UI.

## Layout per app

```
agents/catalog/<slug>/
├── SOUL.md.tmpl     # persona + tone + scope; uses {{WORKSPACE_NAME}}, {{USER_NAME}} placeholders
├── metadata.yaml    # chooser metadata (name, category, description, recommendedModel, etc.)
└── icon.svg         # 64×64 monochrome icon for the gallery thumbnail
```

## metadata.yaml schema

```yaml
name: Personal Assistant            # display name (English)
nameDe: Persoenlicher Assistent     # display name (German — UI shows by locale)
slug: personal-assistant            # must match directory name
category: lifestyle                 # one of: lifestyle | productivity | learning | creative | development
shortDescription: ...               # one sentence, ≤120 chars, English
shortDescriptionDe: ...             # German equivalent
recommendedModel: openrouter/...    # default model; per-tier override happens in operator
toolsProfile: messaging             # one of: messaging | coding (OpenClaw profile)
tier: free                          # min tier required: free | plus | pro
tags: [chat, daily, summary]        # for filter chips in the chooser
suggestedPrompts:                   # 3 starter prompts shown on the empty-chat screen
  - "Was steht heute an?"
  - "Fasse meine letzten Notizen zusammen."
  - "Erinnere mich morgen 9 Uhr an das Standup."
```

## Placeholders in SOUL.md.tmpl

| Placeholder         | Filled with                                         |
|---------------------|-----------------------------------------------------|
| `{{WORKSPACE_NAME}}`| Tenant's workspace name (per-instance)              |
| `{{USER_NAME}}`     | Display name of the workspace owner                 |
| `{{APP_NAME}}`      | This app's display name (from metadata.yaml `name`) |

The operator's template renderer fills these per workspace. Catalog SOUL.md
files do **not** reference customer/business concepts — that vocabulary lives
in private deployment overlays only (`swarm-emai`).

## Adding an app

1. Create `agents/catalog/<slug>/` with the three files above.
2. Author the SOUL.md persona in German (CLAUDE.md convention) with English
   fallback paragraphs where helpful.
3. Pick a `recommendedModel` that exists in OpenRouter and fits the tier.
4. Add a 64×64 SVG icon — keep it monochrome so it inherits the UI accent.
5. The chooser UI reads this directory at build time; rebuild the customer-
   center to pick up the new entry.

## Naming

The catalog ships in the public `swarm` repo, so it uses neutral terminology:
**tenant**, **workspace**, **user**, **app**. Private deployment repos
(`swarm-emai`, `swarm-cloud`) may layer on their own customer/billing concepts;
the catalog itself stays product-generic.

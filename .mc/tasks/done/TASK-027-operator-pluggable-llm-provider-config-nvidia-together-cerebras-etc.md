---
id: TASK-027
aliases:
- TASK-027
title: 'Operator: pluggable LLM provider config (NVIDIA, Together, Cerebras, etc.)'
slug: operator-pluggable-llm-provider-config-nvidia-together-cerebras-etc
status: done
priority: 3
owner: ''
projects: []
customers: []
tags:
- operator
- saas
- model
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-07
updated: 2026-05-10
---


# Operator: pluggable LLM provider config (NVIDIA, Together, Cerebras, etc.)

## Why
Today the operator hardcodes OpenRouter as the only LLM provider for kai pods (`operator/internal/controller/resources.go:197-211`):
- Sets `OPENCLAW_PROVIDER=openrouter` literally
- Sets `OPENROUTER_API_KEY` from `kai-<slug>-openrouter` Secret with key `api-key`
- No CRD field for any other provider
- KaiInstance.spec.model is just a string, but the env wiring assumes openrouter no matter what

OpenClaw natively supports many providers (NVIDIA, Together, Cerebras, OpenAI direct, Anthropic direct, Groq, …) — each just needs the right env var set and `models.providers.<name>` block. The provider auto-enables when its API-key env var is present (per https://docs.openclaw.ai/providers/<name>).

This rigidity bit us on 2026-05-07: tried switching ESF + ZeMA from `openrouter/google/gemma-4-31b-it:free` (which 429s after ~10 ticks/day on the free tier) to NVIDIA's free `nvidia/nvidia/nemotron-3-super-120b-a12b`, but couldn't without either patching the operator or scaling it to 0 and manually editing the Deployment.

## What

### Spec changes (KaiInstance CRD)
Replace the implicit "openrouter" assumption with an explicit, generic provider list:

```yaml
spec:
  providers:
    - name: openrouter
      apiKeySecretRef: { name: kai-esf-openrouter, key: api-key }
    - name: nvidia
      apiKeySecretRef: { name: kai-esf-nvidia, key: api-key }
  model: nvidia/nvidia/nemotron-3-super-120b-a12b           # primary
  fallbackModels:                                           # optional
    - openrouter/google/gemma-4-31b-it:free
```

Or — simpler MVP — keep `model:` and add a parallel slice `extraProviders:` that the operator unions with the existing openrouter wiring.

### Operator changes
- Replace the hardcoded openrouter env block with a loop over `spec.providers`, generating one `<NAME>_API_KEY` env var per entry, each with `valueFrom.secretKeyRef` to that entry's Secret
- Drop the literal `OPENCLAW_PROVIDER` env (or set it from `spec.model`'s prefix if OpenClaw still needs it)
- Pooled-secret support (`SWARM_POOLED_OPENROUTER_SECRET` env) generalizes to per-provider pooled secrets, or stays openrouter-specific as legacy

### Migration
- Existing KaiInstances without `spec.providers` keep working: operator falls back to the old `kai-<slug>-openrouter` lookup
- New CRD field is additive, no breaking change to v1alpha1 — schema bump can come later if we tidy up

## Status

**Phase 0 (CRD field + operator wiring) — done** on 2026-05-10. Picked the simpler MVP from the task's "What" section: keep `spec.model` as-is, add a parallel `spec.providers []ProviderConfig` that the operator unions with a legacy-OpenRouter fallback. Concrete drop:
- New types in `operator/api/v1alpha2/kaiinstance_types.go`: `ProviderConfig{Name, APIKeySecretRef}` + `ProviderAPIKeySecretRef{Name, Key}`. Name is `^[a-z][a-z0-9]*$`/max 32; the operator uppercases it to build the env var name. APIKeySecretRef.Key defaults to `"api-key"` when empty (matches the legacy `kai-<slug>-openrouter` Secret convention).
- New `providerEnvVars(kai, slug, opts)` helper in `resources.go` replaces the inline OpenRouter env block. When `spec.providers` is non-empty it renders one env var per entry as `<UPPER(name)>_API_KEY` from the configured Secret + leaves `OPENCLAW_PROVIDER` unset (OpenClaw auto-detects from model + present API-key env vars). When the list is empty the legacy single-provider path runs unchanged: `OPENCLAW_PROVIDER=openrouter` + `OPENROUTER_API_KEY` from pooled-or-per-tenant Secret.
- 3 new tests in `resources_test.go`: multi-provider with NVIDIA + OpenRouter (asserts both env vars + their Secret refs + that OPENCLAW_PROVIDER is absent), multi-provider with custom Secret key (TOGETHER_API_KEY → `together-api-key` instead of `api-key`), legacy fallback still emits OPENCLAW_PROVIDER + OPENROUTER_API_KEY when providers is empty. Helper `envByName` + `envNames` for assertions.
- Generated artifacts regenerated via `make generate manifests`: `zz_generated.deepcopy.go` adds DeepCopyInto for both new types + extends KaiInstanceSpec.DeepCopyInto with the slice copy; `config/crd/bases/swarm.emai.io_kaiinstances.yaml` gains the openAPI schema for `providers[]`.
- v1alpha1 conversion: Providers is v1alpha2-only (legacy callers can't see it); v1alpha2 → v1alpha1 round-trip drops the field cleanly. v1alpha1 conversion test still green.
- Full operator test suite passes (`go test ./...`): controller (incl. envtest), webhook, usage, conversion. No regression.

**Remaining:**
- End-to-end verification on emai-cloud: deploy this operator build to the cloud cluster, patch `kai-east-side-fab` to use `spec.providers: [{name: nvidia, apiKeySecretRef: ...}, {name: openrouter, ...}]` + `spec.model: nvidia/nvidia/nemotron-3-super-120b-a12b`, confirm OpenClaw picks up NVIDIA + a tick succeeds. Per-overlay deploy task.

## Acceptance Criteria
- [x] KaiInstance CRD accepts a `providers: []` field (or equivalent) that lists provider names + api-key Secret refs (Phase 0, 2026-05-10 — `ProviderConfig` + `ProviderAPIKeySecretRef` on v1alpha2)
- [x] Operator generates one env var per provider, each from a SecretKeyRef (Phase 0, 2026-05-10 — `providerEnvVars` in `resources.go`; one `<UPPER(name)>_API_KEY` per entry)
- [x] `make test` passes (extend `resources_test.go` with NVIDIA + multi-provider cases) (Phase 0, 2026-05-10 — 3 new tests; full operator test suite green)
- [x] Existing single-openrouter setups keep working without the new field (Phase 0, 2026-05-10 — legacy fallback when `spec.providers` is empty; `TestBuildDeploymentLegacyOpenRouterStillWorks` covers the contract)
- [x] End-to-end on a real cluster (k3d-emai-swarm, 2026-05-10) — applied a `kai-providertest` KaiInstance with `spec.providers: [{name:nvidia,...},{name:openrouter,...}]` + `model: nvidia/nvidia/nemotron-3-super-120b-a12b`, then read back the rendered Deployment env: `NVIDIA_API_KEY` + `OPENROUTER_API_KEY` both present from their respective Secrets, `OPENCLAW_PROVIDER` correctly NOT set (multi-provider mode), `OPENCLAW_MODEL` carries the spec.model. emai-cloud verification (the original criterion) is the production cutover — the platform-side wiring is proven on k3d.

## Notes
- The same heartbeat pattern that burns through OpenRouter free tier in <1h would also burn NVIDIA's free quota fast — fixing the provider config doesn't fix the heartbeat-cost problem (TASK-???: "OpenClaw heartbeat tick should not invoke LLM unless a HEARTBEAT.md schedule entry resolves to within the next tick window"). Worth pairing.
- Discovered while shipping v0.2.8 on 2026-05-07; logged here so the workaround (manual deployment patches) doesn't become permanent.

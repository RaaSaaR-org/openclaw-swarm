package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/emai-ai/swarm/pkg/openrouter"
	"github.com/emai-ai/swarm/pkg/quotas"
	"github.com/emai-ai/swarm/pkg/users"
)

// keyProvisioner mints + revokes per-workspace OpenRouter sub-keys (TASK-019
// Phase 2.B). Two implementations:
//
//   - noopKeyProvisioner returns a deterministic dev key derived from the
//     workspace slug. Used when OPENROUTER_PROVISIONING_KEY is unset so
//     local dev / tests don't need a real provisioning key.
//   - openrouterKeyProvisioner wraps `pkg/openrouter.Client` and calls the
//     real provisioning API. Wired up in production by setting
//     OPENROUTER_PROVISIONING_KEY.
//
// The per-workspace key is stored in a `kai-<slug>-openrouter` Secret in
// the same namespace as the KaiInstance; the operator already mounts this
// Secret into the agent pod via the `OPENROUTER_API_KEY` env var (per
// TASK-019 Phase 0). The pooled-key fallback `SWARM_POOLED_OPENROUTER_SECRET`
// must be **unset** in the deploy for per-workspace keys to take effect.
type keyProvisioner interface {
	// MintForWorkspace returns the freshly-minted key + the OpenRouter
	// hash identifier for follow-up revoke. The hash is opaque; treat it
	// as a string. Returns ErrProvisioningUnavailable if the implementation
	// is intentionally a no-op so callers can decide whether to surface
	// that to the user (currently we log + continue — the operator's
	// pooled-key fallback covers the gap until ops fixes the provisioning).
	MintForWorkspace(ctx context.Context, slug string, tier users.Tier) (key, hash string, err error)
}

// ErrProvisioningUnavailable signals the provisioner is unconfigured
// (OPENROUTER_PROVISIONING_KEY missing). Callers can branch on it to
// decide whether to fall back to the pooled key.
var ErrProvisioningUnavailable = errors.New("openrouter provisioning unavailable")

// noopKeyProvisioner returns a deterministic dev-mode key. Same input slug
// always yields the same fake key — useful for replayable local tests. The
// fake key starts with `sk-or-v1-noop-` so it's obvious in logs that it's
// not a real OpenRouter token.
type noopKeyProvisioner struct{}

func (noopKeyProvisioner) MintForWorkspace(_ context.Context, slug string, _ users.Tier) (string, string, error) {
	sum := sha256.Sum256([]byte(slug))
	suffix := hex.EncodeToString(sum[:8])
	return "sk-or-v1-noop-" + suffix, "h_noop_" + suffix, nil
}

// openrouterKeyProvisioner wires through to the real REST client. The
// per-tier daily limit comes from pkg/quotas — free tier gets a small cap,
// paid tiers proportionally larger. Empty cap (enterprise) maps to nil
// which OpenRouter treats as "no per-key limit" (the account's overall
// budget still applies upstream).
type openrouterKeyProvisioner struct {
	client *openrouter.Client
}

func newOpenRouterKeyProvisioner(provisioningKey string) (*openrouterKeyProvisioner, error) {
	if provisioningKey == "" {
		return nil, ErrProvisioningUnavailable
	}
	c, err := openrouter.NewClient(provisioningKey)
	if err != nil {
		return nil, err
	}
	return &openrouterKeyProvisioner{client: c}, nil
}

func (p *openrouterKeyProvisioner) MintForWorkspace(ctx context.Context, slug string, tier users.Tier) (string, string, error) {
	limits := quotas.For(quotas.Tier(tier))
	params := openrouter.MintKeyParams{
		Label: "kai-" + slug,
		Name:  strings.TrimPrefix(slug, "kai-"),
	}
	if limits.DailyDollars > 0 {
		dollars := limits.DailyDollars
		params.Limit = &dollars
	}
	minted, err := p.client.MintKey(ctx, params)
	if err != nil {
		return "", "", err
	}
	return minted.Key, minted.Hash, nil
}

// resolveKeyProvisioner picks the implementation based on env config.
// Mirrors the Turnstile / catalog patterns: real impl when env is set,
// safe noop otherwise.
func resolveKeyProvisioner(provisioningKey string) keyProvisioner {
	if provisioningKey == "" {
		log.Printf("signup: no OPENROUTER_PROVISIONING_KEY set — using noop key provisioner (workspaces fall back to the pooled key if SWARM_POOLED_OPENROUTER_SECRET is set on the operator)")
		return noopKeyProvisioner{}
	}
	p, err := newOpenRouterKeyProvisioner(provisioningKey)
	if err != nil {
		log.Printf("signup: OPENROUTER_PROVISIONING_KEY rejected (%v); falling back to noop provisioner", err)
		return noopKeyProvisioner{}
	}
	log.Printf("signup: OpenRouter per-workspace key provisioning enabled")
	return p
}

// writeOpenRouterSecret creates (or updates) the per-tenant
// `kai-<slug>-openrouter` Secret with the freshly-minted key. Idempotent —
// re-mint after a key rotation overwrites the secret in place. Lives next
// to the KaiInstance in the same namespace so the operator's resources.go
// can mount it via `OPENROUTER_API_KEY` env-from-secret.
//
// Stored keys:
//   - `api-key`: the raw `sk-or-v1-…` token the agent pod uses
//   - `provisioning-hash`: OpenRouter's hash for the key, used by future
//     revoke + usage-poll cron jobs
func (s *server) writeOpenRouterSecret(ctx context.Context, slug, key, hash string) error {
	name := "kai-" + slug + "-openrouter"
	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: s.namespace,
			Labels: map[string]string{
				"swarm.io/tenant":  slug,
				"swarm.io/managed": "saas",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"api-key":           key,
			"provisioning-hash": hash,
		},
	}
	_, err := s.core.CoreV1().Secrets(s.namespace).Create(ctx, desired, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return err
	}
	existing, getErr := s.core.CoreV1().Secrets(s.namespace).Get(ctx, name, metav1.GetOptions{})
	if getErr != nil {
		return getErr
	}
	if existing.StringData == nil {
		existing.StringData = map[string]string{}
	}
	existing.StringData["api-key"] = key
	existing.StringData["provisioning-hash"] = hash
	_, updateErr := s.core.CoreV1().Secrets(s.namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return updateErr
}

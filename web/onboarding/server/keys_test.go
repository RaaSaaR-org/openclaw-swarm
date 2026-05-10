package main

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/emai-ai/swarm/pkg/users"
)

func TestNoopKeyProvisioner_DeterministicPerSlug(t *testing.T) {
	t.Parallel()
	p := noopKeyProvisioner{}
	k1, h1, err := p.MintForWorkspace(context.Background(), "anna", users.TierFree)
	if err != nil {
		t.Fatal(err)
	}
	k2, h2, err := p.MintForWorkspace(context.Background(), "anna", users.TierStarter)
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k2 || h1 != h2 {
		t.Errorf("noop should be deterministic per-slug, got two different outputs: (%s,%s) vs (%s,%s)", k1, h1, k2, h2)
	}
	if !strings.HasPrefix(k1, "sk-or-v1-noop-") {
		t.Errorf("noop key should start with sk-or-v1-noop-, got %q", k1)
	}
	k3, _, _ := p.MintForWorkspace(context.Background(), "bob", users.TierFree)
	if k3 == k1 {
		t.Errorf("different slugs should produce different noop keys")
	}
}

func TestResolveKeyProvisioner_FallsBackToNoopWhenUnset(t *testing.T) {
	t.Parallel()
	p := resolveKeyProvisioner("")
	if _, ok := p.(noopKeyProvisioner); !ok {
		t.Errorf("expected noopKeyProvisioner, got %T", p)
	}
}

func TestResolveKeyProvisioner_RealWhenKeySet(t *testing.T) {
	t.Parallel()
	p := resolveKeyProvisioner("sk-or-v1-fake-provisioning")
	if _, ok := p.(*openrouterKeyProvisioner); !ok {
		t.Errorf("expected openrouterKeyProvisioner, got %T", p)
	}
}

func TestWriteOpenRouterSecret_CreatesNewSecret(t *testing.T) {
	t.Parallel()
	core := fake.NewSimpleClientset()
	s := &server{namespace: "swarm-system", core: core}
	if err := s.writeOpenRouterSecret(context.Background(), "anna", "sk-or-v1-fresh", "h_abc"); err != nil {
		t.Fatal(err)
	}
	got, err := core.CoreV1().Secrets("swarm-system").Get(context.Background(), "kai-anna-openrouter", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Data["api-key"]) == "" && got.StringData["api-key"] != "sk-or-v1-fresh" {
		t.Errorf("api-key not stored correctly, StringData=%v Data=%v", got.StringData, got.Data)
	}
	if got.StringData["provisioning-hash"] != "h_abc" {
		t.Errorf("provisioning-hash = %q", got.StringData["provisioning-hash"])
	}
	if got.Labels["swarm.io/tenant"] != "anna" {
		t.Errorf("tenant label = %q", got.Labels["swarm.io/tenant"])
	}
}

func TestWriteOpenRouterSecret_OverwritesOnRotation(t *testing.T) {
	t.Parallel()
	core := fake.NewSimpleClientset()
	s := &server{namespace: "swarm-system", core: core}
	// First mint.
	if err := s.writeOpenRouterSecret(context.Background(), "anna", "sk-or-v1-old", "h_old"); err != nil {
		t.Fatal(err)
	}
	// Rotate — same slug, new key + hash. Existing-secret branch must not fail.
	if err := s.writeOpenRouterSecret(context.Background(), "anna", "sk-or-v1-new", "h_new"); err != nil {
		t.Fatalf("rotation: %v", err)
	}
	got, err := core.CoreV1().Secrets("swarm-system").Get(context.Background(), "kai-anna-openrouter", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Updated value (StringData) wins over original.
	if got.StringData["api-key"] != "sk-or-v1-new" {
		t.Errorf("after rotation, api-key = %q, want sk-or-v1-new", got.StringData["api-key"])
	}
	if got.StringData["provisioning-hash"] != "h_new" {
		t.Errorf("after rotation, hash = %q", got.StringData["provisioning-hash"])
	}
}

func TestMintAndStoreOpenRouterKey_NoopWhenMinterMissing(t *testing.T) {
	t.Parallel()
	core := fake.NewSimpleClientset()
	s := &server{namespace: "swarm-system", core: core, keyMinter: nil}
	s.mintAndStoreOpenRouterKey(context.Background(), "anna", &users.User{Tier: users.TierFree})
	// No Secret should have been created.
	if _, err := core.CoreV1().Secrets("swarm-system").Get(context.Background(), "kai-anna-openrouter", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("expected no Secret, got err=%v", err)
	}
}

func TestMintAndStoreOpenRouterKey_NoopWhenCoreClientMissing(t *testing.T) {
	t.Parallel()
	// core nil → skip silently. The dev-mode (no kubeconfig) path.
	s := &server{namespace: "swarm-system", core: nil, keyMinter: noopKeyProvisioner{}}
	s.mintAndStoreOpenRouterKey(context.Background(), "anna", &users.User{Tier: users.TierFree})
	// Nothing to assert beyond "didn't panic" — passes if it returns.
}

func TestMintAndStoreOpenRouterKey_HappyPath(t *testing.T) {
	t.Parallel()
	core := fake.NewSimpleClientset()
	s := &server{namespace: "swarm-system", core: core, keyMinter: noopKeyProvisioner{}}
	s.mintAndStoreOpenRouterKey(context.Background(), "anna", &users.User{Tier: users.TierFree})
	got, err := core.CoreV1().Secrets("swarm-system").Get(context.Background(), "kai-anna-openrouter", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Secret should exist after mint: %v", err)
	}
	if !strings.HasPrefix(got.StringData["api-key"], "sk-or-v1-noop-") {
		t.Errorf("api-key = %q (want sk-or-v1-noop- prefix)", got.StringData["api-key"])
	}
}

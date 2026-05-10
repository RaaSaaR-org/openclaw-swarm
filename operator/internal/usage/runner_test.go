/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
*/

package usage

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	corefake "k8s.io/client-go/kubernetes/fake"
)

// fakeReader is a deterministic UsageReader for tests.
type fakeReader struct {
	usage map[string]float64
	err   map[string]error
}

func (f *fakeReader) UsageDailyUSD(_ context.Context, apiKey string) (float64, error) {
	if e, ok := f.err[apiKey]; ok && e != nil {
		return 0, e
	}
	return f.usage[apiKey], nil
}

func kaiObj(slug, tier string, suspended bool) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(schema.GroupVersionKind{Group: "swarm.emai.io", Version: "v1alpha2", Kind: "KaiInstance"})
	o.SetName("kai-" + slug)
	o.SetNamespace("swarm-system")
	o.SetLabels(map[string]string{"swarm.io/managed": "saas", "swarm.io/tenant": slug})
	spec := map[string]any{"tenantName": slug, "projectName": "P", "tier": tier, "managed": "saas"}
	if suspended {
		spec["suspended"] = true
	}
	_ = unstructured.SetNestedMap(o.Object, spec, "spec")
	return o
}

func openrouterSecret(slug, key string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kai-" + slug + "-openrouter", Namespace: "swarm-system"},
		Data:       map[string][]byte{"api-key": []byte(key)},
	}
}

func newRunner(t *testing.T, kais []*unstructured.Unstructured, secrets []*corev1.Secret, reader UsageReader) *Runner {
	t.Helper()
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{kaiInstanceGVR: "KaiInstanceList"}
	objs := make([]runtime.Object, 0, len(kais))
	for _, k := range kais {
		objs = append(objs, k)
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
	coreObjs := make([]runtime.Object, 0, len(secrets))
	for _, s := range secrets {
		coreObjs = append(coreObjs, s)
	}
	core := corefake.NewSimpleClientset(coreObjs...)
	return &Runner{
		Dyn:       dyn,
		Core:      core,
		Namespace: "swarm-system",
		Reader:    reader,
		Now:       func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
	}
}

func TestRun_SuspendsOverFreeTier(t *testing.T) {
	t.Parallel()
	kai := kaiObj("anna", "free", false)
	sec := openrouterSecret("anna", "sk-or-anna")
	r := newRunner(t, []*unstructured.Unstructured{kai}, []*corev1.Secret{sec}, &fakeReader{
		usage: map[string]float64{"sk-or-anna": 1.50}, // free tier cap is $1
	})
	results, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Action != "suspended" {
		t.Errorf("Action = %q, want suspended (Reason=%q)", results[0].Action, results[0].Reason)
	}
	// Verify the patch landed on the live object.
	live, err := r.Dyn.Resource(kaiInstanceGVR).Namespace("swarm-system").Get(context.Background(), "kai-anna", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	suspended, _, _ := unstructured.NestedBool(live.Object, "spec", "suspended")
	if !suspended {
		t.Error("spec.suspended was not patched true")
	}
	if ann := live.GetAnnotations()[AnnotationOver]; ann == "" {
		t.Error("usage-suspended-at annotation missing")
	}
}

func TestRun_PassesUnderCap(t *testing.T) {
	t.Parallel()
	kai := kaiObj("anna", "starter", false)
	sec := openrouterSecret("anna", "sk-or-anna")
	r := newRunner(t, []*unstructured.Unstructured{kai}, []*corev1.Secret{sec}, &fakeReader{
		usage: map[string]float64{"sk-or-anna": 0.5}, // starter cap is $3
	})
	results, _ := r.Run(context.Background())
	if results[0].Action != "ok" {
		t.Errorf("Action = %q, want ok (Reason=%q)", results[0].Action, results[0].Reason)
	}
	live, _ := r.Dyn.Resource(kaiInstanceGVR).Namespace("swarm-system").Get(context.Background(), "kai-anna", metav1.GetOptions{})
	suspended, _, _ := unstructured.NestedBool(live.Object, "spec", "suspended")
	if suspended {
		t.Error("under-cap workspace must not be suspended")
	}
	// last-usage-check annotation should be stamped.
	if ann := live.GetAnnotations()[AnnotationLast]; ann == "" {
		t.Error("last-usage-check annotation missing")
	}
}

func TestRun_SkipsAlreadySuspended(t *testing.T) {
	t.Parallel()
	kai := kaiObj("anna", "free", true)
	sec := openrouterSecret("anna", "sk-or-anna")
	called := 0
	r := newRunner(t, []*unstructured.Unstructured{kai}, []*corev1.Secret{sec}, &fakeReader{
		usage: map[string]float64{"sk-or-anna": 0.0},
	})
	r.Reader = &countingReader{inner: r.Reader, count: &called}
	results, _ := r.Run(context.Background())
	if results[0].Action != "skipped" {
		t.Errorf("Action = %q, want skipped (Reason=%q)", results[0].Action, results[0].Reason)
	}
	if called != 0 {
		t.Errorf("must not call OpenRouter for already-suspended workspaces (called %d)", called)
	}
}

func TestRun_SkipsEnterpriseUnboundedTier(t *testing.T) {
	t.Parallel()
	kai := kaiObj("megacorp", "enterprise", false)
	sec := openrouterSecret("megacorp", "sk-or-mc")
	r := newRunner(t, []*unstructured.Unstructured{kai}, []*corev1.Secret{sec}, &fakeReader{
		usage: map[string]float64{"sk-or-mc": 999.0},
	})
	results, _ := r.Run(context.Background())
	if results[0].Action != "skipped" {
		t.Errorf("Action = %q, want skipped", results[0].Action)
	}
}

func TestRun_SkipsWorkspaceWithoutSecret(t *testing.T) {
	t.Parallel()
	kai := kaiObj("anna", "free", false)
	// no Secret seeded → fall back to pooled key, out of scope here.
	r := newRunner(t, []*unstructured.Unstructured{kai}, nil, &fakeReader{})
	results, _ := r.Run(context.Background())
	if results[0].Action != "skipped" {
		t.Errorf("Action = %q, want skipped (Reason=%q)", results[0].Action, results[0].Reason)
	}
}

func TestRun_ErrorReadingUsageRecorded(t *testing.T) {
	t.Parallel()
	kai := kaiObj("anna", "free", false)
	sec := openrouterSecret("anna", "sk-or-bad")
	r := newRunner(t, []*unstructured.Unstructured{kai}, []*corev1.Secret{sec}, &fakeReader{
		err: map[string]error{"sk-or-bad": errors.New("openrouter 401: revoked")},
	})
	results, _ := r.Run(context.Background())
	if results[0].Action != "error" {
		t.Errorf("Action = %q, want error", results[0].Action)
	}
	// Workspace must NOT be suspended on a transient read error.
	live, _ := r.Dyn.Resource(kaiInstanceGVR).Namespace("swarm-system").Get(context.Background(), "kai-anna", metav1.GetOptions{})
	if s, _, _ := unstructured.NestedBool(live.Object, "spec", "suspended"); s {
		t.Error("read error must not trigger suspension")
	}
}

func TestRun_OneBadWorkspaceDoesNotAbortPass(t *testing.T) {
	t.Parallel()
	good := kaiObj("anna", "starter", false)
	bad := kaiObj("bob", "free", false)
	r := newRunner(t, []*unstructured.Unstructured{good, bad}, []*corev1.Secret{
		openrouterSecret("anna", "sk-or-anna"),
		openrouterSecret("bob", "sk-or-bob"),
	}, &fakeReader{
		usage: map[string]float64{"sk-or-anna": 0.5},
		err:   map[string]error{"sk-or-bob": errors.New("boom")},
	})
	results, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run aborted on a single-workspace error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
}

// countingReader counts UsageDailyUSD calls so a test can assert that the
// poll was avoided for already-suspended workspaces.
type countingReader struct {
	inner UsageReader
	count *int
}

func (c *countingReader) UsageDailyUSD(ctx context.Context, key string) (float64, error) {
	*c.count++
	return c.inner.UsageDailyUSD(ctx, key)
}

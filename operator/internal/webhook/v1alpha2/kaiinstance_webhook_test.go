/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha2

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	swarmv1alpha2 "github.com/emai-ai/swarm-operator/api/v1alpha2"
)

func newKai(spec swarmv1alpha2.KaiInstanceSpec) *swarmv1alpha2.KaiInstance {
	return &swarmv1alpha2.KaiInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "kai-test", Namespace: "swarm-system"},
		Spec:       spec,
	}
}

func TestValidator_LegacyTenantPasses(t *testing.T) {
	t.Parallel()
	// No tier set → not SaaS-enrolled → webhook skips the check.
	v := &KaiInstanceValidator{}
	_, err := v.ValidateCreate(context.Background(), newKai(swarmv1alpha2.KaiInstanceSpec{
		TenantName:  "Legacy",
		ProjectName: "P",
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Gi")},
		},
	}))
	if err != nil {
		t.Errorf("legacy tenant (no tier) should pass, got %v", err)
	}
}

func TestValidator_InternalManagedPasses(t *testing.T) {
	t.Parallel()
	// `managed: internal` skips tier checks even if tier is set.
	v := &KaiInstanceValidator{}
	_, err := v.ValidateCreate(context.Background(), newKai(swarmv1alpha2.KaiInstanceSpec{
		TenantName:  "EmAI Internal",
		ProjectName: "P",
		Tier:        "free",
		Managed:     "internal",
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("8Gi")},
		},
	}))
	if err != nil {
		t.Errorf("managed=internal should pass, got %v", err)
	}
}

func TestValidator_RejectsOverFreeTier(t *testing.T) {
	t.Parallel()
	v := &KaiInstanceValidator{}
	_, err := v.ValidateCreate(context.Background(), newKai(swarmv1alpha2.KaiInstanceSpec{
		TenantName:  "Greedy",
		ProjectName: "P",
		Tier:        "free",
		Managed:     "saas",
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("4Gi")},
		},
	}))
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !strings.Contains(err.Error(), "free") || !strings.Contains(err.Error(), "requests.memory") {
		t.Errorf("error message = %q, want mention of free + requests.memory", err.Error())
	}
}

func TestValidator_AcceptsWithinFreeTier(t *testing.T) {
	t.Parallel()
	v := &KaiInstanceValidator{}
	_, err := v.ValidateCreate(context.Background(), newKai(swarmv1alpha2.KaiInstanceSpec{
		TenantName:  "Polite",
		ProjectName: "P",
		Tier:        "free",
		Managed:     "saas",
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")},
		},
	}))
	if err != nil {
		t.Errorf("within-tier spec should pass, got %v", err)
	}
}

func TestValidator_UpdateAlsoChecks(t *testing.T) {
	t.Parallel()
	v := &KaiInstanceValidator{}
	old := newKai(swarmv1alpha2.KaiInstanceSpec{
		TenantName: "T", ProjectName: "P", Tier: "free", Managed: "saas",
	})
	new := newKai(swarmv1alpha2.KaiInstanceSpec{
		TenantName: "T", ProjectName: "P", Tier: "free", Managed: "saas",
		Resources: &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("32")},
		},
	})
	_, err := v.ValidateUpdate(context.Background(), old, new)
	if err == nil {
		t.Error("update with over-tier resources should be rejected")
	}
}

func TestValidator_RejectsUnknownTier(t *testing.T) {
	t.Parallel()
	v := &KaiInstanceValidator{}
	_, err := v.ValidateCreate(context.Background(), newKai(swarmv1alpha2.KaiInstanceSpec{
		TenantName:  "T",
		ProjectName: "P",
		Tier:        "unobtainium",
		Managed:     "saas",
	}))
	if err == nil {
		t.Error("unknown tier should be rejected")
	}
}

func TestValidator_DeleteAlwaysAllowed(t *testing.T) {
	t.Parallel()
	v := &KaiInstanceValidator{}
	if _, err := v.ValidateDelete(context.Background(), newKai(swarmv1alpha2.KaiInstanceSpec{})); err != nil {
		t.Errorf("ValidateDelete should always pass, got %v", err)
	}
}

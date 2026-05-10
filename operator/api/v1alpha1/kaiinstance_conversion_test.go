/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1_test

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/emai-ai/swarm-operator/api/v1alpha1"
	v1alpha2 "github.com/emai-ai/swarm-operator/api/v1alpha2"
)

func TestConvertTo_TenantWinsOverCustomer(t *testing.T) {
	src := &v1alpha1.KaiInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "kai-acme", Namespace: "ws"},
		Spec: v1alpha1.KaiInstanceSpec{
			CustomerName: "Acme Legacy GmbH",
			CustomerSlug: "acme-legacy",
			TenantName:   "Acme GmbH",
			TenantSlug:   "acme",
			ProjectName:  "Robotik Pilot",
		},
	}
	dst := &v1alpha2.KaiInstance{}
	if err := src.ConvertTo(dst); err != nil {
		t.Fatalf("ConvertTo: %v", err)
	}
	if dst.Spec.TenantName != "Acme GmbH" {
		t.Errorf("tenantName = %q, want Acme GmbH (tenant-* wins over customer-*)", dst.Spec.TenantName)
	}
	if dst.Spec.TenantSlug != "acme" {
		t.Errorf("tenantSlug = %q, want acme", dst.Spec.TenantSlug)
	}
	if dst.Spec.ProjectName != "Robotik Pilot" {
		t.Errorf("projectName = %q", dst.Spec.ProjectName)
	}
	if dst.Name != "kai-acme" || dst.Namespace != "ws" {
		t.Errorf("ObjectMeta not preserved: %+v", dst.ObjectMeta)
	}
}

func TestConvertTo_LegacyOnlyFolds(t *testing.T) {
	// v1alpha1 manifest from swarm-emai with NO tenant-* fields — everything
	// the conversion produces must come from the customer-* legacy fields.
	src := &v1alpha1.KaiInstance{
		Spec: v1alpha1.KaiInstanceSpec{
			CustomerName: "East Side Fab e.V.",
			CustomerSlug: "east-side-fab-e-v",
			ProjectName:  "ESF intern",
		},
	}
	dst := &v1alpha2.KaiInstance{}
	if err := src.ConvertTo(dst); err != nil {
		t.Fatalf("ConvertTo: %v", err)
	}
	if dst.Spec.TenantName != "East Side Fab e.V." {
		t.Errorf("tenantName = %q, want fallback to customerName", dst.Spec.TenantName)
	}
	if dst.Spec.TenantSlug != "east-side-fab-e-v" {
		t.Errorf("tenantSlug = %q, want fallback to customerSlug", dst.Spec.TenantSlug)
	}
}

func TestConvertTo_NestedAndOptionalFields(t *testing.T) {
	tr := true
	q := resource.MustParse("512Mi")
	src := &v1alpha1.KaiInstance{
		Spec: v1alpha1.KaiInstanceSpec{
			TenantName:     "Anna",
			ProjectName:    "Side Project",
			Model:          "openrouter/x:y",
			Suspended:      true,
			ExternalAccess: &tr,
			Tier:           "starter",
			UserRef:        "u_01HXX",
			AppRef:         "writing-coach",
			Org:            "ops",
			Managed:        "saas",
			Telegram:       &v1alpha1.TelegramConfig{BotTokenSecretRef: "tg-secret"},
			GatewayAuth:    &v1alpha1.GatewayAuthConfig{Mode: "token", Token: "T0K"},
			Resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceMemory: q},
			},
		},
		Status: v1alpha1.KaiInstanceStatus{
			ObservedGeneration: 7,
			Phase:              v1alpha1.PhaseRunning,
			Ready:              true,
			GatewayURL:         "http://kai.svc/svc",
			ExternalURL:        "https://anna.example",
			CustomerSlug:       "anna",
			ConfigHash:         "deadbeef",
		},
	}
	dst := &v1alpha2.KaiInstance{}
	if err := src.ConvertTo(dst); err != nil {
		t.Fatalf("ConvertTo: %v", err)
	}
	want := v1alpha2.KaiInstanceSpec{
		TenantName:     "Anna",
		ProjectName:    "Side Project",
		Model:          "openrouter/x:y",
		Suspended:      true,
		ExternalAccess: &tr,
		Tier:           "starter",
		UserRef:        "u_01HXX",
		AppRef:         "writing-coach",
		Org:            "ops",
		Managed:        "saas",
		Telegram:       &v1alpha2.TelegramConfig{BotTokenSecretRef: "tg-secret"},
		GatewayAuth:    &v1alpha2.GatewayAuthConfig{Mode: "token", Token: "T0K"},
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceMemory: q},
		},
	}
	if !reflect.DeepEqual(dst.Spec, want) {
		t.Errorf("Spec mismatch:\n got = %+v\nwant = %+v", dst.Spec, want)
	}
	if dst.Status.Phase != v1alpha2.PhaseRunning {
		t.Errorf("phase = %q, want Running", dst.Status.Phase)
	}
	if dst.Status.TenantSlug != "anna" {
		t.Errorf("status.tenantSlug = %q, want anna (renamed from CustomerSlug)", dst.Status.TenantSlug)
	}
	if dst.Status.ObservedGeneration != 7 || dst.Status.ConfigHash != "deadbeef" {
		t.Errorf("status not preserved: %+v", dst.Status)
	}
}

func TestConvertFrom_RoundTripPopulatesBothFields(t *testing.T) {
	// v1alpha2 -> v1alpha1 must populate BOTH customerName and tenantName so
	// any v1alpha1 client (legacy controller, etc.) sees the workspace name
	// regardless of which field it reads.
	src := &v1alpha2.KaiInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "kai-anna"},
		Spec: v1alpha2.KaiInstanceSpec{
			TenantName:  "Anna",
			TenantSlug:  "anna",
			ProjectName: "P",
		},
		Status: v1alpha2.KaiInstanceStatus{TenantSlug: "anna"},
	}
	dst := &v1alpha1.KaiInstance{}
	if err := dst.ConvertFrom(src); err != nil {
		t.Fatalf("ConvertFrom: %v", err)
	}
	if dst.Spec.CustomerName != "Anna" {
		t.Errorf("customerName = %q, want Anna", dst.Spec.CustomerName)
	}
	if dst.Spec.TenantName != "Anna" {
		t.Errorf("tenantName = %q, want Anna (both fields must populate)", dst.Spec.TenantName)
	}
	if dst.Spec.CustomerSlug != "anna" || dst.Spec.TenantSlug != "anna" {
		t.Errorf("slug fields not both populated: customer=%q tenant=%q", dst.Spec.CustomerSlug, dst.Spec.TenantSlug)
	}
	if dst.Status.CustomerSlug != "anna" {
		t.Errorf("status.customerSlug = %q, want anna (renamed back from TenantSlug)", dst.Status.CustomerSlug)
	}
}

func TestConversion_RoundTrip(t *testing.T) {
	original := &v1alpha1.KaiInstance{
		Spec: v1alpha1.KaiInstanceSpec{
			TenantName:  "Anna GmbH",
			TenantSlug:  "anna",
			ProjectName: "Project",
			Tier:        "free",
			AppRef:      "personal-assistant",
		},
	}
	hub := &v1alpha2.KaiInstance{}
	if err := original.ConvertTo(hub); err != nil {
		t.Fatal(err)
	}
	back := &v1alpha1.KaiInstance{}
	if err := back.ConvertFrom(hub); err != nil {
		t.Fatal(err)
	}
	// CustomerName/CustomerSlug are populated on the round-trip even though
	// they weren't on the original — that's the deliberate "tenant-* writes
	// to both" behavior. Compare the tenant-* fields and the rest.
	if back.Spec.TenantName != original.Spec.TenantName ||
		back.Spec.TenantSlug != original.Spec.TenantSlug ||
		back.Spec.ProjectName != original.Spec.ProjectName ||
		back.Spec.Tier != original.Spec.Tier ||
		back.Spec.AppRef != original.Spec.AppRef {
		t.Errorf("round-trip mismatch:\noriginal=%+v\n     got=%+v", original.Spec, back.Spec)
	}
}

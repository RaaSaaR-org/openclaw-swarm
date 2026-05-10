/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	v1alpha2 "github.com/emai-ai/swarm-operator/api/v1alpha2"
)

// ConvertTo turns a v1alpha1 KaiInstance into the v1alpha2 hub. The legacy
// `customerName`/`customerSlug` collapse into `tenantName`/`tenantSlug` —
// when both forms are populated on the source, tenant-* wins (matches the
// EffectiveName/EffectiveSlug priority we ship in v1alpha1 today).
//
// This is the path API-server-side conversion takes when an existing
// internal-tenant manifest in `swarm-emai` lands and the operator needs to
// reconcile against the storage version (v1alpha2). Round-tripping back to
// v1alpha1 (ConvertFrom) is lossy — the customerName field becomes
// reachable only via the tenant-* names, but that's the SaaS-direction
// shape the public swarm repo wants on the wire from v1alpha2 onward.
func (src *KaiInstance) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*v1alpha2.KaiInstance)
	dst.ObjectMeta = src.ObjectMeta

	// Spec: legacy fields fold into tenant-* with precedence (TASK-024).
	dst.Spec.TenantName = pickNonEmpty(src.Spec.TenantName, src.Spec.CustomerName)
	dst.Spec.TenantSlug = pickNonEmpty(src.Spec.TenantSlug, src.Spec.CustomerSlug)
	dst.Spec.ProjectName = src.Spec.ProjectName
	dst.Spec.Model = src.Spec.Model
	dst.Spec.Suspended = src.Spec.Suspended
	dst.Spec.ExternalAccess = src.Spec.ExternalAccess
	dst.Spec.Tier = src.Spec.Tier
	dst.Spec.UserRef = src.Spec.UserRef
	dst.Spec.AppRef = src.Spec.AppRef
	dst.Spec.Org = src.Spec.Org
	dst.Spec.Managed = src.Spec.Managed
	dst.Spec.Resources = src.Spec.Resources

	if src.Spec.Telegram != nil {
		dst.Spec.Telegram = &v1alpha2.TelegramConfig{
			BotTokenSecretRef: src.Spec.Telegram.BotTokenSecretRef,
		}
	}
	if src.Spec.GatewayAuth != nil {
		dst.Spec.GatewayAuth = &v1alpha2.GatewayAuthConfig{
			Mode:  src.Spec.GatewayAuth.Mode,
			Token: src.Spec.GatewayAuth.Token,
		}
	}

	// Status: same field-rename, otherwise direct.
	dst.Status.ObservedGeneration = src.Status.ObservedGeneration
	dst.Status.Phase = v1alpha2.KaiInstancePhase(src.Status.Phase)
	dst.Status.Ready = src.Status.Ready
	dst.Status.GatewayURL = src.Status.GatewayURL
	dst.Status.ExternalURL = src.Status.ExternalURL
	dst.Status.TenantSlug = src.Status.CustomerSlug
	dst.Status.ConfigHash = src.Status.ConfigHash
	dst.Status.Conditions = src.Status.Conditions
	return nil
}

// ConvertFrom is the inverse of ConvertTo. v1alpha2 -> v1alpha1 maps
// `tenantName` back into BOTH `customerName` (the v1alpha1-required field)
// and `tenantName` so a roundtrip preserves the value either way an old
// client looks at it. Same for slug.
func (dst *KaiInstance) ConvertFrom(srcRaw conversion.Hub) error {
	src := srcRaw.(*v1alpha2.KaiInstance)
	dst.ObjectMeta = src.ObjectMeta

	dst.Spec.CustomerName = src.Spec.TenantName
	dst.Spec.TenantName = src.Spec.TenantName
	dst.Spec.CustomerSlug = src.Spec.TenantSlug
	dst.Spec.TenantSlug = src.Spec.TenantSlug
	dst.Spec.ProjectName = src.Spec.ProjectName
	dst.Spec.Model = src.Spec.Model
	dst.Spec.Suspended = src.Spec.Suspended
	dst.Spec.ExternalAccess = src.Spec.ExternalAccess
	dst.Spec.Tier = src.Spec.Tier
	dst.Spec.UserRef = src.Spec.UserRef
	dst.Spec.AppRef = src.Spec.AppRef
	dst.Spec.Org = src.Spec.Org
	dst.Spec.Managed = src.Spec.Managed
	dst.Spec.Resources = src.Spec.Resources

	if src.Spec.Telegram != nil {
		dst.Spec.Telegram = &TelegramConfig{
			BotTokenSecretRef: src.Spec.Telegram.BotTokenSecretRef,
		}
	}
	if src.Spec.GatewayAuth != nil {
		dst.Spec.GatewayAuth = &GatewayAuthConfig{
			Mode:  src.Spec.GatewayAuth.Mode,
			Token: src.Spec.GatewayAuth.Token,
		}
	}

	dst.Status.ObservedGeneration = src.Status.ObservedGeneration
	dst.Status.Phase = KaiInstancePhase(src.Status.Phase)
	dst.Status.Ready = src.Status.Ready
	dst.Status.GatewayURL = src.Status.GatewayURL
	dst.Status.ExternalURL = src.Status.ExternalURL
	dst.Status.CustomerSlug = src.Status.TenantSlug
	dst.Status.ConfigHash = src.Status.ConfigHash
	dst.Status.Conditions = src.Status.Conditions
	return nil
}

// pickNonEmpty is a tiny precedence helper. Kept private to keep the
// EffectiveName/EffectiveSlug semantics out of public API surface.
func pickNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

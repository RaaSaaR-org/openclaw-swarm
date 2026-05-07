/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	swarmv1alpha1 "github.com/emai-ai/swarm-operator/api/v1alpha1"
	"github.com/emai-ai/swarm/pkg/quotas"
)

// isSaaSEnrolled reports whether the operator should apply the SaaS quota
// envelope (pkg/quotas) to this KaiInstance. A workspace is enrolled iff:
//
//   - it has spec.tier set (the explicit "I'm a SaaS workspace" signal), AND
//   - it is NOT managed: internal (PROP-003 coexistence rule — internal
//     EmAI tenants are sized by hand and exempt from the SaaS envelope).
//
// Anything else — legacy tenants from before TASK-012/015 with no tier set,
// or explicitly internal-managed tenants — keeps the original 1Gi/2Gi
// defaults so swarm-emai workspaces don't get silently
// throttled by a feature they never opted into.
func isSaaSEnrolled(kai *swarmv1alpha1.KaiInstance) bool {
	if kai == nil {
		return false
	}
	if kai.Spec.Managed == "internal" {
		return false
	}
	return kai.Spec.Tier != ""
}

const (
	agentImage   = "ghcr.io/openclaw/openclaw:latest"
	gatewayPort  = 18789
	defaultModel = "openrouter/stepfun/step-3.5-flash:free"
	secretName   = "swarm-secrets"
)

// commonLabels returns the standard labels for a KaiInstance's child resources.
//
// The two label groups (`emai.io/*` and `swarm.io/*`) coexist intentionally
// during the TASK-024 rename: existing selectors (NetworkPolicy podSelector,
// ad-hoc kubectl filters in swarm-emai) still match the legacy
// labels, while new tooling can already select on the generic
// `swarm.io/tenant=<slug>` label that's not tied to EmAI's domain. The legacy
// labels are dropped together with the v1alpha1→v1alpha2 CRD bump (TASK-012).
func commonLabels(slug string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "kai-" + slug,
		"app.kubernetes.io/part-of":    "emai-swarm",
		"app.kubernetes.io/managed-by": "swarm-operator",
		"emai.io/component":            "agent",
		"emai.io/role":                 "customer",
		"emai.io/customer":             slug,
		"swarm.io/tenant":              slug,
	}
}

// commonLabelsFor extends commonLabels with the SaaS-direction labels
// derived from optional KaiInstance.Spec fields (tier, userRef, org, appRef,
// managed — added by TASK-012 Phase 2.A). Missing fields skip cleanly so
// existing tenants (which don't set them) keep their existing label set.
func commonLabelsFor(kai *swarmv1alpha1.KaiInstance, slug string) map[string]string {
	l := commonLabels(slug)
	if kai == nil {
		return l
	}
	if kai.Spec.UserRef != "" {
		l["swarm.io/user-id"] = kai.Spec.UserRef
	}
	if kai.Spec.Tier != "" {
		l["swarm.io/tier"] = kai.Spec.Tier
	}
	if kai.Spec.AppRef != "" {
		l["swarm.io/app"] = kai.Spec.AppRef
	}
	if kai.Spec.Org != "" {
		l["swarm.io/org"] = kai.Spec.Org
	}
	if kai.Spec.Managed != "" {
		l["swarm.io/managed"] = kai.Spec.Managed
	}
	return l
}

// childName returns the name for a child resource.
func childName(slug string) string {
	return "kai-" + slug
}

// buildConfigMap creates the identity ConfigMap for a KaiInstance.
func buildConfigMap(kai *swarmv1alpha1.KaiInstance, slug string, tmpl *renderedTemplates) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      childName(slug) + "-identity",
			Namespace: kai.Namespace,
			Labels:    commonLabelsFor(kai, slug),
		},
		Data: map[string]string{
			"SOUL.md":       tmpl.SoulMD,
			"AGENTS.md":     tmpl.AgentsMD,
			"TOOLS.md":      tmpl.ToolsMD,
			"HEARTBEAT.md":  tmpl.HeartbeatMD,
			"openclaw.json": tmpl.OpenClawJSON,
			"SKILL-mc.md":   tmpl.SkillMC,
		},
	}
}

// buildPVC creates the persistent volume claim for agent state.
func buildPVC(kai *swarmv1alpha1.KaiInstance, slug string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      childName(slug) + "-state",
			Namespace: kai.Namespace,
			Labels:    commonLabelsFor(kai, slug),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
	}
}

// deploymentOpts carries reconciler-level configuration that influences how a
// KaiInstance Deployment is rendered. Kept separate from KaiInstance.Spec so
// it stays a deploy-time choice (env var on the operator) rather than a
// per-tenant CRD field.
type deploymentOpts struct {
	// PooledOpenRouterSecret, when non-empty, overrides the default per-tenant
	// `kai-<slug>-openrouter` Secret with a single shared Secret in the same
	// namespace. Empty preserves the legacy per-tenant wiring.
	PooledOpenRouterSecret string
}

// buildDeployment creates the Deployment for the Kai agent.
func buildDeployment(kai *swarmv1alpha1.KaiInstance, slug, hash string, opts deploymentOpts) *appsv1.Deployment {
	labels := commonLabelsFor(kai, slug)
	name := childName(slug)

	replicas := int32(1)
	if kai.Spec.Suspended {
		replicas = 0
	}

	// Model resolution (TASK-019 Phase 1):
	//   1. spec.Model (explicit override) wins.
	//   2. SaaS-enrolled tenants (spec.tier set, not managed:internal) fall
	//      back to the tier's DefaultModel from pkg/quotas — free → free
	//      OpenRouter model, paid → Haiku.
	//   3. Legacy tenants (no tier set or managed:internal) fall back to
	//      the operator's hard-coded `defaultModel`, preserving current
	//      behavior for existing internal workspaces in swarm-emai.
	model := defaultModel
	if isSaaSEnrolled(kai) {
		if tierDefault := quotas.For(quotas.Tier(kai.Spec.Tier)).DefaultModel; tierDefault != "" {
			model = tierDefault
		}
	}
	if kai.Spec.Model != "" {
		model = kai.Spec.Model
	}

	// OpenRouter key source: pooled (one Secret for all tenants) when configured
	// via SWARM_POOLED_OPENROUTER_SECRET; otherwise fall back to the per-tenant
	// `kai-<slug>-openrouter` Secret that onboard.sh historically created.
	openRouterSecret := opts.PooledOpenRouterSecret
	if openRouterSecret == "" {
		openRouterSecret = fmt.Sprintf("kai-%s-openrouter", slug)
	}
	env := []corev1.EnvVar{
		{Name: "NODE_OPTIONS", Value: "--max-old-space-size=1536"},
		{Name: "OPENCLAW_AGENT", Value: name},
		{Name: "OPENCLAW_PROVIDER", Value: "openrouter"},
		{Name: "OPENCLAW_MODEL", Value: model},
		{
			Name: "OPENROUTER_API_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: openRouterSecret},
					Key:                  "api-key",
				},
			},
		},
	}

	// Add Telegram bot token if configured
	if kai.Spec.Telegram != nil && kai.Spec.Telegram.BotTokenSecretRef != "" {
		env = append(env, corev1.EnvVar{
			Name: "TELEGRAM_BOT_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: kai.Spec.Telegram.BotTokenSecretRef},
					Key:                  "bot-token",
				},
			},
		})
	}

	// Resource requirements. Two paths:
	//   - SaaS-enrolled tenants (spec.tier set, not managed: internal):
	//     pkg/quotas clamps spec.resources to the tier ceiling and supplies
	//     tier-appropriate defaults for missing fields.
	//   - Legacy tenants (no tier, or managed: internal): keep the original
	//     1Gi/2Gi defaults so existing internal workspaces in
	//     swarm-emai don't suddenly get throttled by the SaaS
	//     quota envelope. Hand-sized via spec.resources as today.
	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("1Gi"),
			corev1.ResourceCPU:    resource.MustParse("100m"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("2Gi"),
			corev1.ResourceCPU:    resource.MustParse("500m"),
		},
	}
	if kai.Spec.Resources != nil {
		resources = *kai.Spec.Resources
	}
	if isSaaSEnrolled(kai) {
		resources = quotas.ClampResources(&resources, quotas.Tier(kai.Spec.Tier))
	}

	configMapName := name + "-identity"
	automount := false

	// Init container copies ConfigMap files into the PVC so the workspace is writable
	// (OpenClaw needs to create USER.md, MEMORY.md at runtime).
	// Identity files are copied only on first boot (preserves custom edits from
	// onboard.sh or manual overrides). The skill bundle is generic and refreshed
	// every boot. openclaw.json is gated on EXPECTED_HASH: copying it on every
	// restart was clobbering OpenClaw's schema-migrated state and producing a
	// trail of `openclaw.json.clobbered.<timestamp>` files in the PVC.
	initScript := `set -e
mkdir -p /state/workspace /state/workspace/skills/mc /state/workspace/memory
[ -f /state/workspace/SOUL.md ] || cp /identity/SOUL.md /state/workspace/SOUL.md
[ -f /state/workspace/AGENTS.md ] || cp /identity/AGENTS.md /state/workspace/AGENTS.md
[ -f /state/workspace/TOOLS.md ] || cp /identity/TOOLS.md /state/workspace/TOOLS.md
[ -f /state/workspace/HEARTBEAT.md ] || cp /identity/HEARTBEAT.md /state/workspace/HEARTBEAT.md
cp /identity/SKILL-mc.md /state/workspace/skills/mc/SKILL.md
applied=""
[ -f /state/.config-hash ] && applied=$(cat /state/.config-hash)
if [ "$applied" != "$EXPECTED_HASH" ] || [ ! -f /state/openclaw.json ]; then
  cp /identity/openclaw.json /state/openclaw.json
  printf %s "$EXPECTED_HASH" > /state/.config-hash
fi
chown -R 1000:1000 /state`

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: kai.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name": "kai-" + slug,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
					Annotations: map[string]string{
						"emai.io/config-hash": hash,
					},
				},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: &automount,
					InitContainers: []corev1.Container{
						{
							Name:    "copy-identity",
							Image:   "busybox:latest",
							Command: []string{"sh", "-c", initScript},
							Env: []corev1.EnvVar{
								{Name: "EXPECTED_HASH", Value: hash},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "state", MountPath: "/state"},
								{Name: "identity-files", MountPath: "/identity"},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:      "agent",
							Image:     agentImage,
							Env:       env,
							Resources: resources,
							Ports: []corev1.ContainerPort{
								{
									Name:          "gateway",
									ContainerPort: gatewayPort,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							// Probes are generous because the OpenClaw 2026.4.x prep
							// pipeline (model-resolution + auth + core-plugin-tools)
							// can block the Node event loop for 15-30s on slower CPUs
							// (Hetzner ARM CAX21), causing healthz to time out even
							// when the gateway is technically ready.
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt(gatewayPort),
									},
								},
								InitialDelaySeconds: 120,
								PeriodSeconds:       60,
								TimeoutSeconds:      30,
								FailureThreshold:    5,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt(gatewayPort),
									},
								},
								InitialDelaySeconds: 60,
								PeriodSeconds:       30,
								TimeoutSeconds:      30,
								FailureThreshold:    5,
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "state", MountPath: "/home/node/.openclaw"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "state",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: name + "-state",
								},
							},
						},
						{
							Name: "identity-files",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
								},
							},
						},
					},
				},
			},
		},
	}
}

// buildService creates the ClusterIP Service for the Kai agent gateway.
func buildService(kai *swarmv1alpha1.KaiInstance, slug string) *corev1.Service {
	name := childName(slug)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: kai.Namespace,
			Labels:    commonLabelsFor(kai, slug),
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"app.kubernetes.io/name": "kai-" + slug,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "gateway",
					Port:       gatewayPort,
					TargetPort: intstr.FromInt(gatewayPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// buildNetworkPolicy creates the isolation NetworkPolicy for a customer agent.
func buildNetworkPolicy(kai *swarmv1alpha1.KaiInstance, slug string) *networkingv1.NetworkPolicy {
	name := childName(slug)
	protocol := corev1.ProtocolTCP
	protocolUDP := corev1.ProtocolUDP
	dnsPort := intstr.FromInt(53)
	httpsPort := intstr.FromInt(443)

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-isolation",
			Namespace: kai.Namespace,
			Labels:    commonLabelsFor(kai, slug),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"emai.io/customer": slug,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					// Only allow ingress from Kira (central role)
					From: []networkingv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"emai.io/role": "central",
								},
							},
						},
					},
				},
			},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					// DNS
					Ports: []networkingv1.NetworkPolicyPort{
						{Protocol: &protocolUDP, Port: &dnsPort},
						{Protocol: &protocol, Port: &dnsPort},
					},
				},
				{
					// HTTPS (for OpenRouter API, Telegram API)
					Ports: []networkingv1.NetworkPolicyPort{
						{Protocol: &protocol, Port: &httpsPort},
					},
				},
			},
		},
	}
}

// gatewayURL returns the in-cluster gateway URL for a KaiInstance.
func gatewayURL(namespace, slug string) string {
	return fmt.Sprintf("%s.%s.svc:%d", childName(slug), namespace, gatewayPort)
}

// ingressOpts carries reconciler-level config that affects Ingress shape
// (TASK-017 Phase 1). PerSlugSubdomain flips between two URL shapes:
//
//   - false (default, legacy): host=<domain>, path=/ws/<slug>. Existing
//     tenants and chat-bridge clients keep their current URLs.
//   - true: host=<slug>.<domain>, path=/ws. Each tenant gets its own
//     subdomain — covered by the wildcard cert from TASK-017 Phase 0.
//     Wildcard DNS A-record covers the host without per-slug DNS work.
type ingressOpts struct {
	PerSlugSubdomain bool
}

// externalURL returns the public URL for a KaiInstance, matching the
// Ingress shape selected by PerSlugSubdomain.
func externalURL(domain, slug string, opts ingressOpts) string {
	if opts.PerSlugSubdomain {
		return fmt.Sprintf("https://%s.%s/ws", slug, domain)
	}
	return fmt.Sprintf("https://%s/ws/%s", domain, slug)
}

// buildIngress creates the Traefik Ingress for external WebSocket access to a Kai agent.
func buildIngress(kai *swarmv1alpha1.KaiInstance, slug, domain, tlsSecret string, opts ingressOpts) *networkingv1.Ingress {
	name := childName(slug)
	pathType := networkingv1.PathTypePrefix
	ingressClass := "traefik"

	host := domain
	path := "/ws/" + slug
	if opts.PerSlugSubdomain {
		host = slug + "." + domain
		path = "/ws"
	}

	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-ws",
			Namespace: kai.Namespace,
			Labels:    commonLabelsFor(kai, slug),
			Annotations: map[string]string{
				"traefik.ingress.kubernetes.io/router.entrypoints": "web,websecure",
				"traefik.ingress.kubernetes.io/router.tls":         "true",
				"cert-manager.io/cluster-issuer":                   "letsencrypt-prod",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &ingressClass,
			TLS: []networkingv1.IngressTLS{
				{
					Hosts:      []string{host},
					SecretName: tlsSecret,
				},
			},
			Rules: []networkingv1.IngressRule{
				{
					Host: host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     path,
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: name,
											Port: networkingv1.ServiceBackendPort{
												Number: gatewayPort,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

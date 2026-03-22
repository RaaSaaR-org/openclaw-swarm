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
)

const (
	agentImage   = "ghcr.io/openclaw/openclaw:latest"
	gatewayPort  = 18789
	defaultModel = "openrouter/stepfun/step-3.5-flash:free"
	secretName   = "swarm-secrets"
)

// commonLabels returns the standard labels for a KaiInstance's child resources.
func commonLabels(slug string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "kai-" + slug,
		"app.kubernetes.io/part-of":    "emai-swarm",
		"app.kubernetes.io/managed-by": "swarm-operator",
		"emai.io/component":            "agent",
		"emai.io/role":                 "customer",
		"emai.io/customer":             slug,
	}
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
			Labels:    commonLabels(slug),
		},
		Data: map[string]string{
			"SOUL.md":       tmpl.SoulMD,
			"HEARTBEAT.md":  tmpl.HeartbeatMD,
			"openclaw.json": tmpl.OpenClawJSON,
		},
	}
}

// buildPVC creates the persistent volume claim for agent state.
func buildPVC(kai *swarmv1alpha1.KaiInstance, slug string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      childName(slug) + "-state",
			Namespace: kai.Namespace,
			Labels:    commonLabels(slug),
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

// buildDeployment creates the Deployment for the Kai agent.
func buildDeployment(kai *swarmv1alpha1.KaiInstance, slug, hash string) *appsv1.Deployment {
	labels := commonLabels(slug)
	name := childName(slug)

	replicas := int32(1)
	if kai.Spec.Suspended {
		replicas = 0
	}

	model := defaultModel
	if kai.Spec.Model != "" {
		model = kai.Spec.Model
	}

	// Container env vars
	env := []corev1.EnvVar{
		{Name: "NODE_OPTIONS", Value: "--max-old-space-size=1536"},
		{Name: "OPENCLAW_AGENT", Value: name},
		{Name: "OPENCLAW_PROVIDER", Value: "openrouter"},
		{Name: "OPENCLAW_MODEL", Value: model},
		{
			Name: "OPENROUTER_API_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
					Key:                  "openrouter-api-key",
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

	// Resource requirements
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

	configMapName := name + "-identity"
	automount := false

	// Init container copies ConfigMap files into the PVC so the workspace is writable
	// (OpenClaw needs to create USER.md, MEMORY.md at runtime)
	initScript := `mkdir -p /state/workspace && cp /identity/SOUL.md /state/workspace/SOUL.md && cp /identity/HEARTBEAT.md /state/workspace/HEARTBEAT.md && cp /identity/openclaw.json /state/openclaw.json && chown -R 1000:1000 /state`

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
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt(gatewayPort),
									},
								},
								InitialDelaySeconds: 60,
								PeriodSeconds:       30,
								TimeoutSeconds:      5,
								FailureThreshold:    3,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt(gatewayPort),
									},
								},
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
								TimeoutSeconds:      5,
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
			Labels:    commonLabels(slug),
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
			Labels:    commonLabels(slug),
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

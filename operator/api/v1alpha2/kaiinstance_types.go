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

package v1alpha2

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KaiInstanceSpec is the v1alpha2 desired-state shape. Compared to v1alpha1,
// the legacy `customerName`/`customerSlug` fields are gone — only the
// SaaS-direction `tenantName`/`tenantSlug` survive. Other fields keep their
// names and semantics. The conversion webhook fills `tenantName` from
// `customerName` when an old v1alpha1 manifest lands.
type KaiInstanceSpec struct {
	// tenantName is the display name of the workspace's owning entity
	// (e.g. "Acme GmbH"). Required for v1alpha2 — the conversion webhook
	// guarantees this from a v1alpha1 manifest's customerName/tenantName.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	TenantName string `json:"tenantName"`

	// projectName is the project context for the agent.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=200
	ProjectName string `json:"projectName"`

	// tenantSlug is the DNS-safe identifier, auto-derived from tenantName
	// if empty. Once set, it becomes immutable.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	// +optional
	TenantSlug string `json:"tenantSlug,omitempty"`

	// model overrides the default LLM model.
	// +optional
	Model string `json:"model,omitempty"`

	// providers is the list of LLM providers wired into the agent pod
	// (TASK-027). Each entry mounts the API key from a Secret as an
	// `<NAME>_API_KEY` env var (e.g. `OPENROUTER_API_KEY`, `NVIDIA_API_KEY`).
	// OpenClaw auto-enables a provider when its key env var is present.
	//
	// When this list is empty, the operator keeps the legacy single-provider
	// behavior: one openrouter key from `kai-<slug>-openrouter` Secret. New
	// deployments should use this list explicitly so multi-provider setups
	// (primary NVIDIA + fallback OpenRouter, etc.) work without operator
	// patches.
	// +optional
	Providers []ProviderConfig `json:"providers,omitempty"`

	// telegram configures the agent's Telegram bot integration.
	// +optional
	Telegram *TelegramConfig `json:"telegram,omitempty"`

	// gatewayAuth configures the gateway authentication mode.
	// +optional
	GatewayAuth *GatewayAuthConfig `json:"gatewayAuth,omitempty"`

	// resources overrides the default resource requirements for the agent container.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// suspended stops the agent without deleting state. The Deployment is scaled to 0.
	// +optional
	Suspended bool `json:"suspended,omitempty"`

	// externalAccess controls whether an Ingress is created for external access.
	// Defaults to true when omitted.
	// +optional
	ExternalAccess *bool `json:"externalAccess,omitempty"`

	// tier is the SaaS plan this workspace belongs to.
	// +kubebuilder:validation:Enum=free;starter;growth;enterprise
	// +optional
	Tier string `json:"tier,omitempty"`

	// userRef is the Postgres `users.id` (u_<ulid>) of the workspace owner.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	UserRef string `json:"userRef,omitempty"`

	// appRef points at a curated persona under `agents/catalog/<slug>`.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	// +optional
	AppRef string `json:"appRef,omitempty"`

	// org is a free-form cost-center / billing-group label.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	Org string `json:"org,omitempty"`

	// managed selects the deployment mode: `saas` workspaces have a User
	// above them and participate in billing/quota webhooks; `internal`
	// workspaces are system-owned EmAI tenants.
	// +kubebuilder:validation:Enum=saas;internal
	// +optional
	Managed string `json:"managed,omitempty"`
}

// TelegramConfig holds Telegram bot integration settings.
type TelegramConfig struct {
	// botTokenSecretRef is the name of a Secret containing key "bot-token".
	BotTokenSecretRef string `json:"botTokenSecretRef"`
}

// ProviderConfig wires one LLM provider's API key into the agent pod
// (TASK-027). The operator renders one env var per entry —
// `<NAME>_API_KEY`, e.g. `OPENROUTER_API_KEY`, `NVIDIA_API_KEY` — sourced
// from the named Secret. OpenClaw activates a provider when its API-key
// env var is present, so this is the single seam for multi-provider
// setups (primary NVIDIA + fallback OpenRouter, etc.).
type ProviderConfig struct {
	// name is the provider identifier OpenClaw recognizes. Lower-case ASCII
	// (e.g. "openrouter", "nvidia", "together", "cerebras", "groq", "openai",
	// "anthropic"). The operator UPPERCASES it to build the env var name.
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9]*$`
	// +kubebuilder:validation:MaxLength=32
	Name string `json:"name"`

	// apiKeySecretRef points at a Secret holding the provider's API key.
	APIKeySecretRef ProviderAPIKeySecretRef `json:"apiKeySecretRef"`
}

// ProviderAPIKeySecretRef is the Secret + key the operator reads to mount
// a provider's API key. Mirrors the standard Kubernetes SecretKeySelector
// shape but kept narrow (no Optional flag) — a referenced Secret missing
// at provision time should be a hard failure, not a silent skip.
type ProviderAPIKeySecretRef struct {
	// name is the Secret name in the same namespace as the KaiInstance.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// key inside the Secret. Defaults to "api-key" when empty (matches
	// the convention in `kai-<slug>-openrouter` legacy Secrets).
	// +kubebuilder:validation:MaxLength=253
	// +optional
	Key string `json:"key,omitempty"`
}

// GatewayAuthConfig holds gateway authentication settings.
type GatewayAuthConfig struct {
	// mode is the gateway auth mode: "none" or "token".
	// +kubebuilder:validation:Enum=none;token
	Mode string `json:"mode"`

	// token is the shared auth token (only used when mode=token).
	// +optional
	Token string `json:"token,omitempty"`
}

// KaiInstancePhase represents the lifecycle phase of a KaiInstance.
// +kubebuilder:validation:Enum=Provisioning;Running;Suspended;Failed
type KaiInstancePhase string

const (
	PhaseProvisioning KaiInstancePhase = "Provisioning"
	PhaseRunning      KaiInstancePhase = "Running"
	PhaseSuspended    KaiInstancePhase = "Suspended"
	PhaseFailed       KaiInstancePhase = "Failed"
)

// Condition types for KaiInstance.
const (
	ConditionConfigMapReady       = "ConfigMapReady"
	ConditionDeploymentAvailable  = "DeploymentAvailable"
	ConditionNetworkPolicyApplied = "NetworkPolicyApplied"
	ConditionPVCBound             = "PVCBound"
	ConditionIngressReady         = "IngressReady"
)

// KaiInstanceFinalizer reserves a pre-delete hook on the KaiInstance object
// so the operator gets a chance to run cleanup before garbage collection.
const KaiInstanceFinalizer = "swarm.emai.io/finalizer"

// KaiInstanceStatus defines the observed state of KaiInstance.
type KaiInstanceStatus struct {
	// observedGeneration reflects the metadata.generation that the operator
	// last successfully reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// phase is the current lifecycle phase.
	// +optional
	Phase KaiInstancePhase `json:"phase,omitempty"`

	// ready indicates whether the agent is fully operational.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// gatewayURL is the in-cluster URL for the agent's gateway.
	// +optional
	GatewayURL string `json:"gatewayURL,omitempty"`

	// externalURL is the public URL for the agent's gateway.
	// +optional
	ExternalURL string `json:"externalURL,omitempty"`

	// tenantSlug is the resolved slug (derived from spec or auto-generated).
	// +optional
	TenantSlug string `json:"tenantSlug,omitempty"`

	// configHash is the SHA256 hash of the rendered config, used to detect drift.
	// +optional
	ConfigHash string `json:"configHash,omitempty"`

	// conditions represent the current state of the KaiInstance resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Gateway",type=string,JSONPath=`.status.gatewayURL`
// +kubebuilder:printcolumn:name="External",type=string,JSONPath=`.status.externalURL`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KaiInstance is the Schema for the kaiinstances API at v1alpha2 (storage).
type KaiInstance struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of KaiInstance
	// +required
	Spec KaiInstanceSpec `json:"spec"`

	// status defines the observed state of KaiInstance
	// +optional
	Status KaiInstanceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// KaiInstanceList contains a list of KaiInstance
type KaiInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []KaiInstance `json:"items"`
}

// Hub marks v1alpha2 as the hub version for conversion-webhook purposes.
// All v1alphaN spokes (today only v1alpha1) implement Convertible against
// this hub. controller-runtime calls Hub() on the storage type to identify
// the conversion target.
func (*KaiInstance) Hub() {}

func init() {
	SchemeBuilder.Register(&KaiInstance{}, &KaiInstanceList{})
}

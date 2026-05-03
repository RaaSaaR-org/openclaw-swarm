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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KaiInstanceSpec defines the desired state of a customer Kai agent instance.
type KaiInstanceSpec struct {
	// customerName is the display name of the workspace's owning entity
	// (e.g. "Acme GmbH"). Legacy field name; the SaaS-direction
	// alternative is `tenantName` below — when both are set, `tenantName`
	// wins. The two coexist on v1alpha1 so existing internal-tenant
	// manifests in swarm-emai/swarm-config keep working while new code
	// migrates to the tenant-* names. Both retire in v1alpha2 + a
	// conversion webhook (TASK-012 Phase 2.B + TASK-024 Phase 5).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	CustomerName string `json:"customerName"`

	// projectName is the project context for the agent.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=200
	ProjectName string `json:"projectName"`

	// customerSlug is the DNS-safe identifier, auto-derived from customerName if empty.
	// Once set, it becomes immutable. Legacy field name; the SaaS-direction
	// alternative is `tenantSlug` — when both are set, `tenantSlug` wins.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	// +optional
	CustomerSlug string `json:"customerSlug,omitempty"`

	// tenantName is the SaaS-direction display name for the workspace's
	// owning entity. When set, it takes precedence over `customerName`.
	// Both fields live side-by-side on v1alpha1 so the public swarm repo
	// can be tenant-clean (TASK-024) while existing swarm-emai/swarm-config
	// manifests keep working unchanged. The conversion webhook in v1alpha2
	// (TASK-012 Phase 2.B) drops `customerName` entirely.
	// +kubebuilder:validation:MaxLength=100
	// +optional
	TenantName string `json:"tenantName,omitempty"`

	// tenantSlug is the SaaS-direction DNS-safe identifier. When set, it
	// takes precedence over `customerSlug`. Same coexistence story as
	// `tenantName` above.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	// +optional
	TenantSlug string `json:"tenantSlug,omitempty"`

	// model overrides the default LLM model.
	// +optional
	Model string `json:"model,omitempty"`

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

	// tier is the SaaS plan this workspace belongs to. Drives default model
	// selection and per-tenant quotas (TASK-019). Empty defaults to "free".
	// +kubebuilder:validation:Enum=free;starter;growth;enterprise
	// +optional
	Tier string `json:"tier,omitempty"`

	// userRef is the Postgres `users.id` (u_<ulid>) of the workspace owner.
	// Required for `managed: saas`; null/empty for `managed: internal`
	// (system-owned EmAI tenants — see PROP-003 coexistence rule). When set,
	// the operator labels every child resource with `swarm.io/user-id=<ref>`
	// so the dashboard's "your workspaces" view (TASK-014 Phase 3) can do a
	// label-selector list.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	UserRef string `json:"userRef,omitempty"`

	// appRef points at a curated persona under `agents/catalog/<slug>` (see
	// TASK-018). Empty falls back to the legacy customer-template renderer.
	// The operator's catalog renderer (TASK-018 Phase 1, future) reads this
	// to pick which SOUL.md to embed in the per-tenant ConfigMap.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	// +optional
	AppRef string `json:"appRef,omitempty"`

	// org is a free-form cost-center / billing-group label. Useful for
	// multi-workspace customers that want consolidated billing or for
	// internal EmAI tenants that want grouping. No semantic enforcement —
	// it lands as the `swarm.io/org=<value>` label on child resources.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	Org string `json:"org,omitempty"`

	// managed selects the deployment mode (PROP-003): `saas` workspaces
	// have a User above them and participate in billing/quota webhooks;
	// `internal` workspaces are system-owned EmAI tenants and skip those
	// flows. Empty defaults to `saas` (the SaaS-first default); explicit
	// `internal` is required for system-owned tenants in `swarm-emai`.
	// +kubebuilder:validation:Enum=saas;internal
	// +optional
	Managed string `json:"managed,omitempty"`
}

// TelegramConfig holds Telegram bot integration settings.
type TelegramConfig struct {
	// botTokenSecretRef is the name of a Secret containing key "bot-token".
	BotTokenSecretRef string `json:"botTokenSecretRef"`
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

// EffectiveName returns the workspace's display name, preferring the
// SaaS-direction `tenantName` when set and falling back to the legacy
// `customerName` otherwise. Use this everywhere the operator reads the
// "what to call this workspace" string — never read CustomerName / TenantName
// directly. Mirror logic with EffectiveSlug below. (TASK-024 Phase 2)
func (s KaiInstanceSpec) EffectiveName() string {
	if s.TenantName != "" {
		return s.TenantName
	}
	return s.CustomerName
}

// EffectiveSlug returns the workspace's slug, preferring the
// SaaS-direction `tenantSlug` when set and falling back to the legacy
// `customerSlug` otherwise. Empty when neither is set — caller derives
// the slug from the (effective) name in that case.
func (s KaiInstanceSpec) EffectiveSlug() string {
	if s.TenantSlug != "" {
		return s.TenantSlug
	}
	return s.CustomerSlug
}

// KaiInstanceFinalizer reserves a pre-delete hook on the KaiInstance object so
// the operator gets a chance to run cleanup before garbage collection. Today
// the cleanup is a no-op — child resources cascade via ownerReferences — but
// the hook is in place for future SaaS pre-delete needs (GDPR DSAR snapshot,
// billing-on-delete, audit log entry).
const KaiInstanceFinalizer = "swarm.emai.io/finalizer"

// KaiInstanceStatus defines the observed state of KaiInstance.
type KaiInstanceStatus struct {
	// observedGeneration reflects the metadata.generation that the operator last
	// successfully reconciled. observedGeneration < generation means the operator
	// has not yet processed the latest spec change.
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

	// externalURL is the public URL for the agent's gateway (set when Ingress is created).
	// +optional
	ExternalURL string `json:"externalURL,omitempty"`

	// customerSlug is the resolved slug (derived from spec or auto-generated).
	// +optional
	CustomerSlug string `json:"customerSlug,omitempty"`

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
// +kubebuilder:printcolumn:name="Customer",type=string,JSONPath=`.spec.customerName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Gateway",type=string,JSONPath=`.status.gatewayURL`
// +kubebuilder:printcolumn:name="External",type=string,JSONPath=`.status.externalURL`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KaiInstance is the Schema for the kaiinstances API.
// It represents a customer Kai agent instance in the EmAI Swarm.
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

func init() {
	SchemeBuilder.Register(&KaiInstance{}, &KaiInstanceList{})
}

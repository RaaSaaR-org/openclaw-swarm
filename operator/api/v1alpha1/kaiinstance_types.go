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
	// customerName is the display name of the customer (e.g. "Acme GmbH").
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	CustomerName string `json:"customerName"`

	// projectName is the project context for the agent.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=200
	ProjectName string `json:"projectName"`

	// customerSlug is the DNS-safe identifier, auto-derived from customerName if empty.
	// Once set, it becomes immutable.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	// +optional
	CustomerSlug string `json:"customerSlug,omitempty"`

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
)

// KaiInstanceStatus defines the observed state of KaiInstance.
type KaiInstanceStatus struct {
	// phase is the current lifecycle phase.
	// +optional
	Phase KaiInstancePhase `json:"phase,omitempty"`

	// ready indicates whether the agent is fully operational.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// gatewayURL is the in-cluster URL for the agent's gateway.
	// +optional
	GatewayURL string `json:"gatewayURL,omitempty"`

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

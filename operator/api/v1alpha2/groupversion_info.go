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

// Package v1alpha2 contains API Schema definitions for the swarm v1alpha2 API group.
// v1alpha2 is the SaaS-direction shape (TASK-012 Phase 2.B + TASK-024): the
// legacy `customerName`/`customerSlug` fields are dropped here, replaced by
// `tenantName`/`tenantSlug` (which already coexisted on v1alpha1). v1alpha2 is
// the storage version; v1alpha1 manifests are converted by the operator's
// conversion webhook so existing internal-tenant manifests in `swarm-emai`
// keep applying unchanged.
//
// The API group stays `swarm.emai.io` for now — the group rename to
// `swarm.io` is a separate operational migration (would require recreating
// the CRD) and is deferred to a follow-up release.
// +kubebuilder:object:generate=true
// +groupName=swarm.emai.io
package v1alpha2

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "swarm.emai.io", Version: "v1alpha2"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

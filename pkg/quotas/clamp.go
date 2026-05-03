package quotas

import (
	"k8s.io/apimachinery/pkg/api/resource"
	corev1 "k8s.io/api/core/v1"
)

// ClampResources returns a ResourceRequirements with each request/limit
// clamped to the tier's ceiling. Lower values pass through unchanged — the
// tier defines a maximum, not a minimum. nil/empty fields in the input fall
// back to the tier's preferred values so a tenant that doesn't set
// `spec.resources` gets the tier-appropriate sizing.
//
// Empty / "0" tier fields skip the clamp: enterprise (all fields zero) is
// effectively a passthrough — `spec.resources` becomes the source of truth.
func ClampResources(in *corev1.ResourceRequirements, t Tier) corev1.ResourceRequirements {
	l := For(t)
	out := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}
	if in != nil {
		for k, v := range in.Requests {
			out.Requests[k] = v
		}
		for k, v := range in.Limits {
			out.Limits[k] = v
		}
	}

	// Memory.
	if l.MemoryRequest != "" {
		ceiling := resource.MustParse(l.MemoryRequest)
		set, present := out.Requests[corev1.ResourceMemory]
		if !present || set.Cmp(ceiling) > 0 {
			out.Requests[corev1.ResourceMemory] = ceiling
		}
	}
	if l.MemoryLimit != "" {
		ceiling := resource.MustParse(l.MemoryLimit)
		set, present := out.Limits[corev1.ResourceMemory]
		if !present || set.Cmp(ceiling) > 0 {
			out.Limits[corev1.ResourceMemory] = ceiling
		}
	}

	// CPU.
	if l.CPURequest != "" {
		ceiling := resource.MustParse(l.CPURequest)
		set, present := out.Requests[corev1.ResourceCPU]
		if !present || set.Cmp(ceiling) > 0 {
			out.Requests[corev1.ResourceCPU] = ceiling
		}
	}
	if l.CPULimit != "" {
		ceiling := resource.MustParse(l.CPULimit)
		set, present := out.Limits[corev1.ResourceCPU]
		if !present || set.Cmp(ceiling) > 0 {
			out.Limits[corev1.ResourceCPU] = ceiling
		}
	}

	// Drop empty maps so the result round-trips cleanly with marshal/unmarshal.
	if len(out.Requests) == 0 {
		out.Requests = nil
	}
	if len(out.Limits) == 0 {
		out.Limits = nil
	}
	return out
}

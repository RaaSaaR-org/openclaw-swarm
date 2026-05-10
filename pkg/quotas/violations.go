package quotas

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// ResourceViolations checks `in` against the tier's ceilings and returns one
// violation message per over-tier field. Empty slice means the spec is within
// limits. Tier ceilings of "" are treated as "no ceiling" and skip the check
// (matches the enterprise / passthrough mode in ClampResources).
//
// Used by the validating-admission webhook (TASK-015 Phase 2) to reject
// KaiInstance creates/updates where `spec.resources` would exceed the tier
// at admission time — defense in depth on top of the operator-side
// ClampResources, which silently lowers values to fit.
func ResourceViolations(in *corev1.ResourceRequirements, t Tier) []string {
	if in == nil {
		return nil
	}
	l := For(t)
	var v []string
	check := func(name string, got resource.Quantity, has bool, ceilingStr string) {
		if !has || ceilingStr == "" {
			return
		}
		ceiling := resource.MustParse(ceilingStr)
		if got.Cmp(ceiling) > 0 {
			v = append(v, name+" "+got.String()+" exceeds "+string(t)+" tier ceiling "+ceilingStr)
		}
	}
	memReq, hasMemReq := in.Requests[corev1.ResourceMemory]
	check("requests.memory", memReq, hasMemReq, l.MemoryRequest)
	memLim, hasMemLim := in.Limits[corev1.ResourceMemory]
	check("limits.memory", memLim, hasMemLim, l.MemoryLimit)
	cpuReq, hasCPUReq := in.Requests[corev1.ResourceCPU]
	check("requests.cpu", cpuReq, hasCPUReq, l.CPURequest)
	cpuLim, hasCPULim := in.Limits[corev1.ResourceCPU]
	check("limits.cpu", cpuLim, hasCPULim, l.CPULimit)
	return v
}

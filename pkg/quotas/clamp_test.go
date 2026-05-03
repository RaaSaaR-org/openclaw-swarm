package quotas

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestClampResourcesNilInputUsesTierDefaults(t *testing.T) {
	t.Parallel()
	got := ClampResources(nil, TierFree)
	if mem := got.Requests[corev1.ResourceMemory]; mem.String() != "384Mi" {
		t.Errorf("nil input + free tier: memory request = %s, want 384Mi", mem.String())
	}
	if mem := got.Limits[corev1.ResourceMemory]; mem.String() != "768Mi" {
		t.Errorf("nil input + free tier: memory limit = %s, want 768Mi", mem.String())
	}
}

func TestClampResourcesClampsExceedingValues(t *testing.T) {
	t.Parallel()
	in := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("4Gi"), // way over free's 384Mi
			corev1.ResourceCPU:    resource.MustParse("4000m"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("8Gi"),
		},
	}
	got := ClampResources(in, TierFree)
	if mem := got.Requests[corev1.ResourceMemory]; mem.String() != "384Mi" {
		t.Errorf("clamped memory request = %s, want 384Mi", mem.String())
	}
	if cpu := got.Requests[corev1.ResourceCPU]; cpu.String() != "50m" {
		t.Errorf("clamped CPU request = %s, want 50m", cpu.String())
	}
	if mem := got.Limits[corev1.ResourceMemory]; mem.String() != "768Mi" {
		t.Errorf("clamped memory limit = %s, want 768Mi", mem.String())
	}
}

func TestClampResourcesPassesThroughLowerValues(t *testing.T) {
	t.Parallel()
	in := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
	}
	got := ClampResources(in, TierFree)
	if mem := got.Requests[corev1.ResourceMemory]; mem.String() != "128Mi" {
		t.Errorf("128Mi (under 384Mi ceiling) should pass through, got %s", mem.String())
	}
}

func TestClampResourcesEnterpriseIsPassthrough(t *testing.T) {
	t.Parallel()
	in := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("16Gi"),
			corev1.ResourceCPU:    resource.MustParse("4000m"),
		},
	}
	got := ClampResources(in, TierEnterprise)
	// Enterprise has all-zero limits → no clamp applied → input survives.
	if mem := got.Requests[corev1.ResourceMemory]; mem.String() != "16Gi" {
		t.Errorf("enterprise should pass through, got %s", mem.String())
	}
	wantCPU := resource.MustParse("4000m")
	if cpu := got.Requests[corev1.ResourceCPU]; cpu.Cmp(wantCPU) != 0 {
		t.Errorf("enterprise CPU passthrough failed: got %s, want equivalent of 4000m", cpu.String())
	}
}

func TestClampResourcesDropsEmptyMaps(t *testing.T) {
	t.Parallel()
	got := ClampResources(nil, TierEnterprise)
	if got.Requests != nil || got.Limits != nil {
		t.Errorf("enterprise + nil input should yield empty Requests/Limits, got %+v", got)
	}
}

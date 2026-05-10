package quotas

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestResourceViolations_NilPasses(t *testing.T) {
	t.Parallel()
	if v := ResourceViolations(nil, TierFree); len(v) != 0 {
		t.Errorf("expected no violations, got %v", v)
	}
}

func TestResourceViolations_FreeTierMemoryOverLimit(t *testing.T) {
	t.Parallel()
	in := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("4Gi"), // far above free tier 384Mi
		},
	}
	v := ResourceViolations(in, TierFree)
	if len(v) != 1 {
		t.Fatalf("expected 1 violation, got %d (%v)", len(v), v)
	}
	if !strings.Contains(v[0], "requests.memory") || !strings.Contains(v[0], "free") {
		t.Errorf("violation message = %q", v[0])
	}
}

func TestResourceViolations_PassesUnderLimit(t *testing.T) {
	t.Parallel()
	in := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("256Mi"), // below 384Mi free
			corev1.ResourceCPU:    resource.MustParse("50m"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("384Mi"),
		},
	}
	if v := ResourceViolations(in, TierFree); len(v) != 0 {
		t.Errorf("expected pass, got %v", v)
	}
}

func TestResourceViolations_EnterpriseIsPassthrough(t *testing.T) {
	t.Parallel()
	// Enterprise tier ceilings are all zero/empty → no violations regardless
	// of how big the spec is.
	in := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("128Gi"),
			corev1.ResourceCPU:    resource.MustParse("32"),
		},
	}
	if v := ResourceViolations(in, TierEnterprise); len(v) != 0 {
		t.Errorf("enterprise should pass anything, got %v", v)
	}
}

func TestResourceViolations_AggregatesMultiple(t *testing.T) {
	t.Parallel()
	in := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("4Gi"),
			corev1.ResourceCPU:    resource.MustParse("4"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("8Gi"),
		},
	}
	v := ResourceViolations(in, TierFree)
	if len(v) != 3 {
		t.Errorf("expected 3 violations (memory request + memory limit + cpu request), got %d (%v)", len(v), v)
	}
}

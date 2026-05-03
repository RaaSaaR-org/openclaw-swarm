package controller

import (
	"strings"
	"testing"

	swarmv1alpha1 "github.com/emai-ai/swarm-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newTestKaiInstance(name, namespace string) *swarmv1alpha1.KaiInstance {
	return &swarmv1alpha1.KaiInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: swarmv1alpha1.KaiInstanceSpec{
			CustomerName: "Test Customer",
			ProjectName:  "Test Project",
		},
	}
}

func TestChildName(t *testing.T) {
	if got := childName("east-side-fab"); got != "kai-east-side-fab" {
		t.Errorf("childName('east-side-fab') = %q, want 'kai-east-side-fab'", got)
	}
}

func TestCommonLabels(t *testing.T) {
	labels := commonLabels("test-slug")

	expected := map[string]string{
		"app.kubernetes.io/name":       "kai-test-slug",
		"app.kubernetes.io/part-of":    "emai-swarm",
		"app.kubernetes.io/managed-by": "swarm-operator",
		"emai.io/component":            "agent",
		"emai.io/role":                 "customer",
		"emai.io/customer":             "test-slug",
	}

	for key, want := range expected {
		if got, ok := labels[key]; !ok {
			t.Errorf("missing label %q", key)
		} else if got != want {
			t.Errorf("label %q = %q, want %q", key, got, want)
		}
	}
}

func TestBuildConfigMap(t *testing.T) {
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	tmpl := &renderedTemplates{
		SoulMD:       "# Soul",
		AgentsMD:     "# Agents",
		ToolsMD:      "# Tools",
		HeartbeatMD:  "# Heartbeat",
		OpenClawJSON: `{"agents":{}}`,
		SkillMC:      "# Skill",
	}

	cm := buildConfigMap(kai, "test", tmpl)

	if cm.Name != "kai-test-identity" {
		t.Errorf("ConfigMap name = %q, want 'kai-test-identity'", cm.Name)
	}
	if cm.Namespace != "emai-swarm" {
		t.Errorf("ConfigMap namespace = %q, want 'emai-swarm'", cm.Namespace)
	}

	// All template files should be in the ConfigMap
	expectedKeys := []string{"SOUL.md", "AGENTS.md", "TOOLS.md", "HEARTBEAT.md", "openclaw.json", "SKILL-mc.md"}
	for _, key := range expectedKeys {
		if _, ok := cm.Data[key]; !ok {
			t.Errorf("ConfigMap missing key %q", key)
		}
	}
}

func TestBuildPVC(t *testing.T) {
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	pvc := buildPVC(kai, "test")

	if pvc.Name != "kai-test-state" {
		t.Errorf("PVC name = %q, want 'kai-test-state'", pvc.Name)
	}
	if pvc.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Error("PVC should use ReadWriteOnce")
	}
	storage := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if storage.String() != "1Gi" {
		t.Errorf("PVC storage = %s, want 1Gi", storage.String())
	}
}

func TestBuildDeployment(t *testing.T) {
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	deploy := buildDeployment(kai, "test", "abc123", deploymentOpts{})

	if deploy.Name != "kai-test" {
		t.Errorf("Deployment name = %q, want 'kai-test'", deploy.Name)
	}

	// Should have 1 replica
	if *deploy.Spec.Replicas != 1 {
		t.Errorf("replicas = %d, want 1", *deploy.Spec.Replicas)
	}

	// Should have init container + agent container
	spec := deploy.Spec.Template.Spec
	if len(spec.InitContainers) != 1 {
		t.Fatalf("expected 1 init container, got %d", len(spec.InitContainers))
	}
	if len(spec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(spec.Containers))
	}

	// Init container should create workspace directories and copy files
	initScript := spec.InitContainers[0].Command[2] // sh -c <script>
	for _, dir := range []string{"workspace", "skills/mc", "memory"} {
		if !strings.Contains(initScript, dir) {
			t.Errorf("init script should create %q directory", dir)
		}
	}
	for _, file := range []string{"SOUL.md", "AGENTS.md", "TOOLS.md", "HEARTBEAT.md", "SKILL-mc.md", "openclaw.json"} {
		if !strings.Contains(initScript, file) {
			t.Errorf("init script should reference %q", file)
		}
	}

	// Identity files should use "don't overwrite" pattern
	for _, file := range []string{"SOUL.md", "AGENTS.md", "TOOLS.md", "HEARTBEAT.md"} {
		pattern := "[ -f /state/workspace/" + file + " ] || cp"
		if !strings.Contains(initScript, pattern) {
			t.Errorf("init script should not overwrite existing %s", file)
		}
	}

	// Agent container checks
	container := spec.Containers[0]
	if container.Name != "agent" {
		t.Errorf("container name = %q, want 'agent'", container.Name)
	}
	if container.Image != agentImage {
		t.Errorf("container image = %q, want %q", container.Image, agentImage)
	}

	// Should mount state PVC
	found := false
	for _, vm := range container.VolumeMounts {
		if vm.Name == "state" {
			found = true
		}
	}
	if !found {
		t.Error("container should mount state PVC")
	}

	// Should not automount service account
	if *spec.AutomountServiceAccountToken {
		t.Error("should not automount service account token")
	}

	// Config hash annotation should be set
	if hash, ok := deploy.Spec.Template.Annotations["emai.io/config-hash"]; !ok || hash != "abc123" {
		t.Errorf("config-hash annotation = %q, want 'abc123'", hash)
	}

	// Should have liveness and readiness probes
	if container.LivenessProbe == nil {
		t.Error("should have liveness probe")
	}
	if container.ReadinessProbe == nil {
		t.Error("should have readiness probe")
	}
}

func TestBuildDeploymentSuspended(t *testing.T) {
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	kai.Spec.Suspended = true

	deploy := buildDeployment(kai, "test", "hash", deploymentOpts{})

	if *deploy.Spec.Replicas != 0 {
		t.Errorf("suspended deployment should have 0 replicas, got %d", *deploy.Spec.Replicas)
	}
}

func TestBuildDeploymentCustomModel(t *testing.T) {
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	kai.Spec.Model = "openrouter/anthropic/claude-sonnet"

	deploy := buildDeployment(kai, "test", "hash", deploymentOpts{})

	// Find OPENCLAW_MODEL env var
	for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "OPENCLAW_MODEL" {
			if env.Value != "openrouter/anthropic/claude-sonnet" {
				t.Errorf("OPENCLAW_MODEL = %q, want custom model", env.Value)
			}
			return
		}
	}
	t.Error("OPENCLAW_MODEL env var not found")
}

func TestBuildDeploymentDefaultModel(t *testing.T) {
	kai := newTestKaiInstance("kai-test", "emai-swarm")

	deploy := buildDeployment(kai, "test", "hash", deploymentOpts{})

	for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "OPENCLAW_MODEL" {
			if env.Value != defaultModel {
				t.Errorf("OPENCLAW_MODEL = %q, want default %q", env.Value, defaultModel)
			}
			return
		}
	}
	t.Error("OPENCLAW_MODEL env var not found")
}

func TestBuildDeploymentTelegram(t *testing.T) {
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	kai.Spec.Telegram = &swarmv1alpha1.TelegramConfig{
		BotTokenSecretRef: "kai-test-telegram",
	}

	deploy := buildDeployment(kai, "test", "hash", deploymentOpts{})

	// Should have TELEGRAM_BOT_TOKEN env var
	found := false
	for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "TELEGRAM_BOT_TOKEN" {
			found = true
			if env.ValueFrom.SecretKeyRef.Name != "kai-test-telegram" {
				t.Errorf("telegram secret ref = %q, want 'kai-test-telegram'", env.ValueFrom.SecretKeyRef.Name)
			}
		}
	}
	if !found {
		t.Error("TELEGRAM_BOT_TOKEN env var not found when telegram is configured")
	}
}

func TestCommonLabelsCarriesBothLegacyAndNewTenantLabel(t *testing.T) {
	t.Parallel()
	got := commonLabels("acme")
	// Legacy label must remain so existing NetworkPolicy podSelector and any
	// kubectl filters in swarm-emai/swarm-config keep working until the
	// v1alpha2 CRD bump (TASK-012) flips them in lockstep.
	if got["emai.io/customer"] != "acme" {
		t.Errorf("emai.io/customer = %q, want acme (legacy label must survive Phase 1)", got["emai.io/customer"])
	}
	// New label is what generic / forked tooling should select on going forward.
	if got["swarm.io/tenant"] != "acme" {
		t.Errorf("swarm.io/tenant = %q, want acme (additive new label)", got["swarm.io/tenant"])
	}
}

func TestCommonLabelsForOmitsSaaSLabelsWhenSpecFieldsEmpty(t *testing.T) {
	t.Parallel()
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	got := commonLabelsFor(kai, "test")
	for _, k := range []string{"swarm.io/user-id", "swarm.io/tier", "swarm.io/app", "swarm.io/org", "swarm.io/managed"} {
		if _, present := got[k]; present {
			t.Errorf("%s should not be present when spec field is empty (got %q)", k, got[k])
		}
	}
	// Sanity: legacy + tenant still there.
	if got["swarm.io/tenant"] != "test" {
		t.Errorf("swarm.io/tenant missing or wrong: %q", got["swarm.io/tenant"])
	}
}

func TestCommonLabelsForRendersAllSaaSLabelsWhenSpecFieldsSet(t *testing.T) {
	t.Parallel()
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	kai.Spec.UserRef = "u_01HX3ZQABC"
	kai.Spec.Tier = "starter"
	kai.Spec.AppRef = "personal-assistant"
	kai.Spec.Org = "acme-gmbh"
	kai.Spec.Managed = "saas"

	got := commonLabelsFor(kai, "test")
	cases := map[string]string{
		"swarm.io/user-id": "u_01HX3ZQABC",
		"swarm.io/tier":    "starter",
		"swarm.io/app":     "personal-assistant",
		"swarm.io/org":     "acme-gmbh",
		"swarm.io/managed": "saas",
	}
	for k, want := range cases {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

func TestBuildDeploymentPropagatesSaaSLabelsToPodTemplate(t *testing.T) {
	t.Parallel()
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	kai.Spec.UserRef = "u_01HX3ZQABC"
	kai.Spec.Tier = "starter"
	deploy := buildDeployment(kai, "test", "hash", deploymentOpts{})

	// Pod template labels are what kubectl get pod -l swarm.io/user-id=... selects on.
	pod := deploy.Spec.Template.Labels
	if pod["swarm.io/user-id"] != "u_01HX3ZQABC" {
		t.Errorf("pod label swarm.io/user-id = %q, want u_01HX3ZQABC", pod["swarm.io/user-id"])
	}
	if pod["swarm.io/tier"] != "starter" {
		t.Errorf("pod label swarm.io/tier = %q, want starter", pod["swarm.io/tier"])
	}
}

func TestBuildDeploymentOpenRouterDefaultPerSlugSecret(t *testing.T) {
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	deploy := buildDeployment(kai, "test", "hash", deploymentOpts{})

	got := openRouterSecretRef(t, deploy)
	if got != "kai-test-openrouter" {
		t.Errorf("default OPENROUTER_API_KEY secret ref = %q, want kai-test-openrouter (per-slug fallback)", got)
	}
}

func TestBuildDeploymentOpenRouterPooledSecret(t *testing.T) {
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	deploy := buildDeployment(kai, "test", "hash", deploymentOpts{PooledOpenRouterSecret: "swarm-pooled-openrouter"})

	got := openRouterSecretRef(t, deploy)
	if got != "swarm-pooled-openrouter" {
		t.Errorf("pooled OPENROUTER_API_KEY secret ref = %q, want swarm-pooled-openrouter", got)
	}
	// And critically: a different slug must point at the same shared Secret.
	other := buildDeployment(kai, "betaco", "hash", deploymentOpts{PooledOpenRouterSecret: "swarm-pooled-openrouter"})
	if openRouterSecretRef(t, other) != "swarm-pooled-openrouter" {
		t.Error("pooled secret must be shared across slugs")
	}
}

func openRouterSecretRef(t *testing.T, deploy *appsv1.Deployment) string {
	t.Helper()
	for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "OPENROUTER_API_KEY" {
			if env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil {
				t.Fatalf("OPENROUTER_API_KEY must come from a SecretKeyRef, got %+v", env.ValueFrom)
			}
			if env.ValueFrom.SecretKeyRef.Key != "api-key" {
				t.Errorf("OPENROUTER_API_KEY key = %q, want api-key", env.ValueFrom.SecretKeyRef.Key)
			}
			return env.ValueFrom.SecretKeyRef.LocalObjectReference.Name
		}
	}
	t.Fatal("OPENROUTER_API_KEY env var not found")
	return ""
}

func TestBuildDeploymentNoTelegram(t *testing.T) {
	kai := newTestKaiInstance("kai-test", "emai-swarm")

	deploy := buildDeployment(kai, "test", "hash", deploymentOpts{})

	for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "TELEGRAM_BOT_TOKEN" {
			t.Error("TELEGRAM_BOT_TOKEN should not be set when telegram is not configured")
		}
	}
}

func TestBuildDeploymentCustomResources(t *testing.T) {
	// Custom Spec.Resources are honored only when within the tier ceiling.
	// Use the growth tier (2Gi memory limit) so the test's 1Gi limit passes
	// through unchanged. Without a tier, the operator clamps to free
	// (768Mi limit) — that's the right behavior, but it's covered by a
	// dedicated clamping test below.
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	kai.Spec.Tier = "growth"
	kai.Spec.Resources = &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("1Gi"),
		},
	}

	deploy := buildDeployment(kai, "test", "hash", deploymentOpts{})

	mem := deploy.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceMemory]
	if mem.String() != "1Gi" {
		t.Errorf("custom memory limit (within growth tier) = %s, want 1Gi", mem.String())
	}
}

func TestBuildDeploymentClampsResourcesToTierCeiling(t *testing.T) {
	t.Parallel()
	// Free-tier instance asking for 4Gi should be clamped to free's 768Mi
	// limit. Defense-in-depth — the eventual ValidatingAdmissionWebhook
	// (TASK-015 Phase 2) catches this earlier, but the operator clamps too
	// in case a tenant bypasses the webhook (e.g. cluster admin patches a CR
	// with kubectl --validate=false).
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	kai.Spec.Tier = "free"
	kai.Spec.Resources = &corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("4Gi"),
		},
	}

	deploy := buildDeployment(kai, "test", "hash", deploymentOpts{})
	mem := deploy.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceMemory]
	if mem.String() != "768Mi" {
		t.Errorf("free-tier memory limit = %s, want 768Mi (clamped from 4Gi)", mem.String())
	}
}

func TestBuildDeploymentDefaultModelByTier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tier       string
		wantPrefix string
	}{
		{"free", "openrouter/stepfun"},                   // free OpenRouter model
		{"starter", "openrouter/anthropic/claude-haiku"}, // paid Haiku class
		{"growth", "openrouter/anthropic/claude-haiku"},  // paid Haiku class
	}
	for _, c := range cases {
		t.Run(c.tier, func(t *testing.T) {
			kai := newTestKaiInstance("kai-test", "emai-swarm")
			kai.Spec.Tier = c.tier
			deploy := buildDeployment(kai, "test", "hash", deploymentOpts{})
			for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "OPENCLAW_MODEL" {
					if !strings.HasPrefix(env.Value, c.wantPrefix) {
						t.Errorf("tier=%s: model = %q, want prefix %q", c.tier, env.Value, c.wantPrefix)
					}
					return
				}
			}
			t.Errorf("OPENCLAW_MODEL env not set")
		})
	}
}

func TestBuildDeploymentSpecModelOverridesTierDefault(t *testing.T) {
	t.Parallel()
	// Explicit spec.Model always wins over the tier default — operators
	// running custom models for specific tenants must not be silently
	// overridden by the tier picker.
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	kai.Spec.Tier = "free"
	kai.Spec.Model = "openrouter/anthropic/claude-sonnet"
	deploy := buildDeployment(kai, "test", "hash", deploymentOpts{})
	for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "OPENCLAW_MODEL" {
			if env.Value != "openrouter/anthropic/claude-sonnet" {
				t.Errorf("explicit spec.Model = %q, want openrouter/anthropic/claude-sonnet (tier default must not override)", env.Value)
			}
			return
		}
	}
}

func TestBuildDeploymentLegacyTenantKeepsLegacyModel(t *testing.T) {
	t.Parallel()
	// No tier, no managed → legacy tenant. Must get the operator's hard-coded
	// defaultModel so existing internal workspaces don't suddenly switch.
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	deploy := buildDeployment(kai, "test", "hash", deploymentOpts{})
	for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "OPENCLAW_MODEL" {
			if env.Value != defaultModel {
				t.Errorf("legacy tenant model = %q, want defaultModel %q", env.Value, defaultModel)
			}
			return
		}
	}
}

func TestBuildDeploymentInternalManagedSkipsClamp(t *testing.T) {
	t.Parallel()
	// EmAI-internal tenants (managed:internal) are sized by hand by the
	// platform team — the operator must NOT clamp them to a SaaS tier. This
	// is the PROP-003 coexistence rule baked into the operator.
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	kai.Spec.Managed = "internal"
	kai.Spec.Resources = &corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("4Gi"),
		},
	}

	deploy := buildDeployment(kai, "test", "hash", deploymentOpts{})
	mem := deploy.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceMemory]
	if mem.String() != "4Gi" {
		t.Errorf("internal-managed memory limit = %s, want 4Gi (no clamp)", mem.String())
	}
}

func TestBuildService(t *testing.T) {
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	svc := buildService(kai, "test")

	if svc.Name != "kai-test" {
		t.Errorf("Service name = %q, want 'kai-test'", svc.Name)
	}
	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Error("Service should be ClusterIP")
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != gatewayPort {
		t.Errorf("Service should expose port %d", gatewayPort)
	}
}

func TestBuildNetworkPolicy(t *testing.T) {
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	np := buildNetworkPolicy(kai, "test")

	if np.Name != "kai-test-isolation" {
		t.Errorf("NetworkPolicy name = %q, want 'kai-test-isolation'", np.Name)
	}

	// Should select pods by customer label
	if np.Spec.PodSelector.MatchLabels["emai.io/customer"] != "test" {
		t.Error("NetworkPolicy should select pods by customer label")
	}

	// Should have both ingress and egress policy types
	if len(np.Spec.PolicyTypes) != 2 {
		t.Error("NetworkPolicy should have both Ingress and Egress policy types")
	}

	// Ingress should only allow from central role
	if len(np.Spec.Ingress) != 1 {
		t.Fatal("should have 1 ingress rule")
	}
	ingressFrom := np.Spec.Ingress[0].From[0].PodSelector.MatchLabels
	if ingressFrom["emai.io/role"] != "central" {
		t.Error("ingress should only allow from central role")
	}

	// Egress should allow DNS (53) and HTTPS (443)
	if len(np.Spec.Egress) != 2 {
		t.Fatal("should have 2 egress rules (DNS + HTTPS)")
	}
}

func TestBuildIngress(t *testing.T) {
	kai := newTestKaiInstance("kai-test", "emai-swarm")
	ing := buildIngress(kai, "test", "kai.emai.dev", "kai-tls")

	if ing.Name != "kai-test-ws" {
		t.Errorf("Ingress name = %q, want 'kai-test-ws'", ing.Name)
	}

	// Should have TLS config
	if len(ing.Spec.TLS) != 1 || ing.Spec.TLS[0].SecretName != "kai-tls" {
		t.Error("Ingress should have TLS config with correct secret")
	}

	// Should route /ws/test to the service
	path := ing.Spec.Rules[0].HTTP.Paths[0]
	if path.Path != "/ws/test" {
		t.Errorf("Ingress path = %q, want '/ws/test'", path.Path)
	}
	if path.Backend.Service.Name != "kai-test" {
		t.Errorf("Ingress backend = %q, want 'kai-test'", path.Backend.Service.Name)
	}
}

func TestGatewayURL(t *testing.T) {
	url := gatewayURL("emai-swarm", "east-side-fab")
	expected := "kai-east-side-fab.emai-swarm.svc:18789"
	if url != expected {
		t.Errorf("gatewayURL = %q, want %q", url, expected)
	}
}

func TestExternalURL(t *testing.T) {
	url := externalURL("kai.emai.dev", "east-side-fab")
	expected := "https://kai.emai.dev/ws/east-side-fab"
	if url != expected {
		t.Errorf("externalURL = %q, want %q", url, expected)
	}
}

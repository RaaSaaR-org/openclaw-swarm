package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
)

func fakeServer(t *testing.T, token string, items ...*unstructured.Unstructured) *server {
	t.Helper()
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		kaiInstanceGVR: "KaiInstanceList",
	}
	objs := make([]runtime.Object, 0, len(items))
	for _, it := range items {
		objs = append(objs, it)
	}
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
	return &server{dyn: dyn, namespace: "swarm-system", token: token}
}

func newKai(name, tenant, slug, phase string, ready, suspended bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "swarm.emai.io/v1alpha2",
		"kind":       "KaiInstance",
		"metadata": map[string]any{
			"name":      name,
			"namespace": "swarm-system",
		},
		"spec": map[string]any{
			"tenantName":  tenant,
			"projectName": "Pilot",
			"tenantSlug":  slug,
			"suspended":   suspended,
		},
		"status": map[string]any{
			"phase":      phase,
			"ready":      ready,
			"tenantSlug": slug,
		},
	}}
}

func authedReq(method, path, token string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func TestRequireAuth_RejectsMissingToken(t *testing.T) {
	t.Parallel()
	s := fakeServer(t, "secret")
	called := false
	h := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/instances", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if called {
		t.Fatal("inner handler must not be called when auth fails")
	}
}

func TestRequireAuth_RejectsWrongToken(t *testing.T) {
	t.Parallel()
	s := fakeServer(t, "secret")
	h := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not run on wrong token")
	})
	rr := httptest.NewRecorder()
	h(rr, authedReq(http.MethodGet, "/api/instances", "wrong"))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d", rr.Code)
	}
}

func TestRequireAuth_AcceptsRightToken(t *testing.T) {
	t.Parallel()
	s := fakeServer(t, "right-secret")
	called := false
	h := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	rr := httptest.NewRecorder()
	h(rr, authedReq(http.MethodGet, "/api/instances", "right-secret"))
	if rr.Code != http.StatusOK || !called {
		t.Fatalf("expected handler to run with 200, got code=%d called=%v", rr.Code, called)
	}
}

func TestListInstances_HappyPath(t *testing.T) {
	t.Parallel()
	s := fakeServer(t, "tok",
		newKai("kai-acme", "Acme", "acme", "Running", true, false),
		newKai("kai-betaco", "Beta Co", "betaco", "Provisioning", false, false),
	)

	rr := httptest.NewRecorder()
	s.listInstances(rr, httptest.NewRequest(http.MethodGet, "/api/instances", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	var got []instanceSummary
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(got))
	}
	// Order from fake client isn't guaranteed; check by name.
	names := map[string]instanceSummary{got[0].Name: got[0], got[1].Name: got[1]}
	if names["kai-acme"].Phase != "Running" || !names["kai-acme"].Ready {
		t.Errorf("acme summary wrong: %+v", names["kai-acme"])
	}
	if names["kai-betaco"].Phase != "Provisioning" || names["kai-betaco"].Ready {
		t.Errorf("betaco summary wrong: %+v", names["kai-betaco"])
	}
}

func TestListInstances_NoDynClient_503(t *testing.T) {
	t.Parallel()
	s := &server{namespace: "swarm-system", token: "tok"}
	rr := httptest.NewRecorder()
	s.listInstances(rr, httptest.NewRequest(http.MethodGet, "/api/instances", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no dyn, got %d", rr.Code)
	}
}

func TestGetInstance_HappyPath(t *testing.T) {
	t.Parallel()
	s := fakeServer(t, "tok", newKai("kai-acme", "Acme", "acme", "Running", true, false))
	req := httptest.NewRequest(http.MethodGet, "/api/instances/kai-acme", nil)
	req.SetPathValue("name", "kai-acme")
	rr := httptest.NewRecorder()
	s.getInstance(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestGetInstance_NotFound(t *testing.T) {
	t.Parallel()
	s := fakeServer(t, "tok")
	req := httptest.NewRequest(http.MethodGet, "/api/instances/kai-ghost", nil)
	req.SetPathValue("name", "kai-ghost")
	rr := httptest.NewRecorder()
	s.getInstance(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing instance, got %d", rr.Code)
	}
}

func TestSuspendInstance_HappyPath(t *testing.T) {
	t.Parallel()
	s := fakeServer(t, "tok", newKai("kai-acme", "Acme", "acme", "Running", true, false))
	req := httptest.NewRequest(http.MethodPost, "/api/instances/kai-acme/suspend", nil)
	req.SetPathValue("name", "kai-acme")
	rr := httptest.NewRecorder()
	s.suspendInstance(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["suspended"] != true {
		t.Errorf("expected suspended=true in response, got %v", body)
	}
}

func TestResumeInstance_HappyPath(t *testing.T) {
	t.Parallel()
	s := fakeServer(t, "tok", newKai("kai-acme", "Acme", "acme", "Suspended", false, true))
	req := httptest.NewRequest(http.MethodPost, "/api/instances/kai-acme/resume", nil)
	req.SetPathValue("name", "kai-acme")
	rr := httptest.NewRecorder()
	s.resumeInstance(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["suspended"] != false {
		t.Errorf("expected suspended=false in response, got %v", body)
	}
}

func TestPatchSuspended_NotFound(t *testing.T) {
	t.Parallel()
	s := fakeServer(t, "tok")
	req := httptest.NewRequest(http.MethodPost, "/api/instances/kai-ghost/suspend", nil)
	req.SetPathValue("name", "kai-ghost")
	rr := httptest.NewRecorder()
	s.suspendInstance(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing instance, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestSummarize_PullsFieldsCorrectly(t *testing.T) {
	t.Parallel()
	u := newKai("kai-acme", "Acme GmbH", "acme", "Running", true, false)
	// Add the optional URL fields directly.
	_ = unstructured.SetNestedField(u.Object, "kai-acme.swarm-system.svc:18789", "status", "gatewayURL")
	_ = unstructured.SetNestedField(u.Object, "https://acme.kai.example.com", "status", "externalURL")
	_ = unstructured.SetNestedField(u.Object, "openrouter/anthropic/claude", "spec", "model")

	s := summarize(u)
	if s.Name != "kai-acme" || s.TenantName != "Acme GmbH" || s.TenantSlug != "acme" {
		t.Errorf("identity fields wrong: %+v", s)
	}
	if s.Phase != "Running" || !s.Ready {
		t.Errorf("status fields wrong: %+v", s)
	}
	if s.GatewayURL == "" || s.ExternalURL == "" || s.Model == "" {
		t.Errorf("optional fields lost: %+v", s)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	t.Parallel()
	if firstNonEmpty("a", "b") != "a" {
		t.Error("first non-empty should be a")
	}
	if firstNonEmpty("", "b") != "b" {
		t.Error("fallback should be b")
	}
	if firstNonEmpty("", "") != "" {
		t.Error("both empty should be empty")
	}
	if firstNonEmpty("", "", "c", "d") != "c" {
		t.Error("variadic should walk past leading blanks")
	}
	if firstNonEmpty() != "" {
		t.Error("no args should return empty")
	}
}

// TASK-024 Phase 5 renamed customerName/customerSlug → tenantName/tenantSlug
// in v1alpha2. The admin-console must still render sensibly if a cluster
// somewhere serves a legacy v1alpha1-shaped object — fall back to the old
// keys.
func TestSummarize_FallsBackToLegacyCustomerFields(t *testing.T) {
	t.Parallel()
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "swarm.emai.io/v1alpha1",
		"kind":       "KaiInstance",
		"metadata": map[string]any{
			"name":      "kai-legacy",
			"namespace": "swarm-system",
		},
		"spec": map[string]any{
			"customerName": "Legacy Co",
			"customerSlug": "legacy",
		},
		"status": map[string]any{
			"customerSlug": "legacy",
			"phase":        "Running",
		},
	}}
	s := summarize(u)
	if s.TenantName != "Legacy Co" {
		t.Errorf("expected legacy customerName fallback, got %q", s.TenantName)
	}
	if s.TenantSlug != "legacy" {
		t.Errorf("expected legacy customerSlug fallback, got %q", s.TenantSlug)
	}
}

func TestBoolStr(t *testing.T) {
	t.Parallel()
	if boolStr(true) != "true" || boolStr(false) != "false" {
		t.Fail()
	}
}

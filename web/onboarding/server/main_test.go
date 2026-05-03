package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	return &server{dyn: dyn, namespace: "emai-swarm", token: token}
}

func newReq(method, path, token, body string) *http.Request {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func TestRequireAuth_RejectsMissingAndWrongToken(t *testing.T) {
	t.Parallel()
	s := fakeServer(t, "secret")
	h := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler must not run on auth fail")
	})
	for _, name := range []string{"missing", "wrong"} {
		t.Run(name, func(t *testing.T) {
			tok := ""
			if name == "wrong" {
				tok = "bad-token"
			}
			rr := httptest.NewRecorder()
			h(rr, newReq(http.MethodGet, "/api/auth", tok, ""))
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", rr.Code)
			}
		})
	}
}

func TestRequireAuth_AcceptsRightToken(t *testing.T) {
	t.Parallel()
	s := fakeServer(t, "right")
	called := false
	h := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	rr := httptest.NewRecorder()
	h(rr, newReq(http.MethodGet, "/api/auth", "right", ""))
	if rr.Code != http.StatusOK || !called {
		t.Fatalf("expected handler to run with 200, got code=%d called=%v", rr.Code, called)
	}
}

func TestCreateInstance_HappyPath(t *testing.T) {
	t.Parallel()
	s := fakeServer(t, "tok")
	body := `{"customerName":"Acme GmbH","projectName":"Robot Pilot","customerSlug":"acme"}`
	rr := httptest.NewRecorder()
	s.createInstance(rr, newReq(http.MethodPost, "/api/instances", "tok", body))

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	var resp provisionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Name != "kai-acme" || resp.CustomerSlug != "acme" || resp.Namespace != "emai-swarm" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if len(resp.GatewayToken) != 48 { // 24 random bytes hex-encoded → 48 chars
		t.Errorf("gateway token should be 48 hex chars, got %d (%q)", len(resp.GatewayToken), resp.GatewayToken)
	}
}

func TestCreateInstance_HappyPath_WithOptionalFields(t *testing.T) {
	t.Parallel()
	s := fakeServer(t, "tok")
	body := `{
		"customerName":"Beta Co",
		"projectName":"Pilot",
		"customerSlug":"beta",
		"model":"openrouter/anthropic/claude",
		"telegramSecretRef":"kai-beta-telegram",
		"externalAccess":false
	}`
	rr := httptest.NewRecorder()
	s.createInstance(rr, newReq(http.MethodPost, "/api/instances", "tok", body))
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", rr.Code, rr.Body.String())
	}

	// Read it back via the fake client to confirm the optional fields landed.
	got, err := s.dyn.Resource(kaiInstanceGVR).Namespace("emai-swarm").Get(context.Background(), "kai-beta", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read-back failed: %v", err)
	}
	model, _, _ := unstructured.NestedString(got.Object, "spec", "model")
	if model != "openrouter/anthropic/claude" {
		t.Errorf("model not persisted: %q", model)
	}
	telSecret, _, _ := unstructured.NestedString(got.Object, "spec", "telegram", "botTokenSecretRef")
	if telSecret != "kai-beta-telegram" {
		t.Errorf("telegram ref not persisted: %q", telSecret)
	}
	extAccess, found, _ := unstructured.NestedBool(got.Object, "spec", "externalAccess")
	if !found || extAccess != false {
		t.Errorf("externalAccess not persisted as false (found=%v val=%v)", found, extAccess)
	}
}

func TestCreateInstance_BadJSON_400(t *testing.T) {
	t.Parallel()
	s := fakeServer(t, "tok")
	rr := httptest.NewRecorder()
	s.createInstance(rr, newReq(http.MethodPost, "/api/instances", "tok", "{not json}"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad JSON, got %d", rr.Code)
	}
}

func TestCreateInstance_DuplicateSlug_409(t *testing.T) {
	t.Parallel()
	existing := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "swarm.emai.io/v1alpha1",
		"kind":       "KaiInstance",
		"metadata": map[string]any{
			"name":      "kai-acme",
			"namespace": "emai-swarm",
		},
		"spec": map[string]any{
			"customerName": "Acme",
			"projectName":  "Old",
			"customerSlug": "acme",
		},
	}}
	s := fakeServer(t, "tok", existing)
	body := `{"customerName":"Acme","projectName":"New","customerSlug":"acme"}`
	rr := httptest.NewRecorder()
	s.createInstance(rr, newReq(http.MethodPost, "/api/instances", "tok", body))
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 on duplicate, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestCreateInstance_NoDynClient_503(t *testing.T) {
	t.Parallel()
	s := &server{namespace: "emai-swarm", token: "tok"}
	body := `{"customerName":"Acme","projectName":"P","customerSlug":"acme"}`
	rr := httptest.NewRecorder()
	s.createInstance(rr, newReq(http.MethodPost, "/api/instances", "tok", body))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no dyn, got %d", rr.Code)
	}
}

func TestValidateProvision(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		req       provisionRequest
		wantValid bool
		wantMsg   string
	}{
		{"happy", provisionRequest{CustomerName: "Acme", ProjectName: "P", CustomerSlug: "acme"}, true, ""},
		{"missing-customer", provisionRequest{ProjectName: "P", CustomerSlug: "acme"}, false, "customerName is required"},
		{"missing-project", provisionRequest{CustomerName: "A", CustomerSlug: "acme"}, false, "projectName is required"},
		{"missing-slug", provisionRequest{CustomerName: "A", ProjectName: "P"}, false, "customerSlug is required"},
		{"slug-uppercase", provisionRequest{CustomerName: "A", ProjectName: "P", CustomerSlug: "Acme"}, false, "DNS-safe"},
		{"slug-trailing-hyphen", provisionRequest{CustomerName: "A", ProjectName: "P", CustomerSlug: "acme-"}, false, "DNS-safe"},
		{"slug-leading-hyphen", provisionRequest{CustomerName: "A", ProjectName: "P", CustomerSlug: "-acme"}, false, "DNS-safe"},
		{"slug-too-long", provisionRequest{CustomerName: "A", ProjectName: "P", CustomerSlug: strings.Repeat("a", 64)}, false, "63 characters"},
		{"customer-too-long", provisionRequest{CustomerName: strings.Repeat("a", 101), ProjectName: "P", CustomerSlug: "a"}, false, "100 characters"},
		{"project-too-long", provisionRequest{CustomerName: "A", ProjectName: strings.Repeat("p", 201), CustomerSlug: "a"}, false, "200 characters"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := validateProvision(&c.req)
			if c.wantValid && err != nil {
				t.Fatalf("expected valid, got error: %v", err)
			}
			if !c.wantValid {
				if err == nil {
					t.Fatalf("expected validation error containing %q, got nil", c.wantMsg)
				}
				if !strings.Contains(err.Error(), c.wantMsg) {
					t.Errorf("error %q should contain %q", err.Error(), c.wantMsg)
				}
			}
		})
	}
}

func TestGenerateToken_FreshAndDistinct(t *testing.T) {
	t.Parallel()
	tok1, err := generateToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	tok2, err := generateToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if tok1 == tok2 {
		t.Fatal("two generations should be distinct")
	}
	if len(tok1) != 48 {
		t.Errorf("expected 48 hex chars, got %d", len(tok1))
	}
}


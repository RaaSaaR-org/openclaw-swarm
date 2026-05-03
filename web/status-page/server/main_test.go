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

// fakeDynServer wires the status-page server with a fake dynamic client seeded
// with the given KaiInstance objects. Use this for every handler test.
func fakeDynServer(t *testing.T, items ...*unstructured.Unstructured) *server {
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
	return &server{dyn: dyn, namespace: "emai-swarm"}
}

func newKaiInstance(slug, customerName, projectName, token, phase string, ready, suspended bool) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "swarm.emai.io/v1alpha1",
		"kind":       "KaiInstance",
		"metadata": map[string]any{
			"name":      "kai-" + slug,
			"namespace": "emai-swarm",
		},
		"spec": map[string]any{
			"customerName": customerName,
			"projectName":  projectName,
			"customerSlug": slug,
			"suspended":    suspended,
			"gatewayAuth": map[string]any{
				"mode":  "token",
				"token": token,
			},
		},
		"status": map[string]any{
			"phase":        phase,
			"ready":        ready,
			"customerSlug": slug,
			"externalURL":  "https://" + slug + ".kai.example.com",
		},
	}}
	return obj
}

func doStatusReq(t *testing.T, s *server, slug, headerToken, queryToken string) (int, map[string]any) {
	t.Helper()
	// Use a safe placeholder URL — the handler reads the slug from PathValue,
	// so passing arbitrary (even malformed) slugs through the URL would just
	// trip the http parser before the handler could test its own validation.
	url := "/api/status/x"
	if queryToken != "" {
		url += "?token=" + queryToken
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("slug", slug)
	if headerToken != "" {
		req.Header.Set("Authorization", "Bearer "+headerToken)
	}
	rr := httptest.NewRecorder()
	s.getStatus(rr, req)
	var body map[string]any
	if rr.Body.Len() > 0 {
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
	}
	return rr.Code, body
}

func TestGetStatus_HappyPath_BearerToken(t *testing.T) {
	t.Parallel()
	kai := newKaiInstance("acme", "Acme GmbH", "Robot Pilot", "secret-token", "Running", true, false)
	s := fakeDynServer(t, kai)

	code, body := doStatusReq(t, s, "acme", "secret-token", "")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%v)", code, body)
	}
	if body["status"] != "online" {
		t.Errorf("expected status=online, got %v", body["status"])
	}
	if body["customerName"] != "Acme GmbH" {
		t.Errorf("expected customerName=Acme GmbH, got %v", body["customerName"])
	}
}

func TestGetStatus_HappyPath_QueryToken(t *testing.T) {
	t.Parallel()
	kai := newKaiInstance("acme", "Acme GmbH", "Pilot", "qtok", "Running", true, false)
	s := fakeDynServer(t, kai)

	code, body := doStatusReq(t, s, "acme", "", "qtok")
	if code != http.StatusOK {
		t.Fatalf("expected 200 with query token, got %d (body=%v)", code, body)
	}
}

func TestGetStatus_NoToken_Returns401(t *testing.T) {
	t.Parallel()
	s := fakeDynServer(t, newKaiInstance("acme", "A", "B", "tok", "Running", true, false))
	code, _ := doStatusReq(t, s, "acme", "", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when no token, got %d", code)
	}
}

func TestGetStatus_WrongToken_Returns401(t *testing.T) {
	t.Parallel()
	s := fakeDynServer(t, newKaiInstance("acme", "A", "B", "right", "Running", true, false))
	code, body := doStatusReq(t, s, "acme", "wrong", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on wrong token, got %d", code)
	}
	// Anti-enumeration: same shape as not-found.
	if body["error"] != "unauthorized" {
		t.Errorf("expected unauthorized error body, got %v", body)
	}
}

func TestGetStatus_BadSlug_Returns401(t *testing.T) {
	t.Parallel()
	s := fakeDynServer(t)
	for _, slug := range []string{"BAD-CASE", "trailing-", "-leading", "with space"} {
		t.Run(slug, func(t *testing.T) {
			code, _ := doStatusReq(t, s, slug, "anything", "")
			if code != http.StatusUnauthorized {
				t.Errorf("expected 401 for bad slug %q, got %d", slug, code)
			}
		})
	}
}

func TestGetStatus_NotFound_Returns401(t *testing.T) {
	t.Parallel()
	// Anti-enumeration: missing slug must look identical to wrong-token.
	s := fakeDynServer(t)
	code, body := doStatusReq(t, s, "ghost", "anything", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing slug, got %d (body=%v)", code, body)
	}
}

func TestGetStatus_NoDynClient_Returns503(t *testing.T) {
	t.Parallel()
	s := &server{namespace: "emai-swarm"} // dyn intentionally nil
	code, _ := doStatusReq(t, s, "acme", "anything", "")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no dyn client, got %d", code)
	}
}

func TestTranslatePhase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		phase             string
		ready, suspended  bool
		wantStatus        string
		wantMessageHasNon string
	}{
		{"running-ready", "Running", true, false, "online", "operational"},
		{"running-not-ready", "Running", false, false, "setting-up", "almost"},
		{"provisioning", "Provisioning", false, false, "setting-up", "first time"},
		{"suspended-via-spec", "Running", true, true, "maintenance", "paused"},
		{"suspended-via-phase", "Suspended", false, false, "maintenance", "paused"},
		{"failed", "Failed", false, false, "issue", "investigating"},
		{"unknown-phase", "WhoKnows", false, false, "unknown", "unavailable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			gotStatus, gotMsg := translatePhase(c.phase, c.ready, c.suspended)
			if gotStatus != c.wantStatus {
				t.Errorf("status: got %q want %q", gotStatus, c.wantStatus)
			}
			if c.wantMessageHasNon != "" && !contains(gotMsg, c.wantMessageHasNon) {
				t.Errorf("message %q should contain %q", gotMsg, c.wantMessageHasNon)
			}
		})
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

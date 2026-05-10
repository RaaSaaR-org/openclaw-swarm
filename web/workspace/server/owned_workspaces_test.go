package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	"github.com/emai-ai/swarm/pkg/auth"
	"github.com/emai-ai/swarm/pkg/users"
)

// kaiObj is a tiny constructor for an unstructured KaiInstance with the
// fields handleOwnedWorkspaces reads. Exists so tests don't repeat the
// SetGroupVersionKind / NestedMap dance four times.
func kaiObj(slug, name, project, userID, appRef string, ready bool) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(schema.GroupVersionKind{Group: "swarm.emai.io", Version: "v1alpha2", Kind: "KaiInstance"})
	o.SetName("kai-" + slug)
	o.SetNamespace("swarm-system")
	o.SetLabels(map[string]string{"swarm.io/user-id": userID})
	spec := map[string]any{
		"managed":      "saas",
		"userRef":      userID,
		"customerName": name,
		"projectName":  project,
	}
	if appRef != "" {
		spec["appRef"] = appRef
	}
	_ = unstructured.SetNestedMap(o.Object, spec, "spec")
	status := "Provisioning"
	if ready {
		status = "Running"
	}
	_ = unstructured.SetNestedMap(o.Object, map[string]any{"phase": status, "ready": ready}, "status")
	return o
}

// newFixtureWithKaiObjects is a thin extension of newFixtureWithBinding that
// seeds the dynamic client with a list of KaiInstances. Used by the
// owned-workspaces tests where we need multiple CRs in the same namespace.
func newFixtureWithKaiObjects(t *testing.T, currentSlug, userID string, kais []*unstructured.Unstructured) *fixture {
	t.Helper()
	const ns = "swarm-system"

	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		kaiInstanceGVR: "KaiInstanceList",
	}
	objs := make([]runtime.Object, 0, len(kais))
	for _, k := range kais {
		objs = append(objs, k)
	}
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)

	// Reuse newFixtureWithBinding for everything ELSE (chat-bridge JWT secret,
	// users secret, MemoryStore) but swap the dynamic client for our seeded
	// version. binding-managed is "saas" so the JWT carries Uid.
	f := newFixtureWithBinding(t, currentSlug, nil, "saas", userID)
	f.server.dyn = dyn

	// Also seed a verified User row matching userID so /owner enrichment works
	// if a test exercises it.
	hash, _ := auth.HashPassword("password-not-used")
	if _, err := f.server.users.Create(context.Background(), users.CreateParams{
		Email: "owner@example.com", PasswordHash: hash, Tier: users.TierFree, Language: users.LangDE, App: users.DefaultApp,
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return f
}

// authedReq builds a request with a valid SaaS session cookie for the given
// slug + userId. Used as the table fixture for /owned-workspaces.
func authedReq(t *testing.T, f *fixture, method, path, slug, userID string) *http.Request {
	t.Helper()
	cookie, err := auth.IssueSessionWithUID(slug, "owner@example.com", userID, f.jwtSecret, time.Now())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	req := slugReq(method, path, slug, "")
	req.AddCookie(cookie)
	return req
}

func TestHandleOwnedWorkspaces_ListsOnlyUsersWorkspaces(t *testing.T) {
	t.Parallel()
	const userID = "u_alice"
	f := newFixtureWithKaiObjects(t, "primary", userID, []*unstructured.Unstructured{
		kaiObj("primary", "Primary", "Robotik", userID, "project-assistant", true),
		kaiObj("side-thing", "Side Thing", "Personal", userID, "personal-assistant", true),
		// Different user — must NOT show up in the response.
		kaiObj("not-mine", "Not Mine", "Other", "u_bob", "writing-coach", true),
	})

	req := authedReq(t, f, http.MethodGet, "/api/workspace/primary/owned-workspaces", "primary", userID)
	rr := httptest.NewRecorder()
	f.server.handleOwnedWorkspaces(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	var got ownedWorkspacesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Workspaces) != 2 {
		t.Fatalf("expected 2 workspaces, got %d (%+v)", len(got.Workspaces), got.Workspaces)
	}
	// Find the current one.
	var current *ownedWorkspace
	for i := range got.Workspaces {
		if got.Workspaces[i].Slug == "primary" {
			current = &got.Workspaces[i]
		}
	}
	if current == nil || !current.Current {
		t.Errorf("expected primary marked Current=true, got %+v", got.Workspaces)
	}
	for _, w := range got.Workspaces {
		if w.Slug == "not-mine" {
			t.Errorf("workspace from another user leaked into the response: %+v", w)
		}
	}
}

func TestHandleOwnedWorkspaces_ExposesStatusAndAppRef(t *testing.T) {
	t.Parallel()
	const userID = "u_alice"
	f := newFixtureWithKaiObjects(t, "primary", userID, []*unstructured.Unstructured{
		kaiObj("primary", "Primary", "Robotik", userID, "writing-coach", false),
	})
	req := authedReq(t, f, http.MethodGet, "/api/workspace/primary/owned-workspaces", "primary", userID)
	rr := httptest.NewRecorder()
	f.server.handleOwnedWorkspaces(rr, req)

	var got ownedWorkspacesResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.Workspaces) != 1 {
		t.Fatalf("want 1 workspace, got %d", len(got.Workspaces))
	}
	w := got.Workspaces[0]
	if w.Status != "setting-up" {
		t.Errorf("status = %q, want setting-up (Provisioning + ready=false)", w.Status)
	}
	if w.AppRef != "writing-coach" {
		t.Errorf("appRef = %q, want writing-coach", w.AppRef)
	}
}

func TestHandleOwnedWorkspaces_LegacySessionGetsEmpty(t *testing.T) {
	t.Parallel()
	// Internal-managed binding → JWT has no Uid → response is empty list,
	// not 401 (we don't want to surprise legacy users with a missing endpoint).
	const userID = "" // no Uid in claims = legacy
	f := newFixtureWithBinding(t, "legacy", nil, "internal", "")

	cookie, err := auth.IssueSession("legacy", "old-admin@example.com", f.jwtSecret, time.Now())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	req := slugReq(http.MethodGet, "/api/workspace/legacy/owned-workspaces", "legacy", "")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	f.server.handleOwnedWorkspaces(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var got ownedWorkspacesResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.Workspaces) != 0 {
		t.Errorf("legacy session should get empty list, got %+v", got.Workspaces)
	}
	_ = userID
}

func TestHandleOwnedWorkspaces_RejectsUnauthenticated(t *testing.T) {
	t.Parallel()
	f := newFixtureWithBinding(t, "primary", nil, "saas", "u_alice")
	req := slugReq(http.MethodGet, "/api/workspace/primary/owned-workspaces", "primary", "")
	rr := httptest.NewRecorder()
	f.server.handleOwnedWorkspaces(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

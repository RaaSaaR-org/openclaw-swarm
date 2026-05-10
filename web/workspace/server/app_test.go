package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/emai-ai/swarm/pkg/auth"
)

// writeCatalogFixture lays a minimal catalog tree under dir with one
// metadata.yaml per app. Returns the dir.
func writeCatalogFixture(t *testing.T, apps map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for slug, body := range apps {
		appDir := filepath.Join(root, slug)
		if err := os.MkdirAll(appDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", appDir, err)
		}
		if err := os.WriteFile(filepath.Join(appDir, "metadata.yaml"), []byte(body), 0o644); err != nil {
			t.Fatalf("write metadata: %v", err)
		}
	}
	return root
}

func TestLoadCatalog_ReadsAndSortsByDir(t *testing.T) {
	t.Parallel()
	dir := writeCatalogFixture(t, map[string]string{
		"writing-coach": `name: Writing Coach
nameDe: Schreibcoach
category: creative
shortDescription: Edits drafts.
shortDescriptionDe: Bearbeitet Drafts.
recommendedModel: openrouter/x
toolsProfile: messaging
tier: free
`,
		"coding-helper": `name: Coding Helper
category: development
shortDescription: Reads code.
toolsProfile: coding
tier: free
`,
	})
	got, err := loadCatalog(dir)
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 apps, got %d (%+v)", len(got), got)
	}
	if got[0].Slug != "coding-helper" || got[1].Slug != "writing-coach" {
		t.Errorf("expected sorted by slug, got %v / %v", got[0].Slug, got[1].Slug)
	}
	wc := got[1]
	if wc.NameDe != "Schreibcoach" {
		t.Errorf("nameDe = %q", wc.NameDe)
	}
	if wc.ShortDescriptionDe != "Bearbeitet Drafts." {
		t.Errorf("shortDescriptionDe = %q", wc.ShortDescriptionDe)
	}
	if wc.RecommendedModel != "openrouter/x" {
		t.Errorf("recommendedModel = %q", wc.RecommendedModel)
	}
	// English fallback when DE missing
	ch := got[0]
	if ch.NameDe != "Coding Helper" {
		t.Errorf("nameDe should fall back to name when nameDe absent, got %q", ch.NameDe)
	}
	if ch.ShortDescriptionDe != "Reads code." {
		t.Errorf("shortDescriptionDe should fall back, got %q", ch.ShortDescriptionDe)
	}
}

func TestLoadCatalog_SkipsNonAppDirs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// A subdir without metadata.yaml — must be skipped, not error.
	if err := os.MkdirAll(filepath.Join(root, "_template"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A file at the root — must be skipped.
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# catalog"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A real app dir with a non-DNS-safe name (uppercase letters) — must be skipped.
	if err := os.MkdirAll(filepath.Join(root, "BadName"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "BadName", "metadata.yaml"), []byte("name: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadCatalog(root)
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want 0 apps, got %d (%+v)", len(got), got)
	}
}

func TestLoadCatalog_MissingDirReturnsEmpty(t *testing.T) {
	t.Parallel()
	got, err := loadCatalog("/nonexistent/catalog/path")
	if err != nil {
		t.Errorf("missing dir should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %+v", got)
	}
}

func TestHandleListCatalog_Authed(t *testing.T) {
	dir := writeCatalogFixture(t, map[string]string{
		"personal-assistant": `name: Personal Assistant
nameDe: Persoenlicher Assistent
category: lifestyle
toolsProfile: messaging
tier: free
shortDescription: Day-to-day helper.
`,
	})
	t.Setenv("KAI_CATALOG_DIR", dir)

	const userID = "u_alice"
	f := newFixtureWithKaiObjects(t, "primary", userID, []*unstructured.Unstructured{
		kaiObj("primary", "Primary", "Robotik", userID, "personal-assistant", true),
	})
	req := authedReq(t, f, http.MethodGet, "/api/workspace/primary/catalog", "primary", userID)
	rr := httptest.NewRecorder()
	f.server.handleListCatalog(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	var got catalogResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Apps) != 1 || got.Apps[0].Slug != "personal-assistant" {
		t.Errorf("apps = %+v", got.Apps)
	}
}

func TestHandleListCatalog_Unauthed(t *testing.T) {
	t.Parallel()
	const userID = "u_alice"
	f := newFixtureWithKaiObjects(t, "primary", userID, []*unstructured.Unstructured{
		kaiObj("primary", "Primary", "Robotik", userID, "personal-assistant", true),
	})
	req := slugReq(http.MethodGet, "/api/workspace/primary/catalog", "primary", "")
	rr := httptest.NewRecorder()
	f.server.handleListCatalog(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestHandleSwitchApp_HappyPath(t *testing.T) {
	dir := writeCatalogFixture(t, map[string]string{
		"personal-assistant": `name: Personal Assistant
category: lifestyle
toolsProfile: messaging
tier: free
`,
		"writing-coach": `name: Writing Coach
category: creative
toolsProfile: messaging
tier: free
`,
	})
	t.Setenv("KAI_CATALOG_DIR", dir)

	const userID = "u_alice"
	f := newFixtureWithKaiObjects(t, "primary", userID, []*unstructured.Unstructured{
		kaiObj("primary", "Primary", "Robotik", userID, "personal-assistant", true),
	})
	body, _ := json.Marshal(switchAppRequest{AppRef: "writing-coach"})
	req := authedReq(t, f, http.MethodPatch, "/api/workspace/primary/app", "primary", userID)
	req.Body = http.NoBody
	req.Body = nopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	f.server.handleSwitchApp(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	var got switchAppResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.AppRef != "writing-coach" {
		t.Errorf("response appRef = %q", got.AppRef)
	}
	// Verify the CR was actually patched.
	live, err := f.server.dyn.Resource(kaiInstanceGVR).Namespace("swarm-system").Get(context.Background(), "kai-primary", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got2, _, _ := unstructured.NestedString(live.Object, "spec", "appRef")
	if got2 != "writing-coach" {
		t.Errorf("spec.appRef = %q, want writing-coach", got2)
	}
	if live.GetLabels()["swarm.io/app"] != "writing-coach" {
		t.Errorf("swarm.io/app label = %q", live.GetLabels()["swarm.io/app"])
	}
}

func TestHandleSwitchApp_RejectsUnknownApp(t *testing.T) {
	dir := writeCatalogFixture(t, map[string]string{
		"personal-assistant": `name: Personal Assistant
category: lifestyle
toolsProfile: messaging
tier: free
`,
	})
	t.Setenv("KAI_CATALOG_DIR", dir)

	const userID = "u_alice"
	f := newFixtureWithKaiObjects(t, "primary", userID, []*unstructured.Unstructured{
		kaiObj("primary", "Primary", "Robotik", userID, "personal-assistant", true),
	})
	body, _ := json.Marshal(switchAppRequest{AppRef: "nonexistent-app"})
	req := authedReq(t, f, http.MethodPatch, "/api/workspace/primary/app", "primary", userID)
	req.Body = nopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))

	rr := httptest.NewRecorder()
	f.server.handleSwitchApp(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown app, got %d (%s)", rr.Code, rr.Body.String())
	}
}

func TestHandleSwitchApp_RejectsCrossUserOwnership(t *testing.T) {
	dir := writeCatalogFixture(t, map[string]string{
		"writing-coach": "name: Writing Coach\ncategory: creative\ntoolsProfile: messaging\ntier: free\n",
	})
	t.Setenv("KAI_CATALOG_DIR", dir)

	// Workspace is owned by u_bob; the request comes from u_alice. Must 401.
	const owner = "u_bob"
	const attacker = "u_alice"
	f := newFixtureWithKaiObjects(t, "victim", attacker, []*unstructured.Unstructured{
		kaiObj("victim", "Bob's", "Some project", owner, "personal-assistant", true),
	})
	body, _ := json.Marshal(switchAppRequest{AppRef: "writing-coach"})
	req := authedReq(t, f, http.MethodPatch, "/api/workspace/victim/app", "victim", attacker)
	req.Body = nopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))

	rr := httptest.NewRecorder()
	f.server.handleSwitchApp(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for cross-user, got %d (%s)", rr.Code, rr.Body.String())
	}
	// CR must NOT have been patched.
	live, err := f.server.dyn.Resource(kaiInstanceGVR).Namespace("swarm-system").Get(context.Background(), "kai-victim", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, _, _ := unstructured.NestedString(live.Object, "spec", "appRef")
	if got != "personal-assistant" {
		t.Errorf("appRef leaked through cross-user attempt: %q", got)
	}
}

func TestHandleSwitchApp_RejectsLegacyInternalSession(t *testing.T) {
	dir := writeCatalogFixture(t, map[string]string{
		"writing-coach": "name: Writing Coach\ncategory: creative\ntoolsProfile: messaging\ntier: free\n",
	})
	t.Setenv("KAI_CATALOG_DIR", dir)

	// Internal-managed binding → JWT has no Uid → switch-app must refuse.
	f := newFixtureWithBinding(t, "legacy", nil, "internal", "")
	body, _ := json.Marshal(switchAppRequest{AppRef: "writing-coach"})
	req := slugReq(http.MethodPatch, "/api/workspace/legacy/app", "legacy", "")
	req.Body = nopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))

	// Internal session: legacy IssueSession (no Uid claim).
	cookie, err := auth.IssueSession("legacy", "old-admin@example.com", f.jwtSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	f.server.handleSwitchApp(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 for internal-managed, got %d (%s)", rr.Code, rr.Body.String())
	}
}

// --- small helpers below ---

type readCloser struct {
	*bytes.Reader
}

func (r *readCloser) Close() error { return nil }

func nopCloser(r *bytes.Reader) *readCloser { return &readCloser{r} }

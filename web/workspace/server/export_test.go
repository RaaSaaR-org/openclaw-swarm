package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	"github.com/emai-ai/swarm/pkg/auth"
	"github.com/emai-ai/swarm/pkg/users"
)

// rebuildDynWithKais swaps the fixture's dynamic client for a freshly-seeded
// fake holding the supplied KaiInstance objects. Lets a test compute the
// labels (e.g. with the store-generated User ID) AFTER the fixture is up.
func rebuildDynWithKais(t *testing.T, f *fixture, kais []*unstructured.Unstructured) {
	t.Helper()
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{kaiInstanceGVR: "KaiInstanceList"}
	objs := make([]runtime.Object, 0, len(kais))
	for _, k := range kais {
		objs = append(objs, k)
	}
	f.server.dyn = fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

// readZip is a tiny helper that decodes a zip blob into a name→bytes map.
// Tests assert on file presence + JSON shape, not on byte-exact output.
func readZip(t *testing.T, body []byte) map[string][]byte {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = b
	}
	return out
}

// TestExport_HappyPath — auth'd SaaS user can pull a zip with the User
// record + (empty) KaiInstance list. No Stripe configured, no instances.
func TestExport_HappyPath(t *testing.T) {
	t.Parallel()
	const userID = "u_alice"
	f := newFixtureWithBinding(t, "primary", nil, "saas", userID)
	hash, _ := auth.HashPassword("pw")
	if _, err := f.server.users.Create(context.Background(), users.CreateParams{
		Email: "alice@example.org", PasswordHash: hash, Tier: users.TierStarter, Language: users.LangDE, App: users.DefaultApp,
	}); err != nil {
		t.Fatal(err)
	}
	alice, _ := f.server.users.GetByEmail(context.Background(), "alice@example.org")

	cookie, err := auth.IssueSessionWithUID("primary", "alice@example.org", alice.ID, f.jwtSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req := slugReq(http.MethodGet, "/api/workspace/primary/account/export", "primary", "")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	f.server.handleAccountExport(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".zip") {
		t.Errorf("Content-Disposition = %q, expected attachment + .zip", cd)
	}

	files := readZip(t, rr.Body.Bytes())
	if _, ok := files["user.json"]; !ok {
		t.Errorf("zip missing user.json (got: %v)", keys(files))
	}
	if _, ok := files["kai-instances.json"]; !ok {
		t.Errorf("zip missing kai-instances.json")
	}
	if _, ok := files["README.txt"]; !ok {
		t.Errorf("zip missing README.txt")
	}
	// stripe/invoices.json must be absent — no Stripe configured.
	if _, ok := files["stripe/invoices.json"]; ok {
		t.Errorf("zip should NOT contain stripe/invoices.json without Stripe wiring")
	}

	var view exportUserView
	if err := json.Unmarshal(files["user.json"], &view); err != nil {
		t.Fatalf("user.json decode: %v", err)
	}
	if view.Email != "alice@example.org" {
		t.Errorf("user.json email = %q", view.Email)
	}
	if view.Tier != "starter" {
		t.Errorf("user.json tier = %q", view.Tier)
	}
	// PasswordHash MUST NOT appear in any file. Check the raw bytes.
	for name, body := range files {
		if bytes.Contains(body, []byte("argon2id")) {
			t.Errorf("file %s contains an argon2id hash — password hash leaked", name)
		}
		if bytes.Contains(body, []byte("PasswordHash")) {
			t.Errorf("file %s contains the literal PasswordHash field name", name)
		}
	}
}

// TestExport_ListsUserKaiInstances — when the dynamic client has labelled
// CRs for this user, the zip's kai-instances.json includes only theirs.
// Builds the fixture in two phases so the kai-objects' label matches the
// store-generated alice.ID rather than a hardcoded test constant.
func TestExport_ListsUserKaiInstances(t *testing.T) {
	t.Parallel()
	// Step 1: build a fixture with NO kai objects yet — we need alice's
	// real ULID before we can label the CRs correctly.
	f := newFixtureWithBinding(t, "primary", nil, "saas", "ignored-userref-on-binding")
	hash, _ := auth.HashPassword("pw")
	if _, err := f.server.users.Create(context.Background(), users.CreateParams{
		Email: "alice@example.org", PasswordHash: hash, Tier: users.TierFree, Language: users.LangDE, App: users.DefaultApp,
	}); err != nil {
		t.Fatal(err)
	}
	alice, _ := f.server.users.GetByEmail(context.Background(), "alice@example.org")

	// Step 2: re-seed the fixture's dyn client with kai objects labelled
	// with alice's actual ULID + a second user's instance for negative.
	rebuildDynWithKais(t, f, []*unstructured.Unstructured{
		kaiObj("primary", "Primary", "Robotik", alice.ID, "project-assistant", true),
		kaiObj("side", "Side", "Personal", alice.ID, "personal-assistant", true),
		kaiObj("not-mine", "Not Mine", "Other", "u_bob", "writing-coach", true),
	})

	cookie, err := auth.IssueSessionWithUID("primary", "alice@example.org", alice.ID, f.jwtSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req := slugReq(http.MethodGet, "/api/workspace/primary/account/export", "primary", "")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	f.server.handleAccountExport(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", rr.Code, rr.Body.String())
	}

	files := readZip(t, rr.Body.Bytes())
	var instances []kaiInstanceView
	if err := json.Unmarshal(files["kai-instances.json"], &instances); err != nil {
		t.Fatalf("kai-instances.json decode: %v\n%s", err, files["kai-instances.json"])
	}
	if len(instances) != 2 {
		t.Errorf("expected 2 instances (alice's only), got %d", len(instances))
	}
	for _, inst := range instances {
		if !strings.HasPrefix(inst.Name, "kai-") {
			t.Errorf("instance name = %q, expected kai- prefix", inst.Name)
		}
		if inst.Name == "kai-not-mine" {
			t.Errorf("export leaked another user's instance: %v", inst.Name)
		}
	}
}

// TestExport_RejectsLegacyInternalSession — internal-managed tenants don't
// have a central User row, so there's nothing to export. 403 matches the
// account-deletion endpoint's behavior on legacy sessions.
func TestExport_RejectsLegacyInternalSession(t *testing.T) {
	t.Parallel()
	f := newFixtureWithBinding(t, "primary", nil, "saas", "u_alice")
	cookie, err := auth.IssueSession("primary", "old-admin@example.com", f.jwtSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req := slugReq(http.MethodGet, "/api/workspace/primary/account/export", "primary", "")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	f.server.handleAccountExport(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

// TestExport_RequiresAuth — no cookie → 401.
func TestExport_RequiresAuth(t *testing.T) {
	t.Parallel()
	f := newFixtureWithBinding(t, "primary", nil, "saas", "u_alice")
	req := slugReq(http.MethodGet, "/api/workspace/primary/account/export", "primary", "")
	rr := httptest.NewRecorder()
	f.server.handleAccountExport(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

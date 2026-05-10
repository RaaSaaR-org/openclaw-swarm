package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/emai-ai/swarm/pkg/auth"
	"github.com/emai-ai/swarm/pkg/users"
)

// setKaiSuspended flips spec.suspended on the fixture's KaiInstance via the
// fake dynamic client so tests can model an idle-suspended workspace.
func setKaiSuspended(t *testing.T, f *fixture, slug string, suspended bool) {
	t.Helper()
	obj, err := f.server.dyn.Resource(kaiInstanceGVR).Namespace(f.server.namespace).Get(
		context.Background(), "kai-"+slug, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get kai-%s: %v", slug, err)
	}
	if err := unstructured.SetNestedField(obj.Object, suspended, "spec", "suspended"); err != nil {
		t.Fatalf("set spec.suspended: %v", err)
	}
	if _, err := f.server.dyn.Resource(kaiInstanceGVR).Namespace(f.server.namespace).Update(
		context.Background(), obj, metav1.UpdateOptions{},
	); err != nil {
		t.Fatalf("update kai-%s: %v", slug, err)
	}
}

// kaiSuspended reads spec.suspended on the fixture's KaiInstance.
func kaiSuspended(t *testing.T, f *fixture, slug string) bool {
	t.Helper()
	obj, err := f.server.dyn.Resource(kaiInstanceGVR).Namespace(f.server.namespace).Get(
		context.Background(), "kai-"+slug, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get kai-%s: %v", slug, err)
	}
	got, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspended")
	return got
}

// seedSaaSUser stuffs the workspace's MemoryStore with a verified user and
// returns the row. The test then exercises the SaaS login branch.
func seedSaaSUser(t *testing.T, f *fixture, email, password string) *users.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := f.server.users.Create(context.Background(), users.CreateParams{
		Email:        email,
		PasswordHash: hash,
		Tier:         users.TierFree,
		Language:     users.LangDE,
		App:          users.DefaultApp,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := f.server.users.MarkEmailVerified(context.Background(), u.ID, time.Now().UTC()); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Re-read so EmailVerifiedAt is populated.
	got, err := f.server.users.GetByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return got
}

func TestHandleLogin_SaaS_AcceptsVerifiedUser(t *testing.T) {
	t.Parallel()
	// First seed the user so we know the ID, then bind the workspace to it.
	tmp := newFixtureWithBinding(t, "acme", nil, "saas", "u_placeholder")
	u := seedSaaSUser(t, tmp, "alice@acme.de", "correct horse battery")

	f := newFixtureWithBinding(t, "acme", nil, "saas", u.ID)
	// Reuse the seeded store so the second fixture sees the same user.
	f.server.users = tmp.server.users

	req := slugReq(http.MethodPost, "/api/workspace/acme/login", "acme",
		`{"email":"alice@acme.de","password":"correct horse battery"}`)
	rr := httptest.NewRecorder()
	f.server.handleLogin(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	cookies := rr.Result().Cookies()
	var sess *http.Cookie
	for _, c := range cookies {
		if c.Name == auth.SessionCookieName {
			sess = c
		}
	}
	if sess == nil {
		t.Fatalf("expected session cookie, got %v", cookies)
	}
	// JWT carries the platform user id so /owner can read it without re-querying the store.
	parsed, err := auth.ParseJWT(sess.Value, f.jwtSecret)
	if err != nil {
		t.Fatalf("parse jwt: %v", err)
	}
	if parsed.Uid != u.ID {
		t.Errorf("claims.Uid = %q, want %q (the platform user ID)", parsed.Uid, u.ID)
	}
	if parsed.Sub != "alice@acme.de" {
		t.Errorf("claims.Sub = %q, want alice@acme.de", parsed.Sub)
	}
}

func TestHandleLogin_SaaS_RejectsUnverifiedUser(t *testing.T) {
	t.Parallel()
	tmp := newFixtureWithBinding(t, "acme", nil, "saas", "u_placeholder")
	hash, _ := auth.HashPassword("correct horse")
	u, _ := tmp.server.users.Create(context.Background(), users.CreateParams{
		Email: "alice@acme.de", PasswordHash: hash, Tier: users.TierFree, Language: users.LangDE, App: users.DefaultApp,
	})
	// Skip MarkEmailVerified — that's the whole point of this test.

	f := newFixtureWithBinding(t, "acme", nil, "saas", u.ID)
	f.server.users = tmp.server.users

	req := slugReq(http.MethodPost, "/api/workspace/acme/login", "acme",
		`{"email":"alice@acme.de","password":"correct horse"}`)
	rr := httptest.NewRecorder()
	f.server.handleLogin(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unverified user, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestHandleLogin_SaaS_RejectsUserNotOwningWorkspace(t *testing.T) {
	t.Parallel()
	// Verified user, but the workspace's userRef points elsewhere — the user
	// must not be able to log in to a workspace they don't own.
	tmp := newFixtureWithBinding(t, "acme", nil, "saas", "u_placeholder")
	seedSaaSUser(t, tmp, "alice@acme.de", "correct horse")

	f := newFixtureWithBinding(t, "acme", nil, "saas", "u_someone_else")
	f.server.users = tmp.server.users

	req := slugReq(http.MethodPost, "/api/workspace/acme/login", "acme",
		`{"email":"alice@acme.de","password":"correct horse"}`)
	rr := httptest.NewRecorder()
	f.server.handleLogin(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for cross-workspace login, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestHandleAuthInfo_SaaSWorkspace_DoesNotPromptBootstrap(t *testing.T) {
	t.Parallel()
	// Empty users Secret on a SaaS workspace MUST NOT trigger the
	// bootstrap-admin form — that flow is internal-only.
	f := newFixtureWithBinding(t, "acme", nil, "saas", "u_x")

	req := slugReq(http.MethodGet, "/api/workspace/acme/auth", "acme", "")
	rr := httptest.NewRecorder()
	f.server.handleAuthInfo(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["needsSetup"] != false {
		t.Errorf("expected needsSetup=false on SaaS, got %v", body)
	}
}

func TestHandleOwner_ReturnsClaimsAndStoreEnrichment(t *testing.T) {
	t.Parallel()
	tmp := newFixtureWithBinding(t, "acme", nil, "saas", "u_placeholder")
	u := seedSaaSUser(t, tmp, "alice@acme.de", "correct horse")

	f := newFixtureWithBinding(t, "acme", nil, "saas", u.ID)
	f.server.users = tmp.server.users

	// Sign in to obtain a real cookie.
	loginReq := slugReq(http.MethodPost, "/api/workspace/acme/login", "acme",
		`{"email":"alice@acme.de","password":"correct horse"}`)
	loginRR := httptest.NewRecorder()
	f.server.handleLogin(loginRR, loginReq)
	if loginRR.Code != http.StatusOK {
		t.Fatalf("login: %d (%s)", loginRR.Code, loginRR.Body.String())
	}
	var sess *http.Cookie
	for _, c := range loginRR.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			sess = c
		}
	}

	ownerReq := slugReq(http.MethodGet, "/api/workspace/acme/owner", "acme", "")
	ownerReq.AddCookie(sess)
	ownerRR := httptest.NewRecorder()
	f.server.handleOwner(ownerRR, ownerReq)

	if ownerRR.Code != http.StatusOK {
		t.Fatalf("owner: %d (%s)", ownerRR.Code, ownerRR.Body.String())
	}
	var got ownerResponse
	if err := json.Unmarshal(ownerRR.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Email != "alice@acme.de" {
		t.Errorf("Email = %q, want alice@acme.de", got.Email)
	}
	if got.UserID != u.ID {
		t.Errorf("UserID = %q, want %q", got.UserID, u.ID)
	}
	if got.Tier != users.TierFree {
		t.Errorf("Tier = %q, want free", got.Tier)
	}
	if got.Managed != "saas" {
		t.Errorf("Managed = %q, want saas", got.Managed)
	}
}

func TestHandleOwner_RejectsUnauthenticated(t *testing.T) {
	t.Parallel()
	f := newFixtureWithBinding(t, "acme", nil, "saas", "u_x")
	req := slugReq(http.MethodGet, "/api/workspace/acme/owner", "acme", "")
	rr := httptest.NewRecorder()
	f.server.handleOwner(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// TASK-015 Phase 3.B: a successful SaaS login on a suspended workspace must
// flip spec.suspended back to false so the operator wakes the deployment up.
func TestHandleLogin_SaaS_ResumesSuspendedWorkspaceOnLogin(t *testing.T) {
	t.Parallel()
	tmp := newFixtureWithBinding(t, "acme", nil, "saas", "u_placeholder")
	u := seedSaaSUser(t, tmp, "alice@acme.de", "correct horse")

	f := newFixtureWithBinding(t, "acme", nil, "saas", u.ID)
	f.server.users = tmp.server.users
	setKaiSuspended(t, f, "acme", true)

	req := slugReq(http.MethodPost, "/api/workspace/acme/login", "acme",
		`{"email":"alice@acme.de","password":"correct horse"}`)
	rr := httptest.NewRecorder()
	f.server.handleLogin(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("login: %d (%s)", rr.Code, rr.Body.String())
	}
	if got := kaiSuspended(t, f, "acme"); got != false {
		t.Fatalf("spec.suspended after login = %v, want false (login should auto-resume)", got)
	}
}

// Workspace already running: login must not issue a no-op patch that bumps
// the resourceVersion for nothing — keeps the resume contract crisp and
// avoids unnecessary reconciles on the operator.
func TestHandleLogin_SaaS_DoesNotPatchWhenNotSuspended(t *testing.T) {
	t.Parallel()
	tmp := newFixtureWithBinding(t, "acme", nil, "saas", "u_placeholder")
	u := seedSaaSUser(t, tmp, "alice@acme.de", "correct horse")

	f := newFixtureWithBinding(t, "acme", nil, "saas", u.ID)
	f.server.users = tmp.server.users
	// spec.suspended absent → reads as false; binding.Suspended=false →
	// resumeWorkspace must not be called.

	// Track Patch calls on the fake dynamic client.
	var patchCalls int
	if fc, ok := f.server.dyn.(interface {
		PrependReactor(verb, resource string, reaction k8stesting.ReactionFunc)
	}); ok {
		fc.PrependReactor("patch", "kaiinstances", func(action k8stesting.Action) (bool, runtime.Object, error) {
			patchCalls++
			return false, nil, nil // let the default reactor run
		})
	} else {
		t.Fatal("dynamic client does not expose PrependReactor — fixture changed")
	}

	req := slugReq(http.MethodPost, "/api/workspace/acme/login", "acme",
		`{"email":"alice@acme.de","password":"correct horse"}`)
	rr := httptest.NewRecorder()
	f.server.handleLogin(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("login: %d (%s)", rr.Code, rr.Body.String())
	}
	if patchCalls != 0 {
		t.Fatalf("expected 0 patch calls when workspace is not suspended, got %d", patchCalls)
	}
}

// Resume is best-effort: a Patch failure in the K8s API must not turn a valid
// login into a 5xx. The user gets their cookie; they can retry login or ask
// support if the workspace stays asleep.
func TestHandleLogin_SaaS_ResumePatchFailureDoesNotBlockLogin(t *testing.T) {
	t.Parallel()
	tmp := newFixtureWithBinding(t, "acme", nil, "saas", "u_placeholder")
	u := seedSaaSUser(t, tmp, "alice@acme.de", "correct horse")

	f := newFixtureWithBinding(t, "acme", nil, "saas", u.ID)
	f.server.users = tmp.server.users
	setKaiSuspended(t, f, "acme", true)

	if fc, ok := f.server.dyn.(interface {
		PrependReactor(verb, resource string, reaction k8stesting.ReactionFunc)
	}); ok {
		fc.PrependReactor("patch", "kaiinstances", func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("simulated apiserver outage")
		})
	} else {
		t.Fatal("dynamic client does not expose PrependReactor")
	}

	req := slugReq(http.MethodPost, "/api/workspace/acme/login", "acme",
		`{"email":"alice@acme.de","password":"correct horse"}`)
	rr := httptest.NewRecorder()
	f.server.handleLogin(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("login: %d (%s) — patch failures must not block login", rr.Code, rr.Body.String())
	}
	cookies := rr.Result().Cookies()
	var sess *http.Cookie
	for _, c := range cookies {
		if c.Name == auth.SessionCookieName {
			sess = c
		}
	}
	if sess == nil {
		t.Fatalf("expected session cookie even when resume patch failed, got %v", cookies)
	}
}

// loadKaiBinding must parse spec.suspended; the login handler relies on it
// to gate the resume call.
func TestLoadKaiBinding_ParsesSuspended(t *testing.T) {
	t.Parallel()
	f := newFixtureWithBinding(t, "acme", nil, "saas", "u_x")
	setKaiSuspended(t, f, "acme", true)

	binding, err := f.server.loadKaiBinding(context.Background(), "acme")
	if err != nil {
		t.Fatalf("loadKaiBinding: %v", err)
	}
	if !binding.Suspended {
		t.Errorf("Suspended = false, want true")
	}
}

func TestKaiBinding_IsSaaSContract(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		binding kaiBinding
		want    bool
	}{
		{"saas+userRef", kaiBinding{Managed: "saas", UserRef: "u_x"}, true},
		{"saas missing userRef", kaiBinding{Managed: "saas"}, false},
		{"internal+userRef ignored", kaiBinding{Managed: "internal", UserRef: "u_x"}, false},
		{"empty", kaiBinding{}, false},
		{"unrecognized managed", kaiBinding{Managed: "weird", UserRef: "u_x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.binding.IsSaaS(); got != tc.want {
				t.Errorf("IsSaaS() = %v, want %v", got, tc.want)
			}
		})
	}
}

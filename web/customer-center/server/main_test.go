package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	corefake "k8s.io/client-go/kubernetes/fake"

	pkgauth "github.com/emai-ai/swarm/pkg/auth"
)

// fixture holds the most common test setup: one KaiInstance with a chat-bridge
// Secret (JWT) and a (possibly empty) users Secret.
type fixture struct {
	server     *server
	jwtSecret  []byte
	clientCore kubernetes.Interface
}

func newFixture(t *testing.T, slug string, users []userRecord) *fixture {
	t.Helper()
	const ns = "emai-swarm"

	// Dynamic client for KaiInstances. Most center handlers don't need KaiInstance
	// access; the auth handlers + handleAuthInfo are what touch the users Secret.
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		kaiInstanceGVR: "KaiInstanceList",
	}
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)

	// Typed core client with the chat-bridge JWT Secret + optional users Secret.
	jwtSecret := []byte("test-jwt-secret-32-bytes-long-xxxx")
	chatBridge := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kai-" + slug + "-chat-bridge", Namespace: ns},
		Data:       map[string][]byte{"jwt-secret": jwtSecret},
	}
	usersJSON, _ := json.Marshal(users)
	usersSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kai-" + slug + "-users", Namespace: ns},
		Data:       map[string][]byte{"users.json": usersJSON},
	}
	core := corefake.NewSimpleClientset(chatBridge, usersSecret)

	return &fixture{
		server:     &server{dyn: dyn, core: core, namespace: ns, revoker: pkgauth.NewMemoryRevoker()},
		jwtSecret:  jwtSecret,
		clientCore: core,
	}
}

func newReq(method, path, body string) *http.Request {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func slugReq(method, path, slug, body string) *http.Request {
	req := newReq(method, path, body)
	req.SetPathValue("slug", slug)
	return req
}

func TestHandleLogin_BootstrapAdmin_CreatesFirstUserAndIssuesCookie(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "acme", nil) // empty users → bootstrap path
	req := slugReq(http.MethodPost, "/api/center/acme/login", "acme",
		`{"email":"alice@acme.de","password":"correct horse"}`)
	rr := httptest.NewRecorder()
	f.server.handleLogin(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 (bootstrap), got %d (body=%s)", rr.Code, rr.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["setup"] != "true" {
		t.Errorf("expected setup=true on bootstrap, got %v", body)
	}
	// Cookie set?
	cookies := rr.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == pkgauth.SessionCookieName && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected session cookie to be set on bootstrap")
	}
	// User landed in the Secret with a real argon2 hash.
	users, _, err := f.server.readUsersSecret(context.Background(), "acme")
	if err != nil {
		t.Fatalf("readUsersSecret: %v", err)
	}
	if len(users) != 1 || users[0].Email != "alice@acme.de" {
		t.Fatalf("expected one user alice, got %+v", users)
	}
	if !strings.HasPrefix(users[0].PasswordHash, "$argon2id$") {
		t.Errorf("expected argon2id PHC hash, got %q", users[0].PasswordHash)
	}
}

func TestHandleLogin_NormalPath_AcceptsCorrectPassword(t *testing.T) {
	t.Parallel()
	hash, err := pkgauth.HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	f := newFixture(t, "acme", []userRecord{{Email: "alice@acme.de", PasswordHash: hash, CreatedAt: now, PasswordUpdatedAt: now}})

	req := slugReq(http.MethodPost, "/api/center/acme/login", "acme",
		`{"email":"alice@acme.de","password":"correct horse"}`)
	rr := httptest.NewRecorder()
	f.server.handleLogin(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestHandleLogin_WrongPassword_401(t *testing.T) {
	t.Parallel()
	hash, _ := pkgauth.HashPassword("right")
	now := time.Now().UTC().Format(time.RFC3339)
	f := newFixture(t, "acme", []userRecord{{Email: "alice@acme.de", PasswordHash: hash, CreatedAt: now, PasswordUpdatedAt: now}})
	req := slugReq(http.MethodPost, "/api/center/acme/login", "acme",
		`{"email":"alice@acme.de","password":"wrong"}`)
	rr := httptest.NewRecorder()
	f.server.handleLogin(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on wrong password, got %d", rr.Code)
	}
}

func TestHandleLogin_UnknownUser_401(t *testing.T) {
	t.Parallel()
	hash, _ := pkgauth.HashPassword("any")
	f := newFixture(t, "acme", []userRecord{{Email: "alice@acme.de", PasswordHash: hash}})
	req := slugReq(http.MethodPost, "/api/center/acme/login", "acme",
		`{"email":"ghost@acme.de","password":"any"}`)
	rr := httptest.NewRecorder()
	f.server.handleLogin(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unknown user, got %d", rr.Code)
	}
}

func TestHandleLogin_BadSlug_401(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "acme", nil)
	req := slugReq(http.MethodPost, "/api/center/x/login", "BAD-SLUG",
		`{"email":"alice@acme.de","password":"any"}`)
	rr := httptest.NewRecorder()
	f.server.handleLogin(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad slug, got %d", rr.Code)
	}
}

func TestHandleLogout_ClearsCookie(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "acme", nil)
	rr := httptest.NewRecorder()
	f.server.handleLogout(rr, newReq(http.MethodPost, "/api/center/acme/logout", ""))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
	cookies := rr.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == pkgauth.SessionCookieName && c.MaxAge < 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("expected an expiring session cookie on logout")
	}
}

func TestHandleLogout_RevokesJtiSoStolenCookieIsRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "acme", []userRecord{{Email: "alice@acme.de", PasswordHash: "x"}})

	cookie, err := pkgauth.IssueSession("acme", "alice@acme.de", f.jwtSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: cookie authenticates before logout.
	authInfoReq := slugReq(http.MethodGet, "/api/center/acme/auth-info", "acme", "")
	authInfoReq.AddCookie(cookie)
	rr := httptest.NewRecorder()
	f.server.handleAuthInfo(rr, authInfoReq)
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["authenticated"] != true {
		t.Fatalf("pre-logout: expected authenticated, got %v", body)
	}

	// Logout (with the cookie attached so the server can read the jti).
	logoutReq := slugReq(http.MethodPost, "/api/center/acme/logout", "acme", "")
	logoutReq.AddCookie(cookie)
	rr = httptest.NewRecorder()
	f.server.handleLogout(rr, logoutReq)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("logout: expected 204, got %d", rr.Code)
	}

	// Replay the same cookie — server must now reject it as revoked.
	rr = httptest.NewRecorder()
	authInfoReq2 := slugReq(http.MethodGet, "/api/center/acme/auth-info", "acme", "")
	authInfoReq2.AddCookie(cookie)
	f.server.handleAuthInfo(rr, authInfoReq2)
	body = nil
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["authenticated"] != false {
		t.Fatalf("post-logout replay: expected authenticated=false, got %v", body)
	}
}

func TestHandleAuthInfo_BootstrapNeeded(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "acme", nil)
	rr := httptest.NewRecorder()
	f.server.handleAuthInfo(rr, slugReq(http.MethodGet, "/api/center/acme/auth-info", "acme", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["authenticated"] != false || body["needsSetup"] != true {
		t.Errorf("expected unauth + needsSetup=true, got %v", body)
	}
}

func TestHandleAuthInfo_AuthedSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "acme", []userRecord{{Email: "alice@acme.de", PasswordHash: "x"}})
	cookie, err := pkgauth.IssueSession("acme", "alice@acme.de", f.jwtSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req := slugReq(http.MethodGet, "/api/center/acme/auth-info", "acme", "")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	f.server.handleAuthInfo(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["authenticated"] != true || body["email"] != "alice@acme.de" {
		t.Errorf("expected authed, got %v", body)
	}
}

func TestListUsers_RequiresAuth(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "acme", []userRecord{{Email: "alice@acme.de", PasswordHash: "x"}})
	req := slugReq(http.MethodGet, "/api/center/acme/users", "acme", "")
	rr := httptest.NewRecorder()
	f.server.listUsers(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", rr.Code)
	}
}

func TestListUsers_AuthedReturnsRedactedList(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Format(time.RFC3339)
	users := []userRecord{
		{Email: "alice@acme.de", PasswordHash: "secret-hash-1", CreatedAt: now, PasswordUpdatedAt: now},
		{Email: "bob@acme.de", PasswordHash: "secret-hash-2", CreatedAt: now, PasswordUpdatedAt: now},
	}
	f := newFixture(t, "acme", users)

	cookie, _ := pkgauth.IssueSession("acme", "alice@acme.de", f.jwtSecret, time.Now())
	req := slugReq(http.MethodGet, "/api/center/acme/users", "acme", "")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	f.server.listUsers(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	var got []userPublic
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 2 {
		t.Fatalf("expected 2 users, got %d", len(got))
	}
	for _, u := range got {
		if strings.Contains(rr.Body.String(), "secret-hash-") {
			t.Fatal("password hash leaked into list response")
		}
		_ = u
	}
}

func TestAddUser_HappyPath(t *testing.T) {
	t.Parallel()
	hash, _ := pkgauth.HashPassword("seed-pw")
	now := time.Now().UTC().Format(time.RFC3339)
	f := newFixture(t, "acme", []userRecord{{Email: "admin@acme.de", PasswordHash: hash, CreatedAt: now, PasswordUpdatedAt: now}})

	cookie, _ := pkgauth.IssueSession("acme", "admin@acme.de", f.jwtSecret, time.Now())
	req := slugReq(http.MethodPost, "/api/center/acme/users", "acme",
		`{"email":"new@acme.de","password":"another-pw"}`)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	f.server.addUser(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	users, _, err := f.server.readUsersSecret(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users after add, got %d", len(users))
	}
}

func TestAddUser_DuplicateEmail_409(t *testing.T) {
	t.Parallel()
	hash, _ := pkgauth.HashPassword("pw")
	f := newFixture(t, "acme", []userRecord{{Email: "alice@acme.de", PasswordHash: hash}})
	cookie, _ := pkgauth.IssueSession("acme", "alice@acme.de", f.jwtSecret, time.Now())
	req := slugReq(http.MethodPost, "/api/center/acme/users", "acme",
		`{"email":"alice@acme.de","password":"new-pw-strong"}`)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	f.server.addUser(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 on duplicate, got %d", rr.Code)
	}
}

func TestAddUser_WeakPassword_400(t *testing.T) {
	t.Parallel()
	hash, _ := pkgauth.HashPassword("pw")
	f := newFixture(t, "acme", []userRecord{{Email: "admin@acme.de", PasswordHash: hash}})
	cookie, _ := pkgauth.IssueSession("acme", "admin@acme.de", f.jwtSecret, time.Now())
	req := slugReq(http.MethodPost, "/api/center/acme/users", "acme",
		`{"email":"new@acme.de","password":"short"}`)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	f.server.addUser(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for short password, got %d", rr.Code)
	}
}

func TestRemoveUser_HappyPathAndIdempotent(t *testing.T) {
	t.Parallel()
	hash, _ := pkgauth.HashPassword("pw")
	f := newFixture(t, "acme", []userRecord{
		{Email: "admin@acme.de", PasswordHash: hash},
		{Email: "old@acme.de", PasswordHash: hash},
	})
	cookie, _ := pkgauth.IssueSession("acme", "admin@acme.de", f.jwtSecret, time.Now())

	req := slugReq(http.MethodDelete, "/api/center/acme/users/old@acme.de", "acme", "")
	req.SetPathValue("email", "old@acme.de")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	f.server.removeUser(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	users, _, _ := f.server.readUsersSecret(context.Background(), "acme")
	if len(users) != 1 || users[0].Email != "admin@acme.de" {
		t.Fatalf("expected only admin to remain, got %+v", users)
	}

	// Idempotent: deleting again should still return 204.
	rr2 := httptest.NewRecorder()
	req2 := slugReq(http.MethodDelete, "/api/center/acme/users/old@acme.de", "acme", "")
	req2.SetPathValue("email", "old@acme.de")
	req2.AddCookie(cookie)
	f.server.removeUser(rr2, req2)
	if rr2.Code != http.StatusNoContent {
		t.Fatalf("idempotency: second delete should return 204, got %d", rr2.Code)
	}
}

func TestResetPassword_HappyPathAndUnknownUser(t *testing.T) {
	t.Parallel()
	hash, _ := pkgauth.HashPassword("old-pw")
	f := newFixture(t, "acme", []userRecord{
		{Email: "admin@acme.de", PasswordHash: hash},
	})
	cookie, _ := pkgauth.IssueSession("acme", "admin@acme.de", f.jwtSecret, time.Now())

	// Happy path
	req := slugReq(http.MethodPost, "/api/center/acme/users/admin@acme.de/reset-password", "acme",
		`{"password":"new-pw-stronger"}`)
	req.SetPathValue("email", "admin@acme.de")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	f.server.resetPassword(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	users, _, _ := f.server.readUsersSecret(context.Background(), "acme")
	if !pkgauth.VerifyArgon2id("new-pw-stronger", users[0].PasswordHash) {
		t.Fatal("new password should verify against the stored hash")
	}
	if pkgauth.VerifyArgon2id("old-pw", users[0].PasswordHash) {
		t.Fatal("old password should no longer verify")
	}

	// Unknown user → 404
	req2 := slugReq(http.MethodPost, "/api/center/acme/users/ghost@acme.de/reset-password", "acme",
		`{"password":"some-pw-xx"}`)
	req2.SetPathValue("email", "ghost@acme.de")
	req2.AddCookie(cookie)
	rr2 := httptest.NewRecorder()
	f.server.resetPassword(rr2, req2)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown user, got %d", rr2.Code)
	}
}

func TestValidEmailAndPassword(t *testing.T) {
	t.Parallel()
	if !validEmail("alice@acme.de") {
		t.Error("alice@acme.de should be valid")
	}
	for _, bad := range []string{"", "notanemail", "@no-local", "no-at.com", strings.Repeat("a", 250) + "@x.com"} {
		if validEmail(bad) {
			t.Errorf("%q should not validate", bad)
		}
	}
	if !validPassword("12345678") || validPassword("short") || validPassword(strings.Repeat("a", 1025)) {
		t.Error("validPassword bounds wrong")
	}
}

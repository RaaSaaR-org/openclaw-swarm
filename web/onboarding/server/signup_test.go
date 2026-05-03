package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	"github.com/emai-ai/swarm/pkg/email"
	"github.com/emai-ai/swarm/pkg/users"
)

// captureSender is a tiny in-memory email.Sender for tests so we can assert
// what got rendered without hitting disk.
type captureSender struct {
	last *email.Message
}

func (c *captureSender) Send(_ context.Context, m email.Message) error {
	c.last = &m
	return nil
}

func newSignupServer(t *testing.T) (*server, *captureSender) {
	t.Helper()
	cap := &captureSender{}
	// Fake dynamic client so provision-on-verify (TASK-013 Phase 1.A)
	// has somewhere to land its KaiInstance. Empty list-kinds map for
	// kaiInstanceGVR so List ops don't 404 in unrelated codepaths.
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		kaiInstanceGVR: "KaiInstanceList",
	}
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	s := &server{
		namespace: "emai-swarm",
		dyn:       dyn,
		users:     users.NewMemoryStore(),
		email:     cap,
		signup: signupConfig{
			Enabled:       true,
			Secret:        []byte("test-hmac-secret-32-bytes-long!!"),
			VerifyBaseURL: "https://kai.example.org/api/signup",
			VerifyTTL:     24 * time.Hour,
			From:          "Kai <noreply@kai.example.org>",
			IPLimitPerHr:  5,
			Captcha:       noopCaptcha{},
		},
		rl: newRateLimiter(5),
	}
	return s, cap
}

func signupReq(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/signup", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "10.0.0.1:12345"
	return r
}

func TestHandleSignupHappyPath(t *testing.T) {
	t.Parallel()
	s, cap := newSignupServer(t)
	rr := httptest.NewRecorder()
	s.handleSignup(rr, signupReq(`{"email":"alice@example.org","password":"correct horse"}`))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if cap.last == nil {
		t.Fatal("expected verify email to be sent")
	}
	if cap.last.To != "alice@example.org" {
		t.Errorf("To = %q, want alice@example.org", cap.last.To)
	}
	if !strings.Contains(cap.last.HTML, "https://kai.example.org/api/signup/verify?id=u_") {
		t.Errorf("html missing verify link, got: %s", cap.last.HTML)
	}
	// User row exists, unverified.
	u, err := s.users.GetByEmail(context.Background(), "alice@example.org")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if u.EmailVerifiedAt != nil {
		t.Error("user must be unverified before clicking the link")
	}
	if u.Tier != users.TierFree {
		t.Errorf("tier = %q, want free", u.Tier)
	}
}

func TestHandleSignupDisabledReturns404(t *testing.T) {
	t.Parallel()
	s, _ := newSignupServer(t)
	s.signup.Enabled = false
	rr := httptest.NewRecorder()
	s.handleSignup(rr, signupReq(`{"email":"a@b.org","password":"correct horse"}`))
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestHandleSignupValidations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		body    string
		want    int
		matchIn string
	}{
		{"bad json", `{not json`, http.StatusBadRequest, "invalid JSON"},
		{"bad email", `{"email":"not-an-email","password":"correct horse"}`, http.StatusBadRequest, "invalid email"},
		{"disposable", `{"email":"abc@mailinator.com","password":"correct horse"}`, http.StatusBadRequest, "disposable"},
		{"short pw", `{"email":"a@b.org","password":"x"}`, http.StatusBadRequest, "8-1024"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := newSignupServer(t)
			rr := httptest.NewRecorder()
			s.handleSignup(rr, signupReq(c.body))
			if rr.Code != c.want {
				t.Errorf("status = %d, want %d (body=%s)", rr.Code, c.want, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), c.matchIn) {
				t.Errorf("body %q does not contain %q", rr.Body.String(), c.matchIn)
			}
		})
	}
}

func TestHandleSignupRateLimit(t *testing.T) {
	t.Parallel()
	s, _ := newSignupServer(t)
	for i := 0; i < 5; i++ {
		rr := httptest.NewRecorder()
		body := `{"email":"a` + itoa(i) + `@example.org","password":"correct horse"}`
		s.handleSignup(rr, signupReq(body))
		if rr.Code != http.StatusAccepted {
			t.Fatalf("call #%d: expected 202, got %d", i, rr.Code)
		}
	}
	// 6th call from the same IP must be throttled.
	rr := httptest.NewRecorder()
	s.handleSignup(rr, signupReq(`{"email":"a6@example.org","password":"correct horse"}`))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after 5 signups, got %d", rr.Code)
	}
}

func TestHandleSignupDuplicateEmailIsUniformResponse(t *testing.T) {
	t.Parallel()
	s, _ := newSignupServer(t)
	rr := httptest.NewRecorder()
	s.handleSignup(rr, signupReq(`{"email":"alice@example.org","password":"correct horse"}`))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("first signup: expected 202, got %d", rr.Code)
	}
	// Second signup with same email — must still return 202 (don't leak that the address is taken).
	rr = httptest.NewRecorder()
	r := signupReq(`{"email":"alice@example.org","password":"correct horse"}`)
	r.RemoteAddr = "10.0.0.2:12345" // different IP so rate limit doesn't fire
	s.handleSignup(rr, r)
	if rr.Code != http.StatusAccepted {
		t.Errorf("duplicate signup: expected uniform 202, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestHandleVerifyHappyPath(t *testing.T) {
	t.Parallel()
	s, cap := newSignupServer(t)
	rr := httptest.NewRecorder()
	s.handleSignup(rr, signupReq(`{"email":"alice@example.org","password":"correct horse"}`))
	if cap.last == nil {
		t.Fatal("expected verify email")
	}
	link := extractVerifyURL(t, cap.last.Text)
	parsed, _ := url.Parse(link)
	id := parsed.Query().Get("id")
	tok := parsed.Query().Get("token")
	if id == "" || tok == "" {
		t.Fatalf("malformed verify link: %s", link)
	}

	rr = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/signup/verify?id="+id+"&token="+tok, nil)
	s.handleVerify(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("verify: expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	u, _ := s.users.GetByID(context.Background(), id)
	if u.EmailVerifiedAt == nil {
		t.Error("user must be marked verified after clicking the link")
	}
}

func TestHandleVerifyProvisionsKaiInstance(t *testing.T) {
	t.Parallel()
	s, cap := newSignupServer(t)
	// Sign up + verify.
	rr := httptest.NewRecorder()
	s.handleSignup(rr, signupReq(`{"email":"alice@example.org","password":"correct horse"}`))
	link := extractVerifyURL(t, cap.last.Text)
	parsed, _ := url.Parse(link)
	id := parsed.Query().Get("id")
	tok := parsed.Query().Get("token")

	rr = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/signup/verify?id="+id+"&token="+tok, nil)
	s.handleVerify(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("verify: expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}

	// A KaiInstance should now exist with the right SaaS Spec fields.
	wantSlug := slugFromUserID(id)
	got, err := s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).Get(context.Background(), "kai-"+wantSlug, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("KaiInstance kai-%s should be created on verify: %v", wantSlug, err)
	}
	spec, _ := got.Object["spec"].(map[string]any)
	if spec["userRef"] != id {
		t.Errorf("spec.userRef = %v, want %s", spec["userRef"], id)
	}
	if spec["tier"] != "free" {
		t.Errorf("spec.tier = %v, want free", spec["tier"])
	}
	if spec["managed"] != "saas" {
		t.Errorf("spec.managed = %v, want saas", spec["managed"])
	}
	if spec["appRef"] != defaultSignupApp {
		t.Errorf("spec.appRef = %v, want %s", spec["appRef"], defaultSignupApp)
	}
	if spec["customerSlug"] != wantSlug {
		t.Errorf("spec.customerSlug = %v, want %s", spec["customerSlug"], wantSlug)
	}
}

func TestHandleVerifyDoubleClickIsIdempotent(t *testing.T) {
	t.Parallel()
	s, cap := newSignupServer(t)
	rr := httptest.NewRecorder()
	s.handleSignup(rr, signupReq(`{"email":"alice@example.org","password":"correct horse"}`))
	link := extractVerifyURL(t, cap.last.Text)
	parsed, _ := url.Parse(link)
	id, tok := parsed.Query().Get("id"), parsed.Query().Get("token")

	// First verify creates the workspace.
	rr = httptest.NewRecorder()
	s.handleVerify(rr, httptest.NewRequest(http.MethodGet, "/api/signup/verify?id="+id+"&token="+tok, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("first verify: expected 200, got %d", rr.Code)
	}
	// Second verify (user clicked link twice) must not error or duplicate.
	rr = httptest.NewRecorder()
	s.handleVerify(rr, httptest.NewRequest(http.MethodGet, "/api/signup/verify?id="+id+"&token="+tok, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("second verify: expected 200 (idempotent), got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestHandleVerifyWithoutDynClientStillVerifies(t *testing.T) {
	t.Parallel()
	// Dev mode: no kubeconfig → no dynamic client. Verify must still
	// succeed (the user is marked verified) even if no workspace lands.
	s, cap := newSignupServer(t)
	s.dyn = nil
	rr := httptest.NewRecorder()
	s.handleSignup(rr, signupReq(`{"email":"alice@example.org","password":"correct horse"}`))
	link := extractVerifyURL(t, cap.last.Text)
	parsed, _ := url.Parse(link)
	id, tok := parsed.Query().Get("id"), parsed.Query().Get("token")

	rr = httptest.NewRecorder()
	s.handleVerify(rr, httptest.NewRequest(http.MethodGet, "/api/signup/verify?id="+id+"&token="+tok, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("verify (no K8s): expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	u, _ := s.users.GetByID(context.Background(), id)
	if u.EmailVerifiedAt == nil {
		t.Error("user must be verified even when provisioning is skipped")
	}
}

func TestSlugFromUserID(t *testing.T) {
	t.Parallel()
	got := slugFromUserID("u_01HX3ZQABCDEFGHJKMNPQRSTVWXY1Z")
	want := "u01hx3zqabcde" // "u" prefix + first 12 chars of ULID body, lowercased
	if got != want {
		t.Errorf("slugFromUserID = %q, want %q", got, want)
	}
	if len(got) != 13 {
		t.Errorf("slug length = %d, want 13", len(got))
	}
}

func TestHandleVerifyRejectsTamperedToken(t *testing.T) {
	t.Parallel()
	s, _ := newSignupServer(t)
	good, _ := s.buildVerifyLink("u_test", time.Now().Add(time.Hour))
	parsed, _ := url.Parse(good)
	id := parsed.Query().Get("id")
	tok := parsed.Query().Get("token")
	// Flip a byte in the signature.
	bad := strings.Replace(tok, tok[:1], "X", 1)

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/signup/verify?id="+id+"&token="+bad, nil)
	s.handleVerify(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("tampered token: expected 400, got %d", rr.Code)
	}
}

func TestHandleVerifyRejectsExpired(t *testing.T) {
	t.Parallel()
	s, _ := newSignupServer(t)
	link, _ := s.buildVerifyLink("u_test", time.Now().Add(-time.Hour))
	parsed, _ := url.Parse(link)
	id := parsed.Query().Get("id")
	tok := parsed.Query().Get("token")

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/signup/verify?id="+id+"&token="+tok, nil)
	s.handleVerify(rr, r)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "expired") {
		t.Errorf("expired token: expected 400 with 'expired', got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestRateLimiterRefills(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(2)
	now := time.Unix(1_700_000_000, 0)
	if !rl.allow("ip1", now) || !rl.allow("ip1", now) {
		t.Fatal("first two allowances should pass")
	}
	if rl.allow("ip1", now) {
		t.Fatal("third allowance with empty bucket must be refused")
	}
	// Half an hour later → 1 token refilled.
	if !rl.allow("ip1", now.Add(30*time.Minute)) {
		t.Error("should allow after 1 token refill")
	}
}

func TestClientIPHonorsXFFOnlyFromLoopback(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.5")
	if ip := clientIP(r); ip != "203.0.113.5" {
		t.Errorf("loopback request must trust XFF, got %q", ip)
	}
	r.RemoteAddr = "203.0.113.99:1234"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if ip := clientIP(r); ip != "203.0.113.99" {
		t.Errorf("non-loopback must ignore XFF, got %q", ip)
	}
}

// itoa is a tiny stdlib-free int formatter so we don't pull strconv just for tests.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte("0123456789")
	out := make([]byte, 0, 4)
	for n > 0 {
		out = append([]byte{digits[n%10]}, out...)
		n /= 10
	}
	return string(out)
}

// extractVerifyURL pulls the verify link out of the rendered TEXT body — the
// HTML body html-escapes ampersands which complicates url.Parse, while the
// text body has the raw URL.
func extractVerifyURL(t *testing.T, body string) string {
	t.Helper()
	const marker = "https://kai.example.org/api/signup/verify?"
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("verify link not found in body: %s", body)
	}
	end := strings.IndexAny(body[i:], "\n \"<")
	if end < 0 {
		end = len(body) - i
	}
	return body[i : i+end]
}

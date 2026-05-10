package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emai-ai/swarm/pkg/auth"
	"github.com/emai-ai/swarm/pkg/users"
)

// signResendBody returns the headers Resend would send for the given body
// with the given (raw) HMAC secret. Mirrors the svix scheme.
func signResendBody(t *testing.T, secret []byte, body string) (id, ts, sigHeader string) {
	t.Helper()
	id = "msg_test_001"
	ts = strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(id + "." + ts + "." + body))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	sigHeader = "v1," + sig
	return
}

func newWebhookServer(t *testing.T) *server {
	t.Helper()
	s, _ := newSignupServer(t)
	s.emailWebhook = emailWebhookConfig{
		Secret:    []byte("test-secret-bytes-must-be-long-enough"),
		Tolerance: 5 * time.Minute,
	}
	return s
}

func TestHandleEmailWebhook_BounceMarksUser(t *testing.T) {
	t.Parallel()
	s := newWebhookServer(t)
	hash, _ := auth.HashPassword("pw")
	u, _ := s.users.Create(context.Background(), users.CreateParams{
		Email: "alice@example.org", PasswordHash: hash, Tier: users.TierFree, Language: users.LangDE, App: users.DefaultApp,
	})

	body := `{"type":"email.bounced","data":{"email_id":"e1","to":["alice@example.org"]}}`
	id, ts, sig := signResendBody(t, s.emailWebhook.Secret, body)
	req := httptest.NewRequest(http.MethodPost, "/api/email/webhook", strings.NewReader(body))
	req.Header.Set("svix-id", id)
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", sig)

	rr := httptest.NewRecorder()
	s.handleEmailWebhook(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", rr.Code, rr.Body.String())
	}

	got, _ := s.users.GetByID(context.Background(), u.ID)
	if got.EmailBouncedAt == nil {
		t.Errorf("EmailBouncedAt should be set after bounce webhook")
	}
}

func TestHandleEmailWebhook_ComplaintAlsoMarks(t *testing.T) {
	t.Parallel()
	s := newWebhookServer(t)
	hash, _ := auth.HashPassword("pw")
	u, _ := s.users.Create(context.Background(), users.CreateParams{
		Email: "bob@example.org", PasswordHash: hash, Tier: users.TierFree, Language: users.LangDE, App: users.DefaultApp,
	})

	body := `{"type":"email.complained","data":{"email_id":"e1","to":["bob@example.org"]}}`
	id, ts, sig := signResendBody(t, s.emailWebhook.Secret, body)
	req := httptest.NewRequest(http.MethodPost, "/api/email/webhook", strings.NewReader(body))
	req.Header.Set("svix-id", id)
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", sig)

	rr := httptest.NewRecorder()
	s.handleEmailWebhook(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", rr.Code, rr.Body.String())
	}
	got, _ := s.users.GetByID(context.Background(), u.ID)
	if got.EmailBouncedAt == nil {
		t.Errorf("complaint should mark too")
	}
}

func TestHandleEmailWebhook_DeliveredIgnored(t *testing.T) {
	t.Parallel()
	s := newWebhookServer(t)
	hash, _ := auth.HashPassword("pw")
	u, _ := s.users.Create(context.Background(), users.CreateParams{
		Email: "carol@example.org", PasswordHash: hash, Tier: users.TierFree, Language: users.LangDE, App: users.DefaultApp,
	})

	body := `{"type":"email.delivered","data":{"email_id":"e1","to":["carol@example.org"]}}`
	id, ts, sig := signResendBody(t, s.emailWebhook.Secret, body)
	req := httptest.NewRequest(http.MethodPost, "/api/email/webhook", strings.NewReader(body))
	req.Header.Set("svix-id", id)
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", sig)

	rr := httptest.NewRecorder()
	s.handleEmailWebhook(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	got, _ := s.users.GetByID(context.Background(), u.ID)
	if got.EmailBouncedAt != nil {
		t.Errorf("delivered must not set EmailBouncedAt")
	}
}

func TestHandleEmailWebhook_BadSignature(t *testing.T) {
	t.Parallel()
	s := newWebhookServer(t)
	body := `{"type":"email.bounced","data":{"to":["alice@example.org"]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/email/webhook", strings.NewReader(body))
	req.Header.Set("svix-id", "x")
	req.Header.Set("svix-timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("svix-signature", "v1,AAAA")
	rr := httptest.NewRecorder()
	s.handleEmailWebhook(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("bad sig: status %d, want 401", rr.Code)
	}
}

func TestHandleEmailWebhook_StaleTimestampRejected(t *testing.T) {
	t.Parallel()
	s := newWebhookServer(t)
	body := `{"type":"email.bounced","data":{"to":["a@b"]}}`
	id := "msg_old"
	tsStale := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
	mac := hmac.New(sha256.New, s.emailWebhook.Secret)
	mac.Write([]byte(id + "." + tsStale + "." + body))
	sig := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	req := httptest.NewRequest(http.MethodPost, "/api/email/webhook", strings.NewReader(body))
	req.Header.Set("svix-id", id)
	req.Header.Set("svix-timestamp", tsStale)
	req.Header.Set("svix-signature", sig)
	rr := httptest.NewRecorder()
	s.handleEmailWebhook(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("stale ts: status %d, want 401 (replay protection)", rr.Code)
	}
}

func TestHandleEmailWebhook_UnconfiguredReturns503(t *testing.T) {
	t.Parallel()
	s, _ := newSignupServer(t)
	// emailWebhook deliberately left empty
	body := `{"type":"email.bounced","data":{"to":["a@b"]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/email/webhook", strings.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleEmailWebhook(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("unconfigured: status %d, want 503", rr.Code)
	}
}

func TestHandleEmailWebhook_UnknownRecipientStill200(t *testing.T) {
	t.Parallel()
	s := newWebhookServer(t)
	body := `{"type":"email.bounced","data":{"email_id":"e1","to":["nobody@example.org"]}}`
	id, ts, sig := signResendBody(t, s.emailWebhook.Secret, body)
	req := httptest.NewRequest(http.MethodPost, "/api/email/webhook", strings.NewReader(body))
	req.Header.Set("svix-id", id)
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", sig)
	rr := httptest.NewRecorder()
	s.handleEmailWebhook(rr, req)
	// 2xx so Resend doesn't retry; we don't leak which addresses we have.
	if rr.Code != http.StatusOK {
		t.Errorf("unknown recipient: status %d, want 200", rr.Code)
	}
}

func TestLoadResendSecret_PrefixStripped(t *testing.T) {
	t.Parallel()
	// Construct a 24-byte raw secret, base64 it, prefix with whsec_.
	raw := []byte("0123456789abcdef01234567")
	envValue := "whsec_" + base64.StdEncoding.EncodeToString(raw)
	got, err := loadResendSecret(envValue)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(raw) {
		t.Errorf("loadResendSecret got %x, want %x", got, raw)
	}
}

func TestLoadResendSecret_RejectsTooShort(t *testing.T) {
	t.Parallel()
	short := "whsec_" + base64.StdEncoding.EncodeToString([]byte("short"))
	if _, err := loadResendSecret(short); err == nil {
		t.Errorf("expected error for short secret")
	}
}

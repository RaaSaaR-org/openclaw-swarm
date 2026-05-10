package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/emai-ai/swarm/pkg/auth"
	"github.com/emai-ai/swarm/pkg/email"
	"github.com/emai-ai/swarm/pkg/users"
)

// captureEmailSender is a tiny in-memory email.Sender for tests; mirrors
// the one in onboarding/signup_test but kept local so workspace's test
// fixtures stay self-contained.
type captureEmailSender struct {
	all []email.Message
}

func (c *captureEmailSender) Send(_ context.Context, m email.Message) error {
	c.all = append(c.all, m)
	return nil
}

// newDeletionFixture is the workspace test fixture for account-deletion:
// a signed-in user with the email + deletion-secret + base URL all wired.
// Returns the fixture, the user, and the captured email sender.
func newDeletionFixture(t *testing.T, slug string) (*fixture, *users.User, *captureEmailSender) {
	t.Helper()
	const userID = "u_alice"
	f := newFixtureWithBinding(t, slug, nil, "saas", userID)
	cap := &captureEmailSender{}
	f.server.email = cap
	f.server.emailFrom = "Kai <noreply@kai.example.org>"
	f.server.deletionSecret = []byte("test-deletion-secret-32-bytes-ok")
	f.server.deletionBaseURL = "https://kai.example.org"
	f.server.deletionTTL = 24 * time.Hour

	hash, _ := auth.HashPassword("pw")
	u, err := f.server.users.Create(context.Background(), users.CreateParams{
		Email: "alice@example.org", PasswordHash: hash, Tier: users.TierFree, Language: users.LangDE, App: users.DefaultApp,
	})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	// Override the auto-generated ULID so the test's HMAC-signed link can
	// be reconstructed deterministically.
	u, _ = f.server.users.GetByEmail(context.Background(), "alice@example.org")
	return f, u, cap
}

// authedAccountReq is like authedReq from owned_workspaces_test but binds
// the cookie to the seeded user's actual ID (which the deletion flow looks
// up via claims.Uid).
func authedAccountReq(t *testing.T, f *fixture, method, path, slug, userID string) *http.Request {
	t.Helper()
	cookie, err := auth.IssueSessionWithUID(slug, "alice@example.org", userID, f.jwtSecret, time.Now())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	req := slugReq(method, path, slug, "")
	req.AddCookie(cookie)
	return req
}

func TestRequestDeletion_HappyPath(t *testing.T) {
	t.Parallel()
	f, u, cap := newDeletionFixture(t, "primary")
	req := authedAccountReq(t, f, http.MethodPost, "/api/workspace/primary/account/request-deletion", "primary", u.ID)
	rr := httptest.NewRecorder()
	f.server.handleRequestDeletion(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d (%s)", rr.Code, rr.Body.String())
	}
	if len(cap.all) != 1 {
		t.Fatalf("expected 1 email, got %d", len(cap.all))
	}
	mail := cap.all[0]
	if mail.To != "alice@example.org" {
		t.Errorf("To = %q", mail.To)
	}
	// The link should embed the slug + the userID + a token.
	if !strings.Contains(mail.HTML, "/api/workspace/primary/account/confirm-deletion") {
		t.Errorf("HTML missing confirm link\n%s", mail.HTML)
	}
	if !strings.Contains(mail.HTML, "id="+u.ID) {
		t.Errorf("HTML missing id=%s", u.ID)
	}
}

func TestRequestDeletion_RequiresAuth(t *testing.T) {
	t.Parallel()
	f, _, _ := newDeletionFixture(t, "primary")
	req := slugReq(http.MethodPost, "/api/workspace/primary/account/request-deletion", "primary", "")
	rr := httptest.NewRecorder()
	f.server.handleRequestDeletion(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestRequestDeletion_503WhenUnconfigured(t *testing.T) {
	t.Parallel()
	const userID = "u_alice"
	f := newFixtureWithBinding(t, "primary", nil, "saas", userID)
	// email + deletion config NOT wired
	req := authedAccountReq(t, f, http.MethodPost, "/api/workspace/primary/account/request-deletion", "primary", userID)
	rr := httptest.NewRecorder()
	f.server.handleRequestDeletion(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rr.Code)
	}
}

func TestRequestDeletion_LegacyInternalSession403(t *testing.T) {
	t.Parallel()
	f, _, _ := newDeletionFixture(t, "primary")
	// Issue a session WITHOUT a Uid claim (legacy path).
	cookie, err := auth.IssueSession("primary", "old-admin@example.com", f.jwtSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req := slugReq(http.MethodPost, "/api/workspace/primary/account/request-deletion", "primary", "")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	f.server.handleRequestDeletion(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 for legacy session, got %d", rr.Code)
	}
}

func TestConfirmDeletion_HappyPath(t *testing.T) {
	t.Parallel()
	f, u, cap := newDeletionFixture(t, "primary")
	tok, err := f.server.signDeletionToken("primary", u.ID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet,
		"/api/workspace/primary/account/confirm-deletion?id="+u.ID+"&token="+url.QueryEscape(tok), nil)
	req.SetPathValue("slug", "primary")
	rr := httptest.NewRecorder()
	f.server.handleConfirmDeletion(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "deleted") {
		t.Errorf("body = %s", rr.Body.String())
	}
	// Phase 3 wire-up captures a pre-delete User snapshot before SoftDelete
	// runs, so the post-delete email lands reliably even on MemoryStore
	// (which filters soft-deleted rows from GetByID).
	if len(cap.all) != 1 {
		t.Fatalf("expected 1 post-delete email, got %d", len(cap.all))
	}
	if cap.all[0].To != "alice@example.org" {
		t.Errorf("To = %q", cap.all[0].To)
	}
}

func TestConfirmDeletion_RejectsTamperedToken(t *testing.T) {
	t.Parallel()
	f, u, _ := newDeletionFixture(t, "primary")
	// Sign with a different secret → wrong sig.
	other := *f.server
	other.deletionSecret = []byte("wrong-secret-32-bytes-other-side")
	tok, _ := other.signDeletionToken("primary", u.ID, time.Now().Add(time.Hour))
	req := httptest.NewRequest(http.MethodGet,
		"/api/workspace/primary/account/confirm-deletion?id="+u.ID+"&token="+url.QueryEscape(tok), nil)
	req.SetPathValue("slug", "primary")
	rr := httptest.NewRecorder()
	f.server.handleConfirmDeletion(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("tampered token: got %d, want 400", rr.Code)
	}
}

func TestConfirmDeletion_RejectsExpired(t *testing.T) {
	t.Parallel()
	f, u, _ := newDeletionFixture(t, "primary")
	tok, _ := f.server.signDeletionToken("primary", u.ID, time.Now().Add(-time.Hour))
	req := httptest.NewRequest(http.MethodGet,
		"/api/workspace/primary/account/confirm-deletion?id="+u.ID+"&token="+url.QueryEscape(tok), nil)
	req.SetPathValue("slug", "primary")
	rr := httptest.NewRecorder()
	f.server.handleConfirmDeletion(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expired: got %d, want 400", rr.Code)
	}
}

func TestConfirmDeletion_TokenForOtherSlugRejected(t *testing.T) {
	t.Parallel()
	f, u, _ := newDeletionFixture(t, "primary")
	// Sign for a different slug; reuse on /primary must fail.
	tok, _ := f.server.signDeletionToken("victim", u.ID, time.Now().Add(time.Hour))
	req := httptest.NewRequest(http.MethodGet,
		"/api/workspace/primary/account/confirm-deletion?id="+u.ID+"&token="+url.QueryEscape(tok), nil)
	req.SetPathValue("slug", "primary")
	rr := httptest.NewRecorder()
	f.server.handleConfirmDeletion(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("cross-slug: got %d, want 400", rr.Code)
	}
}

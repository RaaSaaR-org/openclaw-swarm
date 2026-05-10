package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/emai-ai/swarm/pkg/email"
	"github.com/emai-ai/swarm/pkg/users"
)

// Account-deletion flow (TASK-021 Phase 1).
//
// Two-step confirmed deletion:
//   1. POST /api/workspace/{slug}/account/request-deletion — auth required,
//      mints an HMAC-signed token, sends an email with a link to step 2.
//   2. GET  /api/workspace/{slug}/account/confirm-deletion?id=X&token=Y —
//      validates the token, calls Store.SoftDelete (TASK-021 Phase 0),
//      sends the `account-deleted` email (TASK-020 Phase 1) with the
//      30-day grace window date + a "restore" link (Phase 1.B follow-up).
//
// The 30-day grace window is enforced by the GDPR purge cron (TASK-021
// Phase 2) which calls Store.PurgeDeletedBefore(now - GracePeriod).

const (
	deletionTokenPurpose = "delete"
	deletionDefaultTTL   = 24 * time.Hour
)

// requestDeletionResponse is the JSON returned by step 1. We always return
// `status: confirmation_sent` even on edge cases (no email configured,
// already-deleted user) so the response shape doesn't leak account state.
type requestDeletionResponse struct {
	Status string `json:"status"`
}

// confirmDeletionResponse is the JSON returned by step 2 on success.
type confirmDeletionResponse struct {
	Status            string `json:"status"`
	GraceDays         int    `json:"graceDays"`
	FinalDeletionDate string `json:"finalDeletionDate"`
}

// handleRequestDeletion is step 1. Mints a token, dispatches the
// confirmation email, returns 202. Auth required (the JWT cookie's Uid
// is the user we'd delete). Failures fall back to 503 when email is not
// wired or the deletion secret is missing — better to fail loudly than
// silently no-op.
func (s *server) handleRequestDeletion(w http.ResponseWriter, r *http.Request) {
	if !s.deletionConfigured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "account deletion not configured"})
		return
	}
	slug := r.PathValue("slug")
	if !slugRegex.MatchString(slug) || len(slug) > 63 {
		writeUnauthorized(w)
		return
	}
	claims, ok := s.authedClaims(r, slug)
	if !ok {
		writeUnauthorized(w)
		return
	}
	if claims.Uid == "" {
		// Legacy internal-managed sessions don't have a User row to delete.
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "internal_managed_session"})
		return
	}

	u, err := s.users.GetByID(r.Context(), claims.Uid)
	if err != nil {
		if errors.Is(err, users.ErrNotFound) {
			// Already deleted — uniform response so callers can't enumerate.
			writeJSON(w, http.StatusAccepted, requestDeletionResponse{Status: "confirmation_sent"})
			return
		}
		log.Printf("request-deletion: lookup uid=%s: %v", claims.Uid, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
		return
	}

	link, err := s.buildDeletionLink(slug, u.ID, time.Now().Add(s.deletionTTLOrDefault()))
	if err != nil {
		log.Printf("request-deletion: build link uid=%s: %v", u.ID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "link build failed"})
		return
	}
	if err := s.sendDeletionRequestEmail(r.Context(), u, link); err != nil {
		// Sender failure — log but still 202; the user can retry, and we
		// don't want the response shape to leak whether the email landed.
		log.Printf("request-deletion: send mail to %s: %v", u.Email, err)
	}
	writeJSON(w, http.StatusAccepted, requestDeletionResponse{Status: "confirmation_sent"})
}

// handleConfirmDeletion is step 2. Validates the token, soft-deletes the
// user, dispatches the `account-deleted` email. Idempotent on
// already-deleted users (returns 200 with the same body). Token validation
// happens before the user lookup so a tampered token can't probe whether
// a user exists.
func (s *server) handleConfirmDeletion(w http.ResponseWriter, r *http.Request) {
	if !s.deletionConfigured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "account deletion not configured"})
		return
	}
	slug := r.PathValue("slug")
	if !slugRegex.MatchString(slug) || len(slug) > 63 {
		writeUnauthorized(w)
		return
	}
	id := r.URL.Query().Get("id")
	tok := r.URL.Query().Get("token")
	if id == "" || tok == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing id or token"})
		return
	}
	if err := s.checkDeletionToken(slug, id, tok, time.Now()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid token: " + err.Error()})
		return
	}

	// Pre-delete lookup: feeds the cascade (needs StripeCustomerID) and
	// the post-delete email (needs Email + Language). MemoryStore filters
	// soft-deleted rows from GetByID, so an already-deleted retry returns
	// nil here — both the cascade and the email become safe no-ops.
	preUser, _ := s.users.GetByID(r.Context(), id)

	// Cascade (TASK-021 Phase 3) BEFORE SoftDelete: the StripeCustomerID
	// + label-selector list still resolve. Best-effort; a cascade failure
	// must not block the user's right to erasure.
	if preUser != nil {
		s.runDeletionCascade(r.Context(), preUser)
	}

	now := time.Now().UTC()
	if err := s.users.SoftDelete(r.Context(), id, now); err != nil {
		if errors.Is(err, users.ErrNotFound) || errors.Is(err, users.ErrAlreadyDeleted) {
			// Already deleted — 200 with the (cached) grace info. We don't
			// know the original soft-delete time so we report from `now`;
			// the cron uses the actual `deleted_at` column anyway.
		} else {
			log.Printf("confirm-deletion: SoftDelete uid=%s: %v", id, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
			return
		}
	}

	finalAt := now.Add(users.GracePeriod)
	resp := confirmDeletionResponse{
		Status:            "deleted",
		GraceDays:         int(users.GracePeriod / (24 * time.Hour)),
		FinalDeletionDate: finalAt.Format("2006-01-02"),
	}

	// Post-delete email — best-effort, uses the pre-delete snapshot since
	// MemoryStore filters deleted users from GetByID.
	if preUser != nil {
		if err := s.sendAccountDeletedEmail(r.Context(), preUser, slug, finalAt); err != nil {
			log.Printf("confirm-deletion: send mail: %v", err)
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// deletionConfigured reports whether all four of email + emailFrom-or-default
// + deletionSecret + deletionBaseURL are wired. Partial wiring returns false
// — same all-or-nothing pattern as the usage-monitor email branch.
func (s *server) deletionConfigured() bool {
	return s.email != nil && s.users != nil && len(s.deletionSecret) > 0 && s.deletionBaseURL != ""
}

func (s *server) deletionTTLOrDefault() time.Duration {
	if s.deletionTTL > 0 {
		return s.deletionTTL
	}
	return deletionDefaultTTL
}

// buildDeletionLink mints the URL the confirmation email links to. Format:
//
//	{baseURL}/api/workspace/{slug}/account/confirm-deletion?id=<userID>&token=<base64-mac>.<exp>
//
// The HMAC covers `<purpose>|<slug>|<userID>|<exp-unix>` so a leaked token
// can't be replayed past expiry, can't be reused for a different slug,
// and can't be repurposed for a different operation (verify-email tokens
// can't unlock deletion).
func (s *server) buildDeletionLink(slug, userID string, exp time.Time) (string, error) {
	if len(s.deletionSecret) == 0 {
		return "", errors.New("deletion secret not configured")
	}
	tok, err := s.signDeletionToken(slug, userID, exp)
	if err != nil {
		return "", err
	}
	base := strings.TrimRight(s.deletionBaseURL, "/")
	return fmt.Sprintf("%s/api/workspace/%s/account/confirm-deletion?id=%s&token=%s",
		base, slug, userID, tok), nil
}

func (s *server) signDeletionToken(slug, userID string, exp time.Time) (string, error) {
	expSec := exp.UTC().Unix()
	mac := hmac.New(sha256.New, s.deletionSecret)
	fmt.Fprintf(mac, "%s|%s|%s|%d", deletionTokenPurpose, slug, userID, expSec)
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%s.%d", sig, expSec), nil
}

func (s *server) checkDeletionToken(slug, userID, token string, now time.Time) error {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return errors.New("malformed token")
	}
	wantSig, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return errors.New("bad token encoding")
	}
	var expSec int64
	if _, err := fmt.Sscanf(parts[1], "%d", &expSec); err != nil {
		return errors.New("bad token expiry")
	}
	if now.UTC().Unix() >= expSec {
		return errors.New("token expired")
	}
	mac := hmac.New(sha256.New, s.deletionSecret)
	fmt.Fprintf(mac, "%s|%s|%s|%d", deletionTokenPurpose, slug, userID, expSec)
	got := mac.Sum(nil)
	if !hmac.Equal(got, wantSig) {
		return errors.New("signature mismatch")
	}
	return nil
}

func (s *server) sendDeletionRequestEmail(ctx context.Context, u *users.User, link string) error {
	if s.email == nil {
		return errors.New("email sender not configured")
	}
	mailLang := email.LangDE
	if u.Language == users.LangEN {
		mailLang = email.LangEN
	}
	// We reuse the `reset` template shape (Name + ResetURL + ExpiresInHours)
	// for the confirmation step — the template is generic enough ("click
	// the link to confirm an account action") that the same layout works
	// for password-reset and deletion-confirmation. A dedicated
	// `delete-confirm` template is a Phase 1.B refinement.
	hours := int(s.deletionTTLOrDefault() / time.Hour)
	return email.Dispatch(ctx, s.email, email.SendOptions{
		Template: email.TemplateReset,
		Lang:     mailLang,
		To:       u.Email,
		From:     s.emailFrom,
	}, struct {
		Name           string
		ResetURL       string
		ExpiresInHours int
	}{
		Name:           strings.SplitN(u.Email, "@", 2)[0],
		ResetURL:       link,
		ExpiresInHours: hours,
	})
}

func (s *server) sendAccountDeletedEmail(ctx context.Context, u *users.User, slug string, finalAt time.Time) error {
	if s.email == nil {
		return nil
	}
	mailLang := email.LangDE
	if u.Language == users.LangEN {
		mailLang = email.LangEN
	}
	restoreURL := strings.TrimRight(s.deletionBaseURL, "/") + "/workspace/" + slug
	return email.Dispatch(ctx, s.email, email.SendOptions{
		Template: email.TemplateAccountDeleted,
		Lang:     mailLang,
		To:       u.Email,
		From:     s.emailFrom,
	}, struct {
		Name              string
		GraceDays         int
		RestoreURL        string
		FinalDeletionDate string
	}{
		Name:              strings.SplitN(u.Email, "@", 2)[0],
		GraceDays:         int(users.GracePeriod / (24 * time.Hour)),
		RestoreURL:        restoreURL,
		FinalDeletionDate: finalAt.Format("2006-01-02"),
	})
}

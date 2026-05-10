package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/emai-ai/swarm/pkg/users"
)

// emailWebhookConfig is the runtime config for POST /api/email/webhook
// (TASK-020 Phase 4). Receives provider events from Resend (bounced /
// complained / delivered) and updates `User.email_bounced_at` so future
// sends skip dead addresses.
//
// Resend signs webhooks with svix — same scheme used by Stripe et al.:
//   - svix-id: unique webhook ID
//   - svix-timestamp: unix seconds (replay-protection ceiling)
//   - svix-signature: `v1,<base64-hmac>` (one or more `v1,...` entries
//     separated by spaces); HMAC-SHA256 over `<id>.<timestamp>.<body>`
//
// Without `RESEND_WEBHOOK_SECRET` the endpoint refuses with 503 — better
// to fail loudly than silently accept unsigned webhooks.
type emailWebhookConfig struct {
	Secret    []byte // raw secret bytes from Resend's `whsec_<base64>` minus the prefix
	Tolerance time.Duration
}

// resendWebhookPayload is the subset of the Resend webhook body we care
// about. Resend's full schema is much richer; we only need the type +
// the recipient(s).
type resendWebhookPayload struct {
	Type      string             `json:"type"`
	CreatedAt string             `json:"created_at"`
	Data      resendWebhookData  `json:"data"`
}

type resendWebhookData struct {
	EmailID string   `json:"email_id"`
	To      []string `json:"to"`
}

// loadResendSecret returns the webhook secret bytes for the given
// `whsec_<base64>` env value, or an error explaining why it can't be
// used. The `whsec_` prefix is Resend's convention; we strip it before
// using as HMAC key. Empty input returns nil/nil so the caller can
// distinguish "not configured" from "configured but invalid".
func loadResendSecret(envValue string) ([]byte, error) {
	if envValue == "" {
		return nil, nil
	}
	value := strings.TrimPrefix(envValue, "whsec_")
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("RESEND_WEBHOOK_SECRET base64-decode: %w", err)
	}
	if len(raw) < 16 {
		return nil, errors.New("RESEND_WEBHOOK_SECRET decoded to <16 bytes — not a real svix secret")
	}
	return raw, nil
}

// handleEmailWebhook is POST /api/email/webhook. Verifies the svix
// signature, parses the payload, and updates the user record on
// bounce/complaint events. Always returns 2xx on signature-valid
// requests so Resend doesn't retry — but logs the user-not-found case
// so ops can audit. Signature failures return 401.
func (s *server) handleEmailWebhook(w http.ResponseWriter, r *http.Request) {
	if len(s.emailWebhook.Secret) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "RESEND_WEBHOOK_SECRET not configured"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body: " + err.Error()})
		return
	}

	id := r.Header.Get("svix-id")
	tsStr := r.Header.Get("svix-timestamp")
	sig := r.Header.Get("svix-signature")
	if id == "" || tsStr == "" || sig == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing svix-* headers"})
		return
	}
	if err := s.verifyResendSignature(id, tsStr, sig, body, time.Now()); err != nil {
		log.Printf("email-webhook: signature verify failed: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "signature mismatch"})
		return
	}

	var event resendWebhookPayload
	if err := json.Unmarshal(body, &event); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if !isBounceOrComplaint(event.Type) {
		// delivered / opened / clicked / sent: not actionable for our
		// "stop emailing dead addresses" goal. 2xx so Resend marks the
		// webhook as delivered.
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "type": event.Type})
		return
	}
	if len(event.Data.To) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no_recipient"})
		return
	}

	if err := s.markBouncedFromWebhook(r.Context(), event.Data.To[0], time.Now()); err != nil {
		// Log but still 2xx — re-delivery wouldn't help if the user
		// genuinely doesn't exist on our end.
		log.Printf("email-webhook: mark bounced for %s: %v", event.Data.To[0], err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "marked"})
}

// markBouncedFromWebhook looks up the user by email and stamps
// EmailBouncedAt. ErrNotFound is squashed at this seam — webhook callers
// don't get to enumerate which addresses we have on file.
func (s *server) markBouncedFromWebhook(ctx context.Context, addr string, now time.Time) error {
	if s.users == nil {
		return errors.New("users store not configured")
	}
	addr = users.NormalizeEmail(addr)
	u, err := s.users.GetByEmail(ctx, addr)
	if err != nil {
		if errors.Is(err, users.ErrNotFound) {
			return nil
		}
		return err
	}
	return s.users.MarkEmailBounced(ctx, u.ID, now)
}

// verifyResendSignature implements the svix verification algorithm: HMAC-
// SHA256 over `<id>.<timestamp>.<body>`, compare in constant time. Multiple
// signatures may be space-separated; any match passes. Replay protection:
// reject timestamps older than `Tolerance` (5min default).
func (s *server) verifyResendSignature(id, tsStr, sig string, body []byte, now time.Time) error {
	tolerance := s.emailWebhook.Tolerance
	if tolerance == 0 {
		tolerance = 5 * time.Minute
	}
	tsUnix, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return fmt.Errorf("svix-timestamp parse: %w", err)
	}
	ts := time.Unix(tsUnix, 0)
	if now.Sub(ts) > tolerance || ts.Sub(now) > tolerance {
		return fmt.Errorf("svix-timestamp outside ±%s window", tolerance)
	}

	signed := id + "." + tsStr + "." + string(body)
	mac := hmac.New(sha256.New, s.emailWebhook.Secret)
	mac.Write([]byte(signed))
	want := mac.Sum(nil)

	for _, entry := range strings.Fields(sig) {
		// Each entry is `v<n>,<base64-sig>`; we accept any v1.
		parts := strings.SplitN(entry, ",", 2)
		if len(parts) != 2 || parts[0] != "v1" {
			continue
		}
		got, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			continue
		}
		if hmac.Equal(got, want) {
			return nil
		}
	}
	return errors.New("no signature matched")
}

func isBounceOrComplaint(t string) bool {
	switch t {
	case "email.bounced", "email.complained":
		return true
	}
	return false
}

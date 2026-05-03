package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ResendSender talks to Resend's REST API. We deliberately avoid Resend's Go
// SDK — the API surface we use is one POST and the SDK pulls in its own
// transport opinions; staying on net/http keeps the dep graph small and makes
// it trivial to swap providers later (PROP-002 calls out a one-day switching
// budget).
type ResendSender struct {
	APIKey  string
	Client  *http.Client // optional; defaults to a 10s-timeout client
	BaseURL string       // optional; defaults to https://api.resend.com — overridable for tests
}

// NewResendSender constructs a sender from an API key. Empty keys fail at
// construction so callers don't accidentally ship a no-op sender to prod.
func NewResendSender(apiKey string) (*ResendSender, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("%w: empty Resend API key", ErrInvalidMessage)
	}
	return &ResendSender{APIKey: apiKey}, nil
}

func (r *ResendSender) httpClient() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (r *ResendSender) baseURL() string {
	if r.BaseURL != "" {
		return r.BaseURL
	}
	return "https://api.resend.com"
}

type resendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html,omitempty"`
	Text    string   `json:"text,omitempty"`
	ReplyTo string   `json:"reply_to,omitempty"`
}

type resendResponse struct {
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
	Name    string `json:"name,omitempty"`
}

// Send POSTs the message to /emails. Non-2xx responses are surfaced verbatim
// (status + body) so callers can debug from a single error string. We don't
// retry — Resend itself queues + retries delivery; transient send-time errors
// at this layer are the caller's call (probably "log and move on").
func (r *ResendSender) Send(ctx context.Context, m Message) error {
	if m.To == "" || m.From == "" || m.Subject == "" {
		return fmt.Errorf("%w: From, To, and Subject are required", ErrInvalidMessage)
	}
	body, err := json.Marshal(resendRequest{
		From:    m.From,
		To:      []string{m.To},
		Subject: m.Subject,
		HTML:    m.HTML,
		Text:    m.Text,
		ReplyTo: m.ReplyTo,
	})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL()+"/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("resend: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("resend %d: %s", resp.StatusCode, string(respBody))
	}
	var ok resendResponse
	_ = json.Unmarshal(respBody, &ok) // best-effort; success doesn't depend on parsing the id
	return nil
}

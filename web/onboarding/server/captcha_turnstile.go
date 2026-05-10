package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// turnstileEndpoint is Cloudflare's siteverify URL. Overridden in tests.
const turnstileEndpoint = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// turnstileCaptcha is the production captchaVerifier — it forwards the
// client-side widget token to Cloudflare's Turnstile siteverify endpoint and
// rejects the signup if Cloudflare says the token is invalid or expired.
//
// Wired up in setupSignup when TURNSTILE_SECRET_KEY is set; absent the env
// var, signup falls back to noopCaptcha (tests + dev). Cloudflare's docs:
// https://developers.cloudflare.com/turnstile/get-started/server-side-validation/
type turnstileCaptcha struct {
	secret   string
	endpoint string // overridable for tests
	client   *http.Client
}

func newTurnstileCaptcha(secret string) *turnstileCaptcha {
	return &turnstileCaptcha{
		secret:   secret,
		endpoint: turnstileEndpoint,
		client:   &http.Client{Timeout: 5 * time.Second},
	}
}

// turnstileResponse is the JSON shape returned by siteverify. Cloudflare
// returns more fields than this (action, cdata, hostname, challenge_ts);
// we only inspect the bits that decide accept/reject.
type turnstileResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
}

// Verify exchanges the client-side widget token for a verdict from Cloudflare.
// Returns nil only when Cloudflare reports success=true. An empty token is
// rejected up front (saves a network round-trip and catches the "user
// submitted before the widget rendered" case explicitly).
func (t *turnstileCaptcha) Verify(ctx context.Context, token, remoteIP string) error {
	if t == nil || t.secret == "" {
		return errors.New("turnstile not configured")
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("missing turnstile token")
	}
	form := url.Values{
		"secret":   {t.secret},
		"response": {token},
	}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("turnstile: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("turnstile: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("turnstile: read body: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("turnstile: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var v turnstileResponse
	if err := json.Unmarshal(body, &v); err != nil {
		return fmt.Errorf("turnstile: decode body: %w", err)
	}
	if !v.Success {
		if len(v.ErrorCodes) == 0 {
			return errors.New("turnstile: verification failed")
		}
		return fmt.Errorf("turnstile: %s", strings.Join(v.ErrorCodes, ","))
	}
	return nil
}

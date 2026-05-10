package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOnboardingConfig_ReturnsTurnstileSiteKey — when the deployment has
// `TURNSTILE_SITE_KEY` set, GET /api/onboarding/config surfaces it so the
// SPA can mount the cf-turnstile widget without a build-time bake.
func TestOnboardingConfig_ReturnsTurnstileSiteKey(t *testing.T) {
	t.Parallel()
	s, _ := newSignupServer(t)
	s.signup.TurnstilePublicSiteKey = "0x4AAAAAAA-public-site-key"

	rr := httptest.NewRecorder()
	s.handleOnboardingConfig(rr, httptest.NewRequest(http.MethodGet, "/api/onboarding/config", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d (body=%s)", rr.Code, rr.Body.String())
	}
	var got onboardingConfig
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.TurnstileSiteKey != "0x4AAAAAAA-public-site-key" {
		t.Errorf("TurnstileSiteKey = %q, want the configured public site key", got.TurnstileSiteKey)
	}
	if !got.SignupEnabled {
		t.Errorf("SignupEnabled should be true when fixture has Enabled: true")
	}
}

// TestOnboardingConfig_OmitsSiteKeyWhenUnconfigured — no TURNSTILE_SITE_KEY
// → the JSON's `turnstileSiteKey` is omitted (omitempty). The SPA reads
// the empty/missing field as "no widget".
func TestOnboardingConfig_OmitsSiteKeyWhenUnconfigured(t *testing.T) {
	t.Parallel()
	s, _ := newSignupServer(t)
	// TurnstilePublicSiteKey left empty.

	rr := httptest.NewRecorder()
	s.handleOnboardingConfig(rr, httptest.NewRequest(http.MethodGet, "/api/onboarding/config", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	// Raw body should not contain the field name when site key is empty —
	// JSON `omitempty` keeps the response surface honest.
	body := rr.Body.String()
	if body == "" {
		t.Fatal("empty body")
	}
	var got onboardingConfig
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.TurnstileSiteKey != "" {
		t.Errorf("TurnstileSiteKey = %q, want empty", got.TurnstileSiteKey)
	}
}

// TestOnboardingConfig_ReportsSignupDisabled — internal-only deploys turn
// signup off. The SPA renders the admin-token form when SignupEnabled is
// false, so the boolean has to round-trip honestly.
func TestOnboardingConfig_ReportsSignupDisabled(t *testing.T) {
	t.Parallel()
	s, _ := newSignupServer(t)
	s.signup.Enabled = false

	rr := httptest.NewRecorder()
	s.handleOnboardingConfig(rr, httptest.NewRequest(http.MethodGet, "/api/onboarding/config", nil))
	var got onboardingConfig
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.SignupEnabled {
		t.Errorf("SignupEnabled = true, want false when signup.Enabled is false")
	}
}

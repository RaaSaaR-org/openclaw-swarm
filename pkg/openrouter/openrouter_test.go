package openrouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetKeyHappyPath(t *testing.T) {
	t.Parallel()
	var got struct {
		path string
		auth string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		// Shape captured from a real OpenRouter response, 2026-05-03.
		_, _ = w.Write([]byte(`{"data":{"label":"sk-or-v1-72e...cb7","is_management_key":false,"is_provisioning_key":false,"limit":null,"limit_remaining":null,"is_free_tier":true,"usage":0.123,"usage_daily":0.05,"usage_weekly":0.1,"usage_monthly":0.123}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "sk-test", BaseURL: srv.URL}
	info, err := c.GetKey(context.Background())
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if got.path != "/api/v1/key" {
		t.Errorf("path = %q, want /api/v1/key", got.path)
	}
	if got.auth != "Bearer sk-test" {
		t.Errorf("auth = %q, want Bearer sk-test", got.auth)
	}
	if info.Usage != 0.123 || info.UsageMonthly != 0.123 {
		t.Errorf("usage round-trip mismatch: %+v", info)
	}
	if !info.IsFreeTier {
		t.Error("IsFreeTier should be true")
	}
	if info.IsProvisioningKey {
		t.Error("IsProvisioningKey should be false in the fixture")
	}
}

func TestGetCreditsHappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credits" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":{"total_credits":50.0,"total_usage":12.34}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "sk-test", BaseURL: srv.URL}
	cr, err := c.GetCredits(context.Background())
	if err != nil {
		t.Fatalf("GetCredits: %v", err)
	}
	if cr.TotalCredits != 50.0 || cr.TotalUsage != 12.34 {
		t.Errorf("credits round-trip mismatch: %+v", cr)
	}
}

func TestNon2xxSurfacesStatusAndBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid key","code":401}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "sk-bad", BaseURL: srv.URL}
	_, err := c.GetKey(context.Background())
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid key") {
		t.Errorf("error must include status and body, got: %v", err)
	}
}

func TestNewClientRejectsEmptyKey(t *testing.T) {
	t.Parallel()
	if _, err := NewClient(""); err == nil {
		t.Error("expected error on empty key")
	}
}

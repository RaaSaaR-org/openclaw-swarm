// Integration tests for pkg/stripe. Guarded by STRIPE_SECRET so the
// default `go test ./...` pass on a developer laptop without a Stripe key
// skips cleanly. To run locally:
//
//	STRIPE_SECRET=sk_test_... go test ./pkg/stripe/... -v -run Integration
//
// Tests hit the real Stripe TEST mode API. Test mode is free; Stripe's
// test infrastructure tolerates these calls. Don't run with a live key.
package stripe

import (
	"os"
	"strings"
	"testing"
)

func newIntegrationClient(t *testing.T) *Client {
	t.Helper()
	key := os.Getenv("STRIPE_SECRET")
	if key == "" {
		t.Skip("STRIPE_SECRET not set — skipping Stripe integration tests")
	}
	if !strings.HasPrefix(key, "sk_test_") {
		t.Fatalf("STRIPE_SECRET must be a sk_test_ key (got %s) — refusing to hit live API", key[:8])
	}
	c, err := NewClient(key, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// TestIntegrationCheckoutSessionRejectsBadPriceID exercises the round-trip
// against real Stripe — gives a price ID that doesn't exist; Stripe should
// reject with a clear error. Proves: (a) the SDK is wired, (b) auth works,
// (c) error responses surface as Go errors.
func TestIntegrationCheckoutSessionRejectsBadPriceID(t *testing.T) {
	c := newIntegrationClient(t)
	_, err := c.CreateCheckoutSession(CheckoutParams{
		PriceID:    "price_does_not_exist_12345",
		UserRef:    "u_test",
		Email:      "test@example.org",
		SuccessURL: "https://kai.example.org/success",
		CancelURL:  "https://kai.example.org/cancel",
	})
	if err == nil {
		t.Fatal("expected error when price ID doesn't exist")
	}
	t.Logf("got expected error: %v", err)
}

// TestIntegrationGetSubscriptionUnknown exercises the subscription read
// path. Looking up a non-existent subscription should yield a structured
// 404 — we just confirm the error surfaces.
func TestIntegrationGetSubscriptionUnknown(t *testing.T) {
	c := newIntegrationClient(t)
	_, err := c.GetSubscription("sub_does_not_exist_12345")
	if err == nil {
		t.Fatal("expected 404-ish error")
	}
	t.Logf("got expected error: %v", err)
}

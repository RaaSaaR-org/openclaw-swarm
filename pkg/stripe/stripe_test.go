package stripe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNewClientRejectsEmptyKey(t *testing.T) {
	t.Parallel()
	if _, err := NewClient("", nil); err == nil {
		t.Error("expected error on empty key")
	}
}

func TestCreateCheckoutSessionValidates(t *testing.T) {
	t.Parallel()
	c, _ := NewClient("sk_test_x", nil)
	cases := []CheckoutParams{
		{}, // all empty
		{PriceID: "p", UserRef: "u"},                                                     // missing URLs
		{UserRef: "u", SuccessURL: "s", CancelURL: "c"},                                  // missing PriceID
		{PriceID: "p", SuccessURL: "s", CancelURL: "c"},                                  // missing UserRef
	}
	for i, p := range cases {
		if _, err := c.CreateCheckoutSession(p); err == nil {
			t.Errorf("case %d: expected error on incomplete params, got nil", i)
		}
	}
}

func TestTierForPriceID(t *testing.T) {
	t.Parallel()
	c, _ := NewClient("sk_test_x", map[string]Tier{
		"price_starter_eur_10": TierStarter,
		"price_growth_eur_30":  TierGrowth,
	})
	if got := c.TierForPriceID("price_starter_eur_10"); got != TierStarter {
		t.Errorf("starter price → %q, want starter", got)
	}
	if got := c.TierForPriceID("price_unknown"); got != TierFree {
		t.Errorf("unknown price → %q, want free (safe default)", got)
	}
	if got := c.TierForPriceID(""); got != TierFree {
		t.Errorf("empty price → %q, want free", got)
	}
}

func TestParseWebhookRejectsMissingSecret(t *testing.T) {
	t.Parallel()
	c, _ := NewClient("sk_test_x", nil)
	_, err := c.ParseWebhook([]byte(`{}`), "t=0,v1=abc", "")
	if err == nil {
		t.Error("expected error on empty webhook secret")
	}
}

// TestParseWebhookRejectsTamperedSignature confirms we delegate to
// stripe-go's webhook.ConstructEvent and that its signature check actually
// fires. We don't try to test ConstructEvent's internal correctness — that's
// stripe-go's responsibility — but we DO verify our wiring rejects an
// obviously-bad signature. Use a current timestamp so the replay-window
// tolerance check passes and the signature check is the one that fires.
func TestParseWebhookRejectsTamperedSignature(t *testing.T) {
	t.Parallel()
	c, _ := NewClient("sk_test_x", nil)
	header := fmt.Sprintf("t=%d,v1=%s", time.Now().Unix(), strings.Repeat("0", 64))
	_, err := c.ParseWebhook(
		[]byte(`{"id":"evt_test","type":"checkout.session.completed"}`),
		header,
		"whsec_test",
	)
	if err == nil {
		t.Error("expected signature-mismatch error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "signature") {
		t.Errorf("error must mention signature, got: %v", err)
	}
}

// TestParseWebhookAcceptsValidSignature constructs a payload + signature
// the way Stripe would, hands it to ParseWebhook, and verifies the event
// parses cleanly.
func TestParseWebhookAcceptsValidSignature(t *testing.T) {
	t.Parallel()
	c, _ := NewClient("sk_test_x", nil)
	secret := "whsec_test_secret"
	payload := []byte(`{"id":"evt_test","object":"event","type":"checkout.session.completed"}`)
	ts := time.Now().Unix()

	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.%s", ts, payload)
	sig := hex.EncodeToString(mac.Sum(nil))
	header := fmt.Sprintf("t=%d,v1=%s", ts, sig)

	event, err := c.ParseWebhook(payload, header, secret)
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if event.Type != "checkout.session.completed" {
		t.Errorf("event.Type = %q, want checkout.session.completed", event.Type)
	}
	if event.ID != "evt_test" {
		t.Errorf("event.ID = %q, want evt_test", event.ID)
	}
}

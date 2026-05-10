package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emai-ai/swarm/pkg/auth"
	stripepkg "github.com/emai-ai/swarm/pkg/stripe"
	"github.com/emai-ai/swarm/pkg/users"
)

// stripeWebhookSig builds a valid Stripe-Signature header for the given
// payload + secret. Mirrors stripe-go's webhook.ConstructEvent expectations:
// `t=<unix>,v1=<hex(hmac-sha256("<unix>.<payload>"))>`.
func stripeWebhookSig(t *testing.T, secret, payload string) string {
	t.Helper()
	ts := time.Now().Unix()
	signed := fmt.Sprintf("%d.%s", ts, payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signed))
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func newBillingFixture(t *testing.T, slug string) (*fixture, *users.User, *captureEmailSender) {
	t.Helper()
	const userID = "u_alice"
	f := newFixtureWithBinding(t, slug, nil, "saas", userID)
	cap := &captureEmailSender{}
	f.server.email = cap
	f.server.emailFrom = "Kai <noreply@kai.example.org>"

	hash, _ := auth.HashPassword("pw")
	_, _ = f.server.users.Create(context.Background(), users.CreateParams{
		Email: "alice@example.org", PasswordHash: hash, Tier: users.TierFree, Language: users.LangDE, App: users.DefaultApp,
	})
	u, _ := f.server.users.GetByEmail(context.Background(), "alice@example.org")
	return f, u, cap
}

// configureStripe wires a Stripe client + the in-memory tier mapping for
// testing the webhook dispatch path. The API key is a placeholder — none
// of these tests hit the real Stripe API; we only verify webhook handling.
func configureStripe(t *testing.T, f *fixture) {
	t.Helper()
	tierToPrice := map[users.Tier]string{
		users.TierStarter: "price_starter",
		users.TierGrowth:  "price_growth",
	}
	priceToTier := map[string]stripepkg.Tier{
		"price_starter": stripepkg.TierStarter,
		"price_growth":  stripepkg.TierGrowth,
	}
	client, _ := stripepkg.NewClient("sk_test_dummy", priceToTier)
	f.server.stripe = stripeConfig{
		Client:          client,
		WebhookSecret:   "whsec_test_secret_minimum_32_bytes_x",
		TierToPriceID:   tierToPrice,
		SuccessURL:      "https://kai.example.org/workspace/{slug}?upgrade=success",
		CancelURL:       "https://kai.example.org/workspace/{slug}?upgrade=cancel",
		PortalReturnURL: "https://kai.example.org/workspace/{slug}",
	}
}

func TestBilling_Webhook503WhenNotConfigured(t *testing.T) {
	t.Parallel()
	f, _, _ := newBillingFixture(t, "primary")
	// Stripe NOT wired
	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	f.server.handleBillingWebhook(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rr.Code)
	}
}

func TestBilling_WebhookRejectsBadSignature(t *testing.T) {
	t.Parallel()
	f, _, _ := newBillingFixture(t, "primary")
	configureStripe(t, f)
	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", strings.NewReader(`{}`))
	req.Header.Set("Stripe-Signature", "t=0,v1=deadbeef")
	rr := httptest.NewRecorder()
	f.server.handleBillingWebhook(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d (%s)", rr.Code, rr.Body.String())
	}
}

func TestBilling_WebhookRejectsMissingHeader(t *testing.T) {
	t.Parallel()
	f, _, _ := newBillingFixture(t, "primary")
	configureStripe(t, f)
	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	f.server.handleBillingWebhook(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestBilling_WebhookCheckoutCompletedSetsCustomerID(t *testing.T) {
	t.Parallel()
	f, u, _ := newBillingFixture(t, "primary")
	configureStripe(t, f)

	body := fmt.Sprintf(`{
		"id": "evt_test_001",
		"object": "event",
		"type": "checkout.session.completed",
		"data": {
			"object": {
				"id": "cs_test_001",
				"object": "checkout.session",
				"client_reference_id": %q,
				"customer": "cus_test_alice",
				"customer_email": "alice@example.org"
			}
		}
	}`, u.ID)

	sig := stripeWebhookSig(t, f.server.stripe.WebhookSecret, body)
	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", sig)
	rr := httptest.NewRecorder()
	f.server.handleBillingWebhook(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", rr.Code, rr.Body.String())
	}
	got, _ := f.server.users.GetByID(context.Background(), u.ID)
	if got.StripeCustomerID != "cus_test_alice" {
		t.Errorf("StripeCustomerID = %q, want cus_test_alice", got.StripeCustomerID)
	}
}

func TestBilling_WebhookSubscriptionUpdatedSyncsTier(t *testing.T) {
	t.Parallel()
	f, u, _ := newBillingFixture(t, "primary")
	configureStripe(t, f)

	body := fmt.Sprintf(`{
		"id": "evt_test_002",
		"object": "event",
		"type": "customer.subscription.updated",
		"data": {
			"object": {
				"id": "sub_test_001",
				"object": "subscription",
				"status": "active",
				"metadata": { "user_ref": %q },
				"items": {
					"data": [{
						"id": "si_test_001",
						"price": { "id": "price_growth" }
					}]
				}
			}
		}
	}`, u.ID)

	sig := stripeWebhookSig(t, f.server.stripe.WebhookSecret, body)
	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", sig)
	rr := httptest.NewRecorder()
	f.server.handleBillingWebhook(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", rr.Code, rr.Body.String())
	}
	got, _ := f.server.users.GetByID(context.Background(), u.ID)
	if got.Tier != users.TierGrowth {
		t.Errorf("Tier = %q, want growth", got.Tier)
	}
}

// TestBilling_WebhookSubscriptionCreatedSyncsTier — bug fix on 2026-05-10:
// real Stripe fires `customer.subscription.created` (not `.updated`) for
// new subscriptions. Earlier dispatcher only handled `.updated`, leaving
// the user's tier on free after a successful checkout that didn't pass
// `Subscription` inline on the session. Both event names now route to
// `handleSubscriptionUpdated`.
func TestBilling_WebhookSubscriptionCreatedSyncsTier(t *testing.T) {
	t.Parallel()
	f, u, _ := newBillingFixture(t, "primary")
	configureStripe(t, f)

	body := fmt.Sprintf(`{
		"id": "evt_test_sub_created",
		"object": "event",
		"type": "customer.subscription.created",
		"data": {
			"object": {
				"id": "sub_created_001",
				"object": "subscription",
				"status": "active",
				"metadata": { "user_ref": %q },
				"items": {
					"data": [{
						"id": "si_created_001",
						"price": { "id": "price_starter" }
					}]
				}
			}
		}
	}`, u.ID)

	sig := stripeWebhookSig(t, f.server.stripe.WebhookSecret, body)
	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", sig)
	rr := httptest.NewRecorder()
	f.server.handleBillingWebhook(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", rr.Code, rr.Body.String())
	}
	got, _ := f.server.users.GetByID(context.Background(), u.ID)
	if got.Tier != users.TierStarter {
		t.Errorf("Tier = %q, want starter (subscription.created should sync via metadata.user_ref)", got.Tier)
	}
}

// TestBilling_WebhookInvoicePaymentSucceededAliasOfPaid — bug fix on
// 2026-05-10: Stripe fires both `invoice.paid` and
// `invoice.payment_succeeded` for the same condition. Both names must
// hit handleInvoicePaid; the idempotency cache ensures the second one
// is a no-op on the same event.ID.
func TestBilling_WebhookInvoicePaymentSucceededAliasOfPaid(t *testing.T) {
	t.Parallel()
	f, u, cap := newBillingFixture(t, "primary")
	configureStripe(t, f)

	body := fmt.Sprintf(`{
		"id": "evt_test_inv_succ",
		"object": "event",
		"type": "invoice.payment_succeeded",
		"data": {
			"object": {
				"id": "in_test_succ_001",
				"object": "invoice",
				"customer": "cus_test_alice",
				"customer_email": %q,
				"amount_paid": 1000,
				"amount_due": 1000,
				"currency": "eur",
				"hosted_invoice_url": "https://invoice.stripe.com/i/aliased",
				"period_start": 1714521600,
				"period_end": 1717113600,
				"lines": { "data": [{ "description": "Kai Starter" }] }
			}
		}
	}`, u.Email)
	sig := stripeWebhookSig(t, f.server.stripe.WebhookSecret, body)
	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", sig)
	rr := httptest.NewRecorder()
	f.server.handleBillingWebhook(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", rr.Code, rr.Body.String())
	}
	if len(cap.all) != 1 {
		t.Fatalf("expected 1 receipt email from invoice.payment_succeeded alias, got %d", len(cap.all))
	}
}

func TestBilling_WebhookSubscriptionDeletedDowngradesToFree(t *testing.T) {
	t.Parallel()
	f, u, _ := newBillingFixture(t, "primary")
	configureStripe(t, f)
	// Pre-set the user to growth so we can verify the downgrade.
	_ = f.server.users.UpdateTier(context.Background(), u.ID, users.TierGrowth)

	body := fmt.Sprintf(`{
		"id": "evt_test_003",
		"object": "event",
		"type": "customer.subscription.deleted",
		"data": {
			"object": {
				"id": "sub_test_001",
				"object": "subscription",
				"metadata": { "user_ref": %q },
				"items": { "data": [{ "id": "si_x", "price": { "id": "price_growth" } }] }
			}
		}
	}`, u.ID)

	sig := stripeWebhookSig(t, f.server.stripe.WebhookSecret, body)
	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", sig)
	rr := httptest.NewRecorder()
	f.server.handleBillingWebhook(rr, req)

	got, _ := f.server.users.GetByID(context.Background(), u.ID)
	if got.Tier != users.TierFree {
		t.Errorf("Tier = %q, want free", got.Tier)
	}
}

func TestBilling_WebhookIdempotent(t *testing.T) {
	t.Parallel()
	f, u, _ := newBillingFixture(t, "primary")
	configureStripe(t, f)

	body := fmt.Sprintf(`{
		"id": "evt_idem_001",
		"object": "event",
		"type": "checkout.session.completed",
		"data": {
			"object": {
				"id": "cs_idem_001",
				"object": "checkout.session",
				"client_reference_id": %q,
				"customer": "cus_first"
			}
		}
	}`, u.ID)
	sig := stripeWebhookSig(t, f.server.stripe.WebhookSecret, body)

	// First delivery → applies.
	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", sig)
	rr := httptest.NewRecorder()
	f.server.handleBillingWebhook(rr, req)
	got, _ := f.server.users.GetByID(context.Background(), u.ID)
	if got.StripeCustomerID != "cus_first" {
		t.Fatalf("first delivery didn't set customer id: %q", got.StripeCustomerID)
	}

	// Manually re-set the customer id to a different value to detect re-processing.
	_ = f.server.users.UpdateStripeCustomerID(context.Background(), u.ID, "cus_manually_overridden")

	// Second delivery (same event.ID + valid sig) → ack-without-reprocessing.
	sig2 := stripeWebhookSig(t, f.server.stripe.WebhookSecret, body)
	req = httptest.NewRequest(http.MethodPost, "/api/billing/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", sig2)
	rr = httptest.NewRecorder()
	f.server.handleBillingWebhook(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("duplicate delivery: status %d", rr.Code)
	}
	got, _ = f.server.users.GetByID(context.Background(), u.ID)
	if got.StripeCustomerID != "cus_manually_overridden" {
		t.Errorf("duplicate delivery re-processed (expected idempotent): %q", got.StripeCustomerID)
	}
}

func TestBilling_WebhookInvoicePaidSendsReceipt(t *testing.T) {
	t.Parallel()
	f, u, cap := newBillingFixture(t, "primary")
	configureStripe(t, f)

	body := fmt.Sprintf(`{
		"id": "evt_test_010",
		"object": "event",
		"type": "invoice.paid",
		"data": {
			"object": {
				"id": "in_test_001",
				"object": "invoice",
				"customer": "cus_test_alice",
				"customer_email": %q,
				"amount_paid": 1000,
				"currency": "eur",
				"period_start": 1746000000,
				"period_end": 1748678400,
				"hosted_invoice_url": "https://invoice.stripe.com/i/test",
				"lines": {
					"data": [{ "description": "Kai Starter — monthly" }]
				}
			}
		}
	}`, u.Email)

	sig := stripeWebhookSig(t, f.server.stripe.WebhookSecret, body)
	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", sig)
	rr := httptest.NewRecorder()
	f.server.handleBillingWebhook(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", rr.Code, rr.Body.String())
	}
	if len(cap.all) != 1 {
		t.Fatalf("expected 1 receipt email, got %d", len(cap.all))
	}
	mail := cap.all[0]
	if !strings.Contains(mail.HTML, "Kai Starter") {
		t.Errorf("HTML missing plan name\n%s", mail.HTML)
	}
	if !strings.Contains(mail.HTML, "EUR 10.00") {
		t.Errorf("HTML missing amount EUR 10.00\n%s", mail.HTML)
	}
}

func TestBilling_WebhookInvoicePaymentFailedSendsDunning(t *testing.T) {
	t.Parallel()
	f, u, cap := newBillingFixture(t, "primary")
	configureStripe(t, f)

	body := fmt.Sprintf(`{
		"id": "evt_test_011",
		"object": "event",
		"type": "invoice.payment_failed",
		"data": {
			"object": {
				"id": "in_test_002",
				"object": "invoice",
				"customer": "cus_test_alice",
				"customer_email": %q,
				"amount_paid": 0,
				"amount_due": 1000,
				"currency": "eur",
				"hosted_invoice_url": "https://invoice.stripe.com/i/retry",
				"lines": { "data": [{ "description": "Kai Growth — monthly" }] }
			}
		}
	}`, u.Email)

	sig := stripeWebhookSig(t, f.server.stripe.WebhookSecret, body)
	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", sig)
	rr := httptest.NewRecorder()
	f.server.handleBillingWebhook(rr, req)

	if len(cap.all) != 1 {
		t.Fatalf("expected 1 dunning email, got %d", len(cap.all))
	}
	if !strings.Contains(cap.all[0].HTML, "fehlgeschlagen") && !strings.Contains(cap.all[0].HTML, "failed") {
		t.Errorf("HTML missing failure copy\n%s", cap.all[0].HTML)
	}
}

// TestBilling_DunningFirstAttempt — Phase 2 dunning state machine: first
// failure (attempt_count=1, next_payment_attempt > 0) sends the standard
// "we'll retry" copy + the actual retry date from Stripe's payload.
func TestBilling_DunningFirstAttempt(t *testing.T) {
	t.Parallel()
	f, u, cap := newBillingFixture(t, "primary")
	configureStripe(t, f)

	// next_payment_attempt = 1779696000 = 2026-05-25 08:00 UTC.
	body := fmt.Sprintf(`{
		"id": "evt_test_dunning_1",
		"object": "event",
		"type": "invoice.payment_failed",
		"data": {
			"object": {
				"id": "in_test_d1",
				"object": "invoice",
				"customer": "cus_test_alice",
				"customer_email": %q,
				"amount_due": 1000,
				"currency": "eur",
				"attempt_count": 1,
				"next_payment_attempt": 1779696000,
				"hosted_invoice_url": "https://invoice.stripe.com/i/d1",
				"lines": { "data": [{ "description": "Kai Growth — monthly" }] }
			}
		}
	}`, u.Email)
	sig := stripeWebhookSig(t, f.server.stripe.WebhookSecret, body)
	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", sig)
	rr := httptest.NewRecorder()
	f.server.handleBillingWebhook(rr, req)

	if len(cap.all) != 1 {
		t.Fatalf("expected 1 email, got %d", len(cap.all))
	}
	mail := cap.all[0]
	// First-attempt copy: standard "we'll retry on <date>" + non-final subject.
	if !strings.Contains(mail.HTML, "2026-05-25") {
		t.Errorf("HTML missing retry date 2026-05-25\n%s", mail.HTML)
	}
	if strings.Contains(mail.Subject, "Letzte Mahnung") || strings.Contains(mail.Subject, "Final notice") {
		t.Errorf("first-attempt subject should not be the final-notice variant: %q", mail.Subject)
	}
	// Attempt count 1 must NOT render "Versuch N" / "attempt N" parenthetical.
	if strings.Contains(mail.HTML, "(Versuch") || strings.Contains(mail.HTML, "(attempt") {
		t.Errorf("first-attempt HTML should not show the attempt parenthetical\n%s", mail.HTML)
	}
}

// TestBilling_DunningRetryAttemptShowsCount — retry attempt (attempt_count >= 2,
// next_payment_attempt > 0) renders the same "we'll retry" branch but
// includes the attempt number in the body so the user sees they're not
// on the first failure anymore.
func TestBilling_DunningRetryAttemptShowsCount(t *testing.T) {
	t.Parallel()
	f, u, cap := newBillingFixture(t, "primary")
	configureStripe(t, f)

	body := fmt.Sprintf(`{
		"id": "evt_test_dunning_2",
		"object": "event",
		"type": "invoice.payment_failed",
		"data": {
			"object": {
				"id": "in_test_d2",
				"object": "invoice",
				"customer": "cus_test_alice",
				"customer_email": %q,
				"amount_due": 1000,
				"currency": "eur",
				"attempt_count": 2,
				"next_payment_attempt": 1779696000,
				"hosted_invoice_url": "https://invoice.stripe.com/i/d2",
				"lines": { "data": [{ "description": "Kai Growth — monthly" }] }
			}
		}
	}`, u.Email)
	sig := stripeWebhookSig(t, f.server.stripe.WebhookSecret, body)
	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", sig)
	rr := httptest.NewRecorder()
	f.server.handleBillingWebhook(rr, req)

	if len(cap.all) != 1 {
		t.Fatalf("expected 1 email, got %d", len(cap.all))
	}
	mail := cap.all[0]
	// Retry attempt #2 surfaces the attempt count; copy is still the
	// non-final variant.
	if !strings.Contains(mail.HTML, "Versuch 2") && !strings.Contains(mail.HTML, "attempt 2") {
		t.Errorf("HTML missing attempt count\n%s", mail.HTML)
	}
}

// TestBilling_DunningFinalAttemptWarnsOfDowngrade — Stripe has exhausted
// retries (next_payment_attempt = 0) so the email shifts to the final-notice
// variant: subject mentions pause, body announces downgrade.
func TestBilling_DunningFinalAttemptWarnsOfDowngrade(t *testing.T) {
	t.Parallel()
	f, u, cap := newBillingFixture(t, "primary")
	configureStripe(t, f)

	body := fmt.Sprintf(`{
		"id": "evt_test_dunning_3",
		"object": "event",
		"type": "invoice.payment_failed",
		"data": {
			"object": {
				"id": "in_test_d3",
				"object": "invoice",
				"customer": "cus_test_alice",
				"customer_email": %q,
				"amount_due": 1000,
				"currency": "eur",
				"attempt_count": 4,
				"next_payment_attempt": 0,
				"hosted_invoice_url": "https://invoice.stripe.com/i/d3",
				"lines": { "data": [{ "description": "Kai Growth — monthly" }] }
			}
		}
	}`, u.Email)
	sig := stripeWebhookSig(t, f.server.stripe.WebhookSecret, body)
	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", sig)
	rr := httptest.NewRecorder()
	f.server.handleBillingWebhook(rr, req)

	if len(cap.all) != 1 {
		t.Fatalf("expected 1 email, got %d", len(cap.all))
	}
	mail := cap.all[0]
	// Final-notice subject + body announces the upcoming downgrade /
	// pause. German fixture (default Lang on the test user is DE).
	if !strings.Contains(mail.Subject, "Letzte Mahnung") && !strings.Contains(mail.Subject, "Final notice") {
		t.Errorf("subject should mention final notice, got %q", mail.Subject)
	}
	if !strings.Contains(mail.HTML, "Free-Tarif") && !strings.Contains(mail.HTML, "free tier") &&
		!strings.Contains(mail.HTML, "pausieren") && !strings.Contains(mail.HTML, "paused") {
		t.Errorf("HTML missing downgrade warning\n%s", mail.HTML)
	}
}

func TestBilling_CheckoutRejectsUnauthed(t *testing.T) {
	t.Parallel()
	f, _, _ := newBillingFixture(t, "primary")
	configureStripe(t, f)
	req := slugReq(http.MethodPost, "/api/workspace/primary/billing/checkout", "primary", "")
	req.Body = nopCloser(bytes.NewReader([]byte(`{"tier":"starter"}`)))
	rr := httptest.NewRecorder()
	f.server.handleBillingCheckout(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestBilling_CheckoutRejectsBadTier(t *testing.T) {
	t.Parallel()
	f, u, _ := newBillingFixture(t, "primary")
	configureStripe(t, f)
	req := authedAccountReq(t, f, http.MethodPost, "/api/workspace/primary/billing/checkout", "primary", u.ID)
	req.Body = nopCloser(bytes.NewReader([]byte(`{"tier":"enterprise"}`)))
	rr := httptest.NewRecorder()
	f.server.handleBillingCheckout(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestBilling_CheckoutRejectsLegacyInternalSession(t *testing.T) {
	t.Parallel()
	f, _, _ := newBillingFixture(t, "primary")
	configureStripe(t, f)
	cookie, _ := auth.IssueSession("primary", "old-admin@example.com", f.jwtSecret, time.Now())
	req := slugReq(http.MethodPost, "/api/workspace/primary/billing/checkout", "primary", "")
	req.AddCookie(cookie)
	req.Body = nopCloser(bytes.NewReader([]byte(`{"tier":"starter"}`)))
	rr := httptest.NewRecorder()
	f.server.handleBillingCheckout(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("legacy: expected 403, got %d", rr.Code)
	}
}

func TestBilling_PortalRefusesUserWithoutStripeCustomer(t *testing.T) {
	t.Parallel()
	f, u, _ := newBillingFixture(t, "primary")
	configureStripe(t, f)
	req := authedAccountReq(t, f, http.MethodPost, "/api/workspace/primary/billing/portal", "primary", u.ID)
	rr := httptest.NewRecorder()
	f.server.handleBillingPortal(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for user without StripeCustomerID, got %d (%s)", rr.Code, rr.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["error"] != "no_stripe_customer" {
		t.Errorf("error body = %v", body)
	}
}

func TestBilling_FormatAmountEUR(t *testing.T) {
	t.Parallel()
	cases := []struct {
		minor    int64
		currency string
		want     string
	}{
		{1000, "eur", "EUR 10.00"},
		{999, "usd", "USD 9.99"},
		{0, "eur", "EUR 0.00"},
		{2999, "EUR", "EUR 29.99"},
	}
	for _, c := range cases {
		if got := formatAmount(c.minor, c.currency); got != c.want {
			t.Errorf("formatAmount(%d, %q) = %q, want %q", c.minor, c.currency, got, c.want)
		}
	}
}

func TestBilling_SubstituteSlug(t *testing.T) {
	t.Parallel()
	if got := substituteSlug("https://x/{slug}/y", "anna"); got != "https://x/anna/y" {
		t.Errorf("substituteSlug = %q", got)
	}
	if got := substituteSlug("no-placeholder", "x"); got != "no-placeholder" {
		t.Errorf("no placeholder: %q", got)
	}
}

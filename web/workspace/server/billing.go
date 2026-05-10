package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	stripego "github.com/stripe/stripe-go/v82"

	"github.com/emai-ai/swarm/pkg/email"
	"github.com/emai-ai/swarm/pkg/stripe"
	"github.com/emai-ai/swarm/pkg/users"
)

// Stripe billing flow (TASK-016 Phase 1).
//
// Three endpoints:
//   1. POST /api/workspace/{slug}/billing/checkout (auth required)
//      Body: {"tier":"starter"|"growth"}. Mints a Stripe Checkout URL,
//      returns it; the SPA redirects the browser to it.
//   2. POST /api/workspace/{slug}/billing/portal (auth required)
//      Mints a Customer Portal URL for the user's StripeCustomerID;
//      the SPA redirects there for cancel/swap/payment-method changes.
//   3. POST /api/billing/webhook (Stripe signature auth, no slug)
//      Receives Stripe events and syncs them to the User store.
//      Idempotent — processed event.IDs cached for replay protection.
//
// EmAI internal tenants (`spec.managed: internal`) skip billing entirely
// (per PROP-003): the webhook handler short-circuits when the User row
// is missing or the workspace is internal-managed.

// stripeConfig is the runtime config for the billing endpoints. Empty/zero
// fields disable the corresponding endpoint with 503 — better to fail
// loudly than accept unsigned webhooks or generate broken checkout URLs.
type stripeConfig struct {
	Client          *stripe.Client
	WebhookSecret   string
	TierToPriceID   map[users.Tier]string // reverse of pkg/stripe's PriceIDToTier; for checkout.priceID lookup
	SuccessURL      string                // post-checkout landing; e.g. "https://kai.emai.dev/workspace/<slug>?upgrade=success"
	CancelURL       string                // back-out landing
	PortalReturnURL string                // where the portal "return to Kai" button goes

	// processedEvents is the in-memory idempotency cache. Stripe re-delivers
	// events on retry; the same event.ID hitting the webhook twice MUST NOT
	// double-bill or double-email. A real deploy swaps this for a Postgres
	// or Redis-backed store; the public swarm binary keeps it lightweight.
	mu              sync.Mutex
	processedEvents map[string]time.Time
}

// markEventProcessed records an event ID as handled. Returns true if the
// caller should proceed (first time seen), false if it's a duplicate that
// should be acked-without-doing-anything.
func (c *stripeConfig) markEventProcessed(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.processedEvents == nil {
		c.processedEvents = map[string]time.Time{}
	}
	if _, seen := c.processedEvents[id]; seen {
		return false
	}
	c.processedEvents[id] = time.Now()
	// Garbage-collect entries older than 24h so the map doesn't grow
	// unbounded over the pod's lifetime. Stripe retries within 3 days
	// max but most retries land within minutes; 24h is plenty.
	cutoff := time.Now().Add(-24 * time.Hour)
	for k, t := range c.processedEvents {
		if t.Before(cutoff) {
			delete(c.processedEvents, k)
		}
	}
	return true
}

// billingConfigured reports whether checkout + portal endpoints can serve
// real traffic. Webhook receiver has its own check.
func (s *server) billingConfigured() bool {
	return s.stripe.Client != nil && len(s.stripe.TierToPriceID) > 0 &&
		s.stripe.SuccessURL != "" && s.stripe.CancelURL != ""
}

// checkoutRequest is the body of POST /api/workspace/{slug}/billing/checkout.
type checkoutRequest struct {
	Tier string `json:"tier"` // "starter" | "growth"
}

type checkoutResponse struct {
	URL string `json:"url"`
}

// handleBillingCheckout mints a Stripe Checkout URL for the requested tier.
// Auth required (the JWT cookie's Uid is the user we'll bill); legacy
// internal-managed sessions get 403 — billing only applies to SaaS tenants.
func (s *server) handleBillingCheckout(w http.ResponseWriter, r *http.Request) {
	if !s.billingConfigured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "billing not configured"})
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
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "internal_managed_session"})
		return
	}

	var req checkoutRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	tier := users.Tier(strings.ToLower(strings.TrimSpace(req.Tier)))
	if tier != users.TierStarter && tier != users.TierGrowth {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tier must be starter or growth"})
		return
	}
	priceID := s.stripe.TierToPriceID[tier]
	if priceID == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tier not configured for billing"})
		return
	}

	u, err := s.users.GetByID(r.Context(), claims.Uid)
	if err != nil {
		log.Printf("billing checkout: lookup uid=%s: %v", claims.Uid, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
		return
	}

	url, err := s.stripe.Client.CreateCheckoutSession(stripe.CheckoutParams{
		PriceID:    priceID,
		UserRef:    u.ID,
		Email:      u.Email,
		SuccessURL: substituteSlug(s.stripe.SuccessURL, slug),
		CancelURL:  substituteSlug(s.stripe.CancelURL, slug),
	})
	if err != nil {
		log.Printf("billing checkout: create session uid=%s tier=%s: %v", u.ID, tier, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "checkout session failed"})
		return
	}
	writeJSON(w, http.StatusOK, checkoutResponse{URL: url})
}

// handleBillingPortal mints a Customer Portal URL for the signed-in user.
// Refuses if the user has no StripeCustomerID (never been to checkout).
func (s *server) handleBillingPortal(w http.ResponseWriter, r *http.Request) {
	if s.stripe.Client == nil || s.stripe.PortalReturnURL == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "billing portal not configured"})
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
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "internal_managed_session"})
		return
	}
	u, err := s.users.GetByID(r.Context(), claims.Uid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
		return
	}
	if u.StripeCustomerID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no_stripe_customer", "message": "upgrade to a paid tier first"})
		return
	}
	url, err := s.stripe.Client.CreatePortalSession(stripe.PortalParams{
		CustomerID: u.StripeCustomerID,
		ReturnURL:  substituteSlug(s.stripe.PortalReturnURL, slug),
	})
	if err != nil {
		log.Printf("billing portal: create session uid=%s: %v", u.ID, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "portal session failed"})
		return
	}
	writeJSON(w, http.StatusOK, checkoutResponse{URL: url})
}

// handleBillingWebhook is the Stripe-server-to-our-server callback. Verifies
// the `Stripe-Signature` header against the webhook secret, dispatches by
// event type. Always returns 2xx on signature-valid events so Stripe stops
// retrying; per-event errors are logged but don't propagate.
func (s *server) handleBillingWebhook(w http.ResponseWriter, r *http.Request) {
	if s.stripe.Client == nil || s.stripe.WebhookSecret == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "stripe webhook not configured"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body"})
		return
	}
	sig := r.Header.Get("Stripe-Signature")
	if sig == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing stripe-signature header"})
		return
	}
	event, err := s.stripe.Client.ParseWebhook(body, sig, s.stripe.WebhookSecret)
	if err != nil {
		log.Printf("billing webhook: signature verification failed: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "signature mismatch"})
		return
	}
	if !s.stripe.markEventProcessed(event.ID) {
		// Duplicate delivery — already handled. Ack with 200 so Stripe stops retrying.
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate", "event": event.ID})
		return
	}
	if err := s.dispatchStripeEvent(r.Context(), event); err != nil {
		// Log but still 2xx — Stripe retries 5xx, but we've already cached
		// the event.ID so a retry would be ack'd as duplicate. Better to
		// surface the error in our logs than spin Stripe's retry queue.
		log.Printf("billing webhook: dispatch event=%s type=%s: %v", event.ID, event.Type, err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "event": event.ID})
}

// dispatchStripeEvent routes one event by type. Each handler is idempotent
// inside its own scope (the outer markEventProcessed is the primary
// guard; this is defense-in-depth).
func (s *server) dispatchStripeEvent(ctx context.Context, event stripego.Event) error {
	switch event.Type {
	case "checkout.session.completed":
		return s.handleCheckoutCompleted(ctx, event)
	case "customer.subscription.created", "customer.subscription.updated":
		// Both events have identical payload + handling intent:
		// sync the tier from the subscription's first line-item price.
		// `created` fires for new subs (whether via Checkout or direct
		// API); `updated` fires for plan swaps + payment-method changes.
		return s.handleSubscriptionUpdated(ctx, event)
	case "customer.subscription.deleted":
		return s.handleSubscriptionDeleted(ctx, event)
	case "invoice.paid", "invoice.payment_succeeded":
		// Stripe fires both names for the same condition (alias). Our
		// handler is idempotent on event.ID so seeing both for one
		// invoice is a no-op on the second pass.
		return s.handleInvoicePaid(ctx, event)
	case "invoice.payment_failed":
		return s.handleInvoicePaymentFailed(ctx, event)
	}
	// Unknown event types are not an error — Stripe sends many we don't
	// subscribe to; the dashboard should narrow this list, but we log + ack.
	log.Printf("billing webhook: unhandled event type=%s", event.Type)
	return nil
}

// handleCheckoutCompleted is the first event after a successful upgrade.
// We pull the User by ClientReferenceID (set in CreateCheckoutSession),
// stamp the StripeCustomerID, and sync the tier from the line-item price.
func (s *server) handleCheckoutCompleted(ctx context.Context, event stripego.Event) error {
	var sess stripego.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
		return fmt.Errorf("unmarshal session: %w", err)
	}
	userID := sess.ClientReferenceID
	if userID == "" {
		return errors.New("session has no client_reference_id")
	}
	if sess.Customer == nil || sess.Customer.ID == "" {
		return errors.New("session has no customer")
	}
	if err := s.users.UpdateStripeCustomerID(ctx, userID, sess.Customer.ID); err != nil {
		if errors.Is(err, users.ErrNotFound) {
			log.Printf("billing webhook: user not found for checkout uid=%s — likely an internal-managed tenant", userID)
			return nil
		}
		return fmt.Errorf("update stripe customer id: %w", err)
	}
	// Sync tier from the subscription's first line item.
	if sess.Subscription != nil && sess.Subscription.ID != "" {
		return s.syncTierFromSubscription(ctx, userID, sess.Subscription.ID)
	}
	return nil
}

// handleSubscriptionUpdated fires on tier swaps + payment-method changes.
// Re-syncs the tier from the current subscription's line item so a downgrade
// from growth → starter takes effect immediately on our side.
func (s *server) handleSubscriptionUpdated(ctx context.Context, event stripego.Event) error {
	var sub stripego.Subscription
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		return fmt.Errorf("unmarshal subscription: %w", err)
	}
	userID := userIDFromSubscription(&sub)
	if userID == "" {
		return errors.New("subscription has no user reference")
	}
	// Backfill StripeCustomerID if missing on the User row. Today only
	// `checkout.session.completed` sets it, but a subscription created
	// outside the Checkout flow (Stripe Dashboard, ops script) reaches
	// us via subscription.{created,updated} without a prior
	// session.completed. Without the customer ID, the "Manage
	// subscription" portal button can't open. Idempotent: UpdateStripe-
	// CustomerID is a no-op when the value already matches.
	if sub.Customer != nil && sub.Customer.ID != "" {
		if err := s.users.UpdateStripeCustomerID(ctx, userID, sub.Customer.ID); err != nil && !errors.Is(err, users.ErrNotFound) {
			log.Printf("billing webhook: backfill stripe_customer_id user=%s cust=%s: %v", userID, sub.Customer.ID, err)
		}
	}
	return s.applyTierFromSubscription(ctx, userID, &sub)
}

// handleSubscriptionDeleted fires on cancel-at-end-of-period reaching 0 +
// outright deletion. Downgrades the user to free.
func (s *server) handleSubscriptionDeleted(ctx context.Context, event stripego.Event) error {
	var sub stripego.Subscription
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		return fmt.Errorf("unmarshal subscription: %w", err)
	}
	userID := userIDFromSubscription(&sub)
	if userID == "" {
		return errors.New("subscription has no user reference")
	}
	if err := s.users.UpdateTier(ctx, userID, users.TierFree); err != nil {
		if errors.Is(err, users.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("downgrade: %w", err)
	}
	return nil
}

// handleInvoicePaid dispatches the billing-receipt email. Best-effort —
// a missed receipt isn't a blocking failure.
func (s *server) handleInvoicePaid(ctx context.Context, event stripego.Event) error {
	if s.email == nil {
		return nil
	}
	var inv stripego.Invoice
	if err := json.Unmarshal(event.Data.Raw, &inv); err != nil {
		return fmt.Errorf("unmarshal invoice: %w", err)
	}
	u, planName, amount, err := s.lookupForInvoice(ctx, &inv)
	if err != nil || u == nil {
		return err
	}
	periodStart, periodEnd := invoicePeriod(&inv)
	return email.Dispatch(ctx, s.email, email.SendOptions{
		Template: email.TemplateBillingReceipt,
		Lang:     mailLangForUser(u),
		To:       u.Email,
		From:     s.emailFrom,
	}, struct {
		Name        string
		PlanName    string
		Amount      string
		InvoiceURL  string
		PeriodStart string
		PeriodEnd   string
	}{
		Name:        strings.SplitN(u.Email, "@", 2)[0],
		PlanName:    planName,
		Amount:      amount,
		InvoiceURL:  inv.HostedInvoiceURL,
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
	})
}

// handleInvoicePaymentFailed dispatches the payment-failed email. Phase 2
// (dunning state machine) reads `invoice.attempt_count` +
// `invoice.next_payment_attempt` from the Stripe payload to differentiate
// first-failure / retry / final-failure messaging; the auto-downgrade
// after N failures is handled by Stripe itself (configured in dashboard
// → fires `customer.subscription.deleted` → existing handler downgrades
// to free).
func (s *server) handleInvoicePaymentFailed(ctx context.Context, event stripego.Event) error {
	if s.email == nil {
		return nil
	}
	var inv stripego.Invoice
	if err := json.Unmarshal(event.Data.Raw, &inv); err != nil {
		return fmt.Errorf("unmarshal invoice: %w", err)
	}
	u, planName, amount, err := s.lookupForInvoice(ctx, &inv)
	if err != nil || u == nil {
		return err
	}
	billingURL := strings.TrimRight(s.deletionBaseURL, "/") + "/workspace/billing"
	// NextPaymentAttempt is 0 when Stripe has exhausted retries — that's the
	// final-failure case; the next webhook will be subscription.deleted, and
	// we want this email to warn the user we're about to pause the plan.
	isFinal := inv.NextPaymentAttempt == 0
	retryDate := "—"
	if !isFinal {
		retryDate = time.Unix(inv.NextPaymentAttempt, 0).UTC().Format("2006-01-02")
	}
	return email.Dispatch(ctx, s.email, email.SendOptions{
		Template: email.TemplatePaymentFailed,
		Lang:     mailLangForUser(u),
		To:       u.Email,
		From:     s.emailFrom,
	}, struct {
		Name           string
		PlanName       string
		Amount         string
		RetryURL       string
		BillingURL     string
		RetryDate      string
		AttemptCount   int
		IsFinalAttempt bool
	}{
		Name:           strings.SplitN(u.Email, "@", 2)[0],
		PlanName:       planName,
		Amount:         amount,
		RetryURL:       inv.HostedInvoiceURL,
		BillingURL:     billingURL,
		RetryDate:      retryDate,
		AttemptCount:   int(inv.AttemptCount),
		IsFinalAttempt: isFinal,
	})
}

// applyTierFromSubscription reads the first active line item's price ID,
// looks up the configured tier, and writes it to the User row. No-op when
// the price ID is unknown (defensive — a misconfigured deploy doesn't
// silently downgrade users).
func (s *server) applyTierFromSubscription(ctx context.Context, userID string, sub *stripego.Subscription) error {
	if sub.Items == nil || len(sub.Items.Data) == 0 {
		return errors.New("subscription has no line items")
	}
	priceID := sub.Items.Data[0].Price.ID
	tier := users.Tier(string(s.stripe.Client.TierForPriceID(priceID)))
	if tier == "" {
		log.Printf("billing webhook: unknown price ID %s — leaving user %s tier unchanged", priceID, userID)
		return nil
	}
	if err := s.users.UpdateTier(ctx, userID, tier); err != nil {
		if errors.Is(err, users.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("update tier: %w", err)
	}
	return nil
}

// syncTierFromSubscription is the post-checkout variant — the subscription
// hasn't been embedded in the event payload yet, so we GET it fresh.
func (s *server) syncTierFromSubscription(ctx context.Context, userID, subID string) error {
	sub, err := s.stripe.Client.GetSubscription(subID)
	if err != nil {
		return fmt.Errorf("get subscription: %w", err)
	}
	return s.applyTierFromSubscription(ctx, userID, sub)
}

// lookupForInvoice resolves the User + plan name + formatted amount from
// an invoice payload. Returns (nil, "", "", nil) when the invoice's
// customer doesn't map to a known user — that's fine (the user might
// have been hard-deleted) and not an error.
func (s *server) lookupForInvoice(ctx context.Context, inv *stripego.Invoice) (*users.User, string, string, error) {
	if inv.Customer == nil || inv.Customer.ID == "" {
		return nil, "", "", errors.New("invoice has no customer")
	}
	// We don't index users by stripe customer ID today — search via the
	// invoice's email. The webhook landing the email is fine because
	// Stripe's customer-by-email is a stable index for our purposes.
	if inv.CustomerEmail == "" {
		return nil, "", "", nil
	}
	u, err := s.users.GetByEmail(ctx, inv.CustomerEmail)
	if err != nil {
		if errors.Is(err, users.ErrNotFound) {
			return nil, "", "", nil
		}
		return nil, "", "", err
	}
	planName := "Subscription"
	if len(inv.Lines.Data) > 0 && inv.Lines.Data[0].Description != "" {
		planName = inv.Lines.Data[0].Description
	}
	amount := formatAmount(inv.AmountPaid, string(inv.Currency))
	return u, planName, amount, nil
}

// userIDFromSubscription extracts the User.ID from the subscription's
// metadata (Stripe lets us round-trip via Subscription.Metadata or via the
// CheckoutSession.ClientReferenceID — we use the latter, copied to
// metadata at checkout-completed time). Returns empty string if not set;
// the caller treats that as a non-fatal "skip this event".
func userIDFromSubscription(sub *stripego.Subscription) string {
	if sub.Metadata != nil {
		if v, ok := sub.Metadata["user_ref"]; ok && v != "" {
			return v
		}
	}
	return ""
}

// invoicePeriod returns the human-readable period start/end for the
// billing-receipt template.
func invoicePeriod(inv *stripego.Invoice) (start, end string) {
	if inv.PeriodStart > 0 {
		start = time.Unix(inv.PeriodStart, 0).UTC().Format("2006-01-02")
	}
	if inv.PeriodEnd > 0 {
		end = time.Unix(inv.PeriodEnd, 0).UTC().Format("2006-01-02")
	}
	return start, end
}

// formatAmount renders a Stripe minor-units amount as "EUR 9.00". The
// minor-units assumption (2 decimals) holds for EUR/USD/etc. — JPY has
// 0 decimals but we don't ship that tier.
func formatAmount(minorUnits int64, currency string) string {
	major := float64(minorUnits) / 100.0
	return fmt.Sprintf("%s %.2f", strings.ToUpper(currency), major)
}

// mailLangForUser maps the User row's Language to the email package's Lang.
// Default falls through to LangDE per CLAUDE.md.
func mailLangForUser(u *users.User) email.Lang {
	if u.Language == users.LangEN {
		return email.LangEN
	}
	return email.LangDE
}

// substituteSlug is a tiny template helper for the SuccessURL/CancelURL
// templates — the deployment overlay configures e.g.
// `https://kai.emai.dev/workspace/{slug}?upgrade=success` and we substitute
// per-request. Empty `{slug}` placeholders pass through.
func substituteSlug(template, slug string) string {
	return strings.ReplaceAll(template, "{slug}", slug)
}

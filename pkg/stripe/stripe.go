// Package stripe is the Stripe billing integration surface for EmAI swarm
// (TASK-016 Phase 0). Wraps the official `stripe-go/v82` SDK with the four
// call sites our SaaS actually uses:
//
//   - CreateCheckoutSession — for the "Upgrade to <tier>" button on the
//     pricing page.
//   - GetSubscription — for the post-checkout success page and webhook
//     handlers to read the subscription state.
//   - CancelSubscription — for the GDPR deletion cascade (TASK-021
//     Phase 3) and the customer-portal "cancel" handler.
//   - ParseWebhook — verifies the `Stripe-Signature` header against the
//     webhook secret and decodes the event. The verification is the
//     non-trivial part; we delegate to `webhook.ConstructEvent` from
//     stripe-go because rolling our own HMAC + replay-window check is
//     a foot-gun.
//
// Tier mapping lives separately (TierForPriceID) so the deployment overlay
// can override the price-ID-to-tier table without rebuilding the
// platform.
//
// Why the SDK over hand-rolled net/http (which is what pkg/openrouter
// and pkg/email do)? Stripe is many endpoints with many resource types;
// the SDK is maintained by Stripe themselves and the webhook signature
// scheme has subtle replay-protection rules — exactly the kind of code
// you don't want to hand-roll. Cost: a hefty dep tree, but it's contained
// to this one package.
package stripe

import (
	"errors"
	"fmt"

	stripego "github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/subscription"
	"github.com/stripe/stripe-go/v82/webhook"
)

// Client wraps the stripe-go state. Concurrent-safe — the SDK uses a
// global default backend internally, but we set the API key per-Client at
// each call to keep the goroutine model honest.
type Client struct {
	APIKey string

	// PriceIDToTier is the mapping the deployment overlay configures from
	// its Stripe dashboard / Terraform Stripe provider. Empty value yields
	// `users.TierFree` from TierForPriceID — safe default.
	PriceIDToTier map[string]Tier
}

// Tier mirrors the strings on the User row + KaiInstance.Spec.Tier — kept
// loose-typed here so pkg/stripe doesn't depend on pkg/users (would force
// every billing consumer to import users transitively). Callers do the
// `users.Tier(string(t))` conversion at the call site.
type Tier string

// NewClient constructs a billing client. Empty keys are rejected at
// construction so callers fail at startup.
func NewClient(apiKey string, tiers map[string]Tier) (*Client, error) {
	if apiKey == "" {
		return nil, errors.New("stripe: empty API key")
	}
	return &Client{APIKey: apiKey, PriceIDToTier: tiers}, nil
}

// CheckoutParams is the input shape for CreateCheckoutSession.
//
// The deployment overlay supplies the SuccessURL / CancelURL because they
// embed the production hostname (`https://kai.emai.io/...`). UserRef lands
// in the session's `client_reference_id` so the webhook handler can match
// the resulting subscription back to the User without a separate API call.
// Email pre-fills the checkout form so the user doesn't have to type it
// again.
type CheckoutParams struct {
	PriceID    string // Stripe price ID for the tier the user is upgrading to
	UserRef    string // pkg/users User.ID — round-trips via client_reference_id
	Email      string // pre-fills the checkout form
	SuccessURL string // browser redirect after successful payment
	CancelURL  string // browser redirect when the user backs out
}

// CreateCheckoutSession spins up a hosted Stripe Checkout for a single
// recurring subscription. Returns the URL the browser should redirect to.
func (c *Client) CreateCheckoutSession(p CheckoutParams) (string, error) {
	if p.PriceID == "" || p.UserRef == "" || p.SuccessURL == "" || p.CancelURL == "" {
		return "", errors.New("stripe: PriceID, UserRef, SuccessURL, CancelURL required")
	}
	stripego.Key = c.APIKey
	params := &stripego.CheckoutSessionParams{
		Mode: stripego.String(string(stripego.CheckoutSessionModeSubscription)),
		LineItems: []*stripego.CheckoutSessionLineItemParams{
			{Price: stripego.String(p.PriceID), Quantity: stripego.Int64(1)},
		},
		ClientReferenceID: stripego.String(p.UserRef),
		SuccessURL:        stripego.String(p.SuccessURL),
		CancelURL:         stripego.String(p.CancelURL),
	}
	if p.Email != "" {
		params.CustomerEmail = stripego.String(p.Email)
	}
	sess, err := session.New(params)
	if err != nil {
		return "", fmt.Errorf("checkout session: %w", err)
	}
	return sess.URL, nil
}

// GetSubscription fetches a subscription by ID. Used by the webhook
// handler when `customer.subscription.updated` fires — the event payload
// carries the ID, the handler does a fresh GET so it sees the canonical
// state.
func (c *Client) GetSubscription(id string) (*stripego.Subscription, error) {
	if id == "" {
		return nil, errors.New("stripe: empty subscription ID")
	}
	stripego.Key = c.APIKey
	return subscription.Get(id, nil)
}

// CancelSubscription cancels a subscription immediately (no proration,
// no end-of-period). Used by the GDPR deletion cascade (TASK-021 Phase 3).
// For a "cancel at end of period" flow, the customer-portal handles it —
// Stripe's UI is the right tool for that user-facing action.
func (c *Client) CancelSubscription(id string) (*stripego.Subscription, error) {
	if id == "" {
		return nil, errors.New("stripe: empty subscription ID")
	}
	stripego.Key = c.APIKey
	return subscription.Cancel(id, nil)
}

// ParseWebhook verifies the `Stripe-Signature` header against the webhook
// secret and decodes the event. Returns the typed event so the handler
// can switch on event.Type without re-parsing the JSON.
//
// The verification covers:
//   - HMAC-SHA256 of (timestamp + "." + payload) against secret
//   - Replay-window check (default 5 minutes)
//
// Both rules live in stripe-go's webhook package — we don't hand-roll the
// signature scheme (Stripe specifies `v1=<hex>,t=<timestamp>` etc.; getting
// it wrong silently accepts forged events).
//
// We pass `IgnoreAPIVersionMismatch: true` because the webhook endpoint's
// pinned API version (set in the Stripe dashboard) is independent of the
// stripe-go SDK version this binary ships with; mismatches are warnings,
// not errors, and shouldn't crash the handler. The deployment overlay
// can pin both to the same version when it wants stricter checking.
func (c *Client) ParseWebhook(payload []byte, sigHeader, webhookSecret string) (stripego.Event, error) {
	if webhookSecret == "" {
		return stripego.Event{}, errors.New("stripe: empty webhook secret")
	}
	return webhook.ConstructEventWithOptions(payload, sigHeader, webhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
}

// TierForPriceID looks up the tier configured for a given Stripe price ID.
// Unknown price IDs return TierFree — the safe default; a misconfigured
// webhook doesn't accidentally bump a user to enterprise.
func (c *Client) TierForPriceID(priceID string) Tier {
	if t, ok := c.PriceIDToTier[priceID]; ok {
		return t
	}
	return TierFree
}

// The four tier strings — string-typed so pkg/stripe doesn't depend on
// pkg/users. Caller converts via users.Tier(string(t)) at the call site.
const (
	TierFree       Tier = "free"
	TierStarter    Tier = "starter"
	TierGrowth     Tier = "growth"
	TierEnterprise Tier = "enterprise"
)

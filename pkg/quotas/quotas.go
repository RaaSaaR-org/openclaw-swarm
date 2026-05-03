// Package quotas is the canonical tier→limits map for the SaaS direction
// (PROP-002 + TASK-015). One source of truth, two consumers: the operator
// clamps `spec.resources` at reconcile time, and the signup flow checks
// per-user instance counts before provisioning. A future
// ValidatingAdmissionWebhook (Phase 2) is defense-in-depth and reuses the
// same map.
//
// Numbers here are PUBLIC defaults — what a fork of the swarm repo gets
// out of the box. Production deployments override per-tier numbers via a
// ConfigMap shipped from the deployment overlay (`swarm-cloud` /
// `swarm-emai`); see `Override` below.
package quotas

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Tier matches the enum values on `KaiInstanceSpec.Tier` and on the User
// row's `tier` column. Strings are durable contract — renames cost a
// migration, not a refactor.
type Tier string

const (
	TierFree       Tier = "free"
	TierStarter    Tier = "starter"
	TierGrowth     Tier = "growth"
	TierEnterprise Tier = "enterprise"
)

// Limits is the numerical envelope for one tier. Zero means "unbounded";
// callers must treat zero as "skip enforcement", not "deny everything", so
// the enterprise tier can opt out of any individual limit by leaving it 0.
//
// Memory and CPU are in Kubernetes resource units (Mi, m); we keep them as
// strings here so the operator can hand them straight to
// `resource.MustParse` without import dependencies bleeding into pkg/quotas.
type Limits struct {
	MaxInstancesPerUser int           // hard cap on KaiInstances per User; 0 = unbounded
	MemoryRequest       string        // e.g. "384Mi"; clamped down if spec exceeds
	MemoryLimit         string        // e.g. "768Mi"
	CPURequest          string        // e.g. "100m"
	CPULimit            string        // e.g. "500m"
	DailyTokens         int64         // OpenRouter token budget per workspace per UTC day; 0 = unbounded
	DailyMessages       int           // soft cap on user-sent chat messages per UTC day; 0 = unbounded
	MaxTelegramBots     int           // 0 = unbounded; free tier = 0 (disable Telegram entirely)
	IdleSuspendAfter    time.Duration // suspend instance after this much inactivity; 0 = never
}

// defaults map the public-repo tier shapes. PROP-002 + TASK-015 numbers:
//   - free: 1 instance, 384Mi memory (matches `cc1ffec` argon2 headroom),
//     100 messages/day on free OpenRouter models, no Telegram, idle-suspend
//     after 14 days.
//   - starter: 3 instances, 1Gi memory, paid Haiku/Flash class, 500k
//     tokens/day, Telegram allowed, no idle suspend.
//   - growth: 10 instances, 2Gi memory, 2M tokens/day, no idle suspend.
//   - enterprise: unbounded — overrides via ConfigMap in swarm-cloud.
//
// These ARE the public defaults. Deployments override via Override().
var defaults = map[Tier]Limits{
	TierFree: {
		MaxInstancesPerUser: 1,
		MemoryRequest:       "384Mi",
		MemoryLimit:         "768Mi",
		CPURequest:          "50m",
		CPULimit:            "300m",
		DailyTokens:         0, // tracked via message cap on free tier
		DailyMessages:       100,
		MaxTelegramBots:     0,
		IdleSuspendAfter:    14 * 24 * time.Hour,
	},
	TierStarter: {
		MaxInstancesPerUser: 3,
		MemoryRequest:       "1Gi",
		MemoryLimit:         "2Gi",
		CPURequest:          "100m",
		CPULimit:            "500m",
		DailyTokens:         500_000,
		DailyMessages:       0,
		MaxTelegramBots:     1,
		IdleSuspendAfter:    0,
	},
	TierGrowth: {
		MaxInstancesPerUser: 10,
		MemoryRequest:       "1Gi",
		MemoryLimit:         "2Gi",
		CPURequest:          "200m",
		CPULimit:            "1000m",
		DailyTokens:         2_000_000,
		DailyMessages:       0,
		MaxTelegramBots:     5,
		IdleSuspendAfter:    0,
	},
	TierEnterprise: {
		// Enterprise is "we'll size it for you" — every numerical field is 0
		// (unbounded). The deployment overlay's ConfigMap is the actual
		// source of enterprise limits per tenant.
	},
}

// For returns the Limits for a known tier. Empty/unknown tiers fall back to
// `free` because that's the safe default — a misconfigured tenant gets
// throttled, not handed unlimited resources.
func For(t Tier) Limits {
	if t == "" {
		return defaults[TierFree]
	}
	if l, ok := defaults[Tier(strings.ToLower(string(t)))]; ok {
		l := l // copy
		return l
	}
	return defaults[TierFree]
}

// ValidTier reports whether t is one of the four enum values. Mirrors
// pkg/users.ValidTier so consumers don't have to import both packages just
// for validation.
func ValidTier(t Tier) bool {
	switch Tier(strings.ToLower(string(t))) {
	case TierFree, TierStarter, TierGrowth, TierEnterprise:
		return true
	}
	return false
}

// Override replaces the in-memory defaults for one tier. Used at deployment
// startup when the overlay ConfigMap has different per-tier numbers than the
// public defaults. Not safe for concurrent use — call once during init.
func Override(t Tier, l Limits) error {
	if !ValidTier(t) {
		return fmt.Errorf("%w: %q", ErrInvalidTier, t)
	}
	defaults[Tier(strings.ToLower(string(t)))] = l
	return nil
}

// MaxInstancesReached returns whether userInstanceCount is at or above the
// tier's `MaxInstancesPerUser`. 0 (unbounded) always returns false.
func MaxInstancesReached(t Tier, userInstanceCount int) bool {
	l := For(t)
	if l.MaxInstancesPerUser == 0 {
		return false
	}
	return userInstanceCount >= l.MaxInstancesPerUser
}

// Sentinel errors so callers branch on the failure mode without parsing
// strings.
var (
	ErrInvalidTier = errors.New("quotas: invalid tier")
	ErrOverLimit   = errors.New("quotas: tier limit exceeded")
)

// Package idle is the once-a-day idle-suspend pass that flips
// `spec.suspended=true` on free-tier KaiInstances whose owning User hasn't
// logged in for longer than `pkg/quotas.For(tier).IdleSuspendAfter`
// (TASK-015 Phase 3). Paid tiers keep IdleSuspendAfter=0 so they are never
// touched.
//
// Architecture mirrors `cmd/gdpr-purge/internal/gdpr.Runner`: narrow
// interface seams (Lister + Patcher + UserLookup) so tests don't need a
// real cluster or a real Postgres pool. The cron entrypoint at
// `cmd/idle-suspend/main.go` owns the dynamic K8s client + the
// `pkg/userspg.PoolStore` wiring and hands the Runner the satisfied
// interfaces.
//
// Why per-User activity instead of per-KaiInstance: extending KaiInstance
// status with a `lastActivityAt` field would require something to *write*
// it (chat-bridge? operator? per-message reconciliation?) — significant
// new infrastructure. `User.LastLoginAt` already updates on every
// workspace-dashboard login (TASK-014 Phase 3) and is a coarser-but-honest
// signal: "this human hasn't touched our product in 14 days, free their
// resources". When they come back, the dashboard's resume path can flip
// suspended=false (Phase 3.B follow-up).
package idle

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/emai-ai/swarm/pkg/quotas"
	"github.com/emai-ai/swarm/pkg/users"
)

// Lister is the slice of `dynamic.Interface` the Runner needs to fetch
// candidate KaiInstances. The full dynamic client interface is sprawling;
// pinning the seam to a single method keeps the test fake trivial.
type Lister interface {
	ListSaaSInstances(ctx context.Context) ([]*unstructured.Unstructured, error)
}

// Patcher applies `spec.suspended=true` to a single KaiInstance by name.
// Idempotent — re-applying to an already-suspended instance is a no-op
// for the operator (its reconciler is level-triggered).
type Patcher interface {
	SetSuspended(ctx context.Context, name string) error
}

// UserLookup is the slice of `pkg/users.Store` the Runner needs.
// `users.ErrNotFound` from GetByID means the user was deleted out from
// under their KaiInstance — the runner treats that as "definitely idle"
// and suspends. (The GDPR cascade in TASK-021 should already have
// deleted the KaiInstances; this is a belt-and-braces.)
type UserLookup interface {
	GetByID(ctx context.Context, id string) (*users.User, error)
}

// Runner executes one suspend pass. The zero value isn't useful — callers
// must set Lister, Patcher, Users; Now defaults to `time.Now`.
type Runner struct {
	Lister  Lister
	Patcher Patcher
	Users   UserLookup
	// Now overrides `time.Now` for tests. Defaults to `time.Now` when nil.
	Now func() time.Time
}

// Result is the pass outcome. Suspended is the count of instances flipped
// in this run (for metrics + the cron's stdout summary). Inspected is the
// total candidate count — Suspended / Inspected ratio is a useful signal
// in dashboards.
type Result struct {
	Inspected int
	Suspended int
}

// Run does one idle-suspend pass. Errors on individual instances are
// logged and counted but do not abort the run — one bad workspace must
// not block the rest. A fatal error from the Lister itself does abort
// (we have nothing to iterate on).
func (r *Runner) Run(ctx context.Context) (Result, error) {
	if r.Lister == nil || r.Patcher == nil || r.Users == nil {
		return Result{}, errors.New("idle: Runner.Lister, Patcher, Users are all required")
	}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	items, err := r.Lister.ListSaaSInstances(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("list saas instances: %w", err)
	}
	res := Result{Inspected: len(items)}
	for _, item := range items {
		if r.shouldSuspend(ctx, item, now()) {
			name := item.GetName()
			if err := r.Patcher.SetSuspended(ctx, name); err != nil {
				log.Printf("idle: patch suspend %s: %v", name, err)
				continue
			}
			res.Suspended++
			log.Printf("idle: suspended %s (idle past tier limit)", name)
		}
	}
	return res, nil
}

// shouldSuspend decides whether a single KaiInstance qualifies. Returns
// false for: already-suspended instances, paid-tier instances (0 idle
// window means never), missing tier label (legacy / mis-stamped), and
// fresh-login users.
func (r *Runner) shouldSuspend(ctx context.Context, item *unstructured.Unstructured, now time.Time) bool {
	// Skip already-suspended instances — patching them is a no-op but the
	// log noise is misleading.
	if suspended, _, _ := unstructured.NestedBool(item.Object, "spec", "suspended"); suspended {
		return false
	}
	labels := item.GetLabels()
	tierLabel := labels["swarm.io/tier"]
	if tierLabel == "" {
		// Legacy or unstamped — leave alone. ClampResources on the operator
		// also short-circuits in this case for the same reason.
		return false
	}
	limits := quotas.For(quotas.Tier(tierLabel))
	if limits.IdleSuspendAfter == 0 {
		return false // paid tiers never auto-suspend
	}
	userID := labels["swarm.io/user-id"]
	if userID == "" {
		return false
	}
	u, err := r.Users.GetByID(ctx, userID)
	if err != nil {
		// User deleted — the GDPR cascade should already have removed
		// this KaiInstance; treat as idle so a stale workspace gets
		// suspended rather than left running.
		if errors.Is(err, users.ErrNotFound) {
			return true
		}
		log.Printf("idle: user lookup uid=%s: %v", userID, err)
		return false
	}
	if u.LastLoginAt == nil {
		// Never logged in. Use CreatedAt as the reference — same idle window.
		return now.Sub(u.CreatedAt) > limits.IdleSuspendAfter
	}
	return now.Sub(*u.LastLoginAt) > limits.IdleSuspendAfter
}

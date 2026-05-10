// Package gdpr is the once-a-day GDPR purge pass that hard-deletes every
// soft-deleted user older than the grace window (TASK-021 Phase 2). The
// `Store.PurgeDeletedBefore` primitive (TASK-021 Phase 0, in `pkg/users`)
// does the actual work; this package is the small testable shell around it
// that the cron entrypoint at `web/onboarding/cmd/gdpr-purge/main.go` wires
// real clients into.
//
// Architecture mirrors `operator/internal/usage` from TASK-019 Phase 3: a
// Runner with a narrow interface seam (Purger here, UsageReader there) so
// tests can drive deterministic counts + errors without standing up a real
// Postgres pool. The cron entrypoint owns the pgxpool + the
// `pkg/userspg.PoolStore` wiring and hands the Runner the already-satisfied
// interface.
package gdpr

import (
	"context"
	"fmt"
	"time"

	"github.com/emai-ai/swarm/pkg/users"
)

// Purger is the slice of `users.Store` the Runner needs. The full Store
// interface has 12+ methods (Create/Get/Update/Mark…); a daily cron only
// needs the one. Keeping the seam narrow makes the test fake trivial and
// keeps any future Store growth invisible to this package.
type Purger interface {
	PurgeDeletedBefore(ctx context.Context, before time.Time) (int, error)
}

// Runner is the once-a-day pass. The zero value isn't useful — callers must
// set Store; GracePeriod and Now have sensible defaults (the public 30-day
// constant from `pkg/users` and `time.Now`, respectively) so production
// wiring is just `&Runner{Store: poolStore}`.
type Runner struct {
	// Store is the Purger to call. Required.
	Store Purger
	// GracePeriod is how long after `deleted_at` a soft-deleted user
	// survives. Defaults to `users.GracePeriod` (30 days) when zero. Tests
	// shrink this to drive deterministic cutoffs.
	GracePeriod time.Duration
	// Now overrides `time.Now` for tests. Defaults to `time.Now` when nil.
	Now func() time.Time
}

// Result is the pass outcome. Count is the number of rows hard-deleted; Cutoff
// is the `before` timestamp the Runner passed to `PurgeDeletedBefore` (useful
// in logs so ops can correlate with the soft-delete timestamps in audit).
type Result struct {
	Cutoff time.Time
	Count  int
}

// Run does one purge pass. The cutoff is `now - gracePeriod`. Errors from
// the Store are returned verbatim — the cron entrypoint logs + non-zero exits
// so the CronJob's status surfaces the failure.
func (r *Runner) Run(ctx context.Context) (Result, error) {
	if r.Store == nil {
		return Result{}, fmt.Errorf("gdpr: Runner.Store is nil")
	}
	grace := r.GracePeriod
	if grace == 0 {
		grace = users.GracePeriod
	}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	cutoff := now().UTC().Add(-grace)
	n, err := r.Store.PurgeDeletedBefore(ctx, cutoff)
	if err != nil {
		return Result{Cutoff: cutoff}, fmt.Errorf("purge deleted before %s: %w", cutoff.Format(time.RFC3339), err)
	}
	return Result{Cutoff: cutoff, Count: n}, nil
}

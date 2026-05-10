// gdpr-purge is the once-a-day workload that hard-deletes every soft-deleted
// user older than `pkg/users.GracePeriod` (TASK-021 Phase 2). The
// `pkg/users.Store.PurgeDeletedBefore` primitive (Phase 0) does the actual
// work; this binary is the cron entrypoint that opens the production
// Postgres pool, hands it to the testable Runner in
// `internal/gdpr`, logs the count, and exits.
//
// Run as a Kubernetes CronJob (see kubernetes/onboarding/gdpr-purge-cronjob.yaml).
// Schedule is daily at 03:00 UTC — well clear of the operator's
// usage-monitor cron at 00:30 UTC so the two cron pods don't compete for
// cluster resources.
//
// Env vars:
//
//	SWARM_USERS_DSN    Postgres URL — same DSN the onboarding server uses;
//	                 the cron + the server share one Postgres instance.
//	SWARM_GDPR_GRACE   Optional override for the grace window (Go duration
//	                 string, e.g. "168h"). Defaults to `users.GracePeriod`
//	                 (30 days). Lets ops shrink the window in staging
//	                 without redeploying.
//	SWARM_GDPR_TIMEOUT Optional wall-clock ceiling (Go duration). Defaults
//	                 to 5m — a daily purge of even a large tenant should
//	                 be sub-second; 5 minutes is a generous failsafe.
//
// Deploy note: this cron does NOT run `userspg.Migrate(...)`. The schema is
// owned by the onboarding server's startup path; the cron expects the
// `users` + `deletion_audit` tables to already exist. If a brand-new
// deployment ships the cron before the server has run once, the daily fire
// will hit a table-doesn't-exist error and exit non-zero — the CronJob's
// failed-job history makes that visible. The fix is the same as for any
// fresh database: run the server (or the migration tool) before the cron.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emai-ai/swarm-onboarding/cmd/gdpr-purge/internal/gdpr"
	"github.com/emai-ai/swarm/pkg/userspg"
)

func main() {
	var (
		dsn     string
		grace   time.Duration
		timeout time.Duration
	)
	flag.StringVar(&dsn, "dsn", os.Getenv("SWARM_USERS_DSN"), "Postgres DSN (SWARM_USERS_DSN)")
	flag.DurationVar(&grace, "grace", parseDurationDefault(os.Getenv("SWARM_GDPR_GRACE"), 0), "grace window override (default: pkg/users.GracePeriod = 30 days)")
	flag.DurationVar(&timeout, "timeout", parseDurationDefault(os.Getenv("SWARM_GDPR_TIMEOUT"), 5*time.Minute), "wall-clock ceiling for the pass")
	flag.Parse()

	if dsn == "" {
		log.Fatal("SWARM_USERS_DSN must be set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("gdpr-purge: pgxpool.New: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("gdpr-purge: ping postgres: %v", err)
	}

	store, err := userspg.New(pool)
	if err != nil {
		log.Fatalf("gdpr-purge: userspg.New: %v", err)
	}

	r := &gdpr.Runner{
		Store:       store,
		GracePeriod: grace, // zero -> Runner falls back to users.GracePeriod
	}
	res, err := r.Run(ctx)
	if err != nil {
		log.Fatalf("gdpr-purge: pass aborted (cutoff=%s): %v", res.Cutoff.Format(time.RFC3339), err)
	}

	log.Printf("gdpr-purge pass complete: cutoff=%s purged=%d", res.Cutoff.Format(time.RFC3339), res.Count)
}

// parseDurationDefault parses a duration string and falls back to `def` for
// empty input or parse failures. Used for env-var overrides where a
// misconfigured value should not fail startup — log + ignore is friendlier.
func parseDurationDefault(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gdpr-purge: ignoring malformed duration %q: %v\n", s, err)
		return def
	}
	return d
}

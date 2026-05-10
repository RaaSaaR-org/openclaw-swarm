package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emai-ai/swarm/pkg/users"
	"github.com/emai-ai/swarm/pkg/userspg"
)

// newPoolStore opens a Postgres pool from `SWARM_USERS_DSN`, runs the
// userspg schema migration (idempotent — onboarding owns the canonical
// migration call but running again is safe), and returns the resulting
// PoolStore as the `users.Store` the workspace dashboard reads from.
//
// Architectural note: same as the onboarding-side wiring — Postgres is
// opt-in via env var. When unset the binary uses MemoryStore and never
// dials pgx. When set, both onboarding + workspace must point at the same
// DSN so a signup in one is visible to the other.
func newPoolStore(dsn string) (users.Store, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if err := userspg.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate users schema: %w", err)
	}
	store, err := userspg.New(pool)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("userspg.New: %w", err)
	}
	return store, nil
}

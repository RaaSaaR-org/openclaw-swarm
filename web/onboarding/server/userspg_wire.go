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
// userspg schema migration (idempotent), and returns the resulting
// PoolStore as the `users.Store` the rest of the binary uses.
//
// Architectural note: this file makes the onboarding binary a pgx
// consumer. The original PROP-001 split intended `pkg/userspg` to ship
// only via the swarm-cloud overlay so pure-memory deployments stayed
// pgx-free. We pull pgx into the public binary to make development
// against k3d straightforward — onboarding + workspace can share the
// same Postgres without an overlay-side fork. Deployments that don't
// want Postgres simply leave `SWARM_USERS_DSN` unset; the runtime cost
// of the unused pgx code paths is the only price.
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

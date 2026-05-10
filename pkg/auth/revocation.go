package auth

import (
	"context"
	"sync"
	"time"
)

// Revoker decides whether a JWT identifier (`jti`) is still valid for a given
// slug. Implementations persist the revocation set somewhere durable (or not,
// in the case of the in-memory variant used for tests / dev mode).
//
// Revoke marks `jti` as no-longer-valid until `exp`. Implementations may prune
// entries past their `exp` opportunistically — the caller does not need to
// schedule cleanup.
//
// IsRevoked returns true when the `jti` is in the revocation set and has not
// yet expired. An empty `jti` is never revoked (legacy tokens issued before
// this field existed remain valid until their natural expiry).
type Revoker interface {
	Revoke(ctx context.Context, slug, jti string, exp time.Time) error
	IsRevoked(ctx context.Context, slug, jti string) (bool, error)
}

// MemoryRevoker is the in-memory implementation used in tests and in the
// `SWARM_INSECURE_DEV_AUTH` path where no K8s Secret exists. Safe for concurrent
// use; entries are pruned on every read.
type MemoryRevoker struct {
	mu      sync.Mutex
	entries map[string]map[string]int64 // slug -> jti -> exp (unix seconds)
	now     func() time.Time
}

// NewMemoryRevoker returns a ready-to-use in-memory Revoker.
func NewMemoryRevoker() *MemoryRevoker {
	return &MemoryRevoker{
		entries: map[string]map[string]int64{},
		now:     time.Now,
	}
}

func (m *MemoryRevoker) Revoke(_ context.Context, slug, jti string, exp time.Time) error {
	if jti == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, ok := m.entries[slug]
	if !ok {
		bucket = map[string]int64{}
		m.entries[slug] = bucket
	}
	bucket[jti] = exp.UTC().Unix()
	return nil
}

func (m *MemoryRevoker) IsRevoked(_ context.Context, slug, jti string) (bool, error) {
	if jti == "" {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, ok := m.entries[slug]
	if !ok {
		return false, nil
	}
	nowSec := m.now().UTC().Unix()
	exp, present := bucket[jti]
	if present && exp <= nowSec {
		delete(bucket, jti)
		return false, nil
	}
	for j, e := range bucket {
		if e <= nowSec {
			delete(bucket, j)
		}
	}
	return present && exp > nowSec, nil
}

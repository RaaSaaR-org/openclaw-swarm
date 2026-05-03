// Package authk8s implements auth.Revoker on top of a Kubernetes Secret. The
// per-tenant chat-bridge Secret carries a `revoked-jtis` JSON array; entries
// TTL out at the original session expiry and are pruned on every read. The
// implementation is split out of pkg/auth so the JWT/argon2id helpers stay
// dep-light — only consumers that actually run in K8s pull client-go.
package authk8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/emai-ai/swarm/pkg/auth"
)

// SecretKey is the chat-bridge Secret data key holding the JSON revocation list.
const SecretKey = "revoked-jtis"

// MaxEntriesPerSlug bounds the per-tenant list. With ~50 bytes/entry, 1000
// entries fit well under the K8s 1 MiB Secret limit. When the cap is hit,
// the oldest-by-exp entries are dropped so newer revocations always land.
const MaxEntriesPerSlug = 1000

// SecretRevoker satisfies auth.Revoker by storing the revocation set in the
// per-tenant `kai-<slug>-chat-bridge` Secret. Safe for concurrent use; reads
// always prune expired entries before answering.
type SecretRevoker struct {
	Client    kubernetes.Interface
	Namespace string
	Now       func() time.Time // injected for tests; defaults to time.Now

	mu sync.Mutex // serializes read-modify-write per-process; cross-process conflicts handled via 409 retry
}

// Compile-time assertion the API contract is satisfied.
var _ auth.Revoker = (*SecretRevoker)(nil)

type entry struct {
	Jti string `json:"jti"`
	Exp int64  `json:"exp"`
}

func (r *SecretRevoker) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Revoke records `jti` as no-longer-valid until `exp`. If the chat-bridge
// Secret is missing, returns the underlying NotFound error so the caller can
// distinguish provisioning errors from transient API failures.
func (r *SecretRevoker) Revoke(ctx context.Context, slug, jti string, exp time.Time) error {
	if jti == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.update(ctx, slug, func(list []entry) []entry {
		nowSec := r.now().UTC().Unix()
		expSec := exp.UTC().Unix()
		if expSec <= nowSec {
			return list // nothing to record — it's already past
		}
		// Drop expired entries first so the cap accounting is honest.
		list = pruneExpired(list, nowSec)
		// Replace existing entry with the same jti (idempotent).
		for i := range list {
			if list[i].Jti == jti {
				list[i].Exp = expSec
				return list
			}
		}
		list = append(list, entry{Jti: jti, Exp: expSec})
		if len(list) > MaxEntriesPerSlug {
			sort.Slice(list, func(i, j int) bool { return list[i].Exp < list[j].Exp })
			list = list[len(list)-MaxEntriesPerSlug:]
		}
		return list
	})
}

// IsRevoked returns true when `jti` is in the per-slug revocation set and has
// not yet expired. Empty `jti` is never revoked (covers legacy tokens issued
// before this field existed).
func (r *SecretRevoker) IsRevoked(ctx context.Context, slug, jti string) (bool, error) {
	if jti == "" {
		return false, nil
	}
	list, err := r.read(ctx, slug)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	nowSec := r.now().UTC().Unix()
	for _, e := range list {
		if e.Jti == jti && e.Exp > nowSec {
			return true, nil
		}
	}
	return false, nil
}

func (r *SecretRevoker) read(ctx context.Context, slug string) ([]entry, error) {
	sec, err := r.Client.CoreV1().Secrets(r.Namespace).Get(ctx, "kai-"+slug+"-chat-bridge", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	raw := sec.Data[SecretKey]
	if len(raw) == 0 {
		return nil, nil
	}
	var list []entry
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("parse %s: %w", SecretKey, err)
	}
	return list, nil
}

// update fetches the chat-bridge Secret, applies fn, and writes back. Retries
// once on 409 conflict — the typical case is two concurrent revokes from
// different replicas.
func (r *SecretRevoker) update(ctx context.Context, slug string, fn func([]entry) []entry) error {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		sec, err := r.Client.CoreV1().Secrets(r.Namespace).Get(ctx, "kai-"+slug+"-chat-bridge", metav1.GetOptions{})
		if err != nil {
			return err
		}
		var list []entry
		if raw := sec.Data[SecretKey]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &list); err != nil {
				return fmt.Errorf("parse %s: %w", SecretKey, err)
			}
		}
		updated := fn(list)
		encoded, err := json.Marshal(updated)
		if err != nil {
			return err
		}
		if sec.Data == nil {
			sec.Data = map[string][]byte{}
		}
		sec.Data[SecretKey] = encoded
		_, err = r.Client.CoreV1().Secrets(r.Namespace).Update(ctx, sec, metav1.UpdateOptions{})
		if err == nil {
			return nil
		}
		if !apierrors.IsConflict(err) {
			return err
		}
		lastErr = err
	}
	return fmt.Errorf("revocation update conflict after %d attempts: %w", maxAttempts, lastErr)
}

func pruneExpired(list []entry, nowSec int64) []entry {
	out := list[:0]
	for _, e := range list {
		if e.Exp > nowSec {
			out = append(out, e)
		}
	}
	return out
}

// ErrUnsupported is returned by adapters that wrap a SecretRevoker but cannot
// fulfil the request (e.g. namespace not configured). Exported for callers
// that want to distinguish it from API errors.
var ErrUnsupported = errors.New("authk8s: revoker not configured")

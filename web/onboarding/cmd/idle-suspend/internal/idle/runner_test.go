package idle

import (
	"context"
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/emai-ai/swarm/pkg/users"
)

// fakeLister returns a fixed list. Tests construct it directly.
type fakeLister struct {
	items []*unstructured.Unstructured
	err   error
}

func (f *fakeLister) ListSaaSInstances(_ context.Context) ([]*unstructured.Unstructured, error) {
	return f.items, f.err
}

// fakePatcher records every name it was asked to suspend so tests can
// assert who got flipped (and who didn't).
type fakePatcher struct {
	suspended []string
	err       error
}

func (f *fakePatcher) SetSuspended(_ context.Context, name string) error {
	if f.err != nil {
		return f.err
	}
	f.suspended = append(f.suspended, name)
	return nil
}

// fakeUsers is a tiny map-backed Store that satisfies UserLookup.
type fakeUsers struct {
	byID map[string]*users.User
	err  error
}

func (f *fakeUsers) GetByID(_ context.Context, id string) (*users.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	u, ok := f.byID[id]
	if !ok {
		return nil, users.ErrNotFound
	}
	return u, nil
}

// kaiObj builds an unstructured KaiInstance with the labels the Runner
// reads. Mirrors the workspace test helper.
func kaiObj(name, tier, userID string, suspended bool) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(schema.GroupVersionKind{Group: "swarm.emai.io", Version: "v1alpha2", Kind: "KaiInstance"})
	o.SetName(name)
	o.SetLabels(map[string]string{
		"swarm.io/managed": "saas",
		"swarm.io/tier":    tier,
		"swarm.io/user-id": userID,
	})
	if suspended {
		_ = unstructured.SetNestedField(o.Object, true, "spec", "suspended")
	}
	o.SetCreationTimestamp(metav1.Time{Time: time.Now()})
	return o
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestRunner_Required(t *testing.T) {
	t.Parallel()
	r := &Runner{}
	if _, err := r.Run(context.Background()); err == nil {
		t.Fatal("expected error when Lister/Patcher/Users are nil")
	}
}

func TestRunner_FreeTierIdleUserGetsSuspended(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	// User last logged in 20 days ago — past the 14-day free-tier window.
	idle := now.Add(-20 * 24 * time.Hour)
	users := &fakeUsers{byID: map[string]*users.User{
		"u_alice": {ID: "u_alice", Tier: "free", LastLoginAt: ptrTime(idle)},
	}}
	patcher := &fakePatcher{}
	r := &Runner{
		Lister:  &fakeLister{items: []*unstructured.Unstructured{kaiObj("kai-alice", "free", "u_alice", false)}},
		Patcher: patcher,
		Users:   users,
		Now:     func() time.Time { return now },
	}
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Inspected != 1 || res.Suspended != 1 {
		t.Errorf("Result = %+v, want Inspected=1 Suspended=1", res)
	}
	if len(patcher.suspended) != 1 || patcher.suspended[0] != "kai-alice" {
		t.Errorf("patched names = %v, want [kai-alice]", patcher.suspended)
	}
}

func TestRunner_FreeTierActiveUserPreserved(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	// Logged in 3 days ago — well within the window.
	fresh := now.Add(-3 * 24 * time.Hour)
	users := &fakeUsers{byID: map[string]*users.User{
		"u_bob": {ID: "u_bob", Tier: "free", LastLoginAt: ptrTime(fresh)},
	}}
	patcher := &fakePatcher{}
	r := &Runner{
		Lister:  &fakeLister{items: []*unstructured.Unstructured{kaiObj("kai-bob", "free", "u_bob", false)}},
		Patcher: patcher,
		Users:   users,
		Now:     func() time.Time { return now },
	}
	res, _ := r.Run(context.Background())
	if res.Suspended != 0 {
		t.Errorf("expected 0 suspended, got %d (patched=%v)", res.Suspended, patcher.suspended)
	}
}

func TestRunner_PaidTierNeverSuspended(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	// Starter user idle for 60 days — must NOT be suspended (Starter
	// IdleSuspendAfter = 0 in pkg/quotas).
	idle := now.Add(-60 * 24 * time.Hour)
	usersFake := &fakeUsers{byID: map[string]*users.User{
		"u_paid": {ID: "u_paid", Tier: "starter", LastLoginAt: ptrTime(idle)},
	}}
	patcher := &fakePatcher{}
	r := &Runner{
		Lister:  &fakeLister{items: []*unstructured.Unstructured{kaiObj("kai-paid", "starter", "u_paid", false)}},
		Patcher: patcher,
		Users:   usersFake,
		Now:     func() time.Time { return now },
	}
	res, _ := r.Run(context.Background())
	if res.Suspended != 0 {
		t.Errorf("paid tier must never auto-suspend, got Suspended=%d", res.Suspended)
	}
}

func TestRunner_AlreadySuspendedSkipped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	idle := now.Add(-30 * 24 * time.Hour)
	usersFake := &fakeUsers{byID: map[string]*users.User{
		"u_dup": {ID: "u_dup", Tier: "free", LastLoginAt: ptrTime(idle)},
	}}
	patcher := &fakePatcher{}
	r := &Runner{
		Lister:  &fakeLister{items: []*unstructured.Unstructured{kaiObj("kai-dup", "free", "u_dup", true)}}, // already suspended
		Patcher: patcher,
		Users:   usersFake,
		Now:     func() time.Time { return now },
	}
	res, _ := r.Run(context.Background())
	if res.Suspended != 0 {
		t.Errorf("already-suspended must not be re-flipped, got %d", res.Suspended)
	}
	if len(patcher.suspended) != 0 {
		t.Errorf("expected no patches, got %v", patcher.suspended)
	}
}

func TestRunner_DeletedUserSuspendsOrphanedInstance(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	// User missing from the store — fake returns ErrNotFound. The runner
	// treats that as "definitely idle, suspend it" since the GDPR cascade
	// should already have cleaned this up but didn't.
	usersFake := &fakeUsers{byID: map[string]*users.User{}}
	patcher := &fakePatcher{}
	r := &Runner{
		Lister:  &fakeLister{items: []*unstructured.Unstructured{kaiObj("kai-orphan", "free", "u_gone", false)}},
		Patcher: patcher,
		Users:   usersFake,
		Now:     func() time.Time { return now },
	}
	res, _ := r.Run(context.Background())
	if res.Suspended != 1 {
		t.Errorf("orphaned instance should be suspended, got %d", res.Suspended)
	}
}

func TestRunner_NeverLoggedInUsesCreatedAt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	// Account created 30 days ago, never logged in. Past the 14-day window.
	usersFake := &fakeUsers{byID: map[string]*users.User{
		"u_lurker": {ID: "u_lurker", Tier: "free", CreatedAt: now.Add(-30 * 24 * time.Hour), LastLoginAt: nil},
	}}
	patcher := &fakePatcher{}
	r := &Runner{
		Lister:  &fakeLister{items: []*unstructured.Unstructured{kaiObj("kai-lurker", "free", "u_lurker", false)}},
		Patcher: patcher,
		Users:   usersFake,
		Now:     func() time.Time { return now },
	}
	res, _ := r.Run(context.Background())
	if res.Suspended != 1 {
		t.Errorf("expected lurker suspended via CreatedAt fallback, got %d", res.Suspended)
	}
}

func TestRunner_LegacyTenantWithoutTierLabelSkipped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	// No tier label → legacy/mis-stamped. Must skip even if the user is
	// idle — clamp + suspend logic short-circuit on missing labels.
	o := kaiObj("kai-legacy", "free", "u_legacy", false)
	labels := o.GetLabels()
	delete(labels, "swarm.io/tier")
	o.SetLabels(labels)
	usersFake := &fakeUsers{byID: map[string]*users.User{
		"u_legacy": {ID: "u_legacy", Tier: "free", LastLoginAt: ptrTime(now.Add(-60 * 24 * time.Hour))},
	}}
	patcher := &fakePatcher{}
	r := &Runner{
		Lister:  &fakeLister{items: []*unstructured.Unstructured{o}},
		Patcher: patcher,
		Users:   usersFake,
		Now:     func() time.Time { return now },
	}
	res, _ := r.Run(context.Background())
	if res.Suspended != 0 {
		t.Errorf("legacy tenant must be skipped, got %d suspended", res.Suspended)
	}
}

func TestRunner_PerInstanceErrorContinues(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	idle := now.Add(-30 * 24 * time.Hour)
	usersFake := &fakeUsers{byID: map[string]*users.User{
		"u_a": {ID: "u_a", Tier: "free", LastLoginAt: ptrTime(idle)},
		"u_b": {ID: "u_b", Tier: "free", LastLoginAt: ptrTime(idle)},
	}}
	// Patcher fails on every call — Run should not abort, just log + skip.
	patcher := &fakePatcher{err: errors.New("simulated API timeout")}
	r := &Runner{
		Lister: &fakeLister{items: []*unstructured.Unstructured{
			kaiObj("kai-a", "free", "u_a", false),
			kaiObj("kai-b", "free", "u_b", false),
		}},
		Patcher: patcher,
		Users:   usersFake,
		Now:     func() time.Time { return now },
	}
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run should not propagate per-instance errors, got %v", err)
	}
	if res.Inspected != 2 || res.Suspended != 0 {
		t.Errorf("Result = %+v, want Inspected=2 Suspended=0 (both patches failed)", res)
	}
}

func TestRunner_ListerErrorAborts(t *testing.T) {
	t.Parallel()
	r := &Runner{
		Lister:  &fakeLister{err: errors.New("API server down")},
		Patcher: &fakePatcher{},
		Users:   &fakeUsers{byID: map[string]*users.User{}},
	}
	if _, err := r.Run(context.Background()); err == nil {
		t.Error("expected lister error to abort the run")
	}
}

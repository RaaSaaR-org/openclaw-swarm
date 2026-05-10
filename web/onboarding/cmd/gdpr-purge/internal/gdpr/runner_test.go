package gdpr

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/emai-ai/swarm/pkg/users"
)

// fakePurger is a deterministic Purger for tests. Records the cutoff it was
// called with so the test can assert the Runner subtracted the grace window
// from `now`.
type fakePurger struct {
	count     int
	err       error
	called    bool
	gotCutoff time.Time
}

func (f *fakePurger) PurgeDeletedBefore(_ context.Context, before time.Time) (int, error) {
	f.called = true
	f.gotCutoff = before
	return f.count, f.err
}

func TestRunner_NoStore(t *testing.T) {
	r := &Runner{}
	if _, err := r.Run(context.Background()); err == nil {
		t.Fatal("Run with nil Store should error")
	}
}

func TestRunner_HappyPath(t *testing.T) {
	now := time.Date(2026, 5, 10, 3, 0, 0, 0, time.UTC)
	fp := &fakePurger{count: 7}
	r := &Runner{
		Store: fp,
		Now:   func() time.Time { return now },
	}
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !fp.called {
		t.Fatal("fake Purger was not called")
	}
	if res.Count != 7 {
		t.Errorf("Count = %d, want 7", res.Count)
	}
	wantCutoff := now.Add(-users.GracePeriod).UTC()
	if !res.Cutoff.Equal(wantCutoff) {
		t.Errorf("Cutoff = %s, want %s", res.Cutoff, wantCutoff)
	}
	if !fp.gotCutoff.Equal(wantCutoff) {
		t.Errorf("Purger received cutoff = %s, want %s", fp.gotCutoff, wantCutoff)
	}
}

func TestRunner_CustomGracePeriod(t *testing.T) {
	now := time.Date(2026, 5, 10, 3, 0, 0, 0, time.UTC)
	fp := &fakePurger{count: 0}
	r := &Runner{
		Store:       fp,
		GracePeriod: 7 * 24 * time.Hour,
		Now:         func() time.Time { return now },
	}
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantCutoff := now.Add(-7 * 24 * time.Hour).UTC()
	if !fp.gotCutoff.Equal(wantCutoff) {
		t.Errorf("Purger cutoff = %s, want %s", fp.gotCutoff, wantCutoff)
	}
}

func TestRunner_StoreError(t *testing.T) {
	want := errors.New("connection refused")
	fp := &fakePurger{err: want}
	r := &Runner{
		Store: fp,
		Now:   func() time.Time { return time.Date(2026, 5, 10, 3, 0, 0, 0, time.UTC) },
	}
	res, err := r.Run(context.Background())
	if err == nil {
		t.Fatal("Run should propagate Store error")
	}
	if !errors.Is(err, want) {
		t.Errorf("Run err = %v, want wrapping %v", err, want)
	}
	// Cutoff is still populated on error so the caller can log it.
	if res.Cutoff.IsZero() {
		t.Error("Cutoff should be set even on Store error")
	}
}

func TestRunner_ZeroResultIsValid(t *testing.T) {
	// A pass that purges zero rows is the common steady-state result — must
	// not error.
	fp := &fakePurger{count: 0}
	r := &Runner{
		Store: fp,
		Now:   func() time.Time { return time.Date(2026, 5, 10, 3, 0, 0, 0, time.UTC) },
	}
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Count != 0 {
		t.Errorf("Count = %d, want 0", res.Count)
	}
}

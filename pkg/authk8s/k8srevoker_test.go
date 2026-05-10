package authk8s

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const ns = "swarm-system"

func newRevoker(t *testing.T, slug string, seed []byte, now time.Time) *SecretRevoker {
	t.Helper()
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kai-" + slug + "-chat-bridge", Namespace: ns},
		Data:       map[string][]byte{"jwt-secret": []byte("not-the-revocation-list")},
	}
	if seed != nil {
		sec.Data[SecretKey] = seed
	}
	c := fake.NewSimpleClientset(sec)
	return &SecretRevoker{
		Client:    c,
		Namespace: ns,
		Now:       func() time.Time { return now },
	}
}

func TestSecretRevokerRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	r := newRevoker(t, "acme", nil, now)
	ctx := context.Background()

	if got, err := r.IsRevoked(ctx, "acme", "abc"); err != nil || got {
		t.Fatalf("fresh jti must not be revoked, got %v err=%v", got, err)
	}
	if err := r.Revoke(ctx, "acme", "abc", now.Add(time.Hour)); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got, err := r.IsRevoked(ctx, "acme", "abc"); err != nil || !got {
		t.Fatalf("expected revoked, got %v err=%v", got, err)
	}
	// Verify the chat-bridge Secret's other keys are untouched.
	sec, _ := r.Client.CoreV1().Secrets(ns).Get(ctx, "kai-acme-chat-bridge", metav1.GetOptions{})
	if string(sec.Data["jwt-secret"]) != "not-the-revocation-list" {
		t.Errorf("jwt-secret must not be touched by revocation update, got %q", sec.Data["jwt-secret"])
	}
}

func TestSecretRevokerEmptyJti(t *testing.T) {
	t.Parallel()
	r := newRevoker(t, "acme", nil, time.Unix(1_700_000_000, 0))
	ctx := context.Background()
	if err := r.Revoke(ctx, "acme", "", time.Unix(1_700_000_000+3600, 0)); err != nil {
		t.Fatalf("revoke empty jti must be no-op: %v", err)
	}
	if got, _ := r.IsRevoked(ctx, "acme", ""); got {
		t.Fatal("empty jti must never be revoked")
	}
}

func TestSecretRevokerPrunesExpiredOnWrite(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	stale, _ := json.Marshal([]entry{
		{Jti: "old1", Exp: now.Add(-time.Hour).Unix()},
		{Jti: "old2", Exp: now.Add(-time.Minute).Unix()},
	})
	r := newRevoker(t, "acme", stale, now)
	ctx := context.Background()

	if err := r.Revoke(ctx, "acme", "fresh", now.Add(time.Hour)); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	sec, _ := r.Client.CoreV1().Secrets(ns).Get(ctx, "kai-acme-chat-bridge", metav1.GetOptions{})
	var got []entry
	if err := json.Unmarshal(sec.Data[SecretKey], &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Jti != "fresh" {
		t.Fatalf("expected only the fresh entry after prune, got %+v", got)
	}
}

func TestSecretRevokerCapBoundsList(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)

	// Seed at the cap with monotonically-increasing exp times.
	seed := make([]entry, MaxEntriesPerSlug)
	for i := range seed {
		seed[i] = entry{Jti: "j" + itoa(i), Exp: now.Add(time.Hour + time.Duration(i)*time.Second).Unix()}
	}
	raw, _ := json.Marshal(seed)
	r := newRevoker(t, "acme", raw, now)
	ctx := context.Background()

	// Revoke one more — the oldest-by-exp must be evicted, the new one must land.
	newExp := now.Add(2 * time.Hour)
	if err := r.Revoke(ctx, "acme", "newest", newExp); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	sec, _ := r.Client.CoreV1().Secrets(ns).Get(ctx, "kai-acme-chat-bridge", metav1.GetOptions{})
	var got []entry
	_ = json.Unmarshal(sec.Data[SecretKey], &got)
	if len(got) != MaxEntriesPerSlug {
		t.Fatalf("expected list capped at %d, got %d", MaxEntriesPerSlug, len(got))
	}
	if !contains(got, "newest") {
		t.Fatal("newest entry must always land even at the cap")
	}
	if contains(got, "j0") {
		t.Fatal("oldest-by-exp entry j0 should have been evicted at the cap")
	}
}

func TestSecretRevokerIdempotent(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	r := newRevoker(t, "acme", nil, now)
	ctx := context.Background()

	exp := now.Add(time.Hour)
	for i := 0; i < 5; i++ {
		if err := r.Revoke(ctx, "acme", "abc", exp); err != nil {
			t.Fatalf("revoke #%d: %v", i, err)
		}
	}
	sec, _ := r.Client.CoreV1().Secrets(ns).Get(ctx, "kai-acme-chat-bridge", metav1.GetOptions{})
	var got []entry
	_ = json.Unmarshal(sec.Data[SecretKey], &got)
	if len(got) != 1 {
		t.Fatalf("expected 1 entry after 5 idempotent revokes, got %d (%+v)", len(got), got)
	}
}

func TestSecretRevokerMissingSecretIsNotRevoked(t *testing.T) {
	t.Parallel()
	c := fake.NewSimpleClientset() // no chat-bridge secret
	r := &SecretRevoker{Client: c, Namespace: ns}
	got, err := r.IsRevoked(context.Background(), "acme", "abc")
	if err != nil || got {
		t.Fatalf("missing secret must mean not-revoked, got %v err=%v", got, err)
	}
}

func contains(xs []entry, jti string) bool {
	for _, e := range xs {
		if e.Jti == jti {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte("0123456789")
	out := make([]byte, 0, 6)
	for n > 0 {
		out = append([]byte{digits[n%10]}, out...)
		n /= 10
	}
	return string(out)
}

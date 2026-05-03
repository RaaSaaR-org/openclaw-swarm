package auth

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSignJWTAddsJti(t *testing.T) {
	t.Parallel()
	secret := mustSecret(t)
	now := time.Unix(1_700_000_000, 0)

	c1, err := IssueSession("acme", "alice@example.com", secret, now)
	if err != nil {
		t.Fatalf("issue 1: %v", err)
	}
	c2, err := IssueSession("acme", "alice@example.com", secret, now)
	if err != nil {
		t.Fatalf("issue 2: %v", err)
	}

	claims1, err := ParseJWT(c1.Value, secret)
	if err != nil {
		t.Fatalf("parse 1: %v", err)
	}
	claims2, err := ParseJWT(c2.Value, secret)
	if err != nil {
		t.Fatalf("parse 2: %v", err)
	}
	if claims1.Jti == "" || claims2.Jti == "" {
		t.Fatalf("expected non-empty jti, got %q / %q", claims1.Jti, claims2.Jti)
	}
	if claims1.Jti == claims2.Jti {
		t.Fatalf("expected unique jti per IssueSession, got duplicate %q", claims1.Jti)
	}
	if len(claims1.Jti) != 16 || strings.Trim(claims1.Jti, "0123456789abcdef") != "" {
		t.Fatalf("expected 16 hex chars, got %q", claims1.Jti)
	}
}

func TestParseJWTBackwardCompatNoJti(t *testing.T) {
	t.Parallel()
	// Tokens minted before jti existed must still parse cleanly (Jti -> "").
	secret := mustSecret(t)
	now := time.Unix(1_700_000_000, 0)
	legacy := SessionClaims{Sub: "a", Slug: "acme", Iat: now.Unix(), Exp: now.Add(time.Hour).Unix()}
	tok, err := SignJWT(legacy, secret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	got, err := ParseJWT(tok, secret)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Jti != "" {
		t.Fatalf("expected empty jti for legacy claim, got %q", got.Jti)
	}
}

func TestMemoryRevokerRoundTrip(t *testing.T) {
	t.Parallel()
	r := NewMemoryRevoker()
	ctx := context.Background()
	exp := time.Unix(1_700_000_000+3600, 0)
	r.now = func() time.Time { return time.Unix(1_700_000_000, 0) }

	if got, _ := r.IsRevoked(ctx, "acme", "abc"); got {
		t.Fatal("fresh jti must not be revoked")
	}
	if err := r.Revoke(ctx, "acme", "abc", exp); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got, _ := r.IsRevoked(ctx, "acme", "abc"); !got {
		t.Fatal("just-revoked jti must be reported as revoked")
	}
	// Different slug → different bucket.
	if got, _ := r.IsRevoked(ctx, "betaco", "abc"); got {
		t.Fatal("revocation must be slug-scoped")
	}
	// Empty jti is never revoked.
	if got, _ := r.IsRevoked(ctx, "acme", ""); got {
		t.Fatal("empty jti must never be revoked")
	}
	if err := r.Revoke(ctx, "acme", "", exp); err != nil {
		t.Fatalf("revoke empty jti must be a no-op error-free: %v", err)
	}
}

func TestMemoryRevokerPrunesExpired(t *testing.T) {
	t.Parallel()
	r := NewMemoryRevoker()
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	r.now = func() time.Time { return now }

	if err := r.Revoke(ctx, "acme", "stale", now.Add(-time.Hour)); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// Bypass the early-return: write directly so we exercise the prune branch.
	r.entries["acme"]["stale"] = now.Add(-time.Hour).Unix()
	r.entries["acme"]["fresh"] = now.Add(time.Hour).Unix()

	got, _ := r.IsRevoked(ctx, "acme", "fresh")
	if !got {
		t.Fatal("fresh entry must still be revoked")
	}
	if _, present := r.entries["acme"]["stale"]; present {
		t.Fatal("expired entry should be pruned on read")
	}
}

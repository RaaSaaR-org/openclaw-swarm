package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/emai-ai/swarm/pkg/auth"
)

func mustHash(t *testing.T, secret string) string {
	t.Helper()
	h, err := auth.HashPassword(secret)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return h
}

func TestParseTokenStore_AdminAndTenant(t *testing.T) {
	t.Parallel()
	yaml := fmt.Sprintf(`tokens:
  - name: kira-admin
    hash: "%s"
    role: admin
  - name: kai-acme
    hash: "%s"
    role: tenant
    slug: acme
    customer_id: CUST-001
`, mustHash(t, "admin-secret"), mustHash(t, "tenant-secret"))

	s, err := parseTokenStore([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Len() != 2 {
		t.Fatalf("expected 2 tokens, got %d", s.Len())
	}
	admin := s.Verify("admin-secret")
	if admin == nil || admin.Role != RoleAdmin || admin.Name != "kira-admin" {
		t.Fatalf("admin verify failed: %+v", admin)
	}
	tenant := s.Verify("tenant-secret")
	if tenant == nil || tenant.Role != RoleTenant || tenant.Slug != "acme" || tenant.CustomerID != "CUST-001" {
		t.Fatalf("tenant verify failed: %+v", tenant)
	}
	if got := s.Verify("wrong"); got != nil {
		t.Fatalf("wrong bearer matched: %+v", got)
	}
}

func TestParseTokenStore_RejectsBadInputs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"empty file", `tokens: []`, "empty"},
		{"missing name", `tokens: [{hash: "x", role: admin}]`, "name is required"},
		{"missing hash", `tokens: [{name: a, role: admin}]`, "hash is required"},
		{"unknown role", fmt.Sprintf(`tokens:
  - name: x
    hash: "%s"
    role: superuser`, mustHash(t, "x")), "unknown role"},
		{"tenant without slug", fmt.Sprintf(`tokens:
  - name: x
    hash: "%s"
    role: tenant
    customer_id: CUST-001`, mustHash(t, "x")), "tenant role requires slug and customer_id"},
		{"tenant without customer_id", fmt.Sprintf(`tokens:
  - name: x
    hash: "%s"
    role: tenant
    slug: foo`, mustHash(t, "x")), "tenant role requires slug and customer_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseTokenStore([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %q", tc.want, err)
			}
		})
	}
}

// Hot vs cold timing: 100 cache hits should be substantially faster than one
// cold argon2 verify. Without the cache, hot_total would be ~100× cold; with
// it, hot_total is microseconds.
func TestVerify_CacheAvoidsArgon2OnRepeat(t *testing.T) {
	t.Parallel()
	yaml := fmt.Sprintf(`tokens:
  - name: bot
    hash: "%s"
    role: admin
`, mustHash(t, "hunter2"))
	s, err := parseTokenStore([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}

	t0 := time.Now()
	if s.Verify("hunter2") == nil {
		t.Fatal("first verify should succeed")
	}
	cold := time.Since(t0)

	t1 := time.Now()
	for i := 0; i < 100; i++ {
		if s.Verify("hunter2") == nil {
			t.Fatal("hot verify should succeed")
		}
	}
	hot := time.Since(t1)

	if hot >= cold/10 {
		t.Fatalf("cache not effective: cold=%s, 100×hot=%s", cold, hot)
	}
}

// Failed verifications must not populate the cache — otherwise an attacker
// could exhaust memory by hammering with random bearers.
func TestVerify_FailedDoesNotGrowCache(t *testing.T) {
	t.Parallel()
	yaml := fmt.Sprintf(`tokens:
  - name: bot
    hash: "%s"
    role: admin
`, mustHash(t, "right"))
	s, err := parseTokenStore([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if s.Verify(fmt.Sprintf("wrong-%d", i)) != nil {
			t.Fatal("should not match")
		}
	}
	if got := s.cacheSize(); got != 0 {
		t.Fatalf("cache must be empty after failed verifies, got %d entries", got)
	}
}

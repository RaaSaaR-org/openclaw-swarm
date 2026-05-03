// Integration tests for the OpenRouter client. Guarded by OPENROUTER_KEY so
// the default `go test ./...` pass on a developer laptop without an API key
// skips cleanly. To run locally:
//
//	OPENROUTER_KEY=sk-or-v1-... go test ./pkg/openrouter/... -v -run Integration
//
// Each test is a single API call against the real production endpoint.
// Costs nothing — both /key and /credits are free reads — but does expose
// the key shape (HTTP-Referer / X-Title headers etc.) the production cron
// will use.
package openrouter

import (
	"context"
	"os"
	"testing"
)

func newIntegrationClient(t *testing.T) *Client {
	t.Helper()
	key := os.Getenv("OPENROUTER_KEY")
	if key == "" {
		t.Skip("OPENROUTER_KEY not set — skipping OpenRouter integration tests")
	}
	c, err := NewClient(key)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestIntegrationGetKey(t *testing.T) {
	c := newIntegrationClient(t)
	info, err := c.GetKey(context.Background())
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	// Sanity: real keys have a non-empty redacted label.
	if info.Label == "" {
		t.Error("expected non-empty redacted Label")
	}
	// Usage is in USD; can't assert a specific number, but the shape must parse.
	t.Logf("integration key info: label=%s usage=$%.4f daily=$%.4f weekly=$%.4f monthly=$%.4f free=%v",
		info.Label, info.Usage, info.UsageDaily, info.UsageWeekly, info.UsageMonthly, info.IsFreeTier)
}

func TestIntegrationGetCredits(t *testing.T) {
	c := newIntegrationClient(t)
	cr, err := c.GetCredits(context.Background())
	if err != nil {
		t.Fatalf("GetCredits: %v", err)
	}
	t.Logf("integration credits: total=$%.4f used=$%.4f", cr.TotalCredits, cr.TotalUsage)
}

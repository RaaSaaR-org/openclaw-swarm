package quotas

import (
	"testing"
	"time"
)

func TestForKnownTiers(t *testing.T) {
	t.Parallel()
	for _, tier := range []Tier{TierFree, TierStarter, TierGrowth, TierEnterprise} {
		l := For(tier)
		// Sanity: enterprise is intentionally all-zero (overlay-controlled);
		// every other tier must define memory.
		if tier != TierEnterprise && l.MemoryRequest == "" {
			t.Errorf("%s: MemoryRequest must be set", tier)
		}
	}
}

func TestForUnknownTierFallsBackToFree(t *testing.T) {
	t.Parallel()
	got := For("vip")
	want := For(TierFree)
	if got.MemoryRequest != want.MemoryRequest || got.DailyMessages != want.DailyMessages {
		t.Errorf("unknown tier should fall back to free, got %+v", got)
	}
}

func TestForEmptyTierFallsBackToFree(t *testing.T) {
	t.Parallel()
	got := For("")
	want := For(TierFree)
	if got.MemoryRequest != want.MemoryRequest {
		t.Errorf("empty tier should fall back to free, got %+v", got)
	}
}

func TestFreeTierShape(t *testing.T) {
	t.Parallel()
	l := For(TierFree)
	if l.MaxInstancesPerUser != 1 {
		t.Errorf("free max instances = %d, want 1", l.MaxInstancesPerUser)
	}
	if l.MemoryRequest != "384Mi" {
		t.Errorf("free memory request = %q, want 384Mi (matches argon2 headroom)", l.MemoryRequest)
	}
	if l.MaxTelegramBots != 0 {
		t.Errorf("free Telegram = %d, want 0 (disabled on free tier)", l.MaxTelegramBots)
	}
	if l.IdleSuspendAfter != 14*24*time.Hour {
		t.Errorf("free idle suspend = %v, want 14d", l.IdleSuspendAfter)
	}
}

func TestEnterpriseIsAllZero(t *testing.T) {
	t.Parallel()
	l := For(TierEnterprise)
	if l.MaxInstancesPerUser != 0 || l.MemoryRequest != "" || l.DailyTokens != 0 {
		t.Errorf("enterprise must be all-zero (overlay-controlled), got %+v", l)
	}
}

func TestMaxInstancesReached(t *testing.T) {
	t.Parallel()
	if !MaxInstancesReached(TierFree, 1) {
		t.Error("free tier with 1 instance must report cap reached")
	}
	if MaxInstancesReached(TierFree, 0) {
		t.Error("free tier with 0 instances must report cap NOT reached")
	}
	if MaxInstancesReached(TierEnterprise, 1_000_000) {
		t.Error("enterprise (unbounded) must never report cap reached")
	}
}

func TestValidTier(t *testing.T) {
	t.Parallel()
	for _, ok := range []Tier{TierFree, TierStarter, TierGrowth, TierEnterprise, "FREE", "Starter"} {
		if !ValidTier(ok) {
			t.Errorf("%q must be valid", ok)
		}
	}
	for _, bad := range []Tier{"vip", "", "freelance"} {
		if ValidTier(bad) {
			t.Errorf("%q must NOT be valid", bad)
		}
	}
}

func TestOverrideReplacesTierLimits(t *testing.T) {
	// Not parallel — we mutate the shared defaults map.
	original := For(TierStarter)
	t.Cleanup(func() { _ = Override(TierStarter, original) })

	custom := Limits{MaxInstancesPerUser: 5, MemoryRequest: "2Gi", MemoryLimit: "4Gi"}
	if err := Override(TierStarter, custom); err != nil {
		t.Fatalf("Override: %v", err)
	}
	got := For(TierStarter)
	if got.MaxInstancesPerUser != 5 || got.MemoryRequest != "2Gi" {
		t.Errorf("Override didn't take effect: %+v", got)
	}

	// Override on bad tier rejects.
	if err := Override("vip", custom); err == nil {
		t.Error("Override should reject unknown tier")
	}
}

package server

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
)

func TestAgentQuotaLimiterEnforcesTokenRequestTenantAndProviderBudgets(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	policy := agentdomain.LimitPolicy{Tiers: map[string]agentdomain.Limits{
		"test": {RequestsPerMinute: 2, TokensPerMinute: 10, MaxTenantConcurrent: 1, DefaultOutputTokens: 4, MaxOutputTokens: 8, JSONTimeout: time.Second, StreamTimeout: 2 * time.Second},
	}}
	limiter := newAgentQuotaLimiter(policy, 1)
	limiter.now = func() time.Time { return now }
	request := agentAdmission{TenantID: "tenant-a", TokenID: "token-a", Tier: "test", EstimatedTokens: 4}

	release, err := limiter.acquire(context.Background(), request)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := limiter.acquire(context.Background(), request); !isAgentQuotaError(err) {
		t.Fatalf("tenant concurrency error = %v, want quota error", err)
	}
	if _, err := limiter.acquire(context.Background(), agentAdmission{TenantID: "tenant-b", TokenID: "token-b", Tier: "test", EstimatedTokens: 1}); !isAgentQuotaError(err) {
		t.Fatalf("provider concurrency error = %v, want quota error", err)
	}
	release(3)

	release, err = limiter.acquire(context.Background(), request)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	release(4)
	if _, err := limiter.acquire(context.Background(), request); !isAgentQuotaError(err) {
		t.Fatalf("request budget error = %v, want quota error", err)
	}

	now = now.Add(time.Minute)
	release, err = limiter.acquire(context.Background(), agentAdmission{TenantID: "tenant-a", TokenID: "token-a", Tier: "test", EstimatedTokens: 8})
	if err != nil {
		t.Fatalf("new window acquire: %v", err)
	}
	release(8)
	if _, err := limiter.acquire(context.Background(), request); !isAgentQuotaError(err) {
		t.Fatalf("token budget error = %v, want quota error", err)
	}
}

func TestAgentQuotaLimiterRejectsUntrustedOrCanceledAdmissionWithoutReservation(t *testing.T) {
	limiter := newAgentQuotaLimiter(agentdomain.DefaultLimitPolicy(), 2)
	for _, request := range []agentAdmission{
		{},
		{TenantID: "tenant", TokenID: "token", Tier: "unknown", EstimatedTokens: 1},
		{TenantID: "tenant", TokenID: "token", Tier: string(agentdomain.TierStandard), EstimatedTokens: agentdomain.HardMaxOutputTokens + 1},
	} {
		if _, err := limiter.acquire(context.Background(), request); err == nil {
			t.Fatalf("admission %+v unexpectedly succeeded", request)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := limiter.acquire(ctx, agentAdmission{TenantID: "tenant", TokenID: "token", Tier: string(agentdomain.TierStandard), EstimatedTokens: 1})
	var agentErr *agentdomain.Error
	if !errors.As(err, &agentErr) || agentErr.Code != agentdomain.ErrorRequestCancelled {
		t.Fatalf("cancel error = %v, want request_cancelled", err)
	}
}

func TestAgentQuotaReleaseIsIdempotent(t *testing.T) {
	limiter := newAgentQuotaLimiter(agentdomain.DefaultLimitPolicy(), 1)
	request := agentAdmission{TenantID: "tenant", TokenID: "token", Tier: string(agentdomain.TierStandard), EstimatedTokens: 1}
	release, err := limiter.acquire(context.Background(), request)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	release(1)
	release(1)
	if _, err := limiter.acquire(context.Background(), request); err != nil {
		t.Fatalf("idempotent release leaked concurrency: %v", err)
	}
}

func TestAgentLimitTierComesOnlyFromVerifiedPrincipalField(t *testing.T) {
	principal := domain.Principal{RateLimitTier: "limited", Scopes: []string{"rate_limit_tier:elevated", "tier:elevated"}}
	tier, err := agentLimitTierFromPrincipal(principal)
	if err != nil || tier != agentdomain.TierLimited {
		t.Fatalf("tier=%q err=%v", tier, err)
	}
	for _, forged := range []string{"", "unknown", "elevated "} {
		principal.RateLimitTier = forged
		if _, err := agentLimitTierFromPrincipal(principal); err == nil {
			t.Fatalf("forged/unknown tier %q accepted", forged)
		}
	}
}

// quotaPruneTestPolicy grants generous per-key budgets so the window-hygiene
// tests measure map growth instead of admission denials.
func quotaPruneTestPolicy() agentdomain.LimitPolicy {
	return agentdomain.LimitPolicy{Tiers: map[string]agentdomain.Limits{
		"test": {RequestsPerMinute: 100, TokensPerMinute: 1000, MaxTenantConcurrent: 64, DefaultOutputTokens: 1, MaxOutputTokens: 8, JSONTimeout: time.Second, StreamTimeout: 2 * time.Second},
	}}
}

func TestAgentQuotaLimiterPrunesExpiredWindows(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	limiter := newAgentQuotaLimiter(quotaPruneTestPolicy(), 8)
	limiter.now = func() time.Time { return now }

	for i := 0; i < 40; i++ {
		release, err := limiter.acquire(context.Background(), agentAdmission{TenantID: "tenant", TokenID: "token-" + strconv.Itoa(i), Tier: "test", EstimatedTokens: 1})
		if err != nil {
			t.Fatalf("seed acquire %d: %v", i, err)
		}
		release(1)
	}
	if got := len(limiter.windows); got != 40 {
		t.Fatalf("seeded windows = %d, want 40", got)
	}

	now = now.Add(2 * time.Minute)
	release, err := limiter.acquire(context.Background(), agentAdmission{TenantID: "tenant", TokenID: "fresh", Tier: "test", EstimatedTokens: 1})
	if err != nil {
		t.Fatalf("post-boundary acquire: %v", err)
	}
	release(1)

	if got := len(limiter.windows); got != 1 {
		t.Fatalf("windows after boundary acquire = %d, want expired entries pruned down to 1", got)
	}
	if _, ok := limiter.windows[agentQuotaKey{tenant: "tenant", token: "fresh"}]; !ok {
		t.Fatal("live key missing after prune")
	}
}

func TestAgentQuotaLimiterPruneKeepsActiveWindow(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	limiter := newAgentQuotaLimiter(quotaPruneTestPolicy(), 8)
	limiter.now = func() time.Time { return now }

	release, err := limiter.acquire(context.Background(), agentAdmission{TenantID: "tenant", TokenID: "stale", Tier: "test", EstimatedTokens: 1})
	if err != nil {
		t.Fatalf("stale acquire: %v", err)
	}
	release(1)

	now = now.Add(30 * time.Second)
	release, err = limiter.acquire(context.Background(), agentAdmission{TenantID: "tenant", TokenID: "active", Tier: "test", EstimatedTokens: 1})
	if err != nil {
		t.Fatalf("active acquire: %v", err)
	}
	release(1)

	now = now.Add(40 * time.Second)
	release, err = limiter.acquire(context.Background(), agentAdmission{TenantID: "tenant", TokenID: "trigger", Tier: "test", EstimatedTokens: 1})
	if err != nil {
		t.Fatalf("trigger acquire: %v", err)
	}
	release(1)

	if _, ok := limiter.windows[agentQuotaKey{tenant: "tenant", token: "stale"}]; ok {
		t.Fatal("expired window survived pruning")
	}
	for _, token := range []string{"active", "trigger"} {
		if _, ok := limiter.windows[agentQuotaKey{tenant: "tenant", token: token}]; !ok {
			t.Fatalf("in-window key %q was pruned", token)
		}
	}
}

func TestAgentQuotaLimiterWindowPruneIsConcurrencySafe(t *testing.T) {
	limiter := newAgentQuotaLimiter(quotaPruneTestPolicy(), 16)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				token := "token-" + strconv.Itoa((worker+i)%16)
				release, err := limiter.acquire(context.Background(), agentAdmission{TenantID: "tenant", TokenID: token, Tier: "test", EstimatedTokens: 1})
				if err != nil {
					continue
				}
				release(1)
			}
		}(worker)
	}
	wg.Wait()

	if got := len(limiter.windows); got > 16 {
		t.Fatalf("windows map = %d, want at most the 16 distinct tokens", got)
	}
}

package server

import (
	"context"
	"errors"
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

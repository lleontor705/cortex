package agent

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const HardMaxOutputTokens = 4096

type Transport string

const (
	TransportJSON   Transport = "json"
	TransportStream Transport = "stream"
)

type LimitTier string

const (
	TierLimited  LimitTier = "limited"
	TierStandard LimitTier = "standard"
	TierElevated LimitTier = "elevated"
)

var ErrUnknownLimitTier = errors.New("unknown agent limit tier")

const (
	ErrorQuotaExceeded    ErrorCode = "quota_exceeded"
	ErrorAgentTimeout     ErrorCode = "agent_timeout"
	ErrorRequestCancelled ErrorCode = "request_cancelled"
	ErrorAuditUnavailable ErrorCode = "audit_unavailable"
)

// Limits are trusted server-side budgets. None of these values may be supplied
// by an agent request.
type Limits struct {
	RequestsPerMinute   int
	TokensPerMinute     int
	MaxTenantConcurrent int
	DefaultOutputTokens int
	MaxOutputTokens     int
	JSONTimeout         time.Duration
	StreamTimeout       time.Duration
}

type LimitPolicy struct {
	Tiers map[string]Limits
}

func DefaultLimitPolicy() LimitPolicy {
	return LimitPolicy{Tiers: map[string]Limits{
		string(TierLimited): {
			RequestsPerMinute: 10, TokensPerMinute: 20_000, MaxTenantConcurrent: 1,
			DefaultOutputTokens: 800, MaxOutputTokens: HardMaxOutputTokens,
			JSONTimeout: 30 * time.Second, StreamTimeout: 60 * time.Second,
		},
		string(TierStandard): {
			RequestsPerMinute: 30, TokensPerMinute: 60_000, MaxTenantConcurrent: 2,
			DefaultOutputTokens: 1200, MaxOutputTokens: HardMaxOutputTokens,
			JSONTimeout: 30 * time.Second, StreamTimeout: 60 * time.Second,
		},
		string(TierElevated): {
			RequestsPerMinute: 120, TokensPerMinute: 240_000, MaxTenantConcurrent: 8,
			DefaultOutputTokens: 1200, MaxOutputTokens: HardMaxOutputTokens,
			JSONTimeout: 30 * time.Second, StreamTimeout: 60 * time.Second,
		},
	}}
}

func (p LimitPolicy) ForTier(tier LimitTier) (Limits, error) {
	limits, ok := p.Tiers[string(tier)]
	if !ok || !limits.valid() {
		return Limits{}, fmt.Errorf("%w: %s", ErrUnknownLimitTier, tier)
	}
	return limits, nil
}

func (l Limits) valid() bool {
	return l.RequestsPerMinute > 0 && l.TokensPerMinute > 0 && l.MaxTenantConcurrent > 0 &&
		l.DefaultOutputTokens > 0 && l.DefaultOutputTokens <= l.MaxOutputTokens &&
		l.MaxOutputTokens <= HardMaxOutputTokens && l.JSONTimeout > 0 && l.StreamTimeout > 0
}

// QuotaError carries bounded retry metadata without exposing limiter keys or
// internal capacity state.
type QuotaError struct {
	RetryAfter time.Duration
}

func (e *QuotaError) Error() string { return string(ErrorQuotaExceeded) }

// WithRequestTimeout derives the server-owned transport deadline while
// preserving an earlier upstream deadline and cancellation.
func WithRequestTimeout(parent context.Context, limits Limits, transport Transport) (context.Context, context.CancelFunc) {
	timeout := limits.JSONTimeout
	if transport == TransportStream {
		timeout = limits.StreamTimeout
	}
	return context.WithTimeout(parent, timeout)
}

func ContextErrorCode(err error) ErrorCode {
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorAgentTimeout
	}
	if errors.Is(err, context.Canceled) {
		return ErrorRequestCancelled
	}
	return ErrorProviderUnavailable
}

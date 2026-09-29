package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
)

const (
	// quotaWindowDuration is the fixed rolling admission window; requests older
	// than one window never count toward the current budget.
	quotaWindowDuration = time.Minute
	// quotaSweepBatch caps how many expired windows a single acquire may drop so
	// the sweep cost stays bounded no matter how many stale keys accumulated.
	quotaSweepBatch = 64
)

type agentAdmission struct {
	TenantID        string
	TokenID         string
	Tier            string
	Transport       agentdomain.Transport
	EstimatedTokens int
}

type agentQuotaKey struct {
	tenant string
	token  string
}

type agentQuotaWindow struct {
	started  time.Time
	requests int
	tokens   int
}

// agentQuotaLimiter is process-local admission control. Distributed deployments
// must compose it behind a shared limiter before enabling the feature globally.
type agentQuotaLimiter struct {
	mu                  sync.Mutex
	policy              agentdomain.LimitPolicy
	providerConcurrency int
	now                 func() time.Time
	windows             map[agentQuotaKey]agentQuotaWindow
	tenantActive        map[string]int
	providerActive      int
}

func newAgentQuotaLimiter(policy agentdomain.LimitPolicy, providerConcurrency int) *agentQuotaLimiter {
	if providerConcurrency < 1 {
		providerConcurrency = 1
	}
	return &agentQuotaLimiter{
		policy: policy, providerConcurrency: providerConcurrency, now: time.Now,
		windows: make(map[agentQuotaKey]agentQuotaWindow), tenantActive: make(map[string]int),
	}
}

// acquire reserves request, estimated-token, tenant and provider capacity. The
// returned release function is idempotent and reconciles estimated tokens with
// actual usage without ever refunding the request charge.
func (l *agentQuotaLimiter) acquire(ctx context.Context, admission agentAdmission) (func(actualTokens int), error) {
	if err := ctx.Err(); err != nil {
		return nil, &agentdomain.Error{Code: agentdomain.ContextErrorCode(err), Err: err}
	}
	if strings.TrimSpace(admission.TenantID) == "" || strings.TrimSpace(admission.TokenID) == "" || admission.EstimatedTokens <= 0 {
		return nil, &agentdomain.Error{Code: agentdomain.ErrorInvalidRequest}
	}
	limits, err := l.policy.ForTier(agentdomain.LimitTier(admission.Tier))
	if err != nil || admission.EstimatedTokens > limits.MaxOutputTokens {
		return nil, &agentdomain.Error{Code: agentdomain.ErrorInvalidRequest, Err: err}
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.pruneExpiredWindowsLocked(now)
	key := agentQuotaKey{tenant: admission.TenantID, token: admission.TokenID}
	window := l.windows[key]
	if quotaWindowExpired(now, window) {
		window = agentQuotaWindow{started: now}
	}
	retry := quotaWindowDuration - now.Sub(window.started)
	if retry <= 0 || retry > quotaWindowDuration {
		retry = quotaWindowDuration
	}
	if window.requests >= limits.RequestsPerMinute || window.tokens+admission.EstimatedTokens > limits.TokensPerMinute ||
		l.tenantActive[admission.TenantID] >= limits.MaxTenantConcurrent || l.providerActive >= l.providerConcurrency {
		return nil, quotaExceeded(retry)
	}
	window.requests++
	window.tokens += admission.EstimatedTokens
	l.windows[key] = window
	l.tenantActive[admission.TenantID]++
	l.providerActive++

	var once sync.Once
	release := func(actualTokens int) {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			current := l.windows[key]
			if current.started.Equal(window.started) {
				if actualTokens < 0 {
					actualTokens = 0
				}
				current.tokens += actualTokens - admission.EstimatedTokens
				if current.tokens < 0 {
					current.tokens = 0
				}
				l.windows[key] = current
			}
			if l.tenantActive[admission.TenantID] > 0 {
				l.tenantActive[admission.TenantID]--
			}
			if l.providerActive > 0 {
				l.providerActive--
			}
		})
	}
	return release, nil
}

// quotaWindowExpired reports whether a stored window fell outside the current
// rolling bucket. A window started in the future (clock stepped back) is also
// treated as expired because acquire resets it on touch regardless.
func quotaWindowExpired(now time.Time, window agentQuotaWindow) bool {
	return window.started.IsZero() || now.Sub(window.started) >= quotaWindowDuration || now.Before(window.started)
}

// pruneExpiredWindowsLocked drops a bounded batch of windows whose bucket has
// elapsed so key memory stays proportional to the tokens active in the current
// window instead of growing with every token ever seen. Callers must hold l.mu.
func (l *agentQuotaLimiter) pruneExpiredWindowsLocked(now time.Time) {
	removed := 0
	for key, window := range l.windows {
		if removed == quotaSweepBatch {
			return
		}
		if quotaWindowExpired(now, window) {
			delete(l.windows, key)
			removed++
		}
	}
}

func quotaExceeded(retry time.Duration) error {
	return &agentdomain.Error{Code: agentdomain.ErrorQuotaExceeded, Err: &agentdomain.QuotaError{RetryAfter: retry}}
}

func isAgentQuotaError(err error) bool {
	var agentErr *agentdomain.Error
	return errors.As(err, &agentErr) && agentErr.Code == agentdomain.ErrorQuotaExceeded
}

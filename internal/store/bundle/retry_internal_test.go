// Package bundle: internal tests for unexported retry/backoff functions.
//
// This file is in package `bundle` (NOT `bundle_test`) so it can access
// unexported identifiers: retryOnBusy, computeBackoff, IsSQLiteBusy. This
// avoids expanding the public API just for testability.
//
// The deterministic retryOnBusy test (TestRetryOnBusy_*) is the AUTHORITATIVE
// defect pin for REQ-TX-002's edge scenario ("busy timeout exceeded → stable
// retryable SQLITE_BUSY"). It injects a pure function that always returns a
// BUSY-classified error — NO real SQLite contention, NO timing dependency,
// NO t.Skip path. It deterministically proves retry exhaustion behavior.
package bundle

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// ---------------------------------------------------------------------------
// CRIT-1: Deterministic retryOnBusy defect pin (REQ-TX-002 edge scenario)
// ---------------------------------------------------------------------------

// TestRetryOnBusy_AlwaysBusyRetriesExactlyMaxThenReturnsStableError is the
// AUTHORITATIVE deterministic defect pin for REQ-TX-002's edge scenario
// ("busy timeout exceeded → stable retryable SQLITE_BUSY").
//
// Unlike TestUnitOfWork_BusyCapReturnsStableError (which depends on real
// SQLite write-lock contention timing), this test injects a PURE FUNCTION
// that always returns a BUSY-classified error. It deterministically proves:
//  1. retryOnBusy retries exactly (1 + MaxRetries) times
//  2. the returned error is non-nil
//  3. IsSQLiteBusy(returnedErr) == true (stable retryable signal)
//  4. no panic
//  5. total elapsed time is bounded (sum of backoffs < sane cap — no unbounded blocking)
//
// This test has NO t.Skip path and does NOT depend on SQLite timing.
func TestRetryOnBusy_AlwaysBusyRetriesExactlyMaxThenReturnsStableError(t *testing.T) {
	cfg := domain.BusyRetryConfig{
		MaxRetries:   3,
		BaseBackoff:  1 * time.Millisecond,
		MaxBackoff:   5 * time.Millisecond,
		JitterFactor: 0.0, // no jitter for deterministic timing
	}

	var calls int32
	alwaysBusy := func() error {
		atomic.AddInt32(&calls, 1)
		return errors.New("SQLITE_BUSY: database is locked")
	}

	ctx := context.Background()
	var sleeps []time.Duration
	err := retryOnBusyWithSleeper(ctx, cfg, alwaysBusy, func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	})

	// 1. Exactly (1 + MaxRetries) calls: the initial attempt + MaxRetries retries.
	expectedCalls := int32(1 + cfg.MaxRetries)
	if calls != expectedCalls {
		t.Errorf("fn called %d times, want exactly %d (1 + MaxRetries=%d) — retry count contract violated",
			calls, expectedCalls, cfg.MaxRetries)
	}

	// 2. Non-nil error returned after exhaustion.
	if err == nil {
		t.Fatal("retryOnBusy returned nil for an always-BUSY op — should return the error after retry exhaustion")
	}

	// 3. The returned error is a stable retryable BUSY signal.
	if !IsSQLiteBusy(err) {
		t.Errorf("returned err = %v, want IsSQLiteBusy(err)=true (stable retryable signal for REQ-TX-002)", err)
	}

	// 4. No panic — we reached here.

	// 5. Exact backoff policy, without wall-clock scheduling assumptions.
	wantSleeps := []time.Duration{1 * time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond}
	if len(sleeps) != len(wantSleeps) {
		t.Fatalf("sleep count = %d, want %d (%v)", len(sleeps), len(wantSleeps), wantSleeps)
	}
	for i := range wantSleeps {
		if sleeps[i] != wantSleeps[i] {
			t.Errorf("sleep[%d] = %v, want %v", i, sleeps[i], wantSleeps[i])
		}
	}
	t.Logf("retryOnBusy: %d calls with backoffs %v", calls, sleeps)
}

// TestRetryOnBusy_NonBusyErrorReturnsImmediately proves that a non-BUSY error
// is returned immediately WITHOUT consuming the retry budget. This is the
// complementary contract: only BUSY errors trigger retry.
func TestRetryOnBusy_NonBusyErrorReturnsImmediately(t *testing.T) {
	cfg := domain.BusyRetryConfig{
		MaxRetries:   5,
		BaseBackoff:  10 * time.Millisecond,
		MaxBackoff:   50 * time.Millisecond,
		JitterFactor: 0.0,
	}

	var calls int32
	nonBusy := func() error {
		atomic.AddInt32(&calls, 1)
		return errors.New("constraint failed: UNIQUE violation")
	}

	ctx := context.Background()
	err := retryOnBusy(ctx, cfg, nonBusy)

	if calls != 1 {
		t.Errorf("fn called %d times for a non-BUSY error, want exactly 1 (no retry)", calls)
	}
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if IsSQLiteBusy(err) {
		t.Errorf("non-BUSY error classified as BUSY: %v", err)
	}
}

// TestRetryOnBusy_SuccessReturnsNilImmediately proves that a successful op
// returns nil on the first attempt without retrying.
func TestRetryOnBusy_SuccessReturnsNilImmediately(t *testing.T) {
	cfg := domain.BusyRetryConfig{
		MaxRetries:   5,
		BaseBackoff:  10 * time.Millisecond,
		MaxBackoff:   50 * time.Millisecond,
		JitterFactor: 0.0,
	}

	var calls int32
	success := func() error {
		atomic.AddInt32(&calls, 1)
		return nil
	}

	ctx := context.Background()
	err := retryOnBusy(ctx, cfg, success)

	if calls != 1 {
		t.Errorf("fn called %d times for a successful op, want exactly 1", calls)
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// TestRetryOnBusy_RespectsContextCancellation proves that a cancelled context
// aborts retry immediately rather than blocking.
func TestRetryOnBusy_RespectsContextCancellation(t *testing.T) {
	cfg := domain.BusyRetryConfig{
		MaxRetries:   10,
		BaseBackoff:  50 * time.Millisecond,
		MaxBackoff:   200 * time.Millisecond,
		JitterFactor: 0.0,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	var calls int32
	alwaysBusy := func() error {
		atomic.AddInt32(&calls, 1)
		return errors.New("SQLITE_BUSY")
	}

	_ = retryOnBusy(ctx, cfg, alwaysBusy)

	// With a pre-cancelled context, retryOnBusy should call fn at most once (the
	// initial attempt before the ctx.Err() check fires on the next iteration).
	if calls > 1 {
		t.Errorf("fn called %d times with pre-cancelled context, want at most 1", calls)
	}
}

// ---------------------------------------------------------------------------
// SUGG-1: computeBackoff table-driven tests (jitter clamp + overflow guard)
// ---------------------------------------------------------------------------

// TestComputeBackoff_TableDriven exercises computeBackoff across multiple
// configs and attempt indices, asserting the result stays within [min, max]
// bounds. This covers the cap branch, the jitter branch, and the overflow
// guard — raising computeBackoff coverage above 40%.
func TestComputeBackoff_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		cfg     domain.BusyRetryConfig
		attempt int
		wantMin time.Duration
		wantMax time.Duration
	}{
		{
			name:    "attempt0_no_jitter_exact",
			cfg:     domain.BusyRetryConfig{BaseBackoff: 10 * time.Millisecond, MaxBackoff: 100 * time.Millisecond, JitterFactor: 0},
			attempt: 0,
			wantMin: 10 * time.Millisecond, // 10 * 2^0
			wantMax: 10 * time.Millisecond,
		},
		{
			name:    "attempt1_no_jitter_exact",
			cfg:     domain.BusyRetryConfig{BaseBackoff: 10 * time.Millisecond, MaxBackoff: 100 * time.Millisecond, JitterFactor: 0},
			attempt: 1,
			wantMin: 20 * time.Millisecond, // 10 * 2^1
			wantMax: 20 * time.Millisecond,
		},
		{
			name:    "attempt2_no_jitter_exact",
			cfg:     domain.BusyRetryConfig{BaseBackoff: 10 * time.Millisecond, MaxBackoff: 100 * time.Millisecond, JitterFactor: 0},
			attempt: 2,
			wantMin: 40 * time.Millisecond, // 10 * 2^2
			wantMax: 40 * time.Millisecond,
		},
		{
			name:    "capped_at_max_no_jitter",
			cfg:     domain.BusyRetryConfig{BaseBackoff: 100 * time.Millisecond, MaxBackoff: 100 * time.Millisecond, JitterFactor: 0},
			attempt: 5,
			wantMin: 100 * time.Millisecond, // 100 * 2^5 >> 100 → capped
			wantMax: 100 * time.Millisecond,
		},
		{
			name:    "overflow_guard_caps_to_max",
			cfg:     domain.BusyRetryConfig{BaseBackoff: 1 * time.Nanosecond, MaxBackoff: 50 * time.Millisecond, JitterFactor: 0},
			attempt: 63,                    // 1 << 63 sets the sign bit → negative int64 → overflow guard fires
			wantMin: 50 * time.Millisecond, // negative → capped to MaxBackoff
			wantMax: 50 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeBackoff(tt.cfg, tt.attempt)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("computeBackoff() = %v, want [%v, %v]", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

// TestComputeBackoff_JitterStaysWithinBounds runs many jittered iterations and
// asserts the result NEVER exceeds [base-delta, base+delta]. This exercises
// the jitter branch (including the negative-clamp defensive guard).
func TestComputeBackoff_JitterStaysWithinBounds(t *testing.T) {
	cfg := domain.BusyRetryConfig{
		BaseBackoff:  20 * time.Millisecond,
		MaxBackoff:   100 * time.Millisecond,
		JitterFactor: 0.3, // ±30%
	}
	attempt := 0
	// base = 20ms, delta = 6ms → range [14ms, 26ms]
	base := cfg.BaseBackoff << uint(attempt)
	delta := time.Duration(float64(base) * cfg.JitterFactor)
	wantMin := base - delta
	wantMax := base + delta

	for i := 0; i < 200; i++ {
		got := computeBackoff(cfg, attempt)
		if got < 0 {
			t.Fatalf("iteration %d: computeBackoff returned negative %v — jitter clamp failed", i, got)
		}
		if got < wantMin || got > wantMax {
			t.Errorf("iteration %d: computeBackoff = %v, want [%v, %v]", i, got, wantMin, wantMax)
		}
	}
}

// TestComputeBackoff_JitterNeverNegative proves the negative-clamp guard
// ensures backoff is never negative even under maximal jitter.
func TestComputeBackoff_JitterNeverNegative(t *testing.T) {
	cfg := domain.BusyRetryConfig{
		BaseBackoff:  1 * time.Millisecond,
		MaxBackoff:   100 * time.Millisecond,
		JitterFactor: 1.0, // ±100% — maximal jitter
	}
	for attempt := 0; attempt < 5; attempt++ {
		for i := 0; i < 100; i++ {
			got := computeBackoff(cfg, attempt)
			if got < 0 {
				t.Fatalf("attempt %d iter %d: computeBackoff = %v, must be >= 0", attempt, i, got)
			}
		}
	}
}

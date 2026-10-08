package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDefaultLimitPolicyProvidesIncreasingFailClosedTiers(t *testing.T) {
	policy := DefaultLimitPolicy()
	limited, err := policy.ForTier(TierLimited)
	if err != nil {
		t.Fatalf("limited tier: %v", err)
	}
	standard, err := policy.ForTier(TierStandard)
	if err != nil {
		t.Fatalf("standard tier: %v", err)
	}
	elevated, err := policy.ForTier(TierElevated)
	if err != nil {
		t.Fatalf("elevated tier: %v", err)
	}
	if limited.RequestsPerMinute >= standard.RequestsPerMinute || standard.RequestsPerMinute >= elevated.RequestsPerMinute {
		t.Fatalf("request budgets are not increasing: limited=%+v standard=%+v elevated=%+v", limited, standard, elevated)
	}
	if _, err := policy.ForTier("unknown"); !errors.Is(err, ErrUnknownLimitTier) {
		t.Fatalf("unknown tier error = %v, want ErrUnknownLimitTier", err)
	}
	for name, limits := range map[string]Limits{"limited": limited, "standard": standard, "elevated": elevated} {
		if limits.DefaultOutputTokens <= 0 || limits.DefaultOutputTokens > limits.MaxOutputTokens || limits.MaxOutputTokens > HardMaxOutputTokens {
			t.Fatalf("%s output limits are unsafe: %+v", name, limits)
		}
		if limits.JSONTimeout <= 0 || limits.StreamTimeout <= limits.JSONTimeout {
			t.Fatalf("%s deadlines are unsafe: %+v", name, limits)
		}
	}
}

func TestLimitPolicyWithJSONTimeoutOverridesEveryTierWithoutMutation(t *testing.T) {
	policy := DefaultLimitPolicy()
	updated := policy.WithJSONTimeout(90 * time.Second)
	for _, tier := range []LimitTier{TierLimited, TierStandard, TierElevated} {
		limits, err := updated.ForTier(tier)
		if err != nil {
			t.Fatalf("%s tier: %v", tier, err)
		}
		if limits.JSONTimeout != 90*time.Second {
			t.Fatalf("%s JSONTimeout = %s, want 90s", tier, limits.JSONTimeout)
		}
		if limits.StreamTimeout != 60*time.Second {
			t.Fatalf("%s StreamTimeout = %s, want untouched 60s", tier, limits.StreamTimeout)
		}
	}
	for _, tier := range []LimitTier{TierLimited, TierStandard, TierElevated} {
		limits, err := policy.ForTier(tier)
		if err != nil {
			t.Fatalf("receiver mutated for %s: %v", tier, err)
		}
		if limits.JSONTimeout != 30*time.Second {
			t.Fatalf("receiver %s JSONTimeout = %s, want untouched 30s", tier, limits.JSONTimeout)
		}
	}
	unchanged := DefaultLimitPolicy().WithJSONTimeout(0)
	limited, err := unchanged.ForTier(TierLimited)
	if err != nil {
		t.Fatalf("non-positive override must keep the policy valid: %v", err)
	}
	if limited.JSONTimeout != 30*time.Second {
		t.Fatalf("non-positive override changed JSONTimeout to %s", limited.JSONTimeout)
	}
}

func TestWithRequestTimeoutPropagatesCancellationAndKeepsEarlierDeadline(t *testing.T) {
	limits, _ := DefaultLimitPolicy().ForTier(TierStandard)
	parent, parentCancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer parentCancel()
	ctx, cancel := WithRequestTimeout(parent, limits, TransportStream)
	defer cancel()
	parentDeadline, _ := parent.Deadline()
	gotDeadline, _ := ctx.Deadline()
	if !gotDeadline.Equal(parentDeadline) {
		t.Fatalf("deadline = %v, want inherited %v", gotDeadline, parentDeadline)
	}
	parentCancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not reach request context")
	}
	if code := ContextErrorCode(ctx.Err()); code != ErrorRequestCancelled {
		t.Fatalf("cancel code = %q, want %q", code, ErrorRequestCancelled)
	}
}

func TestWithRequestTimeoutClassifiesDeadline(t *testing.T) {
	ctx, cancel := WithRequestTimeout(context.Background(), Limits{JSONTimeout: time.Nanosecond}, TransportJSON)
	defer cancel()
	<-ctx.Done()
	if code := ContextErrorCode(ctx.Err()); code != ErrorAgentTimeout {
		t.Fatalf("deadline code = %q, want %q", code, ErrorAgentTimeout)
	}
}

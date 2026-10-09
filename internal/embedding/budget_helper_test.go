package embedding

import "github.com/lleontor705/cortex/v2/internal/ratelimit"

// newTestBudget builds a wall-time-free shared budget limiter for the pacing
// tests. rpm 0 means unlimited.
func newTestBudget(rpm int, clock *budgetClock) *ratelimit.Limiter {
	return ratelimit.New(rpm, ratelimit.WithClock(clock.Now), ratelimit.WithSleep(clock.Sleep))
}

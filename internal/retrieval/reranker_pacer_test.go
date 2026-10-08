package retrieval

import (
	"sync"
	"testing"
	"time"
)

// pacerTestClock is a wall-time-free clock seam dedicated to the pacer tests.
// When advance is true, Sleep moves the clock forward by the slept duration
// (emulating sequential time); when false, time stays fixed so concurrent
// callers all reserve their slots from the same instant — the real-world
// behavior of overlapping sleeps that the production limiter relies on.
type pacerTestClock struct {
	mu      sync.Mutex
	now     time.Time
	sleeps  []time.Duration
	advance bool
}

func (c *pacerTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *pacerTestClock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	if c.advance {
		c.now = c.now.Add(d)
	}
}

func (c *pacerTestClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *pacerTestClock) recordedSleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

func newPacedTestPacer(clock *pacerTestClock) *rerankPacer {
	p := newRerankPacer()
	p.now = clock.Now
	p.sleep = clock.Sleep
	return p
}

// TestRerankPacerWithinBudgetDoesNotSleep pins the REQ-RET-107 behavior
// contract: a caller within budget proceeds without sleeping, and callers
// spaced at the provider rate never wait.
func TestRerankPacerWithinBudgetDoesNotSleep(t *testing.T) {
	clock := &pacerTestClock{now: time.Unix(1_700_000_000, 0), advance: true}
	p := newPacedTestPacer(clock)

	p.wait() // first call: the single initial token
	if sleeps := clock.recordedSleeps(); len(sleeps) != 0 {
		t.Fatalf("first wait must not sleep, got %v", sleeps)
	}

	// Advance exactly one provider-rate interval; the next call is within budget.
	clock.add(time.Second)
	p.wait()
	if sleeps := clock.recordedSleeps(); len(sleeps) != 0 {
		t.Fatalf("wait one gap later must not sleep, got %v", sleeps)
	}
}

// TestRerankPacerConcurrentWithinBudget fires N concurrent callers when the
// accrued budget covers them: none may sleep.
func TestRerankPacerConcurrentWithinBudget(t *testing.T) {
	clock := &pacerTestClock{now: time.Unix(1_700_000_000, 0), advance: false}
	p := newPacedTestPacer(clock)

	p.wait() // stamp the accrual epoch with the initial free token

	// Accrue 10 extra tokens of budget (10s idle at 1 token/s).
	clock.add(10 * time.Second)

	const n = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	proceeded := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.wait()
			mu.Lock()
			proceeded++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if proceeded != n {
		t.Fatalf("only %d/%d concurrent callers proceeded", proceeded, n)
	}
	if sleeps := clock.recordedSleeps(); len(sleeps) != 0 {
		t.Fatalf("callers within the accrued budget must not sleep, got %v", sleeps)
	}
}

// TestRerankPacerExcessCallersWait reserves refill slots: the first caller
// proceeds, and every excess caller sleeps exactly its own rate-spaced delay
// (1s, 2s, 3s, ...) — never a queued concatenation of other callers' gaps.
func TestRerankPacerExcessCallersWait(t *testing.T) {
	clock := &pacerTestClock{now: time.Unix(1_700_000_000, 0), advance: false}
	p := newPacedTestPacer(clock)

	const n = 5
	var wg sync.WaitGroup
	var mu sync.Mutex
	zeroDelay := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.wait()
			mu.Lock()
			defer mu.Unlock()
			if len(clock.recordedSleeps()) == 0 {
				zeroDelay++
			}
		}()
	}
	wg.Wait()

	sleeps := clock.recordedSleeps()
	if zeroDelay != 1 {
		t.Fatalf("expected exactly 1 caller within budget (0 delay), got %d", zeroDelay)
	}
	if len(sleeps) != n-1 {
		t.Fatalf("expected %d paced sleeps, got %v", n-1, sleeps)
	}
	for i, d := range sleeps {
		want := time.Duration(i+1) * time.Second
		if d != want {
			t.Fatalf("excess caller delay = %v, want %v (reservations must be rate-spaced, got %v)", d, want, sleeps)
		}
	}
}

// TestRerankPacerSustainedRateHammers the limiter with 120 sequential calls:
// every emission after the free token must be spaced by at least one
// provider-rate interval, keeping the shared key at or under 60 rpm.
func TestRerankPacerSustainedRateNotExceeded(t *testing.T) {
	clock := &pacerTestClock{now: time.Unix(1_700_000_000, 0), advance: true}
	p := newPacedTestPacer(clock)

	emissions := make([]time.Time, 0, 120)
	for i := 0; i < 120; i++ {
		p.wait()
		emissions = append(emissions, clock.Now())
	}
	for i := 1; i < len(emissions); i++ {
		if gap := emissions[i].Sub(emissions[i-1]); gap < time.Second {
			t.Fatalf("emission %d gap %v violates the 1s provider-rate spacing", i, gap)
		}
	}
}

// TestRerankPacerBurstCappedAtBudget verifies idle credit accrues at the
// provider rate and never exceeds the per-minute budget.
func TestRerankPacerBurstCappedAtBudget(t *testing.T) {
	clock := &pacerTestClock{now: time.Unix(1_700_000_000, 0), advance: true}
	p := newPacedTestPacer(clock)

	p.wait() // stamp the accrual epoch with the initial free token

	// Two minutes idle: credit must cap at the 60-request budget.
	clock.add(2 * time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < rerankRequestsPerMin; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.wait()
		}()
	}
	wg.Wait()
	if sleeps := clock.recordedSleeps(); len(sleeps) != 0 {
		t.Fatalf("budget-sized burst must proceed without sleep, got %v", sleeps)
	}

	// One more caller exceeds the budget and must wait for the next refill.
	p.wait()
	sleeps := clock.recordedSleeps()
	if len(sleeps) != 1 {
		t.Fatalf("expected exactly one paced sleep past the budget, got %v", sleeps)
	}
}

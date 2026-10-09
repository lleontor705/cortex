package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a deterministic manual-advance clock. Advancing is
// goroutine-safe so concurrent Wait callers observe monotonic fake time.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(0, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestUnlimitedRpmPassthrough(t *testing.T) {
	for _, rpm := range []int{0, -5} {
		l := New(rpm)
		// Unlimited: every Wait returns immediately with no bookkeeping, and
		// a zero time source is never consulted.
		for i := 0; i < 100; i++ {
			l.Wait()
		}
		if l.RPM() != rpm {
			t.Fatalf("rpm = %d, want %d", l.RPM(), rpm)
		}
		if !l.last.IsZero() {
			t.Fatal("unlimited limiter must not track bookkeeping state")
		}
	}
}

func TestUnlimitedRegistryReturnsNil(t *testing.T) {
	if r := NewRegistry(0); r != nil {
		t.Fatal("rpm 0 must build a nil (unlimited) registry")
	}
	var r *Registry
	if got := r.For("https://api.example.com/v1"); got != nil {
		t.Fatal("nil registry must hand out nil (unlimited) budgets")
	}
	if r.RPM() != 0 {
		t.Fatal("nil registry rpm must be 0")
	}
}

func TestWaitConsumesBurstThenPaces(t *testing.T) {
	const rpm = 60
	clock := newFakeClock()
	var sleeps atomic.Int64
	l := New(rpm, WithClock(clock.Now), WithSleep(func(d time.Duration) {
		sleeps.Add(1)
		clock.Advance(d)
	}))
	// First call is immediate (the seeded token).
	l.Wait()
	// Sustained synchronous load: each subsequent Wait sleeps exactly one
	// provider-rate interval (1s at 60 rpm) and advances the fake clock, so
	// N calls must consume N-1 refill tokens over ~N-1 seconds.
	for i := 0; i < 10; i++ {
		l.Wait()
	}
	if got := sleeps.Load(); got != 10 {
		t.Fatalf("sleeps = %d, want 10 (one per paced call)", got)
	}
	if elapsed := clock.Now().Sub(time.Unix(0, 0)); elapsed != 10*time.Second {
		t.Fatalf("elapsed fake time = %s, want 10s", elapsed)
	}
}

func TestConcurrentAcquisitionRespectsBudget(t *testing.T) {
	const rpm = 60
	clock := newFakeClock()
	l := New(rpm, WithClock(clock.Now), WithSleep(func(d time.Duration) {
		clock.Advance(d)
	}))
	// 120 concurrent callers against a fresh 60-rpm bucket: the bucket can
	// never emit faster than one token per second, so exhausting 120 waits
	// deterministically advances fake time by at least 120s - the 60-token
	// initial burst minus the seeded token, rounded by refill spacing.
	const callers = 120
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			l.Wait()
		}()
	}
	close(start)
	wg.Wait()
	elapsed := clock.Now().Sub(time.Unix(0, 0))
	// The initial burst (rpm tokens) is free; the remaining callers pace at
	// the refill rate. Allow a small float-drift tolerance but require the
	// provider rate to be respected: elapsed >= (callers - rpm - 1) seconds.
	minElapsed := time.Duration(callers-rpm-1) * time.Second
	if elapsed < minElapsed {
		t.Fatalf("120 concurrent calls completed in %s; provider rate violated (minimum %s)", elapsed, minElapsed)
	}
	// And the pacing is bounded: no caller may be pushed more than one full
	// minute beyond the required spacing.
	if elapsed > 2*minElapsed+time.Minute {
		t.Fatalf("120 concurrent calls took %s; excessive serialization beyond the refill spacing", elapsed)
	}
}

func TestBurstCapNeverExceedsBudget(t *testing.T) {
	const rpm = 60
	clock := newFakeClock()
	l := New(rpm, WithClock(clock.Now), WithSleep(func(d time.Duration) { clock.Advance(d) }))
	// Idle far beyond one minute: the accrued balance is capped at the
	// per-minute budget, so rpm+1 calls consume the full capped burst (seeded
	// token included) and exactly rpm of them must pace at one provider-rate
	// interval each — never a larger free burst.
	clock.Advance(10 * time.Minute)
	start := clock.Now()
	for i := 0; i <= rpm; i++ {
		l.Wait()
	}
	if elapsed := clock.Now().Sub(start); elapsed != time.Duration(rpm)*time.Second {
		t.Fatalf("elapsed = %s, want exactly %s (capped burst + refill spacing)", elapsed, time.Duration(rpm)*time.Second)
	}
}

func TestRegistrySharesBudgetAcrossSubsystems(t *testing.T) {
	const rpm = 60
	clock := newFakeClock()
	reg := NewRegistry(rpm, WithClock(clock.Now), WithSleep(func(d time.Duration) { clock.Advance(d) }))
	a := reg.For("https://Provider.example/v1/")
	b := reg.For("https://provider.example/v1")
	c := reg.For("https://other.example/v1")
	if a == nil || b == nil || c == nil {
		t.Fatal("registry must hand out budgets for every base URL")
	}
	if a != b {
		t.Fatal("case/trailing-slash variants of the same provider must share one budget")
	}
	if a == c {
		t.Fatal("distinct providers must not share a budget")
	}
	if reg.RPM() != rpm {
		t.Fatalf("registry rpm = %d, want %d", reg.RPM(), rpm)
	}
	// The shared budget paces exactly like a standalone limiter: consuming
	// the burst from the reranker path is visible to the embedding path.
	for i := 0; i < rpm+1; i++ {
		a.Wait()
	}
	if elapsed := clock.Now().Sub(time.Unix(0, 0)); elapsed < time.Second {
		t.Fatalf("shared budget exhausted in %s; refill pacing not enforced", elapsed)
	}
}

func TestNormalizeProviderKey(t *testing.T) {
	cases := []struct{ a, b string }{
		{"https://api.nan.builders/v1", "https://API.NAN.builders/v1/"},
		{"https://api.nan.builders/v1", " https://api.nan.builders/v1 "},
		{"not a url", "not a url"},
		{"http://localhost:11434", "http://LOCALHOST:11434"},
	}
	for _, tc := range cases {
		if NormalizeProviderKey(tc.a) != NormalizeProviderKey(tc.b) {
			t.Fatalf("keys for %q and %q must match", tc.a, tc.b)
		}
	}
	if NormalizeProviderKey("https://a.example/v1") == NormalizeProviderKey("https://b.example/v1") {
		t.Fatal("distinct hosts must not collapse to one key")
	}
}

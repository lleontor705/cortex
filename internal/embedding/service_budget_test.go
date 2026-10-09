package embedding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// budgetClock is a wall-time-free clock seam: Sleep records the requested
// delay and advances the fake clock by it, so a paced caller never waits on
// real time.
type budgetClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *budgetClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *budgetClock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
}

func (c *budgetClock) recordedSleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

// TestSecureEmbeddingPacesOnSharedBudget pins the shared provider budget
// contract for the keyed embedding backends: with a 60-rpm shared bucket the
// first Embed proceeds on the seeded token and the second must wait exactly
// one provider-rate interval instead of oversubscribing the shared key.
func TestSecureEmbeddingPacesOnSharedBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`))
	}))
	defer srv.Close()

	policy := OutboundPolicy{AllowLoopback: true, AllowInsecureLoopbackHTTP: true}
	if err := policy.ApproveDestination(srv.URL); err != nil {
		t.Fatal(err)
	}
	clock := &budgetClock{now: time.Unix(1_700_000_000, 0)}
	budget := newTestBudget(60, clock)
	svc, err := NewSecure(Config{Provider: "openai-compatible", APIKey: "test-key", BaseURL: srv.URL, Model: "m", Budget: budget}, policy)
	if err != nil {
		t.Fatalf("NewSecure: %v", err)
	}
	ctx := context.Background()
	if _, err := svc.Embed(ctx, "first"); err != nil {
		t.Fatalf("first Embed: %v", err)
	}
	if got := clock.recordedSleeps(); len(got) != 0 {
		t.Fatalf("first Embed must ride the seeded token, sleeps = %v", got)
	}
	if _, err := svc.Embed(ctx, "second"); err != nil {
		t.Fatalf("second Embed: %v", err)
	}
	sleeps := clock.recordedSleeps()
	if len(sleeps) != 1 || sleeps[0] != time.Second {
		t.Fatalf("second Embed sleeps = %v, want exactly one 1s provider-rate gap", sleeps)
	}
}

// TestEmbeddingNilBudgetUnlimited pins the default: a nil shared budget keeps
// the historical unlimited behavior (no sleeps, no bookkeeping).
func TestEmbeddingNilBudgetUnlimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`))
	}))
	defer srv.Close()

	policy := OutboundPolicy{AllowLoopback: true, AllowInsecureLoopbackHTTP: true}
	if err := policy.ApproveDestination(srv.URL); err != nil {
		t.Fatal(err)
	}
	clock := &budgetClock{now: time.Unix(1_700_000_000, 0)}
	svc, err := NewSecure(Config{Provider: "openai-compatible", APIKey: "test-key", BaseURL: srv.URL, Model: "m", Budget: newTestBudget(0, clock)}, policy)
	if err != nil {
		t.Fatalf("NewSecure: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := svc.Embed(context.Background(), "text"); err != nil {
			t.Fatalf("Embed %d: %v", i, err)
		}
	}
	if got := clock.recordedSleeps(); len(got) != 0 {
		t.Fatalf("unlimited budget must never sleep, sleeps = %v", got)
	}
}

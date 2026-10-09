package server

import (
	"context"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
	"github.com/lleontor705/cortex/v2/internal/ratelimit"
)

// budgetClock is a wall-time-free clock seam: Sleep records the delay and
// advances the fake clock, so paced callers never wait on real time.
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

// TestConfiguredChatProviderPacesOnSharedBudget pins the chat side of the
// shared provider budget: with a 60-rpm registry the second completion must
// wait one provider-rate interval instead of oversubscribing the shared key.
func TestConfiguredChatProviderPacesOnSharedBudget(t *testing.T) {
	var hits int
	var mu sync.Mutex
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"claims\":[{\"text\":\"ok\",\"citation_handles\":[\"h\"]}]}"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer provider.Close()

	pool := x509.NewCertPool()
	pool.AddCert(provider.Certificate())
	clock := &budgetClock{now: time.Unix(1_700_000_000, 0)}
	budgets := ratelimit.NewRegistry(60, ratelimit.WithClock(clock.Now), ratelimit.WithSleep(clock.Sleep))
	completion, err := newConfiguredChatProvider(config.ServerLLMConfig{
		Provider: "generic", BaseURL: provider.URL + "/v1", APIKey: "k", Model: "m",
		AllowLoopback: true, MaxConcurrent: 1, MaxRedirects: 1, MaxResponseBodyBytes: 4096,
		MaxErrorBodyBytes: 1024, CACertPool: pool,
	}, budgets)
	if err != nil {
		t.Fatalf("newConfiguredChatProvider() = %v", err)
	}
	ctx := context.Background()
	req := agentdomain.CompletionRequest{SystemPrompt: "p", UserPrompt: "u"}
	if _, err := completion.Complete(ctx, req); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if got := clock.recordedSleeps(); len(got) != 0 {
		t.Fatalf("first Complete must ride the seeded token, sleeps = %v", got)
	}
	if _, err := completion.Complete(ctx, req); err != nil {
		t.Fatalf("second Complete: %v", err)
	}
	sleeps := clock.recordedSleeps()
	if len(sleeps) != 1 || sleeps[0] != time.Second {
		t.Fatalf("second Complete sleeps = %v, want exactly one 1s provider-rate gap", sleeps)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 2 {
		t.Fatalf("provider hits = %d, want 2", hits)
	}
}

// TestProviderRateBudgetDisabledByDefault pins the composition default: with
// CORTEX_PROVIDER_RPM unset the registry is nil and the chat provider never
// paces (preserving current behavior).
func TestProviderRateBudgetDisabledByDefault(t *testing.T) {
	t.Setenv("CORTEX_PROVIDER_RPM", "")
	rpm, err := config.ProviderRPMFromEnv()
	if err != nil || rpm != 0 {
		t.Fatalf("ProviderRPMFromEnv() = %d, %v; want 0, nil", rpm, err)
	}
	if budgets := ratelimit.NewRegistry(rpm); budgets != nil {
		t.Fatal("rpm 0 must compose a nil (unlimited) registry")
	}
	var budgets *ratelimit.Registry
	if got := budgets.For("https://provider.example/v1"); got != nil {
		t.Fatal("nil registry must hand out nil (unlimited) budgets")
	}
}

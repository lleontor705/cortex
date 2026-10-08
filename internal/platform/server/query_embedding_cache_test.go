package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// countingEmbeddingService is a fake embedding.Service that records how many
// provider round-trips each query caused.
type countingEmbeddingService struct {
	mu    sync.Mutex
	calls map[string]int
	vec   []float32
	err   error
}

func (f *countingEmbeddingService) Embed(_ context.Context, text string) ([]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[text]++
	if f.err != nil {
		return nil, f.err
	}
	return f.vec, nil
}

func (f *countingEmbeddingService) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := f.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		out[i] = vec
	}
	return out, nil
}

func (f *countingEmbeddingService) Dimensions() int { return len(f.vec) }

func (f *countingEmbeddingService) Model() string { return "fake-embedding-model" }

func (f *countingEmbeddingService) callsFor(t *testing.T, text string) int {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[text]
}

func newCountingEmbeddingService() *countingEmbeddingService {
	return &countingEmbeddingService{calls: map[string]int{}, vec: []float32{0.1, 0.2, 0.3}}
}

func TestQueryEmbeddingCacheHitAfterMissSkipsProviderRoundTrip(t *testing.T) {
	svc := newCountingEmbeddingService()
	cache := newQueryEmbeddingCache(8, time.Minute)

	first, err := cache.embed(context.Background(), svc, "qwen3-embedding", "How does hybrid search work?")
	if err != nil {
		t.Fatalf("first embed = %v", err)
	}
	second, err := cache.embed(context.Background(), svc, "qwen3-embedding", "How does hybrid search work?")
	if err != nil {
		t.Fatalf("second embed = %v", err)
	}
	if svc.callsFor(t, "How does hybrid search work?") != 1 {
		t.Fatalf("provider called %d times, want exactly one round-trip", svc.callsFor(t, "How does hybrid search work?"))
	}
	if first[0] != second[0] {
		t.Fatal("cached vector differs from the first embedded vector")
	}
	if cache.misses.Load() != 1 || cache.hits.Load() != 1 {
		t.Fatalf("counters hits=%d misses=%d, want 1/1", cache.hits.Load(), cache.misses.Load())
	}
}

func TestQueryEmbeddingCacheKeysOnNormalizedTextAndModel(t *testing.T) {
	svc := newCountingEmbeddingService()
	cache := newQueryEmbeddingCache(8, time.Minute)

	// Same query modulo case/leading-trailing whitespace is a hit.
	if _, err := cache.embed(context.Background(), svc, "m", "Find  Symbols"); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if _, err := cache.embed(context.Background(), svc, "m", "  find symbols  "); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if got := svc.callsFor(t, "Find  Symbols"); got != 1 {
		t.Fatalf("provider calls after normalized repeat = %d, want 1", got)
	}
	if cache.hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", cache.hits.Load())
	}
	// A different model id must not reuse the vector.
	if _, err := cache.embed(context.Background(), svc, "other-model", "find symbols"); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if got := svc.callsFor(t, "find symbols"); got != 1 {
		t.Fatalf("different model must re-embed; provider calls = %d", got)
	}
	if cache.misses.Load() != 2 {
		t.Fatalf("misses = %d, want 2 (first embed + other model)", cache.misses.Load())
	}
}

func TestQueryEmbeddingCacheTTLExpiryReEmbeds(t *testing.T) {
	svc := newCountingEmbeddingService()
	cache := newQueryEmbeddingCache(8, 30*time.Millisecond)
	if _, err := cache.embed(context.Background(), svc, "m", "ttl query"); err != nil {
		t.Fatalf("embed: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := cache.embed(context.Background(), svc, "m", "ttl query"); err != nil {
		t.Fatalf("embed after ttl: %v", err)
	}
	if got := svc.callsFor(t, "ttl query"); got != 2 {
		t.Fatalf("provider calls after TTL expiry = %d, want 2", got)
	}
}

func TestQueryEmbeddingCacheLRUEvictionReEmbeds(t *testing.T) {
	svc := newCountingEmbeddingService()
	cache := newQueryEmbeddingCache(1, time.Minute)
	if _, err := cache.embed(context.Background(), svc, "m", "first"); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if _, err := cache.embed(context.Background(), svc, "m", "second"); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if _, err := cache.embed(context.Background(), svc, "m", "first"); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if got := svc.callsFor(t, "first"); got != 2 {
		t.Fatalf("provider calls for evicted entry = %d, want 2", got)
	}
	if cache.cache.Len() != 1 {
		t.Fatalf("cache len = %d, want bounded to capacity 1", cache.cache.Len())
	}
}

func TestQueryEmbeddingCacheNeverCachesProviderErrors(t *testing.T) {
	svc := newCountingEmbeddingService()
	svc.err = errors.New("provider unavailable")
	cache := newQueryEmbeddingCache(8, time.Minute)
	if _, err := cache.embed(context.Background(), svc, "m", "failing query"); err == nil {
		t.Fatal("provider error must propagate")
	}
	svc.err = nil
	if _, err := cache.embed(context.Background(), svc, "m", "failing query"); err != nil {
		t.Fatalf("embed after error cleared = %v", err)
	}
	if got := svc.callsFor(t, "failing query"); got != 2 {
		t.Fatalf("provider calls = %d, want 2 (errors must not be cached)", got)
	}
}

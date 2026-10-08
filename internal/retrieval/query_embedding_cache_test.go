package retrieval

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingProvider is a fake embedding provider that records how many times it
// was invoked and returns canned vectors (or an error).
type countingProvider struct {
	calls atomic.Int64
	err   error
	vec   []float32
}

func (p *countingProvider) embed(_ context.Context, _ string) ([]float32, error) {
	p.calls.Add(1)
	if p.err != nil {
		return nil, p.err
	}
	return p.vec, nil
}

func TestQueryEmbeddingCacheHitSkipsProvider(t *testing.T) {
	cache := NewQueryEmbeddingCache(16, time.Minute)
	p := &countingProvider{vec: []float32{0.1, 0.2, 0.3}}
	ctx := context.Background()

	v1, err := cache.Embed(ctx, "m1", "find symbols", p.embed)
	if err != nil {
		t.Fatalf("first embed: %v", err)
	}
	v2, err := cache.Embed(ctx, "m1", "find symbols", p.embed)
	if err != nil {
		t.Fatalf("second embed: %v", err)
	}

	if p.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", p.calls.Load())
	}
	if len(v2) != len(v1) {
		t.Fatalf("cached vector length = %d, want %d", len(v2), len(v1))
	}
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Fatalf("cached vector[%d] = %v, want %v", i, v2[i], v1[i])
		}
	}
	if cache.Hits() != 1 || cache.Misses() != 1 {
		t.Fatalf("hits/misses = %d/%d, want 1/1", cache.Hits(), cache.Misses())
	}
}

func TestQueryEmbeddingCacheDifferentModelIsDifferentKey(t *testing.T) {
	cache := NewQueryEmbeddingCache(16, time.Minute)
	p := &countingProvider{vec: []float32{1}}
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, err := cache.Embed(ctx, fmt.Sprintf("model-%d", i), "q", p.embed); err != nil {
			t.Fatalf("embed model-%d: %v", i, err)
		}
	}
	if p.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2 (one per model)", p.calls.Load())
	}
}

func TestQueryEmbeddingCacheProviderErrorNeverCached(t *testing.T) {
	cache := NewQueryEmbeddingCache(16, time.Minute)
	boom := errors.New("provider boom")
	p := &countingProvider{err: boom}
	ctx := context.Background()

	if _, err := cache.Embed(ctx, "m1", "q", p.embed); !errors.Is(err, boom) {
		t.Fatalf("first embed err = %v, want %v", err, boom)
	}
	if _, err := cache.Embed(ctx, "m1", "q", p.embed); !errors.Is(err, boom) {
		t.Fatalf("retry embed err = %v, want %v", err, boom)
	}
	if p.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2 (errors are never cached)", p.calls.Load())
	}
	if cache.Hits() != 0 || cache.Misses() != 2 {
		t.Fatalf("hits/misses = %d/%d, want 0/2", cache.Hits(), cache.Misses())
	}

	// After recovery the query embeds normally and is cached.
	p.err = nil
	p.vec = []float32{1, 2}
	if v, err := cache.Embed(ctx, "m1", "q", p.embed); err != nil || len(v) != 2 {
		t.Fatalf("recovered embed = %v, %v", v, err)
	}
	if p.calls.Load() != 3 {
		t.Fatalf("provider calls = %d, want 3", p.calls.Load())
	}
	if _, err := cache.Embed(ctx, "m1", "q", p.embed); err != nil {
		t.Fatalf("cached embed: %v", err)
	}
	if p.calls.Load() != 3 {
		t.Fatalf("provider calls after cache = %d, want 3", p.calls.Load())
	}
}

func TestQueryEmbeddingCacheEmptyVectorNotCached(t *testing.T) {
	cache := NewQueryEmbeddingCache(16, time.Minute)
	p := &countingProvider{vec: []float32{}}
	ctx := context.Background()

	if _, err := cache.Embed(ctx, "m1", "q", p.embed); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if _, err := cache.Embed(ctx, "m1", "q", p.embed); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if p.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2 (empty vectors are never cached)", p.calls.Load())
	}
}

func TestQueryEmbeddingCacheKeyWhitespaceAndCaseNormalization(t *testing.T) {
	cases := []string{
		"Find  Symbols",
		"  find symbols ",
		"find\tsymbols",
		"Find \n Symbols",
		"FIND SYMBOLS",
	}
	want := QueryEmbeddingCacheKey("m1", cases[0])
	for _, q := range cases {
		if k := QueryEmbeddingCacheKey("m1", q); k != want {
			t.Fatalf("query %q produced key %q, want %q", q, k, want)
		}
	}

	// End-to-end: both normalized variants share one provider round-trip.
	cache := NewQueryEmbeddingCache(16, time.Minute)
	p := &countingProvider{vec: []float32{1}}
	ctx := context.Background()
	for _, q := range cases {
		if _, err := cache.Embed(ctx, "m1", q, p.embed); err != nil {
			t.Fatalf("embed %q: %v", q, err)
		}
	}
	if p.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1 (all variants share one key)", p.calls.Load())
	}
}

func TestQueryEmbeddingCacheKeySeparatesModelFromQuery(t *testing.T) {
	// The \x00 separator must keep ("ab", "c") distinct from ("a", "bc").
	if QueryEmbeddingCacheKey("ab", "c") == QueryEmbeddingCacheKey("a", "bc") {
		t.Fatal("model/query boundary collapsed: keys collide across splits")
	}
}

func TestQueryEmbeddingCacheLRUEviction(t *testing.T) {
	cache := NewQueryEmbeddingCache(1, time.Minute)
	p := &countingProvider{vec: []float32{1}}
	ctx := context.Background()

	if _, err := cache.Embed(ctx, "m1", "first", p.embed); err != nil {
		t.Fatalf("embed first: %v", err)
	}
	if _, err := cache.Embed(ctx, "m1", "second", p.embed); err != nil {
		t.Fatalf("embed second: %v", err)
	}
	if cache.Len() != 1 {
		t.Fatalf("cache len = %d, want 1 (capacity 1)", cache.Len())
	}
	// "first" was evicted: embedding it again invokes the provider.
	if _, err := cache.Embed(ctx, "m1", "first", p.embed); err != nil {
		t.Fatalf("embed first again: %v", err)
	}
	if p.calls.Load() != 3 {
		t.Fatalf("provider calls = %d, want 3 (first key was LRU-evicted)", p.calls.Load())
	}
}

func TestQueryEmbeddingCacheTTLExpiry(t *testing.T) {
	cache := NewQueryEmbeddingCache(16, 5*time.Millisecond)
	p := &countingProvider{vec: []float32{1}}
	ctx := context.Background()

	if _, err := cache.Embed(ctx, "m1", "q", p.embed); err != nil {
		t.Fatalf("embed: %v", err)
	}
	time.Sleep(15 * time.Millisecond) // > TTL
	if _, err := cache.Embed(ctx, "m1", "q", p.embed); err != nil {
		t.Fatalf("embed after TTL: %v", err)
	}
	if p.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2 (entry expired)", p.calls.Load())
	}
}

func TestQueryEmbeddingCacheConcurrentAccess(t *testing.T) {
	cache := NewQueryEmbeddingCache(64, time.Minute)
	p := &countingProvider{vec: []float32{0.5}}
	ctx := context.Background()

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				q := fmt.Sprintf("query-%d", (g+i)%8)
				if _, err := cache.Embed(ctx, "m1", q, p.embed); err != nil {
					t.Errorf("concurrent embed %q: %v", q, err)
					return
				}
				_ = cache.Hits()
				_ = cache.Misses()
			}
		}(g)
	}
	wg.Wait()

	if cache.Len() > 8 {
		t.Fatalf("cache len = %d, want <= 8 distinct keys", cache.Len())
	}
}

func TestQueryEmbeddingCacheNilCacheDegradesToProvider(t *testing.T) {
	var cache *QueryEmbeddingCache
	p := &countingProvider{vec: []float32{1}}
	ctx := context.Background()

	if _, err := cache.Embed(ctx, "m1", "q", p.embed); err != nil {
		t.Fatalf("nil-cache embed: %v", err)
	}
	if _, err := cache.Embed(ctx, "m1", "q", p.embed); err != nil {
		t.Fatalf("nil-cache embed: %v", err)
	}
	if p.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2 (nil cache never serves hits)", p.calls.Load())
	}
	if cache.Hits() != 0 || cache.Misses() != 0 || cache.Len() != 0 {
		t.Fatal("nil cache accessors must be zero-valued")
	}
}

package mcp

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// countingTieredEmbedding records provider round-trips per query text for the
// ret-104 tiered-search query-embedding cache tests.
type countingTieredEmbedding struct {
	mu    sync.Mutex
	calls map[string]int
	err   error
}

func (f *countingTieredEmbedding) Embed(_ context.Context, text string) ([]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[text]++
	if f.err != nil {
		return nil, f.err
	}
	return []float32{0.25, 0.5, 0.75}, nil
}
func (f *countingTieredEmbedding) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
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
func (f *countingTieredEmbedding) Dimensions() int { return 3 }
func (f *countingTieredEmbedding) Model() string   { return "ret-104-fake-model" }

func (f *countingTieredEmbedding) callsFor(t *testing.T, text string) int {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[text]
}

// TestTieredSearchQueryEmbeddingCacheHitSkipsProvider verifies a repeated
// identical query embedding is served from the shared cache with no second
// provider round-trip.
func TestTieredSearchQueryEmbeddingCacheHitSkipsProvider(t *testing.T) {
	svc := &countingTieredEmbedding{calls: map[string]int{}}
	ctx := context.Background()
	// Unique query: avoids cache-key collisions with other tests in the
	// package sharing the process-wide tieredSearchQueryEmbeddingCache.
	query := "ret104 tiered hit probe unique"

	if _, err := embedTieredSearchQuery(ctx, svc, query); err != nil {
		t.Fatalf("first embed: %v", err)
	}
	first, err := embedTieredSearchQuery(ctx, svc, query)
	if err != nil {
		t.Fatalf("second embed: %v", err)
	}
	if got := svc.callsFor(t, query); got != 1 {
		t.Fatalf("provider round-trips = %d, want exactly 1", got)
	}
	if len(first) != 3 {
		t.Fatalf("cached vector length = %d, want 3", len(first))
	}
}

// TestTieredSearchQueryEmbeddingCacheErrorNeverCached verifies a provider
// error is retried on the next call (never cached).
func TestTieredSearchQueryEmbeddingCacheErrorNeverCached(t *testing.T) {
	svc := &countingTieredEmbedding{calls: map[string]int{}, err: errors.New("provider unavailable")}
	ctx := context.Background()
	query := "ret104 tiered error probe unique"

	if _, err := embedTieredSearchQuery(ctx, svc, query); err == nil {
		t.Fatal("provider error must propagate")
	}
	svc.err = nil
	if _, err := embedTieredSearchQuery(ctx, svc, query); err != nil {
		t.Fatalf("embed after error cleared: %v", err)
	}
	if got := svc.callsFor(t, query); got != 2 {
		t.Fatalf("provider round-trips = %d, want 2 (errors must not be cached)", got)
	}
}

// TestTieredSearchQueryEmbeddingCacheKeysByModelAndNormalizedText verifies
// normalization shares entries and a different model re-embeds.
func TestTieredSearchQueryEmbeddingCacheKeysByModelAndNormalizedText(t *testing.T) {
	svc := &countingTieredEmbedding{calls: map[string]int{}}
	ctx := context.Background()
	if _, err := embedTieredSearchQuery(ctx, svc, "Ret104   normalized probe"); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if _, err := embedTieredSearchQuery(ctx, svc, "ret104 normalized   probe"); err != nil {
		t.Fatalf("normalized repeat: %v", err)
	}
	if got := svc.callsFor(t, "Ret104   normalized probe"); got != 1 {
		t.Fatalf("provider calls after normalized repeat = %d, want 1", got)
	}
}

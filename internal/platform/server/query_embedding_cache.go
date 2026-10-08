package server

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lleontor705/cortex/v2/internal/embedding"
	"github.com/lleontor705/cortex/v2/internal/retrieval"
)

// A query embedding is a pure function of the query text and the embedding
// model that produced it. It NEVER depends on the corpus, tenant, workspace,
// project, or any search filter: those only decide what is searched *with*
// the vector, not the vector itself. Caching a query embedding and reusing it
// across searches (including searches scoped to different projects) is
// therefore semantically safe, and a cache hit still runs the full lexical
// fusion + rerank pipeline afterwards.
//
// Cache key: embedding model id + normalized (lowercased, whitespace-collapsed)
// query text. Bounded by an LRU cache with TTL so memory stays capped even
// under arbitrary query cardinality.
const (
	queryEmbeddingCacheCapacity = 128
	queryEmbeddingCacheTTL      = 10 * time.Minute
)

// queryEmbeddingCache caches hybrid-search query embeddings. Concurrency-safe
// via the underlying ScopedCache; hits/misses are exported through atomic
// counters for observability without changing the response shape.
type queryEmbeddingCache struct {
	cache  *retrieval.ScopedCache[[]float32]
	hits   atomic.Int64
	misses atomic.Int64
}

func newQueryEmbeddingCache(capacity int, ttl time.Duration) *queryEmbeddingCache {
	return &queryEmbeddingCache{cache: retrieval.NewScopedCache[[]float32](capacity, ttl)}
}

// hybridQueryEmbeddingCache is the process-wide cache shared by hybrid search
// handlers; identical repeated queries skip the provider round-trip.
var hybridQueryEmbeddingCache = newQueryEmbeddingCache(queryEmbeddingCacheCapacity, queryEmbeddingCacheTTL)

func queryEmbeddingCacheKey(model, query string) string {
	// Normalization collapses all whitespace runs to single spaces and
	// case-folds, so "Find  Symbols" and "  find symbols " share one entry.
	return model + "\x00" + strings.ToLower(strings.Join(strings.Fields(query), " "))
}

// embed returns the cached vector for (model, query) or performs one provider
// round-trip and caches the result. Provider errors are never cached.
func (c *queryEmbeddingCache) embed(ctx context.Context, svc embedding.Service, model, query string) ([]float32, error) {
	if c == nil || c.cache == nil || svc == nil {
		return svc.Embed(ctx, query)
	}
	key := queryEmbeddingCacheKey(model, query)
	if vector, ok := c.cache.Get(key); ok {
		c.hits.Add(1)
		return vector, nil
	}
	c.misses.Add(1)
	vector, err := svc.Embed(ctx, query)
	if err != nil || len(vector) == 0 {
		return vector, err
	}
	c.cache.Set(key, vector)
	return vector, nil
}

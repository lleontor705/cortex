package server

import (
	"context"
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
const (
	queryEmbeddingCacheCapacity = 128
	queryEmbeddingCacheTTL      = 10 * time.Minute
)

// queryEmbeddingCache caches hybrid-search query embeddings by delegating to
// the shared retrieval.QueryEmbeddingCache helper (ret-101 / REQ-RET-101):
// the same key normalization (embedding model id + lowercased,
// whitespace-collapsed query), LRU+TTL bounding, and never-cached provider
// errors. The wrapper keeps its own hits/misses counters so the existing
// server-side observability surface (and tests) stay stable while the shared
// helper owns storage and key semantics.
type queryEmbeddingCache struct {
	cache  *retrieval.QueryEmbeddingCache
	hits   atomic.Int64
	misses atomic.Int64
}

func newQueryEmbeddingCache(capacity int, ttl time.Duration) *queryEmbeddingCache {
	return &queryEmbeddingCache{cache: retrieval.NewQueryEmbeddingCache(capacity, ttl)}
}

// hybridQueryEmbeddingCache is the process-wide cache shared by hybrid search
// handlers; identical repeated queries skip the provider round-trip.
var hybridQueryEmbeddingCache = newQueryEmbeddingCache(queryEmbeddingCacheCapacity, queryEmbeddingCacheTTL)

// embed returns the cached vector for (model, query) or performs one provider
// round-trip and caches the result. Provider errors are never cached.
func (c *queryEmbeddingCache) embed(ctx context.Context, svc embedding.Service, model, query string) ([]float32, error) {
	if c == nil || c.cache == nil || svc == nil {
		return svc.Embed(ctx, query)
	}
	// Mirror the shared helper's hit/miss outcome: a provider round-trip
	// always advances the helper's miss counter, so an unchanged miss count
	// means the vector was served from cache.
	missesBefore := c.cache.Misses()
	vector, err := c.cache.Embed(ctx, model, query, svc.Embed)
	if c.cache.Misses() == missesBefore {
		c.hits.Add(1)
		prometheusMetrics.queryEmbeddingCacheHits.Inc()
	} else {
		c.misses.Add(1)
		prometheusMetrics.queryEmbeddingCacheMisses.Inc()
		// A miss IS one provider round-trip on this path: count the request,
		// and classify the outcome for the provider error surface.
		prometheusMetrics.embeddingProviderRequests.Inc()
		if err != nil {
			prometheusMetrics.embeddingProviderErrors.Inc()
		}
	}
	return vector, err
}

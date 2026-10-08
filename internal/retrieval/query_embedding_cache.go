package retrieval

import (
	"context"
	"strings"
	"sync/atomic"
	"time"
)

// QueryEmbeddingCache is the ONE shared mechanism for caching query embeddings
// across search surfaces (REQ-RET-101). It wraps ScopedCache[[]float32] with
// the standard query-embedding keying: embedding model id + normalized query
// text.
//
// A query embedding is a pure function of the query text and the embedding
// model that produced it. It NEVER depends on the corpus, tenant, workspace,
// project, or any search filter: those only decide what is searched *with*
// the vector, not the vector itself. Caching a query embedding and reusing it
// across searches (including searches scoped to different projects) is
// therefore semantically safe.
//
// Contract:
//   - Keyed by model id + normalized query text (QueryEmbeddingCacheKey):
//     lowercased, whitespace-collapsed via strings.Fields join — matching the
//     PR #128 hybrid-search normalization — so "Find  Symbols" and
//     "  find symbols " share one entry.
//   - Provider errors are NEVER cached: a failed embed is a miss and the
//     provider is invoked again on retry. Empty vectors are likewise not
//     cached.
//   - TTL + LRU eviction are inherited from ScopedCache, so memory stays
//     bounded under arbitrary query cardinality.
//   - Hits and misses are exported through atomic counters for observability
//     without changing any response shape.
//   - Concurrency-safe via the underlying ScopedCache lock.
//
// Reindex/document embedding paths MUST NOT use this cache (REQ-RET-101):
// document embeddings depend on mutable observation content, not just the text
// passed to the embedder.
type QueryEmbeddingCache struct {
	cache  *ScopedCache[[]float32]
	hits   atomic.Int64
	misses atomic.Int64
}

// NewQueryEmbeddingCache creates a QueryEmbeddingCache with the given capacity
// and TTL. Non-positive values fall back to the ScopedCache defaults
// (capacity 256, TTL 5m). A nil cache is safe to use: Embed degrades to a
// direct provider call.
func NewQueryEmbeddingCache(capacity int, ttl time.Duration) *QueryEmbeddingCache {
	return &QueryEmbeddingCache{cache: NewScopedCache[[]float32](capacity, ttl)}
}

// QueryEmbeddingCacheKey builds the cache key for a query embedding: the
// embedding model id + normalized query text. Normalization collapses all
// whitespace runs to single spaces and case-folds, so queries differing only
// by whitespace/case share one entry. The \x00 separator keeps model/query
// boundaries unambiguous.
func QueryEmbeddingCacheKey(model, query string) string {
	return model + "\x00" + strings.ToLower(strings.Join(strings.Fields(query), " "))
}

// Embed returns the cached vector for (model, query) or performs one provider
// round-trip through embed and caches the result. Provider errors and empty
// vectors are never cached. The embed function receives the ORIGINAL (not
// normalized) query text so providers see exactly what the caller intended.
func (c *QueryEmbeddingCache) Embed(ctx context.Context, model, query string, embed func(ctx context.Context, query string) ([]float32, error)) ([]float32, error) {
	if c == nil || c.cache == nil {
		return embed(ctx, query)
	}
	key := QueryEmbeddingCacheKey(model, query)
	if vector, ok := c.cache.Get(key); ok {
		c.hits.Add(1)
		return vector, nil
	}
	c.misses.Add(1)
	vector, err := embed(ctx, query)
	if err != nil || len(vector) == 0 {
		return vector, err
	}
	c.cache.Set(key, vector)
	return vector, nil
}

// Hits returns the number of cache hits observed since construction.
func (c *QueryEmbeddingCache) Hits() int64 {
	if c == nil {
		return 0
	}
	return c.hits.Load()
}

// Misses returns the number of cache misses observed since construction.
func (c *QueryEmbeddingCache) Misses() int64 {
	if c == nil {
		return 0
	}
	return c.misses.Load()
}

// Len returns the number of entries currently held in the cache.
func (c *QueryEmbeddingCache) Len() int {
	if c == nil {
		return 0
	}
	return c.cache.Len()
}

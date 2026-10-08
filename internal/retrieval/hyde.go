package retrieval

import (
	"context"
	"strings"
	"sync/atomic"
	"time"
)

// HyDE (Hypothetical Document Embeddings, arXiv 2212.10496) support for the
// adaptive retrieval pipeline (REQ-RET-108).
//
// For a high-uncertainty query the generator asks the configured chat LLM to
// write a short hypothetical document that WOULD answer the query, embeds
// that document, and hands the resulting vector back to the composition layer
// as an additional hybrid-search input. Engagement is strictly gated:
//
//   - Feature flag OFF by default. EnvHyDE must be explicitly enabled
//     (HyDEFromEnv, same injectable-getenv convention as
//     embedding.LeaseBatchFromEnv). When disabled, NO generation call is ever
//     made.
//   - Tier-gated (HyDEAllowedForTier in adaptive.go): only
//     TierSemanticHybrid and TierMultiHopGraph qualify. TierDirectFactual and
//     TierArchitecturalGlobal never trigger generation.
//   - Cached: generated hypothetical documents live in a ScopedCache[string]
//     keyed by embedding model + normalized query (QueryEmbeddingCacheKey)
//     with a TTL, so repeated queries within the TTL reuse the document
//     without a new LLM call.
//   - Provider errors are NEVER cached: a failed generation or embedding is a
//     miss and retrieval degrades to the normal (non-HyDE) path.
//
// The composition layer (search entry points) is responsible for obtaining
// the two seams: generate (configured chat LLM) and embed (embedding
// service). Both seams must be non-nil for HyDE to engage; anything else
// degrades to false with zero side effects.
const (
	// EnvHyDE is the feature-flag environment variable for HyDE (REQ-RET-108).
	// Default OFF: any value other than a truthy token keeps HyDE disabled.
	EnvHyDE = "CORTEX_RETRIEVAL_HYDE"

	// hydeDefaultTTL bounds how long a generated hypothetical document is
	// reused across repeated queries.
	hydeDefaultTTL = 10 * time.Minute

	// hydeDefaultCapacity bounds the hypothetical-document cache.
	hydeDefaultCapacity = 128
)

// HyDEFromEnv reports whether EnvHyDE explicitly enables HyDE. Unset, empty,
// or unrecognized values are OFF (fail-closed default). Injected getenv keeps
// tests free of environment mutation.
func HyDEFromEnv(getenv func(string) string) bool {
	if getenv == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(getenv(EnvHyDE))) {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}

// HyDEGenerator produces and caches hypothetical document embeddings.
// Construct with NewHyDEGenerator; a nil generator is safe to call.
type HyDEGenerator struct {
	generate func(ctx context.Context, query string) (string, error)
	embed    func(ctx context.Context, document string) ([]float32, error)
	model    string
	enabled  bool
	cache    *ScopedCache[string]

	hits       atomic.Int64
	misses     atomic.Int64
	calls      atomic.Int64 // chat-LLM generation attempts (0 when disabled/gated)
	embedCalls atomic.Int64 // embedding attempts
}

// NewHyDEGenerator builds a HyDE generator. enabled comes from HyDEFromEnv
// (or composition config); generate is the configured chat LLM seam and embed
// the embedding seam. capacity/ttl <= 0 fall back to defaults. The returned
// generator is safe for concurrent use; if enabled is false or either seam is
// nil, VectorForQuery always returns false without side effects.
func NewHyDEGenerator(enabled bool, model string, capacity int, ttl time.Duration, generate func(ctx context.Context, query string) (string, error), embed func(ctx context.Context, document string) ([]float32, error)) *HyDEGenerator {
	if capacity <= 0 {
		capacity = hydeDefaultCapacity
	}
	if ttl <= 0 {
		ttl = hydeDefaultTTL
	}
	return &HyDEGenerator{
		generate: generate,
		embed:    embed,
		model:    model,
		enabled:  enabled && generate != nil && embed != nil,
		cache:    NewScopedCache[string](capacity, ttl),
	}
}

// Enabled reports whether the generator will engage at all.
func (g *HyDEGenerator) Enabled() bool {
	return g != nil && g.enabled
}

// Calls returns the number of chat-LLM generation attempts observed. A count
// of 0 proves no generation call was made (disabled or tier-gated paths).
func (g *HyDEGenerator) Calls() int64 {
	if g == nil {
		return 0
	}
	return g.calls.Load()
}

// Hits and Misses expose document-cache observability. All accessors are
// nil-receiver safe: a nil generator is a fully degraded, zero-call state.
func (g *HyDEGenerator) Hits() int64 {
	if g == nil {
		return 0
	}
	return g.hits.Load()
}

func (g *HyDEGenerator) Misses() int64 {
	if g == nil {
		return 0
	}
	return g.misses.Load()
}

// VectorForQuery returns the embedding of the generated hypothetical document
// for the query, or false when HyDE does not engage (disabled, tier-gated, or
// provider failure). The tier gate runs FIRST so cheap tiers never incur even
// a cache lookup. Provider errors are not cached: the next call retries.
func (g *HyDEGenerator) VectorForQuery(ctx context.Context, tier QueryTier, query string) ([]float32, bool) {
	if !g.Enabled() || !HyDEAllowedForTier(tier) {
		return nil, false
	}
	key := QueryEmbeddingCacheKey(g.model, query)
	if doc, ok := g.cache.Get(key); ok {
		g.hits.Add(1)
		return g.embedDocument(ctx, doc)
	}
	g.misses.Add(1)

	doc, err := g.generate(ctx, query)
	if err != nil || strings.TrimSpace(doc) == "" {
		// Degrade to the normal path; never cache failures or empty docs.
		return nil, false
	}
	vector, ok := g.embedDocument(ctx, doc)
	if !ok {
		// Embedding failed: cache nothing (mirroring the REQ-RET-101
		// contract that a failed embed is a miss) so the next call retries
		// the full generation path.
		return nil, false
	}
	g.cache.Set(key, doc)
	return vector, true
}

func (g *HyDEGenerator) embedDocument(ctx context.Context, doc string) ([]float32, bool) {
	g.embedCalls.Add(1)
	vector, err := g.embed(ctx, doc)
	if err != nil || len(vector) == 0 {
		return nil, false
	}
	return vector, true
}

// hydePrompt renders the generation prompt for the chat-LLM seam. Exported
// for composition layers that want the canonical arXiv 2212.10496 prompt
// shape; the generator itself stays transport-agnostic.
func hydePrompt(query string) string {
	var b strings.Builder
	b.WriteString("Write a short passage (3-5 sentences) that directly answers the following question. ")
	b.WriteString("Write it as if it were an excerpt from a technical document or knowledge base. ")
	b.WriteString("Do not add commentary, disclaimers, or headers.\n\nQuestion: ")
	b.WriteString(query)
	return b.String()
}

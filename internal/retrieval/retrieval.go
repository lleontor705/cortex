// Package retrieval provides shared retrieval-pipeline orchestration that
// bridges the domain.VectorIndex port (W8.1, ADR-05) to the full search-result
// types that MCP, bench, CLI, and TUI consumers require.
//
// The VectorIndex port returns lightweight VectorCandidate results carrying
// only an observation ID and a similarity score. Consumers need full
// observation data to format responses and to fuse vector results with FTS5
// results via Reciprocal Rank Fusion. This package centralizes that
// post-fetch orchestration so it is NOT duplicated across consumer packages.
//
// Dependency direction: this package imports ONLY internal/domain. It defines
// a narrow ObservationLookup interface (satisfied structurally by every
// concrete observation store) so it never reaches into a store package. Both
// internal/mcp and bench/locomo import this package; neither duplicates the
// helpers anymore.
//
// The functions here are a pure extraction of logic that was previously
// duplicated verbatim in internal/mcp/tools_cortex.go and
// bench/locomo/runner.go. The extraction preserves byte-for-byte behavior:
// same RRF constant (k=60), same 1-based rank indexing, same tie-breaking
// (sort.Slice, NOT stable — matching the original), same soft-delete drop
// discipline.
package retrieval

import (
	"context"
	"sort"

	"github.com/lleontor705/cortex/internal/domain"
)

// rrfConstant is the Reciprocal Rank Fusion smoothing constant (k=60, the
// standard value from the TREC conference). RRF scores a candidate by its
// POSITION in each input list: score(id) = sum over lists of 1/(k + rank),
// where rank is the 1-based position. A raw relevance SCORE (BM25, cosine
// similarity) is never treated as a rank input — only list position matters.
const rrfConstant = 60.0

// ObservationLookup is the observation-store subset needed for candidate
// revalidation. Every concrete observation store (*sqlite.Store, test fakes,
// any domain.ObservationRepository) satisfies this structurally. Defining the
// narrow interface here keeps this package free of any store import while
// remaining compatible with every backend.
type ObservationLookup interface {
	GetByID(ctx context.Context, id int64) (*domain.Observation, error)
}

// BatchObservationLookup is the OPTIONAL batch-capable superset of
// ObservationLookup (VEC-01). RevalidateCandidates detects it via type
// assertion and, when hydration succeeds, replaces the per-candidate N+1
// GetByID loop with a single GetByIDs call over the unique candidate IDs.
//
// It is deliberately retrieval-local: it is NOT added to
// domain.ObservationRepository (which has multiple implementors), so stores
// opt in simply by exposing the method — *sqlite.Store does. A lookup that
// does not implement it keeps the exact legacy per-ID behavior.
//
// GetByIDs contract:
//
//   - Empty/nil ids MUST issue no SQL and return an empty map.
//   - Live rows MUST be keyed by observation ID.
//   - Soft-deleted and missing IDs MUST be absent from the map (or mapped to
//     nil), which the engine treats as a drop — identical legacy semantics.
//   - Rows for IDs that were not requested MAY be present and are ignored.
type BatchObservationLookup interface {
	ObservationLookup
	GetByIDs(ctx context.Context, ids []int64) (map[int64]*domain.Observation, error)
}

// RevalidateCandidates converts lightweight VectorCandidate results (ID +
// score from a domain.VectorIndex) into full VectorSearchResult entries by
// looking up the observation data via the provided ObservationLookup.
//
// Candidates whose observation cannot be loaded (soft-deleted, missing,
// store error) are DROPPED — the same revalidation discipline the
// store-layer pipeline applies to fused candidates. A nil observation from
// the store is treated the same as an error: the candidate is dropped.
//
// Batch fast path (VEC-01): when obs also implements BatchObservationLookup,
// the unique candidate IDs are hydrated with ONE GetByIDs call and the
// results are rebuilt by iterating the original candidate sequence. If the
// batch call fails, the error is swallowed and the unchanged per-ID loop
// runs instead — outputs are byte-equivalent either way.
//
// The output preserves the INPUT ORDER of candidates (NOT re-sorted by
// score). Callers that need score-sorted output should sort the returned
// slice or rely on FuseResults, which re-sorts via RRF.
func RevalidateCandidates(ctx context.Context, obs ObservationLookup, candidates []domain.VectorCandidate) []*domain.VectorSearchResult {
	results := make([]*domain.VectorSearchResult, 0, len(candidates))
	if len(candidates) == 0 {
		return results // zero candidates: zero hydration work, zero SQL
	}

	// Optional batch fast path. Any failure falls through to the legacy
	// per-ID loop without surfacing the batch error (VEC-01 fallback rule).
	if batch, ok := obs.(BatchObservationLookup); ok {
		if batchResults, ok := revalidateCandidatesBatch(ctx, batch, candidates); ok {
			return batchResults
		}
	}

	for _, c := range candidates {
		o, err := obs.GetByID(ctx, c.ID)
		if err != nil || o == nil {
			continue // soft-deleted or missing: drop
		}
		results = append(results, &domain.VectorSearchResult{
			Observation: *o,
			Similarity:  c.Score,
		})
	}
	return results
}

// revalidateCandidatesBatch hydrates the unique candidate IDs with a single
// GetByIDs call and rebuilds results in candidate order. It reports ok=false
// ONLY when the batch call errored (the caller then uses the legacy loop).
// Duplicate candidates are hydrated once but emitted per occurrence; map
// misses, nil values, and unrequested extra rows keep legacy semantics.
func revalidateCandidatesBatch(ctx context.Context, batch BatchObservationLookup, candidates []domain.VectorCandidate) ([]*domain.VectorSearchResult, bool) {
	// Deduplicate IDs (first-seen order) so the store hydrates each ID once.
	ids := make([]int64, 0, len(candidates))
	seen := make(map[int64]struct{}, len(candidates))
	for _, c := range candidates {
		if _, dup := seen[c.ID]; !dup {
			seen[c.ID] = struct{}{}
			ids = append(ids, c.ID)
		}
	}

	byID, err := batch.GetByIDs(ctx, ids)
	if err != nil {
		return nil, false // swallow: legacy per-ID fallback owns this case
	}

	// Rebuild strictly by candidate iteration (never map order) so rank,
	// similarity slots, and duplicates are byte-equivalent to the legacy path.
	results := make([]*domain.VectorSearchResult, 0, len(candidates))
	for _, c := range candidates {
		if o := byID[c.ID]; o != nil { // map miss or nil -> drop (legacy rule)
			results = append(results, &domain.VectorSearchResult{
				Observation: *o,
				Similarity:  c.Score,
			})
		}
	}
	return results, true
}

// FuseResults combines FTS5 full-text search results with vector similarity
// search results using Reciprocal Rank Fusion (k=60).
//
// Each input list is treated as a TRUE ranked list: only the 1-based POSITION
// (rank) contributes to the RRF score — a raw relevance score (BM25, cosine
// similarity) is NEVER fed into RRF as a rank input. The RRF formula is:
//
//	score(id) = 1/(k + rank_fts) + 1/(k + rank_vec)
//
// where rank is the 1-based position in the respective list. An ID appearing
// in BOTH lists accumulates RRF credit from each (additive). An ID appearing
// in only one list gets credit only from that list.
//
// The output is sorted by descending RRF score, truncated to limit. When
// scores are tied, sort.Slice (NOT stable) is used — matching the original
// behavior in every consumer. Callers must not depend on tie-breaking order.
//
// For vector-only results (no FTS5 match), the VectorSearchResult.Similarity
// score is carried onto the SearchResult.Rank field so downstream consumers
// can inspect it.
func FuseResults(ftsResults []*domain.SearchResult, vecResults []*domain.VectorSearchResult, limit int) []*domain.SearchResult {
	type scored struct {
		result *domain.SearchResult
		score  float64
	}

	scoreMap := make(map[int64]*scored)

	// Score FTS5 results: 1-based rank position only.
	for rank, r := range ftsResults {
		scoreMap[r.ID] = &scored{
			result: r,
			score:  1.0 / (rrfConstant + float64(rank+1)),
		}
	}

	// Add vector result scores: 1-based rank position only. An ID already
	// present from FTS5 accumulates additive RRF credit.
	for rank, vr := range vecResults {
		rrf := 1.0 / (rrfConstant + float64(rank+1))
		if existing, ok := scoreMap[vr.ID]; ok {
			existing.score += rrf
		} else {
			scoreMap[vr.ID] = &scored{
				result: &domain.SearchResult{
					Observation: vr.Observation,
					Rank:        vr.Similarity,
				},
				score: rrf,
			}
		}
	}

	// Sort by descending RRF score. sort.Slice (NOT stable) preserves the
	// original consumer behavior; ties are broken non-deterministically.
	sorted := make([]*scored, 0, len(scoreMap))
	for _, s := range scoreMap {
		sorted = append(sorted, s)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].score > sorted[j].score
	})

	// Truncate to limit.
	results := make([]*domain.SearchResult, 0, limit)
	for i, s := range sorted {
		if i >= limit {
			break
		}
		results = append(results, s.result)
	}

	return results
}

// ---------------------------------------------------------------------------
// SearchVectors — capability-driven vector search (W8.4, REQ-VEC-001/002)
//
// SearchVectors is the retrieval-engine entry point that SELECTS STRATEGY from
// the configured VectorIndex's declared Capabilities (ADR-05, REQ-VEC-001
// happy path). It is the single function every consumer (MCP, bench, CLI)
// SHOULD call instead of reaching for idx.Search directly, so the strategy
// selection is centralized and consistent.
//
// Strategy selection (driven by Capabilities.Filters):
//
//   - PreFilter (e.g. Qdrant): the adapter applies filters server-side at index
//     scan time (Must conditions). The engine TRUSTS the adapter's filtered
//     results. It passes the query as-is (no pool expansion) and does NOT
//     re-apply filters in-engine. Revalidation still runs (soft-delete drop).
//
//   - PostFilter (e.g. sqlite_blob, pgvector): the adapter applies filters via
//     WHERE clauses AFTER the ANN scan. PostFilter is less precise (the ANN
//     may return candidates that the filter then removes, reducing recall).
//     The engine expands the retrieval POOL (limit * PostFilterPoolMultiplier)
//     to give in-engine filtering headroom, then RE-APPLIES the filters in-
//     engine as a safety net against silent filter drops (REQ-VEC-002).
//
//   - none / empty: the adapter does NO filtering. The engine retrieves a
//     larger pool and applies ALL filtering in-engine.
//
// In-engine filter re-application (the safety net) matches the revalidated
// observation's fields (Project, Scope) against the VectorQuery filter map.
// This is the "filter never silently dropped" guarantee: even if a PostFilter
// adapter returns candidates that don't match the declared filter, the engine
// removes them before returning to the caller.
// ---------------------------------------------------------------------------

// PostFilterPoolMultiplier is the factor by which the retrieval pool is
// expanded when the adapter declares PostFilter or none. A multiplier of 3
// gives in-engine filtering enough headroom to recover candidates the adapter's
// post-filter removed while keeping the pool bounded. This is a heuristic; the
// engine truncates to the requested limit after in-engine filtering.
const PostFilterPoolMultiplier = 3

// SearchVectors executes a vector similarity search with capability-driven
// strategy selection. It reads idx.Capabilities, selects the appropriate
// filter strategy, retrieves candidates, revalidates them against the live
// observation store, applies in-engine filter safety-net when needed, and
// truncates to the requested limit.
//
// Returns full VectorSearchResult entries (observation + similarity score).
// Soft-deleted, missing, or filter-mismatched candidates are dropped.
func SearchVectors(ctx context.Context, idx domain.VectorIndex, q domain.VectorQuery, obs ObservationLookup) ([]*domain.VectorSearchResult, error) {
	if idx == nil {
		return nil, nil
	}

	// Read declared capabilities to select the strategy.
	caps, capsErr := idx.Capabilities(ctx)

	// Determine whether to trust adapter filters (PreFilter) or re-apply
	// in-engine (PostFilter / none / unknown). On Capabilities error, treat
	// as PostFilter (defensive: re-apply filters in-engine).
	trustAdapter := false
	poolMultiplier := 1
	if capsErr == nil && caps.Filters == "PreFilter" {
		trustAdapter = true
	} else {
		// PostFilter, none, empty, or Capabilities error: expand pool and
		// re-apply filters in-engine.
		poolMultiplier = PostFilterPoolMultiplier
	}

	// Expand the pool for non-PreFilter strategies so in-engine filtering has
	// headroom after the adapter's less-precise post-filter.
	poolQ := q
	if !trustAdapter && q.Limit > 0 {
		poolQ.Limit = q.Limit * poolMultiplier
	}

	candidates, err := idx.Search(ctx, poolQ)
	if err != nil {
		return nil, err
	}

	// Revalidate: load full observations, drop soft-deleted/missing.
	results := RevalidateCandidates(ctx, obs, candidates)

	// In-engine filter safety net for non-PreFilter strategies. This is the
	// "filter never silently dropped" guarantee (REQ-VEC-002).
	if !trustAdapter && q.Filters != nil {
		results = applyFiltersInEngine(results, q.Filters)
	}

	// Truncate to the requested limit AFTER in-engine filtering.
	if q.Limit > 0 && len(results) > q.Limit {
		results = results[:q.Limit]
	}

	return results, nil
}

// applyFiltersInEngine re-applies the VectorQuery filter map against the
// revalidated observations' fields. A result that does not match EVERY
// declared string filter is dropped. This is the safety net for PostFilter and
// none-filter adapters where the adapter may silently drop or imprecisely
// apply filters.
//
// Recognized filter keys (matched against the corresponding Observation field):
//
//	"project" → Observation.Project
//	"scope"   → Observation.Scope
//	"type"    → Observation.Type
//	"source"  → Observation.Source
//
// Unknown keys are ignored (filter-transparent) — applying them would require
// a schema the observation model does not expose. The recognized set covers
// every filter the MCP/HTTP surface declares.
func applyFiltersInEngine(results []*domain.VectorSearchResult, filters map[string]any) []*domain.VectorSearchResult {
	if len(filters) == 0 {
		return results
	}
	filtered := make([]*domain.VectorSearchResult, 0, len(results))
	for _, r := range results {
		if matchesFilters(r.Observation, filters) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// matchesFilters reports whether the observation matches every recognized
// string filter in the map. An empty filter value in the map is treated as
// "not set" and does not constrain.
func matchesFilters(obs domain.Observation, filters map[string]any) bool {
	for key, val := range filters {
		s, ok := val.(string)
		if !ok || s == "" {
			continue // not a string filter or empty — skip
		}
		switch key {
		case "project":
			if obs.Project != s {
				return false
			}
		case "scope":
			if obs.Scope != s {
				return false
			}
		case "type":
			if obs.Type != s {
				return false
			}
		case "source":
			if obs.Source != s {
				return false
			}
			// Unknown keys: ignore (filter-transparent).
		}
	}
	return true
}

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

// RevalidateCandidates converts lightweight VectorCandidate results (ID +
// score from a domain.VectorIndex) into full VectorSearchResult entries by
// looking up the observation data via the provided ObservationLookup.
//
// Candidates whose observation cannot be loaded (soft-deleted, missing,
// store error) are DROPPED — the same revalidation discipline the
// store-layer pipeline applies to fused candidates. A nil observation from
// the store is treated the same as an error: the candidate is dropped.
//
// The output preserves the INPUT ORDER of candidates (NOT re-sorted by
// score). Callers that need score-sorted output should sort the returned
// slice or rely on FuseResults, which re-sorts via RRF.
func RevalidateCandidates(ctx context.Context, obs ObservationLookup, candidates []domain.VectorCandidate) []*domain.VectorSearchResult {
	results := make([]*domain.VectorSearchResult, 0, len(candidates))
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

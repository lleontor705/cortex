package embedding

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// Near-duplicate detection at ingest (default OFF).
//
// After an observation is successfully embedded and upserted (the same seam
// the embedding worker uses), the optional dedup hook queries the vector
// index for the nearest neighbor above a similarity threshold and, if found
// on a DIFFERENT observation id, records a `duplicates_of` graph relation
// from the new observation to the existing one. Nothing is deleted or
// skipped: provenance is preserved, and stats can expose duplicate counts
// through the edge store.
//
// Exact-scan cost: the vector search is an exact O(N·d) cosine scan over the
// tenant's vectors (at 4096 dims ≈ 8K multiply-adds per candidate row). At
// the current corpus scale this is acceptable; it runs once per embedded
// observation inside the worker, never on the interactive save path. Once
// dims ≤ 2000 and an ANN index exists (pgvector HNSW), the same query is
// index-backed.
const (
	// EnvIngestDedup enables the hook when set to "true" or "1".
	EnvIngestDedup = "CORTEX_INGEST_DEDUP"
	// EnvIngestDedupThreshold overrides the similarity threshold (0 < t <= 1).
	EnvIngestDedupThreshold = "CORTEX_INGEST_DEDUP_THRESHOLD"
	// DefaultDedupThreshold is the minimum cosine similarity for a
	// near-duplicate verdict.
	DefaultDedupThreshold = 0.95
	// RelationDuplicatesOf is the graph relation recorded from the duplicate
	// (new) observation to the pre-existing (canonical) observation.
	RelationDuplicatesOf = "duplicates_of"

	dedupSource      = "embedding-worker-dedup"
	dedupSearchLimit = 5
)

// DedupConfig carries the resolved ingest-dedup settings.
type DedupConfig struct {
	Enabled   bool
	Threshold float64
}

// edgeCreator is the graph-write surface the dedup hook needs. The concrete
// graph store satisfies it structurally; the port keeps the worker free of
// store imports.
type edgeCreator interface {
	CreateEdge(ctx context.Context, edge *domain.Edge) error
	GetEdgesForObservation(ctx context.Context, obsID int64) ([]*domain.Edge, error)
}

// vectorSearcher is the nearest-neighbor read surface the dedup hook needs.
// It is resolved by runtime type-assertion on the worker's vectorWriter so
// existing minimal fakes keep compiling; every real domain.VectorIndex
// adapter implements Search.
type vectorSearcher interface {
	Search(ctx context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error)
}

// DedupConfigFromEnv resolves the CORTEX_INGEST_DEDUP* environment variables.
// Returns nil when the feature is OFF (the default). An unparsable or
// out-of-range threshold falls back to the documented default rather than
// disabling the feature.
func DedupConfigFromEnv() *DedupConfig {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvIngestDedup))) {
	case "true", "1":
	default:
		return nil
	}
	threshold := DefaultDedupThreshold
	if raw := strings.TrimSpace(os.Getenv(EnvIngestDedupThreshold)); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil && parsed > 0 && parsed <= 1 {
			threshold = parsed
		} else {
			log.Printf("embedding worker: invalid %s=%q; using default %.2f", EnvIngestDedupThreshold, raw, DefaultDedupThreshold)
		}
	}
	return &DedupConfig{Enabled: true, Threshold: threshold}
}

// EnableDedup wires the optional near-duplicate hook. Called by the
// composition root when the feature is configured; a nil cfg disables it.
// Call before Start; not goroutine-safe with concurrent processing.
func (w *Worker) EnableDedup(edges edgeCreator, cfg *DedupConfig) {
	if cfg == nil || !cfg.Enabled || edges == nil {
		return
	}
	w.dedupEdges = edges
	w.dedup = cfg
}

// maybeLinkDuplicate finds the nearest pre-existing observation above the
// configured similarity threshold and records a duplicates_of edge. It is
// best-effort: any failure is logged and never fails the embed intent — the
// embedding outcome and the dedup linkage are independent concerns.
func (w *Worker) maybeLinkDuplicate(ctx context.Context, obsID int64, vec []float32) {
	if w.dedup == nil || w.dedupEdges == nil {
		return
	}
	searcher, ok := w.vectors.(vectorSearcher)
	if !ok {
		return
	}

	// Exact-scan vector search (see the package-level cost note).
	candidates, err := searcher.Search(ctx, domain.VectorQuery{
		Vector:    vec,
		Limit:     dedupSearchLimit,
		Threshold: w.dedup.Threshold,
	})
	if err != nil {
		log.Printf("embedding worker: dedup search for observation %d: %v", obsID, err)
		return
	}

	match := nearestOther(candidates, obsID, w.dedup.Threshold)
	if match == nil {
		return
	}

	// Idempotency: a retried intent must not create duplicate edges.
	existing, err := w.dedupEdges.GetEdgesForObservation(ctx, obsID)
	if err != nil {
		log.Printf("embedding worker: dedup edge lookup for observation %d: %v", obsID, err)
		return
	}
	for _, e := range existing {
		if e.RelationType == RelationDuplicatesOf && e.ToObsID == match.ID {
			return
		}
	}

	edge := &domain.Edge{
		FromObsID:    obsID,
		ToObsID:      match.ID,
		RelationType: RelationDuplicatesOf,
		Weight:       1.0,
		Confidence:   clampSimilarity(match.Score),
		Source:       dedupSource,
		Reasoning: fmt.Sprintf("ingest dedup: cosine similarity %.4f >= threshold %.2f (nearest observation %d)",
			match.Score, w.dedup.Threshold, match.ID),
	}
	if err := w.dedupEdges.CreateEdge(ctx, edge); err != nil {
		log.Printf("embedding worker: record duplicates_of %d->%d: %v", obsID, match.ID, err)
	}
}

// nearestOther returns the best candidate whose id differs from obsID and
// whose score meets the threshold. Candidates are expected sorted by score
// descending (the VectorIndex contract); the scan is defensive and takes the
// max over qualifying candidates regardless of order.
func nearestOther(candidates []domain.VectorCandidate, obsID int64, threshold float64) *domain.VectorCandidate {
	var best *domain.VectorCandidate
	for i := range candidates {
		c := &candidates[i]
		if c.ID == obsID || c.ID == 0 {
			continue
		}
		if c.Score < threshold {
			continue
		}
		if best == nil || c.Score > best.Score {
			best = c
		}
	}
	return best
}

// clampSimilarity maps an adapter score onto the edge confidence range
// [0,1]; scores above 1 (possible with non-cosine provenance) saturate.
func clampSimilarity(s float64) float64 {
	if math.IsNaN(s) {
		return 0
	}
	if s < 0 {
		return 0
	}
	if s > 1 {
		return 1
	}
	return s
}

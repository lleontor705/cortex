// Package external: reindex implementation (W8.4 — replay external vector
// replica).
//
// Reindex replays observations from the authoritative SQLite store into a
// configured external VectorIndex (qdrant, pgvector). It is the recovery /
// consistency path for an external vector replica that has drifted or is being
// initialized fresh. SQLite remains the single source of truth for observation
// data (ADR-05); the external index is a read-optional dense candidate source.
//
// DESIGN — narrow port, no raw BLOB access:
//
// The ReindexSource port exposes ONLY List (observation iteration) and
// GetEmbedding (single-vector retrieval via the existing VectorRepository
// contract). It deliberately does NOT reach into the SQLite vector BLOB table
// directly. When GetEmbedding returns ErrVectorSearchDisabled (the zero-CGO
// stub) or ErrNotFound, the reindex falls back to re-embedding via the
// provided EmbeddingProvider — regenerating fresh vectors rather than reading
// a source whose contract is insufficient.
//
// IDEMPOTENCE: upsert is by observation ID (the external adapter's PK). Running
// Reindex multiple times produces the same replica state — no duplicates, no
// growth. The model-version namespace on each VectorPoint lets the adapter
// enforce dimension consistency (REQ-VEC-001).
//
// BATCHING: observations are processed in batches of BatchSize. Each batch is
// upserted in one call to VectorIndex.Upsert, respecting the adapter's
// MaxBatchSize (the adapter chunks further if needed).
//
// PROGRESS: an optional OnProgress callback is invoked after each batch with
// cumulative counts, enabling CLI/UI progress reporting.
//
// FAILURE: target Upsert errors are returned explicitly (not silently
// swallowed). The result reflects counts up to the failure point. Individual
// observations that cannot be embedded (no vector, no provider, embed error)
// are counted as Skipped — the reindex is best-effort and continues.
package external

import (
	"context"
	"errors"
	"fmt"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// ReindexSource is the narrow port for the authoritative observation store.
// It exposes ONLY the two operations the reindex needs:
//
//   - List: page through active observations (for text + metadata)
//   - GetEmbedding: retrieve a single existing embedding vector
//
// It deliberately does NOT embed the full ObservationRepository (which would
// drag in Save/Update/Delete/etc. that the reindex never calls) and does NOT
// expose raw vector BLOB access or bulk vector reads. If the source cannot
// provide embeddings (zero-CGO stub returns ErrVectorSearchDisabled), the
// reindex falls back to the EmbeddingProvider. This keeps the port minimal
// and avoids coupling to SQLite-internal schema.
//
// The concrete authoritative store satisfies this structurally: *sqlite.Store
// has List, and *sqlite.VectorStore has GetEmbedding. A composite wiring in
// the server composition root combines both into a single value satisfying
// this interface; test fakes implement it directly.
type ReindexSource interface {
	// List retrieves observations matching the filter, paginated by
	// Limit/Offset. Used by the reindex to iterate active observations in
	// deterministic ID-ascending order.
	List(ctx context.Context, filter domain.ObservationFilter) ([]*domain.Observation, error)

	// GetEmbedding retrieves an existing embedding for an observation.
	// Returns ErrNotFound if no embedding exists, or ErrVectorSearchDisabled
	// if the source's vector backend is unavailable (zero-CGO stub). Both
	// trigger re-embedding via the EmbeddingProvider.
	GetEmbedding(ctx context.Context, observationID int64) ([]float32, string, error)
}

// ReindexOptions configures a Reindex run.
type ReindexOptions struct {
	// BatchSize is the number of observations processed per Upsert call to
	// the target. Default 64. Larger batches reduce round-trips; smaller
	// batches reduce memory and per-batch failure blast radius.
	BatchSize int

	// OnProgress is invoked after each batch with cumulative counts. Optional;
	// nil means no progress reporting.
	OnProgress func(p ReindexProgress)
}

// ReindexProgress is the cumulative state at a progress checkpoint.
type ReindexProgress struct {
	Processed  int // observations examined (including skipped)
	Upserted   int // vectors successfully upserted into the target
	ReEmbedded int // vectors regenerated via EmbeddingProvider (no source vector)
	Skipped    int // observations with no vector and no way to produce one
}

// ReindexResult is the final outcome of a Reindex run.
type ReindexResult struct {
	Total      int // total observations examined
	Upserted   int // vectors successfully upserted
	ReEmbedded int // vectors regenerated via EmbeddingProvider
	Skipped    int // observations skipped (no vector available)
	Batches    int // number of Upsert calls made to the target
}

// Reindex replays observations from src into target, copying existing
// embeddings where available and re-embedding via provider where not.
//
// The run is idempotent: the target's upsert is by observation ID, so running
// Reindex multiple times converges the replica to the source state without
// duplicates.
//
// Returns a ReindexResult with final counts. If the target returns an Upsert
// error, it is returned wrapped (with counts visible in the result up to the
// failure batch).
func Reindex(ctx context.Context, src ReindexSource, provider domain.EmbeddingProvider, target domain.VectorIndex, opts ReindexOptions) (*ReindexResult, error) {
	if src == nil {
		return nil, errors.New("external: reindex requires a non-nil source")
	}
	if target == nil {
		return nil, errors.New("external: reindex requires a non-nil target VectorIndex")
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 64
	}

	result := &ReindexResult{}
	progress := ReindexProgress{}

	// Iterate observations in batches. We use Limit/Offset on the
	// ObservationFilter to page through the source. OrderAsc=true gives
	// deterministic ID-ascending order (stable across re-runs, important for
	// idempotent replay).
	offset := 0
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		batch, err := src.List(ctx, domain.ObservationFilter{
			Limit:           opts.BatchSize,
			Offset:          offset,
			OrderAsc:        true,
			IncludeArchived: false, // only active observations
		})
		if err != nil {
			return result, fmt.Errorf("external: reindex list offset %d: %w", offset, err)
		}
		if len(batch) == 0 {
			break // source exhausted
		}

		points, reEmbedded, skipped, err := buildBatchPoints(ctx, src, provider, batch)
		if err != nil {
			return result, err
		}
		result.ReEmbedded += reEmbedded
		result.Skipped += skipped
		progress.ReEmbedded = result.ReEmbedded
		progress.Skipped = result.Skipped

		// Upsert the batch if any points were produced.
		if len(points) > 0 {
			if err := target.Upsert(ctx, points); err != nil {
				return result, fmt.Errorf("external: reindex upsert batch at offset %d: %w", offset, err)
			}
			result.Upserted += len(points)
			result.Batches++
			progress.Upserted = result.Upserted
		}

		result.Total += len(batch)
		progress.Processed = result.Total
		if opts.OnProgress != nil {
			opts.OnProgress(progress)
		}

		offset += len(batch)
		if len(batch) < opts.BatchSize {
			break // last partial batch — done
		}
	}

	return result, nil
}

// buildBatchPoints produces VectorPoints for a batch of observations. For each
// observation:
//   - Try to retrieve an existing embedding from the source.
//   - If the source returns ErrNotFound or ErrVectorSearchDisabled, fall back
//     to re-embedding via the provider (if supplied).
//   - If neither path yields a vector, the observation is counted as skipped.
//
// Re-embedding is batched: all observations in the batch that need
// re-embedding are embedded in ONE provider.Embed call (efficiency).
func buildBatchPoints(ctx context.Context, src ReindexSource, provider domain.EmbeddingProvider, batch []*domain.Observation) (points []domain.VectorPoint, reEmbedded, skipped int, err error) {
	// First pass: collect existing embeddings and identify which observations
	// need re-embedding.
	type needEmbed struct {
		idx int
		obs *domain.Observation
	}
	points = make([]domain.VectorPoint, 0, len(batch))
	var needReembed []needEmbed

	for i, obs := range batch {
		if obs == nil {
			skipped++
			continue
		}
		vec, model, err := src.GetEmbedding(ctx, obs.ID)
		if err == nil && len(vec) > 0 {
			// Existing embedding: copy with the source's model.
			points = append(points, domain.VectorPoint{
				ID:     obs.ID,
				Vector: vec,
				ModelInfo: domain.ModelInfo{
					Name:      model,
					Dimension: len(vec),
				},
				Metadata: observationMetadata(obs),
			})
			continue
		}
		// Source has no vector (NotFound) or is disabled. Both are "need
		// re-embed" — the distinction is logged but not semantically
		// different here.
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrVectorSearchDisabled) {
			needReembed = append(needReembed, needEmbed{idx: i, obs: obs})
			continue
		}
		// Unexpected error from GetEmbedding: surface it.
		return nil, 0, 0, fmt.Errorf("external: reindex get embedding for observation %d: %w", obs.ID, err)
	}

	// Re-embed the observations that need it, if a provider is available.
	if len(needReembed) > 0 {
		if provider == nil {
			// No provider: count as skipped.
			skipped += len(needReembed)
			return points, reEmbedded, skipped, nil
		}
		texts := make([]string, len(needReembed))
		for i, ne := range needReembed {
			texts[i] = ne.obs.Title + "\n" + ne.obs.Content
		}
		vectors, modelInfo, err := provider.Embed(ctx, texts)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("external: reindex re-embed %d observations: %w", len(needReembed), err)
		}
		if len(vectors) != len(needReembed) {
			return nil, 0, 0, fmt.Errorf("external: reindex re-embed returned %d vectors for %d texts", len(vectors), len(needReembed))
		}
		for i, ne := range needReembed {
			points = append(points, domain.VectorPoint{
				ID:        ne.obs.ID,
				Vector:    vectors[i],
				ModelInfo: modelInfo,
				Metadata:  observationMetadata(ne.obs),
			})
		}
		reEmbedded = len(needReembed)
	}

	return points, reEmbedded, skipped, nil
}

// observationMetadata extracts the filter-relevant fields from an observation
// into a metadata map, so the target adapter can store them for PreFilter /
// PostFilter search (project, scope, type, source, tenant_id).
func observationMetadata(obs *domain.Observation) map[string]any {
	if obs == nil {
		return nil
	}
	m := map[string]any{
		"project": obs.Project,
		"scope":   obs.Scope,
		"type":    obs.Type,
		"source":  obs.Source,
	}
	return m
}

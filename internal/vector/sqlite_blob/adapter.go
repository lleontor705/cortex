// Package sqlite_blob implements the always-available zero-CGO VectorIndex
// adapter (ADR-05, REQ-VEC-001).
//
// It wraps the existing SQLite BLOB vector scan (internal/store/sqlite) behind
// the domain.VectorIndex port. This is the single highest-leverage change of
// the vector modernization: bundle.Stores.Vectors changes from the concrete
// *sqlite.VectorStore to domain.VectorIndex, unblocking every future adapter
// (qdrant, pgvector) without touching MCP/HTTP/CLI/TUI.
//
// Build-tag semantics are PRESERVED EXACTLY:
//   - Default build (cortex_vectors NOT set, zero-CGO): the underlying
//     sqlite.VectorStore is the stub that returns ErrVectorSearchDisabled.
//     This adapter reports unhealthy/degraded and passes the disabled error
//     through. No external service, no CGO.
//   - cortex_vectors build tag: the underlying sqlite.VectorStore is the full
//     O(N) cosine BLOB scan. This adapter delegates to it and reports healthy.
//
// Dimension-mismatch corruption is FIXED (REQ-VEC-001 error scenario): the
// legacy cosine path logged a warning and scored mismatched vectors 0 (silent
// corruption). This adapter REJECTS any upsert whose vector dimension does not
// match the declared ModelInfo.Dimension with domain.ErrDimensionMismatch —
// the mismatched vector is never stored.
//
// The adapter does NOT own the *sql.DB or the observation_vectors schema — it
// delegates to the existing concrete store, preserving byte-for-byte local
// behavior. Qdrant and pgvector adapters are separate (W8.2/W8.3).
package sqlite_blob

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lleontor705/cortex/internal/domain"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
)

// adapterID is the stable identifier declared via ID() and Capabilities().IndexType.
const adapterID = "sqlite_blob"

// Adapter wraps the existing concrete *sqlite.VectorStore as a domain.VectorIndex.
// It is the zero-CGO default and is always available for wiring (operations
// return ErrVectorSearchDisabled when the cortex_vectors tag is not set).
type Adapter struct {
	store *sqlitestore.VectorStore
	caps  domain.Capabilities
}

// New creates a sqlite_blob adapter over the existing concrete VectorStore.
// The db may be nil for capability/health-only wiring (tests, capability
// negotiation before the database is open). A nil db produces a stub store
// that reports unavailable, matching the zero-CGO default.
func New(db *sql.DB) *Adapter {
	return &Adapter{
		store: sqlitestore.NewVectorStore(db),
		caps: domain.Capabilities{
			IndexType:       adapterID,
			DistanceMetrics: []string{"cosine"},
			MaxDimensions:   sqlitestore.MaxEmbeddingDimension,
			Filters:         "PostFilter",
			Hybrid:          "disabled",
			Namespaces:      "supported",
			Consistency:     "strong",
			BatchUpsert:     true,
			MaxBatchSize:    0, // unbounded: upsert loops within the caller's tx
		},
	}
}

// ID returns the stable adapter identifier.
func (a *Adapter) ID() string { return adapterID }

// Upsert stores a batch of vectors. Each point's vector dimension MUST match
// its declared ModelInfo.Dimension; a mismatch is rejected with
// domain.ErrDimensionMismatch (REQ-VEC-001 dim-mismatch corruption pin). The
// mismatched point and every subsequent point in the batch are rejected — the
// caller treats the batch atomically.
//
// Model-version namespace: the ModelInfo.Name is forwarded to the underlying
// store so vectors are namespaced by model, preventing cross-model corruption.
func (a *Adapter) Upsert(ctx context.Context, points []domain.VectorPoint) error {
	for _, p := range points {
		// Namespace / dimension enforcement BEFORE delegating. The legacy path
		// scored mismatched vectors 0 (silent corruption); this rejects them.
		if p.ModelInfo.Dimension > 0 && len(p.Vector) != p.ModelInfo.Dimension {
			ns := p.ModelInfo.Name
			if p.ModelInfo.Version != "" {
				ns = p.ModelInfo.Name + ":" + p.ModelInfo.Version
			}
			return domain.NewDimensionMismatchError(p.ModelInfo.Dimension, len(p.Vector), ns)
		}
		model := p.ModelInfo.Name
		if err := a.store.StoreEmbedding(ctx, p.ID, p.Vector, model); err != nil {
			return fmt.Errorf("sqlite_blob: upsert point %d: %w", p.ID, err)
		}
	}
	return nil
}

// Search translates a domain.VectorQuery into the underlying store's
// VectorSearchOptions and returns VectorCandidate results. Filters map
// "project"/"scope" onto the legacy Project/Scope fields, preserving exact
// local behavior.
func (a *Adapter) Search(ctx context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	opts := domain.VectorSearchOptions{
		Embedding: q.Vector,
		Limit:     q.Limit,
		Threshold: q.Threshold,
	}
	if q.Filters != nil {
		opts = filtersToSearchOptionsWith(opts, q.Filters)
	}
	results, err := a.store.SearchByVector(ctx, opts)
	if err != nil {
		return nil, err
	}
	candidates := make([]domain.VectorCandidate, 0, len(results))
	for _, r := range results {
		candidates = append(candidates, domain.VectorCandidate{
			ID:         r.ID,
			Score:      r.Similarity,
			Provenance: adapterID,
		})
	}
	return candidates, nil
}

// Delete removes vectors by observation ID. The underlying store's
// DeleteEmbedding handles the not-found case; missing IDs in the batch are
// tolerated (idempotent delete).
func (a *Adapter) Delete(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		if err := a.store.DeleteEmbedding(ctx, id); err != nil {
			// Tolerate not-found for batch idempotency.
			if domain.IsNotFoundError(err) {
				continue
			}
			return fmt.Errorf("sqlite_blob: delete point %d: %w", id, err)
		}
	}
	return nil
}

// Health reports the adapter's current health. When the underlying store is
// unavailable (zero-CGO stub), Health returns degraded with a diagnostic
// message. When the store is available (cortex_vectors enabled), Health
// returns healthy.
func (a *Adapter) Health(_ context.Context) domain.Health {
	if a.store == nil || !a.store.IsAvailable() {
		return domain.Health{
			Status:  domain.StatusDegraded,
			Message: "sqlite_blob: vector search disabled (rebuild with -tags cortex_vectors)",
		}
	}
	return domain.Health{Status: domain.StatusHealthy, Message: "sqlite_blob: ready"}
}

// Capabilities declares the sqlite_blob adapter's supported features for
// capability-driven strategy selection (ADR-05). sqlite_blob is an exact O(N)
// cosine scan with post-filtering, strong consistency (same SQLite tx), and
// batch upsert support.
func (a *Adapter) Capabilities(_ context.Context) (domain.Capabilities, error) {
	return a.caps, nil
}

// Close releases resources. sqlite_blob holds no resources beyond the shared
// *sql.DB (owned by the caller), so Close is a no-op.
func (a *Adapter) Close() error { return nil }

// Ensure the Adapter implements domain.VectorIndex (W8.1 adoption, REQ-VEC-001).
var _ domain.VectorIndex = (*Adapter)(nil)

// filtersToSearchOptions builds a VectorSearchOptions from a filter map,
// mapping the "project" and "scope" keys onto the legacy Project/Scope fields.
// This preserves the exact local filter behavior the concrete store already
// implements.
func filtersToSearchOptions(filters map[string]any) domain.VectorSearchOptions {
	return filtersToSearchOptionsWith(domain.VectorSearchOptions{}, filters)
}

// filtersToSearchOptionsWith applies the filter map onto a base options struct.
func filtersToSearchOptionsWith(base domain.VectorSearchOptions, filters map[string]any) domain.VectorSearchOptions {
	if v, ok := filters["project"]; ok {
		if s, ok := v.(string); ok {
			base.Project = s
		}
	}
	if v, ok := filters["scope"]; ok {
		if s, ok := v.(string); ok {
			base.Scope = s
		}
	}
	return base
}

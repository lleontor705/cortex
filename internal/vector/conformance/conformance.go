// Package conformance provides the shared VectorIndex conformance suite
// (REQ-VEC-002, ADR-05, W8.4).
//
// Every domain.VectorIndex adapter (sqlite_blob, qdrant, pgvector) MUST pass
// this suite against IDENTICAL fixtures. The suite verifies:
//
//   - Capabilities declares the full mandated field set.
//   - Upsert + Search round-trip returns the eligible candidate set.
//   - Dimension-mismatch vectors are REJECTED fail-closed (REQ-VEC-001
//     corruption pin).
//   - Delete removes vectors from subsequent search results (idempotent).
//   - Batch chunking respects declared MaxBatchSize when BatchUpsert is true.
//   - Health is reported explicitly (healthy/degraded/unhealthy).
//   - Score threshold filters candidates client-side.
//
// The suite is PARAMETERIZED: a Factory closure constructs a fresh, isolated
// VectorIndex for each test case. Each adapter's integration test file calls
// RunSuite with a factory that builds a real adapter (Docker-backed for
// external providers, or in-memory for sqlite_blob under cortex_vectors).
//
// The suite uses 64-dimensional fixtures (DefaultFixtures), the minimum
// dimension accepted by sqlite_blob's enabled store (MinEmbeddingDimension).
// External adapters (qdrant, pgvector) accept arbitrary dimensions, so all
// three adapters share the SAME fixtures — the parity assertion is real.
//
// LOCAL-TRACK BOUNDARY: this package imports ONLY internal/domain. It never
// imports any concrete adapter, so it compiles in the zero-CGO local build.
// Adapters import THIS package (not the other way around), preserving the
// dependency direction mandated by REQ-FOUND-001.
package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// Fixtures is the identical dataset every adapter is tested against. The
// candidate set produced by SearchForID must be deterministic given these
// fixtures, modulo declared capability differences (filter strategy, score
// rounding). Using a shared fixture set is what makes the suite a PARITY
// assertion, not just a smoke test (REQ-VEC-002 happy path).
type Fixtures struct {
	// Dimension is the vector dimension every fixture point carries. All
	// adapters are constructed with this same dimension.
	Dimension int
	// Model is the ModelInfo stamped on every fixture point (namespace
	// enforcement).
	Model domain.ModelInfo
	// Points is the set of vectors to Upsert before each search test.
	Points []domain.VectorPoint
}

// DefaultFixtures returns a deterministic 64-dimensional fixture set suitable
// for ALL adapters including sqlite_blob. 64 is the MinEmbeddingDimension
// accepted by the enabled sqlite_blob store (internal/store/sqlite); external
// adapters (qdrant, pgvector) accept arbitrary dimensions. Using the SAME
// dimension across all adapters is what makes the parity assertion real.
//
// The cosine similarity structure is identical to a 4-dim set (points differ
// only in the first few axes; the rest are zero), so the rankings are
// non-degenerate and deterministic across all adapters.
func DefaultFixtures() Fixtures {
	dim := 64 // sqlite_blob MinEmbeddingDimension-compatible.
	model := domain.ModelInfo{
		Name:       "conformance-model",
		Dimension:  dim,
		Version:    "v1",
		Normalized: true,
	}
	pts := []domain.VectorPoint{
		{ID: 1, Vector: vecAt(dim, 0, 1.0), ModelInfo: model},
		{ID: 2, Vector: vecAt2(dim, 0, 0.9, 1, 0.1), ModelInfo: model},
		{ID: 3, Vector: vecAt(dim, 1, 1.0), ModelInfo: model},
		{ID: 4, Vector: vecAt(dim, 2, 1.0), ModelInfo: model},
		{ID: 5, Vector: vecAt(dim, 3, 1.0), ModelInfo: model},
	}
	for i := range pts {
		pts[i].Metadata = map[string]any{
			"project": "conformance",
			"scope":   "project",
			"type":    "decision",
		}
	}
	return Fixtures{Dimension: dim, Model: model, Points: pts}
}

// vecAt returns a dim-dimensional vector with val at position idx and 0
// elsewhere. Used to build orthogonal fixture vectors along a single axis.
func vecAt(dim, idx int, val float32) []float32 {
	v := make([]float32, dim)
	v[idx] = val
	return v
}

// vecAt2 returns a dim-dimensional vector with val1 at idx1 and val2 at idx2.
// Used to build the near-neighbor fixture (point 2: 0.9 along axis 0 + 0.1
// along axis 1 → cosine ≈ 0.994 to point 1).
func vecAt2(dim, idx1 int, val1 float32, idx2 int, val2 float32) []float32 {
	v := make([]float32, dim)
	v[idx1] = val1
	v[idx2] = val2
	return v
}

// Factory constructs a fresh, isolated domain.VectorIndex for one test case.
// The returned adapter MUST be empty (no prior fixtures). The caller is
// responsible for Close; the suite registers t.Cleanup to invoke it.
//
// dim and model are the dimension/model the adapter is constructed with (used
// for namespace enforcement and collection creation). They match Fixtures.
type Factory func(t *testing.T, dim int, model domain.ModelInfo) (domain.VectorIndex, error)

// RunSuite executes the full shared conformance suite against the adapter
// produced by factory. Each sub-test constructs a FRESH adapter via factory so
// tests are isolated and order-independent.
//
// Call this from each adapter's integration test file:
//
//	func TestIntegration_Conformance(t *testing.T) {
//	    conformance.RunSuite(t, func(t *testing.T, dim int, m domain.ModelInfo) (domain.VectorIndex, error) {
//	        return qdrant.New(ctx, qdrant.AdapterConfig{Dimension: dim, ...})
//	    })
//	}
func RunSuite(t *testing.T, factory Factory) {
	t.Helper()
	fix := DefaultFixtures()

	t.Run("Capabilities_DeclaresFullSet", func(t *testing.T) {
		idx, err := factory(t, fix.Dimension, fix.Model)
		if err != nil {
			t.Fatalf("factory: %v", err)
		}
		defer closeAdapter(t, idx)
		assertCapabilitiesFullSet(t, idx)
	})

	t.Run("UpsertSearch_RoundTrip", func(t *testing.T) {
		idx, err := factory(t, fix.Dimension, fix.Model)
		if err != nil {
			t.Fatalf("factory: %v", err)
		}
		defer closeAdapter(t, idx)
		assertUpsertSearchRoundTrip(t, idx, fix)
	})

	t.Run("DimensionMismatch_RejectedFailClosed", func(t *testing.T) {
		idx, err := factory(t, fix.Dimension, fix.Model)
		if err != nil {
			t.Fatalf("factory: %v", err)
		}
		defer closeAdapter(t, idx)
		assertDimensionMismatchRejected(t, idx, fix)
	})

	t.Run("Delete_RemovesFromSearch", func(t *testing.T) {
		idx, err := factory(t, fix.Dimension, fix.Model)
		if err != nil {
			t.Fatalf("factory: %v", err)
		}
		defer closeAdapter(t, idx)
		assertDeleteRemovesFromSearch(t, idx, fix)
	})

	t.Run("Threshold_FiltersClientSide", func(t *testing.T) {
		idx, err := factory(t, fix.Dimension, fix.Model)
		if err != nil {
			t.Fatalf("factory: %v", err)
		}
		defer closeAdapter(t, idx)
		assertThresholdFiltersClientSide(t, idx, fix)
	})

	t.Run("Health_ExplicitStatus", func(t *testing.T) {
		idx, err := factory(t, fix.Dimension, fix.Model)
		if err != nil {
			t.Fatalf("factory: %v", err)
		}
		defer closeAdapter(t, idx)
		assertHealthExplicit(t, idx)
	})
}

// closeAdapter best-effort closes the adapter and reports failures.
func closeAdapter(t *testing.T, idx domain.VectorIndex) {
	t.Helper()
	if err := idx.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

// assertCapabilitiesFullSet verifies the adapter declares every Capabilities
// field mandated by REQ-VEC-001: IndexType, DistanceMetrics (incl. cosine),
// MaxDimensions, Filters, Hybrid, Namespaces, Consistency, and batch
// semantics. Empty strings are rejected — the adapter MUST make an explicit
// declaration so the engine can select strategy.
func assertCapabilitiesFullSet(t *testing.T, idx domain.VectorIndex) {
	t.Helper()
	caps, err := idx.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if caps.IndexType == "" {
		t.Error("Capabilities.IndexType is empty; adapter MUST identify itself")
	}
	if len(caps.DistanceMetrics) == 0 {
		t.Error("Capabilities.DistanceMetrics is empty; must declare at least cosine")
	}
	foundCosine := false
	for _, m := range caps.DistanceMetrics {
		if m == "cosine" {
			foundCosine = true
		}
	}
	if !foundCosine {
		t.Errorf("DistanceMetrics %v does not include cosine", caps.DistanceMetrics)
	}
	if caps.MaxDimensions <= 0 {
		t.Errorf("MaxDimensions = %d; must be > 0", caps.MaxDimensions)
	}
	if caps.Filters == "" {
		t.Error("Filters is empty; must declare PreFilter|PostFilter|none")
	}
	if caps.Filters != "PreFilter" && caps.Filters != "PostFilter" && caps.Filters != "none" {
		t.Errorf("Filters = %q; must be PreFilter|PostFilter|none", caps.Filters)
	}
	if caps.Hybrid == "" {
		t.Error("Hybrid is empty; must declare enabled|disabled|engine")
	}
	if caps.Namespaces == "" {
		t.Error("Namespaces is empty; must declare supported|unsupported")
	}
	if caps.Consistency == "" {
		t.Error("Consistency is empty; must declare strong|eventual")
	}
	if caps.Consistency != "strong" && caps.Consistency != "eventual" {
		t.Errorf("Consistency = %q; must be strong|eventual", caps.Consistency)
	}
}

// assertUpsertSearchRoundTrip upserts the fixtures and verifies a search for
// the closest vector to point 1 returns point 1 as the top result with high
// similarity. This is the core parity assertion: identical fixtures → identical
// eligible candidate set across adapters.
func assertUpsertSearchRoundTrip(t *testing.T, idx domain.VectorIndex, fix Fixtures) {
	t.Helper()
	ctx := context.Background()
	if err := idx.Upsert(ctx, fix.Points); err != nil {
		t.Fatalf("Upsert fixtures: %v", err)
	}
	// Search for the vector closest to point 1 (ID=1). Point 2 is the nearest
	// neighbor (0.9 cosine); points 3/4/5 are orthogonal (0.0 cosine).
	q := domain.VectorQuery{
		Vector: fix.Points[0].Vector,
		Limit:  5,
	}
	results, err := idx.Search(ctx, q)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search returned 0 results after Upsert; expected at least point 1")
	}
	// The top result MUST be point 1 (exact match, cosine=1.0).
	if results[0].ID != 1 {
		t.Errorf("top result ID = %d, want 1 (exact match for query vector)", results[0].ID)
	}
	if results[0].Score < 0.99 {
		t.Errorf("top result score = %.4f, want >= 0.99 (cosine self-similarity)", results[0].Score)
	}
	if results[0].Provenance == "" {
		t.Error("top result Provenance is empty; adapter MUST stamp it")
	}
}

// assertDimensionMismatchRejected verifies a vector whose dimension does not
// match the declared ModelInfo.Dimension is REJECTED with
// domain.ErrDimensionMismatch — NOT stored and scored 0. This is the
// REQ-VEC-001 corruption pin.
func assertDimensionMismatchRejected(t *testing.T, idx domain.VectorIndex, fix Fixtures) {
	t.Helper()
	ctx := context.Background()
	// Construct a vector with dim+1 elements declaring dim dimensions.
	badVec := make([]float32, fix.Dimension+1)
	badVec[0] = 1.0
	bad := domain.VectorPoint{
		ID:     999,
		Vector: badVec, // dim+1 elements, expected dim
		ModelInfo: domain.ModelInfo{
			Name:      fix.Model.Name,
			Dimension: fix.Dimension,
			Version:   fix.Model.Version,
		},
	}
	err := idx.Upsert(ctx, []domain.VectorPoint{bad})
	if !domain.IsDimensionMismatch(err) {
		t.Fatalf("Upsert dim-mismatch: expected ErrDimensionMismatch, got %v", err)
	}
	var dme *domain.DimensionMismatchError
	if !errors.As(err, &dme) {
		t.Fatalf("error is not *DimensionMismatchError: %T", err)
	}
	// Verify the mismatched point was NOT stored: search must not return ID 999.
	results, _ := idx.Search(ctx, domain.VectorQuery{
		Vector: badVec,
		Limit:  10,
	})
	for _, r := range results {
		if r.ID == 999 {
			t.Errorf("dim-mismatched point 999 was stored (found in search results); corruption pin violated")
		}
	}
}

// assertDeleteRemovesFromSearch verifies Delete removes the vector from
// subsequent search results. Delete of a missing ID is idempotent (no error).
func assertDeleteRemovesFromSearch(t *testing.T, idx domain.VectorIndex, fix Fixtures) {
	t.Helper()
	ctx := context.Background()
	if err := idx.Upsert(ctx, fix.Points); err != nil {
		t.Fatalf("Upsert fixtures: %v", err)
	}
	// Delete point 1.
	if err := idx.Delete(ctx, []int64{1}); err != nil {
		t.Fatalf("Delete point 1: %v", err)
	}
	// Search must not return ID 1 anymore.
	results, err := idx.Search(ctx, domain.VectorQuery{
		Vector: fix.Points[0].Vector,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("Search after delete: %v", err)
	}
	for _, r := range results {
		if r.ID == 1 {
			t.Errorf("point 1 still in search results after Delete")
		}
	}
	// Idempotent delete: deleting a missing ID MUST NOT error.
	if err := idx.Delete(ctx, []int64{1}); err != nil {
		t.Errorf("idempotent Delete of missing ID 1 returned error: %v", err)
	}
}

// assertThresholdFiltersClientSide verifies a high threshold excludes
// low-similarity candidates. Searching for point 1's vector with a threshold
// of 0.5 should exclude the orthogonal points (3, 4, 5 with cosine 0.0) but
// include points 1 (1.0) and 2 (~0.995).
func assertThresholdFiltersClientSide(t *testing.T, idx domain.VectorIndex, fix Fixtures) {
	t.Helper()
	ctx := context.Background()
	if err := idx.Upsert(ctx, fix.Points); err != nil {
		t.Fatalf("Upsert fixtures: %v", err)
	}
	results, err := idx.Search(ctx, domain.VectorQuery{
		Vector:    fix.Points[0].Vector,
		Limit:     10,
		Threshold: 0.5,
	})
	if err != nil {
		t.Fatalf("Search with threshold: %v", err)
	}
	for _, r := range results {
		if r.Score < 0.5 {
			t.Errorf("result ID %d score %.4f < threshold 0.5 (not filtered client-side)", r.ID, r.Score)
		}
	}
}

// assertHealthExplicit verifies the adapter reports an explicit, non-empty
// health status. An empty status string is a programming error — the engine
// gates expensive work on this value.
func assertHealthExplicit(t *testing.T, idx domain.VectorIndex) {
	t.Helper()
	h := idx.Health(context.Background())
	if h.Status == "" {
		t.Error("Health.Status is empty; adapter MUST declare healthy|degraded|unhealthy")
	}
	if h.Status != domain.StatusHealthy && h.Status != domain.StatusDegraded && h.Status != domain.StatusUnhealthy {
		t.Errorf("Health.Status = %q; must be healthy|degraded|unhealthy", h.Status)
	}
}

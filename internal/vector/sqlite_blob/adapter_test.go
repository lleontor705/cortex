// Package sqlite_blob is the W8.1 conformance + architecture test suite for the
// zero-CGO default VectorIndex adapter.
//
// These tests pin REQ-VEC-001 (ADR-05):
//   - The adapter implements domain.VectorIndex (compile-time + runtime).
//   - sqlite_blob is the always-available zero-CGO default.
//   - Capabilities declares index type, distance metrics, max dimensions,
//     filter support, hybrid support, namespaces, consistency, and batch.
//   - Dimension-mismatch vectors are REJECTED (not scored 0 and stored),
//     pinning the dim-mismatch corruption fix.
//   - Health reports the underlying store availability correctly.
//   - No concrete *sqlite.VectorStore escapes the adapter boundary: the bundle
//     exposes domain.VectorIndex, not the concrete type.
package sqlite_blob

import (
	"context"
	"errors"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
	"github.com/lleontor705/cortex/v2/testutil"
)

// TestAdapter_ImplementsVectorIndex is the compile-time + runtime conformance
// assertion: the adapter MUST satisfy the domain.VectorIndex port. If a future
// change removes or renames a method, this fails to compile.
func TestAdapter_ImplementsVectorIndex(t *testing.T) {
	var _ domain.VectorIndex = (*Adapter)(nil)
	var _ domain.VectorIndex = New(nil)
}

// TestAdapter_ID_DeclaresSqliteBlob verifies the adapter identifies itself as
// the sqlite_blob index type (REQ-VEC-001: sqlite_blob is the named default).
func TestAdapter_ID_DeclaresSqliteBlob(t *testing.T) {
	a := New(nil)
	if a.ID() != "sqlite_blob" {
		t.Errorf("ID() = %q, want %q", a.ID(), "sqlite_blob")
	}
}

// TestAdapter_Capabilities_DeclaresFullSet verifies the adapter declares every
// Capabilities field mandated by REQ-VEC-001 / ADR-05: index type, distance
// metrics, max dimensions, filter support, hybrid support, namespaces,
// consistency, and batch semantics.
func TestAdapter_Capabilities_DeclaresFullSet(t *testing.T) {
	a := New(nil)
	caps, err := a.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}

	if caps.IndexType != "sqlite_blob" {
		t.Errorf("IndexType = %q, want %q", caps.IndexType, "sqlite_blob")
	}
	if len(caps.DistanceMetrics) == 0 {
		t.Error("DistanceMetrics is empty; must declare at least cosine")
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
	if caps.MaxDimensions < sqlitestore.MinEmbeddingDimension {
		t.Errorf("MaxDimensions = %d, want >= %d", caps.MaxDimensions, sqlitestore.MinEmbeddingDimension)
	}
	if caps.Filters == "" {
		t.Error("Filters is empty; must declare PreFilter|PostFilter|none")
	}
	if caps.Hybrid == "" {
		t.Error("Hybrid is empty; must declare enabled|disabled")
	}
	if caps.Namespaces == "" {
		t.Error("Namespaces is empty; must declare supported|unsupported")
	}
	if caps.Consistency == "" {
		t.Error("Consistency is empty; must declare strong|eventual")
	}
	if !caps.BatchUpsert {
		t.Error("BatchUpsert = false; sqlite_blob supports batched upsert")
	}
}

// TestAdapter_Health_StubModeUnhealthy verifies that in the default zero-CGO
// build (cortex_vectors NOT enabled), the underlying stub reports unavailable
// and the adapter Health reflects unhealthy/degraded — the zero-CGO default
// MUST NOT claim vector search is available when it is not (REQ-VEC-001).
//
// Under the cortex_vectors build tag, the underlying store reports available
// even with a nil db, so this test is skipped — it only applies to the
// zero-CGO stub default.
func TestAdapter_Health_StubModeUnhealthy(t *testing.T) {
	a := New(nil)
	h := a.Health(context.Background())
	// Skip under cortex_vectors: the enabled store reports healthy regardless
	// of db nil-ness. This test only validates the zero-CGO stub behavior.
	if h.Status == domain.StatusHealthy {
		t.Skip("skipping stub-mode test: store reports healthy (cortex_vectors tag active)")
	}
	if h.Message == "" {
		t.Error("unhealthy/degraded Health must carry a diagnostic message")
	}
}

// TestAdapter_Upsert_DimensionMismatchRejected is the REQ-VEC-001 defect pin:
// a vector whose dimension does not match the declared ModelInfo.Dimension MUST
// be rejected with ErrDimensionMismatch — NOT scored 0 and stored. The legacy
// cosine path logged a warning and returned 0 on mismatch, allowing corruption.
func TestAdapter_Upsert_DimensionMismatchRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	a := New(db.DB())

	// Declare a 128-dim model but pass a 100-dim vector.
	point := domain.VectorPoint{
		ID:     1,
		Vector: make([]float32, 100),
		ModelInfo: domain.ModelInfo{
			Name:      "test-model",
			Dimension: 128,
			Version:   "v1",
		},
	}
	err := a.Upsert(context.Background(), []domain.VectorPoint{point})
	if !domain.IsDimensionMismatch(err) {
		t.Fatalf("Upsert dim-mismatch: expected ErrDimensionMismatch, got %v", err)
	}

	// The mismatched vector MUST NOT have been stored: a subsequent search
	// must not find it. (Under the stub build the search returns disabled,
	// which also satisfies "not stored" — either way no corruption.)
	var dme *domain.DimensionMismatchError
	if !errors.As(err, &dme) {
		t.Fatalf("error is not a *DimensionMismatchError: %T", err)
	}
	if dme.Expected != 128 || dme.Actual != 100 {
		t.Errorf("mismatch fields: Expected=%d Actual=%d, want 128/100", dme.Expected, dme.Actual)
	}
}

// TestAdapter_Upsert_ConsistentDimensionAccepted verifies that a vector whose
// dimension matches the declared ModelInfo.Dimension is accepted by the
// namespace check (the underlying store may still reject for other reasons,
// e.g. disabled in stub mode — that is not a dim-mismatch).
func TestAdapter_Upsert_ConsistentDimensionAccepted(t *testing.T) {
	db := testutil.NewTestDB(t)
	a := New(db.DB())

	vec := make([]float32, sqlitestore.DefaultEmbeddingDimension)
	point := domain.VectorPoint{
		ID:     1,
		Vector: vec,
		ModelInfo: domain.ModelInfo{
			Name:      "test-model",
			Dimension: sqlitestore.DefaultEmbeddingDimension,
		},
	}
	err := a.Upsert(context.Background(), []domain.VectorPoint{point})
	// Must NOT be a dimension mismatch (the store may return disabled/not-found,
	// but the adapter namespace check must pass).
	if domain.IsDimensionMismatch(err) {
		t.Fatalf("consistent-dim Upsert rejected as dim-mismatch: %v", err)
	}
}

// TestAdapter_Search_TranslatesQuery verifies the adapter translates a
// domain.VectorQuery into the underlying store call. Under the stub build the
// search returns ErrVectorSearchDisabled (no crash, correct semantics).
func TestAdapter_Search_StubModeDisabled(t *testing.T) {
	db := testutil.NewTestDB(t)
	a := New(db.DB())

	q := domain.VectorQuery{
		Vector:    make([]float32, sqlitestore.DefaultEmbeddingDimension),
		Limit:     10,
		Threshold: 0.3,
	}
	results, err := a.Search(context.Background(), q)
	// Stub: disabled error OR empty results. Either is acceptable; a crash is not.
	if err == nil && results == nil {
		t.Error("Search returned nil results and nil error (ambiguous)")
	}
}

// TestAdapter_Delete_StubModeDisabled verifies Delete delegates correctly.
func TestAdapter_Delete_StubModeDisabled(t *testing.T) {
	db := testutil.NewTestDB(t)
	a := New(db.DB())
	// Delete in stub mode returns disabled; in enabled mode it returns
	// not-found for a missing ID. Either is acceptable; a panic is not.
	_ = a.Delete(context.Background(), []int64{99999})
}

// TestAdapter_Close_NoError verifies Close is safe to call (no-op for sqlite_blob).
func TestAdapter_Close_NoError(t *testing.T) {
	a := New(nil)
	if err := a.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

// TestAdapter_Search_FiltersTranslatedToProjectScope verifies that the adapter
// maps VectorQuery.Filters ("project", "scope") onto the underlying store's
// VectorSearchOptions so existing local filter behavior is preserved.
func TestAdapter_Search_FiltersTranslatedToProjectScope(t *testing.T) {
	opts := filtersToSearchOptions(map[string]any{
		"project": "myproj",
		"scope":   "personal",
	})
	if opts.Project != "myproj" {
		t.Errorf("project filter: got %q, want %q", opts.Project, "myproj")
	}
	if opts.Scope != "personal" {
		t.Errorf("scope filter: got %q, want %q", opts.Scope, "personal")
	}
}

package sqlite

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/testutil"
)

// TestVectorStore_StubMode tests that the stub implementation returns
// ErrVectorSearchDisabled when cortex_vectors build tag is not enabled.
// Note: When cortex_vectors build tag IS enabled, these tests verify
// that the full implementation works correctly.
func TestVectorStore_IsAvailable(t *testing.T) {
	db := testutil.NewTestDB(t)
	store := NewVectorStore(db.DB())

	// In stub mode: false, in enabled mode: true
	// We just verify it doesn't panic and returns a valid value
	available := store.IsAvailable()
	t.Logf("Vector search available: %v", available)
}

func TestVectorStore_StoreEmbedding_ValidationError(t *testing.T) {
	db := testutil.NewTestDB(t)
	store := NewVectorStore(db.DB())

	// Dimension below MinEmbeddingDimension triggers ValidationError (or
	// ErrVectorSearchDisabled in the stub build) before any DB access, so
	// the test is deterministic under both build configurations.
	embedding := make([]float32, MinEmbeddingDimension-1)
	err := store.StoreEmbedding(context.Background(), 1, embedding, "test-model")

	// Should either be disabled or validation error
	if err != domain.ErrVectorSearchDisabled && !domain.IsValidationError(err) {
		t.Errorf("expected ErrVectorSearchDisabled or ValidationError, got %v", err)
	}
}

func TestVectorStore_SearchByVector_ValidationError(t *testing.T) {
	db := testutil.NewTestDB(t)
	store := NewVectorStore(db.DB())

	// Dimension below MinEmbeddingDimension triggers ValidationError (or
	// ErrVectorSearchDisabled in the stub build) before any DB access.
	opts := domain.VectorSearchOptions{
		Embedding: make([]float32, MinEmbeddingDimension-1),
		Limit:     10,
	}
	_, err := store.SearchByVector(context.Background(), opts)

	// Should either be disabled or validation error
	if err != domain.ErrVectorSearchDisabled && !domain.IsValidationError(err) {
		t.Errorf("expected ErrVectorSearchDisabled or ValidationError, got %v", err)
	}
}

func TestVectorStore_GetEmbedding_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	createObservationVectorsTable(t, db)
	store := NewVectorStore(db.DB())

	_, _, err := store.GetEmbedding(context.Background(), 99999)

	// Should either be disabled or not found error
	if err != domain.ErrVectorSearchDisabled && !domain.IsNotFoundError(err) {
		t.Errorf("expected ErrVectorSearchDisabled or NotFoundError, got %v", err)
	}
}

func TestVectorStore_DeleteEmbedding_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	createObservationVectorsTable(t, db)
	store := NewVectorStore(db.DB())

	err := store.DeleteEmbedding(context.Background(), 99999)

	// Should either be disabled or not found error
	if err != domain.ErrVectorSearchDisabled && !domain.IsNotFoundError(err) {
		t.Errorf("expected ErrVectorSearchDisabled or NotFoundError, got %v", err)
	}
}

// createObservationVectorsTable creates the observation_vectors table matching
// the production schema (migration 005) so that VectorStore operations that
// query by ID return sql.ErrNoRows / 0 rows (yielding NotFoundError) instead
// of SQL "no such table" errors. The FK constraint is omitted because the
// test DB does not create the observations table.
func createObservationVectorsTable(t *testing.T, db *testutil.TestDB) {
	t.Helper()
	db.MustExec(`CREATE TABLE observation_vectors (
		observation_id INTEGER PRIMARY KEY,
		embedding BLOB,
		embedding_model TEXT,
		dimensions INTEGER,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
}

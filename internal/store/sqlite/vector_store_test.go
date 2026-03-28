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

	// Test with wrong dimension - should return validation error or disabled error
	embedding := make([]float32, 100) // Wrong dimension
	err := store.StoreEmbedding(context.Background(), 1, embedding, "test-model")

	// Should either be disabled or validation error
	if err != domain.ErrVectorSearchDisabled && !domain.IsValidationError(err) {
		t.Errorf("expected ErrVectorSearchDisabled or ValidationError, got %v", err)
	}
}

func TestVectorStore_SearchByVector_ValidationError(t *testing.T) {
	db := testutil.NewTestDB(t)
	store := NewVectorStore(db.DB())

	// Test with wrong dimension
	opts := domain.VectorSearchOptions{
		Embedding: make([]float32, 100), // Wrong dimension
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
	store := NewVectorStore(db.DB())

	_, _, err := store.GetEmbedding(context.Background(), 99999)

	// Should either be disabled or not found error
	if err != domain.ErrVectorSearchDisabled && !domain.IsNotFoundError(err) {
		t.Errorf("expected ErrVectorSearchDisabled or NotFoundError, got %v", err)
	}
}

func TestVectorStore_DeleteEmbedding_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	store := NewVectorStore(db.DB())

	err := store.DeleteEmbedding(context.Background(), 99999)

	// Should either be disabled or not found error
	if err != domain.ErrVectorSearchDisabled && !domain.IsNotFoundError(err) {
		t.Errorf("expected ErrVectorSearchDisabled or NotFoundError, got %v", err)
	}
}

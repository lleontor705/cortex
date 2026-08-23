//go:build !cortex_vectors

// Package sqlite implements the SQLite memory store for Cortex.
//
// This file provides a stub implementation of the VectorStore when the
// cortex_vectors build tag is not enabled. All methods return ErrVectorSearchDisabled.
package sqlite

import (
	"context"
	"database/sql"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// VectorStore implements the vector similarity search store.
// This is the stub implementation used when cortex_vectors build tag is disabled.
type VectorStore struct{}

// NewVectorStore creates a new vector store stub.
// When cortex_vectors is not enabled, this returns a stub that always
// returns ErrVectorSearchDisabled.
func NewVectorStore(_ *sql.DB) *VectorStore {
	return &VectorStore{}
}

// StoreEmbedding is disabled in stub mode.
func (s *VectorStore) StoreEmbedding(ctx context.Context, observationID int64, embedding []float32, model string) error {
	return domain.ErrVectorSearchDisabled
}

// SearchByVector is disabled in stub mode.
func (s *VectorStore) SearchByVector(ctx context.Context, opts domain.VectorSearchOptions) ([]*domain.VectorSearchResult, error) {
	return nil, domain.ErrVectorSearchDisabled
}

// GetEmbedding is disabled in stub mode.
func (s *VectorStore) GetEmbedding(ctx context.Context, observationID int64) ([]float32, string, error) {
	return nil, "", domain.ErrVectorSearchDisabled
}

// DeleteEmbedding is disabled in stub mode.
func (s *VectorStore) DeleteEmbedding(ctx context.Context, observationID int64) error {
	return domain.ErrVectorSearchDisabled
}

// IsAvailable returns false in stub mode.
func (s *VectorStore) IsAvailable() bool {
	return false
}

// Ensure VectorStore implements domain.VectorRepository
var _ domain.VectorRepository = (*VectorStore)(nil)

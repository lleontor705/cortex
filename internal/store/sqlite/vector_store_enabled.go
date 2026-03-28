//go:build cortex_vectors

// Package sqlite implements the SQLite memory store for Cortex.
//
// This file provides the full vector similarity search implementation when
// the cortex_vectors build tag is enabled. It uses cosine similarity for
// semantic search with 384-dimensional embeddings.
package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// EmbeddingDimension is the expected dimension for embeddings.
// This is set to 384 to match common sentence-transformers models
// like all-MiniLM-L6-v2.
const EmbeddingDimension = 384

// VectorStore implements the vector similarity search store.
// This is the full implementation used when cortex_vectors build tag is enabled.
type VectorStore struct {
	db *sql.DB
}

// NewVectorStore creates a new vector store with the given database connection.
// The database must have the observation_vectors table created by migration 005.
func NewVectorStore(db *sql.DB) *VectorStore {
	return &VectorStore{db: db}
}

// StoreEmbedding stores an embedding vector for an observation.
// The embedding must have exactly EmbeddingDimension (384) dimensions.
// The model parameter identifies the embedding model used (e.g., "all-MiniLM-L6-v2").
func (s *VectorStore) StoreEmbedding(ctx context.Context, observationID int64, embedding []float32, model string) error {
	// Validate embedding dimension
	if len(embedding) != EmbeddingDimension {
		return &domain.ValidationError{
			Field:   "embedding",
			Message: fmt.Sprintf("embedding must have %d dimensions, got %d", EmbeddingDimension, len(embedding)),
		}
	}

	// Serialize embedding to binary format (little-endian float32)
	embeddingBlob, err := serializeEmbedding(embedding)
	if err != nil {
		return fmt.Errorf("vector store: serialize embedding: %w", err)
	}

	// Upsert the embedding
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO observation_vectors (observation_id, embedding, embedding_model, dimensions, updated_at)
		VALUES (?, ?, ?, ?, datetime('now'))
		ON CONFLICT(observation_id) DO UPDATE SET
			embedding = excluded.embedding,
			embedding_model = excluded.embedding_model,
			dimensions = excluded.dimensions,
			updated_at = datetime('now')
	`, observationID, embeddingBlob, model, EmbeddingDimension)
	if err != nil {
		return fmt.Errorf("vector store: store embedding: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("vector store: get rows affected: %w", err)
	}

	if rows == 0 {
		return &domain.NotFoundError{Type: "observation", ID: observationID}
	}

	return nil
}

// SearchByVector performs a similarity search using cosine distance.
// Results are sorted by similarity score (descending) and filtered by threshold.
func (s *VectorStore) SearchByVector(ctx context.Context, opts domain.VectorSearchOptions) ([]*domain.VectorSearchResult, error) {
	// Validate options
	if len(opts.Embedding) != EmbeddingDimension {
		return nil, &domain.ValidationError{
			Field:   "embedding",
			Message: fmt.Sprintf("query embedding must have %d dimensions, got %d", EmbeddingDimension, len(opts.Embedding)),
		}
	}

	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	if opts.Limit > 100 {
		opts.Limit = 100
	}

	if opts.Threshold < 0 {
		opts.Threshold = 0
	}
	if opts.Threshold > 1 {
		opts.Threshold = 1
	}

	// Normalize the query embedding for cosine similarity
	queryNorm := normalizeVector(opts.Embedding)

	// Query all embeddings with observation data
	query := `
		SELECT 
			o.id, o.session_id, o.type, o.title, o.content, o.project, o.scope, 
			o.topic_key, o.created_at, o.updated_at,
			ov.embedding, ov.embedding_model
		FROM observation_vectors ov
		JOIN observations o ON o.id = ov.observation_id
		WHERE o.deleted_at IS NULL
	`
	args := []any{}

	// Apply filters
	if opts.Project != "" {
		query += " AND o.project = ?"
		args = append(args, opts.Project)
	}
	if opts.Scope != "" {
		query += " AND o.scope = ?"
		args = append(args, normalizeScope(opts.Scope))
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("vector store: search query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Calculate similarity for each result
	results := make([]*domain.VectorSearchResult, 0, opts.Limit)
	for rows.Next() {
		result, similarity, err := s.scanVectorResultWithSimilarity(rows, queryNorm)
		if err != nil {
			return nil, err
		}

		// Apply threshold filter
		if similarity >= opts.Threshold {
			result.Similarity = similarity
			results = append(results, result)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vector store: iterate results: %w", err)
	}

	// Sort by similarity (descending)
	sortBySimilarity(results)

	// Apply limit
	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	return results, nil
}

// GetEmbedding retrieves the embedding for an observation.
// Returns the embedding vector and the model name used to generate it.
func (s *VectorStore) GetEmbedding(ctx context.Context, observationID int64) ([]float32, string, error) {
	var embeddingBlob []byte
	var model string

	err := s.db.QueryRowContext(ctx, `
		SELECT embedding, embedding_model
		FROM observation_vectors
		WHERE observation_id = ?
	`, observationID).Scan(&embeddingBlob, &model)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, "", &domain.NotFoundError{Type: "embedding", ID: observationID}
		}
		return nil, "", fmt.Errorf("vector store: get embedding: %w", err)
	}

	embedding, err := deserializeEmbedding(embeddingBlob)
	if err != nil {
		return nil, "", fmt.Errorf("vector store: deserialize embedding: %w", err)
	}

	return embedding, model, nil
}

// DeleteEmbedding removes the embedding for an observation.
func (s *VectorStore) DeleteEmbedding(ctx context.Context, observationID int64) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM observation_vectors WHERE observation_id = ?
	`, observationID)
	if err != nil {
		return fmt.Errorf("vector store: delete embedding: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("vector store: get rows affected: %w", err)
	}

	if rows == 0 {
		return &domain.NotFoundError{Type: "embedding", ID: observationID}
	}

	return nil
}

// IsAvailable returns true when vector search is enabled.
func (s *VectorStore) IsAvailable() bool {
	return true
}

// ─── Helper Functions ───────────────────────────────────────────────────────

// serializeEmbedding converts a float32 slice to a binary BLOB.
// Uses little-endian byte order for cross-platform compatibility.
func serializeEmbedding(embedding []float32) ([]byte, error) {
	buf := make([]byte, len(embedding)*4)
	for i, v := range embedding {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf, nil
}

// deserializeEmbedding converts a binary BLOB back to a float32 slice.
func deserializeEmbedding(data []byte) ([]float32, error) {
	dimension := len(data) / 4 // float32 is 4 bytes
	if dimension == 0 {
		return nil, fmt.Errorf("empty embedding data")
	}

	embedding := make([]float32, dimension)
	buf := bytes.NewReader(data)
	for i := range embedding {
		if err := binary.Read(buf, binary.LittleEndian, &embedding[i]); err != nil {
			return nil, err
		}
	}
	return embedding, nil
}

// normalizeVector normalizes a vector to unit length for cosine similarity.
// Returns a new slice; does not modify the input.
func normalizeVector(v []float32) []float64 {
	// Calculate L2 norm
	var sumSq float64
	for _, x := range v {
		sumSq += float64(x) * float64(x)
	}
	norm := math.Sqrt(sumSq)

	// Avoid division by zero
	if norm < 1e-10 {
		normalized := make([]float64, len(v))
		return normalized
	}

	// Normalize to unit vector
	normalized := make([]float64, len(v))
	for i, x := range v {
		normalized[i] = float64(x) / norm
	}
	return normalized
}

// cosineSimilarity calculates the cosine similarity between two normalized vectors.
// Both vectors should be pre-normalized for efficiency.
func cosineSimilarity(a []float64, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct float64
	for i := range a {
		dotProduct += a[i] * float64(b[i])
	}

	// Since both vectors are normalized, cosine similarity = dot product
	return dotProduct
}

// computeCosineSimilarity computes cosine similarity between a normalized query
// and a raw embedding (which will be normalized during computation).
func computeCosineSimilarity(queryNorm []float64, embedding []float32) float64 {
	// Normalize the embedding
	embNorm := normalizeVector(embedding)

	// Compute dot product of normalized vectors
	return cosineSimilarity(queryNorm, embNorm)
}

// sortBySimilarity sorts results by similarity score in descending order.
func sortBySimilarity(results []*domain.VectorSearchResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})
}

// scanVectorResultWithSimilarity scans a row and computes similarity.
func (s *VectorStore) scanVectorResultWithSimilarity(rows *sql.Rows, queryNorm []float64) (*domain.VectorSearchResult, float64, error) {
	var result domain.VectorSearchResult
	var createdAtStr, updatedAtStr string
	var topicKey sql.NullString
	var project sql.NullString
	var embeddingBlob []byte
	var model string

	err := rows.Scan(
		&result.ID, &result.SessionID, &result.Type, &result.Title, &result.Content,
		&project, &result.Scope, &topicKey,
		&createdAtStr, &updatedAtStr,
		&embeddingBlob, &model,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("vector store: scan result: %w", err)
	}

	result.Project = project.String
	if topicKey.Valid {
		result.TopicKey = topicKey.String
	}
	result.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
	result.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)

	// Deserialize embedding and compute similarity
	embedding, err := deserializeEmbedding(embeddingBlob)
	if err != nil {
		return nil, 0, fmt.Errorf("vector store: deserialize embedding: %w", err)
	}

	similarity := computeCosineSimilarity(queryNorm, embedding)

	return &domain.VectorSearchResult{
		Observation: result,
		Similarity:  similarity,
	}, similarity, nil
}

// Ensure VectorStore implements domain.VectorRepository
var _ domain.VectorRepository = (*VectorStore)(nil)

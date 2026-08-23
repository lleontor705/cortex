//go:build cortex_vectors

// Package sqlite implements the SQLite memory store for Cortex.
//
// This file provides the full vector similarity search implementation when
// the cortex_vectors build tag is enabled. It uses cosine similarity for
// semantic search with 384-dimensional embeddings.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"sort"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// VectorStore implements the vector similarity search store.
// This is the full implementation used when cortex_vectors build tag is enabled.
//
// Embedding dimension bounds (DefaultEmbeddingDimension,
// MinEmbeddingDimension, MaxEmbeddingDimension) are declared in
// vector_constants.go (build-tag-agnostic) so the sqlite_blob adapter can
// reference them under both builds.
type VectorStore struct {
	db *sql.DB
}

// NewVectorStore creates a new vector store with the given database connection.
// The database must have the observation_vectors table created by migration 005.
func NewVectorStore(db *sql.DB) *VectorStore {
	return &VectorStore{db: db}
}

// StoreEmbedding stores an embedding vector for an observation.
// The embedding must have between MinEmbeddingDimension and MaxEmbeddingDimension dimensions.
// The model parameter identifies the embedding model used (e.g., "all-MiniLM-L6-v2").
func (s *VectorStore) StoreEmbedding(ctx context.Context, observationID int64, embedding []float32, model string) error {
	// Validate embedding dimension (flexible — accept any reasonable size)
	dims := len(embedding)
	if dims < MinEmbeddingDimension || dims > MaxEmbeddingDimension {
		return &domain.ValidationError{
			Field:   "embedding",
			Message: fmt.Sprintf("embedding must have %d-%d dimensions, got %d", MinEmbeddingDimension, MaxEmbeddingDimension, dims),
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
	`, observationID, embeddingBlob, model, dims)
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
	dims := len(opts.Embedding)
	if dims < MinEmbeddingDimension || dims > MaxEmbeddingDimension {
		return nil, &domain.ValidationError{
			Field:   "embedding",
			Message: fmt.Sprintf("query embedding must have %d-%d dimensions, got %d", MinEmbeddingDimension, MaxEmbeddingDimension, dims),
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

	// Calculate similarity for each result. The scratch buffer (reused
	// across rows, call-local so concurrent searches never share it)
	// eliminates the per-row embedding/normalization allocations of the
	// previous pipeline.
	var scratch []float64
	results := make([]*domain.VectorSearchResult, 0, opts.Limit)
	for rows.Next() {
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
			return nil, fmt.Errorf("vector store: scan result: %w", err)
		}

		result.Project = project.String
		if topicKey.Valid {
			result.TopicKey = topicKey.String
		}
		result.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		result.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)

		var similarity float64
		similarity, scratch, err = similarityFromEmbeddingBlob(queryNorm, embeddingBlob, scratch)
		if err != nil {
			return nil, fmt.Errorf("vector store: deserialize embedding: %w", err)
		}

		// Apply threshold filter
		if similarity >= opts.Threshold {
			result.Similarity = similarity
			results = append(results, &result)
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

// --- Helper Functions -------------------------------------------------------

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
// It decodes each element with the same math.Float32frombits(
// binary.LittleEndian.Uint32(...)) conversion the previous bytes.NewReader +
// per-element binary.Read pipeline performed, bit-for-bit, without the
// per-element reflection overhead. Trailing bytes of a blob whose length is
// not a multiple of 4 are ignored, exactly like the previous reader-based
// decode, which consumed only len(data)/4 complete floats.
func deserializeEmbedding(data []byte) ([]float32, error) {
	dimension := len(data) / 4 // float32 is 4 bytes
	if dimension == 0 {
		return nil, fmt.Errorf("empty embedding data")
	}

	embedding := make([]float32, dimension)
	for i := range embedding {
		embedding[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
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
		log.Printf("warning: cosine similarity dimension mismatch: query=%d stored=%d", len(a), len(b))
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
	// Normalize the embedding to float64
	norm := 0.0
	embF64 := make([]float64, len(embedding))
	for i, v := range embedding {
		embF64[i] = float64(v)
		norm += embF64[i] * embF64[i]
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range embF64 {
			embF64[i] /= norm
		}
	}

	// Dot product of normalized vectors
	if len(queryNorm) != len(embF64) {
		log.Printf("warning: cosine similarity dimension mismatch: query=%d stored=%d", len(queryNorm), len(embF64))
		return 0
	}
	var dot float64
	for i := range queryNorm {
		dot += queryNorm[i] * embF64[i]
	}
	return dot
}

// similarityFromEmbeddingBlob computes the cosine similarity between the
// normalized query and a raw little-endian float32 embedding BLOB without
// materializing an intermediate []float32 per row.
//
// It is bit-exact with the previous deserializeEmbedding +
// computeCosineSimilarity pipeline: every element is decoded with the same
// float32 bit conversion, widened to float64, squared/accumulated in the same
// element order, normalized with the same float64 division per element, and
// dot-accumulated in the same order. scratch is reused across rows of one
// scan and grown on demand; it MUST NOT be shared between concurrent calls
// (SearchByVector allocates it per invocation).
func similarityFromEmbeddingBlob(queryNorm []float64, data []byte, scratch []float64) (float64, []float64, error) {
	dimension := len(data) / 4
	if dimension == 0 {
		return 0, scratch, fmt.Errorf("empty embedding data")
	}
	if cap(scratch) < dimension {
		scratch = make([]float64, dimension)
	}
	emb := scratch[:dimension]

	var norm float64
	for i := range emb {
		v := math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
		emb[i] = float64(v)
		norm += emb[i] * emb[i]
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range emb {
			emb[i] /= norm
		}
	}

	// Since the query is normalized, cosine similarity = dot product.
	if len(queryNorm) != len(emb) {
		log.Printf("warning: cosine similarity dimension mismatch: query=%d stored=%d", len(queryNorm), len(emb))
		return 0, scratch, nil
	}
	var dot float64
	for i := range queryNorm {
		dot += queryNorm[i] * emb[i]
	}
	return dot, scratch, nil
}

// sortBySimilarity sorts results by similarity score in descending order.
func sortBySimilarity(results []*domain.VectorSearchResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})
}

// Ensure VectorStore implements domain.VectorRepository
var _ domain.VectorRepository = (*VectorStore)(nil)

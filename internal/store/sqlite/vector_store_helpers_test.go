//go:build cortex_vectors

package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"net/url"
	"sort"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/retrieval"

	_ "modernc.org/sqlite" // base driver
)

// TestSerializeEmbedding tests the embedding serialization/deserialization.
func TestSerializeEmbedding(t *testing.T) {
	t.Run("serialize and deserialize preserves values", func(t *testing.T) {
		original := make([]float32, DefaultEmbeddingDimension)
		for i := range original {
			original[i] = float32(i) * 0.001
		}

		data, err := serializeEmbedding(original)
		if err != nil {
			t.Fatalf("serialize failed: %v", err)
		}

		if len(data) != DefaultEmbeddingDimension*4 {
			t.Errorf("expected %d bytes, got %d", DefaultEmbeddingDimension*4, len(data))
		}

		recovered, err := deserializeEmbedding(data)
		if err != nil {
			t.Fatalf("deserialize failed: %v", err)
		}

		if len(recovered) != len(original) {
			t.Fatalf("expected %d elements, got %d", len(original), len(recovered))
		}

		for i := range original {
			if math.Abs(float64(recovered[i]-original[i])) > 1e-6 {
				t.Errorf("element %d: expected %f, got %f", i, original[i], recovered[i])
			}
		}
	})

	t.Run("empty data returns error", func(t *testing.T) {
		_, err := deserializeEmbedding([]byte{})
		if err == nil {
			t.Error("expected error for empty data")
		}
	})

	t.Run("handles negative values", func(t *testing.T) {
		original := []float32{-1.5, -0.5, 0.0, 0.5, 1.5}
		data, err := serializeEmbedding(original)
		if err != nil {
			t.Fatalf("serialize failed: %v", err)
		}

		recovered, err := deserializeEmbedding(data)
		if err != nil {
			t.Fatalf("deserialize failed: %v", err)
		}

		for i := range original {
			if math.Abs(float64(recovered[i]-original[i])) > 1e-6 {
				t.Errorf("element %d: expected %f, got %f", i, original[i], recovered[i])
			}
		}
	})
}

// TestNormalizeVector tests the vector normalization.
func TestNormalizeVector(t *testing.T) {
	t.Run("normalizes to unit length", func(t *testing.T) {
		v := []float32{3.0, 4.0, 0.0}
		norm := normalizeVector(v)

		// Calculate L2 norm of result
		var sumSq float64
		for _, x := range norm {
			sumSq += x * x
		}
		length := math.Sqrt(sumSq)

		if math.Abs(length-1.0) > 1e-6 {
			t.Errorf("normalized vector should have unit length, got %f", length)
		}
	})

	t.Run("zero vector returns zero vector", func(t *testing.T) {
		v := []float32{0.0, 0.0, 0.0}
		norm := normalizeVector(v)

		for i, x := range norm {
			if x != 0.0 {
				t.Errorf("zero vector element %d should be 0, got %f", i, x)
			}
		}
	})

	t.Run("preserves direction", func(t *testing.T) {
		v := []float32{1.0, 1.0, 0.0}
		norm := normalizeVector(v)

		// Normalized should have same direction
		if norm[0] != norm[1] {
			t.Errorf("normalized should preserve direction: %f != %f", norm[0], norm[1])
		}
	})

	t.Run("handles single element", func(t *testing.T) {
		v := []float32{5.0}
		norm := normalizeVector(v)

		if math.Abs(norm[0]-1.0) > 1e-6 {
			t.Errorf("single element should normalize to 1, got %f", norm[0])
		}
	})
}

// TestComputeCosineSimilarity tests cosine similarity calculation.
func TestComputeCosineSimilarity(t *testing.T) {
	t.Run("identical vectors have similarity 1", func(t *testing.T) {
		v := []float32{1.0, 2.0, 3.0}
		norm := normalizeVector(v)
		sim := computeCosineSimilarity(norm, v)
		if math.Abs(sim-1.0) > 1e-6 {
			t.Errorf("identical vectors should have similarity 1, got %f", sim)
		}
	})

	t.Run("orthogonal vectors have similarity 0", func(t *testing.T) {
		v1 := []float32{1.0, 0.0, 0.0}
		v2 := []float32{0.0, 1.0, 0.0}
		norm := normalizeVector(v1)
		sim := computeCosineSimilarity(norm, v2)
		if math.Abs(sim) > 1e-6 {
			t.Errorf("orthogonal vectors should have similarity 0, got %f", sim)
		}
	})

	t.Run("opposite vectors have similarity -1", func(t *testing.T) {
		v1 := []float32{1.0, 2.0, 3.0}
		v2 := []float32{-1.0, -2.0, -3.0}
		norm := normalizeVector(v1)
		sim := computeCosineSimilarity(norm, v2)
		if math.Abs(sim-(-1.0)) > 1e-6 {
			t.Errorf("opposite vectors should have similarity -1, got %f", sim)
		}
	})

	t.Run("partial similarity", func(t *testing.T) {
		v1 := []float32{1.0, 0.0, 0.0}
		v2 := []float32{1.0, 1.0, 0.0}
		norm := normalizeVector(v1)
		sim := computeCosineSimilarity(norm, v2)
		// Expected: 1 / sqrt(2) - 0.707
		expected := 1.0 / math.Sqrt(2)
		if math.Abs(sim-expected) > 1e-6 {
			t.Errorf("expected similarity %f, got %f", expected, sim)
		}
	})

	t.Run("different length vectors return 0", func(t *testing.T) {
		v1 := []float32{1.0, 2.0}
		v2 := []float32{1.0, 2.0, 3.0}
		norm := normalizeVector(v1)
		sim := cosineSimilarity(norm, v2) // Direct call to cosineSimilarity which checks length
		if sim != 0 {
			t.Errorf("different length vectors should have similarity 0, got %f", sim)
		}
	})
}

// TestEmbeddingDimension tests that the constant is set correctly.
func TestEmbeddingDimension(t *testing.T) {
	if DefaultEmbeddingDimension != 768 {
		t.Errorf("DefaultEmbeddingDimension should be 768, got %d", DefaultEmbeddingDimension)
	}
}

// ---------------------------------------------------------------------------
// SearchVectors full-pipeline oracle + benchmarks (VEC-01, cortex_vectors).
//
// The sqlite_blob adapter cannot be imported here (it imports this package,
// which would be an import cycle), so benchVectorIndex mirrors the adapter's
// Search translation over the same concrete VectorStore. The pipeline under
// test — capability-driven strategy, pool expansion, revalidation — is the
// REAL retrieval.SearchVectors.
// ---------------------------------------------------------------------------

const (
	// searchBenchRows/searchBenchDim mirror the #512 baseline fixture
	// (200 observations x 384-dim, PostFilter BLOB cosine scan).
	searchBenchRows = 200
	searchBenchDim  = 384
)

// vectorPipelineSchema extends the batch test schema with the v2 baseline
// observation_vectors table.
const vectorPipelineSchema = batchTestSchema + `
	CREATE TABLE IF NOT EXISTS observation_vectors (
		observation_id  INTEGER PRIMARY KEY,
		embedding       BLOB,
		embedding_model TEXT,
		dimensions      INTEGER,
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
`

// searchBenchEmbedding derives a deterministic 384-dim embedding for row i.
func searchBenchEmbedding(i, dim int) []float32 {
	v := make([]float32, dim)
	for d := 0; d < dim; d++ {
		v[d] = float32(1.0 + 0.001*float64((i*(d+1))%97))
	}
	return v
}

// seedSearchPipelineFixture inserts n observations and embeddings; the query
// vector equals row 0's embedding so row 0 ranks first.
func seedSearchPipelineFixture(tb testing.TB, db *sql.DB) []float32 {
	tb.Helper()
	for i := 0; i < searchBenchRows; i++ {
		if _, err := db.Exec(`
			INSERT INTO observations (session_id, type, title, content, project, scope, source)
			VALUES (?, 'manual', ?, ?, 'proj-vec', 'project', 'manual')
		`, "s-vec", fmt.Sprintf("vec-%04d", i), fmt.Sprintf("content %d", i)); err != nil {
			tb.Fatalf("vector fixture: insert obs %d: %v", i, err)
		}
	}
	vs := NewVectorStore(db)
	ctx := context.Background()
	for i := 0; i < searchBenchRows; i++ {
		if err := vs.StoreEmbedding(ctx, int64(i+1), searchBenchEmbedding(i, searchBenchDim), "bench-model"); err != nil {
			tb.Fatalf("vector fixture: store embedding %d: %v", i, err)
		}
	}
	return searchBenchEmbedding(0, searchBenchDim)
}

// benchVectorIndex mirrors sqlite_blob.Adapter over the concrete VectorStore
// (same PostFilter capabilities, same Search translation) without importing
// the adapter package.
type benchVectorIndex struct{ store *VectorStore }

var _ domain.VectorIndex = (*benchVectorIndex)(nil)

func (b *benchVectorIndex) ID() string { return "sqlite_blob" }

func (b *benchVectorIndex) Upsert(_ context.Context, _ []domain.VectorPoint) error { return nil }
func (b *benchVectorIndex) Delete(_ context.Context, _ []int64) error              { return nil }
func (b *benchVectorIndex) Close() error                                           { return nil }

func (b *benchVectorIndex) Health(_ context.Context) domain.Health {
	return domain.Health{Status: domain.StatusHealthy, Message: "bench"}
}

func (b *benchVectorIndex) Capabilities(_ context.Context) (domain.Capabilities, error) {
	return domain.Capabilities{
		IndexType: "sqlite_blob",
		Filters:   "PostFilter",
	}, nil
}

func (b *benchVectorIndex) Search(ctx context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	results, err := b.store.SearchByVector(ctx, domain.VectorSearchOptions{
		Embedding: q.Vector,
		Limit:     q.Limit,
		Threshold: q.Threshold,
	})
	if err != nil {
		return nil, err
	}
	candidates := make([]domain.VectorCandidate, 0, len(results))
	for _, r := range results {
		candidates = append(candidates, domain.VectorCandidate{
			ID:         r.ID,
			Score:      r.Similarity,
			Provenance: "sqlite_blob",
		})
	}
	return candidates, nil
}

// perIDOnlyLookup hides GetByIDs so retrieval.SearchVectors exercises the
// LEGACY per-ID hydration path over the same *Store (A/B benchmark control).
type perIDOnlyLookup struct{ inner *Store }

func (p perIDOnlyLookup) GetByID(ctx context.Context, id int64) (*domain.Observation, error) {
	return p.inner.GetByID(ctx, id)
}

// openSearchPipelineDB opens an in-memory DB with the production-style DSN
// pragmas (mirrors database.InMemoryConfig via buildDSN) so benchmark numbers
// are comparable with the #512 baseline methodology.
func openSearchPipelineDB(tb testing.TB) *sql.DB {
	tb.Helper()
	v := url.Values{}
	v.Add("_pragma", "busy_timeout=5000")
	v.Add("_pragma", "synchronous=NORMAL")
	v.Add("_pragma", "foreign_keys=ON")
	v.Add("_pragma", "temp_store=MEMORY")
	v.Add("_pragma", "cache_size=-64000")
	db, err := sql.Open("sqlite", ":memory:?"+v.Encode())
	if err != nil {
		tb.Fatalf("open search pipeline db: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	tb.Cleanup(func() { _ = db.Close() })
	return db
}

// TestSearchVectors_Limit50_SingleHydrationSQL pins the VEC-01 query budget
// on the full pipeline: limit=50 PostFilter search (pool 150, legacy clamp
// 100) must issue exactly ONE ANN scan plus ONE batch hydration statement.
func TestSearchVectors_Limit50_SingleHydrationSQL(t *testing.T) {
	db, counter := newCountingDB(t)
	if _, err := db.Exec(vectorPipelineSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	queryVec := seedSearchPipelineFixture(t, db)
	counter.reset()

	store := NewStore(db)
	idx := &benchVectorIndex{store: NewVectorStore(db)}
	results, err := retrieval.SearchVectors(context.Background(), idx, domain.VectorQuery{
		Vector: queryVec,
		Limit:  50,
	}, store)
	if err != nil {
		t.Fatalf("SearchVectors: %v", err)
	}
	if len(results) != 50 {
		t.Fatalf("expected 50 results, got %d", len(results))
	}
	if n := counter.value(); n != 2 {
		t.Fatalf("limit=50 PostFilter search must issue exactly 2 statements (1 ANN scan + 1 batch hydration), got %d", n)
	}
	// Row 0 shares the query embedding: it must rank first with similarity 1.
	if results[0].ID != 1 || math.Abs(results[0].Similarity-1.0) > 1e-6 {
		t.Fatalf("top result must be ID 1 with similarity 1.0, got %d/%f", results[0].ID, results[0].Similarity)
	}
}

// BenchmarkSearchVectorsLimit50 measures the full PostFilter pipeline with
// batch hydration (the new default: *Store implements BatchObservationLookup).
func BenchmarkSearchVectorsLimit50(b *testing.B) {
	db := openSearchPipelineDB(b)
	if _, err := db.Exec(vectorPipelineSchema); err != nil {
		b.Fatalf("schema: %v", err)
	}
	queryVec := seedSearchPipelineFixture(b, db)
	store := NewStore(db)
	idx := &benchVectorIndex{store: NewVectorStore(db)}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := retrieval.SearchVectors(ctx, idx, domain.VectorQuery{
			Vector: queryVec,
			Limit:  50,
		}, store); err != nil {
			b.Fatalf("SearchVectors: %v", err)
		}
	}
}

// BenchmarkSearchVectorsLimit50_LegacyHydration is the pre-batch control:
// identical pipeline with the per-ID-only lookup (100 GetByID statements).
func BenchmarkSearchVectorsLimit50_LegacyHydration(b *testing.B) {
	db := openSearchPipelineDB(b)
	if _, err := db.Exec(vectorPipelineSchema); err != nil {
		b.Fatalf("schema: %v", err)
	}
	queryVec := seedSearchPipelineFixture(b, db)
	store := NewStore(db)
	idx := &benchVectorIndex{store: NewVectorStore(db)}
	legacy := perIDOnlyLookup{inner: store}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := retrieval.SearchVectors(ctx, idx, domain.VectorQuery{
			Vector: queryVec,
			Limit:  50,
		}, legacy); err != nil {
			b.Fatalf("SearchVectors: %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// SearchByVector scan/decode optimization oracles (R1R5, cortex_vectors).
//
// The optimized production scan must be BIT-EXACT with the original
// binary.Read-based pipeline: identical candidate IDs, ordering, similarity
// bits, filters, thresholding, and hydrated fields. referenceSearchByVector
// below is a verbatim copy of the pre-optimization SearchByVector +
// scanVectorResultWithSimilarity + deserializeEmbedding + computeCosineSimilarity
// algorithm (bytes.NewReader + per-element binary.Read decode), kept as the
// frozen baseline oracle for the differential test.
// ---------------------------------------------------------------------------

// referenceNormalizeVector is the pre-optimization normalizeVector.
func referenceNormalizeVector(v []float32) []float64 {
	var sumSq float64
	for _, x := range v {
		sumSq += float64(x) * float64(x)
	}
	norm := math.Sqrt(sumSq)
	if norm < 1e-10 {
		normalized := make([]float64, len(v))
		return normalized
	}
	normalized := make([]float64, len(v))
	for i, x := range v {
		normalized[i] = float64(x) / norm
	}
	return normalized
}

// referenceDeserializeEmbedding is the pre-optimization deserializeEmbedding
// (bytes.NewReader + per-element binary.Read through reflection).
func referenceDeserializeEmbedding(data []byte) ([]float32, error) {
	dimension := len(data) / 4
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

// referenceComputeCosineSimilarity is the pre-optimization
// computeCosineSimilarity (per-row []float64 allocation + float64 division
// per element).
func referenceComputeCosineSimilarity(queryNorm []float64, embedding []float32) float64 {
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
	if len(queryNorm) != len(embF64) {
		return 0
	}
	var dot float64
	for i := range queryNorm {
		dot += queryNorm[i] * embF64[i]
	}
	return dot
}

// referenceSearchByVector is the frozen pre-optimization SearchByVector
// pipeline: same SQL, same scan order, same threshold/sort/truncate semantics.
func referenceSearchByVector(ctx context.Context, db *sql.DB, opts domain.VectorSearchOptions) ([]*domain.VectorSearchResult, error) {
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
	queryNorm := referenceNormalizeVector(opts.Embedding)

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
	if opts.Project != "" {
		query += " AND o.project = ?"
		args = append(args, opts.Project)
	}
	if opts.Scope != "" {
		query += " AND o.scope = ?"
		args = append(args, normalizeScope(opts.Scope))
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("vector store: search query: %w", err)
	}
	defer func() { _ = rows.Close() }()

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

		embedding, err := referenceDeserializeEmbedding(embeddingBlob)
		if err != nil {
			return nil, fmt.Errorf("vector store: deserialize embedding: %w", err)
		}
		similarity := referenceComputeCosineSimilarity(queryNorm, embedding)
		if similarity >= opts.Threshold {
			result.Similarity = similarity
			results = append(results, &result)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vector store: iterate results: %w", err)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})
	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	return results, nil
}

// diffFixtureVec derives a deterministic pseudo-random float32 vector.
func diffFixtureVec(rng *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = float32(rng.NormFloat64())
	}
	return v
}

// seedDifferentialFixture builds an adversarial 200x384-style fixture at the
// given dimension: alternating projects/scopes, NULL topic keys, exact
// duplicate vectors (tie ordering), a zero vector, NaN and +Inf vectors, a
// dimension-mismatched row, a soft-deleted row, an orphan vector row, and a
// trailing-garbage blob. Returns query vectors probing all edge classes.
func seedDifferentialFixture(t *testing.T, db *sql.DB, dim int) [][]float32 {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(dim) * 7919))
	ctx := context.Background()
	vs := NewVectorStore(db)

	insertObs := func(title, project, scope string, deleted bool) int64 {
		var deletedAt any
		if deleted {
			deletedAt = "2026-01-01T00:00:00Z"
		}
		res, err := db.Exec(`
			INSERT INTO observations (session_id, type, title, content, project, scope, source, topic_key, deleted_at)
			VALUES (?, 'manual', ?, ?, ?, ?, 'manual', ?, ?)
		`, "s-diff", title, "content "+title, project, scope, nil, deletedAt)
		if err != nil {
			t.Fatalf("diff fixture: insert obs %s: %v", title, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("diff fixture: last id: %v", err)
		}
		return id
	}

	n := 40
	vectors := make(map[int64][]float32, n+8)
	var firstID, secondID int64
	for i := 0; i < n; i++ {
		project, scope := "p1", "project"
		if i%2 == 1 {
			project = "p2"
		}
		if i%3 == 0 {
			scope = "personal"
		}
		id := insertObs(fmt.Sprintf("diff-%03d", i), project, scope, i == n-1)
		v := diffFixtureVec(rng, dim)
		vectors[id] = v
		if i == 0 {
			firstID = id
		}
		if i == 1 {
			secondID = id
		}
		if err := vs.StoreEmbedding(ctx, id, v, "diff-model"); err != nil {
			t.Fatalf("diff fixture: store embedding %d: %v", id, err)
		}
	}

	// Exact duplicate pair (bit-identical similarity ties).
	dup := diffFixtureVec(rng, dim)
	for _, title := range []string{"dup-a", "dup-b"} {
		id := insertObs(title, "p1", "project", false)
		vectors[id] = dup
		if err := vs.StoreEmbedding(ctx, id, dup, "diff-model"); err != nil {
			t.Fatalf("diff fixture: store dup embedding: %v", err)
		}
	}

	// Zero vector (zero norm: similarity exactly 0).
	id := insertObs("zero-vec", "p1", "project", false)
	vectors[id] = make([]float32, dim)
	if err := vs.StoreEmbedding(ctx, id, make([]float32, dim), "diff-model"); err != nil {
		t.Fatalf("diff fixture: store zero embedding: %v", err)
	}

	// NaN and +Inf vectors (NaN similarity is dropped by every threshold).
	for name, mut := range map[string]func([]float32){
		"nan-vec":  func(v []float32) { v[0] = float32(math.NaN()); v[dim/2] = float32(math.NaN()) },
		"inf-vec":  func(v []float32) { v[1] = float32(math.Inf(1)) },
		"infm-vec": func(v []float32) { v[2] = float32(math.Inf(-1)) },
	} {
		v := diffFixtureVec(rng, dim)
		mut(v)
		id := insertObs(name, "p2", "personal", false)
		vectors[id] = v
		if err := vs.StoreEmbedding(ctx, id, v, "diff-model"); err != nil {
			t.Fatalf("diff fixture: store %s: %v", name, err)
		}
	}

	// Dimension-mismatched row (scores 0 with a warning, still eligible at
	// threshold 0).
	mismatch := diffFixtureVec(rng, dim+2)
	id = insertObs("dim-mismatch", "p1", "project", false)
	vectors[id] = mismatch
	if err := vs.StoreEmbedding(ctx, id, mismatch, "diff-model"); err != nil {
		t.Fatalf("diff fixture: store mismatch embedding: %v", err)
	}

	// Orphan vector row: embedding without an observation (excluded by JOIN).
	{
		blob, err := serializeEmbedding(diffFixtureVec(rng, dim))
		if err != nil {
			t.Fatalf("diff fixture: serialize orphan: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO observation_vectors (observation_id, embedding, embedding_model, dimensions) VALUES (999999, ?, 'diff-model', ?)`, blob, dim); err != nil {
			t.Fatalf("diff fixture: insert orphan: %v", err)
		}
	}

	// Trailing-garbage blob: dim*4 valid bytes plus 3 junk bytes (both
	// implementations must ignore the trailing bytes).
	{
		id := insertObs("trailing-blob", "p1", "personal", false)
		v := diffFixtureVec(rng, dim)
		blob, err := serializeEmbedding(v)
		if err != nil {
			t.Fatalf("diff fixture: serialize trailing: %v", err)
		}
		blob = append(blob, 0xAB, 0xCD, 0xEF)
		vectors[id] = v
		if _, err := db.Exec(`INSERT INTO observation_vectors (observation_id, embedding, embedding_model, dimensions) VALUES (?, ?, 'diff-model', ?)`, id, blob, dim); err != nil {
			t.Fatalf("diff fixture: insert trailing: %v", err)
		}
	}

	queries := [][]float32{
		diffFixtureVec(rng, dim),         // generic random
		diffFixtureVec(rng, dim),         // second random
		dup,                              // exactly matches the duplicate pair
		vectors[firstID],                 // exactly matches the first row
		scaleVec(vectors[secondID], 3.5), // same direction as the second row
		make([]float32, dim),             // zero query (all similarities exactly 0)
	}
	return queries
}

// scaleVec returns a *new* vector scaled by s (same cosine direction).
func scaleVec(v []float32, s float64) []float32 {
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) * s)
	}
	return out
}

// compareSearchResults asserts the optimized and baseline outputs are
// identical: same length, same order, same IDs, bit-exact similarities, and
// identical hydrated observation fields.
func compareSearchResults(t *testing.T, label string, got, want []*domain.VectorSearchResult) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: result length: got %d, want %d", label, len(got), len(want))
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.ID != w.ID {
			t.Errorf("%s[%d]: ID: got %d, want %d", label, i, g.ID, w.ID)
		}
		if g.Similarity != w.Similarity {
			t.Errorf("%s[%d] (id %d): similarity bits: got %x (%v), want %x (%v)",
				label, i, g.ID, math.Float64bits(g.Similarity), g.Similarity, math.Float64bits(w.Similarity), w.Similarity)
		}
		if g.Title != w.Title || g.Content != w.Content || g.Project != w.Project ||
			g.Scope != w.Scope || g.TopicKey != w.TopicKey || g.SessionID != w.SessionID || g.Type != w.Type {
			t.Errorf("%s[%d] (id %d): hydrated fields differ: %+v vs %+v", label, i, g.ID, g.Observation, w.Observation)
		}
		if !g.CreatedAt.Equal(w.CreatedAt) || !g.UpdatedAt.Equal(w.UpdatedAt) {
			t.Errorf("%s[%d] (id %d): timestamps differ: %v/%v vs %v/%v", label, i, g.ID, g.CreatedAt, g.UpdatedAt, w.CreatedAt, w.UpdatedAt)
		}
	}
}

// TestSearchByVector_DifferentialBaseline is the R1R5 exactness oracle: the
// optimized scan must reproduce the frozen binary.Read baseline bit-for-bit
// across random queries, dimensions, thresholds, limits, filters, and every
// adversarial row class (ties, zero/NaN/Inf vectors, dim mismatch, trailing
// bytes, soft-deleted, orphan).
func TestSearchByVector_DifferentialBaseline(t *testing.T) {
	for _, dim := range []int{64, 128, 384} {
		t.Run(fmt.Sprintf("dim%d", dim), func(t *testing.T) {
			db, _ := newCountingDB(t)
			if _, err := db.Exec(vectorPipelineSchema); err != nil {
				t.Fatalf("schema: %v", err)
			}
			queries := seedDifferentialFixture(t, db, dim)
			vs := NewVectorStore(db)
			ctx := context.Background()

			thresholds := []float64{0, 0.25, 0.6, 1.0}
			limits := []int{0, 1, 7, 100}
			type filterCase struct {
				name   string
				optsFn func(o *domain.VectorSearchOptions)
			}
			filters := []filterCase{
				{"none", func(o *domain.VectorSearchOptions) {}},
				{"proj-p1", func(o *domain.VectorSearchOptions) { o.Project = "p1" }},
				{"scope-personal", func(o *domain.VectorSearchOptions) { o.Scope = "personal" }},
				{"both", func(o *domain.VectorSearchOptions) { o.Project = "p1"; o.Scope = "personal" }},
				{"nomatch", func(o *domain.VectorSearchOptions) { o.Project = "nope" }},
			}

			for qi, qv := range queries {
				for _, th := range thresholds {
					for _, lim := range limits {
						for _, fc := range filters {
							label := fmt.Sprintf("q%d/th%v/lim%d/%s", qi, th, lim, fc.name)
							opts := domain.VectorSearchOptions{
								Embedding: qv,
								Limit:     lim,
								Threshold: th,
							}
							fc.optsFn(&opts)
							got, err := vs.SearchByVector(ctx, opts)
							if err != nil {
								t.Fatalf("%s: optimized SearchByVector: %v", label, err)
							}
							want, err := referenceSearchByVector(ctx, db, opts)
							if err != nil {
								t.Fatalf("%s: reference SearchByVector: %v", label, err)
							}
							compareSearchResults(t, label, got, want)
						}
					}
				}
			}
		})
	}
}

// TestSearchByVector_CorruptBlobErrorsBothPaths pins error parity: empty and
// sub-float blobs fail the whole search in BOTH implementations with the
// "empty embedding data" class (dimension 0).
func TestSearchByVector_CorruptBlobErrorsBothPaths(t *testing.T) {
	for name, blob := range map[string][]byte{
		"empty-blob": {},
		"short-blob": {0x01, 0x02, 0x03},
	} {
		t.Run(name, func(t *testing.T) {
			db, _ := newCountingDB(t)
			if _, err := db.Exec(vectorPipelineSchema); err != nil {
				t.Fatalf("schema: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO observations (session_id, type, title, content, scope, source) VALUES ('s', 'manual', 'corrupt', 'c', 'project', 'manual')`); err != nil {
				t.Fatalf("seed obs: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO observation_vectors (observation_id, embedding, embedding_model, dimensions) VALUES (1, ?, 'm', 0)`, blob); err != nil {
				t.Fatalf("seed vec: %v", err)
			}
			vs := NewVectorStore(db)
			opts := domain.VectorSearchOptions{Embedding: make([]float32, 64), Limit: 10}
			_, gotErr := vs.SearchByVector(context.Background(), opts)
			_, wantErr := referenceSearchByVector(context.Background(), db, opts)
			if gotErr == nil || wantErr == nil {
				t.Fatalf("expected both paths to error: got=%v want=%v", gotErr, wantErr)
			}
			if gotErr.Error() != wantErr.Error() {
				t.Errorf("error text differs: got %q, want %q", gotErr.Error(), wantErr.Error())
			}
		})
	}
}

// TestSearchByVector_ScanAllocationBudget pins the R1R5 scan-decode
// allocation reduction: the 200x384 limit-50 scan must allocate far below
// the pre-optimization budget (measured ~86k allocs/op for the scan alone,
// ~89k for the full pipeline). Budget: 20000 allocs/op (a >75% reduction).
func TestSearchByVector_ScanAllocationBudget(t *testing.T) {
	db := openSearchPipelineDB(t)
	if _, err := db.Exec(vectorPipelineSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	queryVec := seedSearchPipelineFixture(t, db)
	vs := NewVectorStore(db)
	ctx := context.Background()
	opts := domain.VectorSearchOptions{Embedding: queryVec, Limit: 50}

	// Warm up statement/driver paths before measuring.
	if _, err := vs.SearchByVector(ctx, opts); err != nil {
		t.Fatalf("warmup search: %v", err)
	}

	allocs := testing.AllocsPerRun(10, func() {
		if _, err := vs.SearchByVector(ctx, opts); err != nil {
			t.Fatalf("measured search: %v", err)
		}
	})
	t.Logf("SearchByVector scan allocations: %.0f/op (budget: 20000)", allocs)
	if allocs > 20000 {
		t.Errorf("SearchByVector scan allocations: %.0f/op exceeds budget 20000 (pre-optimization ~86000/op)", allocs)
	}
}

// BenchmarkSearchByVectorScan_200x384 isolates the ANN scan/decode pipeline
// (SearchByVector alone, no retrieval hydration) for before/after evidence.
func BenchmarkSearchByVectorScan_200x384(b *testing.B) {
	db := openSearchPipelineDB(b)
	if _, err := db.Exec(vectorPipelineSchema); err != nil {
		b.Fatalf("schema: %v", err)
	}
	queryVec := seedSearchPipelineFixture(b, db)
	vs := NewVectorStore(db)
	ctx := context.Background()
	opts := domain.VectorSearchOptions{Embedding: queryVec, Limit: 50}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vs.SearchByVector(ctx, opts); err != nil {
			b.Fatalf("SearchByVector: %v", err)
		}
	}
}

//go:build cortex_vectors

// Adapter-level differential oracle for the sqlite_blob Search scan/decode
// optimization (R1R5): adapter.Search must reproduce the frozen
// pre-optimization pipeline bit-for-bit at the domain.VectorIndex boundary —
// identical candidate IDs, ordering, score bits, provenance, and filter
// translation — across random queries, thresholds, limits, and filters.
//
// The reference below is a verbatim copy of the original algorithm
// (bytes.NewReader + per-element binary.Read decode, per-row []float64
// normalization, float64 division per element) plus the adapter's filter
// translation and the store's limit/threshold clamps. It deliberately does
// not import the concrete store's unexported helpers so it stays a frozen
// baseline even if production internals change.
package sqlite_blob

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
)

const diffDim = 384

// refNormalize copies the pre-optimization normalizeVector.
func refNormalize(v []float32) []float64 {
	var sumSq float64
	for _, x := range v {
		sumSq += float64(x) * float64(x)
	}
	norm := math.Sqrt(sumSq)
	if norm < 1e-10 {
		return make([]float64, len(v))
	}
	normalized := make([]float64, len(v))
	for i, x := range v {
		normalized[i] = float64(x) / norm
	}
	return normalized
}

// refDeserialize copies the pre-optimization deserializeEmbedding.
func refDeserialize(data []byte) ([]float32, error) {
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

// refCosine copies the pre-optimization computeCosineSimilarity.
func refCosine(queryNorm []float64, embedding []float32) float64 {
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

// refNormalizeScope copies the concrete store's normalizeScope (applied to
// scope filters before the scan).
func refNormalizeScope(scope string) string {
	v := strings.TrimSpace(strings.ToLower(scope))
	if v == "personal" {
		return "personal"
	}
	return "project"
}

// refAdapterSearch mirrors adapter.Search over the frozen baseline pipeline:
// filter translation (project/scope string values only), the store's
// limit/threshold clamps, the JOIN scan order, threshold filtering,
// similarity-descending sort, truncation, and candidate translation.
func refAdapterSearch(ctx context.Context, db *sql.DB, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	opts := domain.VectorSearchOptions{
		Embedding: q.Vector,
		Limit:     q.Limit,
		Threshold: q.Threshold,
	}
	if q.Filters != nil {
		if v, ok := q.Filters["project"]; ok {
			if s, ok := v.(string); ok {
				opts.Project = s
			}
		}
		if v, ok := q.Filters["scope"]; ok {
			if s, ok := v.(string); ok {
				opts.Scope = s
			}
		}
	}

	if len(opts.Embedding) < 64 || len(opts.Embedding) > 4096 {
		return nil, fmt.Errorf("dimension out of bounds")
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
	queryNorm := refNormalize(opts.Embedding)

	query := `
		SELECT o.id, ov.embedding
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
		args = append(args, refNormalizeScope(opts.Scope))
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	type scored struct {
		id  int64
		sim float64
	}
	var results []scored
	for rows.Next() {
		var id int64
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		embedding, err := refDeserialize(blob)
		if err != nil {
			return nil, err
		}
		sim := refCosine(queryNorm, embedding)
		if sim >= opts.Threshold {
			results = append(results, scored{id: id, sim: sim})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].sim > results[j].sim
	})
	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	candidates := make([]domain.VectorCandidate, 0, len(results))
	for _, r := range results {
		candidates = append(candidates, domain.VectorCandidate{
			ID:         r.id,
			Score:      r.sim,
			Provenance: "sqlite_blob",
		})
	}
	return candidates, nil
}

// seedAdapterDiffFixture builds the adversarial fixture at the adapter
// boundary and returns deterministic query vectors.
func seedAdapterDiffFixture(t *testing.T, db *sql.DB) [][]float32 {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO sessions (id, project, directory) VALUES ('s-diff', 'p', '/diff')`); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	rng := rand.New(rand.NewSource(104729))
	insertObs := func(title, project, scope string, deleted bool) int64 {
		var deletedAt any
		if deleted {
			deletedAt = "2026-01-01T00:00:00Z"
		}
		res, err := db.Exec(`
			INSERT INTO observations (session_id, type, title, content, project, scope, deleted_at)
			VALUES ('s-diff', 'manual', ?, ?, ?, ?, ?)
		`, title, "content "+title, project, scope, deletedAt)
		if err != nil {
			t.Fatalf("seed obs %s: %v", title, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("last id: %v", err)
		}
		return id
	}
	randVec := func() []float32 {
		v := make([]float32, diffDim)
		for i := range v {
			v[i] = float32(rng.NormFloat64())
		}
		return v
	}

	model := domain.ModelInfo{Name: "diff-model", Dimension: diffDim}
	upsert := func(id int64, v []float32) {
		t.Helper()
		a := New(db)
		if err := a.Upsert(ctx, []domain.VectorPoint{{ID: id, Vector: v, ModelInfo: model}}); err != nil {
			t.Fatalf("upsert %d: %v", id, err)
		}
	}

	byID := map[int64][]float32{}
	var firstID, secondID int64
	for i := 0; i < 60; i++ {
		project, scope := "p1", "project"
		if i%2 == 1 {
			project = "p2"
		}
		if i%3 == 0 {
			scope = "personal"
		}
		id := insertObs(fmt.Sprintf("adiff-%03d", i), project, scope, i == 59)
		v := randVec()
		byID[id] = v
		upsert(id, v)
		if i == 0 {
			firstID = id
		}
		if i == 1 {
			secondID = id
		}
	}

	dup := randVec()
	for _, title := range []string{"adup-a", "adup-b"} {
		id := insertObs(title, "p1", "project", false)
		byID[id] = dup
		upsert(id, dup)
	}

	zeroID := insertObs("azero", "p1", "project", false)
	byID[zeroID] = make([]float32, diffDim)
	upsert(zeroID, make([]float32, diffDim))

	nanVec := randVec()
	nanVec[7] = float32(math.NaN())
	nanID := insertObs("anan", "p2", "personal", false)
	byID[nanID] = nanVec
	upsert(nanID, nanVec)

	// Dimension-mismatched row directly via SQL (adapter.Upsert would reject
	// it; the scan must score it 0 with a warning, not error).
	mism := make([]float32, diffDim+16)
	for i := range mism {
		mism[i] = float32(rng.NormFloat64())
	}
	mismID := insertObs("amismatch", "p1", "project", false)
	mismBlob := make([]byte, len(mism)*4)
	for i, x := range mism {
		binary.LittleEndian.PutUint32(mismBlob[i*4:], math.Float32bits(x))
	}
	if _, err := db.Exec(`INSERT INTO observation_vectors (observation_id, embedding, embedding_model, dimensions) VALUES (?, ?, 'diff-model', ?)`, mismID, mismBlob, len(mism)); err != nil {
		t.Fatalf("seed mismatch: %v", err)
	}

	// Trailing-garbage blob (trailing bytes ignored by both paths).
	trID := insertObs("atrailing", "p1", "personal", false)
	trVec := randVec()
	byID[trID] = trVec
	trBlob := make([]byte, diffDim*4)
	for i, x := range trVec {
		binary.LittleEndian.PutUint32(trBlob[i*4:], math.Float32bits(x))
	}
	trBlob = append(trBlob, 0x00, 0xFF, 0x00)
	if _, err := db.Exec(`INSERT INTO observation_vectors (observation_id, embedding, embedding_model, dimensions) VALUES (?, ?, 'diff-model', ?)`, trID, trBlob, diffDim); err != nil {
		t.Fatalf("seed trailing: %v", err)
	}

	scaled := make([]float32, diffDim)
	for i, x := range byID[secondID] {
		scaled[i] = float32(float64(x) * -1.75)
	}
	return [][]float32{
		randVec(),
		randVec(),
		dup,
		byID[firstID],
		scaled,
		make([]float32, diffDim),
	}
}

// TestAdapter_Search_DifferentialBaseline is the adapter-boundary R1R5
// exactness pin: optimized adapter.Search vs the frozen binary.Read baseline.
func TestAdapter_Search_DifferentialBaseline(t *testing.T) {
	db := newConformanceDB(t).DB()
	queries := seedAdapterDiffFixture(t, db)
	a := New(db)
	ctx := context.Background()

	thresholds := []float64{0, 0.3, 0.75}
	limits := []int{0, 3, 25, 100}
	filters := []map[string]any{
		nil,
		{"project": "p1"},
		{"scope": "personal"},
		{"scope": "PERSONAL "}, // normalizeScope folds to personal
		{"project": "p1", "scope": "personal"},
		{"project": "p2", "bogus": 123, "scope": 42}, // unknown/non-string filters ignored
		{"project": "nope"},
	}

	for qi, qv := range queries {
		for _, th := range thresholds {
			for _, lim := range limits {
				for fi, f := range filters {
					label := fmt.Sprintf("q%d/th%v/lim%d/f%d", qi, th, lim, fi)
					q := domain.VectorQuery{Vector: qv, Limit: lim, Threshold: th, Filters: f}
					got, err := a.Search(ctx, q)
					if err != nil {
						t.Fatalf("%s: adapter.Search: %v", label, err)
					}
					want, err := refAdapterSearch(ctx, db, q)
					if err != nil {
						t.Fatalf("%s: reference: %v", label, err)
					}
					if len(got) != len(want) {
						t.Fatalf("%s: candidate count: got %d, want %d", label, len(got), len(want))
					}
					for i := range got {
						if got[i].ID != want[i].ID {
							t.Errorf("%s[%d]: ID: got %d, want %d", label, i, got[i].ID, want[i].ID)
						}
						if got[i].Score != want[i].Score {
							t.Errorf("%s[%d] (id %d): score bits: got %x (%v), want %x (%v)",
								label, i, got[i].ID, math.Float64bits(got[i].Score), got[i].Score, math.Float64bits(want[i].Score), want[i].Score)
						}
						if got[i].Provenance != want[i].Provenance {
							t.Errorf("%s[%d]: provenance: got %q, want %q", label, i, got[i].Provenance, want[i].Provenance)
						}
					}
				}
			}
		}
	}
}

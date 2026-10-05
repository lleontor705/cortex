//go:build cortex_vectors

// Differential oracle for the sqlite_blob two-pass scan (REQ-VEC-SCAN-001).
//
// The reference below is the frozen EXACT scan: it scores the original
// float32 vectors captured at fixture time with the pre-modernization
// algorithm (normalize, dot, threshold, sort by score then id, truncate).
// The adapter under test stores a pre-normalized int8 Matryoshka prefix and
// runs a coarse-to-fine shortlist, so its contract is RECALL WITHIN AN
// ACCEPTED TOLERANCE of this exact scan plus a bounded per-candidate score
// deviation — bit-exact score equality is what quantization deliberately
// trades away.
//
// The corpus is sized above minShortlist so the shortlist really discards
// rows; a coarse pass that stopped ranking candidates would fail recall.
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

	"github.com/lleontor705/cortex/v2/internal/domain"
)

const (
	recallDim  = 384
	recallRows = 900

	// aggregateRecallFloor is the accepted recall tolerance of the exact scan
	// across the whole conformance matrix; perQueryRecallFloor only exists so
	// one pathological query cannot hide inside a healthy aggregate.
	aggregateRecallFloor = 0.95
	perQueryRecallFloor  = 0.80

	// scoreTolerance bounds int8 quantization error on a cosine score.
	scoreTolerance = 0.01
)

// refRow is one fixture point as the exact scan sees it.
type refRow struct {
	id      int64
	vec     []float32
	project string
	scope   string
	deleted bool
}

// refNormalize copies the pre-modernization normalizeVector.
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

// refCosine copies the pre-modernization computeCosineSimilarity: a dimension
// mismatch scores 0 and a non-finite embedding scores NaN (filtered out).
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

// refNormalizeScope copies the concrete store's normalizeScope.
func refNormalizeScope(scope string) string {
	v := strings.TrimSpace(strings.ToLower(scope))
	if v == "personal" {
		return "personal"
	}
	return "project"
}

// refSearch is the frozen exact pipeline: clamps, filter translation,
// threshold, score-descending/id-ascending order, and truncation.
func refSearch(rows []refRow, q domain.VectorQuery) []domain.VectorCandidate {
	opts := domain.VectorSearchOptions{Embedding: q.Vector, Limit: q.Limit, Threshold: q.Threshold}
	if q.Filters != nil {
		if v, ok := q.Filters["project"].(string); ok {
			opts.Project = v
		}
		if v, ok := q.Filters["scope"].(string); ok {
			opts.Scope = v
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

	queryNorm := refNormalize(opts.Embedding)
	wantScope := ""
	if opts.Scope != "" {
		wantScope = refNormalizeScope(opts.Scope)
	}
	out := make([]domain.VectorCandidate, 0, opts.Limit)
	for _, r := range rows {
		if r.deleted {
			continue
		}
		if opts.Project != "" && r.project != opts.Project {
			continue
		}
		if wantScope != "" && r.scope != wantScope {
			continue
		}
		sim := refCosine(queryNorm, r.vec)
		if !(sim >= opts.Threshold) {
			continue
		}
		out = append(out, domain.VectorCandidate{ID: r.id, Score: sim, Provenance: adapterID})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out
}

func randomVector(rng *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = float32(rng.NormFloat64())
	}
	return v
}

// matryoshkaVector mimics an MRL embedding: per-dimension energy decays along
// the axis order, so a leading prefix carries almost all of the norm.
func matryoshkaVector(rng *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = float32(rng.NormFloat64() * math.Exp(-float64(i)/14))
	}
	return v
}

// seedRecallCorpus builds recallRows vectors plus adversarial rows (duplicate
// scores, zero vector, non-finite vector, dimension mismatch, trailing blob
// garbage) and returns them for the exact reference.
func seedRecallCorpus(t *testing.T, db *sql.DB) []refRow {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO sessions (id, project, directory) VALUES ('s-recall', 'p1', '/recall')`); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	a := New(db)
	model := domain.ModelInfo{Name: "recall-model", Dimension: recallDim}
	rng := rand.New(rand.NewSource(104729))

	insertObs := func(title, project, scope string, deleted bool) int64 {
		t.Helper()
		var deletedAt any
		if deleted {
			deletedAt = "2026-01-01T00:00:00Z"
		}
		res, err := db.Exec(`INSERT INTO observations (session_id, type, title, content, project, scope, deleted_at)
			VALUES ('s-recall', 'manual', ?, ?, ?, ?, ?)`, title, "content "+title, project, scope, deletedAt)
		if err != nil {
			t.Fatalf("seed obs %s: %v", title, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("last id: %v", err)
		}
		return id
	}
	upsert := func(id int64, v []float32) {
		t.Helper()
		if err := a.Upsert(ctx, []domain.VectorPoint{{ID: id, Vector: v, ModelInfo: model}}); err != nil {
			t.Fatalf("upsert %d: %v", id, err)
		}
	}

	rows := make([]refRow, 0, recallRows+6)
	for i := 0; i < recallRows; i++ {
		project, scope := "p1", "project"
		if i%2 == 1 {
			project = "p2"
		}
		if i%3 == 0 {
			scope = "personal"
		}
		deleted := i%199 == 198
		id := insertObs(fmt.Sprintf("recall-%04d", i), project, scope, deleted)
		v := randomVector(rng, recallDim)
		upsert(id, v)
		rows = append(rows, refRow{id: id, vec: v, project: project, scope: scope, deleted: deleted})
	}

	dup := randomVector(rng, recallDim)
	for _, title := range []string{"dup-a", "dup-b"} {
		id := insertObs(title, "p1", "project", false)
		upsert(id, dup)
		rows = append(rows, refRow{id: id, vec: dup, project: "p1", scope: "project"})
	}

	zeroID := insertObs("zero", "p1", "project", false)
	upsert(zeroID, make([]float32, recallDim))
	rows = append(rows, refRow{id: zeroID, vec: make([]float32, recallDim), project: "p1", scope: "project"})

	// Non-finite vectors must keep the legacy float32 blob, so NaN still
	// scores NaN and is filtered out instead of becoming an int8 code.
	nanVec := randomVector(rng, recallDim)
	nanVec[7] = float32(math.NaN())
	nanID := insertObs("nan", "p2", "personal", false)
	upsert(nanID, nanVec)
	rows = append(rows, refRow{id: nanID, vec: nanVec, project: "p2", scope: "personal"})

	// Dimension-mismatched row written directly (adapter.Upsert rejects it):
	// the scan must score it 0 with a warning, not error or misalign.
	mism := make([]float32, recallDim+16)
	for i := range mism {
		mism[i] = float32(rng.NormFloat64())
	}
	mismID := insertObs("mismatch", "p1", "project", false)
	insertLegacyBlob(t, db, mismID, float32SliceBytes(mism), len(mism), "recall-model")
	rows = append(rows, refRow{id: mismID, vec: mism, project: "p1", scope: "project"})

	// Trailing blob garbage is ignored by both paths (len/4 whole floats).
	trVec := randomVector(rng, recallDim)
	trID := insertObs("trailing", "p1", "personal", false)
	insertLegacyBlob(t, db, trID, append(float32SliceBytes(trVec), 0x00, 0xFF, 0x00), recallDim, "recall-model")
	rows = append(rows, refRow{id: trID, vec: trVec, project: "p1", scope: "personal"})

	return rows
}

func float32SliceBytes(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, x := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(x))
	}
	return buf
}

func insertLegacyBlob(t *testing.T, db *sql.DB, id int64, blob []byte, dims int, model string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO observation_vectors (observation_id, embedding, embedding_model, dimensions)
		VALUES (?, ?, ?, ?)`, id, blob, model, dims); err != nil {
		t.Fatalf("seed legacy blob %d: %v", id, err)
	}
}

// TestAdapter_Search_TwoPassRecallAgainstExactScan is the REQ-VEC-SCAN-001
// happy-path oracle: the coarse prefix pass plus int8 refinement must stay
// within the accepted recall tolerance of the exact scan across random
// queries, thresholds, limits, and filters.
func TestAdapter_Search_TwoPassRecallAgainstExactScan(t *testing.T) {
	db := newConformanceDB(t)
	rows := seedRecallCorpus(t, db.DB())
	a := New(db.DB())
	ctx := context.Background()

	queries := [][]float32{
		randomVector(rand.New(rand.NewSource(7)), recallDim),
		randomVector(rand.New(rand.NewSource(99)), recallDim),
		rows[11].vec,
		make([]float32, recallDim),
	}
	thresholds := []float64{0, 0.03, 0.09}
	limits := []int{0, 10, 50}
	filters := []map[string]any{
		nil,
		{"project": "p1"},
		{"scope": "personal"},
		{"project": "p2", "scope": "personal"},
	}

	var hits, wantTotal, combos int
	for qi, qv := range queries {
		for _, th := range thresholds {
			for _, lim := range limits {
				for fi, f := range filters {
					combos++
					label := fmt.Sprintf("q%d/th%v/lim%d/f%d", qi, th, lim, fi)
					q := domain.VectorQuery{Vector: qv, Limit: lim, Threshold: th, Filters: f}
					got, err := a.Search(ctx, q)
					if err != nil {
						t.Fatalf("%s: Search: %v", label, err)
					}
					want := refSearch(rows, q)
					exact := make(map[int64]float64, len(want))
					for _, c := range want {
						exact[c.ID] = c.Score
					}
					comboHits := 0
					for _, c := range got {
						ws, ok := exact[c.ID]
						if !ok {
							continue
						}
						comboHits++
						if math.Abs(c.Score-ws) > scoreTolerance {
							t.Errorf("%s: id %d score %.6f deviates from exact %.6f by more than %g",
								label, c.ID, c.Score, ws, scoreTolerance)
						}
					}
					hits += comboHits
					wantTotal += len(want)
					if len(want) > 0 {
						if r := float64(comboHits) / float64(len(want)); r < perQueryRecallFloor {
							t.Errorf("%s: recall %.3f below per-query floor %.3f (%d/%d)",
								label, r, perQueryRecallFloor, comboHits, len(want))
						}
					}
					effective := lim
					if effective <= 0 {
						effective = 10
					}
					if effective > 100 {
						effective = 100
					}
					if len(got) > effective {
						t.Errorf("%s: returned %d candidates, limit clamps to %d", label, len(got), effective)
					}
				}
			}
		}
	}

	if wantTotal == 0 {
		t.Fatal("fixture produced no exact-scan candidates; recall is untested")
	}
	aggregate := float64(hits) / float64(wantTotal)
	if aggregate < aggregateRecallFloor {
		t.Errorf("aggregate recall %.4f below accepted tolerance %.3f (%d/%d over %d combos)",
			aggregate, aggregateRecallFloor, hits, wantTotal, combos)
	}
	t.Logf("two-pass recall vs exact scan: %.4f (%d/%d over %d query combos)", aggregate, hits, wantTotal, combos)
}

// TestAdapter_Search_DeterministicOrdering pins the REQ-VEC-SCAN-001 edge
// case: goroutine-sharded scoring must return an identical sequence across
// runs, and scores must never ascend. The zero-vector query forces every
// candidate into a tie so the id tie-break carries the ordering.
func TestAdapter_Search_DeterministicOrdering(t *testing.T) {
	db := newConformanceDB(t)
	rows := seedRecallCorpus(t, db.DB())
	a := New(db.DB())
	ctx := context.Background()

	queries := []domain.VectorQuery{
		{Vector: rows[5].vec, Limit: 25},
		{Vector: make([]float32, recallDim), Limit: 40},
	}
	for qi, base := range queries {
		first, err := a.Search(ctx, base)
		if err != nil {
			t.Fatalf("q%d: Search: %v", qi, err)
		}
		if len(first) == 0 {
			t.Fatalf("q%d: empty result set", qi)
		}
		for i := 1; i < len(first); i++ {
			if first[i].Score > first[i-1].Score {
				t.Fatalf("q%d: scores ascend at rank %d: %.17g > %.17g", qi, i, first[i].Score, first[i-1].Score)
			}
		}
		for run := 1; run <= 5; run++ {
			got, err := a.Search(ctx, base)
			if err != nil {
				t.Fatalf("q%d run %d: Search: %v", qi, run, err)
			}
			if len(got) != len(first) {
				t.Fatalf("q%d run %d: %d candidates, want %d", qi, run, len(got), len(first))
			}
			for i := range got {
				if got[i].ID != first[i].ID {
					t.Fatalf("q%d run %d rank %d: id %d, want %d", qi, run, i, got[i].ID, first[i].ID)
				}
				if math.Float64bits(got[i].Score) != math.Float64bits(first[i].Score) {
					t.Fatalf("q%d run %d rank %d: score bits differ", qi, run, i)
				}
			}
		}
	}
}

// TestAdapter_Upsert_StorageReduction pins the storage target: int8
// quantization alone halves the byte count four-fold, and an MRL-structured
// vector — the representation the prefix scan is designed for — lands inside
// the 8-48x band because its leading prefix keeps almost all of the energy.
func TestAdapter_Upsert_StorageReduction(t *testing.T) {
	db := newConformanceDB(t)
	a := New(db.DB())
	ctx := context.Background()
	rng := rand.New(rand.NewSource(4242))
	float32Bytes := recallDim * 4

	cases := []struct {
		id       int64
		name     string
		vec      []float32
		minRatio float64
		maxRatio float64
	}{
		{1, "uniform-384d", randomVector(rng, recallDim), 3.5, 4.5},
		{2, "matryoshka-384d", matryoshkaVector(rng, recallDim), 8, 48},
	}
	for _, tc := range cases {
		model := domain.ModelInfo{Name: "storage-model", Dimension: recallDim}
		if err := a.Upsert(ctx, []domain.VectorPoint{{ID: tc.id, Vector: tc.vec, ModelInfo: model}}); err != nil {
			t.Fatalf("%s: upsert: %v", tc.name, err)
		}
		var payload []byte
		if err := db.DB().QueryRow(`SELECT embedding FROM observation_vectors WHERE observation_id = ?`, tc.id).Scan(&payload); err != nil {
			t.Fatalf("%s: read payload: %v", tc.name, err)
		}
		ratio := float64(float32Bytes) / float64(len(payload))
		if ratio < tc.minRatio || ratio > tc.maxRatio {
			t.Errorf("%s: storage ratio %.2fx outside [%.1fx, %.1fx] (%d B payload, %d B float32)",
				tc.name, ratio, tc.minRatio, tc.maxRatio, len(payload), float32Bytes)
		}
		t.Logf("%s: %d B -> %d B (%.2fx)", tc.name, float32Bytes, len(payload), ratio)
	}
}

// TestAdapter_Upsert_DimensionMismatchNeverStored is the REQ-VEC-001 error
// oracle: a mismatched vector is rejected with domain.ErrDimensionMismatch
// and leaves no trace, whether the point already had a vector or not.
func TestAdapter_Upsert_DimensionMismatchNeverStored(t *testing.T) {
	db := newConformanceDB(t)
	a := New(db.DB())
	ctx := context.Background()
	model := domain.ModelInfo{Name: "mismatch-model", Dimension: recallDim, Version: "v1"}

	valid := randomVector(rand.New(rand.NewSource(11)), recallDim)
	if err := a.Upsert(ctx, []domain.VectorPoint{{ID: 1, Vector: valid, ModelInfo: model}}); err != nil {
		t.Fatalf("valid upsert: %v", err)
	}
	before := storedPayload(t, db.DB(), 1)

	// Fresh point 2 must never gain a row; existing point 1 must keep its
	// payload byte-for-byte.
	short := append([]float32(nil), valid[:100]...)
	for _, id := range []int64{1, 2} {
		err := a.Upsert(ctx, []domain.VectorPoint{{ID: id, Vector: short, ModelInfo: model}})
		if !domain.IsDimensionMismatch(err) {
			t.Fatalf("point %d: expected ErrDimensionMismatch, got %v", id, err)
		}
	}
	var fresh int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM observation_vectors WHERE observation_id = 2`).Scan(&fresh); err != nil {
		t.Fatalf("count point 2: %v", err)
	}
	if fresh != 0 {
		t.Errorf("mismatched point 2 was stored (%d rows)", fresh)
	}
	if after := storedPayload(t, db.DB(), 1); !bytes.Equal(before, after) {
		t.Errorf("mismatched upsert overwrote the existing vector for point 1")
	}
}

func storedPayload(t *testing.T, db *sql.DB, id int64) []byte {
	t.Helper()
	var payload []byte
	if err := db.QueryRow(`SELECT embedding FROM observation_vectors WHERE observation_id = ?`, id).Scan(&payload); err != nil {
		t.Fatalf("read payload %d: %v", id, err)
	}
	return payload
}

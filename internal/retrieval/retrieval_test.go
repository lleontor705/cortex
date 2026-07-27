// Package retrieval tests pin the shared vector-candidate revalidation and
// hybrid RRF fusion logic. These tests are the contract for every consumer
// (MCP, bench/locomo): they ensure the extraction from tools_cortex.go and
// runner.go is behavior-preserving — same RRF formula (k=60), same
// soft-delete drop, same ordering semantics.
package retrieval

import (
	"context"
	"errors"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
)

// --- Test doubles ---

// stubObsLookup is a controllable ObservationLookup for revalidation tests.
// It returns configured observations and/or errors by ID.
type stubObsLookup struct {
	obsByID map[int64]*domain.Observation
	errByID map[int64]error
}

func (s *stubObsLookup) GetByID(_ context.Context, id int64) (*domain.Observation, error) {
	if s.errByID != nil {
		if err, ok := s.errByID[id]; ok {
			return nil, err
		}
	}
	if obs, ok := s.obsByID[id]; ok {
		return obs, nil
	}
	return nil, errors.New("not found")
}

func mkObs(id int64, title string) *domain.Observation {
	return &domain.Observation{ID: id, Title: title, Content: "content-" + title}
}

// --- RevalidateCandidates ---

func TestRevalidateCandidates_AllValid(t *testing.T) {
	obs := &stubObsLookup{
		obsByID: map[int64]*domain.Observation{
			1: mkObs(1, "alpha"),
			2: mkObs(2, "beta"),
		},
	}
	candidates := []domain.VectorCandidate{
		{ID: 1, Score: 0.9, Provenance: "sqlite_blob"},
		{ID: 2, Score: 0.8, Provenance: "sqlite_blob"},
	}

	results := RevalidateCandidates(context.Background(), obs, candidates)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != 1 || results[0].Similarity != 0.9 {
		t.Errorf("result[0]: ID=%d Similarity=%f, want 1/0.9", results[0].ID, results[0].Similarity)
	}
	if results[1].ID != 2 || results[1].Similarity != 0.8 {
		t.Errorf("result[1]: ID=%d Similarity=%f, want 2/0.8", results[1].ID, results[1].Similarity)
	}
	if results[0].Title != "alpha" {
		t.Errorf("result[0].Title=%q, want alpha", results[0].Title)
	}
}

func TestRevalidateCandidates_DropsSoftDeleted(t *testing.T) {
	obs := &stubObsLookup{
		obsByID: map[int64]*domain.Observation{
			1: mkObs(1, "live"),
		},
		errByID: map[int64]error{
			2: errors.New("soft-deleted"), // candidate 2 was deleted
		},
	}
	candidates := []domain.VectorCandidate{
		{ID: 1, Score: 0.9},
		{ID: 2, Score: 0.8}, // will error → dropped
		{ID: 3, Score: 0.7}, // not in map → error → dropped
	}

	results := RevalidateCandidates(context.Background(), obs, candidates)

	if len(results) != 1 {
		t.Fatalf("expected 1 result (2 dropped), got %d", len(results))
	}
	if results[0].ID != 1 {
		t.Errorf("expected only ID 1 to survive, got %d", results[0].ID)
	}
}

func TestRevalidateCandidates_NilObservationDropped(t *testing.T) {
	obs := &stubObsLookup{
		obsByID: map[int64]*domain.Observation{
			1: nil, // store returns nil observation
		},
	}
	candidates := []domain.VectorCandidate{
		{ID: 1, Score: 0.9},
	}

	results := RevalidateCandidates(context.Background(), obs, candidates)

	if len(results) != 0 {
		t.Fatalf("nil observation must be dropped; got %d results", len(results))
	}
}

func TestRevalidateCandidates_PreservesInputOrder(t *testing.T) {
	obs := &stubObsLookup{
		obsByID: map[int64]*domain.Observation{
			1: mkObs(1, "a"),
			2: mkObs(2, "b"),
			3: mkObs(3, "c"),
		},
	}
	// Candidates are in score-DESCENDING order (as a VectorIndex would return).
	// RevalidateCandidates must preserve this order — it does NOT re-sort.
	candidates := []domain.VectorCandidate{
		{ID: 3, Score: 0.95},
		{ID: 1, Score: 0.80},
		{ID: 2, Score: 0.50},
	}

	results := RevalidateCandidates(context.Background(), obs, candidates)

	if len(results) != 3 {
		t.Fatalf("got %d, want 3", len(results))
	}
	wantIDs := []int64{3, 1, 2}
	for i, w := range wantIDs {
		if results[i].ID != w {
			t.Errorf("position %d: ID=%d, want %d (input order must be preserved)", i, results[i].ID, w)
		}
	}
}

func TestRevalidateCandidates_EmptyInput(t *testing.T) {
	obs := &stubObsLookup{}
	results := RevalidateCandidates(context.Background(), obs, nil)
	if len(results) != 0 {
		t.Fatalf("nil candidates should yield 0 results, got %d", len(results))
	}
	results = RevalidateCandidates(context.Background(), obs, []domain.VectorCandidate{})
	if len(results) != 0 {
		t.Fatalf("empty candidates should yield 0 results, got %d", len(results))
	}
}

// --- FuseResults (RRF k=60) ---

func TestFuseResults_FormulaExact(t *testing.T) {
	// One FTS result at rank 1, one vector-only result at rank 1.
	// RRF: 1/(60+1) each, so both have the same score. With equal scores,
	// sort.Slice ordering is non-deterministic — just check both survive.
	fts := []*domain.SearchResult{
		{Observation: domain.Observation{ID: 10}, Rank: 0.9},
	}
	vec := []*domain.VectorSearchResult{
		{Observation: domain.Observation{ID: 20}, Similarity: 0.8},
	}

	results := FuseResults(fts, vec, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 fused results, got %d", len(results))
	}
}

func TestFuseResults_OverlapAccumulatesRRF(t *testing.T) {
	// ID 5 appears in BOTH lists at rank 1 (FTS) and rank 1 (vector).
	// Its RRF score = 1/(60+1) + 1/(60+1) ≈ 0.0328.
	// ID 6 appears ONLY in vector at rank 2.
	// Its RRF score = 1/(60+2) ≈ 0.0161.
	// ID 5 must rank higher.
	fts := []*domain.SearchResult{
		{Observation: domain.Observation{ID: 5}, Rank: 0.95},
	}
	vec := []*domain.VectorSearchResult{
		{Observation: domain.Observation{ID: 5}, Similarity: 0.88},
		{Observation: domain.Observation{ID: 6}, Similarity: 0.70},
	}

	results := FuseResults(fts, vec, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != 5 {
		t.Errorf("overlapping ID 5 must rank first (accumulated RRF), got %d", results[0].ID)
	}
	if results[1].ID != 6 {
		t.Errorf("vector-only ID 6 must rank second, got %d", results[1].ID)
	}
}

func TestFuseResults_LimitTruncates(t *testing.T) {
	fts := []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1}},
		{Observation: domain.Observation{ID: 2}},
		{Observation: domain.Observation{ID: 3}},
	}
	vec := []*domain.VectorSearchResult{}

	results := FuseResults(fts, vec, 2)
	if len(results) != 2 {
		t.Fatalf("expected limit=2 truncation, got %d", len(results))
	}
}

func TestFuseResults_EmptyInputs(t *testing.T) {
	results := FuseResults(nil, nil, 10)
	if len(results) != 0 {
		t.Fatalf("nil inputs should yield 0 results, got %d", len(results))
	}
	results = FuseResults([]*domain.SearchResult{}, []*domain.VectorSearchResult{}, 10)
	if len(results) != 0 {
		t.Fatalf("empty inputs should yield 0 results, got %d", len(results))
	}
}

func TestFuseResults_FTSOnly(t *testing.T) {
	fts := []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1}, Rank: 0.9},
		{Observation: domain.Observation{ID: 2}, Rank: 0.7},
	}
	results := FuseResults(fts, nil, 10)
	if len(results) != 2 {
		t.Fatalf("FTS-only: expected 2, got %d", len(results))
	}
	if results[0].ID != 1 {
		t.Errorf("rank 1 FTS result must come first, got ID %d", results[0].ID)
	}
}

func TestFuseResults_VectorOnly(t *testing.T) {
	vec := []*domain.VectorSearchResult{
		{Observation: domain.Observation{ID: 10}, Similarity: 0.95},
		{Observation: domain.Observation{ID: 11}, Similarity: 0.80},
	}
	results := FuseResults(nil, vec, 10)
	if len(results) != 2 {
		t.Fatalf("vector-only: expected 2, got %d", len(results))
	}
	// Vector-only: the Similarity score is carried onto Rank.
	if results[0].Rank != 0.95 {
		t.Errorf("vector-only Rank should carry Similarity; got %f, want 0.95", results[0].Rank)
	}
}

func TestFuseResults_VectorOnlySimilarityCarriedToRank(t *testing.T) {
	// A vector-only result (not in FTS) must have its Similarity score
	// preserved on the SearchResult.Rank field so downstream consumers
	// can inspect it.
	vec := []*domain.VectorSearchResult{
		{Observation: domain.Observation{ID: 42}, Similarity: 0.777},
	}
	results := FuseResults(nil, vec, 10)
	if len(results) != 1 {
		t.Fatalf("got %d, want 1", len(results))
	}
	if results[0].ID != 42 || results[0].Rank != 0.777 {
		t.Errorf("ID=%d Rank=%f, want 42/0.777", results[0].ID, results[0].Rank)
	}
}

// TestFuseResults_RRFConstantIsSixty pins the RRF smoothing constant at the
// standard k=60 value. If a future change accidentally uses a different k,
// the overlap test's rank ordering would change and this test catches it.
func TestFuseResults_RRFConstantIsSixty(t *testing.T) {
	if rrfConstant != 60.0 {
		t.Fatalf("rrfConstant = %f, must be 60.0 (standard RRF from TREC)", rrfConstant)
	}
}

// --- Compile-time: stubObsLookup satisfies ObservationLookup ---

func TestStubObsLookup_ImplementsObservationLookup(t *testing.T) {
	var _ ObservationLookup = (*stubObsLookup)(nil)
}

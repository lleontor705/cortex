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

// ---------------------------------------------------------------------------
// SearchVectors — capability-driven strategy (W8.4, REQ-VEC-001/002)
// ---------------------------------------------------------------------------

// fakeVectorIndex is a controllable domain.VectorIndex for strategy tests. It
// records the limit passed to each Search call (to verify pool expansion for
// PostFilter adapters) and returns canned candidates.
type fakeVectorIndex struct {
	caps       domain.Capabilities
	candidates []domain.VectorCandidate
	searchErr  error
	capsErr    error
	health     domain.Health
	searchLim  int  // last limit passed to Search
	searched   bool // Search was called
}

func (f *fakeVectorIndex) ID() string { return f.caps.IndexType }

func (f *fakeVectorIndex) Upsert(_ context.Context, _ []domain.VectorPoint) error {
	return nil
}

func (f *fakeVectorIndex) Search(_ context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	f.searched = true
	f.searchLim = q.Limit
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	// Return up to q.Limit candidates from the canned slice.
	end := q.Limit
	if end > len(f.candidates) {
		end = len(f.candidates)
	}
	if end < 0 {
		end = 0
	}
	return f.candidates[:end], nil
}

func (f *fakeVectorIndex) Delete(_ context.Context, _ []int64) error    { return nil }
func (f *fakeVectorIndex) Close() error                                 { return nil }

func (f *fakeVectorIndex) Health(_ context.Context) domain.Health {
	if f.health.Status != "" {
		return f.health
	}
	return domain.Health{Status: domain.StatusHealthy}
}

func (f *fakeVectorIndex) Capabilities(_ context.Context) (domain.Capabilities, error) {
	return f.caps, f.capsErr
}

// mkObsWithProject creates an observation with a Project/Scope for filter tests.
func mkObsWithProject(id int64, project, scope string) *domain.Observation {
	return &domain.Observation{ID: id, Title: "obs", Content: "c", Project: project, Scope: scope}
}

// TestSearchVectors_PreFilter_TrustsAdapter verifies that when the adapter
// declares Filters=PreFilter, the engine trusts the adapter's filtered results
// and does NOT expand the pool or re-apply filters in-engine. The limit passed
// to the adapter equals the requested limit (no expansion).
func TestSearchVectors_PreFilter_TrustsAdapter(t *testing.T) {
	idx := &fakeVectorIndex{
		caps: domain.Capabilities{IndexType: "qdrant", Filters: "PreFilter"},
		candidates: []domain.VectorCandidate{
			{ID: 1, Score: 0.9, Provenance: "qdrant"},
			{ID: 2, Score: 0.8, Provenance: "qdrant"},
		},
	}
	obs := &stubObsLookup{
		obsByID: map[int64]*domain.Observation{
			1: mkObsWithProject(1, "other", "project"),
			2: mkObsWithProject(2, "other", "project"),
		},
	}
	q := domain.VectorQuery{
		Vector:  []float32{1, 0, 0, 0},
		Limit:   2,
		Filters: map[string]any{"project": "myproj"},
	}
	results, err := SearchVectors(context.Background(), idx, q, obs)
	if err != nil {
		t.Fatalf("SearchVectors: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// PreFilter: limit NOT expanded.
	if idx.searchLim != 2 {
		t.Errorf("PreFilter: adapter limit = %d, want 2 (no pool expansion)", idx.searchLim)
	}
	// PreFilter: filters NOT re-applied in-engine (the observations have
	// project="other" but the filter wants "myproj" — they survive because
	// the adapter is trusted to have applied the filter server-side).
	for _, r := range results {
		if r.Project == "myproj" {
			t.Errorf("PreFilter trust broken: result %d has project=myproj but fixture has 'other'", r.ID)
		}
	}
}

// TestSearchVectors_PostFilter_ExpandsPool verifies that when the adapter
// declares Filters=PostFilter, the engine expands the retrieval pool (limit *
// multiplier) so in-engine filtering has headroom after the adapter's
// less-precise post-filter.
func TestSearchVectors_PostFilter_ExpandsPool(t *testing.T) {
	idx := &fakeVectorIndex{
		caps: domain.Capabilities{IndexType: "sqlite_blob", Filters: "PostFilter"},
		candidates: []domain.VectorCandidate{
			{ID: 1, Score: 0.9},
			{ID: 2, Score: 0.8},
			{ID: 3, Score: 0.7},
			{ID: 4, Score: 0.6},
			{ID: 5, Score: 0.5},
			{ID: 6, Score: 0.4},
		},
	}
	obs := &stubObsLookup{
		obsByID: map[int64]*domain.Observation{
			1: mkObsWithProject(1, "myproj", "project"),
			2: mkObsWithProject(2, "myproj", "project"),
			3: mkObsWithProject(3, "other", "project"),
			4: mkObsWithProject(4, "myproj", "project"),
			5: mkObsWithProject(5, "other", "project"),
			6: mkObsWithProject(6, "other", "project"),
		},
	}
	q := domain.VectorQuery{
		Vector:  []float32{1, 0, 0, 0},
		Limit:   2,
		Filters: map[string]any{"project": "myproj"},
	}
	results, err := SearchVectors(context.Background(), idx, q, obs)
	if err != nil {
		t.Fatalf("SearchVectors: %v", err)
	}
	// Pool MUST be expanded beyond the requested limit=2.
	if idx.searchLim <= 2 {
		t.Errorf("PostFilter: adapter limit = %d, want > 2 (pool expansion)", idx.searchLim)
	}
	// In-engine filter re-application: only "myproj" results survive.
	for _, r := range results {
		if r.Project != "myproj" {
			t.Errorf("PostFilter in-engine re-apply failed: result %d has project=%q, want myproj", r.ID, r.Project)
		}
	}
	// Truncated to requested limit.
	if len(results) > 2 {
		t.Errorf("PostFilter: expected <= 2 results after truncation, got %d", len(results))
	}
}

// TestSearchVectors_FilterNeverSilentlyDropped is the REQ-VEC-002 defect pin:
// even if a PostFilter adapter returns candidates that DON'T match the filter
// (simulating a silent filter drop), the engine's in-engine re-application
// MUST remove them. The filter is NEVER silently dropped.
func TestSearchVectors_FilterNeverSilentlyDropped(t *testing.T) {
	idx := &fakeVectorIndex{
		caps: domain.Capabilities{IndexType: "broken_pgvector", Filters: "PostFilter"},
		// All candidates have the WRONG project — simulating an adapter that
		// silently dropped the filter.
		candidates: []domain.VectorCandidate{
			{ID: 1, Score: 0.9},
			{ID: 2, Score: 0.8},
			{ID: 3, Score: 0.7},
		},
	}
	obs := &stubObsLookup{
		obsByID: map[int64]*domain.Observation{
			1: mkObsWithProject(1, "WRONG", "project"),
			2: mkObsWithProject(2, "WRONG", "project"),
			3: mkObsWithProject(3, "WRONG", "project"),
		},
	}
	q := domain.VectorQuery{
		Limit:   5,
		Filters: map[string]any{"project": "myproj"},
	}
	results, err := SearchVectors(context.Background(), idx, q, obs)
	if err != nil {
		t.Fatalf("SearchVectors: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("filter silently dropped: %d results survived despite all having wrong project; expected 0", len(results))
		for _, r := range results {
			t.Errorf("  leaked result: ID=%d project=%q", r.ID, r.Project)
		}
	}
}

// TestSearchVectors_PreFilter_DoesNotReApplyFilters verifies the PreFilter
// path does NOT call in-engine filtering (the adapter is trusted). We confirm
// by checking that candidates with non-matching projects SURVIVE under
// PreFilter (trust) but would be filtered under PostFilter.
func TestSearchVectors_PreFilter_DoesNotReApplyFilters(t *testing.T) {
	idx := &fakeVectorIndex{
		caps: domain.Capabilities{IndexType: "qdrant", Filters: "PreFilter"},
		candidates: []domain.VectorCandidate{
			{ID: 1, Score: 0.9},
		},
	}
	obs := &stubObsLookup{
		obsByID: map[int64]*domain.Observation{
			1: mkObsWithProject(1, "DIFFERENT", "project"),
		},
	}
	q := domain.VectorQuery{
		Limit:   5,
		Filters: map[string]any{"project": "myproj"},
	}
	results, err := SearchVectors(context.Background(), idx, q, obs)
	if err != nil {
		t.Fatalf("SearchVectors: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("PreFilter: expected 1 result (adapter trusted, no in-engine filter), got %d", len(results))
	}
}

// TestSearchVectors_NoneStrategy_FiltersInEngine verifies that when the adapter
// declares Filters=none, the engine applies ALL filtering in-engine (the
// adapter does no filtering at all).
func TestSearchVectors_NoneStrategy_FiltersInEngine(t *testing.T) {
	idx := &fakeVectorIndex{
		caps: domain.Capabilities{IndexType: "stub", Filters: "none"},
		candidates: []domain.VectorCandidate{
			{ID: 1, Score: 0.9},
			{ID: 2, Score: 0.8},
		},
	}
	obs := &stubObsLookup{
		obsByID: map[int64]*domain.Observation{
			1: mkObsWithProject(1, "myproj", "project"),
			2: mkObsWithProject(2, "other", "project"),
		},
	}
	q := domain.VectorQuery{
		Limit:   5,
		Filters: map[string]any{"project": "myproj"},
	}
	results, err := SearchVectors(context.Background(), idx, q, obs)
	if err != nil {
		t.Fatalf("SearchVectors: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("none strategy: expected 1 result after in-engine filter, got %d", len(results))
	}
	if results[0].ID != 1 {
		t.Errorf("none strategy: expected ID 1 (myproj), got %d", results[0].ID)
	}
}

// TestSearchVectors_PropagatesSearchError verifies errors from the adapter are
// returned to the caller (no silent swallowing).
func TestSearchVectors_PropagatesSearchError(t *testing.T) {
	idx := &fakeVectorIndex{
		caps:      domain.Capabilities{Filters: "PreFilter"},
		searchErr: domain.ErrVectorSearchDisabled,
	}
	q := domain.VectorQuery{Limit: 5}
	_, err := SearchVectors(context.Background(), idx, q, &stubObsLookup{})
	if err == nil {
		t.Fatal("expected search error to propagate, got nil")
	}
}

// TestSearchVectors_TruncatesToLimit verifies the final result set is truncated
// to the requested limit AFTER in-engine filtering.
func TestSearchVectors_TruncatesToLimit(t *testing.T) {
	idx := &fakeVectorIndex{
		caps: domain.Capabilities{Filters: "PreFilter"},
		candidates: []domain.VectorCandidate{
			{ID: 1, Score: 0.9},
			{ID: 2, Score: 0.8},
			{ID: 3, Score: 0.7},
			{ID: 4, Score: 0.6},
			{ID: 5, Score: 0.5},
		},
	}
	obs := &stubObsLookup{
		obsByID: map[int64]*domain.Observation{
			1: mkObsWithProject(1, "p", "s"),
			2: mkObsWithProject(2, "p", "s"),
			3: mkObsWithProject(3, "p", "s"),
			4: mkObsWithProject(4, "p", "s"),
			5: mkObsWithProject(5, "p", "s"),
		},
	}
	q := domain.VectorQuery{Limit: 3}
	results, err := SearchVectors(context.Background(), idx, q, obs)
	if err != nil {
		t.Fatalf("SearchVectors: %v", err)
	}
	if len(results) > 3 {
		t.Errorf("expected <= 3 results after truncation, got %d", len(results))
	}
}

// TestSearchVectors_NilFilters_NoCrash verifies no panic when Filters is nil.
func TestSearchVectors_NilFilters_NoCrash(t *testing.T) {
	idx := &fakeVectorIndex{
		caps:       domain.Capabilities{Filters: "PostFilter"},
		candidates: []domain.VectorCandidate{{ID: 1, Score: 0.9}},
	}
	obs := &stubObsLookup{
		obsByID: map[int64]*domain.Observation{1: mkObs(1, "x")},
	}
	q := domain.VectorQuery{Limit: 5, Filters: nil}
	results, err := SearchVectors(context.Background(), idx, q, obs)
	if err != nil {
		t.Fatalf("SearchVectors nil filters: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result (no filters to apply), got %d", len(results))
	}
}

// Compile-time: fakeVectorIndex implements domain.VectorIndex.
func TestFakeVectorIndex_ImplementsVectorIndex(t *testing.T) {
	var _ domain.VectorIndex = (*fakeVectorIndex)(nil)
}

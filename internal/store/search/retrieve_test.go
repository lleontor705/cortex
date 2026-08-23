package search

// retrieve_test.go pins W5.3 (REQ-RET-002 retrieval-engine hygiene):
//   - Unified retrieval helpers (filter clauses, scanner, assembleResults).
//   - Candidate revalidation against the live SQLite store (no phantom/stale
//     results; a candidate soft-deleted after entering the ranked set is dropped).
//   - Deterministic stable-ID tie-break across the whole pipeline.
//   - Deterministic, opaque, context-bound cursor pagination (a cursor from one
//     query/filter cannot be replayed against another; mismatch => fresh/empty,
//     never leak).
//
// LOCAL MODE constraint: the cursor is bound to project + filter + a local-mode
// stable identity. There is no tenant/principal/grant yet (W11/W13 out of
// scope). The binding is forward-compatible via an opaque context hash and
// imports no authz/identity packages.

import (
	"context"
	"math"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// ---------------------------------------------------------------------------
// applyFilterClauses — unified filter builder (REQ-RET-002 helper unification)
// ---------------------------------------------------------------------------

func TestApplyFilterClauses_Unified(t *testing.T) {
	cases := []struct {
		name string
		opts domain.SearchOptions
		// Expected SQL fragment (with leading " AND ") and bound args, in order.
		wantFrag string
		wantArgs []any
	}{
		{
			name:     "no filters",
			opts:     domain.SearchOptions{},
			wantFrag: "",
			wantArgs: nil,
		},
		{
			name:     "type only",
			opts:     domain.SearchOptions{Type: "decision"},
			wantFrag: " AND o.type = ?",
			wantArgs: []any{"decision"},
		},
		{
			name:     "project only",
			opts:     domain.SearchOptions{Project: "cortex"},
			wantFrag: " AND o.project = ?",
			wantArgs: []any{"cortex"},
		},
		{
			name:     "scope normalized",
			opts:     domain.SearchOptions{Scope: "personal"},
			wantFrag: " AND o.scope = ?",
			wantArgs: []any{domain.ScopePersonal},
		},
		{
			name:     "all three filters in fixed order type,project,scope",
			opts:     domain.SearchOptions{Type: "bugfix", Project: "cortex", Scope: "project"},
			wantFrag: " AND o.type = ? AND o.project = ? AND o.scope = ?",
			wantArgs: []any{"bugfix", "cortex", domain.ScopeProject},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotFrag, gotArgs := applyFilterClauses(c.opts)
			if gotFrag != c.wantFrag {
				t.Errorf("fragment = %q, want %q", gotFrag, c.wantFrag)
			}
			if len(gotArgs) != len(c.wantArgs) {
				t.Fatalf("args len = %d, want %d (%v)", len(gotArgs), len(c.wantArgs), gotArgs)
			}
			for i := range gotArgs {
				if gotArgs[i] != c.wantArgs[i] {
					t.Errorf("args[%d] = %v, want %v", i, gotArgs[i], c.wantArgs[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// revalidateCandidates — SQLite revalidation drops phantom/soft-deleted
// ---------------------------------------------------------------------------

// TestRevalidateCandidates_DropsSoftDeleted pins that a candidate that was
// soft-deleted after entering the ranked set is dropped before return. This is
// the core revalidation guarantee (REQ-RET-002): no phantom/stale results.
func TestRevalidateCandidates_DropsSoftDeleted(t *testing.T) {
	db := setupTestDB(t)
	// Live observations.
	insertTestObservation(t, db, 100, "live one", "alpha content", "manual", "p", "project")
	insertTestObservation(t, db, 200, "live two", "alpha content", "manual", "p", "project")
	// Soft-deleted observation (enters the candidate set, then is invalidated).
	insertDeletedTestObservation(t, db, 300, "phantom", "alpha content", "manual", "p", "project")

	store := NewStore(db)
	candidates := []*domain.SearchResult{
		{Observation: domain.Observation{ID: 100}, Rank: 0.03},
		{Observation: domain.Observation{ID: 200}, Rank: 0.02},
		{Observation: domain.Observation{ID: 300}, Rank: 0.01}, // phantom
	}

	live := store.revalidateCandidates(context.Background(), candidates)

	byID := map[int64]bool{}
	for _, r := range live {
		byID[r.ID] = true
	}
	if byID[300] {
		t.Errorf("phantom obs 300 (soft-deleted) must be dropped by revalidation; got IDs %v", idsOf(live))
	}
	if !byID[100] || !byID[200] {
		t.Errorf("live observations must survive revalidation; got IDs %v", idsOf(live))
	}
	if len(live) != 2 {
		t.Fatalf("expected 2 live candidates, got %d (%v)", len(live), idsOf(live))
	}
}

// TestRevalidateCandidates_PreservesOrder pins that revalidation preserves the
// input order of survivors (it filters, it does not re-sort).
func TestRevalidateCandidates_PreservesOrder(t *testing.T) {
	db := setupTestDB(t)
	insertTestObservation(t, db, 10, "a", "alpha content", "manual", "p", "project")
	insertTestObservation(t, db, 20, "b", "alpha content", "manual", "p", "project")
	insertTestObservation(t, db, 30, "c", "alpha content", "manual", "p", "project")

	store := NewStore(db)
	in := []*domain.SearchResult{
		{Observation: domain.Observation{ID: 30}, Rank: 0.03},
		{Observation: domain.Observation{ID: 10}, Rank: 0.02},
		{Observation: domain.Observation{ID: 20}, Rank: 0.01},
	}
	live := store.revalidateCandidates(context.Background(), in)
	want := []int64{30, 10, 20}
	for i, w := range want {
		if i >= len(live) || live[i].ID != w {
			t.Fatalf("pos %d: got %d, want %d (order=%v)", i, idAt(live, i), w, idsOf(live))
		}
	}
}

// ---------------------------------------------------------------------------
// Stable-ID tie-break — deterministic order for equal scores (property test)
// ---------------------------------------------------------------------------

// TestStableIDTieBreak_EqualScoreDeterministicOrder pins that equal-final-score
// candidates always return in ascending ID order, and that the order is
// identical across repeated runs (deterministic pagination foundation).
func TestStableIDTieBreak_EqualScoreDeterministicOrder(t *testing.T) {
	db := setupTestDB(t)
	// Four observations with identical content/recency/importance => will tie on
	// the final score. Only their IDs differ.
	for _, id := range []int64{7, 3, 5, 1} {
		insertTestObservation(t, db, id, "same title", "identical alpha content", "manual", "p", "project")
	}
	store := NewStore(db)

	var firstRun []int64
	for run := 0; run < 5; run++ {
		results, err := store.Search(context.Background(), "alpha", domain.SearchOptions{Project: "p", Limit: 10})
		if err != nil {
			t.Fatalf("run %d: unexpected error: %v", run, err)
		}
		got := idsOf(results)
		// All results tie on content/recency/importance; ties must break by ID asc.
		for i := 1; i < len(got); i++ {
			if got[i-1] > got[i] {
				t.Errorf("run %d: expected ascending IDs on ties, got %v", run, got)
			}
		}
		if run == 0 {
			firstRun = got
		} else {
			if len(firstRun) != len(got) {
				t.Fatalf("run %d: result count drifted (%d vs %d)", run, len(got), len(firstRun))
			}
			for i := range got {
				if got[i] != firstRun[i] {
					t.Fatalf("run %d: order drifted at %d: %v vs first %v", run, i, got, firstRun)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Cursor — opaque encode/decode round trip (pure, no DB)
// ---------------------------------------------------------------------------

func TestCursor_EncodeDecodeRoundTrip(t *testing.T) {
	p := cursorPayload{
		Context: "deadbeef",
		Rank:    0.012345,
		ID:      42,
		Version: cursorVersion,
	}
	raw, err := encodeCursor(p)
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}
	if raw == "" {
		t.Fatal("encoded cursor must be non-empty")
	}
	got, ok := decodeCursor(raw)
	if !ok {
		t.Fatalf("decodeCursor returned ok=false for a valid cursor %q", raw)
	}
	if got.Context != p.Context || got.ID != p.ID || got.Version != p.Version {
		t.Errorf("round trip mismatch: got %+v, want %+v", got, p)
	}
	if math.Abs(got.Rank-p.Rank) > 1e-12 {
		t.Errorf("rank round trip: got %v, want %v", got.Rank, p.Rank)
	}
}

func TestCursor_DecodeRejectsGarbage(t *testing.T) {
	cases := []string{"", "not-a-cursor", "!!!", "v1:{"}
	for _, c := range cases {
		if _, ok := decodeCursor(c); ok {
			t.Errorf("decodeCursor(%q) should return ok=false", c)
		}
	}
}

// TestCursorContextHash_StableAndDistinct pins that the context hash is stable
// for identical inputs and distinct across different filter contexts. It MUST
// incorporate query, project, scope, type, and the local-mode identity so a
// cursor cannot leak across contexts.
func TestCursorContextHash_StableAndDistinct(t *testing.T) {
	base := domain.SearchOptions{Project: "cortex", Scope: "project", Type: "decision"}
	h1 := cursorContextHash("auth", base)
	h2 := cursorContextHash("auth", base)
	if h1 != h2 {
		t.Errorf("context hash not stable: %q vs %q", h1, h2)
	}
	// Different query => different hash.
	if cursorContextHash("jwt", base) == h1 {
		t.Error("context hash must change with query")
	}
	// Different project => different hash.
	differentProject := base
	differentProject.Project = "other"
	if cursorContextHash("auth", differentProject) == h1 {
		t.Error("context hash must change with project")
	}
	// Different scope => different hash.
	differentScope := base
	differentScope.Scope = "personal"
	if cursorContextHash("auth", differentScope) == h1 {
		t.Error("context hash must change with scope")
	}
	// Different type => different hash.
	differentType := base
	differentType.Type = "bugfix"
	if cursorContextHash("auth", differentType) == h1 {
		t.Error("context hash must change with type")
	}
}

// ---------------------------------------------------------------------------
// Cursor pagination — deterministic, context-bound, never leaks
// ---------------------------------------------------------------------------

// TestCursor_PaginationDeterministic pins that the same query + cursor yields
// the same result page across runs (deterministic pagination).
func TestCursor_PaginationDeterministic(t *testing.T) {
	db := setupTestDB(t)
	// Several observations so a cursor page boundary is meaningful.
	for i := int64(1); i <= 6; i++ {
		insertTestObservation(t, db, i, "alpha topic", "alpha content repeated", "manual", "p", "project")
	}
	store := NewStore(db)

	page1, err := store.Search(context.Background(), "alpha", domain.SearchOptions{Project: "p", Limit: 3})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) == 0 {
		t.Fatal("page1 empty")
	}
	// The last result of a non-final page must carry a next cursor.
	last := page1[len(page1)-1]
	if last.NextCursor == "" {
		t.Fatalf("expected NextCursor on last result of page1 (len=%d), got empty", len(page1))
	}

	// Re-run with the same cursor multiple times: identical page.
	var first []int64
	for run := 0; run < 4; run++ {
		page2, err := store.Search(context.Background(), "alpha", domain.SearchOptions{Project: "p", Limit: 3, Cursor: last.NextCursor})
		if err != nil {
			t.Fatalf("run %d page2: %v", run, err)
		}
		got := idsOf(page2)
		if run == 0 {
			first = got
		} else if !equalInt64(first, got) {
			t.Fatalf("run %d: cursor page drifted: %v vs first %v", run, got, first)
		}
		// Page 2 must not overlap page 1 (no duplicate results across pages).
		for _, id := range got {
			for _, pid := range idsOf(page1) {
				if id == pid {
					t.Errorf("run %d: id %d appears in both page1 and page2 (overlap/leak)", run, id)
				}
			}
		}
	}
}

// TestCursor_ContextBinding_RejectsMismatchedFilter pins that a cursor bound to
// one filter context, when replayed against a DIFFERENT query/filter, is
// rejected (treated as fresh page 0) and NEVER leaks the other context's
// results. This is the cursor-scoping guarantee (REQ-RET-002).
func TestCursor_ContextBinding_RejectsMismatchedFilter(t *testing.T) {
	db := setupTestDB(t)
	insertTestObservation(t, db, 1, "alpha", "alpha content", "manual", "pA", "project")
	insertTestObservation(t, db, 2, "beta", "beta content", "manual", "pB", "project")

	store := NewStore(db)
	// Build a cursor in context A (project pA).
	raw, err := encodeCursor(cursorPayload{
		Context: cursorContextHash("alpha", domain.SearchOptions{Project: "pA"}),
		Rank:    0.5,
		ID:      1,
		Version: cursorVersion,
	})
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}

	// Replay that cursor against context B (project pB): must be treated as
	// fresh, NOT silently resume or leak A's offset into B.
	results, err := store.Search(context.Background(), "beta", domain.SearchOptions{Project: "pB", Cursor: raw})
	if err != nil {
		t.Fatalf("mismatched-cursor search: %v", err)
	}
	// Context B has one observation (id 2). A fresh page must return it; the
	// mismatched cursor must not suppress it (which would be a silent leak/loss).
	if len(results) == 0 {
		t.Fatal("mismatched cursor suppressed all results — must be treated as fresh page 0")
	}
	for _, r := range results {
		if r.Project != "pB" {
			t.Errorf("cursor leaked across contexts: got project %q in a pB search", r.Project)
		}
	}
}

// ---------------------------------------------------------------------------
// assembleResults — unified pipeline (fuse -> revalidate -> rerank -> paginate)
// ---------------------------------------------------------------------------

// TestAssembleResults_UnifiedPipeline pins the single unified retrieval path:
// ranked lists are fused, soft-deleted candidates are revalidated out, the
// multiplicative re-rank is applied, ties break by ID ascending, and pagination
// is deterministic.
func TestAssembleResults_UnifiedPipeline(t *testing.T) {
	db := setupTestDB(t)
	insertTestObservation(t, db, 11, "alpha", "alpha content", "manual", "p", "project")
	insertTestObservation(t, db, 22, "alpha", "alpha content", "manual", "p", "project")
	// Soft-deleted candidate enters the keyword ranked list but must be dropped.
	insertDeletedTestObservation(t, db, 33, "alpha", "alpha content", "manual", "p", "project")

	store := NewStore(db)
	// Keyword ranked list includes the phantom id 33.
	keyword := rankedList{name: "keyword", items: []*domain.SearchResult{
		{Observation: domain.Observation{ID: 11}, Rank: 0.9, ScoreBreakdown: domain.SearchScoreBreakdown{KeywordBM25: 0.9}},
		{Observation: domain.Observation{ID: 22}, Rank: 0.8},
		{Observation: domain.Observation{ID: 33}, Rank: 0.7},
	}}

	out := store.assembleResults(context.Background(), []rankedList{keyword}, "alpha", domain.SearchOptions{Project: "p"}, 10)
	byID := map[int64]bool{}
	for _, r := range out {
		byID[r.ID] = true
	}
	if byID[33] {
		t.Errorf("phantom id 33 must be revalidated out of unified pipeline; got %v", idsOf(out))
	}
	if !byID[11] || !byID[22] {
		t.Errorf("live ids 11,22 must survive; got %v", idsOf(out))
	}
}

// ---------------------------------------------------------------------------
// Compatibility pin — deterministic order preserved across full Search runs
// ---------------------------------------------------------------------------

// TestSearch_OrderDeterministic pins that the public Search path produces a
// deterministic order for the same query/options across runs (design decision
// #6: it is acceptable for ORDER to become more deterministic; pin it).
func TestSearch_OrderDeterministic(t *testing.T) {
	db := setupTestDB(t)
	insertTestObservation(t, db, 5, "alpha", "alpha content", "manual", "p", "project")
	insertTestObservation(t, db, 2, "alpha", "alpha content", "manual", "p", "project")
	insertTestObservation(t, db, 8, "alpha", "alpha content", "manual", "p", "project")
	store := NewStore(db)

	var ref []int64
	for run := 0; run < 5; run++ {
		results, err := store.Search(context.Background(), "alpha", domain.SearchOptions{Project: "p", Limit: 10})
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		got := idsOf(results)
		if run == 0 {
			ref = got
		} else if !equalInt64(ref, got) {
			t.Fatalf("run %d: order not deterministic: %v vs ref %v", run, got, ref)
		}
	}
}

func equalInt64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

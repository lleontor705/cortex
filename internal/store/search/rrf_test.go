package search

// rrf_test.go pins REQ-RET-002: correct ranked-list RRF (k=60), formal
// dual-level ('/' topic-exact vs keyword) fusion, and recency/importance as a
// FINAL multiplicative re-rank (NOT pseudo-ranked-lists fed into RRF).
//
// The legacy pipeline mixed BM25 scores with recency/importance as
// pseudo-ranked-lists inside RRF. These tests pin the corrected semantics:
//   - RRF operates exclusively over TRUE ranked lists (position = rank).
//   - A raw relevance SCORE (BM25/FTS5) is never treated as a rank input.
//   - Recency (0.995^hours) and importance are a final MULTIPLICATIVE re-rank
//     over the RRF-fused candidate set.
//   - '/' routing is a pure, deterministic, observable function of the query.

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// ---------------------------------------------------------------------------
// Test helpers (local to this file)
// ---------------------------------------------------------------------------

// mkFused builds an RRF-fused candidate with a known fusion score and no
// recency/importance applied yet (UpdatedAt zero -> neutral recency).
func mkFused(id int64, rrf float64) *domain.SearchResult {
	return &domain.SearchResult{
		Observation: domain.Observation{ID: id},
		Rank:        rrf,
		ScoreBreakdown: domain.SearchScoreBreakdown{
			Strategy:    "enhanced",
			FusionScore: rrf,
		},
	}
}

func idAt(rs []*domain.SearchResult, i int) int64 {
	if i < 0 || i >= len(rs) {
		return -1
	}
	return rs[i].ID
}

func idsOf(rs []*domain.SearchResult) []int64 {
	out := make([]int64, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

// ---------------------------------------------------------------------------
// RRF formula + score-as-rank defect pin (pure function, no DB)
// ---------------------------------------------------------------------------

// TestRRFFuse_FormulaExact pins the RRF formula: score(id) = sum over lists of
// 1/(k + rank), where rank is the 1-based POSITION in the list (k=60).
func TestRRFFuse_FormulaExact(t *testing.T) {
	const k = 60.0
	listA := rankedList{
		name: "topic_exact",
		items: []*domain.SearchResult{
			{Observation: domain.Observation{ID: 10}},
			{Observation: domain.Observation{ID: 20}},
			{Observation: domain.Observation{ID: 30}},
		},
	}
	listB := rankedList{
		name: "keyword",
		items: []*domain.SearchResult{
			{Observation: domain.Observation{ID: 20}},
			{Observation: domain.Observation{ID: 10}},
		},
	}

	fused := rrfFuse([]rankedList{listA, listB}, k, 10)

	want := map[int64]float64{
		10: 1/(k+1) + 1/(k+2), // rank1 in A, rank2 in B
		20: 1/(k+2) + 1/(k+1), // rank2 in A, rank1 in B
		30: 1 / (k + 3),       // rank3 in A only
	}
	if len(fused) != 3 {
		t.Fatalf("expected 3 fused candidates, got %d", len(fused))
	}
	for _, r := range fused {
		got := r.ScoreBreakdown.FusionScore
		w := want[r.ID]
		if math.Abs(got-w) > 1e-12 {
			t.Errorf("id %d: fusion score = %.12f, want %.12f", r.ID, got, w)
		}
	}
	// id10 and id20 tie (symmetric); id30 strictly last. Tie-break by ID asc.
	if fused[2].ID != 30 {
		t.Errorf("expected id30 last (lowest fusion), got order %v", idsOf(fused))
	}
}

// TestRRFFuse_ScoreAsRankPin pins that a raw relevance SCORE (BM25/FTS5) is
// NEVER treated as a rank input. Items with wildly different Rank (score)
// values at the SAME positions must yield identical fusion scores; the score
// values must not influence RRF. This is the score-as-rank defect pin
// (REQ-RET-002 defect scenario).
func TestRRFFuse_ScoreAsRankPin(t *testing.T) {
	const k = 60.0
	highScore := rankedList{name: "keyword", items: []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1}, Rank: 99999.0, ScoreBreakdown: domain.SearchScoreBreakdown{KeywordBM25: 99999.0}},
		{Observation: domain.Observation{ID: 2}, Rank: 0.0001},
	}}
	lowScore := rankedList{name: "keyword", items: []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1}, Rank: 0.5},
		{Observation: domain.Observation{ID: 2}, Rank: 0.3},
	}}

	wantPos1 := 1 / (k + 1)
	wantPos2 := 1 / (k + 2)

	for name, list := range map[string]rankedList{"high_score": highScore, "low_score": lowScore} {
		f := rrfFuse([]rankedList{list}, k, 10)
		scoreByID := map[int64]float64{}
		for _, r := range f {
			scoreByID[r.ID] = r.ScoreBreakdown.FusionScore
		}
		if math.Abs(scoreByID[1]-wantPos1) > 1e-12 {
			t.Errorf("[%s] id1 fusion = %.12f, want %.12f (position-only; raw score ignored)", name, scoreByID[1], wantPos1)
		}
		if math.Abs(scoreByID[2]-wantPos2) > 1e-12 {
			t.Errorf("[%s] id2 fusion = %.12f, want %.12f (position-only; raw score ignored)", name, scoreByID[2], wantPos2)
		}
	}
}

// TestRRFFuse_OnlyPositionsMatter pins that swapping positions changes fusion
// scores, while changing only the raw score values (keeping positions) does not.
func TestRRFFuse_OnlyPositionsMatter(t *testing.T) {
	const k = 60.0
	base := rankedList{name: "keyword", items: []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1}, Rank: 5.0},
		{Observation: domain.Observation{ID: 2}, Rank: 1.0},
	}}
	swapped := rankedList{name: "keyword", items: []*domain.SearchResult{
		{Observation: domain.Observation{ID: 2}, Rank: 5.0},
		{Observation: domain.Observation{ID: 1}, Rank: 1.0},
	}}
	get := func(f []*domain.SearchResult, id int64) float64 {
		for _, r := range f {
			if r.ID == id {
				return r.ScoreBreakdown.FusionScore
			}
		}
		return -1
	}
	fb := rrfFuse([]rankedList{base}, k, 10)
	fs := rrfFuse([]rankedList{swapped}, k, 10)
	if get(fb, 1) <= get(fb, 2) {
		t.Errorf("base: id1 (rank1) should outrank id2: %.6f vs %.6f", get(fb, 1), get(fb, 2))
	}
	if get(fs, 2) <= get(fs, 1) {
		t.Errorf("swapped: id2 (rank1) should outrank id1: %.6f vs %.6f", get(fs, 2), get(fs, 1))
	}
	// Same order, different raw scores -> identical fusion.
	sameOrder := rankedList{name: "keyword", items: []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1}, Rank: 999.0},
		{Observation: domain.Observation{ID: 2}, Rank: 0.001},
	}}
	fso := rrfFuse([]rankedList{sameOrder}, k, 10)
	if math.Abs(get(fb, 1)-get(fso, 1)) > 1e-12 || math.Abs(get(fb, 2)-get(fso, 2)) > 1e-12 {
		t.Errorf("fusion changed when only raw scores changed: base(%.6f,%.6f) sameOrder(%.6f,%.6f)",
			get(fb, 1), get(fb, 2), get(fso, 1), get(fso, 2))
	}
}

// ---------------------------------------------------------------------------
// Dual-level routing (pure, deterministic, observable)
// ---------------------------------------------------------------------------

// TestClassifyQuery_DeterministicRouting pins the dual-level routing decision:
// a query containing '/' routes to topic-exact+keyword fusion (dual_level); a
// plain query routes to keyword-only. Routing is a PURE function of the query.
func TestClassifyQuery_DeterministicRouting(t *testing.T) {
	cases := []struct {
		query string
		want  searchProfile
	}{
		{"auth/setup", profileDualLevel},
		{"sdd/cortex-v2/spec", profileDualLevel},
		{"  architecture/auth-model  ", profileDualLevel},
		{"authentication", profileKeyword},
		{"jwt tokens", profileKeyword},
		{"", profileKeyword},
		{"   ", profileKeyword},
	}
	for _, c := range cases {
		got := classifyQuery(c.query)
		if got != c.want {
			t.Errorf("classifyQuery(%q) = %q, want %q", c.query, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Final multiplicative re-rank: recency + importance (pure helpers)
// ---------------------------------------------------------------------------

// TestRecencyFactor_Formula pins the exact recency factor 0.995^hours, with
// negative hours clamped to 0 (future timestamps treated as now).
func TestRecencyFactor_Formula(t *testing.T) {
	cases := []struct {
		hours float64
		want  float64
	}{
		{0, 1.0},
		{1, 0.995},
		{24, math.Pow(0.995, 24)},
		{720, math.Pow(0.995, 720)},
		{-5, 1.0},
	}
	for _, c := range cases {
		got := recencyFactor(c.hours)
		if math.Abs(got-c.want) > 1e-12 {
			t.Errorf("recencyFactor(%v) = %.12f, want %.12f", c.hours, got, c.want)
		}
	}
}

// TestImportanceFactor pins the importance multiplier: neutral (1.0) when no
// score data exists or score is 0; otherwise (1 + score) — a monotonic
// multiplicative boost that never zeroes a legitimate match.
func TestImportanceFactor(t *testing.T) {
	cases := []struct {
		score float64
		found bool
		want  float64
	}{
		{0, false, 1.0},
		{4.0, true, 5.0},
		{1.0, true, 2.0},
		{0.0, true, 1.0},
	}
	for _, c := range cases {
		got := importanceFactor(c.score, c.found)
		if math.Abs(got-c.want) > 1e-12 {
			t.Errorf("importanceFactor(%v,%v) = %.6f, want %.6f", c.score, c.found, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Final multiplicative re-rank: importance is NOT an RRF input (DB-backed)
// ---------------------------------------------------------------------------

// TestRerank_ImportanceIsMultiplicativeNotRRFInput pins that importance is
// applied as a FINAL multiplicative re-rank over RRF-fused candidates, NOT as a
// pseudo-ranked-list inside RRF. Candidates are given known RRF fusion scores;
// the DB holds importance scores. The final order must follow the multiplicative
// model (rrf * (1+importance)).
//
// NOTE: id30 has the HIGHEST RRF but ranks LAST — impossible if importance were
// an RRF input (additive 1/(k+rank)); only the multiplicative re-rank produces
// this inversion. This is the defect pin (REQ-RET-002 defect scenario).
func TestRerank_ImportanceIsMultiplicativeNotRRFInput(t *testing.T) {
	db := setupTestDB(t)
	// Observations 10/20/30 must exist (FK on importance_scores).
	insertTestObservation(t, db, 10, "t10", "c10", "manual", "p", "project")
	insertTestObservation(t, db, 20, "t20", "c20", "manual", "p", "project")
	insertTestObservation(t, db, 30, "t30", "c30", "manual", "p", "project")
	insertImportanceScore(t, db, 10, 4.0, 5, time.Now())
	insertImportanceScore(t, db, 20, 1.0, 2, time.Now())
	insertImportanceScore(t, db, 30, 0.0, 0, time.Now())

	store := NewStore(db)
	// Known RRF fusion scores; UpdatedAt zero -> recency neutral.
	candidates := []*domain.SearchResult{
		mkFused(10, 0.010),
		mkFused(20, 0.020),
		mkFused(30, 0.030),
	}
	// Multiplicative model (factor = 1 + importance):
	//   id10: 0.010 * 5.0 = 0.050
	//   id20: 0.020 * 2.0 = 0.040
	//   id30: 0.030 * 1.0 = 0.030
	// Order DESC: 10, 20, 30.
	reranked := store.rerankByRecencyImportance(context.Background(), candidates, 100)
	wantOrder := []int64{10, 20, 30}
	for i, w := range wantOrder {
		if i >= len(reranked) || reranked[i].ID != w {
			t.Fatalf("pos %d: got id %d, want %d (order=%v)", i, idAt(reranked, i), w, idsOf(reranked))
		}
	}
}

// TestRerank_RecencyIsFinalMultiplier pins that recency (0.995^hours from the
// observation's timestamp) is a FINAL multiplicative re-rank, not an RRF input.
// id2 has the LOWER RRF but wins because it is far more recent.
func TestRerank_RecencyIsFinalMultiplier(t *testing.T) {
	db := setupTestDB(t)
	store := NewStore(db)
	now := time.Now()
	candidates := []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1, UpdatedAt: now.Add(-720 * time.Hour)}, Rank: 0.030,
			ScoreBreakdown: domain.SearchScoreBreakdown{Strategy: "enhanced", FusionScore: 0.030}},
		{Observation: domain.Observation{ID: 2, UpdatedAt: now.Add(-1 * time.Hour)}, Rank: 0.010,
			ScoreBreakdown: domain.SearchScoreBreakdown{Strategy: "enhanced", FusionScore: 0.010}},
	}
	// No importance rows -> factor 1.0.
	//   id1 = 0.030 * 0.995^720 ~ 0.000804
	//   id2 = 0.010 * 0.995^1   ~ 0.00995
	// Order DESC: 2, 1.
	reranked := store.rerankByRecencyImportance(context.Background(), candidates, 100)
	wantOrder := []int64{2, 1}
	for i, w := range wantOrder {
		if i >= len(reranked) || reranked[i].ID != w {
			t.Fatalf("pos %d: got id %d, want %d (order=%v)", i, idAt(reranked, i), w, idsOf(reranked))
		}
	}
}

// ---------------------------------------------------------------------------
// Dual-level pipeline wiring (DB-backed, REQ-RET-002 happy + routing scenarios)
// ---------------------------------------------------------------------------

// TestSearch_DualLevelFusesTopicExactAndKeyword pins the dual-level '/' routing:
// a topic-exact retriever and a keyword retriever each emit a ranked list, RRF
// fuses them, and the final result includes BOTH sources (REQ-RET-002 happy).
func TestSearch_DualLevelFusesTopicExactAndKeyword(t *testing.T) {
	db := setupTestDB(t)
	// Topic-exact hit only (topic_key matches; no 'auth'/'setup' tokens).
	insertTestObservationWithTopicKey(t, db, 1, "Config", "Settings file", "config", "p", "project", "auth/setup")
	// Keyword hit only (no topic_key; strong 'auth setup' overlap).
	insertTestObservation(t, db, 2, "Auth Setup Guide", "How to configure auth setup step by step", "manual", "p", "project")

	if got := classifyQuery("auth/setup"); got != profileDualLevel {
		t.Fatalf("routing: classifyQuery = %q, want %q", got, profileDualLevel)
	}
	store := NewStore(db)
	results, err := store.Search(context.Background(), "auth/setup", domain.SearchOptions{Project: "p", Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byID := map[int64]*domain.SearchResult{}
	for _, r := range results {
		byID[r.ID] = r
	}
	if r, ok := byID[1]; !ok {
		t.Error("expected topic-exact obs1 in dual-level fusion results")
	} else {
		if !r.ScoreBreakdown.TopicKeyExact {
			t.Error("expected TopicKeyExact on topic-exact result")
		}
		if r.ScoreBreakdown.FusionScore == 0 {
			t.Error("expected FusionScore populated on topic-exact result")
		}
	}
	if _, ok := byID[2]; !ok {
		t.Error("expected keyword obs2 in dual-level fusion results")
	}
}

// TestSearch_KeywordRoutingDoesNotUseTopicExact pins that a plain (no '/')
// query routes to keyword-only retrieval and never performs topic-exact lookup
// (REQ-RET-002 routing-consistency edge scenario).
func TestSearch_KeywordRoutingDoesNotUseTopicExact(t *testing.T) {
	db := setupTestDB(t)
	// An observation that ONLY matches by topic_key (no keyword overlap).
	insertTestObservationWithTopicKey(t, db, 1, "Config", "Settings file", "config", "p", "project", "auth/setup")

	if got := classifyQuery("authentication"); got != profileKeyword {
		t.Fatalf("routing: classifyQuery = %q, want %q", got, profileKeyword)
	}
	store := NewStore(db)
	results, err := store.Search(context.Background(), "authentication", domain.SearchOptions{Project: "p", Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, r := range results {
		if r.ID == 1 {
			t.Error("keyword routing must not surface topic-exact-only obs1 for a plain query")
		}
	}
}

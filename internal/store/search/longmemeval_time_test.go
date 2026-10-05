package search

// longmemeval_time_test.go covers REQ-LME-001 acceptance criterion 2:
// time-aware query expansion prunes ranked lists by an inferred time range
// and degrades safely (unpruned fallback) on missing/malformed timestamps.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestHeuristicTimeRange_Patterns(t *testing.T) {
	ref := time.Date(2025, 6, 15, 12, 30, 0, 0, time.UTC)
	utc := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	cases := []struct {
		name     string
		query    string
		wantFrom time.Time
		wantTo   time.Time
		wantOK   bool
	}{
		{"iso day", "update from 2024-03-15", utc(2024, 3, 15), utc(2024, 3, 16), true},
		{"month year", "notes from March 2024", utc(2024, 3, 1), utc(2024, 4, 1), true},
		{"today", "what about today", utc(2025, 6, 15), utc(2025, 6, 16), true},
		{"yesterday", "summarize yesterday", utc(2025, 6, 14), utc(2025, 6, 15), true},
		{"tomorrow", "plan for tomorrow", utc(2025, 6, 16), utc(2025, 6, 17), true},
		{"last week", "last week report", ref.AddDate(0, 0, -7), ref, true},
		{"last month", "last month totals", ref.AddDate(0, -1, 0), ref, true},
		{"last year", "last year budget", ref.AddDate(-1, 0, 0), ref, true},
		{"bare year", "deploy logs from 2023", utc(2023, 1, 1), utc(2024, 1, 1), true},
		{"no signal", "plain keyword query", time.Time{}, time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := heuristicTimeRange(tc.query, ref)
			if ok != tc.wantOK {
				t.Fatalf("heuristicTimeRange(%q) ok=%v, want %v", tc.query, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if !got.From.Equal(tc.wantFrom) || !got.To.Equal(tc.wantTo) {
				t.Errorf("range = [%v, %v], want [%v, %v]", got.From, got.To, tc.wantFrom, tc.wantTo)
			}
		})
	}
}

// insertRawTimestampObservation inserts created_at/updated_at exactly as
// given, including malformed values that parseSearchTime cannot parse.
func insertRawTimestampObservation(t *testing.T, db *sql.DB, id int64, title, content, obsType, project, scope, rawTime string) {
	t.Helper()
	ensureTestSession(t, db)
	_, err := db.Exec(`
		INSERT INTO observations (id, session_id, type, title, content, project, scope, created_at, updated_at)
		VALUES (?, 'test-session', ?, ?, ?, ?, ?, ?, ?)
	`, id, obsType, title, content, project, scope, rawTime, rawTime)
	if err != nil {
		t.Fatalf("insert raw timestamp observation: %v", err)
	}
}

// TestSearch_TimeRangePruning_E2E pins criterion 2 end to end: a temporal
// query keeps the in-range and malformed-timestamp candidates, prunes the
// out-of-range one, and a no-signal query returns everything.
func TestSearch_TimeRangePruning_E2E(t *testing.T) {
	db := setupTestDB(t)
	content := "march 2024 status update milestone"
	insertTestObservationUpdatedAt(t, db, 41, "in range", content, "manual", "lme-time", "project",
		time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC))
	insertTestObservationUpdatedAt(t, db, 42, "out of range", content, "manual", "lme-time", "project",
		time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC))
	insertRawTimestampObservation(t, db, 43, "malformed stamp", content, "manual", "lme-time", "project",
		"not-a-timestamp")

	store := NewStore(db)
	ctx := context.Background()
	opts := domain.SearchOptions{Project: "lme-time"}

	temporal, err := store.Search(ctx, "march 2024 status update", opts)
	if err != nil {
		t.Fatalf("temporal search: %v", err)
	}
	temporalIDs := map[int64]bool{}
	for _, r := range temporal {
		temporalIDs[r.ID] = true
	}
	if !temporalIDs[41] {
		t.Errorf("in-range obs 41 must be kept, got %v", idsOf(temporal))
	}
	if !temporalIDs[43] {
		t.Errorf("malformed-timestamp obs 43 must survive via unpruned fallback, got %v", idsOf(temporal))
	}
	if temporalIDs[42] {
		t.Errorf("out-of-range obs 42 must be pruned, got %v", idsOf(temporal))
	}

	noSignal, err := store.Search(ctx, "status update", opts)
	if err != nil {
		t.Fatalf("no-signal search: %v", err)
	}
	noSignalIDs := map[int64]bool{}
	for _, r := range noSignal {
		noSignalIDs[r.ID] = true
	}
	for _, id := range []int64{41, 42, 43} {
		if !noSignalIDs[id] {
			t.Errorf("no-signal query must return obs %d unpruned, got %v", id, idsOf(noSignal))
		}
	}
}

// TestAssembleResults_TimeRangeInferencerSeam pins the pluggable LLM seam:
// an injected TimeRangeInferencer prunes fabricated ranked lists before
// fusion, receives the query, and is the sole source of the range.
func TestAssembleResults_TimeRangeInferencerSeam(t *testing.T) {
	db := setupTestDB(t)
	utc := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	insertTestObservationUpdatedAt(t, db, 51, "keep a", "seam content", "manual", "p", "project", utc(2024, 5, 1))
	insertTestObservationUpdatedAt(t, db, 52, "prune b", "seam content", "manual", "p", "project", utc(2023, 1, 1))
	insertTestObservationUpdatedAt(t, db, 53, "keep c", "seam content", "manual", "p", "project", utc(2024, 9, 1))

	store := NewStore(db)
	var sawQuery string
	store.TimeRangeInferencer = func(query string, reference time.Time) (*domain.TimeRange, bool) {
		sawQuery = query
		return &domain.TimeRange{From: utc(2024, 1, 1), To: utc(2025, 1, 1)}, true
	}

	mk := func(id int64, createdAt time.Time) *domain.SearchResult {
		return &domain.SearchResult{Observation: domain.Observation{ID: id, CreatedAt: createdAt}}
	}
	lists := []rankedList{
		{name: "keyword", items: []*domain.SearchResult{
			mk(51, utc(2024, 5, 1)),
			mk(52, utc(2023, 1, 1)),
			mk(53, utc(2024, 9, 1)),
		}},
	}

	out := store.assembleResults(context.Background(), lists, "fabricated query",
		domain.SearchOptions{Project: "p"}, 10)

	if sawQuery != "fabricated query" {
		t.Errorf("inferencer must receive the query, got %q", sawQuery)
	}
	got := map[int64]bool{}
	for _, r := range out {
		got[r.ID] = true
	}
	if got[52] {
		t.Errorf("obs 52 (2023) must be pruned by the injected inferencer, got %v", idsOf(out))
	}
	if !got[51] || !got[53] {
		t.Errorf("obs 51/53 inside the injected range must survive, got %v", idsOf(out))
	}
}

// TestTimeRangeContains_UnprunedFallback pins the malformed/missing timestamp
// contract directly: zero times are always contained and zero bounds are
// open sides of the range.
func TestTimeRangeContains_UnprunedFallback(t *testing.T) {
	tr := &domain.TimeRange{
		From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if !timeRangeContains(tr, time.Time{}) {
		t.Error("zero (missing/malformed) timestamp must be kept")
	}
	if !timeRangeContains(tr, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("in-range timestamp must be kept")
	}
	if timeRangeContains(tr, time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("pre-range timestamp must be pruned")
	}
	open := &domain.TimeRange{From: time.Time{}}
	if !timeRangeContains(open, time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("zero From must be an open lower bound")
	}
}

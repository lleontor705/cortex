package search

import (
	"context"
	"sync"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
)

// feedback_test.go pins REQ-RET-001: request/session-scoped feedback
// attribution with the shared mutable search-query field removed.
//
// Coverage:
//   - happy:   feedback attributed to the originating SearchID
//   - edge:    concurrent interleaved searches never cross-attribute (race pin)
//   - error:   unknown/expired SearchID is a safe no-op (no panic, no fallback)
//   - property: SearchID uniqueness; results carry a stable SearchID

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// feedbackRecord captures one feedback attribution as observed by the sink.
type feedbackRecord struct {
	searchID domain.SearchID
	query    string
	obsID    int64
	rank     int
}

// recordingSink is a thread-safe FeedbackSink that records every attribution so
// tests can assert feedback bound to the CORRECT SearchID (REQ-RET-001).
type recordingSink struct {
	mu      sync.Mutex
	records []feedbackRecord
}

func (r *recordingSink) sink() FeedbackSink {
	return func(_ context.Context, searchID domain.SearchID, query string, observationID int64, rankPosition int) error {
		r.mu.Lock()
		r.records = append(r.records, feedbackRecord{searchID, query, observationID, rankPosition})
		r.mu.Unlock()
		return nil
	}
}

func (r *recordingSink) snapshot() []feedbackRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]feedbackRecord, len(r.records))
	copy(out, r.records)
	return out
}

// ---------------------------------------------------------------------------
// Scenario: Happy path — feedback attributed to correct search (REQ-RET-001)
// ---------------------------------------------------------------------------

// TestFeedback_AttributesToCorrectSearchID verifies that feedback referencing a
// result from Search A is recorded against Search A's SearchID and query — not
// whichever search happened to run last.
func TestFeedback_AttributesToCorrectSearchID(t *testing.T) {
	db := setupTestDB(t)
	insertTestObservation(t, db, 1, "Alpha Gamma", "alpha gamma content", "decision", "p", "project")
	insertTestObservation(t, db, 2, "Beta Gamma", "beta gamma content", "decision", "p", "project")

	store := NewStore(db)
	sink := &recordingSink{}
	store.SetFeedbackSink(sink.sink())

	// Search A returns the Alpha result.
	resultsA, err := store.Search(context.Background(), "alpha", domain.SearchOptions{Project: "p"})
	if err != nil {
		t.Fatalf("Search A: %v", err)
	}
	if len(resultsA) == 0 {
		t.Fatal("Search A returned no results")
	}
	searchIDA := resultsA[0].SearchID
	if searchIDA == "" {
		t.Fatal("Search A result carries no SearchID")
	}

	// A second search runs AFTER Search A. Under the legacy shared-field model
	// this would clobber the shared state; under request-scoped attribution it
	// must NOT affect feedback for Search A.
	if _, err := store.Search(context.Background(), "beta", domain.SearchOptions{Project: "p"}); err != nil {
		t.Fatalf("Search B: %v", err)
	}

	// Feedback for the Alpha result must attribute to query "alpha".
	if err := store.RecordFeedback(context.Background(), searchIDA, resultsA[0].ID, 1); err != nil {
		t.Fatalf("RecordFeedback: %v", err)
	}

	got := sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 attribution, got %d", len(got))
	}
	if got[0].query != "alpha" {
		t.Errorf("cross-attribution: feedback recorded against %q, want %q", got[0].query, "alpha")
	}
	if got[0].searchID != searchIDA {
		t.Errorf("searchID mismatch: got %q, want %q", got[0].searchID, searchIDA)
	}
}

// ---------------------------------------------------------------------------
// Scenario: Edge — concurrent interleaved searches (race pin, REQ-RET-001)
// ---------------------------------------------------------------------------

// TestFeedback_ConcurrentNoCrossAttribution runs two searches concurrently,
// interleaving result attribution. Each feedback must bind to its OWN SearchID;
// cross-attribution is impossible. This test MUST pass under `-race` with no
// data race reported — it pins that the shared mutable search-query defect is
// fixed.
func TestFeedback_ConcurrentNoCrossAttribution(t *testing.T) {
	db := setupTestDB(t)
	insertTestObservation(t, db, 1, "Alpha Unique Term", "alpha unique content", "decision", "p", "project")
	insertTestObservation(t, db, 2, "Beta Unique Term", "beta unique content", "decision", "p", "project")

	store := NewStore(db)
	sink := &recordingSink{}
	store.SetFeedbackSink(sink.sink())

	type searchOutcome struct {
		searchID domain.SearchID
		obsID    int64
		query    string
	}

	const goroutines = 50
	outcomes := make([]searchOutcome, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Alternate between query "alpha" and query "beta" across goroutines so the
	// searches interleave aggressively. Each goroutine records feedback using the
	// SearchID stamped on its OWN result.
	for i := 0; i < goroutines; i++ {
		i := i
		query := "alpha"
		if i%2 == 1 {
			query = "beta"
		}
		go func() {
			defer wg.Done()
			results, err := store.Search(context.Background(), query, domain.SearchOptions{Project: "p"})
			if err != nil || len(results) == 0 {
				t.Errorf("goroutine %d: search %q err=%v len=%d", i, query, err, len(results))
				return
			}
			sid := results[0].SearchID
			if sid == "" {
				t.Errorf("goroutine %d: empty SearchID", i)
				return
			}
			if err := store.RecordFeedback(context.Background(), sid, results[0].ID, 1); err != nil {
				t.Errorf("goroutine %d: RecordFeedback: %v", i, err)
				return
			}
			outcomes[i] = searchOutcome{searchID: sid, obsID: results[0].ID, query: query}
		}()
	}
	wg.Wait()

	// Every attribution must match its originating query — no cross-attribution.
	records := sink.snapshot()
	if len(records) == 0 {
		t.Fatal("no feedback recorded")
	}
	// Build the expected searchID -> query map from outcomes.
	want := make(map[domain.SearchID]string, goroutines)
	for _, o := range outcomes {
		want[o.searchID] = o.query
	}
	for _, rec := range records {
		expectedQuery, ok := want[rec.searchID]
		if !ok {
			t.Errorf("feedback attributed to unknown SearchID %q", rec.searchID)
			continue
		}
		if rec.query != expectedQuery {
			t.Errorf("cross-attribution for SearchID %q: got query %q, want %q", rec.searchID, rec.query, expectedQuery)
		}
	}
	// No two distinct queries may share a SearchID (uniqueness under concurrency).
	alphaSeen, betaSeen := false, false
	for _, rec := range records {
		if rec.query == "alpha" {
			alphaSeen = true
		}
		if rec.query == "beta" {
			betaSeen = true
		}
	}
	if !alphaSeen || !betaSeen {
		t.Errorf("expected both queries attributed; alpha=%v beta=%v", alphaSeen, betaSeen)
	}
}

// ---------------------------------------------------------------------------
// Scenario: Error — unknown/expired SearchID is a safe no-op (REQ-RET-001)
// ---------------------------------------------------------------------------

// TestFeedback_UnknownSearchID_SafeNoop verifies that feedback referencing an
// unknown or expired SearchID is handled safely: no panic, no error, and NO
// global fallback to a shared query.
func TestFeedback_UnknownSearchID_SafeNoop(t *testing.T) {
	db := setupTestDB(t)
	insertTestObservation(t, db, 1, "Some Topic", "some content here", "decision", "p", "project")

	store := NewStore(db)
	sink := &recordingSink{}
	store.SetFeedbackSink(sink.sink())

	// An unknown SearchID must NOT trigger the sink and must NOT panic.
	unknown := domain.SearchID("sch_doesnotexist")
	if err := store.RecordFeedback(context.Background(), unknown, 1, 0); err != nil {
		t.Errorf("unknown SearchID returned error: %v", err)
	}
	if got := sink.snapshot(); len(got) != 0 {
		t.Errorf("unknown SearchID triggered sink (global fallback): %d records", len(got))
	}
}

// TestFeedback_NoSink_SafeNoop verifies that without a wired sink, feedback is
// validated (SearchID known) but performs no persistence — never panics.
func TestFeedback_NoSink_SafeNoop(t *testing.T) {
	db := setupTestDB(t)
	insertTestObservation(t, db, 1, "Some Topic", "some content here", "decision", "p", "project")

	store := NewStore(db) // no sink wired
	results, err := store.Search(context.Background(), "some", domain.SearchOptions{Project: "p"})
	if err != nil || len(results) == 0 {
		t.Fatalf("search: err=%v len=%d", err, len(results))
	}
	// Known SearchID but no sink: safe no-op (no persistence, no panic).
	if err := store.RecordFeedback(context.Background(), results[0].SearchID, results[0].ID, 1); err != nil {
		t.Errorf("RecordFeedback with no sink returned error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Property: SearchID uniqueness and result stamping (REQ-RET-001)
// ---------------------------------------------------------------------------

// TestSearchID_Uniqueness verifies that sequential and concurrent searches
// produce distinct SearchIDs.
func TestSearchID_Uniqueness(t *testing.T) {
	seen := make(map[domain.SearchID]bool)
	const n = 200
	ids := make([]domain.SearchID, n)
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			id := NewSearchID()
			mu.Lock()
			ids[i] = id
			if seen[id] {
				t.Errorf("duplicate SearchID generated: %q", id)
			}
			seen[id] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if id == "" {
			t.Error("empty SearchID generated")
		}
		if id[:4] != "sch_" {
			t.Errorf("SearchID %q missing sch_ prefix", id)
		}
	}
}

// TestResults_CarrySearchID verifies every result from a search carries the
// same stable SearchID, and two searches carry distinct SearchIDs.
func TestResults_CarrySearchID(t *testing.T) {
	db := setupTestDB(t)
	insertTestObservation(t, db, 1, "Alpha Gamma", "alpha gamma content", "decision", "p", "project")
	insertTestObservation(t, db, 2, "Beta Gamma", "beta gamma content", "decision", "p", "project")

	store := NewStore(db)

	r1, err := store.Search(context.Background(), "gamma", domain.SearchOptions{Project: "p"})
	if err != nil {
		t.Fatalf("search 1: %v", err)
	}
	if len(r1) == 0 {
		t.Fatal("search 1 returned no results")
	}
	sid1 := r1[0].SearchID
	if sid1 == "" {
		t.Fatal("results carry no SearchID")
	}
	for _, r := range r1 {
		if r.SearchID != sid1 {
			t.Errorf("result #%d SearchID %q != %q (inconsistent within one search)", r.ID, r.SearchID, sid1)
		}
	}

	r2, err := store.Search(context.Background(), "gamma", domain.SearchOptions{Project: "p"})
	if err != nil {
		t.Fatalf("search 2: %v", err)
	}
	if len(r2) == 0 {
		t.Fatal("search 2 returned no results")
	}
	if r2[0].SearchID == sid1 {
		t.Error("two distinct searches produced the same SearchID")
	}
}

package search

// longmemeval_fact_test.go covers REQ-LME-001 acceptance criteria 1 and 3:
// fact-augmented FTS5 keys recall interrogative queries that pure session-text
// keys miss, and long sessions are decomposed into bounded units before
// indexing with byte-exact (lossless) reassembly.

import (
	"context"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

const lmeFactSession = "Our team celebrated a surprise victory during the summer picnic."
const lmeFactQuery = "What was the surprise victory during the summer picnic?"

// TestLongMemEval_FactKeysRecallInterrogativeQuery pins the fact-key happy
// path: the interrogative query has NO match against session-text keys
// (precondition), yet Search recalls the session through the fact-key list.
func TestLongMemEval_FactKeysRecallInterrogativeQuery(t *testing.T) {
	db := setupTestDB(t)
	insertTestObservation(t, db, 101, "Picnic report", lmeFactSession, "manual", "lme-project", "project")
	store := NewStore(db)
	ctx := context.Background()
	opts := domain.SearchOptions{Project: "lme-project"}

	keywordHits, err := store.searchKeywords(ctx, lmeFactQuery, opts, 10)
	if err != nil {
		t.Fatalf("keyword precondition search: %v", err)
	}
	if len(keywordHits) != 0 {
		t.Fatalf("precondition: session-text keys must miss the interrogative query, got %d hits", len(keywordHits))
	}

	results, err := store.Search(ctx, lmeFactQuery, opts)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	found := false
	for _, r := range results {
		if r.ID == 101 {
			found = true
		}
	}
	if !found {
		t.Fatalf("fact keys must recall session 101 for %q, got %v", lmeFactQuery, idsOf(results))
	}

	var factRows, factMeta int
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_fact_keys`).Scan(&factRows); err != nil {
		t.Fatalf("count fact keys: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_fact_meta`).Scan(&factMeta); err != nil {
		t.Fatalf("count fact meta: %v", err)
	}
	if factRows == 0 || factMeta != 1 {
		t.Errorf("fact keys must be indexed alongside session text: keys=%d meta=%d", factRows, factMeta)
	}
}

// TestDecomposeSession_ByteExactAndBounded pins session decomposition: joined
// units reproduce the input bytes exactly (no data loss), every unit respects
// the cap, and short content stays a single unit.
func TestDecomposeSession_ByteExactAndBounded(t *testing.T) {
	long := strings.Repeat("The archive log entry was recorded quietly by the keeper. ", 80)
	if len(long) <= factUnitCap {
		t.Fatalf("test setup: long content must exceed the cap, got %d bytes", len(long))
	}
	units := decomposeSession(long, factUnitCap)
	if got := strings.Join(units, ""); got != long {
		t.Errorf("reassembled units must equal original bytes (%d vs %d)", len(got), len(long))
	}
	for i, u := range units {
		if len(u) > factUnitCap {
			t.Errorf("unit %d exceeds cap: %d > %d", i, len(u), factUnitCap)
		}
	}

	compact := strings.Repeat("x", 3*factUnitCap) // no enders, no whitespace: hard cut path
	compactUnits := decomposeSession(compact, factUnitCap)
	if got := strings.Join(compactUnits, ""); got != compact {
		t.Errorf("hard-cut units must equal original bytes (%d vs %d)", len(got), len(compact))
	}
	for i, u := range compactUnits {
		if len(u) > factUnitCap {
			t.Errorf("hard-cut unit %d exceeds cap: %d", i, len(u))
		}
	}

	short := "one short sentence"
	shortUnits := decomposeSession(short, factUnitCap)
	if len(shortUnits) != 1 || shortUnits[0] != short {
		t.Errorf("short content must stay one unit, got %q", shortUnits)
	}
}

// TestLongMemEval_LongSessionTailRecall pins that a fact stated only in the
// tail of a long session (>cap bytes) reaches the fact index and recalls,
// while the interrogative query still misses pure session-text keys.
func TestLongMemEval_LongSessionTailRecall(t *testing.T) {
	tail := "Mira finally adopted a greyhound named Comet at the shelter open house."
	long := strings.Repeat("The archive log entry was recorded quietly by the keeper. ", 60) + tail
	if len(long) <= factUnitCap {
		t.Fatalf("test setup: long content must exceed the cap, got %d bytes", len(long))
	}

	keys := extractFactKeys(long)
	if len(keys) == 0 || !strings.Contains(keys[len(keys)-1], "Comet") {
		t.Fatalf("tail fact sentence must survive decomposition, last key=%q", keys[len(keys)-1])
	}

	db := setupTestDB(t)
	insertTestObservation(t, db, 102, "Adoption news", long, "manual", "lme-project", "project")
	store := NewStore(db)
	ctx := context.Background()
	opts := domain.SearchOptions{Project: "lme-project"}
	query := "Who adopted the greyhound named Comet?"

	keywordHits, err := store.searchKeywords(ctx, query, opts, 10)
	if err != nil {
		t.Fatalf("keyword precondition search: %v", err)
	}
	if len(keywordHits) != 0 {
		t.Fatalf("precondition: session-text keys must miss the query, got %d hits", len(keywordHits))
	}

	results, err := store.Search(ctx, query, opts)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	found := false
	for _, r := range results {
		if r.ID == 102 {
			found = true
		}
	}
	if !found {
		t.Fatalf("tail fact must recall long session 102 for %q, got %v", query, idsOf(results))
	}
}

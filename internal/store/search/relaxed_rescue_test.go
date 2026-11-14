package search

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// TestRelaxedRescue_NaturalLanguageQuery pins that a natural-language query whose
// strict AND-joined FTS5 query matches nothing (stopwords like "how", "do", "we")
// is rescued by the relaxed stopword-pruned tiers and returns the observation.
func TestRelaxedRescue_NaturalLanguageQuery(t *testing.T) {
	db := setupTestDB(t)
	insertTestObservation(t, db, 1, "pgbouncer pooling gotcha",
		"postgres connection pooling via pgbouncer breaks prepared statements.", "gotcha", "p", "project")

	store := NewStore(db)
	query := "how do we handle postgres connection pooling"
	results, err := store.Search(context.Background(), query, domain.SearchOptions{Project: "p", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	found := false
	for _, r := range results {
		if r.ID == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected observation 1 to be rescued for %q, got %d results", query, len(results))
	}
}

// TestRelaxedRescue_Disabled pins that SetRelaxedRescue(false) turns the rescue
// off and that re-enabling restores it for the same data.
func TestRelaxedRescue_Disabled(t *testing.T) {
	db := setupTestDB(t)
	insertTestObservation(t, db, 1, "pgbouncer pooling gotcha",
		"postgres connection pooling via pgbouncer breaks prepared statements.", "gotcha", "p", "project")

	store := NewStore(db)
	ctx := context.Background()
	query := "how do we handle postgres connection pooling"
	opts := domain.SearchOptions{Project: "p", Limit: 10}

	store.SetRelaxedRescue(false)
	if got := store.rescueStarvedKeywords(ctx, query, opts, 30); len(got) != 0 {
		t.Fatalf("expected no rescue with the switch off, got %v", idsOf(got))
	}
	store.SetRelaxedRescue(true)
	if got := store.rescueStarvedKeywords(ctx, query, opts, 30); !containsID(got, 1) {
		t.Fatalf("expected rescue to return observation 1 once re-enabled, got %v", idsOf(got))
	}
}

// TestRelaxedRescue_OnlyWhenEveryListIsEmpty pins the no-second-vote contract:
// any populated list suppresses the rescue, so relaxed results can never add
// RRF credit to a pool another retriever already filled.
func TestRelaxedRescue_OnlyWhenEveryListIsEmpty(t *testing.T) {
	db := setupTestDB(t)
	insertTestObservation(t, db, 1, "pgbouncer pooling gotcha",
		"postgres connection pooling via pgbouncer breaks prepared statements.", "gotcha", "p", "project")

	store := NewStore(db)
	ctx := context.Background()
	query := "how do we handle postgres connection pooling"
	opts := domain.SearchOptions{Project: "p", Limit: 10}

	populated := []*domain.SearchResult{{}}
	if got := store.rescueStarvedKeywords(ctx, query, opts, 30, nil, populated); got != nil {
		t.Fatalf("rescue must stay out of a populated pool, got %v", idsOf(got))
	}
	if got := store.rescueStarvedKeywords(ctx, query, opts, 30, nil, nil); !containsID(got, 1) {
		t.Fatalf("rescue must fire when every list is empty, got %v", idsOf(got))
	}
	// A keyword-only query has nothing to relax.
	if got := store.rescueStarvedKeywords(ctx, "kubernetes", opts, 30); got != nil {
		t.Fatalf("keyword-only query must not be relaxed, got %v", idsOf(got))
	}
}

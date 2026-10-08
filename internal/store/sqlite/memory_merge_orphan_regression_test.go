package sqlite

// Regression coverage for issue #134 (merge-projects case-sensitive WHERE vs
// lowercased inputs) and issue #135 (doctor orphan percentage mixing a
// per-project numerator with a global denominator).

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// --- Issue #134: merge-projects silent zero-match failure -------------------

func TestStore_MergeProjects_MixedCaseStoredRowsAreMoved(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s-up", "EntryModel Scanner")
	createTestSession(t, db, "s-low", "entrymodel-scanner")

	for _, proj := range []string{"EntryModel Scanner", "NEW ENTRY MODEL WEBSOCKET", "EntryModel Scanner", "entrymodel-scanner"} {
		o := &domain.Observation{SessionID: "s-up", Title: "t", Content: "c", Project: proj}
		if err := store.Save(ctx, o); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	res, err := store.MergeProjects(ctx, []string{"EntryModel Scanner", "NEW ENTRY MODEL WEBSOCKET"}, "entrymodel-scanner")
	if err != nil {
		t.Fatalf("MergeProjects: %v", err)
	}
	if res.ObservationsUpdated != 3 {
		t.Errorf("ObservationsUpdated = %d, want 3 (mixed-case rows must move)", res.ObservationsUpdated)
	}
	if len(res.SourcesMerged) != 2 {
		t.Errorf("SourcesMerged = %+v, want 2 entries", res.SourcesMerged)
	}
	if len(res.SourcesNoMatch) != 0 {
		t.Errorf("SourcesNoMatch = %+v, want empty", res.SourcesNoMatch)
	}

	leftovers, err := store.List(ctx, domain.ObservationFilter{Project: "EntryModel Scanner", Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(leftovers) != 0 {
		t.Errorf("mixed-case project still holds %d observations after merge", len(leftovers))
	}
}

func TestStore_MergeProjects_LowercaseVariantOfCanonicalMovesStoredRows(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "EntryModel-Scanner")

	o := &domain.Observation{SessionID: "s1", Title: "t", Content: "c", Project: "EntryModel-Scanner"}
	if err := store.Save(ctx, o); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Source listed in a different case than the canonical AND than nothing
	// stored-equal must still move the mixed-case rows into canonical.
	res, err := store.MergeProjects(ctx, []string{"EntryModel-Scanner"}, "entrymodel-scanner")
	if err != nil {
		t.Fatalf("MergeProjects: %v", err)
	}
	if res.ObservationsUpdated != 1 {
		t.Errorf("ObservationsUpdated = %d, want 1", res.ObservationsUpdated)
	}
	if len(res.SourcesMerged) != 1 || res.SourcesMerged[0] != "EntryModel-Scanner" {
		t.Errorf("SourcesMerged = %+v, want [EntryModel-Scanner]", res.SourcesMerged)
	}
}

func TestStore_MergeProjects_ZeroMatchReportedNotMerged(t *testing.T) {
	store, _, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	res, err := store.MergeProjects(ctx, []string{"ghost-project"}, "canonical")
	if err != nil {
		t.Fatalf("MergeProjects: %v", err)
	}
	if len(res.SourcesMerged) != 0 {
		t.Errorf("SourcesMerged = %+v, want empty for zero-match source", res.SourcesMerged)
	}
	if len(res.SourcesNoMatch) != 1 || res.SourcesNoMatch[0] != "ghost-project" {
		t.Errorf("SourcesNoMatch = %+v, want [ghost-project]", res.SourcesNoMatch)
	}
	if res.ObservationsUpdated != 0 || res.SessionsUpdated != 0 {
		t.Errorf("updated counts = %d/%d, want 0/0", res.ObservationsUpdated, res.SessionsUpdated)
	}
}

// --- Issue #135: global orphan count ----------------------------------------

func TestStore_CountOrphanObservations_GlobalAcrossProjects(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj-a")
	createTestSession(t, db, "s2", "proj-b")

	inA1 := &domain.Observation{SessionID: "s1", Title: "a1", Content: "c", Project: "proj-a"}
	inA2 := &domain.Observation{SessionID: "s1", Title: "a2", Content: "c", Project: "proj-a"}
	inB := &domain.Observation{SessionID: "s2", Title: "b1", Content: "c", Project: "proj-b"}
	for _, o := range []*domain.Observation{inA1, inA2, inB} {
		if err := store.Save(ctx, o); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	// Only proj-a pair is connected; proj-b observation is an orphan too.
	insertEdge(t, db, inA1.ID, inA2.ID, "references")

	count, err := store.CountOrphanObservations(ctx)
	if err != nil {
		t.Fatalf("CountOrphanObservations: %v", err)
	}
	if count != 1 {
		t.Errorf("global orphan count = %d, want 1", count)
	}

	// The old per-project call scoped to proj-a would have said 0 orphans:
	// the global number must not depend on which project is most recent.
	if got, err := store.OrphanObservations(ctx, "proj-a", 100); err != nil || len(got) != 0 {
		t.Fatalf("per-project sanity: got=%d err=%v", len(got), err)
	}
}

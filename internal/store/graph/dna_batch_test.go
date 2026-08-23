package graph

// dna_batch_test.go covers the optional DNA batch edge-count capability:
// per-observation counts must be identical to the per-ID
// CountEdgesByObservation path (including self-loops counting once and
// observations with zero edges yielding a zero count), the empty input must
// not touch the database, and inputs larger than one chunk must still be
// correct across the chunk boundary.

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestDNABatchCountEdgesByObservationIDs(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	createTestSession(t, db, "s1", "test-project")
	a := createTestObservation(t, db, "A", "s1")
	b := createTestObservation(t, db, "B", "s1")
	c := createTestObservation(t, db, "C", "s1")
	d := createTestObservation(t, db, "D", "s1")

	ctx := context.Background()
	mustCreateEdge(t, store, ctx, &domain.Edge{FromObsID: a, ToObsID: b, RelationType: domain.RelationReferences, Weight: 1.0})
	mustCreateEdge(t, store, ctx, &domain.Edge{FromObsID: c, ToObsID: b, RelationType: domain.RelationReferences, Weight: 1.0})
	// Self-loop: the legacy per-ID count treats it as a single connection.
	mustCreateEdge(t, store, ctx, &domain.Edge{FromObsID: a, ToObsID: a, RelationType: domain.RelationReferences, Weight: 1.0})

	ids := []int64{a, b, c, d}
	batch, err := store.CountEdgesByObservationIDs(ctx, ids)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, id := range ids {
		legacy, err := store.CountEdgesByObservation(ctx, id)
		if err != nil {
			t.Fatalf("legacy count for %d: %v", id, err)
		}
		if batch[id] != legacy {
			t.Errorf("id %d: batch count %d != legacy count %d", id, batch[id], legacy)
		}
	}

	if batch[a] != 2 {
		t.Errorf("expected 2 edges for a (outgoing + self-loop), got %d", batch[a])
	}
	if batch[b] != 2 {
		t.Errorf("expected 2 edges for b (two incoming), got %d", batch[b])
	}
	if batch[c] != 1 {
		t.Errorf("expected 1 edge for c, got %d", batch[c])
	}
	if batch[d] != 0 {
		t.Errorf("expected 0 edges for orphan d, got %d", batch[d])
	}
}

func TestDNABatchCountEdgesEmpty(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	got, err := store.CountEdgesByObservationIDs(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map for empty input, got %d entries", len(got))
	}
}

func TestDNABatchCountEdgesChunkBoundary(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	const total = 510 // crosses the 500-ID chunk boundary
	createTestSession(t, db, "s1", "test-project")
	ids := make([]int64, 0, total)
	for i := 0; i < total; i++ {
		ids = append(ids, createTestObservation(t, db, "Orphan", "s1"))
	}

	ctx := context.Background()
	// One edge inside the final chunk so boundary chunks carry real data.
	mustCreateEdge(t, store, ctx, &domain.Edge{FromObsID: ids[total-4], ToObsID: ids[total-3], RelationType: domain.RelationReferences, Weight: 1.0})

	batch, err := store.CountEdgesByObservationIDs(ctx, ids)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(batch) != 2 {
		t.Fatalf("expected exactly 2 non-zero counts, got %d entries", len(batch))
	}
	for _, id := range ids {
		legacy, err := store.CountEdgesByObservation(ctx, id)
		if err != nil {
			t.Fatalf("legacy count for %d: %v", id, err)
		}
		if batch[id] != legacy {
			t.Errorf("id %d: batch count %d != legacy count %d", id, batch[id], legacy)
		}
	}
}

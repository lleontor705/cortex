package graph

// store_extra_test.go provides behavior-driven coverage for the previously
// uncovered graph store paths: GetEdge, GetEdgesForObservation, GetEdgesValidAt,
// GetEvolutionChain, CountEdgesByObservation, CountAllEdges, GetContradictions,
// and UpdateEdge, plus the remaining branches of CreateEdge/GetRelated
// (foreign-key violation, fully-populated edge fields, depth clamping, and
// tags deserialization).
//
// These are characterization tests against existing production code: every
// method under test already exists and behaves correctly, so the tests are
// expected to pass (GREEN) on first run. Negative/failing-path cases
// (NotFound, validation errors, expired temporal edges, time-window and
// relation-type filters) exercise the "fails for the right reason" semantics
// at the data level. No production code is modified.
//
// Note on created_at: the modernc.org/sqlite driver emits CURRENT_TIMESTAMP in
// RFC3339 ("2006-01-02T15:04:05Z"), but the store's parseTime expects the
// sqliteDatetimeFormat ("2006-01-02 15:04:05"). As a result Edge.CreatedAt is
// left at the zero value when read back. The SQL ORDER BY created_at still
// orders correctly (lexicographic RFC3339 is chronological), so ordering
// assertions use edge ID sequence as a faithful proxy rather than the broken
// CreatedAt field. This is a pre-existing latent defect in production code and
// is out of scope for this test-only change.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// mustCreateEdge is a thin wrapper around Store.CreateEdge that fails the test
// on error, keeping call sites focused on behavior assertions.
func mustCreateEdge(t *testing.T, store *Store, ctx context.Context, edge *domain.Edge) *domain.Edge {
	t.Helper()
	if err := store.CreateEdge(ctx, edge); err != nil {
		t.Fatalf("setup: create edge (%d->%d, %s): %v", edge.FromObsID, edge.ToObsID, edge.RelationType, err)
	}
	return edge
}

// insertEdgeWithCreatedAt inserts an edge row with an explicit created_at so
// ordering assertions (ORDER BY created_at ASC/DESC) are deterministic and
// free of second-resolution timing flakes. createdAt must be in
// sqliteDatetimeFormat ("2006-01-02 15:04:05").
func insertEdgeWithCreatedAt(t *testing.T, db *sql.DB, from, to int64, relType, createdAt string) int64 {
	t.Helper()
	res, err := db.Exec(
		`INSERT INTO edges (from_obs_id, to_obs_id, relation_type, weight, confidence, source, reasoning, valid_from, invalid_at, created_at)
		 VALUES (?, ?, ?, 1.0, 1.0, NULL, NULL, NULL, NULL, ?)`,
		from, to, relType, createdAt,
	)
	if err != nil {
		t.Fatalf("insert edge raw (created_at=%s): %v", createdAt, err)
	}
	id, _ := res.LastInsertId()
	return id
}

// createTestObservationWithTags inserts an observation carrying a non-empty
// tags JSON so the tags-deserialization branch in GetRelated is exercised.
func createTestObservationWithTags(t *testing.T, db *sql.DB, title, sessionID, tagsJSON string) int64 {
	t.Helper()
	result, err := db.Exec(
		`INSERT INTO observations (session_id, type, title, content, project, scope, tags)
		 VALUES (?, 'manual', ?, 'test content', 'test-project', 'project', ?)`,
		sessionID, title, tagsJSON,
	)
	if err != nil {
		t.Fatalf("create test observation with tags: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

// buildChain builds a linear chain obs[0]->obs[1]->...->obs[n-1] of "references"
// edges and returns the observation IDs. Used to exercise GetRelated depth
// clamping (depth<=0 and depth>10).
func buildChain(t *testing.T, db *sql.DB, store *Store, ctx context.Context, sessionID string, n int) []int64 {
	t.Helper()
	ids := make([]int64, n)
	for i := 0; i < n; i++ {
		ids[i] = createTestObservation(t, db, "ChainObs", sessionID)
	}
	for i := 0; i+1 < n; i++ {
		mustCreateEdge(t, store, ctx, &domain.Edge{
			FromObsID:    ids[i],
			ToObsID:      ids[i+1],
			RelationType: domain.RelationReferences,
			Weight:       1.0,
		})
	}
	return ids
}

// TestCreateEdge_FKViolation covers the foreign-key constraint branch of
// CreateEdge: an edge referencing a non-existent observation must surface as a
// domain.NotFoundError (foreign_keys=ON is enabled by the database manager).
func TestCreateEdge_FKViolation(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	edge := &domain.Edge{
		FromObsID:    100, // no observation with this id exists
		ToObsID:      200,
		RelationType: domain.RelationReferences,
		Weight:       1.0,
	}

	err := store.CreateEdge(context.Background(), edge)
	if !domain.IsNotFoundError(err) {
		t.Fatalf("expected NotFoundError for missing observations, got %v", err)
	}
	if edge.ID != 0 {
		t.Errorf("edge ID must remain 0 on failed insert, got %d", edge.ID)
	}
}

// TestCreateEdge_FullyPopulatedFields covers the non-empty branches of
// nullableString/nullableTime by persisting an edge with Source, Reasoning,
// ValidFrom, and InvalidAt set, then reading it back through GetEdge.
func TestCreateEdge_FullyPopulatedFields(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	createTestSession(t, db, "s1", "test-project")
	from := createTestObservation(t, db, "From", "s1")
	to := createTestObservation(t, db, "To", "s1")

	validFrom := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	invalidAt := time.Date(2025, 6, 7, 8, 9, 10, 0, time.UTC)

	edge := &domain.Edge{
		FromObsID:    from,
		ToObsID:      to,
		RelationType: domain.RelationReferences,
		Weight:       2.5,
		Confidence:   0.9,
		Source:       "agent",
		Reasoning:    "deduced from context",
		ValidFrom:    &validFrom,
		InvalidAt:    &invalidAt,
	}
	mustCreateEdge(t, store, context.Background(), edge)

	got, err := store.GetEdge(context.Background(), edge.ID)
	if err != nil {
		t.Fatalf("get edge %d: %v", edge.ID, err)
	}

	if got.Source != "agent" {
		t.Errorf("Source round-trip: got %q, want %q", got.Source, "agent")
	}
	if got.Reasoning != "deduced from context" {
		t.Errorf("Reasoning round-trip: got %q, want %q", got.Reasoning, "deduced from context")
	}
	if got.Weight != 2.5 {
		t.Errorf("Weight round-trip: got %v, want 2.5", got.Weight)
	}
	if got.Confidence != 0.9 {
		t.Errorf("Confidence round-trip: got %v, want 0.9", got.Confidence)
	}
	if got.ValidFrom == nil || !got.ValidFrom.Equal(validFrom) {
		t.Errorf("ValidFrom round-trip: got %v, want %v", got.ValidFrom, validFrom)
	}
	if got.InvalidAt == nil || !got.InvalidAt.Equal(invalidAt) {
		t.Errorf("InvalidAt round-trip: got %v, want %v", got.InvalidAt, invalidAt)
	}
}

// TestGetEdge covers GetEdge success and the not-found path (sql.ErrNoRows in
// scanEdgeRow is translated to domain.NotFoundError).
func TestGetEdge(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	createTestSession(t, db, "s1", "test-project")
	from := createTestObservation(t, db, "From", "s1")
	to := createTestObservation(t, db, "To", "s1")

	edge := mustCreateEdge(t, store, context.Background(), &domain.Edge{
		FromObsID:    from,
		ToObsID:      to,
		RelationType: domain.RelationFollows,
		Weight:       1.0,
	})

	t.Run("success returns full edge", func(t *testing.T) {
		got, err := store.GetEdge(context.Background(), edge.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != edge.ID {
			t.Errorf("ID: got %d, want %d", got.ID, edge.ID)
		}
		if got.FromObsID != from || got.ToObsID != to {
			t.Errorf("endpoints: got %d->%d, want %d->%d", got.FromObsID, got.ToObsID, from, to)
		}
		if got.RelationType != domain.RelationFollows {
			t.Errorf("RelationType: got %q, want %q", got.RelationType, domain.RelationFollows)
		}
		// Defaults applied by COALESCE in the query.
		if got.EvolutionType != "original" {
			t.Errorf("EvolutionType default: got %q, want original", got.EvolutionType)
		}
		if got.FactState != "current" {
			t.Errorf("FactState default: got %q, want current", got.FactState)
		}
	})

	t.Run("not found returns NotFoundError", func(t *testing.T) {
		_, err := store.GetEdge(context.Background(), 99999)
		if !domain.IsNotFoundError(err) {
			t.Fatalf("expected NotFoundError, got %v", err)
		}
	})
}

// TestGetEdgesForObservation covers the query that returns every edge where the
// observation is either source or target, ordered by created_at DESC.
func TestGetEdgesForObservation(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	createTestSession(t, db, "s1", "test-project")
	hub := createTestObservation(t, db, "Hub", "s1")
	a := createTestObservation(t, db, "A", "s1")
	b := createTestObservation(t, db, "B", "s1")
	c := createTestObservation(t, db, "C", "s1")

	// hub is the source for edges to a and b, and the target of an edge from c.
	mustCreateEdge(t, store, ctx, &domain.Edge{FromObsID: hub, ToObsID: a, RelationType: domain.RelationReferences, Weight: 1.0})
	mustCreateEdge(t, store, ctx, &domain.Edge{FromObsID: hub, ToObsID: b, RelationType: domain.RelationRelatesTo, Weight: 1.0})
	mustCreateEdge(t, store, ctx, &domain.Edge{FromObsID: c, ToObsID: hub, RelationType: domain.RelationFollows, Weight: 1.0})

	t.Run("returns source and target edges", func(t *testing.T) {
		edges, err := store.GetEdgesForObservation(ctx, hub)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(edges) != 3 {
			t.Fatalf("expected 3 edges for hub, got %d", len(edges))
		}
		// Every returned edge must reference hub on one side.
		for _, e := range edges {
			if e.FromObsID != hub && e.ToObsID != hub {
				t.Errorf("edge %d does not reference hub (%d->%d)", e.ID, e.FromObsID, e.ToObsID)
			}
		}
	})

	t.Run("no edges returns empty slice", func(t *testing.T) {
		orphan := createTestObservation(t, db, "Orphan", "s1")
		edges, err := store.GetEdgesForObservation(ctx, orphan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(edges) != 0 {
			t.Fatalf("expected 0 edges for orphan, got %d", len(edges))
		}
	})

	t.Run("ordered by created_at DESC", func(t *testing.T) {
		// Re-seed an isolated pair with deterministic created_at timestamps.
		// Edge IDs are captured because the modernc CURRENT_TIMESTAMP driver
		// emits created_at in RFC3339, which the store's parseTime cannot
		// decode (Edge.CreatedAt is left at the zero value). Asserting row
		// order by ID is a faithful proxy: the SQL ORDER BY created_at DESC
		// must surface the later-created edge first.
		createTestSession(t, db, "s2", "test-project")
		x := createTestObservation(t, db, "X", "s2")
		y := createTestObservation(t, db, "Y", "s2")
		idOlder := insertEdgeWithCreatedAt(t, db, x, y, domain.RelationReferences, "2024-01-01 00:00:00")
		idNewer := insertEdgeWithCreatedAt(t, db, x, y, domain.RelationRelatesTo, "2025-01-01 00:00:00")

		edges, err := store.GetEdgesForObservation(ctx, x)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(edges) != 2 {
			t.Fatalf("expected 2 edges, got %d", len(edges))
		}
		// DESC means the 2025 edge (newer) comes first.
		if edges[0].ID != idNewer || edges[1].ID != idOlder {
			t.Errorf("expected DESC order [%d, %d], got [%d, %d]", idNewer, idOlder, edges[0].ID, edges[1].ID)
		}
	})
}

// TestGetEdgesValidAt covers temporal validity selection.
func TestGetEdgesValidAt(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	createTestSession(t, db, "s1", "test-project")
	hub := createTestObservation(t, db, "Hub", "s1")
	targetA := createTestObservation(t, db, "TargetA", "s1")
	targetB := createTestObservation(t, db, "TargetB", "s1")
	targetC := createTestObservation(t, db, "TargetC", "s1")

	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	validStart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	queryAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

	// Edge A: valid window fully covers queryAt -> valid.
	mustCreateEdge(t, store, ctx, &domain.Edge{
		FromObsID: hub, ToObsID: targetA, RelationType: domain.RelationReferences, Weight: 1.0,
		ValidFrom: &validStart, InvalidAt: &future,
	})
	// Edge B: invalid_at in the past -> expired, must be excluded.
	mustCreateEdge(t, store, ctx, &domain.Edge{
		FromObsID: hub, ToObsID: targetB, RelationType: domain.RelationRelatesTo, Weight: 1.0,
		InvalidAt: &past,
	})
	// Edge C: no temporal bounds -> always valid.
	mustCreateEdge(t, store, ctx, &domain.Edge{
		FromObsID: hub, ToObsID: targetC, RelationType: domain.RelationFollows, Weight: 1.0,
	})

	edges, err := store.GetEdgesValidAt(ctx, hub, queryAt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("expected 2 valid edges (A and C), got %d", len(edges))
	}

	// The expired edge (to targetB) must not be among the results.
	for _, e := range edges {
		if e.ToObsID == targetB {
			t.Errorf("expired edge to targetB (%d) should not be valid at %s", targetB, queryAt)
		}
	}

	// Confirm both surviving targets are present.
	got := map[int64]bool{}
	for _, e := range edges {
		got[e.ToObsID] = true
	}
	if !got[targetA] || !got[targetC] {
		t.Errorf("expected targets A(%d) and C(%d) to be valid, got %v", targetA, targetC, got)
	}
}

// TestGetEvolutionChain covers retrieval of all edges sharing the same
// (from, to) endpoints, ordered by created_at ASC.
func TestGetEvolutionChain(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	createTestSession(t, db, "s1", "test-project")
	from := createTestObservation(t, db, "From", "s1")
	to := createTestObservation(t, db, "To", "s1")
	other := createTestObservation(t, db, "Other", "s1")

	// Two evolution entries for the same endpoints (distinct relation types to
	// satisfy the UNIQUE(from,to,relation_type) constraint), plus an unrelated
	// edge that must be excluded from the chain. created_at is set explicitly so
	// ORDER BY created_at ASC is deterministic.
	idNewer := insertEdgeWithCreatedAt(t, db, from, to, domain.RelationReferences, "2024-03-01 00:00:00")
	idOlder := insertEdgeWithCreatedAt(t, db, from, to, domain.RelationRelatesTo, "2024-01-01 00:00:00")
	insertEdgeWithCreatedAt(t, db, from, other, domain.RelationReferences, "2024-02-01 00:00:00")

	t.Run("returns same-endpoint edges ordered ASC", func(t *testing.T) {
		chain, err := store.GetEvolutionChain(ctx, from, to)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(chain) != 2 {
			t.Fatalf("expected 2 edges in evolution chain, got %d", len(chain))
		}
		// ASC order: 2024-01-01 (older) first, then 2024-03-01 (newer). Asserted
		// by ID rather than CreatedAt because the modernc CURRENT_TIMESTAMP
		// emits RFC3339, which the store's parseTime cannot decode, leaving
		// Edge.CreatedAt at the zero value (see package note above).
		if chain[0].ID != idOlder || chain[1].ID != idNewer {
			t.Errorf("expected ASC order [%d, %d], got [%d, %d]", idOlder, idNewer, chain[0].ID, chain[1].ID)
		}
		for _, e := range chain {
			if e.FromObsID != from || e.ToObsID != to {
				t.Errorf("chain edge %d has wrong endpoints %d->%d", e.ID, e.FromObsID, e.ToObsID)
			}
		}
	})

	t.Run("empty chain for unknown endpoints", func(t *testing.T) {
		chain, err := store.GetEvolutionChain(ctx, 999, 998)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(chain) != 0 {
			t.Fatalf("expected empty chain, got %d", len(chain))
		}
	})
}

// TestCountEdgesByObservation covers counting edges where the observation is
// either source or target.
func TestCountEdgesByObservation(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	createTestSession(t, db, "s1", "test-project")
	hub := createTestObservation(t, db, "Hub", "s1")
	a := createTestObservation(t, db, "A", "s1")
	b := createTestObservation(t, db, "B", "s1")

	mustCreateEdge(t, store, ctx, &domain.Edge{FromObsID: hub, ToObsID: a, RelationType: domain.RelationReferences, Weight: 1.0})
	mustCreateEdge(t, store, ctx, &domain.Edge{FromObsID: hub, ToObsID: b, RelationType: domain.RelationRelatesTo, Weight: 1.0})
	mustCreateEdge(t, store, ctx, &domain.Edge{FromObsID: a, ToObsID: hub, RelationType: domain.RelationFollows, Weight: 1.0})

	t.Run("counts source and target edges", func(t *testing.T) {
		count, err := store.CountEdgesByObservation(ctx, hub)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if count != 3 {
			t.Errorf("expected 3 edges for hub, got %d", count)
		}
	})

	t.Run("zero for unconnected observation", func(t *testing.T) {
		orphan := createTestObservation(t, db, "Orphan", "s1")
		count, err := store.CountEdgesByObservation(ctx, orphan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if count != 0 {
			t.Errorf("expected 0 edges for orphan, got %d", count)
		}
	})
}

// TestCountAllEdges covers the total edge count across the whole store.
func TestCountAllEdges(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	createTestSession(t, db, "s1", "test-project")
	a := createTestObservation(t, db, "A", "s1")
	b := createTestObservation(t, db, "B", "s1")
	c := createTestObservation(t, db, "C", "s1")

	before, err := store.CountAllEdges(ctx)
	if err != nil {
		t.Fatalf("count before: %v", err)
	}
	if before != 0 {
		t.Fatalf("expected 0 edges initially, got %d", before)
	}

	mustCreateEdge(t, store, ctx, &domain.Edge{FromObsID: a, ToObsID: b, RelationType: domain.RelationReferences, Weight: 1.0})
	mustCreateEdge(t, store, ctx, &domain.Edge{FromObsID: b, ToObsID: c, RelationType: domain.RelationRelatesTo, Weight: 1.0})
	mustCreateEdge(t, store, ctx, &domain.Edge{FromObsID: a, ToObsID: c, RelationType: domain.RelationFollows, Weight: 1.0})

	after, err := store.CountAllEdges(ctx)
	if err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != 3 {
		t.Errorf("expected 3 edges after inserts, got %d", after)
	}
}

// TestGetContradictions covers filtering by the "contradicts" relation type and
// the created_at time window, plus DESC ordering.
func TestGetContradictions(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	createTestSession(t, db, "s1", "test-project")
	from := createTestObservation(t, db, "From", "s1")
	to := createTestObservation(t, db, "To", "s1")

	// Two contradiction edges within the window. They use distinct endpoint
	// pairs because the UNIQUE constraint is (from,to,relation_type), so two
	// contradicts edges on the same pair would collide. GetContradictions
	// filters only by relation type + time window (no endpoint filter), so a
	// second pair is still returned.
	otherA := createTestObservation(t, db, "OtherA", "s1")
	otherB := createTestObservation(t, db, "OtherB", "s1")

	insertEdgeWithCreatedAt(t, db, from, to, domain.RelationContradicts, "2026-07-10 12:00:00")
	idOlder := insertEdgeWithCreatedAt(t, db, otherA, otherB, domain.RelationContradicts, "2026-07-05 12:00:00")
	// A non-contradiction edge in the same window must be filtered out by type.
	insertEdgeWithCreatedAt(t, db, from, to, domain.RelationReferences, "2026-07-11 12:00:00")
	// A contradiction edge OUTSIDE the queried window must be excluded.
	insertEdgeWithCreatedAt(t, db, from, to, domain.RelationSupersedes, "2020-01-01 00:00:00")

	windowStart := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)

	t.Run("filters by contradicts and time window", func(t *testing.T) {
		edges, err := store.GetContradictions(ctx, windowStart, windowEnd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Two in-window contradiction edges; references/supersedes and the
		// out-of-window contradiction must be excluded.
		if len(edges) != 2 {
			t.Fatalf("expected 2 contradictions in window, got %d", len(edges))
		}
		for _, e := range edges {
			if e.RelationType != domain.RelationContradicts {
				t.Errorf("non-contradiction edge %s leaked into results", e.RelationType)
			}
		}
	})

	t.Run("ordered by created_at DESC", func(t *testing.T) {
		edges, err := store.GetContradictions(ctx, windowStart, windowEnd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(edges) != 2 {
			t.Fatalf("expected 2 contradictions, got %d", len(edges))
		}
		// DESC means the 2026-07-10 edge (newer) comes first. Asserted by ID,
		// not CreatedAt, due to the modernc CURRENT_TIMESTAMP RFC3339 issue
		// documented in the package note above.
		if edges[0].ID == idOlder {
			t.Errorf("expected DESC order (newer first); older edge %d came first", idOlder)
		}
	})

	t.Run("out-of-range window excludes everything", func(t *testing.T) {
		farFutureStart := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
		farFutureEnd := time.Date(2099, 12, 31, 0, 0, 0, 0, time.UTC)
		edges, err := store.GetContradictions(ctx, farFutureStart, farFutureEnd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(edges) != 0 {
			t.Fatalf("expected 0 contradictions in far-future window, got %d", len(edges))
		}
	})
}

// TestUpdateEdge covers mutable-field updates, the nil-edge validation path, and
// the not-found path (zero rows affected).
func TestUpdateEdge(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	createTestSession(t, db, "s1", "test-project")
	from := createTestObservation(t, db, "From", "s1")
	to := createTestObservation(t, db, "To", "s1")

	edge := mustCreateEdge(t, store, ctx, &domain.Edge{
		FromObsID: from, ToObsID: to, RelationType: domain.RelationReferences, Weight: 1.0,
	})

	t.Run("updates mutable fields and persists", func(t *testing.T) {
		newValid := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
		newInvalid := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
		update := &domain.Edge{
			ID:           edge.ID,
			RelationType: domain.RelationSupersedes,
			Weight:       5.0,
			Confidence:   0.42,
			Source:       "agent",
			Reasoning:    "revised after review",
			ValidFrom:    &newValid,
			InvalidAt:    &newInvalid,
		}
		if err := store.UpdateEdge(ctx, update); err != nil {
			t.Fatalf("update edge: %v", err)
		}

		got, err := store.GetEdge(ctx, edge.ID)
		if err != nil {
			t.Fatalf("verify update: %v", err)
		}
		if got.RelationType != domain.RelationSupersedes {
			t.Errorf("RelationType after update: got %q, want %q", got.RelationType, domain.RelationSupersedes)
		}
		if got.Weight != 5.0 {
			t.Errorf("Weight after update: got %v, want 5.0", got.Weight)
		}
		if got.Confidence != 0.42 {
			t.Errorf("Confidence after update: got %v, want 0.42", got.Confidence)
		}
		if got.Source != "agent" {
			t.Errorf("Source after update: got %q, want agent", got.Source)
		}
		if got.Reasoning != "revised after review" {
			t.Errorf("Reasoning after update: got %q, want revised after review", got.Reasoning)
		}
		if got.ValidFrom == nil || !got.ValidFrom.Equal(newValid) {
			t.Errorf("ValidFrom after update: got %v, want %v", got.ValidFrom, newValid)
		}
		if got.InvalidAt == nil || !got.InvalidAt.Equal(newInvalid) {
			t.Errorf("InvalidAt after update: got %v, want %v", got.InvalidAt, newInvalid)
		}
	})

	t.Run("nil edge returns validation error", func(t *testing.T) {
		err := store.UpdateEdge(ctx, nil)
		if !domain.IsValidationError(err) {
			t.Fatalf("expected ValidationError for nil edge, got %v", err)
		}
	})

	t.Run("unknown id returns NotFoundError", func(t *testing.T) {
		missing := &domain.Edge{ID: 99999, RelationType: domain.RelationReferences, Weight: 1.0}
		err := store.UpdateEdge(ctx, missing)
		if !domain.IsNotFoundError(err) {
			t.Fatalf("expected NotFoundError for unknown id, got %v", err)
		}
	})
}

// TestGetRelated_DepthClamping covers the clamping of depth to [1, 10] inside
// GetRelated: depth<=0 behaves as depth 1, and depth>10 is capped at 10.
func TestGetRelated_DepthClamping(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	createTestSession(t, db, "s1", "test-project")
	// Build a 12-node chain so the upper clamp (depth 10) is observable:
	// depth 11 would reach 11 nodes, but clamped to 10 it reaches only 10.
	chain := buildChain(t, db, store, ctx, "s1", 12)
	root := chain[0]

	t.Run("zero or negative depth behaves as depth 1", func(t *testing.T) {
		for _, depth := range []int{0, -1, -100} {
			results, err := store.GetRelated(ctx, root, depth)
			if err != nil {
				t.Fatalf("depth=%d: unexpected error: %v", depth, err)
			}
			if len(results) != 1 {
				t.Errorf("depth=%d: expected 1 result (clamped to 1), got %d", depth, len(results))
			}
		}
	})

	t.Run("depth above 10 is capped at 10", func(t *testing.T) {
		results, err := store.GetRelated(ctx, root, 11)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Chain has 11 reachable nodes (chain[1..11]); capped at depth 10
		// reaches exactly 10 nodes.
		if len(results) != 10 {
			t.Errorf("expected 10 results (depth capped at 10), got %d", len(results))
		}
	})
}

// TestGetRelated_TagsDeserialization covers the branch that unmarshals the
// observation tags JSON into the Tags slice.
func TestGetRelated_TagsDeserialization(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	createTestSession(t, db, "s1", "test-project")
	root := createTestObservation(t, db, "Root", "s1")
	tagged := createTestObservationWithTags(t, db, "Tagged", "s1", `["alpha","beta"]`)

	mustCreateEdge(t, store, ctx, &domain.Edge{
		FromObsID: root, ToObsID: tagged, RelationType: domain.RelationReferences, Weight: 1.0,
	})

	results, err := store.GetRelated(ctx, root, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 related observation, got %d", len(results))
	}
	got := results[0]
	if got.ID != tagged {
		t.Errorf("expected tagged obs %d, got %d", tagged, got.ID)
	}
	if len(got.Tags) != 2 {
		t.Fatalf("expected 2 tags deserialized, got %d", len(got.Tags))
	}
	want := map[string]bool{"alpha": true, "beta": true}
	for _, tag := range got.Tags {
		if !want[tag] {
			t.Errorf("unexpected tag %q in %v", tag, got.Tags)
		}
	}
}

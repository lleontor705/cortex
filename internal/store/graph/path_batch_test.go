package graph

// path_batch_test.go covers the GRAPH-01/GRAPH-02 store-level seams for the
// level-neighbor batch capability: one SQL statement per expanded BFS level
// on the joined-star fixture, hydrated adjacency that deduplicates duplicate
// and reversed edges, excludes closed v2 facts and soft-deleted neighbors,
// deterministic ordering under shuffled insertion order, and legacy/v2 parity
// through the domain service.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"math/rand"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	graphdomain "github.com/lleontor705/cortex/internal/domain/graph"
	"github.com/lleontor705/cortex/internal/migration"
	"modernc.org/sqlite"
)

// --- counting driver --------------------------------------------------------

var countDriverSeq atomic.Int64

// countingDriver wraps the modernc SQLite driver and counts every SQL
// statement executed through QueryContext/ExecContext, giving tests a real
// SQL-statement oracle for the "one lookup per level" budget.
type countingDriver struct {
	inner driver.Driver
	count *atomic.Int64
}

func (d *countingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: conn, count: d.count}, nil
}

type countingConn struct {
	driver.Conn
	count *atomic.Int64
}

func (c *countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.count.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *countingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.count.Add(1)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

// openCountingV2DB opens an in-memory v2-baseline SQLite database whose SQL
// statements are counted. A single pooled connection keeps :memory: coherent.
func openCountingV2DB(t *testing.T) (*sql.DB, *atomic.Int64) {
	t.Helper()
	count := &atomic.Int64{}
	name := fmt.Sprintf("sqlitecount-%d", countDriverSeq.Add(1))
	sql.Register(name, &countingDriver{inner: &sqlite.Driver{}, count: count})
	db, err := sql.Open(name, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	b, err := migration.NewV2Baseline()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Apply(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db, count
}

// --- fixtures ----------------------------------------------------------------

// seedJoinedStar builds two 300-leaf stars joined at hubs through the
// smallest leaf: hub A (first row) -- 300 leaves -- hub B -- 300 leaves.
// IDs are deterministic because rows are inserted sequentially: A=1,
// leaves=2..301, B=302, M-leaves=303..602. Returns (hubA, joinLeaf, hubB,
// smallestM). Edge insertion order is shuffled when rng != nil to prove
// determinism. Accepts testing.TB so retained benchmarks share the exact
// functional fixture.
func seedJoinedStar(t testing.TB, store *Store, db *sql.DB, v2 bool, rng *rand.Rand) (int64, int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO sessions(id,project,directory) VALUES ('s','p','/d')`); err != nil {
		t.Fatal(err)
	}
	newObs := func() int64 {
		res, err := db.Exec(`INSERT INTO observations(session_id,type,title,content,project) VALUES ('s','manual','o','c','p')`)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	hubA := newObs()
	leaves := make([]int64, 0, 300)
	for i := 0; i < 300; i++ {
		leaves = append(leaves, newObs())
	}
	hubB := newObs()
	ms := make([]int64, 0, 300)
	for i := 0; i < 300; i++ {
		ms = append(ms, newObs())
	}
	if hubA != 1 || leaves[0] != 2 || hubB != 302 || ms[0] != 303 {
		t.Fatalf("fixture IDs drifted: hubA=%d leaf0=%d hubB=%d m0=%d", hubA, leaves[0], hubB, ms[0])
	}

	edge := func(from, to int64, rel string) {
		t.Helper()
		if err := store.CreateEdge(ctx, &domain.Edge{FromObsID: from, ToObsID: to, RelationType: rel, Weight: 1}); err != nil {
			t.Fatalf("edge %d-%d: %v", from, to, err)
		}
	}
	type pair struct{ from, to int64 }
	var pairs []pair
	for _, l := range leaves {
		pairs = append(pairs, pair{hubA, l})
	}
	pairs = append(pairs, pair{leaves[0], hubB})
	for _, m := range ms {
		pairs = append(pairs, pair{hubB, m})
	}
	if rng != nil {
		rng.Shuffle(len(pairs), func(i, j int) { pairs[i], pairs[j] = pairs[j], pairs[i] })
	}
	for _, p := range pairs {
		edge(p.from, p.to, domain.RelationReferences)
	}
	return hubA, leaves[0], hubB, ms[0]
}

// noBatchStore hides the batch capability so the domain service exercises the
// bounded per-node fallback against the same store. It delegates exactly the
// domain.GraphRepository surface.
type noBatchStore struct{ inner *Store }

func (n noBatchStore) CreateEdge(ctx context.Context, edge *domain.Edge) error {
	return n.inner.CreateEdge(ctx, edge)
}
func (n noBatchStore) GetRelated(ctx context.Context, obsID int64, depth int) ([]*domain.Observation, error) {
	return n.inner.GetRelated(ctx, obsID, depth)
}
func (n noBatchStore) DeleteEdge(ctx context.Context, id int64) error {
	return n.inner.DeleteEdge(ctx, id)
}
func (n noBatchStore) GetEdgesForObservation(ctx context.Context, obsID int64) ([]*domain.Edge, error) {
	return n.inner.GetEdgesForObservation(ctx, obsID)
}
func (n noBatchStore) GetEdge(ctx context.Context, id int64) (*domain.Edge, error) {
	return n.inner.GetEdge(ctx, id)
}
func (n noBatchStore) GetEvolutionChain(ctx context.Context, fromObsID, toObsID int64) ([]*domain.Edge, error) {
	return n.inner.GetEvolutionChain(ctx, fromObsID, toObsID)
}
func (n noBatchStore) CountEdgesByObservation(ctx context.Context, obsID int64) (int, error) {
	return n.inner.CountEdgesByObservation(ctx, obsID)
}
func (n noBatchStore) CountAllEdges(ctx context.Context) (int, error) {
	return n.inner.CountAllEdges(ctx)
}
func (n noBatchStore) GetContradictions(ctx context.Context, from, to time.Time) ([]*domain.Edge, error) {
	return n.inner.GetContradictions(ctx, from, to)
}
func (n noBatchStore) UpdateEdge(ctx context.Context, edge *domain.Edge) error {
	return n.inner.UpdateEdge(ctx, edge)
}

// --- GRAPH-01: joined star, <=3 SQL statements, speed, determinism ----------

func TestV2JoinedStarPathBatchThreeSQLStatements(t *testing.T) {
	db, count := openCountingV2DB(t)
	store := NewStore(db)
	hubA, joinLeaf, hubB, target := seedJoinedStar(t, store, db, true, nil)
	ctx := context.Background()

	// Best-of-N wall-clock probe: transiently loaded CI runners (full-suite
	// parallel packages) must not flake the <=25ms budget, while a genuine
	// regression that is consistently slow still fails. Every attempt must
	// still issue exactly 3 statements and return the deterministic path,
	// so the functional assertions stay hard and non-vacuous.
	const attempts = 5
	var batchedDur time.Duration
	var path []int64
	for i := 0; i < attempts; i++ {
		count.Store(0)
		start := time.Now()
		p, err := graphdomain.NewService(store).FindPathBounded(ctx, hubA, target, 5, 0)
		d := time.Since(start)
		if err != nil {
			t.Fatalf("FindPathBounded: %v", err)
		}
		if want := []int64{hubA, joinLeaf, hubB, target}; !reflect.DeepEqual(p, want) {
			t.Fatalf("path = %v, want %v", p, want)
		}
		if got := count.Load(); got != 3 {
			t.Fatalf("SQL statements for 3-hop path = %d, want exactly 3", got)
		}
		path = p
		if i == 0 || d < batchedDur {
			batchedDur = d
		}
		if batchedDur <= 25*time.Millisecond {
			break
		}
	}
	if batchedDur > 25*time.Millisecond {
		t.Fatalf("batched FindPathBounded took %s (best of %d), want <= 25ms", batchedDur, attempts)
	}

	// The bounded per-node fallback on the same store must stay correct and
	// measurably slower (legacy behavior issued 302 per-node lookups).
	count.Store(0)
	fbStart := time.Now()
	fallbackPath, err := graphdomain.NewService(noBatchStore{store}).FindPathBounded(ctx, hubA, target, 5, 0)
	fallbackDur := time.Since(fbStart)
	if err != nil {
		t.Fatalf("fallback FindPathBounded: %v", err)
	}
	if !reflect.DeepEqual(fallbackPath, path) {
		t.Fatalf("fallback path %v != batched path %v", fallbackPath, path)
	}
	if fallbackDur < 4*batchedDur {
		t.Fatalf("speedup not >= 4x: batched %s vs fallback %s", batchedDur, fallbackDur)
	}
	if got := count.Load(); got < 300 {
		t.Fatalf("fallback issued %d statements, expected the legacy ~302 per-node sweep", got)
	}
}

func TestV2GetRelatedBoundedUsesBatchCapability(t *testing.T) {
	db, count := openCountingV2DB(t)
	store := NewStore(db)
	hubA, _, hubB, _ := seedJoinedStar(t, store, db, true, nil)
	ctx := context.Background()

	count.Store(0)
	res, err := graphdomain.NewService(store).GetRelatedBounded(ctx, hubA, domain.GraphTraversalOptions{Depth: 3, MaxVisited: 100, MaxResults: 50})
	if err != nil {
		t.Fatalf("GetRelatedBounded: %v", err)
	}
	// 100 admitted nodes at default chunking: level 0 (1 node) plus level 1
	// (99 leaves) -> exactly two batched statements before the sentinel.
	if got := count.Load(); got > 2 {
		t.Fatalf("SQL statements = %d, want <= 2 for depth-3 traversal truncated at 100 visited", got)
	}
	if !res.Truncated {
		t.Fatal("expected truncation on the 600-node star with budget 100 visited / 50 results")
	}
	// 99 eligible level-1 rows exceed the 51-row results probe, so both
	// sentinels fire even though the visited budget is the tighter node bound.
	if want := []string{domain.TruncationReasonMaxVisited, domain.TruncationReasonMaxResults}; !reflect.DeepEqual(res.TruncationReasons, want) {
		t.Fatalf("reasons = %v, want %v", res.TruncationReasons, want)
	}
	// Rows must be hop-then-ID ordered ascending.
	for i := 1; i < len(res.Observations); i++ {
		if res.Observations[i].ID < res.Observations[i-1].ID {
			t.Fatalf("rows not ascending by ID within hop block: %d after %d", res.Observations[i].ID, res.Observations[i-1].ID)
		}
	}
	if len(res.Observations) != 50 {
		t.Fatalf("emitted %d rows, want exactly max_results=50", len(res.Observations))
	}
	if res.Observations[0].Title == "" || res.Observations[0].Type == "" {
		t.Fatalf("neighbor observations must be hydrated, got %+v", res.Observations[0])
	}
	_ = hubB
}

// --- capability semantics: dedup, filters, ordering --------------------------

func TestGetLevelNeighborObservationsDedupFiltersOrder(t *testing.T) {
	store, db := openV2Graph(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO sessions(id,project,directory) VALUES ('s','p','/d')`); err != nil {
		t.Fatal(err)
	}
	obs := func() int64 {
		res, err := db.Exec(`INSERT INTO observations(session_id,type,title,content,project) VALUES ('s','manual','o','c','p')`)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	root, a, b, c, ghost, closed := obs(), obs(), obs(), obs(), obs(), obs()

	// Duplicate adjacency: two relation types on the same pair.
	for _, rel := range []string{domain.RelationReferences, domain.RelationRelatesTo} {
		if err := store.CreateEdge(ctx, &domain.Edge{FromObsID: root, ToObsID: a, RelationType: rel, Weight: 1}); err != nil {
			t.Fatal(err)
		}
	}
	// Reversed duplicate: root<-b plus root->b.
	if err := store.CreateEdge(ctx, &domain.Edge{FromObsID: root, ToObsID: b, RelationType: domain.RelationReferences, Weight: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEdge(ctx, &domain.Edge{FromObsID: b, ToObsID: root, RelationType: domain.RelationFollows, Weight: 1}); err != nil {
		t.Fatal(err)
	}
	// Closed v2 fact: valid_until set and superseded state -> excluded.
	if _, err := db.Exec(`INSERT INTO edges(from_obs_id,to_obs_id,relation_type,valid_from,valid_until,tx_from,tx_until,fact_state,evolution_type) VALUES (?,?,?, '2020-01-01T00:00:00Z','2025-01-01T00:00:00Z','2020-01-01T00:00:00Z','2025-01-01T00:00:00Z','superseded','original')`, root, closed, domain.RelationReferences); err != nil {
		t.Fatal(err)
	}
	// Soft-deleted neighbor -> excluded.
	if _, err := db.Exec(`UPDATE observations SET deleted_at='2026-01-01T00:00:00Z' WHERE id=?`, ghost); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEdge(ctx, &domain.Edge{FromObsID: root, ToObsID: ghost, RelationType: domain.RelationReferences, Weight: 1}); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetLevelNeighborObservations(ctx, []int64{root})
	if err != nil {
		t.Fatalf("GetLevelNeighborObservations: %v", err)
	}
	list := got[root]
	ids := make([]int64, 0, len(list))
	for _, o := range list {
		ids = append(ids, o.ID)
	}
	if want := []int64{a, b}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("adjacency = %v, want deduplicated ascending [a b] excluding closed facts and deleted neighbors", ids)
	}

	// Empty frontier: no work, no error.
	empty, err := store.GetLevelNeighborObservations(ctx, nil)
	if err != nil {
		t.Fatalf("empty frontier: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty frontier returned %d entries", len(empty))
	}
	_ = c
}

// --- legacy/v2 parity --------------------------------------------------------

func TestPathBatchLegacyV2Parity(t *testing.T) {
	ctx := context.Background()
	build := func(t *testing.T, store *Store, db *sql.DB, v2 bool, rng *rand.Rand) ([]int64, *domain.GraphTraversalResult) {
		hubA, joinLeaf, hubB, target := seedJoinedStar(t, store, db, v2, rng)
		path, err := graphdomain.NewService(store).FindPathBounded(ctx, hubA, target, 5, 0)
		if err != nil {
			t.Fatalf("FindPathBounded: %v", err)
		}
		res, err := graphdomain.NewService(store).GetRelatedBounded(ctx, hubA, domain.GraphTraversalOptions{Depth: 2, MaxVisited: 250, MaxResults: 10})
		if err != nil {
			t.Fatalf("GetRelatedBounded: %v", err)
		}
		_ = joinLeaf
		_ = hubB
		return path, res
	}

	legacyStore, legacyDB, cleanup := setupTestStore(t)
	defer cleanup()
	legacyPath, legacyRes := build(t, legacyStore, legacyDB, false, nil)

	v2Store, v2DB := openV2Graph(t)
	v2Path, v2Res := build(t, v2Store, v2DB, true, rand.New(rand.NewSource(7)))

	if !reflect.DeepEqual(legacyPath, v2Path) {
		t.Fatalf("legacy path %v != v2 path %v", legacyPath, v2Path)
	}
	if !reflect.DeepEqual(resultIDsOf(legacyRes), resultIDsOf(v2Res)) {
		t.Fatalf("legacy rows %v != v2 rows %v", resultIDsOf(legacyRes), resultIDsOf(v2Res))
	}
	if legacyRes.Truncated != v2Res.Truncated || !reflect.DeepEqual(legacyRes.TruncationReasons, v2Res.TruncationReasons) {
		t.Fatalf("legacy truncation %+v != v2 %+v", legacyRes, v2Res)
	}
	if !v2Res.Truncated {
		t.Fatal("expected truncation with max_visited 250 on the 600-node star")
	}
}

func resultIDsOf(res *domain.GraphTraversalResult) []int64 {
	ids := make([]int64, 0, len(res.Observations))
	for _, o := range res.Observations {
		ids = append(ids, o.ID)
	}
	return ids
}

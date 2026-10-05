//go:build cortex_vectors

// Package sqlite_test hosts the retrieval.SearchVectors full-pipeline suite
// (VEC-01). It must live outside package sqlite: an in-package test importing
// internal/retrieval forms the cycle sqlite(test) -> retrieval -> embedding ->
// sqlite, which fails the vector-tests gate. The production packages are
// untouched, so the rescue path stays UNWIRED and routing-neutral.
package sqlite_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"math"
	"sync/atomic"
	"testing"

	moderncsqlite "modernc.org/sqlite" // base driver for the counting wrapper

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/retrieval"
	"github.com/lleontor705/cortex/v2/internal/store/sqlite"
)

// ---------------------------------------------------------------------------
// Counting driver infrastructure. This is a copy of the harness in
// memory_store_batch_test.go: that file belongs to package sqlite, whose
// in-package test symbols are invisible to this external test package. The
// registration prefix differs from the original because both harnesses share
// one test binary and sql.Register panics on a duplicate driver name.
// ---------------------------------------------------------------------------

// stmtCounter counts SQL statements that reached the underlying driver.
type stmtCounter struct{ n int64 }

func (c *stmtCounter) add()         { atomic.AddInt64(&c.n, 1) }
func (c *stmtCounter) value() int64 { return atomic.LoadInt64(&c.n) }
func (c *stmtCounter) reset()       { atomic.StoreInt64(&c.n, 0) }

// countingDriver wraps the modernc sqlite driver, returning connections that
// count statements. It is registered under a unique name per database so
// tests can run in parallel without sql.Register panics.
type countingDriver struct {
	base    driver.Driver
	counter *stmtCounter
}

func (d *countingDriver) Open(dsn string) (driver.Conn, error) {
	conn, err := d.base.Open(dsn)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: conn, counter: d.counter}, nil
}

// countingConn delegates every statement to the wrapped connection and
// counts each completed one-shot query/exec (and each execution of a
// prepared statement, via the countingStmt wrapper returned from Prepare —
// modernc.org/sqlite never returns ErrSkip for one-shot calls, so each
// statement execution is counted exactly once at its execution site).
type countingConn struct {
	driver.Conn
	counter *stmtCounter
}

func (c *countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := q.QueryContext(ctx, query, args)
	if err == driver.ErrSkip {
		return nil, err // prepared fallback; counted at the stmt execution
	}
	c.counter.add()
	return rows, err
}

func (c *countingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	e, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	res, err := e.ExecContext(ctx, query, args)
	if err == driver.ErrSkip {
		return nil, err
	}
	c.counter.add()
	return res, err
}

// Prepare is NOT counted: preparation is not a statement execution. The
// returned statement is wrapped so each later Query/Exec on it counts
// exactly once — mirroring the one-shot QueryContext/ExecContext accounting
// for the cached-statement path exercised by Store.GetByIDs.
func (c *countingConn) Prepare(query string) (driver.Stmt, error) {
	st, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &countingStmt{Stmt: st, counter: c.counter}, nil
}

// countingStmt counts each executed prepared statement.
type countingStmt struct {
	driver.Stmt
	counter *stmtCounter
}

func (s *countingStmt) Query(args []driver.Value) (driver.Rows, error) {
	//nolint:staticcheck // SA1019: modernc.org/sqlite's stmt implements only
	// the deprecated Query/Exec driver interfaces (no StmtQueryContext), so
	// a delegating wrapper has no non-deprecated call site available; this
	// is exactly the path database/sql itself takes for such drivers.
	rows, err := s.Stmt.Query(args)
	s.counter.add()
	return rows, err
}

func (s *countingStmt) Exec(args []driver.Value) (driver.Result, error) {
	//nolint:staticcheck // SA1019: see Query justification.
	res, err := s.Stmt.Exec(args)
	s.counter.add()
	return res, err
}

// countingDriverSeq guarantees unique driver registrations across tests.
var countingDriverSeq int64

// newCountingDB opens a fresh in-memory SQLite database whose statements are
// counted. Call counter.reset() after applying the schema and before the
// measured operation.
func newCountingDB(t *testing.T) (*sql.DB, *stmtCounter) {
	t.Helper()

	counter := &stmtCounter{}
	name := fmt.Sprintf("sqlite-counting-sqlite_test-%d", atomic.AddInt64(&countingDriverSeq, 1))
	sql.Register(name, &countingDriver{base: &moderncsqlite.Driver{}, counter: counter})

	db, err := sql.Open(name, ":memory:")
	if err != nil {
		t.Fatalf("sqlite_test: open counting db: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db, counter
}

// benchVectorIndex mirrors sqlite_blob.Adapter over the concrete VectorStore
// (same PostFilter capabilities, same Search translation) without importing
// the adapter package: the adapter imports package sqlite, so importing it
// here would be the same import cycle this package exists to avoid.
type benchVectorIndex struct{ store *sqlite.VectorStore }

var _ domain.VectorIndex = (*benchVectorIndex)(nil)

func (b *benchVectorIndex) ID() string { return "sqlite_blob" }

func (b *benchVectorIndex) Upsert(_ context.Context, _ []domain.VectorPoint) error { return nil }
func (b *benchVectorIndex) Delete(_ context.Context, _ []int64) error              { return nil }
func (b *benchVectorIndex) Close() error                                           { return nil }

func (b *benchVectorIndex) Health(_ context.Context) domain.Health {
	return domain.Health{Status: domain.StatusHealthy, Message: "bench"}
}

func (b *benchVectorIndex) Capabilities(_ context.Context) (domain.Capabilities, error) {
	return domain.Capabilities{
		IndexType: "sqlite_blob",
		Filters:   "PostFilter",
	}, nil
}

func (b *benchVectorIndex) Search(ctx context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	results, err := b.store.SearchByVector(ctx, domain.VectorSearchOptions{
		Embedding: q.Vector,
		Limit:     q.Limit,
		Threshold: q.Threshold,
	})
	if err != nil {
		return nil, err
	}
	candidates := make([]domain.VectorCandidate, 0, len(results))
	for _, r := range results {
		candidates = append(candidates, domain.VectorCandidate{
			ID:         r.ID,
			Score:      r.Similarity,
			Provenance: "sqlite_blob",
		})
	}
	return candidates, nil
}

// perIDOnlyLookup hides GetByIDs so retrieval.SearchVectors exercises the
// LEGACY per-ID hydration path over the same *Store (A/B benchmark control).
type perIDOnlyLookup struct{ inner *sqlite.Store }

func (p perIDOnlyLookup) GetByID(ctx context.Context, id int64) (*domain.Observation, error) {
	return p.inner.GetByID(ctx, id)
}

// TestSearchVectors_Limit50_SingleHydrationSQL pins the VEC-01 query budget
// on the full pipeline: limit=50 PostFilter search (pool 150, legacy clamp
// 100) must issue exactly ONE ANN scan plus ONE batch hydration statement.
func TestSearchVectors_Limit50_SingleHydrationSQL(t *testing.T) {
	db, counter := newCountingDB(t)
	if _, err := db.Exec(sqlite.VectorPipelineSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	queryVec := sqlite.SeedSearchPipelineFixture(t, db)
	counter.reset()

	store := sqlite.NewStore(db)
	idx := &benchVectorIndex{store: sqlite.NewVectorStore(db)}
	results, err := retrieval.SearchVectors(context.Background(), idx, domain.VectorQuery{
		Vector: queryVec,
		Limit:  50,
	}, store)
	if err != nil {
		t.Fatalf("SearchVectors: %v", err)
	}
	if len(results) != 50 {
		t.Fatalf("expected 50 results, got %d", len(results))
	}
	if n := counter.value(); n != 2 {
		t.Fatalf("limit=50 PostFilter search must issue exactly 2 statements (1 ANN scan + 1 batch hydration), got %d", n)
	}
	// Row 0 shares the query embedding: it must rank first with similarity 1.
	if results[0].ID != 1 || math.Abs(results[0].Similarity-1.0) > 1e-6 {
		t.Fatalf("top result must be ID 1 with similarity 1.0, got %d/%f", results[0].ID, results[0].Similarity)
	}
}

// BenchmarkSearchVectorsLimit50 measures the full PostFilter pipeline with
// batch hydration (the new default: *Store implements BatchObservationLookup).
func BenchmarkSearchVectorsLimit50(b *testing.B) {
	db := sqlite.OpenSearchPipelineDB(b)
	if _, err := db.Exec(sqlite.VectorPipelineSchema); err != nil {
		b.Fatalf("schema: %v", err)
	}
	queryVec := sqlite.SeedSearchPipelineFixture(b, db)
	store := sqlite.NewStore(db)
	idx := &benchVectorIndex{store: sqlite.NewVectorStore(db)}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := retrieval.SearchVectors(ctx, idx, domain.VectorQuery{
			Vector: queryVec,
			Limit:  50,
		}, store); err != nil {
			b.Fatalf("SearchVectors: %v", err)
		}
	}
}

// BenchmarkSearchVectorsLimit50_LegacyHydration is the pre-batch control:
// identical pipeline with the per-ID-only lookup (100 GetByID statements).
func BenchmarkSearchVectorsLimit50_LegacyHydration(b *testing.B) {
	db := sqlite.OpenSearchPipelineDB(b)
	if _, err := db.Exec(sqlite.VectorPipelineSchema); err != nil {
		b.Fatalf("schema: %v", err)
	}
	queryVec := sqlite.SeedSearchPipelineFixture(b, db)
	store := sqlite.NewStore(db)
	idx := &benchVectorIndex{store: sqlite.NewVectorStore(db)}
	legacy := perIDOnlyLookup{inner: store}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := retrieval.SearchVectors(ctx, idx, domain.VectorQuery{
			Vector: queryVec,
			Limit:  50,
		}, legacy); err != nil {
			b.Fatalf("SearchVectors: %v", err)
		}
	}
}

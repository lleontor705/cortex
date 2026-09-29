package mcp

// perf_bench_test.go holds the Wave-5 query-count oracle for the MCP graph
// tools (REQ-SQ-PERF-001 / REQ-SQ-PERF-003). BenchmarkGraphTools dispatches the
// graph tool handlers over a seeded in-memory project through an instrumented
// SQLite driver, so each sub-benchmark reports the exact statement count next
// to ns/op.
//
// At baseline the observation-graph path (cortex_get_blast_radius) issues one
// edge lookup per observation — the N+1 the Wave-7 batch replaces — while the
// batched traversal path (cortex_graph) stays bounded. Fixture construction and
// the untimed probe run outside the counted region.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/migration"
	graphstore "github.com/lleontor705/cortex/v2/internal/store/graph"
	"github.com/lleontor705/cortex/v2/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
	"github.com/mark3labs/mcp-go/mcp"
	"modernc.org/sqlite"
)

const (
	perfGraphProjectName = "demo"
	perfGraphSessionID   = "perf-graph-session"
	// perfGraphQuerySlack bounds the non-fan-out statements of the observation
	// path (root lookup, project list, schema probes) so the assertion pins the
	// per-observation N+1 without being brittle to incidental query shifts.
	perfGraphQuerySlack = 16
)

var perfGraphObservations = []int{100, 500}

// BenchmarkGraphTools measures the two MCP graph tool paths over 100- and
// 500-observation projects. Each run records queries/op; the blast-radius path
// is asserted against its O(n) fan-out ceiling so a regression to O(n^2) or a
// broken counter fails the benchmark.
func BenchmarkGraphTools(b *testing.B) {
	ctx := context.Background()
	rootID := float64(1)

	for _, observations := range perfGraphObservations {
		stores, counter := newGraphPerfStores(b, observations)

		b.Run(fmt.Sprintf("blast_radius/n=%d", observations), func(b *testing.B) {
			handler := handleGetBlastRadius(stores)
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]any{"observation_id": rootID}

			if _, err := handler(ctx, request); err != nil {
				b.Fatalf("blast radius probe: %v", err)
			}

			counter.reset()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := handler(ctx, request); err != nil {
					b.Fatal(err)
				}
			}

			perOp := float64(counter.load()) / float64(b.N)
			b.ReportMetric(perOp, "queries/op")
			if budget := observations + perfGraphQuerySlack; perOp > float64(budget) {
				b.Fatalf("blast radius queries/op = %.0f, exceeds the O(n) baseline bound %d", perOp, budget)
			}
		})

		b.Run(fmt.Sprintf("graph/n=%d", observations), func(b *testing.B) {
			handler := handleGraph(stores)
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]any{
				"observation_id": rootID,
				"depth":          float64(1),
				"max_visited":    float64(10000),
				"max_results":    float64(1000),
			}

			if _, err := handler(ctx, request); err != nil {
				b.Fatalf("graph probe: %v", err)
			}

			counter.reset()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := handler(ctx, request); err != nil {
					b.Fatal(err)
				}
			}

			perOp := float64(counter.load()) / float64(b.N)
			b.ReportMetric(perOp, "queries/op")
			if budget := perfGraphQuerySlack * 2; perOp > float64(budget) {
				b.Fatalf("graph traversal queries/op = %.0f, exceeds the bounded budget %d", perOp, budget)
			}
		})
	}
}

// newGraphPerfStores opens an instrumented in-memory v2 database, seeds a
// project of the requested size, and returns the bundle stores plus the live
// statement counter reset to the post-seed state.
func newGraphPerfStores(b *testing.B, observations int) (*Stores, *graphPerfQueryCounter) {
	b.Helper()

	counter := &graphPerfQueryCounter{}
	driverName := fmt.Sprintf("graph-perf-count-%d", graphPerfDriverSeq.Add(1))
	sql.Register(driverName, &graphPerfCountingDriver{inner: &sqlite.Driver{}, counter: counter})

	db, err := sql.Open(driverName, ":memory:")
	if err != nil {
		b.Fatalf("open counted perf db: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	b.Cleanup(func() { _ = db.Close() })

	base, err := migration.NewV2Baseline()
	if err != nil {
		b.Fatalf("v2 baseline: %v", err)
	}
	if err := base.Apply(context.Background(), db); err != nil {
		b.Fatalf("apply v2 baseline: %v", err)
	}

	seedGraphPerfProject(b, db, observations)

	stores := &Stores{
		Observations: sqlitestore.NewStore(db),
		Sessions:     session.NewStore(db),
		Graph:        graphstore.NewStore(db),
	}
	counter.reset()
	return stores, counter
}

func seedGraphPerfProject(b *testing.B, db *sql.DB, observations int) {
	b.Helper()
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions(id, project, directory) VALUES (?, ?, ?)`,
		perfGraphSessionID, perfGraphProjectName, "."); err != nil {
		b.Fatalf("seed session: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatalf("begin seed tx: %v", err)
	}

	observationStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO observations(session_id, type, title, content, project, scope, source)
		 VALUES (?, 'manual', ?, ?, ?, 'project', 'manual')`)
	if err != nil {
		b.Fatalf("prepare observation insert: %v", err)
	}
	for index := 0; index < observations; index++ {
		title := fmt.Sprintf("perf graph observation %d", index+1)
		if _, err := observationStmt.ExecContext(ctx, perfGraphSessionID, title, title, perfGraphProjectName); err != nil {
			b.Fatalf("seed observation %d: %v", index, err)
		}
	}
	if err := observationStmt.Close(); err != nil {
		b.Fatalf("close observation insert: %v", err)
	}

	edgeStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO edges(from_obs_id, to_obs_id, relation_type) VALUES (?, ?, ?)`)
	if err != nil {
		b.Fatalf("prepare edge insert: %v", err)
	}
	relations := [...]string{"references", "relates_to", "follows", "supersedes", "contradicts"}
	for target := 1; target <= observations; target++ {
		for offset, relation := range relations {
			source := (target+offset)%observations + 1
			if _, err := edgeStmt.ExecContext(ctx, source, target, relation); err != nil {
				b.Fatalf("seed edge %d-%d: %v", source, target, err)
			}
		}
	}
	if err := edgeStmt.Close(); err != nil {
		b.Fatalf("close edge insert: %v", err)
	}

	if err := tx.Commit(); err != nil {
		b.Fatalf("commit seed tx: %v", err)
	}
}

// graphPerfQueryCounter counts SQL statements that reached the wrapped driver.
type graphPerfQueryCounter struct{ n int64 }

func (c *graphPerfQueryCounter) add()        { atomic.AddInt64(&c.n, 1) }
func (c *graphPerfQueryCounter) load() int64 { return atomic.LoadInt64(&c.n) }
func (c *graphPerfQueryCounter) reset()      { atomic.StoreInt64(&c.n, 0) }

var graphPerfDriverSeq atomic.Int64

// graphPerfCountingDriver wraps the modernc SQLite driver under a per-DB
// unique name so concurrent benchmark registrations cannot collide.
type graphPerfCountingDriver struct {
	inner   driver.Driver
	counter *graphPerfQueryCounter
}

func (d *graphPerfCountingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &graphPerfCountingConn{Conn: conn, counter: d.counter}, nil
}

// graphPerfCountingConn counts one-shot statements and wraps prepared
// statements so cached-statement executions are counted exactly once.
type graphPerfCountingConn struct {
	driver.Conn
	counter *graphPerfQueryCounter
}

func (c *graphPerfCountingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := queryer.QueryContext(ctx, query, args)
	if err == driver.ErrSkip {
		return nil, err
	}
	c.counter.add()
	return rows, err
}

func (c *graphPerfCountingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	result, err := execer.ExecContext(ctx, query, args)
	if err == driver.ErrSkip {
		return nil, err
	}
	c.counter.add()
	return result, err
}

func (c *graphPerfCountingConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &graphPerfCountingStmt{Stmt: stmt, counter: c.counter}, nil
}

type graphPerfCountingStmt struct {
	driver.Stmt
	counter *graphPerfQueryCounter
}

func (s *graphPerfCountingStmt) Query(args []driver.Value) (driver.Rows, error) {
	//nolint:staticcheck // SA1019: modernc.org/sqlite exposes only the legacy
	// Query/Exec stmt interfaces, so database/sql itself takes this path.
	rows, err := s.Stmt.Query(args)
	s.counter.add()
	return rows, err
}

func (s *graphPerfCountingStmt) Exec(args []driver.Value) (driver.Result, error) {
	//nolint:staticcheck // SA1019: see Query.
	result, err := s.Stmt.Exec(args)
	s.counter.add()
	return result, err
}

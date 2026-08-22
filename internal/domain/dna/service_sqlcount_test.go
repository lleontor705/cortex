package dna

// service_sqlcount_test.go is the concrete DNA-QUERY-COUNT and
// DNA-DETERMINISM integration oracle: it drives the real local composition
// (SQLite observation store + scoring store + graph store) through a
// counting driver so the N=500 statement budget is measured against actual
// SQL statements instead of mocked provider calls.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
	graphstore "github.com/lleontor705/cortex/internal/store/graph"
	scoringstore "github.com/lleontor705/cortex/internal/store/scoring"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
	"modernc.org/sqlite"
)

// --- counting driver ----------------------------------------------------------

var dnaCountDriverSeq atomic.Int64

// dnaCountingDriver wraps the modernc SQLite driver and counts every SQL
// statement executed through QueryContext/ExecContext, giving the DNA tests
// a real SQL-statement oracle for the "<= 3 statements at N=500" budget.
type dnaCountingDriver struct {
	inner driver.Driver
	count *atomic.Int64
}

func (d *dnaCountingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &dnaCountingConn{Conn: conn, count: d.count}, nil
}

type dnaCountingConn struct {
	driver.Conn
	count *atomic.Int64
}

func (c *dnaCountingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.count.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *dnaCountingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.count.Add(1)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

// openDNACountingDB opens an in-memory SQLite database carrying the DNA test
// schema whose SQL statements are counted. A single pooled connection keeps
// :memory: coherent, mirroring database.InMemoryConfig.
func openDNACountingDB(tb testing.TB) (*sql.DB, *atomic.Int64) {
	tb.Helper()
	count := &atomic.Int64{}
	name := fmt.Sprintf("dnasqlcount-%d", dnaCountDriverSeq.Add(1))
	sql.Register(name, &dnaCountingDriver{inner: &sqlite.Driver{}, count: count})
	db, err := sql.Open(name, ":memory:")
	if err != nil {
		tb.Fatalf("dna: open counting database: %v", err)
	}
	db.SetMaxOpenConns(1)
	tb.Cleanup(func() { _ = db.Close() })
	applyDNAMigrations(tb, db)
	return db, count
}

// dnaBatchedStatementBudget pins the successful batched N=500 path: exactly
// one observation-list statement, one batch score statement, and one batch
// edge-count statement.
const dnaBatchedStatementBudget = 3

// TestGenerateSQLiteBatchedExactlyThreeStatementsN500 asserts the concrete
// local composition emits exactly three SQL statements for the N=500 Project
// DNA generation, and that the counting oracle is not vacuous: the legacy
// per-ID composition on the same data must issue the full 1001-statement
// sweep while producing identical output.
func TestGenerateSQLiteBatchedExactlyThreeStatementsN500(t *testing.T) {
	ctx := context.Background()
	db, count := openDNACountingDB(t)
	// Decision rows 6 and 11 lose their score rows so the batched statement
	// returns partial data and the 0.5 default still rides the same budget.
	seedDNAFixture(t, db, "dna", buildDNAFixture(500), []int64{6, 11})

	svc := NewService(
		sqlitestore.NewStore(db),
		scoringstore.NewStore(db),
		graphstore.NewStore(db),
	)

	count.Store(0) // exclude schema and seed setup from the measured window
	out, err := svc.Generate(ctx, "dna")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := count.Load(); got != dnaBatchedStatementBudget {
		t.Fatalf("batched N=500 emitted %d SQL statements, want exactly %d (1 list + 1 score + 1 edge count)",
			got, dnaBatchedStatementBudget)
	}
	if !strings.Contains(out, "500 observations") {
		t.Errorf("expected the full fixture summary, got:\n%s", out)
	}

	// Contrast on the same data: hiding the batch capabilities must issue
	// the legacy per-ID sweep — 1 list + 500 GetScore + 500
	// CountEdgesByObservation — proving the counter measures real SQL and
	// the oracle fails when the budget is violated.
	legacy := NewService(
		sqlitestore.NewStore(db),
		legacyOnlyScoring{inner: scoringstore.NewStore(db)},
		legacyOnlyEdges{inner: graphstore.NewStore(db)},
	)
	count.Store(0)
	legacyOut, err := legacy.Generate(ctx, "dna")
	if err != nil {
		t.Fatalf("legacy generate: %v", err)
	}
	if got, want := count.Load(), int64(1+500+500); got != want {
		t.Fatalf("legacy N=500 emitted %d SQL statements, want %d", got, want)
	}
	if legacyOut != out {
		t.Fatalf("legacy and batched compositions must produce identical output on the same data")
	}
}

// TestGenerateSQLiteCountingDeterminismN500 asserts that reordered insertion
// yields byte-identical markdown while both orderings ride the same exact
// three-statement budget.
func TestGenerateSQLiteCountingDeterminismN500(t *testing.T) {
	ctx := context.Background()
	obs := buildDNAFixture(500)

	build := func(seedOrder []*domain.Observation) (string, int64) {
		db, count := openDNACountingDB(t)
		seedDNAFixture(t, db, "dna", seedOrder, []int64{6, 11})
		svc := NewService(
			sqlitestore.NewStore(db),
			scoringstore.NewStore(db),
			graphstore.NewStore(db),
		)
		count.Store(0)
		out, err := svc.Generate(ctx, "dna")
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		return out, count.Load()
	}

	forward, forwardCount := build(obs)
	reversed, reversedCount := build(reverseObservations(obs))

	if forward == "" || !strings.Contains(forward, "500 observations") {
		t.Fatalf("expected the full fixture summary, got:\n%s", forward)
	}
	if forward != reversed {
		t.Fatalf("reordered insertion must yield byte-identical markdown under the total order")
	}
	if forwardCount != dnaBatchedStatementBudget || reversedCount != dnaBatchedStatementBudget {
		t.Fatalf("statement counts forward=%d reversed=%d, want exactly %d on both",
			forwardCount, reversedCount, dnaBatchedStatementBudget)
	}
}

package http

// perf_bench_test.go holds the Wave-5 baseline oracles for the local parity
// surface (REQ-SQ-PERF-001). Both benchmarks run over the same bundle-backed
// fixture — 1000 observations and 5000 current edges — and measure only the
// request path; fixture construction happens outside the timed region.
//
// Fixture layout: the requested project "demo" (perfProjectObservations = 100)
// stays under the default max_nodes envelope, so ProjectGraph seeds the whole
// project and then fans out over its edges instead of truncating during the
// seed scan. The remaining observations live in project "bulk". Every
// observation is the target of one edge per canonical relation type, so
// idx_edges_one_current_fact admits exactly perfRelationTypeCount current edges
// per node and the fixture reaches the 5000-edge total.

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/migration"
	"github.com/lleontor705/cortex/v2/internal/store/bundle"
	graphstore "github.com/lleontor705/cortex/v2/internal/store/graph"
	"github.com/lleontor705/cortex/v2/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
	_ "modernc.org/sqlite"
)

const (
	perfProjectObservations = 100
	perfTotalObservations   = 1000
	perfRelationTypeCount   = 5
	perfTotalEdges          = perfTotalObservations * perfRelationTypeCount
	perfProjectName         = "demo"
	perfBulkProjectName     = "bulk"
	perfSessionID           = "perf-session"
)

var perfEdgeRelationTypes = [perfRelationTypeCount]string{
	"references", "relates_to", "follows", "supersedes", "contradicts",
}

// BenchmarkProjectGraph measures the bounded breadth-first walk of the local
// parity port at the default envelope (depth 2, max_nodes 100) over the
// fixture. With 100 scoped observations the seed scan fills exactly max_nodes
// and the level loop issues one CurrentEdges query per frontier node, so the
// baseline exposes the per-node edge fan-out that the one-pass SQL proposal
// targets.
func BenchmarkProjectGraph(b *testing.B) {
	stores := newHTTPPerfBundle(b)
	graphPort := newLocalProjectGraph(&Deps{Observations: stores.Observations, Graph: stores.Graph})
	ctx := context.Background()

	probe, err := graphPort.ProjectGraph(ctx, perfProjectName, 2, 100)
	if err != nil {
		b.Fatalf("project graph probe: %v", err)
	}
	if len(probe.Nodes) != perfProjectObservations {
		b.Fatalf("project graph probe nodes = %d, want %d", len(probe.Nodes), perfProjectObservations)
	}
	if len(probe.Edges) == 0 {
		b.Fatal("project graph probe emitted no edges; the fixture is not exercising the walk")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := graphPort.ProjectGraph(ctx, perfProjectName, 2, 100); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStatsHandler measures the parity stats port the /api/stats handler
// dispatches to. ServerStats recomputes the dashboard counters from the
// observation, session, and graph stores on every call, which is the cost the
// stats TTL quick win addresses.
func BenchmarkStatsHandler(b *testing.B) {
	stores := newHTTPPerfBundle(b)
	port := newLocalOpsPort(stores)
	ctx := context.Background()

	probe, err := port.ServerStats(ctx)
	if err != nil {
		b.Fatalf("stats probe: %v", err)
	}
	if probe.Observations != perfTotalObservations || probe.Edges != perfTotalEdges {
		b.Fatalf("stats probe = %+v, want %d observations / %d edges",
			probe, perfTotalObservations, perfTotalEdges)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := port.ServerStats(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// newHTTPPerfBundle opens a fresh in-memory v2 database and returns the
// bundle-backed stores the parity port and graph traversal consume.
func newHTTPPerfBundle(b *testing.B) *bundle.Stores {
	b.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatalf("open perf db: %v", err)
	}
	db.SetMaxOpenConns(1)
	b.Cleanup(func() { _ = db.Close() })

	base, err := migration.NewV2Baseline()
	if err != nil {
		b.Fatalf("v2 baseline: %v", err)
	}
	if err := base.Apply(context.Background(), db); err != nil {
		b.Fatalf("apply v2 baseline: %v", err)
	}

	seedHTTPPerfGraph(b, db)

	return &bundle.Stores{
		Observations: sqlitestore.NewStore(db),
		Sessions:     session.NewStore(db),
		Graph:        graphstore.NewStore(db),
	}
}

func seedHTTPPerfGraph(b *testing.B, db *sql.DB) {
	b.Helper()
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions(id, project, directory) VALUES (?, ?, ?)`,
		perfSessionID, perfProjectName, "."); err != nil {
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
	for index := 0; index < perfTotalObservations; index++ {
		project := perfProjectName
		if index >= perfProjectObservations {
			project = perfBulkProjectName
		}
		title := fmt.Sprintf("perf observation %d", index+1)
		if _, err := observationStmt.ExecContext(ctx, perfSessionID, title, title, project); err != nil {
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
	for target := 1; target <= perfTotalObservations; target++ {
		first, span := 1, perfProjectObservations
		if target > perfProjectObservations {
			first = perfProjectObservations + 1
			span = perfTotalObservations - perfProjectObservations
		}
		for offset, relation := range perfEdgeRelationTypes {
			source := first + (target-first+offset+1)%span
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

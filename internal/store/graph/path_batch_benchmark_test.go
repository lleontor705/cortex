package graph

// path_batch_benchmark_test.go provides the retained R1R2 benchmark controls
// for GRAPH-01/GRAPH-02 (PATH-BENCH). BenchmarkFindPath and
// BenchmarkGraphTraversal each run batched (level-neighbor batch capability)
// and legacy (bounded per-node fallback via noBatchStore) sub-benchmarks over
// the same joined 300-leaf star fixture used by the functional gates, so the
// normative `go test -bench='FindPath|GraphTraversal'` command emits credible
// batched-vs-legacy distributions over identical work.
//
// Before any timing, each control proves the two modes produce identical
// semantics (same lexicographically smallest shortest path; same traversal
// rows and truncation flags). Hard performance assertions (>=4x speedup,
// <=3 SQL statements, <=25ms) stay in the functional tests in
// path_batch_test.go; benchmarks only measure.

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	graphdomain "github.com/lleontor705/cortex/v2/internal/domain/graph"
	"github.com/lleontor705/cortex/v2/internal/migration"
	_ "modernc.org/sqlite"
)

// openBenchV2DB opens a plain (uncounted) in-memory v2-baseline database with
// a single pooled connection, so benchmark timings measure the production
// store path without driver instrumentation or pool churn.
func openBenchV2DB(b *testing.B) (*Store, *sql.DB) {
	b.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	b.Cleanup(func() { _ = db.Close() })
	base, err := migration.NewV2Baseline()
	if err != nil {
		b.Fatal(err)
	}
	if err := base.Apply(context.Background(), db); err != nil {
		b.Fatal(err)
	}
	return NewStore(db), db
}

// BenchmarkFindPath times the 3-hop joined-star path query hub A -> join leaf
// -> hub B -> smallest M leaf in both modes. The legacy control forces the
// bounded per-node fallback (~302 GetRelated lookups) against the same store.
func BenchmarkFindPath(b *testing.B) {
	ctx := context.Background()
	store, db := openBenchV2DB(b)
	hubA, joinLeaf, hubB, target := seedJoinedStar(b, store, db, true, nil)
	want := []int64{hubA, joinLeaf, hubB, target}

	batched := graphdomain.NewService(store)
	legacy := graphdomain.NewService(noBatchStore{store})

	// Untimed semantics control: identical work must produce the identical
	// deterministic lexicographically smallest shortest path in both modes.
	batchedPath, err := batched.FindPathBounded(ctx, hubA, target, 5, 0)
	if err != nil {
		b.Fatalf("batched control: %v", err)
	}
	legacyPath, err := legacy.FindPathBounded(ctx, hubA, target, 5, 0)
	if err != nil {
		b.Fatalf("legacy control: %v", err)
	}
	if !reflect.DeepEqual(batchedPath, want) || !reflect.DeepEqual(legacyPath, want) {
		b.Fatalf("semantics control: batched=%v legacy=%v want=%v", batchedPath, legacyPath, want)
	}

	b.Run("batched", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := batched.FindPathBounded(ctx, hubA, target, 5, 0); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("legacy", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := legacy.FindPathBounded(ctx, hubA, target, 5, 0); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkGraphTraversal times a depth-3 bounded traversal from hub A with
// a 1000-visited / 1000-results budget in both modes. The budget admits the
// full 602-node reachable set, so the level-1 frontier expansion of 300
// leaves dominates: the legacy control issues ~302 per-node GetRelated
// lookups while the batched control resolves each of the three levels with
// one lookup.
func BenchmarkGraphTraversal(b *testing.B) {
	ctx := context.Background()
	store, db := openBenchV2DB(b)
	hubA, _, _, _ := seedJoinedStar(b, store, db, true, nil)
	opts := domain.GraphTraversalOptions{Depth: 3, MaxVisited: 1000, MaxResults: 1000}

	batched := graphdomain.NewService(store)
	legacy := graphdomain.NewService(noBatchStore{store})

	// Untimed semantics control: both modes must emit identical rows and
	// identical truncation evidence, and the control must be non-vacuous.
	batchedRes, err := batched.GetRelatedBounded(ctx, hubA, opts)
	if err != nil {
		b.Fatalf("batched control: %v", err)
	}
	legacyRes, err := legacy.GetRelatedBounded(ctx, hubA, opts)
	if err != nil {
		b.Fatalf("legacy control: %v", err)
	}
	if len(batchedRes.Observations) == 0 {
		b.Fatal("semantics control: traversal emitted no rows")
	}
	if !reflect.DeepEqual(resultIDsOf(batchedRes), resultIDsOf(legacyRes)) {
		b.Fatalf("semantics control: batched rows %v != legacy rows %v", resultIDsOf(batchedRes), resultIDsOf(legacyRes))
	}
	if batchedRes.Truncated != legacyRes.Truncated || !reflect.DeepEqual(batchedRes.TruncationReasons, legacyRes.TruncationReasons) {
		b.Fatalf("semantics control: batched truncation %+v != legacy %+v", batchedRes, legacyRes)
	}

	b.Run("batched", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := batched.GetRelatedBounded(ctx, hubA, opts); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("legacy", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := legacy.GetRelatedBounded(ctx, hubA, opts); err != nil {
				b.Fatal(err)
			}
		}
	})
}

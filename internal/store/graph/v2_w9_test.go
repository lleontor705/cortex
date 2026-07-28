package graph

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
	_ "modernc.org/sqlite"
)

func openV2Graph(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	b, err := migration.NewV2Baseline()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Apply(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db), db
}

func TestV2SupersedeRequiresAtomicUnitOfWorkAndPreservesHistory(t *testing.T) {
	store, db := openV2Graph(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO sessions(id,project,directory) VALUES ('s','p','/d')`); err != nil {
		t.Fatal(err)
	}
	var ids [2]int64
	for i := range ids {
		r, err := db.Exec(`INSERT INTO observations(session_id,type,title,content,project) VALUES ('s','manual',?,?,?)`, "o", "c", "p")
		if err != nil {
			t.Fatal(err)
		}
		ids[i], _ = r.LastInsertId()
	}
	first := &domain.Edge{FromObsID: ids[0], ToObsID: ids[1], RelationType: domain.RelationSupersedes, Weight: 1}
	if err := store.CreateEdge(ctx, first); err == nil {
		t.Fatal("supersede must not use autocommit")
	}
	insert := func(e *domain.Edge) error {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return err
		}
		if err := store.WithinTx(ctx, tx, func(tc context.Context) error { return store.CreateEdge(tc, e) }); err != nil {
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	}
	if err := insert(first); err != nil {
		t.Fatal(err)
	}
	second := &domain.Edge{FromObsID: ids[0], ToObsID: ids[1], RelationType: domain.RelationSupersedes, Weight: 1}
	if err := insert(second); err != nil {
		t.Fatal(err)
	}
	var current, history int
	if err := db.QueryRow(`SELECT COUNT(*) FROM edges WHERE valid_until IS NULL AND fact_state='current'`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM edges WHERE valid_until IS NOT NULL`).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if current != 1 || history != 1 {
		t.Fatalf("current=%d history=%d, want one each", current, history)
	}
}

func TestV2ScopedTraversalAsOfCycleAndTenant(t *testing.T) {
	store, db := openV2Graph(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO sessions(id,project,directory,tenant_id,workspace_id) VALUES ('s','p','/d','t','w')`); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 3)
	for i := range ids {
		res, err := db.Exec(`INSERT INTO observations(session_id,type,title,content,project,tenant_id,workspace_id) VALUES ('s','manual',?,?, 'p','t','w')`, string(rune('a'+i)), "c")
		if err != nil {
			t.Fatal(err)
		}
		ids[i], _ = res.LastInsertId()
	}
	// A cycle plus one historical hop. As-of must include the closed edge and
	// must not leak a different tenant/project.
	if _, err := db.Exec(`INSERT INTO edges(from_obs_id,to_obs_id,relation_type,valid_from,valid_until,tx_from,tenant_id,workspace_id) VALUES (?,?, 'references','2020-01-01T00:00:00Z','2025-01-01T00:00:00Z','2020-01-01T00:00:00Z','t','w'), (?,?, 'references','2020-01-01T00:00:00Z',NULL,'2020-01-01T00:00:00Z','t','w'), (?,?, 'references','2020-01-01T00:00:00Z',NULL,'2020-01-01T00:00:00Z','t','w')`, ids[0], ids[1], ids[1], ids[2], ids[2], ids[0]); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err := store.GetRelatedScoped(ctx, ids[0], domain.GraphTraversalOptions{Depth: 10, MaxVisited: 10, TenantID: "t", WorkspaceID: "w", Project: "p", AsOf: &at})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("as-of traversal returned %d observations, want 2", len(got))
	}
}

func TestV2ConcurrentSupersedesOneCurrentOrConflict(t *testing.T) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	defer func() { _ = db.Close() }()
	b, err := migration.NewV2Baseline()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Apply(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions(id,project,directory) VALUES ('s','p','/d')`); err != nil {
		t.Fatal(err)
	}
	var ids [3]int64
	for i := range ids {
		r, err := db.Exec(`INSERT INTO observations(session_id,type,title,content,project) VALUES ('s','manual','o','c','p')`)
		if err != nil {
			t.Fatal(err)
		}
		ids[i], _ = r.LastInsertId()
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, from := range []int64{ids[0], ids[1]} {
		wg.Add(1)
		go func(from int64) {
			defer wg.Done()
			<-start
			tx, e := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelSerializable})
			if e != nil {
				results <- e
				return
			}
			st := NewStore(db)
			e = st.WithinTx(context.Background(), tx, func(c context.Context) error {
				return st.CreateEdge(c, &domain.Edge{FromObsID: from, ToObsID: ids[2], RelationType: domain.RelationSupersedes, Weight: 1})
			})
			if e != nil {
				_ = tx.Rollback()
				results <- e
				return
			}
			results <- tx.Commit()
		}(from)
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent supersedes committed %d writers, want exactly one", success)
	}
	var current int
	if err := db.QueryRow(`SELECT COUNT(*) FROM edges WHERE valid_until IS NULL AND fact_state='current'`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != 1 {
		t.Fatalf("current successors=%d, want 1", current)
	}
}

package entity

import (
	"context"
	"database/sql"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/migration"
	_ "modernc.org/sqlite"
)

func TestStoreImplementsTxParticipant(t *testing.T) {
	var _ domain.TxParticipant = (*Store)(nil)
}

func TestFindByEntityScopedFiltersProjectAndTenant(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	b, err := migration.NewV2Baseline()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Apply(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions(id,project,directory,tenant_id,workspace_id) VALUES ('s1','p1','/d','t1','w1'),('s2','p2','/d','t2','w2')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO observations(session_id,type,title,content,project,tenant_id,workspace_id) VALUES ('s1','manual','a','c','p1','t1','w1'),('s2','manual','b','c','p2','t2','w2')`); err != nil {
		t.Fatal(err)
	}
	var ids [2]int64
	rows, _ := db.Query(`SELECT id FROM observations ORDER BY id`)
	for i := range ids {
		if !rows.Next() || rows.Scan(&ids[i]) != nil {
			t.Fatal("scan observation")
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	if err := store.SaveLinks(context.Background(), []*domain.EntityLink{{ObservationID: ids[0], EntityType: domain.EntityFile, EntityValue: "Café.go"}, {ObservationID: ids[1], EntityType: domain.EntityFile, EntityValue: "cafe\u0301.go"}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.FindByEntityScoped(context.Background(), domain.EntityFile, "CAFÉ.GO", "t1", "w1", "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ObservationID != ids[0] {
		t.Fatalf("scoped entity lookup leaked or missed row: %+v", got)
	}
	if got, err := store.FindByEntityScoped(context.Background(), domain.EntityFile, "cafe\u0301.go", "t1", "w1", "p1"); err != nil || len(got) != 1 {
		t.Fatalf("Unicode canonical lookup failed: %v %+v", err, got)
	}
	if got, err := store.FindByEntityScoped(context.Background(), domain.EntityFile, "cafe\u0301.go", "t2", "w2", "p2"); err != nil || len(got) != 1 || got[0].ObservationID != ids[1] {
		t.Fatalf("second scope lookup failed: %v %+v", err, got)
	}
	if got, err := store.FindByEntityScoped(context.Background(), domain.EntityFile, "cafe\u0301.go", "t1", "w1", "p2"); err != nil || len(got) != 0 {
		t.Fatalf("project isolation failed: %v %+v", err, got)
	}
}

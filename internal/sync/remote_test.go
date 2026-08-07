package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
	_ "modernc.org/sqlite"
)

func TestRemoteSyncPushesLocalAndPullsServerData(t *testing.T) {
	db, err := sql.Open("sqlite", "file:remote-sync-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	baseline, err := migration.NewV2Baseline()
	if err != nil {
		t.Fatal(err)
	}
	if err := baseline.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := db.Exec(`INSERT INTO sessions(id,project,directory,started_at) VALUES('local-session','cortex','/repo',?)`, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO observations(session_id,type,title,content,project,scope,confidence,source,created_at,updated_at) VALUES('local-session','manual','Local','local value','cortex','project',1,'manual',?,?)`, now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	var pushed domain.SyncBatch
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/sync/push":
			var request domain.SyncBatch
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			pushed.Sessions = append(pushed.Sessions, request.Sessions...)
			pushed.Observations = append(pushed.Observations, request.Observations...)
			pushed.Prompts = append(pushed.Prompts, request.Prompts...)
			pushed.Edges = append(pushed.Edges, request.Edges...)
			_ = json.NewEncoder(w).Encode(domain.SyncResult{Accepted: countBatch(&request)})
		case "/api/sync/changes":
			_ = json.NewEncoder(w).Encode(domain.SyncPage{SyncBatch: domain.SyncBatch{Sessions: []domain.SyncSession{{SyncID: "server-session", Project: "remote", Directory: "/remote", StartedAt: now, UpdatedAt: now}}}, Cursor: 7})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	syncer, err := NewRemoteSyncer(db, server.URL, "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := syncer.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Cursor != 7 || result.Pushed != 3 || result.Pulled != 1 {
		t.Fatalf("result = %+v", result)
	}
	if len(pushed.Sessions) != 2 || pushed.Sessions[0].SyncID != "local-session" || pushed.Sessions[1].SyncID != "server-session" || len(pushed.Observations) != 1 || pushed.Observations[0].SyncID == "" {
		t.Fatalf("pushed = %+v", pushed)
	}
	var project string
	if err := db.QueryRow(`SELECT project FROM sessions WHERE id='server-session'`).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if project != "remote" {
		t.Fatalf("pulled project = %q", project)
	}
}

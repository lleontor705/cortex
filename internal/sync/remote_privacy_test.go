package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
	"github.com/lleontor705/cortex/v2/internal/migration"
	_ "modernc.org/sqlite"
)

func setupRemotePrivacyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { _ = db.Close() })
	b, _ := migration.NewV2Baseline()
	if err := b.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestRemoteSync_PrivacyPullRedactionAndCursor(t *testing.T) {
	db := setupRemotePrivacyDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/changes" {
			_ = json.NewEncoder(w).Encode(domain.SyncPage{
				SyncBatch: domain.SyncBatch{
					Sessions:     []domain.SyncSession{{SyncID: "s-rem", Project: "a", Directory: "/d", Summary: "Sum <private>c1</private>", StartedAt: now, UpdatedAt: now}},
					Observations: []domain.SyncObservation{{SyncID: "o-rem", SessionSyncID: "s-rem", Type: "manual", Title: "T", Content: "Obs <private>c2</private> ok", Project: "a", Scope: "project", CreatedAt: now, UpdatedAt: now}},
					Prompts:      []domain.SyncPrompt{{SyncID: "p-rem", SessionSyncID: "s-rem", Project: "a", Content: "P <private>c3</private>", CreatedAt: now, UpdatedAt: now}},
				},
				Cursor: 42,
			})
			return
		}
		var req domain.SyncBatch
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(domain.SyncResult{Accepted: countBatch(&req)})
	}))
	defer srv.Close()

	syncer, _ := NewRemoteSyncer(db, srv.URL, "secret", time.Second)
	res, err := syncer.Sync(context.Background())
	if err != nil || res.Cursor != 42 || res.Pulled != 3 {
		t.Fatalf("Sync = (%+v, %v)", res, err)
	}
	var cur, content, dbHash, sum string
	_ = db.QueryRow("SELECT value FROM remote_sync_state WHERE key='cursor'").Scan(&cur)
	_ = db.QueryRow("SELECT content, normalized_hash FROM observations WHERE sync_id='o-rem'").Scan(&content, &dbHash)
	_ = db.QueryRow("SELECT summary FROM sessions WHERE id='s-rem'").Scan(&sum)
	if cur != "42" || content != "Obs [REDACTED] ok" || dbHash != hashNormalized("Obs [REDACTED] ok") || sum != "Sum [REDACTED]" {
		t.Fatalf("state mismatch: cur=%q, content=%q, hash=%q, sum=%q", cur, content, dbHash, sum)
	}
}

func TestRemoteSync_PrivacyPullAtomicRejectionZeroEffects(t *testing.T) {
	db := setupRemotePrivacyDB(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(domain.SyncPage{SyncBatch: domain.SyncBatch{Observations: []domain.SyncObservation{{SyncID: "bad", SessionSyncID: "x", Type: "manual", Title: "T", Content: "Bad <private>unclosed", Project: "a", Scope: "project"}}}, Cursor: 99})
	}))
	defer srv.Close()

	syncer, _ := NewRemoteSyncer(db, srv.URL, "secret", time.Second)
	_ = syncer.ensureState(context.Background())
	_, _ = db.Exec("INSERT INTO remote_sync_state(key, value) VALUES('cursor', '15')")
	if _, err := syncer.Sync(context.Background()); err == nil || !errors.Is(err, privacy.ErrInvalidMarker) {
		t.Fatalf("expected ErrInvalidMarker, got %v", err)
	}
	var cur string
	var count int
	_ = db.QueryRow("SELECT value FROM remote_sync_state WHERE key='cursor'").Scan(&cur)
	_ = db.QueryRow("SELECT count(*) FROM observations").Scan(&count)
	if cur != "15" || count != 0 {
		t.Fatalf("rejection dirty: cur=%q, count=%d", cur, count)
	}
}

func TestRemoteSync_PrivacyPushRedactsOutbound(t *testing.T) {
	db := setupRemotePrivacyDB(t)
	now := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	_, _ = db.Exec("INSERT INTO sessions(id,project,directory,started_at) VALUES('s-push','cortex','/d',?)", now)
	_, _ = db.Exec("INSERT INTO observations(session_id,type,title,content,project,scope,confidence,source,created_at,updated_at) VALUES('s-push','manual','T','Secret <private>canary_out</private> note','cortex','project',1,'manual',?,?)", now, now)

	var pushedContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/push" {
			var b domain.SyncBatch
			_ = json.NewDecoder(r.Body).Decode(&b)
			if len(b.Observations) > 0 {
				pushedContent = b.Observations[0].Content
			}
			_ = json.NewEncoder(w).Encode(domain.SyncResult{Accepted: countBatch(&b)})
			return
		}
		_ = json.NewEncoder(w).Encode(domain.SyncPage{})
	}))
	defer srv.Close()

	syncer, _ := NewRemoteSyncer(db, srv.URL, "secret", time.Second)
	if _, err := syncer.Sync(context.Background()); err != nil || pushedContent != "Secret [REDACTED] note" {
		t.Fatalf("Sync: err=%v, pushed=%q", err, pushedContent)
	}
}

func TestRemoteSync_PrivacyCodeNoninterference_CodeOnlyBatch(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	symDoc := "Doc with <private>canary_sym</private> and generic <private>unclosed"
	relReason := "Inferred relation <private>canary_rel</private> with syntax <private>unclosed"

	batch := &domain.SyncBatch{
		CodeSymbols: []domain.SyncCodeSymbol{
			{
				ID:         "sym-1",
				Project:    "cortex",
				FilePath:   "internal/test.go",
				LineNumber: 10,
				EndLine:    20,
				Kind:       "func",
				Name:       "TestFunc",
				DocSummary: symDoc,
				CreatedAt:  now,
				UpdatedAt:  now,
			},
		},
		CodeRelations: []domain.SyncCodeRelation{
			{
				ID:        1,
				Project:   "cortex",
				SourceID:  "sym-1",
				TargetID:  "sym-2",
				Relation:  "calls",
				Reasoning: relReason,
				CreatedAt: now,
			},
		},
	}

	clean, err := preflightSyncBatch(batch)
	if err != nil {
		t.Fatalf("preflightSyncBatch code-only batch failed: %v", err)
	}
	if clean == nil {
		t.Fatal("preflightSyncBatch returned nil clean batch")
	}

	if clean.CodeSymbols[0].DocSummary != symDoc {
		t.Errorf("CodeSymbols.DocSummary modified: got %q, want %q", clean.CodeSymbols[0].DocSummary, symDoc)
	}
	if clean.CodeRelations[0].Reasoning != relReason {
		t.Errorf("CodeRelations.Reasoning modified: got %q, want %q", clean.CodeRelations[0].Reasoning, relReason)
	}
	if batch.CodeSymbols[0].DocSummary != symDoc || batch.CodeRelations[0].Reasoning != relReason {
		t.Error("input batch was mutated")
	}

	// End-to-end push with code-only
	db := setupRemotePrivacyDB(t)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS code_symbols (
		id TEXT PRIMARY KEY, project TEXT NOT NULL, file_path TEXT NOT NULL,
		line_number INTEGER NOT NULL, end_line INTEGER, start_col INTEGER, end_col INTEGER,
		kind TEXT NOT NULL, name TEXT NOT NULL, package_name TEXT, parent_id TEXT,
		visibility TEXT, signature TEXT, doc_summary TEXT, parameters TEXT,
		return_type TEXT, complexity INTEGER, metadata TEXT, file_hash TEXT,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS code_relations (
		id INTEGER PRIMARY KEY AUTOINCREMENT, project TEXT NOT NULL,
		source_id TEXT NOT NULL, target_id TEXT NOT NULL, relation TEXT NOT NULL,
		confidence REAL NOT NULL, reasoning TEXT, created_at TEXT NOT NULL)`)

	nowStr := now.Format(time.RFC3339)
	_, err = db.Exec(`INSERT INTO code_symbols(id, project, file_path, line_number, end_line, kind, name, doc_summary, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"sym-1", "cortex", "internal/test.go", 10, 20, "func", "TestFunc", symDoc, nowStr, nowStr)
	if err != nil {
		t.Fatalf("insert code_symbols: %v", err)
	}
	_, err = db.Exec(`INSERT INTO code_relations(project, source_id, target_id, relation, confidence, reasoning, created_at) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		"cortex", "sym-1", "sym-2", "calls", 0.9, relReason, nowStr)
	if err != nil {
		t.Fatalf("insert code_relations: %v", err)
	}

	var pushedSymbols []domain.SyncCodeSymbol
	var pushedRelations []domain.SyncCodeRelation
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/push" {
			var b domain.SyncBatch
			_ = json.NewDecoder(r.Body).Decode(&b)
			if len(b.CodeSymbols) > 0 {
				pushedSymbols = append(pushedSymbols, b.CodeSymbols...)
			}
			if len(b.CodeRelations) > 0 {
				pushedRelations = append(pushedRelations, b.CodeRelations...)
			}
			_ = json.NewEncoder(w).Encode(domain.SyncResult{Accepted: countBatch(&b)})
			return
		}
		_ = json.NewEncoder(w).Encode(domain.SyncPage{})
	}))
	defer srv.Close()

	syncer, _ := NewRemoteSyncer(db, srv.URL, "secret", time.Second)
	res, err := syncer.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync push failed: %v", err)
	}
	if res.Pushed != 2 {
		t.Errorf("expected 2 items pushed, got %d", res.Pushed)
	}
	if len(pushedSymbols) != 1 || pushedSymbols[0].DocSummary != symDoc {
		t.Errorf("pushed DocSummary mismatch: got %+v", pushedSymbols)
	}
	if len(pushedRelations) != 1 || pushedRelations[0].Reasoning != relReason {
		t.Errorf("pushed Reasoning mismatch: got %+v", pushedRelations)
	}
}

func TestRemoteSync_PrivacyCodeNoninterference_MixedBatch(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	symDoc := "Doc <private>keep_code_canary</private> with <private>unclosed"
	relReason := "Rel <private>keep_rel_canary</private> with <private>unclosed"

	batch := &domain.SyncBatch{
		Sessions: []domain.SyncSession{
			{SyncID: "s-mix", Project: "cortex", Directory: "/app", Summary: "Sess <private>redact_sess</private>", StartedAt: now, UpdatedAt: now},
		},
		Observations: []domain.SyncObservation{
			{SyncID: "o-mix", SessionSyncID: "s-mix", Type: "manual", Title: "Obs <private>redact_title</private>", Content: "Obs <private>redact_content</private> body", Project: "cortex", Scope: "project", CreatedAt: now, UpdatedAt: now},
		},
		Prompts: []domain.SyncPrompt{
			{SyncID: "p-mix", SessionSyncID: "s-mix", Project: "cortex", Content: "Prompt <private>redact_prompt</private>", CreatedAt: now, UpdatedAt: now},
		},
		Edges: []domain.SyncEdge{
			{SyncID: "e-mix", FromSyncID: "o-mix", ToSyncID: "o-mix", Relation: "relates", Reasoning: "Edge <private>redact_edge</private>", Source: "manual", CreatedAt: now, UpdatedAt: now},
		},
		CodeSymbols: []domain.SyncCodeSymbol{
			{ID: "sym-mix", Project: "cortex", FilePath: "a.go", LineNumber: 1, EndLine: 5, Kind: "func", Name: "Fn", DocSummary: symDoc, CreatedAt: now, UpdatedAt: now},
		},
		CodeRelations: []domain.SyncCodeRelation{
			{ID: 10, Project: "cortex", SourceID: "sym-mix", TargetID: "sym-mix", Relation: "calls", Reasoning: relReason, CreatedAt: now},
		},
	}

	clean, err := preflightSyncBatch(batch)
	if err != nil {
		t.Fatalf("preflightSyncBatch failed: %v", err)
	}

	// Memory fields MUST be redacted
	if clean.Sessions[0].Summary != "Sess [REDACTED]" {
		t.Errorf("Sessions.Summary not redacted: %q", clean.Sessions[0].Summary)
	}
	if clean.Observations[0].Title != "Obs [REDACTED]" {
		t.Errorf("Observations.Title not redacted: %q", clean.Observations[0].Title)
	}
	if clean.Observations[0].Content != "Obs [REDACTED] body" {
		t.Errorf("Observations.Content not redacted: %q", clean.Observations[0].Content)
	}
	if clean.Prompts[0].Content != "Prompt [REDACTED]" {
		t.Errorf("Prompts.Content not redacted: %q", clean.Prompts[0].Content)
	}
	if clean.Edges[0].Reasoning != "Edge [REDACTED]" {
		t.Errorf("Edges.Reasoning not redacted: %q", clean.Edges[0].Reasoning)
	}

	// Code fields MUST remain byte-identical
	if clean.CodeSymbols[0].DocSummary != symDoc {
		t.Errorf("CodeSymbols.DocSummary not byte-identical: got %q, want %q", clean.CodeSymbols[0].DocSummary, symDoc)
	}
	if clean.CodeRelations[0].Reasoning != relReason {
		t.Errorf("CodeRelations.Reasoning not byte-identical: got %q, want %q", clean.CodeRelations[0].Reasoning, relReason)
	}

	// Test pull of mixed batch via RemoteSyncer
	db := setupRemotePrivacyDB(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/changes" {
			_ = json.NewEncoder(w).Encode(domain.SyncPage{
				SyncBatch: *batch,
				Cursor:    77,
			})
			return
		}
		var req domain.SyncBatch
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(domain.SyncResult{Accepted: countBatch(&req)})
	}))
	defer srv.Close()

	syncer, _ := NewRemoteSyncer(db, srv.URL, "secret", time.Second)
	res, err := syncer.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync pull of mixed batch failed: %v", err)
	}
	if res.Cursor != 77 {
		t.Errorf("expected cursor 77, got %d", res.Cursor)
	}

	var storedContent, storedSum string
	_ = db.QueryRow("SELECT content FROM observations WHERE sync_id='o-mix'").Scan(&storedContent)
	_ = db.QueryRow("SELECT summary FROM sessions WHERE id='s-mix'").Scan(&storedSum)
	if storedContent != "Obs [REDACTED] body" || storedSum != "Sess [REDACTED]" {
		t.Errorf("persisted mixed memory mismatch: content=%q, sum=%q", storedContent, storedSum)
	}
}

func TestRemoteSync_PrivacyMixedBatchAtomicRejection_ZeroPartialEmit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	symDoc := "Doc with <private>keep_code</private>"

	// Mixed batch with malformed memory observation (unclosed marker)
	batch := &domain.SyncBatch{
		Sessions: []domain.SyncSession{
			{SyncID: "s-bad", Project: "cortex", Directory: "/app", StartedAt: now, UpdatedAt: now},
		},
		Observations: []domain.SyncObservation{
			{SyncID: "o-bad", SessionSyncID: "s-bad", Type: "manual", Title: "T", Content: "Malformed <private>unclosed", Project: "cortex", Scope: "project"},
		},
		CodeSymbols: []domain.SyncCodeSymbol{
			{ID: "sym-1", Project: "cortex", FilePath: "b.go", LineNumber: 1, Kind: "func", Name: "B", DocSummary: symDoc},
		},
	}

	clean, err := preflightSyncBatch(batch)
	if err == nil || !errors.Is(err, privacy.ErrInvalidMarker) {
		t.Fatalf("expected ErrInvalidMarker on mixed batch, got: %v", err)
	}
	if clean != nil {
		t.Error("expected nil clean batch on rejection")
	}

	// 1. Pull rejection verification: zero effects on DB
	db := setupRemotePrivacyDB(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(domain.SyncPage{
			SyncBatch: *batch,
			Cursor:    88,
		})
	}))
	defer srv.Close()

	syncer, err := NewRemoteSyncer(db, srv.URL, "secret", time.Second)
	if err != nil {
		t.Fatalf("NewRemoteSyncer: %v", err)
	}
	_ = syncer.ensureState(context.Background())
	_, _ = db.Exec("INSERT INTO remote_sync_state(key, value) VALUES('cursor', '25')")

	if _, err := syncer.Sync(context.Background()); err == nil || !errors.Is(err, privacy.ErrInvalidMarker) {
		t.Fatalf("expected ErrInvalidMarker on sync pull, got: %v", err)
	}

	var cur string
	var obsCount, sessCount int
	_ = db.QueryRow("SELECT value FROM remote_sync_state WHERE key='cursor'").Scan(&cur)
	_ = db.QueryRow("SELECT count(*) FROM observations").Scan(&obsCount)
	_ = db.QueryRow("SELECT count(*) FROM sessions").Scan(&sessCount)
	if cur != "25" || obsCount != 0 || sessCount != 0 {
		t.Fatalf("state dirty on rejected pull: cur=%q, obs=%d, sess=%d", cur, obsCount, sessCount)
	}

	// 2. Push rejection verification: zero partial emit to server
	dbPush := setupRemotePrivacyDB(t)
	nowStr := now.Format(time.RFC3339)
	_, _ = dbPush.Exec("INSERT INTO sessions(id, project, directory, started_at) VALUES('s-push', 'cortex', '/d', ?)", nowStr)
	_, _ = dbPush.Exec("INSERT INTO observations(session_id, type, title, content, project, scope, confidence, source, created_at, updated_at) VALUES('s-push', 'manual', 'T', 'Bad <private>unclosed', 'cortex', 'project', 1, 'manual', ?, ?)", nowStr, nowStr)

	_, _ = dbPush.Exec(`CREATE TABLE IF NOT EXISTS code_symbols (
		id TEXT PRIMARY KEY, project TEXT NOT NULL, file_path TEXT NOT NULL,
		line_number INTEGER NOT NULL, end_line INTEGER, start_col INTEGER, end_col INTEGER,
		kind TEXT NOT NULL, name TEXT NOT NULL, package_name TEXT, parent_id TEXT,
		visibility TEXT, signature TEXT, doc_summary TEXT, parameters TEXT,
		return_type TEXT, complexity INTEGER, metadata TEXT, file_hash TEXT,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`)
	_, _ = dbPush.Exec(`INSERT INTO code_symbols(id, project, file_path, line_number, end_line, kind, name, doc_summary, created_at, updated_at) VALUES('sym-push', 'cortex', 'x.go', 1, 2, 'func', 'X', ?, ?, ?)`,
		symDoc, nowStr, nowStr)

	pushRequestsReceived := 0
	srvPush := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/push" {
			pushRequestsReceived++
			_ = json.NewEncoder(w).Encode(domain.SyncResult{})
			return
		}
		_ = json.NewEncoder(w).Encode(domain.SyncPage{})
	}))
	defer srvPush.Close()

	syncerPush, _ := NewRemoteSyncer(dbPush, srvPush.URL, "secret", time.Second)
	if _, err := syncerPush.Sync(context.Background()); err == nil || !errors.Is(err, privacy.ErrInvalidMarker) {
		t.Fatalf("expected ErrInvalidMarker on sync push with malformed memory, got: %v", err)
	}
	if pushRequestsReceived != 0 {
		t.Fatalf("partial emit occurred on rejected push: received %d push requests", pushRequestsReceived)
	}
}

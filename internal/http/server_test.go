package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/migration"
	graphstore "github.com/lleontor705/cortex/v2/internal/store/graph"
	"github.com/lleontor705/cortex/v2/internal/store/prompt"
	scoringstore "github.com/lleontor705/cortex/v2/internal/store/scoring"
	"github.com/lleontor705/cortex/v2/internal/store/search"
	"github.com/lleontor705/cortex/v2/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
	"github.com/lleontor705/cortex/v2/testutil"
)

func setupTestServer(t *testing.T) *Server {
	return setupTestServerWithOptions(t, Options{})
}

func setupTestServerWithOptions(t *testing.T, opts Options) *Server {
	t.Helper()

	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1, Name: "init",
		UpSQL: `
			CREATE TABLE sessions (id TEXT PRIMARY KEY, project TEXT NOT NULL, directory TEXT NOT NULL,
				started_at TEXT NOT NULL DEFAULT (datetime('now')), ended_at TEXT, summary TEXT);
			CREATE TABLE observations (id INTEGER PRIMARY KEY AUTOINCREMENT, sync_id TEXT,
				session_id TEXT NOT NULL, type TEXT NOT NULL, title TEXT NOT NULL, content TEXT NOT NULL,
				tool_name TEXT, project TEXT, scope TEXT NOT NULL DEFAULT 'project', topic_key TEXT,
				normalized_hash TEXT, revision_count INTEGER NOT NULL DEFAULT 1,
				duplicate_count INTEGER NOT NULL DEFAULT 1, last_seen_at TEXT,
				confidence REAL NOT NULL DEFAULT 1.0,
				source TEXT NOT NULL DEFAULT 'manual',
				tags TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')), deleted_at TEXT,
				FOREIGN KEY (session_id) REFERENCES sessions(id));
			CREATE TABLE user_prompts (id INTEGER PRIMARY KEY AUTOINCREMENT, sync_id TEXT,
				session_id TEXT NOT NULL, content TEXT NOT NULL, project TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')));`,
		DownSQL: `DROP TABLE IF EXISTS user_prompts; DROP TABLE IF EXISTS observations; DROP TABLE IF EXISTS sessions;`,
	})
	registry.Register(migration.Migration{
		Version: 2, Name: "fts",
		UpSQL: `
			CREATE VIRTUAL TABLE observations_fts USING fts5(title, content, type, project, content=observations, content_rowid=id);
			CREATE TRIGGER observations_fts_insert AFTER INSERT ON observations BEGIN
				INSERT INTO observations_fts(rowid, title, content, type, project) VALUES (new.id, new.title, new.content, new.type, new.project);
			END;`,
		DownSQL: `DROP TRIGGER IF EXISTS observations_fts_insert; DROP TABLE IF EXISTS observations_fts;`,
	})
	registry.Register(migration.Migration{
		Version: 3, Name: "graph",
		UpSQL: `CREATE TABLE edges (id INTEGER PRIMARY KEY AUTOINCREMENT, from_obs_id INTEGER NOT NULL,
			to_obs_id INTEGER NOT NULL, relation_type TEXT NOT NULL, weight REAL NOT NULL DEFAULT 1.0,
			confidence REAL NOT NULL DEFAULT 1.0, source TEXT, reasoning TEXT,
			valid_from TEXT, invalid_at TEXT,
			evolution_id INTEGER, evolution_type TEXT NOT NULL DEFAULT 'original',
			fact_state TEXT NOT NULL DEFAULT 'current', change_reason TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (from_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
			FOREIGN KEY (to_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
			UNIQUE(from_obs_id, to_obs_id, relation_type));`,
		DownSQL: `DROP TABLE IF EXISTS edges;`,
	})
	registry.Register(migration.Migration{
		Version: 4, Name: "scoring",
		UpSQL: `CREATE TABLE importance_scores (observation_id INTEGER PRIMARY KEY, score REAL NOT NULL DEFAULT 0.0,
			access_count INTEGER NOT NULL DEFAULT 0, last_accessed DATETIME,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (observation_id) REFERENCES observations(id) ON DELETE CASCADE);
			CREATE TRIGGER importance_init AFTER INSERT ON observations BEGIN
				INSERT INTO importance_scores (observation_id, score, updated_at) VALUES (new.id, 0.0, CURRENT_TIMESTAMP);
			END;`,
		DownSQL: `DROP TRIGGER IF EXISTS importance_init; DROP TABLE IF EXISTS importance_scores;`,
	})
	registry.Register(migration.Migration{
		Version: 5, Name: "temporal_snapshots",
		UpSQL: `CREATE TABLE temporal_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			snapshot_key TEXT NOT NULL,
			timestamp DATETIME NOT NULL,
			description TEXT,
			observation_count INTEGER NOT NULL DEFAULT 0,
			edge_count INTEGER NOT NULL DEFAULT 0,
			root_observation_id INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		DownSQL: `DROP TABLE IF EXISTS temporal_snapshots;`,
	})

	testDB := testutil.NewTestDBWithMigrations(t, registry)
	db := testDB.DB()

	deps := &Deps{
		Observations:      sqlitestore.NewStore(db),
		Sessions:          session.NewStore(db),
		Search:            search.NewStore(db),
		Prompts:           prompt.NewStore(db),
		Graph:             graphstore.NewStore(db),
		Scoring:           scoringstore.NewStore(db),
		TemporalSnapshots: sqlitestore.NewTemporalSnapshotRepository(db),
	}

	return NewServer(":0", deps, opts)
}

func TestHealthEndpoint(t *testing.T) {
	srv := setupTestServer(t)
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestObservationCRUD(t *testing.T) {
	srv := setupTestServer(t)
	handler := srv.httpServer.Handler
	ctx := context.Background()

	// Create session first
	srv.deps.Sessions.Create(ctx, &domain.Session{ID: "s1", Project: "demo", Directory: "."}) //nolint:errcheck

	// Create observation
	body, _ := json.Marshal(domain.Observation{
		SessionID: "s1", Title: "Test obs", Content: "Test content",
		Type: "manual", Project: "demo", Scope: "project",
	})
	req := httptest.NewRequest("POST", "/api/observations", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created domain.Observation
	json.Unmarshal(w.Body.Bytes(), &created) //nolint:errcheck
	if created.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	// List observations
	req = httptest.NewRequest("GET", "/api/observations?project=demo", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}

	// Get observation
	req = httptest.NewRequest("GET", "/api/observations/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("get: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Delete observation
	req = httptest.NewRequest("DELETE", "/api/observations/1", nil)
	req.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("delete: expected 200, got %d", w.Code)
	}
}

func TestSearchEndpoint(t *testing.T) {
	srv := setupTestServer(t)
	handler := srv.httpServer.Handler
	ctx := context.Background()

	srv.deps.Sessions.Create(ctx, &domain.Session{ID: "s1", Project: "demo", Directory: "."}) //nolint:errcheck
	srv.deps.Observations.Save(ctx, &domain.Observation{                                      //nolint:errcheck
		SessionID: "s1", Title: "JWT auth middleware", Content: "Switched to JWT",
		Type: "decision", Project: "demo", Scope: "project",
	})

	req := httptest.NewRequest("GET", "/api/search?q=JWT&project=demo", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("search: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"score_breakdown"`) {
		t.Fatalf("search response missing score_breakdown: %s", w.Body.String())
	}
}

func TestObservationRevisionsEndpoint(t *testing.T) {
	srv := setupTestServer(t)
	handler := srv.httpServer.Handler
	ctx := context.Background()

	srv.deps.Sessions.Create(ctx, &domain.Session{ID: "s1", Project: "demo", Directory: "."}) //nolint:errcheck
	obs := &domain.Observation{
		SessionID: "s1", Title: "Original Title", Content: "Original content",
		Type: "manual", Project: "demo", Scope: "project",
	}
	srv.deps.Observations.Save(ctx, obs) //nolint:errcheck
	obs.Title = "Updated Title"
	obs.Content = "Updated content"
	srv.deps.Observations.Update(ctx, obs) //nolint:errcheck

	req := httptest.NewRequest("GET", "/api/observations/1/revisions", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("revisions: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"reason":"update"`) {
		t.Fatalf("revisions response missing update reason: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"title":"Original Title"`) {
		t.Fatalf("revisions response missing original title: %s", w.Body.String())
	}
}

func TestAuthMiddlewareProtectsAPI(t *testing.T) {
	srv := setupTestServerWithOptions(t, Options{AuthToken: "secret-token"})
	handler := srv.httpServer.Handler

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("health should remain public, got %d", w.Code)
	}

	req = httptest.NewRequest("GET", "/api/observations", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("expected 401 without token, got %d", w.Code)
	}

	req = httptest.NewRequest("GET", "/api/observations", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 with bearer token, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthMiddleware_XAPIKey(t *testing.T) {
	srv := setupTestServerWithOptions(t, Options{AuthToken: "secret-token"})
	handler := srv.httpServer.Handler

	req := httptest.NewRequest("GET", "/api/observations", nil)
	req.Header.Set("X-API-Key", "secret-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 with X-API-Key, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleArchive_Success(t *testing.T) {
	srv := setupTestServer(t)
	handler := srv.httpServer.Handler
	ctx := context.Background()

	// Create session and observation
	srv.deps.Sessions.Create(ctx, &domain.Session{ID: "s1", Project: "demo", Directory: "."}) //nolint:errcheck
	srv.deps.Observations.Save(ctx, &domain.Observation{                                      //nolint:errcheck
		SessionID: "s1", Title: "To archive", Content: "Will be archived",
		Type: "manual", Project: "demo", Scope: "project",
	})

	req := httptest.NewRequest("POST", "/api/observations/1/archive", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("archive: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"status":"archived"`) {
		t.Fatalf("archive response missing status: %s", w.Body.String())
	}
}

func TestHandleArchive_NotFound(t *testing.T) {
	srv := setupTestServer(t)
	handler := srv.httpServer.Handler

	req := httptest.NewRequest("POST", "/api/observations/9999/archive", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("archive not found: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSearchHybrid_Success(t *testing.T) {
	srv := setupTestServer(t)
	handler := srv.httpServer.Handler
	ctx := context.Background()

	srv.deps.Sessions.Create(ctx, &domain.Session{ID: "s1", Project: "demo", Directory: "."}) //nolint:errcheck
	srv.deps.Observations.Save(ctx, &domain.Observation{                                      //nolint:errcheck
		SessionID: "s1", Title: "Hybrid search test", Content: "Vector fusion content",
		Type: "decision", Project: "demo", Scope: "project",
	})

	req := httptest.NewRequest("GET", "/api/search/hybrid?q=fusion&project=demo", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("hybrid search: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSearchHybrid_MissingQuery(t *testing.T) {
	srv := setupTestServer(t)
	handler := srv.httpServer.Handler

	req := httptest.NewRequest("GET", "/api/search/hybrid", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("hybrid search missing query: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "'q' is required") {
		t.Fatalf("expected query required error, got: %s", w.Body.String())
	}
}

func TestAuthMiddleware_InvalidTokens(t *testing.T) {
	srv := setupTestServerWithOptions(t, Options{AuthToken: "secret-token"})
	handler := srv.httpServer.Handler

	tests := []struct {
		name   string
		header string
		value  string
	}{
		{"bearer without token", "Authorization", "Bearer "},
		{"bearer wrong token", "Authorization", "Bearer wrong-token"},
		{"wrong api key", "X-API-Key", "wrong-token"},
		{"empty api key", "X-API-Key", ""},
		{"bearer only keyword", "Authorization", "Bearer"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/observations", nil)
			if tc.value != "" {
				req.Header.Set(tc.header, tc.value)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != 401 {
				t.Fatalf("expected 401 for %s, got %d", tc.name, w.Code)
			}
		})
	}
}

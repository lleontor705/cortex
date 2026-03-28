package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
	graphstore "github.com/lleontor705/cortex/internal/store/graph"
	"github.com/lleontor705/cortex/internal/store/prompt"
	scoringstore "github.com/lleontor705/cortex/internal/store/scoring"
	"github.com/lleontor705/cortex/internal/store/search"
	"github.com/lleontor705/cortex/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/lleontor705/cortex/testutil"
)

// setupTestStores creates an in-memory DB with all migrations and returns a Stores bundle.
func setupTestStores(t *testing.T) *Stores {
	t.Helper()

	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1,
		Name:    "init",
		UpSQL: `
			CREATE TABLE sessions (
				id TEXT PRIMARY KEY, project TEXT NOT NULL, directory TEXT NOT NULL,
				started_at TEXT NOT NULL DEFAULT (datetime('now')), ended_at TEXT, summary TEXT
			);
			CREATE TABLE observations (
				id INTEGER PRIMARY KEY AUTOINCREMENT, sync_id TEXT,
				session_id TEXT NOT NULL, type TEXT NOT NULL, title TEXT NOT NULL,
				content TEXT NOT NULL, tool_name TEXT, project TEXT,
				scope TEXT NOT NULL DEFAULT 'project', topic_key TEXT,
				normalized_hash TEXT, revision_count INTEGER NOT NULL DEFAULT 1,
				duplicate_count INTEGER NOT NULL DEFAULT 1, last_seen_at TEXT,
				confidence REAL NOT NULL DEFAULT 1.0,
				source TEXT NOT NULL DEFAULT 'manual',
				tags TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')), deleted_at TEXT,
				FOREIGN KEY (session_id) REFERENCES sessions(id)
			);
			CREATE TABLE user_prompts (
				id INTEGER PRIMARY KEY AUTOINCREMENT, sync_id TEXT,
				session_id TEXT NOT NULL, content TEXT NOT NULL, project TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now'))
			);`,
		DownSQL: `DROP TABLE IF EXISTS user_prompts; DROP TABLE IF EXISTS observations; DROP TABLE IF EXISTS sessions;`,
	})
	registry.Register(migration.Migration{
		Version: 2,
		Name:    "fts",
		UpSQL: `
			CREATE VIRTUAL TABLE observations_fts USING fts5(title, content, type, project, content=observations, content_rowid=id);
			CREATE TRIGGER observations_fts_insert AFTER INSERT ON observations BEGIN
				INSERT INTO observations_fts(rowid, title, content, type, project)
				VALUES (new.id, new.title, new.content, new.type, new.project);
			END;`,
		DownSQL: `DROP TRIGGER IF EXISTS observations_fts_insert; DROP TABLE IF EXISTS observations_fts;`,
	})
	registry.Register(migration.Migration{
		Version: 3,
		Name:    "graph",
		UpSQL: `
			CREATE TABLE edges (
				id INTEGER PRIMARY KEY AUTOINCREMENT, from_obs_id INTEGER NOT NULL,
				to_obs_id INTEGER NOT NULL, relation_type TEXT NOT NULL,
				weight REAL NOT NULL DEFAULT 1.0,
				confidence REAL NOT NULL DEFAULT 1.0,
				source TEXT, reasoning TEXT, valid_from TEXT, invalid_at TEXT,
				created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (from_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
				FOREIGN KEY (to_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
				UNIQUE(from_obs_id, to_obs_id, relation_type)
			);`,
		DownSQL: `DROP TABLE IF EXISTS edges;`,
	})
	registry.Register(migration.Migration{
		Version: 4,
		Name:    "scoring",
		UpSQL: `
			CREATE TABLE importance_scores (
				observation_id INTEGER PRIMARY KEY, score REAL NOT NULL DEFAULT 0.0,
				access_count INTEGER NOT NULL DEFAULT 0, last_accessed DATETIME,
				updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (observation_id) REFERENCES observations(id) ON DELETE CASCADE
			);
			CREATE TRIGGER importance_init AFTER INSERT ON observations BEGIN
				INSERT INTO importance_scores (observation_id, score, updated_at)
				VALUES (new.id, 0.0, CURRENT_TIMESTAMP);
			END;`,
		DownSQL: `DROP TRIGGER IF EXISTS importance_init; DROP TABLE IF EXISTS importance_scores;`,
	})

	testDB := testutil.NewTestDBWithMigrations(t, registry)
	db := testDB.DB()

	return &Stores{
		Observations: sqlitestore.NewStore(db),
		Sessions:     session.NewStore(db),
		Search:       search.NewStore(db),
		Prompts:      prompt.NewStore(db),
		Graph:        graphstore.NewStore(db),
		Scoring:      scoringstore.NewStore(db),
		Vectors:      sqlitestore.NewVectorStore(db),
	}
}

// createSession creates a test session.
func createSession(t *testing.T, stores *Stores, id, project string) {
	t.Helper()
	err := stores.Sessions.Create(context.Background(), &domain.Session{
		ID: id, Project: project, Directory: ".",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
}

// saveObs creates a test observation and returns it.
func saveObs(t *testing.T, stores *Stores, title, project, sessionID string) *domain.Observation {
	t.Helper()
	obs := &domain.Observation{
		SessionID: sessionID, Title: title, Content: "Content for " + title,
		Type: domain.TypeManual, Project: project, Scope: domain.ScopeProject,
	}
	if err := stores.Observations.Save(context.Background(), obs); err != nil {
		t.Fatalf("save obs: %v", err)
	}
	return obs
}

// callTool is a helper to invoke a tool handler with arguments.
func callTool(t *testing.T, handler server.ToolHandlerFunc, args map[string]interface{}) *mcp.CallToolResult {
	t.Helper()

	rawArgs, _ := json.Marshal(args)
	var params struct {
		Arguments map[string]interface{} `json:"arguments"`
	}
	params.Arguments = args
	rawParams, _ := json.Marshal(params)

	req := mcp.CallToolRequest{}
	_ = json.Unmarshal(rawParams, &req.Params)
	// Manually set arguments since the struct may differ
	_ = rawArgs // suppress unused

	// Build request properly
	req2 := mcp.CallToolRequest{}
	req2.Params.Arguments = args

	result, err := handler(context.Background(), req2)
	if err != nil {
		t.Fatalf("tool handler error: %v", err)
	}
	return result
}

// resultText extracts text content from a tool result.
func resultText(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// ─── Engram-compatible tool tests ───────────────────────────────────────────

func TestHandleSave(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")

	handler := handleSave(stores)
	result := callTool(t, handler, map[string]interface{}{
		"title":      "JWT auth",
		"content":    "Switched to JWT",
		"type":       "decision",
		"project":    "demo",
		"session_id": "s1",
	})

	text := resultText(result)
	if !strings.Contains(text, "Memory saved") {
		t.Errorf("expected 'Memory saved', got %q", text)
	}
}

func TestHandleSearch(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	saveObs(t, stores, "JWT auth middleware", "demo", "s1")

	handler := handleSearch(stores)
	result := callTool(t, handler, map[string]interface{}{
		"query":   "JWT",
		"project": "demo",
	})

	text := resultText(result)
	if !strings.Contains(text, "JWT auth middleware") {
		t.Errorf("expected to find 'JWT auth middleware', got %q", text)
	}
}

func TestHandleDelete(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	obs := saveObs(t, stores, "To delete", "demo", "s1")

	handler := handleDelete(stores)
	result := callTool(t, handler, map[string]interface{}{
		"id": float64(obs.ID),
	})

	text := resultText(result)
	if !strings.Contains(text, "deleted") && !strings.Contains(text, "Deleted") {
		t.Errorf("expected deletion confirmation, got %q", text)
	}
}

func TestHandleSave_MissingTitle(t *testing.T) {
	stores := setupTestStores(t)

	handler := handleSave(stores)
	result := callTool(t, handler, map[string]interface{}{
		"content": "some content",
	})

	text := resultText(result)
	if !strings.Contains(strings.ToLower(text), "title") {
		t.Errorf("expected error about title, got %q", text)
	}
}

// ─── Cortex-exclusive tool tests ────────────────────────────────────────────

func TestHandleRelate(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	obs1 := saveObs(t, stores, "Obs 1", "demo", "s1")
	obs2 := saveObs(t, stores, "Obs 2", "demo", "s1")

	handler := handleRelate(stores)
	result := callTool(t, handler, map[string]interface{}{
		"from_id":       float64(obs1.ID),
		"to_id":         float64(obs2.ID),
		"relation_type": "references",
	})

	text := resultText(result)
	if !strings.Contains(text, "Relationship created") {
		t.Errorf("expected 'Relationship created', got %q", text)
	}
}

func TestHandleGraph(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	obs1 := saveObs(t, stores, "Graph Obs 1", "demo", "s1")
	obs2 := saveObs(t, stores, "Graph Obs 2", "demo", "s1")

	// Create edge
	stores.Graph.CreateEdge(context.Background(), &domain.Edge{ //nolint:errcheck
		FromObsID: obs1.ID, ToObsID: obs2.ID, RelationType: "references", Weight: 1.0,
	})

	handler := handleGraph(stores)
	result := callTool(t, handler, map[string]interface{}{
		"observation_id": float64(obs1.ID),
		"depth":          float64(1),
	})

	text := resultText(result)
	if !strings.Contains(text, "Graph Obs 2") {
		t.Errorf("expected to find 'Graph Obs 2', got %q", text)
	}
}

func TestHandleScore(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	obs := saveObs(t, stores, "Score test", "demo", "s1")

	handler := handleScore(stores)
	result := callTool(t, handler, map[string]interface{}{
		"observation_id": float64(obs.ID),
	})

	text := resultText(result)
	if !strings.Contains(text, "score:") {
		t.Errorf("expected score info, got %q", text)
	}
}

func TestHandleArchive(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	obs := saveObs(t, stores, "To archive", "demo", "s1")

	handler := handleArchive(stores)
	result := callTool(t, handler, map[string]interface{}{
		"observation_id": float64(obs.ID),
	})

	text := resultText(result)
	if !strings.Contains(text, "archived") {
		t.Errorf("expected 'archived', got %q", text)
	}

	// Verify soft-deleted
	_, err := stores.Observations.GetByID(context.Background(), obs.ID)
	if err == nil {
		t.Error("expected observation to be soft-deleted")
	}
}

func TestHandleSearchHybrid(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	saveObs(t, stores, "Hybrid test observation", "demo", "s1")

	handler := handleSearchHybrid(stores)
	result := callTool(t, handler, map[string]interface{}{
		"query":   "Hybrid test",
		"project": "demo",
	})

	text := resultText(result)
	if !strings.Contains(text, "Hybrid test observation") {
		t.Errorf("expected to find observation, got %q", text)
	}
}

func TestHandleRelate_MissingParams(t *testing.T) {
	stores := setupTestStores(t)

	handler := handleRelate(stores)
	result := callTool(t, handler, map[string]interface{}{})

	text := resultText(result)
	if !strings.Contains(text, "required") {
		t.Errorf("expected error about required params, got %q", text)
	}
}

// Ensure unused imports don't cause issues.
var _ = (*sql.DB)(nil)

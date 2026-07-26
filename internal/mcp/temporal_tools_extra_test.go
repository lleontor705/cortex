package mcp

// Behavioral characterization tests for the temporal / observability MCP
// handlers (registerTemporalTools / TemporalToolsHandler) and for temporal-tool
// registration gating.
//
// This file is part of SDD change coverage-70-and-lint (task 1.3 G2). It only
// authors tests in this reserved file; it does NOT modify production code nor
// any other test file. Oracles used: result.IsError, decoded JSON payloads,
// stable semantic fragments, database effects (via the store layer), and server
// tool-registry introspection (GetTool).
//
// Schema is extended locally through a test fixture (setupStoresWithObservability)
// because the shared setupTestStores helper leaves Metrics / QualityMetrics nil
// and does not create the metrics / quality_metrics tables, which the temporal
// handlers require.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain/observability"
	"github.com/lleontor705/cortex/internal/domain/temporal"
	"github.com/lleontor705/cortex/internal/migration"
	graphstore "github.com/lleontor705/cortex/internal/store/graph"
	"github.com/lleontor705/cortex/internal/store/prompt"
	scoringstore "github.com/lleontor705/cortex/internal/store/scoring"
	"github.com/lleontor705/cortex/internal/store/search"
	"github.com/lleontor705/cortex/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
	"github.com/lleontor705/cortex/testutil"
	"github.com/mark3labs/mcp-go/mcp"
)

// temporalToolNames is the canonical list of tools registered by
// registerTemporalTools, in registration order.
var temporalToolNames = []string{
	"cortex_temporal_create_edge",
	"cortex_temporal_get_edges",
	"cortex_temporal_get_relevant",
	"cortex_temporal_create_snapshot",
	"cortex_temporal_record_operation",
	"cortex_temporal_evaluate_quality",
	"cortex_temporal_system_metrics",
	"cortex_temporal_health_check",
	"cortex_temporal_evolution_path",
	"cortex_temporal_fact_state",
}

// setupStoresWithObservability builds an in-memory Stores bundle that, unlike
// setupTestStores, also creates the metrics and quality_metrics tables and
// wires up MetricsRepository / QualityMetricsRepository. This is the minimum
// fixture required to exercise the temporal / observability handlers.
func setupStoresWithObservability(t *testing.T) *Stores {
	t.Helper()

	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1, Name: "init",
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
		Version: 2, Name: "fts",
		UpSQL: `
			CREATE VIRTUAL TABLE observations_fts USING fts5(title, content, type, project, content=observations, content_rowid=id);
			CREATE TRIGGER observations_fts_insert AFTER INSERT ON observations BEGIN
				INSERT INTO observations_fts(rowid, title, content, type, project)
				VALUES (new.id, new.title, new.content, new.type, new.project);
			END;`,
		DownSQL: `DROP TRIGGER IF EXISTS observations_fts_insert; DROP TABLE IF EXISTS observations_fts;`,
	})
	registry.Register(migration.Migration{
		Version: 3, Name: "graph",
		UpSQL: `
			CREATE TABLE edges (
				id INTEGER PRIMARY KEY AUTOINCREMENT, from_obs_id INTEGER NOT NULL,
				to_obs_id INTEGER NOT NULL, relation_type TEXT NOT NULL,
				weight REAL NOT NULL DEFAULT 1.0,
				confidence REAL NOT NULL DEFAULT 1.0,
				source TEXT, reasoning TEXT, valid_from TEXT, invalid_at TEXT,
				evolution_id INTEGER,
				evolution_type TEXT NOT NULL DEFAULT 'original',
				fact_state TEXT NOT NULL DEFAULT 'current',
				change_reason TEXT,
				created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (from_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
				FOREIGN KEY (to_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
				UNIQUE(from_obs_id, to_obs_id, relation_type)
			);`,
		DownSQL: `DROP TABLE IF EXISTS edges;`,
	})
	registry.Register(migration.Migration{
		Version: 4, Name: "scoring",
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
	registry.Register(migration.Migration{
		Version: 5, Name: "temporal_snapshots",
		UpSQL: `
			CREATE TABLE temporal_snapshots (
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
	// Version 6 mirrors migrations/009_temporal_observability.sql for the
	// metrics and quality_metrics tables only (edges temporal columns already
	// exist in version 3 above; snapshots already exist in version 5).
	registry.Register(migration.Migration{
		Version: 6, Name: "observability_tables",
		UpSQL: `
			CREATE TABLE metrics (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				session_id TEXT,
				operation_type TEXT NOT NULL,
				duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
				result_count INTEGER NOT NULL DEFAULT 0,
				success BOOLEAN NOT NULL,
				error TEXT,
				memory_usage_bytes INTEGER NOT NULL DEFAULT 0 CHECK (memory_usage_bytes >= 0),
				timestamp DATETIME NOT NULL,
				observation_count INTEGER NOT NULL DEFAULT 0,
				edge_count INTEGER NOT NULL DEFAULT 0,
				query_complexity REAL NOT NULL DEFAULT 0.0 CHECK (query_complexity >= 0.0 AND query_complexity <= 1.0),
				confidence_score REAL NOT NULL DEFAULT 0.0 CHECK (confidence_score >= 0.0 AND confidence_score <= 1.0),
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);
			CREATE TABLE quality_metrics (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				session_id TEXT,
				evaluation_type TEXT NOT NULL,
				score REAL NOT NULL CHECK (score >= 0.0 AND score <= 1.0),
				total_queries INTEGER NOT NULL DEFAULT 0,
				successful_retrievals INTEGER NOT NULL DEFAULT 0,
				average_latency_ms REAL NOT NULL DEFAULT 0.0 CHECK (average_latency_ms >= 0.0),
				average_relevance REAL NOT NULL DEFAULT 0.0,
				temporal_accuracy REAL NOT NULL DEFAULT 0.0,
				knowledge_coverage REAL NOT NULL DEFAULT 0.0,
				evaluated_at DATETIME NOT NULL,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);`,
		DownSQL: `DROP TABLE IF EXISTS quality_metrics; DROP TABLE IF EXISTS metrics;`,
	})

	testDB := testutil.NewTestDBWithMigrations(t, registry)
	db := testDB.DB()

	return &Stores{
		Observations:      sqlitestore.NewStore(db),
		Sessions:          session.NewStore(db),
		Search:            search.NewStore(db),
		Prompts:           prompt.NewStore(db),
		Graph:             graphstore.NewStore(db),
		Scoring:           scoringstore.NewStore(db),
		Vectors:           sqlitestore.NewVectorStore(db),
		TemporalSnapshots: sqlitestore.NewTemporalSnapshotRepository(db),
		Metrics:           sqlitestore.NewMetricsRepository(db),
		QualityMetrics:    sqlitestore.NewQualityMetricsRepository(db),
	}
}

// setupTemporalHandler wires the temporal + observability services onto an
// observability-aware Stores bundle and returns the handler together with the
// stores (for database-effect assertions).
func setupTemporalHandler(t *testing.T) (*TemporalToolsHandler, *Stores) {
	t.Helper()
	stores := setupStoresWithObservability(t)
	temporalSvc := temporal.NewTemporalService(stores.Graph, stores.Observations, stores.TemporalSnapshots, stores.Metrics)
	observabilitySvc := observability.NewObservabilityService(stores.Metrics, stores.QualityMetrics, stores.TemporalSnapshots, stores.Graph, stores.Observations)
	return NewTemporalToolsHandler(temporalSvc, observabilitySvc), stores
}

// callTemporal adapts a pointer-receiver temporal handler method to the
// by-value server.ToolHandlerFunc signature expected by the shared callTool
// helper. This mirrors the wrapping performed inside registerTemporalTools.
func callTemporal(t *testing.T, fn func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]interface{}) *mcp.CallToolResult {
	t.Helper()
	return callTool(t, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return fn(ctx, &req)
	}, args)
}

// seedTwoObservations creates a session and two observations and returns their
// IDs. Temporal edges require both endpoints to exist (FK constraint).
func seedTwoObservations(t *testing.T, stores *Stores, project string) (int64, int64) {
	t.Helper()
	createSession(t, stores, "s1", project)
	a := saveObs(t, stores, "Alpha Node", project, "s1")
	b := saveObs(t, stores, "Beta Node", project, "s1")
	return a.ID, b.ID
}

// --- temporal_create_edge ------------------------------------------------

func TestTemporalCreateEdge_Success(t *testing.T) {
	h, stores := setupTemporalHandler(t)
	fromID, toID := seedTwoObservations(t, stores, "demo")

	result := callTemporal(t, h.CreateTemporalEdge, map[string]interface{}{
		"from_obs_id":    float64(fromID),
		"to_obs_id":      float64(toID),
		"relation_type":  "references",
		"weight":         float64(1.0),
		"confidence":     float64(0.9),
		"valid_from":     "2020-01-01T00:00:00Z",
		"evolution_type": "original",
		"fact_state":     "current",
	})
	text := resultText(result)

	if !strings.Contains(text, "Temporal edge created successfully with ID") {
		t.Fatalf("expected success message, got %q", text)
	}

	// Database effect: the edge was persisted and is reachable from the source.
	edges, err := stores.Graph.GetEdgesForObservation(context.Background(), fromID)
	if err != nil {
		t.Fatalf("get edges: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge persisted, got %d", len(edges))
	}
}

func TestTemporalCreateEdge_MalformedValidFrom(t *testing.T) {
	h, stores := setupTemporalHandler(t)
	fromID, toID := seedTwoObservations(t, stores, "demo")

	result := callTemporal(t, h.CreateTemporalEdge, map[string]interface{}{
		"from_obs_id":   float64(fromID),
		"to_obs_id":     float64(toID),
		"relation_type": "references",
		"valid_from":    "not-an-rfc3339-date",
	})
	if !result.IsError {
		t.Fatalf("expected IsError for malformed valid_from, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "Invalid valid_from format") {
		t.Errorf("expected 'Invalid valid_from format', got %q", resultText(result))
	}
}

func TestTemporalCreateEdge_MalformedBinding(t *testing.T) {
	h, stores := setupTemporalHandler(t)
	fromID, _ := seedTwoObservations(t, stores, "demo")

	// A string where an int64 is expected cannot be bound -> BindArguments error.
	result := callTemporal(t, h.CreateTemporalEdge, map[string]interface{}{
		"from_obs_id":   "not-a-number",
		"to_obs_id":     float64(fromID),
		"relation_type": "references",
	})
	if !result.IsError {
		t.Fatalf("expected IsError for malformed binding, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "Invalid parameters") {
		t.Errorf("expected 'Invalid parameters', got %q", resultText(result))
	}
}

// --- temporal_get_edges --------------------------------------------------

func TestTemporalGetEdges_Success(t *testing.T) {
	h, stores := setupTemporalHandler(t)
	fromID, toID := seedTwoObservations(t, stores, "demo")

	// Create an edge valid from the distant past so it is valid "now".
	if _, err := callTemporalWithErr(t, h.CreateTemporalEdge, map[string]interface{}{
		"from_obs_id":   float64(fromID),
		"to_obs_id":     float64(toID),
		"relation_type": "references",
		"valid_from":    "2020-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	result := callTemporal(t, h.GetTemporalEdges, map[string]interface{}{
		"observation_id": float64(fromID),
		"at":             "2025-06-15T00:00:00Z",
	})

	var edges []map[string]interface{}
	if err := json.Unmarshal([]byte(resultText(result)), &edges); err != nil {
		t.Fatalf("expected JSON edge array, got error %v (text: %q)", err, resultText(result))
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 valid edge, got %d", len(edges))
	}
	if rt, _ := edges[0]["relation_type"].(string); rt != "references" {
		t.Errorf("expected relation_type 'references', got %#v", edges[0]["relation_type"])
	}
}

func TestTemporalGetEdges_MalformedAt(t *testing.T) {
	h, stores := setupTemporalHandler(t)
	fromID, _ := seedTwoObservations(t, stores, "demo")

	result := callTemporal(t, h.GetTemporalEdges, map[string]interface{}{
		"observation_id": float64(fromID),
		"at":             "bad-date",
	})
	if !result.IsError {
		t.Fatalf("expected IsError for malformed at, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "Invalid 'at' format") {
		t.Errorf("expected \"Invalid 'at' format\", got %q", resultText(result))
	}
}

// --- temporal_get_relevant -----------------------------------------------

func TestTemporalGetRelevant_Success(t *testing.T) {
	h, stores := setupTemporalHandler(t)
	fromID, toID := seedTwoObservations(t, stores, "demo")

	if _, err := callTemporalWithErr(t, h.CreateTemporalEdge, map[string]interface{}{
		"from_obs_id":   float64(fromID),
		"to_obs_id":     float64(toID),
		"relation_type": "references",
		"valid_from":    "2020-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	result := callTemporal(t, h.GetTemporalRelevant, map[string]interface{}{
		"observation_id": float64(fromID),
		"at":             "2025-06-15T00:00:00Z",
		"depth":          float64(1),
	})

	var observations []map[string]interface{}
	if err := json.Unmarshal([]byte(resultText(result)), &observations); err != nil {
		t.Fatalf("expected JSON observation array, got error %v (text: %q)", err, resultText(result))
	}
	if len(observations) < 2 {
		t.Fatalf("expected at least 2 relevant observations (root + neighbor), got %d", len(observations))
	}
}

func TestTemporalGetRelevant_MalformedAt(t *testing.T) {
	h, stores := setupTemporalHandler(t)
	fromID, _ := seedTwoObservations(t, stores, "demo")

	result := callTemporal(t, h.GetTemporalRelevant, map[string]interface{}{
		"observation_id": float64(fromID),
		"at":             "bad-date",
		"depth":          float64(1),
	})
	if !result.IsError {
		t.Fatalf("expected IsError for malformed at, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "Invalid 'at' format") {
		t.Errorf("expected \"Invalid 'at' format\", got %q", resultText(result))
	}
}

// --- temporal_create_snapshot --------------------------------------------

func TestTemporalCreateSnapshot_Success(t *testing.T) {
	h, stores := setupTemporalHandler(t)
	fromID, _ := seedTwoObservations(t, stores, "demo")

	result := callTemporal(t, h.CreateTemporalSnapshot, map[string]interface{}{
		"snapshot_key":        "snap-001",
		"root_observation_id": float64(fromID),
		"description":         "initial graph state",
	})

	var snapshot map[string]interface{}
	if err := json.Unmarshal([]byte(resultText(result)), &snapshot); err != nil {
		t.Fatalf("expected JSON snapshot object, got error %v (text: %q)", err, resultText(result))
	}
	if key, _ := snapshot["snapshot_key"].(string); key != "snap-001" {
		t.Errorf("expected snapshot_key 'snap-001', got %#v", snapshot["snapshot_key"])
	}
	if _, ok := snapshot["id"]; !ok {
		t.Errorf("expected snapshot to carry an id, got %v", snapshot)
	}

	// Database effect: snapshot retrievable by key.
	got, err := stores.TemporalSnapshots.GetBySnapshotKey(context.Background(), "snap-001")
	if err != nil {
		t.Fatalf("get snapshot by key: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 persisted snapshot, got %d", len(got))
	}
}

// --- temporal_record_operation -------------------------------------------

func TestTemporalRecordOperation_Success(t *testing.T) {
	h, stores := setupTemporalHandler(t)

	result := callTemporal(t, h.RecordOperation, map[string]interface{}{
		"session_id":         "sess-obs-1",
		"operation_type":     "search",
		"duration_ms":        float64(42),
		"result_count":       float64(3),
		"success":            true,
		"memory_usage_bytes": float64(1024),
		"timestamp":          "2025-06-15T10:00:00Z",
		"observation_count":  float64(10),
		"edge_count":         float64(2),
		"query_complexity":   float64(0.5),
		"confidence_score":   float64(0.8),
	})
	text := resultText(result)

	if !strings.Contains(text, "Operation recorded successfully") {
		t.Fatalf("expected success message, got %q", text)
	}

	// Database effect: the metric is retrievable in a wide time range.
	metrics, err := stores.Metrics.GetTemporalMetrics(context.Background(), "sess-obs-1",
		parseRFC3339(t, "2000-01-01T00:00:00Z"), parseRFC3339(t, "2100-01-01T00:00:00Z"))
	if err != nil {
		t.Fatalf("get temporal metrics: %v", err)
	}
	if len(metrics) != 1 {
		t.Errorf("expected 1 metric persisted, got %d", len(metrics))
	}
}

func TestTemporalRecordOperation_MalformedTimestamp(t *testing.T) {
	h, _ := setupTemporalHandler(t)

	result := callTemporal(t, h.RecordOperation, map[string]interface{}{
		"session_id":     "sess-obs-1",
		"operation_type": "search",
		"timestamp":      "bad-date",
	})
	if !result.IsError {
		t.Fatalf("expected IsError for malformed timestamp, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "Invalid timestamp format") {
		t.Errorf("expected 'Invalid timestamp format', got %q", resultText(result))
	}
}

// --- temporal_evaluate_quality -------------------------------------------

func TestTemporalEvaluateMemoryQuality_Success(t *testing.T) {
	h, stores := setupTemporalHandler(t)

	result := callTemporal(t, h.EvaluateMemoryQuality, map[string]interface{}{
		"session_id":      "sess-q-1",
		"evaluation_type": "overall",
	})

	var quality map[string]interface{}
	if err := json.Unmarshal([]byte(resultText(result)), &quality); err != nil {
		t.Fatalf("expected JSON quality object, got error %v (text: %q)", err, resultText(result))
	}
	if et, _ := quality["evaluation_type"].(string); et != "overall" {
		t.Errorf("expected evaluation_type 'overall', got %#v", quality["evaluation_type"])
	}
	if _, ok := quality["score"]; !ok {
		t.Errorf("expected quality to carry a score, got %v", quality)
	}

	// Database effect: the quality metric was persisted.
	saved, err := stores.QualityMetrics.GetBySession(context.Background(), "sess-q-1", 10)
	if err != nil {
		t.Fatalf("get quality metrics: %v", err)
	}
	if len(saved) != 1 {
		t.Errorf("expected 1 quality metric persisted, got %d", len(saved))
	}
}

func TestTemporalEvaluateMemoryQuality_UnsupportedType(t *testing.T) {
	h, _ := setupTemporalHandler(t)

	result := callTemporal(t, h.EvaluateMemoryQuality, map[string]interface{}{
		"session_id":      "sess-q-1",
		"evaluation_type": "bogus-dimension",
	})
	if !result.IsError {
		t.Fatalf("expected IsError for unsupported evaluation type, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "Failed to evaluate memory quality") {
		t.Errorf("expected 'Failed to evaluate memory quality', got %q", resultText(result))
	}
}

// --- temporal_system_metrics ---------------------------------------------

func TestTemporalSystemMetrics_Success(t *testing.T) {
	h, _ := setupTemporalHandler(t)

	result := callTemporal(t, h.GetSystemMetrics, map[string]interface{}{
		"session_id": "sess-sys-1",
		"from":       "2000-01-01T00:00:00Z",
		"to":         "2100-01-01T00:00:00Z",
	})

	var sys map[string]interface{}
	if err := json.Unmarshal([]byte(resultText(result)), &sys); err != nil {
		t.Fatalf("expected JSON system metrics object, got error %v (text: %q)", err, resultText(result))
	}
	if sid, _ := sys["session_id"].(string); sid != "sess-sys-1" {
		t.Errorf("expected session_id 'sess-sys-1', got %#v", sys["session_id"])
	}
	if _, ok := sys["total_operations"]; !ok {
		t.Errorf("expected total_operations field, got %v", sys)
	}
}

func TestTemporalSystemMetrics_MalformedFrom(t *testing.T) {
	h, _ := setupTemporalHandler(t)

	result := callTemporal(t, h.GetSystemMetrics, map[string]interface{}{
		"session_id": "sess-sys-1",
		"from":       "bad-date",
		"to":         "2100-01-01T00:00:00Z",
	})
	if !result.IsError {
		t.Fatalf("expected IsError for malformed from, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "Invalid 'from' format") {
		t.Errorf("expected \"Invalid 'from' format\", got %q", resultText(result))
	}
}

func TestTemporalSystemMetrics_MalformedTo(t *testing.T) {
	h, _ := setupTemporalHandler(t)

	result := callTemporal(t, h.GetSystemMetrics, map[string]interface{}{
		"session_id": "sess-sys-1",
		"from":       "2000-01-01T00:00:00Z",
		"to":         "bad-date",
	})
	if !result.IsError {
		t.Fatalf("expected IsError for malformed to, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "Invalid 'to' format") {
		t.Errorf("expected \"Invalid 'to' format\", got %q", resultText(result))
	}
}

// --- temporal_health_check -----------------------------------------------

func TestTemporalHealthCheck_Success(t *testing.T) {
	h, _ := setupTemporalHandler(t)

	result := callTemporal(t, h.GetHealthCheck, map[string]interface{}{})

	var health map[string]interface{}
	if err := json.Unmarshal([]byte(resultText(result)), &health); err != nil {
		t.Fatalf("expected JSON health object, got error %v (text: %q)", err, resultText(result))
	}
	status, _ := health["status"].(string)
	switch status {
	case "healthy", "degraded", "critical":
		// ok
	default:
		t.Errorf("expected a known health status, got %q (full: %v)", status, health)
	}
}

// --- temporal_evolution_path ---------------------------------------------

func TestTemporalEvolutionPath_EmptyForOriginalEdge(t *testing.T) {
	h, stores := setupTemporalHandler(t)
	fromID, toID := seedTwoObservations(t, stores, "demo")

	if _, err := callTemporalWithErr(t, h.CreateTemporalEdge, map[string]interface{}{
		"from_obs_id":   float64(fromID),
		"to_obs_id":     float64(toID),
		"relation_type": "references",
		"valid_from":    "2020-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	edges, err := stores.Graph.GetEdgesForObservation(context.Background(), fromID)
	if err != nil || len(edges) != 1 {
		t.Fatalf("expected 1 seeded edge, got %d (err %v)", len(edges), err)
	}
	edgeID := edges[0].ID

	// An original edge has no evolution chain, so the path is empty.
	result := callTemporal(t, h.GetTemporalEvolutionPath, map[string]interface{}{
		"edge_id": float64(edgeID),
	})
	var path []map[string]interface{}
	if err := json.Unmarshal([]byte(resultText(result)), &path); err != nil {
		t.Fatalf("expected decodable JSON (possibly null), got error %v (text: %q)", err, resultText(result))
	}
	if len(path) != 0 {
		t.Errorf("expected empty evolution path for original edge, got %d", len(path))
	}
}

func TestTemporalEvolutionPath_NotFound(t *testing.T) {
	h, _ := setupTemporalHandler(t)

	result := callTemporal(t, h.GetTemporalEvolutionPath, map[string]interface{}{
		"edge_id": float64(999999),
	})
	if !result.IsError {
		t.Fatalf("expected IsError for missing edge, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "Failed to get evolution path") {
		t.Errorf("expected 'Failed to get evolution path', got %q", resultText(result))
	}
}

// --- temporal_fact_state -------------------------------------------------

func TestTemporalFactState_WithCurrentEdge(t *testing.T) {
	h, stores := setupTemporalHandler(t)
	fromID, toID := seedTwoObservations(t, stores, "demo")

	if _, err := callTemporalWithErr(t, h.CreateTemporalEdge, map[string]interface{}{
		"from_obs_id":   float64(fromID),
		"to_obs_id":     float64(toID),
		"relation_type": "references",
		"valid_from":    "2020-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	result := callTemporal(t, h.GetCurrentFactState, map[string]interface{}{
		"observation_id": float64(fromID),
	})

	var facts map[string]interface{}
	if err := json.Unmarshal([]byte(resultText(result)), &facts); err != nil {
		t.Fatalf("expected JSON fact map, got error %v (text: %q)", err, resultText(result))
	}
	if len(facts) == 0 {
		t.Errorf("expected at least 1 current fact for the seeded edge, got empty map")
	}
}

func TestTemporalFactState_NoEdges(t *testing.T) {
	h, stores := setupTemporalHandler(t)
	fromID, _ := seedTwoObservations(t, stores, "demo")

	result := callTemporal(t, h.GetCurrentFactState, map[string]interface{}{
		"observation_id": float64(fromID),
	})

	var facts map[string]interface{}
	if err := json.Unmarshal([]byte(resultText(result)), &facts); err != nil {
		t.Fatalf("expected JSON fact map, got error %v (text: %q)", err, resultText(result))
	}
	if len(facts) != 0 {
		t.Errorf("expected no facts for an isolated observation, got %d", len(facts))
	}
}

// --- registration gating -------------------------------------------------

func TestShouldRegister_AllowlistLogic(t *testing.T) {
	if !shouldRegister("any_tool", nil) {
		t.Error("nil allowlist (all tools) should register every tool")
	}
	if !shouldRegister("cortex_save", map[string]bool{"cortex_save": true}) {
		t.Error("tool present in allowlist should register")
	}
	if shouldRegister("cortex_save", map[string]bool{"cortex_search": true}) {
		t.Error("tool absent from allowlist should not register")
	}
}

func TestRegisterTemporalTools_NilRepositoriesSkipsRegistration(t *testing.T) {
	// setupTestStores leaves Metrics / QualityMetrics nil -> the guard in
	// registerTemporalTools must skip ALL temporal tools.
	stores := setupTestStores(t)
	srv := NewServerWithTools(stores, nil)

	for _, name := range temporalToolNames {
		if srv.GetTool(name) != nil {
			t.Errorf("expected temporal tool %q to NOT be registered when Metrics/QualityMetrics are nil", name)
		}
	}
}

func TestRegisterTemporalTools_AllRegisteredWhenReposPresent(t *testing.T) {
	stores := setupStoresWithObservability(t)
	srv := NewServerWithTools(stores, nil)

	for _, name := range temporalToolNames {
		if srv.GetTool(name) == nil {
			t.Errorf("expected temporal tool %q to be registered when repositories are present", name)
		}
	}
}

func TestRegisterTemporalTools_AllowlistExcludesTemporal(t *testing.T) {
	stores := setupStoresWithObservability(t)
	// Allowlist contains only a memory tool; every temporal tool must be excluded
	// even though the repositories are present.
	srv := NewServerWithTools(stores, map[string]bool{"cortex_save": true})

	if srv.GetTool("cortex_save") == nil {
		t.Error("expected cortex_save to be registered under its own allowlist")
	}
	for _, name := range temporalToolNames {
		if srv.GetTool(name) != nil {
			t.Errorf("expected temporal tool %q to NOT be registered under a cortex_save-only allowlist", name)
		}
	}
}

// --- helpers -------------------------------------------------------------

// callTemporalWithErr returns both the result and the handler error so seed
// calls can fail the test loudly.
func callTemporalWithErr(t *testing.T, fn func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]interface{}) (*mcp.CallToolResult, error) {
	t.Helper()
	return fn(context.Background(), mustMakeRequest(t, args))
}

// mustMakeRequest builds a *mcp.CallToolRequest carrying the given arguments,
// matching how the real MCP server delivers arguments to handlers.
func mustMakeRequest(t *testing.T, args map[string]interface{}) *mcp.CallToolRequest {
	t.Helper()
	req := &mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

// parseRFC3339 parses an RFC3339 timestamp or fails the test.
func parseRFC3339(t *testing.T, ts string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatalf("parse %q: %v", ts, err)
	}
	return v
}

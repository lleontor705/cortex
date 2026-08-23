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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lleontor705/cortex/v2/internal/domain/observability"
	"github.com/lleontor705/cortex/v2/internal/domain/temporal"
	"github.com/lleontor705/cortex/v2/internal/migration"
	graphstore "github.com/lleontor705/cortex/v2/internal/store/graph"
	"github.com/lleontor705/cortex/v2/internal/store/prompt"
	scoringstore "github.com/lleontor705/cortex/v2/internal/store/scoring"
	"github.com/lleontor705/cortex/v2/internal/store/search"
	"github.com/lleontor705/cortex/v2/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
	"github.com/lleontor705/cortex/v2/internal/vector/sqlite_blob"
	"github.com/lleontor705/cortex/v2/testutil"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
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
		Vectors:           sqlite_blob.New(db),
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

// --- T08R / QW-02: bounded stable public error lowering --------------------
//
// The T10 independent review demonstrated that temporal handlers interpolate
// raw parser, service, and serialization errors into public tool results. The
// oracles below prove the lowered contract: constant validation text plus a
// stable [code: ...] suffix, with no caller-input echo and no backend detail.

// temporalTimestampCanary carries every leak class in one malformed timestamp:
// SQL body, DSN/URL, token, path, and IP fragments.
const temporalTimestampCanary = "CANARY-7f3a'; DROP TABLE metrics; -- postgres://u:sk-LEAK@10.0.0.9:5432/db?x=C:/canary/p"

// temporalLeakCorpus lists raw fragments that must never appear in a public
// temporal tool result: Go parser internals, encoding/json internals,
// database/sql driver text, and the canary classes above.
var temporalLeakCorpus = []string{
	"parsing time",
	"time.ParseError",
	"cannot parse",
	"extra text",
	"json:",
	"Go struct field",
	"sql:",
	"database is closed",
	"CANARY",
	"postgres://",
	"10.0.0.9",
	"sk-LEAK",
	"C:/canary",
	"DROP TABLE",
}

// assertNoTemporalLeak fails when any leak fragment reaches the public text.
func assertNoTemporalLeak(t *testing.T, context, text string) {
	t.Helper()
	for _, fragment := range temporalLeakCorpus {
		if strings.Contains(text, fragment) {
			t.Errorf("%s: public result leaks %q: %q", context, fragment, text)
		}
	}
}

func TestTemporalParseErrorsLoweredConstant(t *testing.T) {
	h, _ := setupTemporalHandler(t)

	cases := []struct {
		name   string
		invoke func() *mcp.CallToolResult
		want   string
	}{
		{"create_edge/valid_from", func() *mcp.CallToolResult {
			return callTemporal(t, h.CreateTemporalEdge, map[string]interface{}{
				"from_obs_id": float64(1), "to_obs_id": float64(2),
				"relation_type": "references", "valid_from": temporalTimestampCanary,
			})
		}, "Invalid valid_from format"},
		{"create_edge/invalid_at", func() *mcp.CallToolResult {
			return callTemporal(t, h.CreateTemporalEdge, map[string]interface{}{
				"from_obs_id": float64(1), "to_obs_id": float64(2),
				"relation_type": "references", "invalid_at": temporalTimestampCanary,
			})
		}, "Invalid invalid_at format"},
		{"get_edges/at", func() *mcp.CallToolResult {
			return callTemporal(t, h.GetTemporalEdges, map[string]interface{}{
				"observation_id": float64(1), "at": temporalTimestampCanary,
			})
		}, "Invalid 'at' format"},
		{"get_relevant/at", func() *mcp.CallToolResult {
			return callTemporal(t, h.GetTemporalRelevant, map[string]interface{}{
				"observation_id": float64(1), "at": temporalTimestampCanary, "depth": float64(1),
			})
		}, "Invalid 'at' format"},
		{"record_operation/timestamp", func() *mcp.CallToolResult {
			return callTemporal(t, h.RecordOperation, map[string]interface{}{
				"session_id": "s", "operation_type": "search", "timestamp": temporalTimestampCanary,
			})
		}, "Invalid timestamp format"},
		{"system_metrics/from", func() *mcp.CallToolResult {
			return callTemporal(t, h.GetSystemMetrics, map[string]interface{}{
				"session_id": "s", "from": temporalTimestampCanary, "to": "2100-01-01T00:00:00Z",
			})
		}, "Invalid 'from' format"},
		{"system_metrics/to", func() *mcp.CallToolResult {
			return callTemporal(t, h.GetSystemMetrics, map[string]interface{}{
				"session_id": "s", "from": "2000-01-01T00:00:00Z", "to": temporalTimestampCanary,
			})
		}, "Invalid 'to' format"},
	}

	for _, c := range cases {
		result := c.invoke()
		if result == nil || !result.IsError {
			t.Fatalf("%s: expected IsError, got %q", c.name, resultText(result))
		}
		text := resultText(result)
		if !strings.Contains(text, c.want) {
			t.Errorf("%s: expected constant %q, got %q", c.name, c.want, text)
		}
		if !strings.Contains(text, "[code: validation]") {
			t.Errorf("%s: expected stable validation code tag, got %q", c.name, text)
		}
		assertNoTemporalLeak(t, c.name, text)
	}
}

func TestTemporalBindErrorsLoweredConstant(t *testing.T) {
	h, _ := setupTemporalHandler(t)

	// A nested object where int64 is expected fails inside BindArguments; the
	// json binder's raw internals must not reach the public result.
	cases := []struct {
		name   string
		invoke func() *mcp.CallToolResult
	}{
		{"create_edge/from_obs_id", func() *mcp.CallToolResult {
			return callTemporal(t, h.CreateTemporalEdge, map[string]interface{}{
				"from_obs_id": map[string]interface{}{"CANARY": "x"}, "to_obs_id": float64(2),
				"relation_type": "references",
			})
		}},
		{"get_edges/observation_id", func() *mcp.CallToolResult {
			return callTemporal(t, h.GetTemporalEdges, map[string]interface{}{
				"observation_id": []interface{}{"CANARY"}, "at": "2025-06-15T00:00:00Z",
			})
		}},
		{"record_operation/duration_ms", func() *mcp.CallToolResult {
			return callTemporal(t, h.RecordOperation, map[string]interface{}{
				"session_id": "s", "operation_type": "search",
				"duration_ms": map[string]interface{}{"CANARY": 1}, "timestamp": "2025-06-15T10:00:00Z",
			})
		}},
	}

	for _, c := range cases {
		result := c.invoke()
		if result == nil || !result.IsError {
			t.Fatalf("%s: expected IsError, got %q", c.name, resultText(result))
		}
		text := resultText(result)
		if !strings.Contains(text, "Invalid parameters") {
			t.Errorf("%s: expected constant 'Invalid parameters', got %q", c.name, text)
		}
		if !strings.Contains(text, "[code: validation]") {
			t.Errorf("%s: expected stable validation code tag, got %q", c.name, text)
		}
		assertNoTemporalLeak(t, c.name, text)
	}
}

func TestTemporalServiceErrorsLoweredAndCanaryFree(t *testing.T) {
	h, stores := setupTemporalHandler(t)

	// Poison the shared handle: every subsequent store call fails with raw
	// database/sql driver text. Callers still inject canary strings through
	// ordinary string parameters; none of it may reach the result.
	if err := stores.Observations.DB().Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	cases := []struct {
		name   string
		invoke func() *mcp.CallToolResult
		want   string
	}{
		{"create_edge", func() *mcp.CallToolResult {
			return callTemporal(t, h.CreateTemporalEdge, map[string]interface{}{
				"from_obs_id": float64(1), "to_obs_id": float64(2),
				"relation_type": "references", "valid_from": "2020-01-01T00:00:00Z",
			})
		}, "Failed to create temporal edge"},
		{"get_edges", func() *mcp.CallToolResult {
			return callTemporal(t, h.GetTemporalEdges, map[string]interface{}{
				"observation_id": float64(1), "at": "2025-06-15T00:00:00Z",
			})
		}, "Failed to get temporal edges"},
		{"get_relevant", func() *mcp.CallToolResult {
			return callTemporal(t, h.GetTemporalRelevant, map[string]interface{}{
				"observation_id": float64(1), "at": "2025-06-15T00:00:00Z", "depth": float64(1),
			})
		}, "Failed to get temporal relevant observations"},
		{"create_snapshot", func() *mcp.CallToolResult {
			return callTemporal(t, h.CreateTemporalSnapshot, map[string]interface{}{
				"snapshot_key": "CANARY-7f3a", "root_observation_id": float64(1),
			})
		}, "Failed to create temporal snapshot"},
		{"record_operation", func() *mcp.CallToolResult {
			return callTemporal(t, h.RecordOperation, map[string]interface{}{
				"session_id": "CANARY-7f3a", "operation_type": "search",
				"timestamp": "2025-06-15T10:00:00Z",
			})
		}, "Failed to record operation"},
		{"evaluate_quality", func() *mcp.CallToolResult {
			return callTemporal(t, h.EvaluateMemoryQuality, map[string]interface{}{
				"session_id": "CANARY-7f3a", "evaluation_type": "overall",
			})
		}, "Failed to evaluate memory quality"},
		{"system_metrics", func() *mcp.CallToolResult {
			return callTemporal(t, h.GetSystemMetrics, map[string]interface{}{
				"session_id": "CANARY-7f3a", "from": "2000-01-01T00:00:00Z", "to": "2100-01-01T00:00:00Z",
			})
		}, "Failed to get system metrics"},
		{"health_check", func() *mcp.CallToolResult {
			return callTemporal(t, h.GetHealthCheck, map[string]interface{}{})
		}, "Failed to get health check"},
		{"evolution_path", func() *mcp.CallToolResult {
			return callTemporal(t, h.GetTemporalEvolutionPath, map[string]interface{}{
				"edge_id": float64(1),
			})
		}, "Failed to get evolution path"},
		{"fact_state", func() *mcp.CallToolResult {
			return callTemporal(t, h.GetCurrentFactState, map[string]interface{}{
				"observation_id": float64(1),
			})
		}, "Failed to get current fact state"},
	}

	for _, c := range cases {
		result := c.invoke()
		if result == nil || !result.IsError {
			t.Fatalf("%s: expected IsError, got %q", c.name, resultText(result))
		}
		text := resultText(result)
		if !strings.Contains(text, c.want) {
			t.Errorf("%s: expected constant %q, got %q", c.name, c.want, text)
		}
		if !strings.Contains(text, "[code: ") {
			t.Errorf("%s: expected stable classification code tag, got %q", c.name, text)
		}
		assertNoTemporalLeak(t, c.name, text)
	}
}

// TestTemporalSerializationFailureRedacted forces the marshal branch with a
// payload that cannot be serialized (a channel) and proves the public error
// stays constant, coded, and free of the raw marshal cause.
func TestTemporalSerializationFailureRedacted(t *testing.T) {
	result, err := temporalJSONResult(struct{ Ch chan int }{}, "edges")
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError, got %q", resultText(result))
	}
	text := resultText(result)
	if !strings.Contains(text, "Failed to serialize edges") {
		t.Errorf("expected constant serialization text, got %q", text)
	}
	if !strings.Contains(text, "[code: internal]") {
		t.Errorf("expected stable internal code tag, got %q", text)
	}
	// encoding/json's raw cause names the payload type; it must not surface.
	assertNoTemporalLeak(t, "serialize/edges", text)
	if strings.Contains(text, "chan") || strings.Contains(text, "unsupported type") {
		t.Errorf("raw marshal cause leaked: %q", text)
	}
}

// TestTemporalSuccessOutputUnchanged pins the success bytes produced by the
// serialization path so the lowered error handling cannot drift the public
// success contract (T08R non-goal: no success JSON changes).
func TestTemporalSuccessOutputUnchanged(t *testing.T) {
	h, stores := setupTemporalHandler(t)
	fromID, _ := seedTwoObservations(t, stores, "demo")

	// No edges exist: the serialized payload is the exact historical bytes.
	empty := callTemporal(t, h.GetTemporalEdges, map[string]interface{}{
		"observation_id": float64(fromID), "at": "2025-06-15T00:00:00Z",
	})
	if empty.IsError {
		t.Fatalf("unexpected error: %q", resultText(empty))
	}
	if got := resultText(empty); got != "null" && got != "[]" {
		t.Errorf("empty edges serialization drifted from historical bytes: %q", got)
	}

	// Deterministic non-JSON success text is byte-exact.
	recorded := callTemporal(t, h.RecordOperation, map[string]interface{}{
		"session_id": "sess-pin", "operation_type": "search",
		"timestamp": "2025-06-15T10:00:00Z",
	})
	if recorded.IsError {
		t.Fatalf("unexpected error: %q", resultText(recorded))
	}
	if got := resultText(recorded); got != "Operation recorded successfully" {
		t.Errorf("record_operation success text drifted: %q", got)
	}

	// Snapshot success remains a JSON object with the historical fields.
	var snapshot map[string]interface{}
	if err := json.Unmarshal([]byte(resultText(callTemporal(t, h.CreateTemporalSnapshot, map[string]interface{}{
		"snapshot_key": "snap-pin", "root_observation_id": float64(fromID),
	}))), &snapshot); err != nil {
		t.Fatalf("snapshot success must remain JSON: %v", err)
	}
	if key, _ := snapshot["snapshot_key"].(string); key != "snap-pin" {
		t.Errorf("snapshot_key drifted: %#v", snapshot["snapshot_key"])
	}
	if _, ok := snapshot["observation_count"]; !ok {
		t.Errorf("snapshot field set drifted: %v", snapshot)
	}
}

// temporalStdioChildEnv marks the re-executed test binary as the stdio MCP
// server child for the round-trip oracle below.
const temporalStdioChildEnv = "CORTEX_TEST_TEMPORAL_STDIO_CHILD"

// TestTemporalStdioServeChild is not a normal test: when invoked with the
// child env marker it serves the temporal MCP profile over real stdio and
// exits, letting the parent test drive it as an independent client process.
//
// The optional mode env selects the backend shape for the T08R2 wire proofs:
//   - normal (default): the observability fixture plus one seeded finite
//     metrics row;
//   - poisoned: a metrics table whose CHECK constraint name carries the full
//     canary corpus (the injected backend failure seam);
//   - marshal: a seeded +Inf query_complexity row with the range check
//     removed (the injected serialization failure seam).
//
// Before serving, every mode self-probes its seam and records the proof file
// named by the proof env so the parent can gate its assertions.
func TestTemporalStdioServeChild(t *testing.T) {
	if os.Getenv(temporalStdioChildEnv) != "1" {
		t.Skip("stdio child helper; spawned by TestTemporalStdioErrorNoEcho")
	}
	mode := os.Getenv(temporalStdioModeEnv)
	proofPath := os.Getenv(temporalStdioProofEnv)

	var srvStores *Stores
	var proof temporalChildProof
	switch mode {
	case "", temporalStdioModeNormal:
		srvStores = setupStoresWithObservability(t)
		proof = temporalSeedNormalWireMetrics(t, srvStores, proofPath)
	case temporalStdioModePoisoned:
		srvStores, proof = temporalPoisonedWireStores(t, proofPath)
	case temporalStdioModeMarshal:
		srvStores, proof = temporalMarshalWireStores(t, proofPath)
	default:
		_, _ = os.Stderr.WriteString("temporal stdio child: unknown mode " + mode + "\n")
		os.Exit(1)
	}

	if proofPath != "" {
		temporalWriteChildProof(proofPath, proof)
	}

	// Pre-handler recording seam (T08R2 blocker 1): a server hook fires
	// strictly before the production tool handler and records the exact
	// wire-received timestamp argument — byte length, rune count, SHA-256,
	// required-fragment presence, and prefix integrity — so the parent can
	// prove the full >= 16 KiB argument crossed stdio untruncated before any
	// production code ran. Hooking is test-side instrumentation only; the
	// registered tools and handlers are still the production registrations.
	wireProofPath := os.Getenv(temporalStdioWireProofEnv)
	hooks := &mcpserver.Hooks{}
	hooks.AddBeforeCallTool(func(ctx context.Context, id any, message *mcp.CallToolRequest) {
		if wireProofPath == "" || message.Params.Name != "cortex_temporal_record_operation" {
			return
		}
		timestamp, _ := message.GetArguments()["timestamp"].(string)
		if timestamp == "" {
			return
		}
		wireProof := temporalVerifyWireTimestamp(timestamp)
		wireProof.Detail = "recorded by BeforeCallTool before handler dispatch"
		data, err := json.Marshal(wireProof)
		if err != nil {
			_, _ = os.Stderr.WriteString("temporal stdio child: encode wire proof: " + err.Error() + "\n")
			return
		}
		if err := os.WriteFile(wireProofPath, data, 0o600); err != nil {
			_, _ = os.Stderr.WriteString("temporal stdio child: write wire proof: " + err.Error() + "\n")
		}
	})

	// Mirror NewServerWithTools exactly (same constructors, allowlist, and
	// registration order) with the observation hooks attached.
	srv := mcpserver.NewMCPServer("cortex", serverVersion,
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithInstructions(serverInstructions),
		mcpserver.WithHooks(hooks),
	)
	registerMemoryTools(srv, srvStores, ProfileTemporal)
	registerCortexTools(srv, srvStores, ProfileTemporal)
	registerTemporalTools(srv, srvStores, ProfileTemporal)
	if err := mcpserver.ServeStdio(srv); err != nil {
		_, _ = os.Stderr.WriteString("temporal stdio child: " + err.Error() + "\n")
		os.Exit(1)
	}
	// Suppress test-framework output on the MCP stdout channel.
	os.Exit(0)
}

// temporalWriteChildProof records the child's seam self-probe for the parent.
func temporalWriteChildProof(path string, proof temporalChildProof) {
	data, err := json.Marshal(proof)
	if err != nil {
		_, _ = os.Stderr.WriteString("temporal stdio child: encode proof: " + err.Error() + "\n")
		os.Exit(1)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		_, _ = os.Stderr.WriteString("temporal stdio child: write proof: " + err.Error() + "\n")
		os.Exit(1)
	}
}

// temporalChildSeamFail records a failed seam probe and aborts the child so
// the parent fails loudly instead of serving an unarmed backend.
func temporalChildSeamFail(path, mode, seam, detail string) {
	if path != "" {
		temporalWriteChildProof(path, temporalChildProof{Mode: mode, Seam: seam, Fired: false, Detail: detail})
	}
	_, _ = os.Stderr.WriteString("temporal stdio child: seam not armed: " + seam + ": " + detail + "\n")
	os.Exit(1)
}

// temporalSeedNormalWireMetrics seeds the finite metrics row used by the
// normal-mode success pins and proves it is retrievable before serving.
func temporalSeedNormalWireMetrics(t *testing.T, stores *Stores, proofPath string) temporalChildProof {
	ts := parseRFC3339(t, "2025-06-15T10:00:00Z")
	if _, err := stores.Observations.DB().Exec(
		`INSERT INTO metrics (session_id, operation_type, duration_ms, result_count, success,
		 memory_usage_bytes, timestamp, observation_count, edge_count, query_complexity, confidence_score)
		 VALUES ('sess-wire-ok', 'search', 5, 1, 1, 128, ?, 0, 0, 0.5, 0.9)`, ts,
	); err != nil {
		temporalChildSeamFail(proofPath, temporalStdioModeNormal, "seeded-finite-metrics-row", "seed: "+err.Error())
	}
	var n int
	if err := stores.Observations.DB().QueryRow(
		`SELECT COUNT(*) FROM metrics WHERE session_id = 'sess-wire-ok'`).Scan(&n); err != nil || n < 1 {
		temporalChildSeamFail(proofPath, temporalStdioModeNormal, "seeded-finite-metrics-row", "verify: rows missing")
	}
	return temporalChildProof{Mode: temporalStdioModeNormal, Seam: "seeded-finite-metrics-row", Fired: true}
}

// temporalPoisonedWireStores builds the backend-failure seam: a metrics table
// whose named CHECK constraint rejects operation_type 'search' and whose name
// carries the SQL/DSN/token/path/URL/IP/body canary corpus. The self-probe
// proves the constraint fires with the canary before serving.
func temporalPoisonedWireStores(t *testing.T, proofPath string) (*Stores, temporalChildProof) {
	const seam = "metrics-check-constraint-canary"
	const canaryConstraint = `CONSTRAINT "CANARY-7f3a'; DROP TABLE metrics; -- postgres://u:sk-LEAK@10.0.0.9:5432/db?x=C:/canary/p https://canary-host.internal/v1 body=canary-body-9911" CHECK (operation_type <> 'search')`

	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1, Name: "wire_poisoned_metrics",
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
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				` + canaryConstraint + `
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
	db := testutil.NewTestDBWithMigrations(t, registry).DB()

	// Self-probe: the canary-named constraint must fire on operation_type
	// 'search' with the canary text in the raw backend error.
	_, err := db.Exec(
		`INSERT INTO metrics (session_id, operation_type, duration_ms, result_count, success, timestamp)
		 VALUES ('probe', 'search', 1, 0, 1, ?)`, time.Now())
	if err == nil {
		temporalChildSeamFail(proofPath, temporalStdioModePoisoned, seam, "probe insert unexpectedly succeeded")
	}
	raw := err.Error()
	for _, fragment := range temporalWireCanaryFragments {
		if !strings.Contains(raw, fragment) {
			temporalChildSeamFail(proofPath, temporalStdioModePoisoned, seam, "probe error lacks canary fragment "+fragment)
		}
	}

	return &Stores{
			Metrics:        sqlitestore.NewMetricsRepository(db),
			QualityMetrics: sqlitestore.NewQualityMetricsRepository(db),
		}, temporalChildProof{
			Mode:   temporalStdioModePoisoned,
			Seam:   seam,
			Fired:  true,
			Detail: "canary constraint fired with full corpus",
		}
}

// temporalMarshalWireStores builds the serialization-failure seam: a seeded
// metrics row with +Inf query_complexity (range check removed) that the real
// system_metrics aggregation forwards to json.Marshal. The self-probe proves
// the stored value scans back as +Inf before serving.
func temporalMarshalWireStores(t *testing.T, proofPath string) (*Stores, temporalChildProof) {
	const seam = "metrics-inf-query-complexity"

	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1, Name: "wire_marshal_metrics",
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
				query_complexity REAL NOT NULL DEFAULT 0.0,
				confidence_score REAL NOT NULL DEFAULT 0.0,
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
	db := testutil.NewTestDBWithMigrations(t, registry).DB()

	if _, err := db.Exec(
		`INSERT INTO metrics (session_id, operation_type, duration_ms, result_count, success,
		 memory_usage_bytes, timestamp, observation_count, edge_count, query_complexity, confidence_score)
		 VALUES ('sess-wire-marshal', 'search', 7, 2, 1, 256, ?, 0, 0, 9e999, 0.5)`, time.Now(),
	); err != nil {
		temporalChildSeamFail(proofPath, temporalStdioModeMarshal, seam, "seed: "+err.Error())
	}

	var complexity float64
	if err := db.QueryRow(
		`SELECT query_complexity FROM metrics WHERE session_id = 'sess-wire-marshal'`).Scan(&complexity); err != nil {
		temporalChildSeamFail(proofPath, temporalStdioModeMarshal, seam, "probe scan: "+err.Error())
	}
	if !math.IsInf(complexity, 1) {
		temporalChildSeamFail(proofPath, temporalStdioModeMarshal, seam,
			"stored query_complexity is not +Inf: "+strconv.FormatFloat(complexity, 'g', -1, 64))
	}

	return &Stores{
			Metrics:        sqlitestore.NewMetricsRepository(db),
			QualityMetrics: sqlitestore.NewQualityMetricsRepository(db),
		}, temporalChildProof{
			Mode:   temporalStdioModeMarshal,
			Seam:   seam,
			Fired:  true,
			Detail: "seeded row scans back as +Inf",
		}
}

// TestTemporalStdioErrorNoEcho replays the reviewer's attack through a real
// stdio MCP boundary: a spawned server process, a real client, and a
// canary-laden timestamp. The public response must carry only the constant
// validation text and its stable code.
func TestTemporalStdioErrorNoEcho(t *testing.T) {
	if os.Getenv(temporalStdioChildEnv) == "1" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	env := append(os.Environ(), temporalStdioChildEnv+"=1")
	stdioClient, err := mcpclient.NewStdioMCPClient(exe, env, "-test.run=TestTemporalStdioServeChild")
	if err != nil {
		t.Fatalf("build stdio client: %v", err)
	}
	defer func() { _ = stdioClient.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := stdioClient.Start(ctx); err != nil {
		t.Fatalf("start stdio client: %v", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "temporal-canary-client", Version: "1.0"}
	if _, err := stdioClient.Initialize(ctx, initReq); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = "cortex_temporal_record_operation"
	callReq.Params.Arguments = map[string]interface{}{
		"session_id":     "sess-stdio-canary",
		"operation_type": "search",
		"timestamp":      temporalTimestampCanary,
	}
	res, err := stdioClient.CallTool(ctx, callReq)
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected IsError over stdio, got %+v", res)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			text = tc.Text
		}
	}
	if !strings.Contains(text, "Invalid timestamp format") {
		t.Errorf("expected constant validation text, got %q", text)
	}
	if !strings.Contains(text, "[code: validation]") {
		t.Errorf("expected stable validation code tag, got %q", text)
	}
	assertNoTemporalLeak(t, "stdio/record_operation", text)
}

// --- T08R2 / QW-02: real-stdio boundary proofs --------------------------------
//
// The T10 review (#486) accepted the in-process lowering oracles but withheld
// approval because the service-failure, serialization-failure, and success
// shape checks never crossed a real stdio client/server framing. The oracles
// below drive every subcase through an actual child process serving the
// temporal MCP profile over ServeStdio, with a real mcp-go stdio client.
//
// Non-vacuity strategy per subcase:
//   - overlong timestamp: the payload starts with a VALID RFC3339 timestamp
//     followed by canaries and >= 16 KiB of padding. If the wire truncated or
//     dropped the argument, time.Parse would succeed (or BindArguments would
//     emit a different constant), flipping the outcome. A same-child control
//     call with the valid prefix alone must succeed.
//   - backend failure: the child serves a backend whose CHECK constraint name
//     carries SQL/DSN/token/path/URL/IP/body canaries. Before serving, the
//     child self-probes that the constraint fires with the canary text and
//     records the proof; the identical request succeeds against the normal
//     child (differential control), and only RecordOperation emits the
//     observed constant.
//   - serialization failure: the child seeds a metrics row with +Inf
//     (9e999) query_complexity; GetSystemMetrics aggregates it and the real
//     json.Marshal inside temporalJSONResult fails. The child self-probes the
//     scanned value is +Inf and records the proof; the finite-row normal child
//     succeeds, and only the marshal branch emits "Failed to serialize
//     metrics" (a backend failure would emit "Failed to get system metrics").

const (
	// temporalStdioModeEnv selects the child's backend shape.
	temporalStdioModeEnv = "CORTEX_TEST_TEMPORAL_STDIO_MODE"
	// temporalStdioProofEnv names the file where the child records its
	// seam self-probe before serving.
	temporalStdioProofEnv = "CORTEX_TEST_TEMPORAL_STDIO_PROOF"
	// temporalStdioWireProofEnv names the file where the child's pre-handler
	// hook records the exact wire-received tool arguments.
	temporalStdioWireProofEnv = "CORTEX_TEST_TEMPORAL_STDIO_WIRE_PROOF"

	temporalStdioModeNormal   = "normal"
	temporalStdioModePoisoned = "poisoned"
	temporalStdioModeMarshal  = "marshal"

	// temporalWireOverlongRunes is the minimum size of a genuinely overlong
	// timestamp argument (far beyond any plausible frame default).
	temporalWireOverlongRunes = 16384
	// temporalWireResponseBoundRunes is the explicit public response bound:
	// every temporal error result over stdio must stay below it.
	temporalWireResponseBoundRunes = 400

	// temporalOverlongPrefix is the valid RFC3339 instant that begins the
	// overlong canary argument; a wire truncation inside this prefix must
	// fail the integrity proof.
	temporalOverlongPrefix = "2025-06-15T10:00:00Z"
)

// temporalWireCanaryFragments are canary classes the injected backend failure
// must carry (constraint name) and that must never appear in a wire response.
var temporalWireCanaryFragments = []string{
	"CANARY-7f3a",
	"DROP TABLE",
	"postgres://",
	"sk-LEAK",
	"10.0.0.9",
	"C:/canary",
	"https://canary-host.internal",
	"canary-body-9911",
}

// temporalWireLeakCorpus is the full forbidden-fragment set for wire
// responses: parser internals, marshal internals, driver text, and every
// injected canary class.
var temporalWireLeakCorpus = append(append([]string{}, temporalLeakCorpus...),
	temporalWireCanaryFragments...,
)

// temporalChildProof is the seam self-probe record the child writes before
// serving so the parent can prove the intended failure seam actually exists.
type temporalChildProof struct {
	Mode   string `json:"mode"`
	Seam   string `json:"seam"`
	Fired  bool   `json:"fired"`
	Detail string `json:"detail"`
}

// temporalWireArgumentProof records what the child's pre-handler hook
// actually received from the stdio transport for a tool call: exact byte
// length, rune count, SHA-256 digest of the received argument, the required
// fragments it is missing, prefix integrity, and the verifier verdict. The
// parent independently recomputes every expectation from its own argument
// and rejects anything short of an exact match, so any truncation or
// mutation on the wire fails the proof.
type temporalWireArgumentProof struct {
	Tool          string   `json:"tool"`
	ReceivedBytes int      `json:"received_bytes"`
	ReceivedRunes int      `json:"received_runes"`
	Digest        string   `json:"digest"`
	Missing       []string `json:"missing"`
	PrefixIntact  bool     `json:"prefix_intact"`
	LengthOk      bool     `json:"length_ok"`
	DigestOk      bool     `json:"digest_ok"`
	RuneCountOk   bool     `json:"rune_count_ok"`
	Verified      bool     `json:"verified"`
	Detail        string   `json:"detail,omitempty"`
}

// temporalSHA256Hex returns the SHA-256 hex digest of s.
func temporalSHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// temporalOverlongRequiredFragments lists the fragments that must be present
// in the intact overlong argument: the valid RFC3339 prefix, every canary
// class carried by the canary body, and the padding marker.
func temporalOverlongRequiredFragments() []string {
	fragments := []string{temporalOverlongPrefix, "OVERLONG-9f."}
	for _, fragment := range temporalWireCanaryFragments {
		if strings.Contains(temporalTimestampCanary, fragment) {
			fragments = append(fragments, fragment)
		}
	}
	return fragments
}

// temporalVerifyWireTimestamp is the single integrity verifier shared by the
// child's pre-handler hook and the in-process mutation controls. It accepts
// only the exact expected overlong argument: identical byte length, rune
// count, and SHA-256 digest; every required fragment present; and the valid
// RFC3339 prefix intact. Any truncation — including one inside the prefix —
// fails at least the length, digest, and prefix checks.
func temporalVerifyWireTimestamp(received string) temporalWireArgumentProof {
	expected := temporalOverlongTimestampCanary()
	proof := temporalWireArgumentProof{
		Tool:          "cortex_temporal_record_operation",
		ReceivedBytes: len(received),
		ReceivedRunes: utf8.RuneCountInString(received),
		Digest:        temporalSHA256Hex(received),
		PrefixIntact:  strings.HasPrefix(received, temporalOverlongPrefix),
	}
	for _, fragment := range temporalOverlongRequiredFragments() {
		if !strings.Contains(received, fragment) {
			proof.Missing = append(proof.Missing, fragment)
		}
	}
	proof.LengthOk = proof.ReceivedBytes == len(expected)
	proof.RuneCountOk = proof.ReceivedRunes == utf8.RuneCountInString(expected)
	proof.DigestOk = proof.Digest == temporalSHA256Hex(expected)
	proof.Verified = proof.PrefixIntact && len(proof.Missing) == 0 &&
		proof.LengthOk && proof.RuneCountOk && proof.DigestOk
	return proof
}

// temporalWireResultText extracts the text content of a wire result.
func temporalWireResultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil {
		t.Fatalf("nil wire result")
	}
	var text string
	found := false
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			text = tc.Text
			found = true
		}
	}
	if !found {
		t.Fatalf("wire result carried no text content: %+v", res)
	}
	return text
}

// assertNoTemporalWireLeak fails when any wire leak fragment reaches the
// public response text.
func assertNoTemporalWireLeak(t *testing.T, context, text string) {
	t.Helper()
	for _, fragment := range temporalWireLeakCorpus {
		if strings.Contains(text, fragment) {
			t.Errorf("%s: wire response leaks %q: %q", context, fragment, text)
		}
	}
}

// assertTemporalWireBounded fails when a public error response exceeds the
// explicit bound.
func assertTemporalWireBounded(t *testing.T, context, text string) {
	t.Helper()
	if n := utf8.RuneCountInString(text); n > temporalWireResponseBoundRunes {
		t.Errorf("%s: wire response exceeds bound: %d runes > %d: %q", context, n, temporalWireResponseBoundRunes, text)
	}
}

// temporalWireChild couples a spawned stdio child with its recorded seam
// proof and a call closure performing initialized CallTool round-trips.
type temporalWireChild struct {
	proof         temporalChildProof
	wireProofPath string
	call          func(t *testing.T, tool string, args map[string]interface{}) *mcp.CallToolResult
}

// startTemporalWireChild spawns the temporal stdio child in the requested
// backend mode, waits for its seam self-probe proof, initializes the MCP
// session, and returns a call closure. It fails the test unless the child
// proves it is serving the requested mode with the intended seam armed.
func startTemporalWireChild(t *testing.T, mode string) temporalWireChild {
	t.Helper()

	proofPath := filepath.Join(t.TempDir(), "seam-proof.json")
	wireProofPath := filepath.Join(t.TempDir(), mode+"-wire-argument-proof.json")
	env := append(os.Environ(),
		temporalStdioChildEnv+"=1",
		temporalStdioModeEnv+"="+mode,
		temporalStdioProofEnv+"="+proofPath,
		temporalStdioWireProofEnv+"="+wireProofPath,
	)
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	stdioClient, err := mcpclient.NewStdioMCPClient(exe, env, "-test.run=TestTemporalStdioServeChild")
	if err != nil {
		t.Fatalf("build stdio client: %v", err)
	}
	t.Cleanup(func() { _ = stdioClient.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := stdioClient.Start(ctx); err != nil {
		t.Fatalf("start stdio client: %v", err)
	}

	// Gate every assertion on the child proving the intended seam exists.
	var proof temporalChildProof
	deadline := time.Now().Add(15 * time.Second)
	for {
		if data, err := os.ReadFile(proofPath); err == nil {
			if json.Unmarshal(data, &proof) == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("child in mode %q never recorded a seam proof at %s", mode, proofPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if proof.Mode != mode {
		t.Fatalf("child proof mode = %q, want %q", proof.Mode, mode)
	}
	if !proof.Fired {
		t.Fatalf("child in mode %q did not arm its seam: %+v", mode, proof)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "temporal-wire-client-" + mode, Version: "1.0"}
	if _, err := stdioClient.Initialize(ctx, initReq); err != nil {
		t.Fatalf("initialize (%s): %v", mode, err)
	}

	return temporalWireChild{
		proof:         proof,
		wireProofPath: wireProofPath,
		call: func(t *testing.T, tool string, args map[string]interface{}) *mcp.CallToolResult {
			t.Helper()
			callCtx, callCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer callCancel()
			callReq := mcp.CallToolRequest{}
			callReq.Params.Name = tool
			callReq.Params.Arguments = args
			res, err := stdioClient.CallTool(callCtx, callReq)
			if err != nil {
				t.Fatalf("call %s (%s): %v", tool, mode, err)
			}
			return res
		},
	}
}

// awaitArgumentProof waits for the child's pre-handler hook to record the
// wire-received overlong argument and returns the decoded proof. It matches
// only proofs describing an overlong (>= 16384 rune) argument so a stale or
// non-attack record can never satisfy it.
func (c temporalWireChild) awaitArgumentProof(t *testing.T) temporalWireArgumentProof {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if data, err := os.ReadFile(c.wireProofPath); err == nil {
			var proof temporalWireArgumentProof
			if json.Unmarshal(data, &proof) == nil && proof.ReceivedRunes >= temporalWireOverlongRunes {
				return proof
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("child never recorded the overlong wire-argument proof at %s", c.wireProofPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// temporalOverlongTimestampCanary builds a timestamp argument that begins with
// a VALID RFC3339 instant and then carries the canary corpus plus >= 16 KiB
// of padding. Truncation or argument loss flips the parse outcome, so the
// oracle cannot pass vacuously.
func temporalOverlongTimestampCanary() string {
	s := temporalOverlongPrefix + temporalTimestampCanary + strings.Repeat("OVERLONG-9f.", 2048)
	if utf8.RuneCountInString(s) < temporalWireOverlongRunes {
		panic("overlong timestamp canary below declared minimum")
	}
	return s
}

// TestTemporalWireIntegrityVerifierRejectsMutations adversarially validates
// the shared wire-argument integrity verifier: every truncation or mutation
// class — including truncation INSIDE the RFC3339 prefix — must fail the
// proof, and only the pristine argument verifies.
func TestTemporalWireIntegrityVerifierRejectsMutations(t *testing.T) {
	base := temporalOverlongTimestampCanary()
	mutations := []struct {
		name   string
		mutate func(string) string
	}{
		{"truncate-inside-prefix", func(s string) string {
			// Drop runes from within the valid RFC3339 prefix itself.
			return s[:10] + s[16:]
		}},
		{"truncate-head", func(s string) string {
			return s[7:]
		}},
		{"truncate-tail", func(s string) string {
			return s[:len(s)-200]
		}},
		{"truncate-middle", func(s string) string {
			return s[:1000] + s[1500:]
		}},
		{"same-length-substitution", func(s string) string {
			// Same byte length, different padding content.
			return strings.Replace(s, "OVERLONG-9f.", "MUTATED-0f.!", 1)
		}},
		{"strip-canary-same-length", func(s string) string {
			// Replace a canary token with same-length filler.
			return strings.Replace(s, "sk-LEAK", "xx-XXXX", 1)
		}},
		{"valid-prefix-only", func(s string) string {
			return temporalOverlongPrefix
		}},
		{"empty", func(string) string {
			return ""
		}},
	}
	for _, m := range mutations {
		mutated := m.mutate(base)
		proof := temporalVerifyWireTimestamp(mutated)
		if proof.Verified {
			t.Errorf("%s: verifier accepted a mutated argument (%d bytes)", m.name, len(mutated))
		}
	}
	// Control: the pristine argument is the only accepted input.
	if proof := temporalVerifyWireTimestamp(base); !proof.Verified {
		t.Fatalf("verifier rejected the pristine argument: %+v", proof)
	}
}

// TestTemporalStdioWireSuccessShapePinned drives real stdio success calls and
// pins both the byte shape of deterministic success text and the semantic
// shape of JSON success payloads.
func TestTemporalStdioWireSuccessShapePinned(t *testing.T) {
	if os.Getenv(temporalStdioChildEnv) == "1" {
		return
	}
	child := startTemporalWireChild(t, temporalStdioModeNormal)
	if child.proof.Seam != "seeded-finite-metrics-row" {
		t.Fatalf("normal child seam = %q, want seeded-finite-metrics-row", child.proof.Seam)
	}

	// record_operation success is byte-exact.
	res := child.call(t, "cortex_temporal_record_operation", map[string]interface{}{
		"session_id":     "sess-wire-ok",
		"operation_type": "search",
		"timestamp":      "2025-06-15T10:00:00Z",
	})
	if res.IsError {
		t.Fatalf("unexpected wire error: %q", temporalWireResultText(t, res))
	}
	if got := temporalWireResultText(t, res); got != "Operation recorded successfully" {
		t.Errorf("wire success bytes drifted: %q", got)
	}

	// get_edges over an empty graph is byte-exact: the historical empty
	// serialization is the JSON literal "null" (json.Marshal of a nil
	// []*domain.Edge). "[]" is NOT acceptable.
	res = child.call(t, "cortex_temporal_get_edges", map[string]interface{}{
		"observation_id": float64(1), "at": "2025-06-15T00:00:00Z",
	})
	if res.IsError {
		t.Fatalf("unexpected wire error: %q", temporalWireResultText(t, res))
	}
	if got := temporalWireResultText(t, res); got != "null" {
		t.Errorf("wire empty-edges bytes must be exactly \"null\", got %q", got)
	}
	// Zero-edge semantic decode is asserted separately from the byte pin.
	var edges []map[string]interface{}
	if err := json.Unmarshal([]byte(temporalWireResultText(t, res)), &edges); err != nil {
		t.Fatalf("wire empty-edges must decode as a JSON array/null value: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("wire empty-edges decoded %d entries", len(edges))
	}

	// system_metrics succeeds and reflects the seeded finite row (proves the
	// aggregation path executed with real data, not an empty shortcut).
	res = child.call(t, "cortex_temporal_system_metrics", map[string]interface{}{
		"session_id": "sess-wire-ok", "from": "2000-01-01T00:00:00Z", "to": "2100-01-01T00:00:00Z",
	})
	if res.IsError {
		t.Fatalf("unexpected wire error: %q", temporalWireResultText(t, res))
	}
	var sys map[string]interface{}
	if err := json.Unmarshal([]byte(temporalWireResultText(t, res)), &sys); err != nil {
		t.Fatalf("wire system metrics must decode: %v", err)
	}
	if total, _ := sys["total_operations"].(float64); total < 1 {
		t.Errorf("wire system metrics must reflect the seeded row, got %v", sys)
	}
}

// TestTemporalStdioWireOverlongTimestampBounded crosses a genuinely overlong
// canary timestamp through real stdio framing and proves the response stays
// constant, canary-free, and within the explicit bound.
func TestTemporalStdioWireOverlongTimestampBounded(t *testing.T) {
	if os.Getenv(temporalStdioChildEnv) == "1" {
		return
	}
	child := startTemporalWireChild(t, temporalStdioModeNormal)

	attackArg := temporalOverlongTimestampCanary()
	res := child.call(t, "cortex_temporal_record_operation", map[string]interface{}{
		"session_id":     "sess-wire-overlong",
		"operation_type": "search",
		"timestamp":      attackArg,
	})

	// Blocker 1: require the child's pre-handler record of the exact
	// wire-received argument and prove the FULL >= 16 KiB payload crossed
	// stdio byte-identically before the production handler executed.
	proof := child.awaitArgumentProof(t)
	expRunes := utf8.RuneCountInString(attackArg)
	if proof.ReceivedRunes < temporalWireOverlongRunes {
		t.Fatalf("received argument is not overlong: %d runes < %d", proof.ReceivedRunes, temporalWireOverlongRunes)
	}
	if proof.ReceivedBytes != len(attackArg) {
		t.Errorf("wire argument byte length differs: received %d, sent %d", proof.ReceivedBytes, len(attackArg))
	}
	if proof.ReceivedRunes != expRunes {
		t.Errorf("wire argument rune count differs: received %d, sent %d", proof.ReceivedRunes, expRunes)
	}
	if proof.Digest != temporalSHA256Hex(attackArg) {
		t.Errorf("wire argument digest differs: received %s, sent %s", proof.Digest, temporalSHA256Hex(attackArg))
	}
	if len(proof.Missing) != 0 {
		t.Errorf("wire argument is missing required fragments: %v", proof.Missing)
	}
	if !proof.PrefixIntact {
		t.Error("wire argument RFC3339 prefix was truncated or damaged")
	}
	if !proof.LengthOk || !proof.RuneCountOk || !proof.DigestOk || !proof.Verified {
		t.Errorf("child-side integrity verdict failed: %+v", proof)
	}

	if !res.IsError {
		t.Fatalf("expected IsError for overlong canary timestamp, got %q", temporalWireResultText(t, res))
	}
	text := temporalWireResultText(t, res)
	if !strings.Contains(text, "Invalid timestamp format") {
		t.Errorf("expected constant validation text, got %q", text)
	}
	if !strings.Contains(text, "[code: validation]") {
		t.Errorf("expected stable validation code tag, got %q", text)
	}
	assertNoTemporalWireLeak(t, "wire/overlong-timestamp", text)
	assertTemporalWireBounded(t, "wire/overlong-timestamp", text)

	// Non-vacuity control: the identical request with only the valid RFC3339
	// prefix succeeds in the same child, proving the argument — not the
	// request shape — caused the validation failure.
	control := child.call(t, "cortex_temporal_record_operation", map[string]interface{}{
		"session_id":     "sess-wire-overlong-control",
		"operation_type": "search",
		"timestamp":      temporalOverlongPrefix,
	})
	if control.IsError {
		t.Fatalf("valid control call must succeed: %q", temporalWireResultText(t, control))
	}
	if got := temporalWireResultText(t, control); got != "Operation recorded successfully" {
		t.Errorf("control success bytes drifted: %q", got)
	}
}

// TestTemporalStdioWireBackendCanaryRedacted injects a backend failure whose
// raw error text carries SQL/DSN/token/path/URL/IP/body canaries and proves
// the wire response stays constant, canary-free, coded, and bounded — while
// the identical request succeeds against the normal backend.
func TestTemporalStdioWireBackendCanaryRedacted(t *testing.T) {
	if os.Getenv(temporalStdioChildEnv) == "1" {
		return
	}
	poisoned := startTemporalWireChild(t, temporalStdioModePoisoned)
	if poisoned.proof.Seam != "metrics-check-constraint-canary" {
		t.Fatalf("poisoned child seam = %q, want metrics-check-constraint-canary", poisoned.proof.Seam)
	}

	attack := map[string]interface{}{
		"session_id":     "sess-wire-poison",
		"operation_type": "search",
		"timestamp":      "2025-06-15T10:00:00Z",
	}
	res := poisoned.call(t, "cortex_temporal_record_operation", attack)
	if !res.IsError {
		t.Fatalf("expected IsError from poisoned backend, got %q", temporalWireResultText(t, res))
	}
	text := temporalWireResultText(t, res)
	if !strings.Contains(text, "Failed to record operation") {
		t.Errorf("expected constant backend-failure text, got %q", text)
	}
	if !strings.Contains(text, "[code: ") {
		t.Errorf("expected stable classification code tag, got %q", text)
	}
	assertNoTemporalWireLeak(t, "wire/backend-canary", text)
	assertTemporalWireBounded(t, "wire/backend-canary", text)

	// Differential control: identical request against the normal backend.
	normal := startTemporalWireChild(t, temporalStdioModeNormal)
	res = normal.call(t, "cortex_temporal_record_operation", attack)
	if res.IsError {
		t.Fatalf("identical request must succeed on the normal backend: %q", temporalWireResultText(t, res))
	}
	if got := temporalWireResultText(t, res); got != "Operation recorded successfully" {
		t.Errorf("differential success bytes drifted: %q", got)
	}
}

// TestTemporalStdioWireSerializationFailureRedacted seeds an unserializable
// (+Inf) aggregate behind the real system_metrics handler and proves the
// marshal failure crosses real stdio as a constant, redacted, bounded error.
func TestTemporalStdioWireSerializationFailureRedacted(t *testing.T) {
	if os.Getenv(temporalStdioChildEnv) == "1" {
		return
	}
	marshal := startTemporalWireChild(t, temporalStdioModeMarshal)
	if marshal.proof.Seam != "metrics-inf-query-complexity" {
		t.Fatalf("marshal child seam = %q, want metrics-inf-query-complexity", marshal.proof.Seam)
	}

	res := marshal.call(t, "cortex_temporal_system_metrics", map[string]interface{}{
		"session_id": "sess-wire-marshal", "from": "2000-01-01T00:00:00Z", "to": "2100-01-01T00:00:00Z",
	})
	if !res.IsError {
		t.Fatalf("expected IsError from unserializable aggregate, got %q", temporalWireResultText(t, res))
	}
	text := temporalWireResultText(t, res)
	if !strings.Contains(text, "Failed to serialize metrics") {
		t.Errorf("expected constant serialization-failure text, got %q", text)
	}
	if !strings.Contains(text, "[code: internal]") {
		t.Errorf("expected stable internal code tag, got %q", text)
	}
	for _, fragment := range []string{"json:", "unsupported value", "+Inf"} {
		if strings.Contains(text, fragment) {
			t.Errorf("wire/serialize: raw marshal cause leaked %q: %q", fragment, text)
		}
	}
	assertNoTemporalWireLeak(t, "wire/serialize", text)
	assertTemporalWireBounded(t, "wire/serialize", text)

	// Differential control: the finite-row normal backend succeeds and only
	// the marshal branch emits the observed constant (a backend failure would
	// emit "Failed to get system metrics").
	normal := startTemporalWireChild(t, temporalStdioModeNormal)
	res = normal.call(t, "cortex_temporal_system_metrics", map[string]interface{}{
		"session_id": "sess-wire-ok", "from": "2000-01-01T00:00:00Z", "to": "2100-01-01T00:00:00Z",
	})
	if res.IsError {
		t.Fatalf("finite-row control must succeed: %q", temporalWireResultText(t, res))
	}
	var sys map[string]interface{}
	if err := json.Unmarshal([]byte(temporalWireResultText(t, res)), &sys); err != nil {
		t.Fatalf("finite-row control must decode: %v", err)
	}
	if total, _ := sys["total_operations"].(float64); total < 1 {
		t.Errorf("finite-row control must reflect the seeded row, got %v", sys)
	}
}

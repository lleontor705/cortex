package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/migration"
	"github.com/lleontor705/cortex/v2/testutil"
)

// setupRepositoryTestDB builds a fresh migrated in-memory SQLite database
// containing the metrics, quality_metrics, and temporal_snapshots tables,
// mirroring migration 009 (temporal_observability). Each test gets an isolated
// database with explicit cleanup.
func setupRepositoryTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1,
		Name:    "temporal_observability",
		UpSQL: `
			CREATE TABLE IF NOT EXISTS metrics (
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

			CREATE TABLE IF NOT EXISTS quality_metrics (
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
			);

			CREATE TABLE IF NOT EXISTS temporal_snapshots (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				snapshot_key TEXT NOT NULL,
				timestamp DATETIME NOT NULL,
				description TEXT,
				observation_count INTEGER NOT NULL DEFAULT 0 CHECK (observation_count >= 0),
				edge_count INTEGER NOT NULL DEFAULT 0 CHECK (edge_count >= 0),
				root_observation_id INTEGER,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				UNIQUE(snapshot_key, timestamp)
			);
		`,
		DownSQL: `
			DROP TABLE IF EXISTS temporal_snapshots;
			DROP TABLE IF EXISTS quality_metrics;
			DROP TABLE IF EXISTS metrics;
		`,
	})

	testDB := testutil.NewTestDBWithMigrations(t, registry)
	return testDB.DB(), func() { testDB.Cleanup() }
}

// mustParseTime parses a fixed RFC3339 UTC timestamp, failing the test on error.
func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("mustParseTime(%q): %v", value, err)
	}
	return ts
}

// --- MetricsRepository --------------------------------------------------------

func TestMetricsRepository_CreateAndGetTemporalMetrics(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewMetricsRepository(db)

	t1 := mustParseTime(t, "2024-01-15T10:00:00Z")
	t2 := mustParseTime(t, "2024-01-15T11:00:00Z")
	t3 := mustParseTime(t, "2024-01-15T12:00:00Z")
	from := mustParseTime(t, "2024-01-15T00:00:00Z")
	to := mustParseTime(t, "2024-01-15T23:59:59Z")

	records := []*domain.Metrics{
		{SessionID: "s1", OperationType: "save", Duration: 10, Success: true, Timestamp: t1, QueryComplexity: 0.2, ConfidenceScore: 0.5},
		{SessionID: "s1", OperationType: "search", Duration: 20, Success: true, Timestamp: t2, QueryComplexity: 0.4, ConfidenceScore: 0.6},
		{SessionID: "s1", OperationType: "get", Duration: 30, Success: true, Timestamp: t3, QueryComplexity: 0.8, ConfidenceScore: 0.9},
		{SessionID: "s2", OperationType: "save", Duration: 5, Success: true, Timestamp: t2, QueryComplexity: 0.1, ConfidenceScore: 0.3},
	}
	for _, m := range records {
		if err := repo.CreateMetric(ctx, m); err != nil {
			t.Fatalf("CreateMetric: %v", err)
		}
	}

	got, err := repo.GetTemporalMetrics(ctx, "s1", from, to)
	if err != nil {
		t.Fatalf("GetTemporalMetrics: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 metrics for s1, got %d", len(got))
	}
	// Ordering must be DESC by timestamp.
	if !got[0].Timestamp.Equal(t3) || !got[1].Timestamp.Equal(t2) || !got[2].Timestamp.Equal(t1) {
		t.Errorf("order = [%s, %s, %s], want DESC [%s, %s, %s]",
			got[0].Timestamp, got[1].Timestamp, got[2].Timestamp, t3, t2, t1)
	}
	// The s2 record must be excluded by the session filter.
	for _, m := range got {
		if m.SessionID != "s1" {
			t.Errorf("session filter leaked %q", m.SessionID)
		}
	}

	// Range filter: from=t1 to=t2 must exclude t3.
	got, err = repo.GetTemporalMetrics(ctx, "s1", t1, t2)
	if err != nil {
		t.Fatalf("GetTemporalMetrics range: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("range filter expected 2, got %d", len(got))
	}
	if !got[0].Timestamp.Equal(t2) || !got[1].Timestamp.Equal(t1) {
		t.Errorf("range order = [%s, %s], want [%s, %s]", got[0].Timestamp, got[1].Timestamp, t2, t1)
	}
}

func TestMetricsRepository_GetTemporalMetrics_EmptyAndDifferentSession(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewMetricsRepository(db)

	from := mustParseTime(t, "2024-01-15T00:00:00Z")
	to := mustParseTime(t, "2024-01-15T23:59:59Z")

	got, err := repo.GetTemporalMetrics(ctx, "nonexistent", from, to)
	if err != nil {
		t.Fatalf("GetTemporalMetrics on empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 metrics, got %d", len(got))
	}
}

func TestMetricsRepository_GetByOperationType(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewMetricsRepository(db)

	t1 := mustParseTime(t, "2024-01-15T10:00:00Z")
	t2 := mustParseTime(t, "2024-01-15T11:00:00Z")
	t3 := mustParseTime(t, "2024-01-15T12:00:00Z")
	from := mustParseTime(t, "2024-01-15T00:00:00Z")
	to := mustParseTime(t, "2024-01-15T23:59:59Z")

	records := []*domain.Metrics{
		{SessionID: "s1", OperationType: "save", Duration: 1, Success: true, Timestamp: t1, QueryComplexity: 0.1, ConfidenceScore: 0.5},
		{SessionID: "s1", OperationType: "search", Duration: 2, Success: true, Timestamp: t2, QueryComplexity: 0.1, ConfidenceScore: 0.5},
		{SessionID: "s1", OperationType: "save", Duration: 3, Success: true, Timestamp: t3, QueryComplexity: 0.1, ConfidenceScore: 0.5},
	}
	for _, m := range records {
		if err := repo.CreateMetric(ctx, m); err != nil {
			t.Fatalf("CreateMetric: %v", err)
		}
	}

	got, err := repo.GetByOperationType(ctx, "save", from, to)
	if err != nil {
		t.Fatalf("GetByOperationType: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 'save' metrics, got %d", len(got))
	}
	for _, m := range got {
		if m.OperationType != "save" {
			t.Errorf("type filter leaked %q", m.OperationType)
		}
	}
	// DESC order: newest first.
	if !got[0].Timestamp.Equal(t3) || !got[1].Timestamp.Equal(t1) {
		t.Errorf("order = [%s, %s], want DESC [%s, %s]", got[0].Timestamp, got[1].Timestamp, t3, t1)
	}
}

func TestMetricsRepository_GetAggregatedMetrics_EmptyReturnsError(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewMetricsRepository(db)

	from := mustParseTime(t, "2024-01-15T00:00:00Z")
	to := mustParseTime(t, "2024-01-15T23:59:59Z")

	// On an empty metrics table, the aggregate query's SUM(memory_usage_bytes)
	// returns SQL NULL, which cannot scan into the non-nullable int64 field.
	// This documents the existing edge-case behavior: an empty aggregate errors
	// rather than returning zeroed fields. Production is intentionally unchanged
	// (coverage-only change per design Decision 7).
	_, err := repo.GetAggregatedMetrics(ctx, from, to)
	if err == nil {
		t.Fatal("GetAggregatedMetrics on empty table: expected error (NULL SUM into int64), got nil")
	}
}

func TestMetricsRepository_GetAggregatedMetrics_WithData(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewMetricsRepository(db)
	ts := mustParseTime(t, "2024-01-15T10:00:00Z")
	from := mustParseTime(t, "2024-01-15T00:00:00Z")
	to := mustParseTime(t, "2024-01-15T23:59:59Z")

	records := []*domain.Metrics{
		{SessionID: "s1", OperationType: "save", Duration: 100, Success: true, MemoryUsage: 1024, Timestamp: ts, ObservationCount: 10, EdgeCount: 2, QueryComplexity: 0.5, ConfidenceScore: 0.8},
		{SessionID: "s1", OperationType: "save", Duration: 200, Success: false, Error: "boom", MemoryUsage: 2048, Timestamp: ts, ObservationCount: 10, EdgeCount: 2, QueryComplexity: 0.5, ConfidenceScore: 0.8},
	}
	for _, m := range records {
		if err := repo.CreateMetric(ctx, m); err != nil {
			t.Fatalf("CreateMetric: %v", err)
		}
	}

	agg, err := repo.GetAggregatedMetrics(ctx, from, to)
	if err != nil {
		t.Fatalf("GetAggregatedMetrics: %v", err)
	}
	if agg.TotalOperations != 2 {
		t.Errorf("TotalOperations = %d, want 2", agg.TotalOperations)
	}
	if agg.SuccessfulOps != 1 {
		t.Errorf("SuccessfulOps = %d, want 1", agg.SuccessfulOps)
	}
	if agg.FailedOps != 1 {
		t.Errorf("FailedOps = %d, want 1", agg.FailedOps)
	}
	if agg.AvgDurationMs != 150 {
		t.Errorf("AvgDurationMs = %v, want 150", agg.AvgDurationMs)
	}
	if agg.TotalMemoryUsage != 3072 {
		t.Errorf("TotalMemoryUsage = %d, want 3072", agg.TotalMemoryUsage)
	}
}

func TestMetricsRepository_NullableErrorRoundTrip(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewMetricsRepository(db)
	from := mustParseTime(t, "2024-01-15T00:00:00Z")
	to := mustParseTime(t, "2024-01-15T23:59:59Z")

	withErr := &domain.Metrics{SessionID: "s1", OperationType: "save", Duration: 1, Success: false, Error: "boom", Timestamp: mustParseTime(t, "2024-01-15T10:00:00Z"), QueryComplexity: 0.1, ConfidenceScore: 0.5}
	noErr := &domain.Metrics{SessionID: "s1", OperationType: "save", Duration: 1, Success: true, Timestamp: mustParseTime(t, "2024-01-15T11:00:00Z"), QueryComplexity: 0.1, ConfidenceScore: 0.5}
	if err := repo.CreateMetric(ctx, withErr); err != nil {
		t.Fatalf("CreateMetric withErr: %v", err)
	}
	if err := repo.CreateMetric(ctx, noErr); err != nil {
		t.Fatalf("CreateMetric noErr: %v", err)
	}

	got, err := repo.GetTemporalMetrics(ctx, "s1", from, to)
	if err != nil {
		t.Fatalf("GetTemporalMetrics: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	// DESC order: noErr (11:00) first, withErr (10:00) second.
	if got[0].Error != "" {
		t.Errorf("first metric Error = %q, want empty (nullable)", got[0].Error)
	}
	if got[1].Error != "boom" {
		t.Errorf("second metric Error = %q, want %q", got[1].Error, "boom")
	}
}

// --- QualityMetricsRepository -------------------------------------------------

func TestQualityMetricsRepository_CreateAndGetBySession(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewQualityMetricsRepository(db)

	ea1 := mustParseTime(t, "2024-02-01T10:00:00Z")
	ea2 := mustParseTime(t, "2024-02-01T11:00:00Z")

	records := []*domain.QualityMetrics{
		{SessionID: "s1", EvaluationType: "relevance", Score: 0.8, TotalQueries: 10, SuccessfulRetrievals: 8, AverageLatency: 50, AverageRelevance: 0.7, TemporalAccuracy: 0.6, KnowledgeCoverage: 0.9, EvaluatedAt: ea1},
		{SessionID: "s1", EvaluationType: "completeness", Score: 0.6, TotalQueries: 5, SuccessfulRetrievals: 3, AverageLatency: 30, AverageRelevance: 0.5, TemporalAccuracy: 0.4, KnowledgeCoverage: 0.5, EvaluatedAt: ea2},
		{SessionID: "s2", EvaluationType: "relevance", Score: 0.9, TotalQueries: 1, SuccessfulRetrievals: 1, AverageLatency: 10, AverageRelevance: 0.9, TemporalAccuracy: 0.9, KnowledgeCoverage: 0.9, EvaluatedAt: ea1},
	}
	for _, q := range records {
		if err := repo.CreateQualityMetric(ctx, q); err != nil {
			t.Fatalf("CreateQualityMetric: %v", err)
		}
	}

	got, err := repo.GetBySession(ctx, "s1", 10)
	if err != nil {
		t.Fatalf("GetBySession: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 for s1, got %d", len(got))
	}
	// DESC by evaluated_at: ea2 first, ea1 second.
	if !got[0].EvaluatedAt.Equal(ea2) || !got[1].EvaluatedAt.Equal(ea1) {
		t.Errorf("order = [%s, %s], want DESC [%s, %s]", got[0].EvaluatedAt, got[1].EvaluatedAt, ea2, ea1)
	}
	if got[0].EvaluationType != "completeness" {
		t.Errorf("first type = %q, want %q", got[0].EvaluationType, "completeness")
	}
}

func TestQualityMetricsRepository_GetBySession_LimitAndEmpty(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewQualityMetricsRepository(db)

	ea1 := mustParseTime(t, "2024-02-01T10:00:00Z")
	ea2 := mustParseTime(t, "2024-02-01T11:00:00Z")
	ea3 := mustParseTime(t, "2024-02-01T12:00:00Z")
	for _, ea := range []time.Time{ea1, ea2, ea3} {
		q := &domain.QualityMetrics{SessionID: "s1", EvaluationType: "relevance", Score: 0.5, TotalQueries: 1, SuccessfulRetrievals: 1, AverageLatency: 1, EvaluatedAt: ea}
		if err := repo.CreateQualityMetric(ctx, q); err != nil {
			t.Fatalf("CreateQualityMetric: %v", err)
		}
	}

	// Limit returns the 2 most recent.
	got, err := repo.GetBySession(ctx, "s1", 2)
	if err != nil {
		t.Fatalf("GetBySession limit: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 with limit, got %d", len(got))
	}
	if !got[0].EvaluatedAt.Equal(ea3) || !got[1].EvaluatedAt.Equal(ea2) {
		t.Errorf("limit order = [%s, %s], want [%s, %s]", got[0].EvaluatedAt, got[1].EvaluatedAt, ea3, ea2)
	}

	// Empty for a session with no metrics.
	got, err = repo.GetBySession(ctx, "nope", 10)
	if err != nil {
		t.Fatalf("GetBySession empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0, got %d", len(got))
	}
}

func TestQualityMetricsRepository_GetByType(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewQualityMetricsRepository(db)

	ea1 := mustParseTime(t, "2024-02-01T10:00:00Z")
	ea2 := mustParseTime(t, "2024-02-01T11:00:00Z")
	ea3 := mustParseTime(t, "2024-02-01T12:00:00Z")
	from := mustParseTime(t, "2024-02-01T00:00:00Z")
	to := mustParseTime(t, "2024-02-01T23:59:59Z")

	records := []*domain.QualityMetrics{
		{SessionID: "s1", EvaluationType: "relevance", Score: 0.5, TotalQueries: 1, SuccessfulRetrievals: 1, AverageLatency: 1, EvaluatedAt: ea1},
		{SessionID: "s1", EvaluationType: "completeness", Score: 0.5, TotalQueries: 1, SuccessfulRetrievals: 1, AverageLatency: 1, EvaluatedAt: ea2},
		{SessionID: "s1", EvaluationType: "relevance", Score: 0.5, TotalQueries: 1, SuccessfulRetrievals: 1, AverageLatency: 1, EvaluatedAt: ea3},
	}
	for _, q := range records {
		if err := repo.CreateQualityMetric(ctx, q); err != nil {
			t.Fatalf("CreateQualityMetric: %v", err)
		}
	}

	got, err := repo.GetByType(ctx, "relevance", from, to)
	if err != nil {
		t.Fatalf("GetByType: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 'relevance', got %d", len(got))
	}
	for _, m := range got {
		if m.EvaluationType != "relevance" {
			t.Errorf("type filter leaked %q", m.EvaluationType)
		}
	}
	// DESC: ea3 then ea1.
	if !got[0].EvaluatedAt.Equal(ea3) || !got[1].EvaluatedAt.Equal(ea1) {
		t.Errorf("order = [%s, %s], want DESC [%s, %s]", got[0].EvaluatedAt, got[1].EvaluatedAt, ea3, ea1)
	}
}

func TestQualityMetricsRepository_GetLatest(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewQualityMetricsRepository(db)

	ea1 := mustParseTime(t, "2024-02-01T10:00:00Z")
	ea2 := mustParseTime(t, "2024-02-01T11:00:00Z")
	ea3 := mustParseTime(t, "2024-02-01T12:00:00Z")
	for i, ea := range []time.Time{ea1, ea2, ea3} {
		q := &domain.QualityMetrics{SessionID: "s" + string(rune('A'+i)), EvaluationType: "relevance", Score: 0.5, TotalQueries: 1, SuccessfulRetrievals: 1, AverageLatency: 1, EvaluatedAt: ea}
		if err := repo.CreateQualityMetric(ctx, q); err != nil {
			t.Fatalf("CreateQualityMetric: %v", err)
		}
	}

	got, err := repo.GetLatest(ctx, 2)
	if err != nil {
		t.Fatalf("GetLatest: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if !got[0].EvaluatedAt.Equal(ea3) || !got[1].EvaluatedAt.Equal(ea2) {
		t.Errorf("order = [%s, %s], want DESC [%s, %s]", got[0].EvaluatedAt, got[1].EvaluatedAt, ea3, ea2)
	}

	// Empty store returns empty.
	got2, err := repo.GetLatest(ctx, 0)
	if err != nil {
		t.Fatalf("GetLatest zero limit: %v", err)
	}
	if len(got2) != 0 {
		t.Errorf("expected 0 with limit 0, got %d", len(got2))
	}
}

// --- TemporalSnapshotRepository ----------------------------------------------

func TestTemporalSnapshotRepository_CreateAndGetByID(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewTemporalSnapshotRepository(db)

	ts := mustParseTime(t, "2024-03-01T10:00:00Z")
	snap := &domain.TemporalSnapshot{
		SnapshotKey:       "snap-1",
		Timestamp:         ts,
		Description:       "initial graph",
		ObservationCount:  10,
		EdgeCount:         5,
		RootObservationID: 7,
	}

	if err := repo.CreateSnapshot(ctx, snap); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if snap.ID == 0 {
		t.Fatal("CreateSnapshot should set the returned ID")
	}

	got, err := repo.GetByID(ctx, snap.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != snap.ID {
		t.Errorf("ID = %d, want %d", got.ID, snap.ID)
	}
	if got.SnapshotKey != "snap-1" {
		t.Errorf("SnapshotKey = %q, want %q", got.SnapshotKey, "snap-1")
	}
	if got.Description != "initial graph" {
		t.Errorf("Description = %q, want %q", got.Description, "initial graph")
	}
	if got.ObservationCount != 10 {
		t.Errorf("ObservationCount = %d, want 10", got.ObservationCount)
	}
	if got.EdgeCount != 5 {
		t.Errorf("EdgeCount = %d, want 5", got.EdgeCount)
	}
	if got.RootObservationID != 7 {
		t.Errorf("RootObservationID = %d, want 7", got.RootObservationID)
	}
	if !got.Timestamp.Equal(ts) {
		t.Errorf("Timestamp = %s, want %s", got.Timestamp, ts)
	}
}

func TestTemporalSnapshotRepository_GetByID_NotFound(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewTemporalSnapshotRepository(db)

	_, err := repo.GetByID(ctx, 9999)
	if err == nil {
		t.Fatal("GetByID expected error for missing id, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetByID error = %v, want sql.ErrNoRows", err)
	}
}

func TestTemporalSnapshotRepository_GetByID_NullableRoot(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewTemporalSnapshotRepository(db)

	// Insert a snapshot directly with a NULL root_observation_id to exercise the
	// nullable fallback path in GetByID.
	res, err := db.ExecContext(ctx, `
		INSERT INTO temporal_snapshots (snapshot_key, timestamp, description, observation_count, edge_count, root_observation_id)
		VALUES (?, ?, ?, ?, ?, NULL)
	`, "null-root", mustParseTime(t, "2024-03-01T10:00:00Z"), "no root", 1, 0)
	if err != nil {
		t.Fatalf("direct insert: %v", err)
	}
	id, _ := res.LastInsertId()

	got, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.RootObservationID != 0 {
		t.Errorf("nullable root RootObservationID = %d, want 0 (fallback)", got.RootObservationID)
	}
	if got.Description != "no root" {
		t.Errorf("Description = %q, want %q", got.Description, "no root")
	}
}

func TestTemporalSnapshotRepository_GetBySnapshotKey(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewTemporalSnapshotRepository(db)

	t1 := mustParseTime(t, "2024-03-01T10:00:00Z")
	t3 := mustParseTime(t, "2024-03-01T12:00:00Z")

	snaps := []*domain.TemporalSnapshot{
		{SnapshotKey: "k", Timestamp: t1, ObservationCount: 1},
		{SnapshotKey: "k", Timestamp: t3, ObservationCount: 2},
		{SnapshotKey: "other", Timestamp: t3, ObservationCount: 3},
	}
	for _, s := range snaps {
		if err := repo.CreateSnapshot(ctx, s); err != nil {
			t.Fatalf("CreateSnapshot: %v", err)
		}
	}

	got, err := repo.GetBySnapshotKey(ctx, "k")
	if err != nil {
		t.Fatalf("GetBySnapshotKey: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 for key 'k', got %d", len(got))
	}
	// DESC by timestamp: t3 first, t1 second.
	if !got[0].Timestamp.Equal(t3) || !got[1].Timestamp.Equal(t1) {
		t.Errorf("order = [%s, %s], want DESC [%s, %s]", got[0].Timestamp, got[1].Timestamp, t3, t1)
	}
}

func TestTemporalSnapshotRepository_GetSnapshotsInRange(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewTemporalSnapshotRepository(db)

	t1 := mustParseTime(t, "2024-03-01T10:00:00Z")
	t2 := mustParseTime(t, "2024-03-01T11:00:00Z")
	t3 := mustParseTime(t, "2024-03-01T12:00:00Z")

	for i, ts := range []time.Time{t1, t2, t3} {
		s := &domain.TemporalSnapshot{SnapshotKey: "snap-" + string(rune('A'+i)), Timestamp: ts, ObservationCount: i}
		if err := repo.CreateSnapshot(ctx, s); err != nil {
			t.Fatalf("CreateSnapshot: %v", err)
		}
	}

	// Range [t1, t2] excludes t3.
	got, err := repo.GetSnapshotsInRange(ctx, t1, t2)
	if err != nil {
		t.Fatalf("GetSnapshotsInRange: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 in range, got %d", len(got))
	}
	if !got[0].Timestamp.Equal(t2) || !got[1].Timestamp.Equal(t1) {
		t.Errorf("order = [%s, %s], want DESC [%s, %s]", got[0].Timestamp, got[1].Timestamp, t2, t1)
	}
}

func TestTemporalSnapshotRepository_GetByRootObservation(t *testing.T) {
	db, cleanup := setupRepositoryTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewTemporalSnapshotRepository(db)

	t1 := mustParseTime(t, "2024-03-01T10:00:00Z")
	t2 := mustParseTime(t, "2024-03-01T11:00:00Z")
	t3 := mustParseTime(t, "2024-03-01T12:00:00Z")

	snaps := []*domain.TemporalSnapshot{
		{SnapshotKey: "a", Timestamp: t1, RootObservationID: 5, ObservationCount: 1},
		{SnapshotKey: "b", Timestamp: t2, RootObservationID: 9, ObservationCount: 1},
		{SnapshotKey: "c", Timestamp: t3, RootObservationID: 5, ObservationCount: 2},
	}
	for _, s := range snaps {
		if err := repo.CreateSnapshot(ctx, s); err != nil {
			t.Fatalf("CreateSnapshot: %v", err)
		}
	}

	got, err := repo.GetByRootObservation(ctx, 5)
	if err != nil {
		t.Fatalf("GetByRootObservation: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 for root 5, got %d", len(got))
	}
	// DESC: t3 then t1.
	if !got[0].Timestamp.Equal(t3) || !got[1].Timestamp.Equal(t1) {
		t.Errorf("order = [%s, %s], want DESC [%s, %s]", got[0].Timestamp, got[1].Timestamp, t3, t1)
	}
}

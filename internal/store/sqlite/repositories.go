// Package sqlite provides concrete implementations of repository interfaces
// for SQLite-based persistence in Cortex.
//
// This package contains the actual database implementations that bridge
// the domain models with SQLite storage, implementing all the repository
// interfaces defined in the domain package.
package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// MetricsRepository implements the MetricsRepository interface using SQLite.
type MetricsRepository struct {
	db *sql.DB
}

// NewMetricsRepository creates a new metrics repository with the given database connection.
func NewMetricsRepository(db *sql.DB) *MetricsRepository {
	return &MetricsRepository{db: db}
}

// CreateMetric records a performance metric.
func (r *MetricsRepository) CreateMetric(ctx context.Context, metric *domain.Metrics) error {
	query := `
		INSERT INTO metrics 
		(session_id, operation_type, duration_ms, result_count, success, error, memory_usage_bytes, 
		 timestamp, observation_count, edge_count, query_complexity, confidence_score)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := r.db.ExecContext(ctx, query,
		metric.SessionID,
		metric.OperationType,
		metric.Duration,
		metric.ResultCount,
		metric.Success,
		metric.Error,
		metric.MemoryUsage,
		metric.Timestamp,
		metric.ObservationCount,
		metric.EdgeCount,
		metric.QueryComplexity,
		metric.ConfidenceScore,
	)

	return err
}

// GetTemporalMetrics retrieves metrics for a session within a time range.
func (r *MetricsRepository) GetTemporalMetrics(ctx context.Context, sessionID string, from, to time.Time) ([]*domain.Metrics, error) {
	query := `
		SELECT id, session_id, operation_type, duration_ms, result_count, success, error,
		       memory_usage_bytes, timestamp, observation_count, edge_count, 
		       query_complexity, confidence_score
		FROM metrics 
		WHERE session_id = ? AND timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp DESC
	`

	rows, err := r.db.QueryContext(ctx, query, sessionID, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var metrics []*domain.Metrics
	for rows.Next() {
		metric := &domain.Metrics{}
		var errorPtr sql.NullString

		err := rows.Scan(
			&metric.ID,
			&metric.SessionID,
			&metric.OperationType,
			&metric.Duration,
			&metric.ResultCount,
			&metric.Success,
			&errorPtr,
			&metric.MemoryUsage,
			&metric.Timestamp,
			&metric.ObservationCount,
			&metric.EdgeCount,
			&metric.QueryComplexity,
			&metric.ConfidenceScore,
		)
		if err != nil {
			return nil, err
		}

		if errorPtr.Valid {
			metric.Error = errorPtr.String
		}

		metrics = append(metrics, metric)
	}

	return metrics, nil
}

// GetByOperationType retrieves metrics filtered by operation type.
func (r *MetricsRepository) GetByOperationType(ctx context.Context, operationType string, from, to time.Time) ([]*domain.Metrics, error) {
	query := `
		SELECT id, session_id, operation_type, duration_ms, result_count, success, error,
		       memory_usage_bytes, timestamp, observation_count, edge_count,
		       query_complexity, confidence_score
		FROM metrics
		WHERE operation_type = ? AND timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp DESC
	`

	rows, err := r.db.QueryContext(ctx, query, operationType, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var metrics []*domain.Metrics
	for rows.Next() {
		metric := &domain.Metrics{}
		var errorPtr sql.NullString

		err := rows.Scan(
			&metric.ID,
			&metric.SessionID,
			&metric.OperationType,
			&metric.Duration,
			&metric.ResultCount,
			&metric.Success,
			&errorPtr,
			&metric.MemoryUsage,
			&metric.Timestamp,
			&metric.ObservationCount,
			&metric.EdgeCount,
			&metric.QueryComplexity,
			&metric.ConfidenceScore,
		)
		if err != nil {
			return nil, err
		}

		if errorPtr.Valid {
			metric.Error = errorPtr.String
		}

		metrics = append(metrics, metric)
	}

	return metrics, nil
}

// GetAggregatedMetrics gets aggregated metrics for a time range.
func (r *MetricsRepository) GetAggregatedMetrics(ctx context.Context, from, to time.Time) (*domain.AggregatedMetrics, error) {
	query := `
		SELECT 
			COUNT(*) as total_operations,
			COUNT(CASE WHEN success = 1 THEN 1 END) as successful_ops,
			COUNT(CASE WHEN success = 0 THEN 1 END) as failed_ops,
			AVG(duration_ms) as avg_duration_ms,
			SUM(memory_usage_bytes) as total_memory_usage,
			AVG(observation_count) as avg_observation_count,
			AVG(edge_count) as avg_edge_count,
			AVG(query_complexity) as avg_query_complexity,
			AVG(confidence_score) as avg_confidence_score
		FROM metrics 
		WHERE timestamp >= ? AND timestamp <= ?
	`

	row := r.db.QueryRowContext(ctx, query, from, to)

	agg := &domain.AggregatedMetrics{}
	var avgDuration, avgObsCount, avgEdgeCount, avgComplexity, avgConfidence sql.NullFloat64

	err := row.Scan(
		&agg.TotalOperations,
		&agg.SuccessfulOps,
		&agg.FailedOps,
		&avgDuration,
		&agg.TotalMemoryUsage,
		&avgObsCount,
		&avgEdgeCount,
		&avgComplexity,
		&avgConfidence,
	)
	if err != nil {
		return nil, err
	}

	if avgDuration.Valid {
		agg.AvgDurationMs = avgDuration.Float64
	}
	if avgObsCount.Valid {
		agg.AvgObservationCount = avgObsCount.Float64
	}
	if avgEdgeCount.Valid {
		agg.AvgEdgeCount = avgEdgeCount.Float64
	}
	if avgComplexity.Valid {
		agg.AvgQueryComplexity = avgComplexity.Float64
	}
	if avgConfidence.Valid {
		agg.AvgConfidenceScore = avgConfidence.Float64
	}

	agg.TimeRange = &domain.TimeRange{From: from, To: to}
	agg.EvaluatedAt = time.Now()

	return agg, nil
}

// QualityMetricsRepository implements the QualityMetricsRepository interface.
type QualityMetricsRepository struct {
	db *sql.DB
}

// NewQualityMetricsRepository creates a new quality metrics repository.
func NewQualityMetricsRepository(db *sql.DB) *QualityMetricsRepository {
	return &QualityMetricsRepository{db: db}
}

// CreateQualityMetric records a quality evaluation result.
func (r *QualityMetricsRepository) CreateQualityMetric(ctx context.Context, quality *domain.QualityMetrics) error {
	query := `
		INSERT INTO quality_metrics 
		(session_id, evaluation_type, score, total_queries, successful_retrievals, 
		 average_latency_ms, average_relevance, temporal_accuracy, knowledge_coverage, evaluated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := r.db.ExecContext(ctx, query,
		quality.SessionID,
		quality.EvaluationType,
		quality.Score,
		quality.TotalQueries,
		quality.SuccessfulRetrievals,
		quality.AverageLatency,
		quality.AverageRelevance,
		quality.TemporalAccuracy,
		quality.KnowledgeCoverage,
		quality.EvaluatedAt,
	)

	return err
}

// GetBySession retrieves quality metrics for a session.
func (r *QualityMetricsRepository) GetBySession(ctx context.Context, sessionID string, limit int) ([]*domain.QualityMetrics, error) {
	query := `
		SELECT id, session_id, evaluation_type, score, total_queries, successful_retrievals,
		       average_latency_ms, average_relevance, temporal_accuracy, knowledge_coverage, evaluated_at
		FROM quality_metrics 
		WHERE session_id = ?
		ORDER BY evaluated_at DESC
		LIMIT ?
	`

	rows, err := r.db.QueryContext(ctx, query, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var metrics []*domain.QualityMetrics
	for rows.Next() {
		metric := &domain.QualityMetrics{}

		err := rows.Scan(
			&metric.ID,
			&metric.SessionID,
			&metric.EvaluationType,
			&metric.Score,
			&metric.TotalQueries,
			&metric.SuccessfulRetrievals,
			&metric.AverageLatency,
			&metric.AverageRelevance,
			&metric.TemporalAccuracy,
			&metric.KnowledgeCoverage,
			&metric.EvaluatedAt,
		)
		if err != nil {
			return nil, err
		}

		metrics = append(metrics, metric)
	}

	return metrics, nil
}

// GetByType retrieves quality metrics filtered by evaluation type.
func (r *QualityMetricsRepository) GetByType(ctx context.Context, evaluationType string, from, to time.Time) ([]*domain.QualityMetrics, error) {
	query := `
		SELECT id, session_id, evaluation_type, score, total_queries, successful_retrievals,
		       average_latency_ms, average_relevance, temporal_accuracy, knowledge_coverage, evaluated_at
		FROM quality_metrics 
		WHERE evaluation_type = ? AND evaluated_at >= ? AND evaluated_at <= ?
		ORDER BY evaluated_at DESC
	`

	rows, err := r.db.QueryContext(ctx, query, evaluationType, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var metrics []*domain.QualityMetrics
	for rows.Next() {
		metric := &domain.QualityMetrics{}

		err := rows.Scan(
			&metric.ID,
			&metric.SessionID,
			&metric.EvaluationType,
			&metric.Score,
			&metric.TotalQueries,
			&metric.SuccessfulRetrievals,
			&metric.AverageLatency,
			&metric.AverageRelevance,
			&metric.TemporalAccuracy,
			&metric.KnowledgeCoverage,
			&metric.EvaluatedAt,
		)
		if err != nil {
			return nil, err
		}

		metrics = append(metrics, metric)
	}

	return metrics, nil
}

// GetLatest gets the most recent quality metrics.
func (r *QualityMetricsRepository) GetLatest(ctx context.Context, limit int) ([]*domain.QualityMetrics, error) {
	query := `
		SELECT id, session_id, evaluation_type, score, total_queries, successful_retrievals,
		       average_latency_ms, average_relevance, temporal_accuracy, knowledge_coverage, evaluated_at
		FROM quality_metrics 
		ORDER BY evaluated_at DESC
		LIMIT ?
	`

	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var metrics []*domain.QualityMetrics
	for rows.Next() {
		metric := &domain.QualityMetrics{}

		err := rows.Scan(
			&metric.ID,
			&metric.SessionID,
			&metric.EvaluationType,
			&metric.Score,
			&metric.TotalQueries,
			&metric.SuccessfulRetrievals,
			&metric.AverageLatency,
			&metric.AverageRelevance,
			&metric.TemporalAccuracy,
			&metric.KnowledgeCoverage,
			&metric.EvaluatedAt,
		)
		if err != nil {
			return nil, err
		}

		metrics = append(metrics, metric)
	}

	return metrics, nil
}

// TemporalSnapshotRepository implements the TemporalSnapshotRepository interface.
type TemporalSnapshotRepository struct {
	db *sql.DB
}

// NewTemporalSnapshotRepository creates a new temporal snapshot repository.
func NewTemporalSnapshotRepository(db *sql.DB) *TemporalSnapshotRepository {
	return &TemporalSnapshotRepository{db: db}
}

// CreateSnapshot creates a point-in-time snapshot of the knowledge graph.
func (r *TemporalSnapshotRepository) CreateSnapshot(ctx context.Context, snapshot *domain.TemporalSnapshot) error {
	query := `
		INSERT INTO temporal_snapshots 
		(snapshot_key, timestamp, description, observation_count, edge_count, root_observation_id)
		VALUES (?, ?, ?, ?, ?, ?)
	`

	result, err := r.db.ExecContext(ctx, query,
		snapshot.SnapshotKey,
		snapshot.Timestamp,
		snapshot.Description,
		snapshot.ObservationCount,
		snapshot.EdgeCount,
		snapshot.RootObservationID,
	)
	if err != nil {
		return err
	}

	id, _ := result.LastInsertId()
	snapshot.ID = id

	return nil
}

// GetByID retrieves a snapshot by its ID.
func (r *TemporalSnapshotRepository) GetByID(ctx context.Context, id int64) (*domain.TemporalSnapshot, error) {
	query := `
		SELECT id, snapshot_key, timestamp, description, observation_count, edge_count, root_observation_id
		FROM temporal_snapshots 
		WHERE id = ?
	`

	row := r.db.QueryRowContext(ctx, query, id)

	snapshot := &domain.TemporalSnapshot{}
	var rootObsID sql.NullInt64

	err := row.Scan(
		&snapshot.ID,
		&snapshot.SnapshotKey,
		&snapshot.Timestamp,
		&snapshot.Description,
		&snapshot.ObservationCount,
		&snapshot.EdgeCount,
		&rootObsID,
	)
	if err != nil {
		return nil, err
	}

	if rootObsID.Valid {
		snapshot.RootObservationID = rootObsID.Int64
	}

	return snapshot, nil
}

// GetBySnapshotKey retrieves snapshots by their key.
func (r *TemporalSnapshotRepository) GetBySnapshotKey(ctx context.Context, snapshotKey string) ([]*domain.TemporalSnapshot, error) {
	query := `
		SELECT id, snapshot_key, timestamp, description, observation_count, edge_count, root_observation_id
		FROM temporal_snapshots 
		WHERE snapshot_key = ?
		ORDER BY timestamp DESC
	`

	rows, err := r.db.QueryContext(ctx, query, snapshotKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var snapshots []*domain.TemporalSnapshot
	for rows.Next() {
		snapshot := &domain.TemporalSnapshot{}
		var rootObsID sql.NullInt64

		err := rows.Scan(
			&snapshot.ID,
			&snapshot.SnapshotKey,
			&snapshot.Timestamp,
			&snapshot.Description,
			&snapshot.ObservationCount,
			&snapshot.EdgeCount,
			&rootObsID,
		)
		if err != nil {
			return nil, err
		}

		if rootObsID.Valid {
			snapshot.RootObservationID = rootObsID.Int64
		}

		snapshots = append(snapshots, snapshot)
	}

	return snapshots, nil
}

// GetSnapshotsInRange retrieves snapshots within a time range.
func (r *TemporalSnapshotRepository) GetSnapshotsInRange(ctx context.Context, from, to time.Time) ([]*domain.TemporalSnapshot, error) {
	query := `
		SELECT id, snapshot_key, timestamp, description, observation_count, edge_count, root_observation_id
		FROM temporal_snapshots 
		WHERE timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp DESC
	`

	rows, err := r.db.QueryContext(ctx, query, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var snapshots []*domain.TemporalSnapshot
	for rows.Next() {
		snapshot := &domain.TemporalSnapshot{}
		var rootObsID sql.NullInt64

		err := rows.Scan(
			&snapshot.ID,
			&snapshot.SnapshotKey,
			&snapshot.Timestamp,
			&snapshot.Description,
			&snapshot.ObservationCount,
			&snapshot.EdgeCount,
			&rootObsID,
		)
		if err != nil {
			return nil, err
		}

		if rootObsID.Valid {
			snapshot.RootObservationID = rootObsID.Int64
		}

		snapshots = append(snapshots, snapshot)
	}

	return snapshots, nil
}

// GetByRootObservation retrieves snapshots for a root observation.
func (r *TemporalSnapshotRepository) GetByRootObservation(ctx context.Context, rootObsID int64) ([]*domain.TemporalSnapshot, error) {
	query := `
		SELECT id, snapshot_key, timestamp, description, observation_count, edge_count, root_observation_id
		FROM temporal_snapshots 
		WHERE root_observation_id = ?
		ORDER BY timestamp DESC
	`

	rows, err := r.db.QueryContext(ctx, query, rootObsID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var snapshots []*domain.TemporalSnapshot
	for rows.Next() {
		snapshot := &domain.TemporalSnapshot{}
		var rootObsID sql.NullInt64

		err := rows.Scan(
			&snapshot.ID,
			&snapshot.SnapshotKey,
			&snapshot.Timestamp,
			&snapshot.Description,
			&snapshot.ObservationCount,
			&snapshot.EdgeCount,
			&rootObsID,
		)
		if err != nil {
			return nil, err
		}

		if rootObsID.Valid {
			snapshot.RootObservationID = rootObsID.Int64
		}

		snapshots = append(snapshots, snapshot)
	}

	return snapshots, nil
}

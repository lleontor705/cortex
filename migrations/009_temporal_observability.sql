-- +migrate Up
-- Add temporal graph and observability tables.

ALTER TABLE edges ADD COLUMN evolution_id INTEGER;
ALTER TABLE edges ADD COLUMN evolution_type TEXT NOT NULL DEFAULT 'original';
ALTER TABLE edges ADD COLUMN fact_state TEXT NOT NULL DEFAULT 'current';
ALTER TABLE edges ADD COLUMN change_reason TEXT;

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

CREATE INDEX IF NOT EXISTS idx_metrics_session_id ON metrics(session_id);
CREATE INDEX IF NOT EXISTS idx_metrics_timestamp ON metrics(timestamp);
CREATE INDEX IF NOT EXISTS idx_metrics_operation_type ON metrics(operation_type);

CREATE INDEX IF NOT EXISTS idx_quality_metrics_session_id ON quality_metrics(session_id);
CREATE INDEX IF NOT EXISTS idx_quality_metrics_evaluated_at ON quality_metrics(evaluated_at);
CREATE INDEX IF NOT EXISTS idx_quality_metrics_type ON quality_metrics(evaluation_type);

CREATE INDEX IF NOT EXISTS idx_temporal_snapshots_snapshot_key ON temporal_snapshots(snapshot_key);
CREATE INDEX IF NOT EXISTS idx_temporal_snapshots_timestamp ON temporal_snapshots(timestamp);
CREATE INDEX IF NOT EXISTS idx_temporal_snapshots_root_observation_id ON temporal_snapshots(root_observation_id);
CREATE INDEX IF NOT EXISTS idx_edges_evolution_id ON edges(evolution_id);

-- +migrate Down
DROP INDEX IF EXISTS idx_edges_evolution_id;
DROP INDEX IF EXISTS idx_temporal_snapshots_root_observation_id;
DROP INDEX IF EXISTS idx_temporal_snapshots_timestamp;
DROP INDEX IF EXISTS idx_temporal_snapshots_snapshot_key;
DROP INDEX IF EXISTS idx_quality_metrics_type;
DROP INDEX IF EXISTS idx_quality_metrics_evaluated_at;
DROP INDEX IF EXISTS idx_quality_metrics_session_id;
DROP INDEX IF EXISTS idx_metrics_operation_type;
DROP INDEX IF EXISTS idx_metrics_timestamp;
DROP INDEX IF EXISTS idx_metrics_session_id;
DROP TABLE IF EXISTS temporal_snapshots;
DROP TABLE IF EXISTS quality_metrics;
DROP TABLE IF EXISTS metrics;
-- SQLite does not support dropping the added temporal columns from edges.

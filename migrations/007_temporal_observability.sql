-- Migration 007: Add temporal graph and observability tables
-- This migration adds support for enhanced temporal graph semantics and observability features

-- Add temporal fields to existing edges table
ALTER TABLE edges ADD COLUMN evolution_id INTEGER;
ALTER TABLE edges ADD COLUMN evolution_type TEXT NOT NULL DEFAULT 'original';
ALTER TABLE edges ADD COLUMN fact_state TEXT NOT NULL DEFAULT 'current';
ALTER TABLE edges ADD COLUMN change_reason TEXT;

-- Create metrics table for observability
CREATE TABLE metrics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT,
    operation_type TEXT NOT NULL,
    duration_ms INTEGER NOT NULL,
    result_count INTEGER NOT NULL DEFAULT 0,
    success BOOLEAN NOT NULL,
    error TEXT,
    memory_usage_bytes INTEGER NOT NULL DEFAULT 0,
    timestamp DATETIME NOT NULL,
    observation_count INTEGER NOT NULL DEFAULT 0,
    edge_count INTEGER NOT NULL DEFAULT 0,
    query_complexity REAL NOT NULL DEFAULT 0.0,
    confidence_score REAL NOT NULL DEFAULT 0.0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Create quality_metrics table for memory quality evaluation
CREATE TABLE quality_metrics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT,
    evaluation_type TEXT NOT NULL,
    score REAL NOT NULL,
    total_queries INTEGER NOT NULL DEFAULT 0,
    successful_retrievals INTEGER NOT NULL DEFAULT 0,
    average_latency_ms REAL NOT NULL DEFAULT 0.0,
    average_relevance REAL NOT NULL DEFAULT 0.0,
    temporal_accuracy REAL NOT NULL DEFAULT 0.0,
    knowledge_coverage REAL NOT NULL DEFAULT 0.0,
    evaluated_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Create temporal_snapshots table for point-in-time graph snapshots
CREATE TABLE temporal_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    snapshot_key TEXT NOT NULL,
    timestamp DATETIME NOT NULL,
    description TEXT,
    observation_count INTEGER NOT NULL DEFAULT 0,
    edge_count INTEGER NOT NULL DEFAULT 0,
    root_observation_id INTEGER,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    
    -- Unique constraint per snapshot key (allows multiple snapshots with same key at different times)
    UNIQUE(snapshot_key, timestamp)
);

-- Create indexes for performance optimization
CREATE INDEX idx_metrics_session_id ON metrics(session_id);
CREATE INDEX idx_metrics_timestamp ON metrics(timestamp);
CREATE INDEX idx_metrics_operation_type ON metrics(operation_type);

CREATE INDEX idx_quality_metrics_session_id ON quality_metrics(session_id);
CREATE INDEX idx_quality_metrics_evaluated_at ON quality_metrics(evaluated_at);
CREATE INDEX idx_quality_metrics_type ON quality_metrics(evaluation_type);

CREATE INDEX idx_temporal_snapshots_snapshot_key ON temporal_snapshots(snapshot_key);
CREATE INDEX idx_temporal_snapshots_timestamp ON temporal_snapshots(timestamp);
CREATE INDEX idx_temporal_snapshots_root_observation_id ON temporal_snapshots(root_observation_id);

-- Add foreign key constraints
-- edges.evolution_id references edges.id (for evolution chains)
CREATE INDEX idx_edges_evolution_id ON edges(evolution_id);

-- temporal_snapshots.root_observation_id references observations.id
CREATE INDEX idx_temporal_snapshots_root_obs ON temporal_snapshots(root_observation_id);

-- Add constraints for new temporal fields
ALTER TABLE edges ADD CONSTRAINT chk_evolution_type 
    CHECK (evolution_type IN ('original', 'modified', 'superseded', 'contradicted'));

ALTER TABLE edges ADD CONSTRAINT chk_fact_state 
    CHECK (fact_state IN ('current', 'historical', 'deprecated', 'superseded'));

-- Add constraints for metrics
ALTER TABLE metrics ADD CONSTRAINT chk_duration 
    CHECK (duration_ms >= 0);

ALTER TABLE metrics ADD CONSTRAINT chk_memory_usage 
    CHECK (memory_usage_bytes >= 0);

ALTER TABLE metrics ADD CONSTRAINT chk_complexity 
    CHECK (query_complexity >= 0.0 AND query_complexity <= 1.0);

ALTER TABLE metrics ADD CONSTRAINT chk_confidence 
    CHECK (confidence_score >= 0.0 AND confidence_score <= 1.0);

-- Add constraints for quality_metrics
ALTER TABLE quality_metrics ADD CONSTRAINT chk_score 
    CHECK (score >= 0.0 AND score <= 1.0);

ALTER TABLE quality_metrics ADD CONSTRAINT chk_latency 
    CHECK (average_latency_ms >= 0.0);

-- Add constraints for temporal_snapshots
ALTER TABLE temporal_snapshots ADD CONSTRAINT chk_observation_count 
    CHECK (observation_count >= 0);

ALTER TABLE temporal_snapshots ADD CONSTRAINT chk_edge_count 
    CHECK (edge_count >= 0);
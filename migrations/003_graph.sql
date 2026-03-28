-- +migrate Up
-- Knowledge graph tables
-- This migration creates the edges table for building a knowledge graph
-- that connects observations through typed relationships.

-- Relation types (documented for reference):
-- - references: Direct reference to another observation
-- - relates_to: Related topic or concept
-- - follows: Sequential relationship
-- - supersedes: Replaces an older observation
-- - contradicts: Conflicting information

CREATE TABLE edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_obs_id INTEGER NOT NULL,
    to_obs_id INTEGER NOT NULL,
    relation_type TEXT NOT NULL,
    weight REAL NOT NULL DEFAULT 1.0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (from_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
    FOREIGN KEY (to_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
    UNIQUE(from_obs_id, to_obs_id, relation_type)
);

-- Indexes for efficient graph queries
CREATE INDEX idx_edges_from ON edges(from_obs_id);
CREATE INDEX idx_edges_to ON edges(to_obs_id);
CREATE INDEX idx_edges_relation ON edges(relation_type);
CREATE INDEX idx_edges_weight ON edges(weight DESC);

-- +migrate Down
-- Remove knowledge graph tables
DROP INDEX IF EXISTS idx_edges_weight;
DROP INDEX IF EXISTS idx_edges_relation;
DROP INDEX IF EXISTS idx_edges_to;
DROP INDEX IF EXISTS idx_edges_from;
DROP TABLE IF EXISTS edges;

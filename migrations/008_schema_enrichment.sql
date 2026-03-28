-- +migrate Up
-- Schema enrichment: confidence, source, tags for observations; temporal edges

-- Observation metadata
ALTER TABLE observations ADD COLUMN confidence REAL NOT NULL DEFAULT 1.0;
ALTER TABLE observations ADD COLUMN source TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE observations ADD COLUMN tags TEXT;  -- JSON array: '["auth","jwt"]'

-- Edge metadata and temporal validity
ALTER TABLE edges ADD COLUMN confidence REAL NOT NULL DEFAULT 1.0;
ALTER TABLE edges ADD COLUMN source TEXT;
ALTER TABLE edges ADD COLUMN reasoning TEXT;
ALTER TABLE edges ADD COLUMN valid_from TEXT;
ALTER TABLE edges ADD COLUMN invalid_at TEXT;

-- Indexes
CREATE INDEX IF NOT EXISTS idx_obs_source ON observations(source) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_obs_confidence ON observations(confidence) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_edges_validity ON edges(invalid_at);

-- +migrate Down
DROP INDEX IF EXISTS idx_edges_validity;
DROP INDEX IF EXISTS idx_obs_confidence;
DROP INDEX IF EXISTS idx_obs_source;

ALTER TABLE edges DROP COLUMN invalid_at;
ALTER TABLE edges DROP COLUMN valid_from;
ALTER TABLE edges DROP COLUMN reasoning;
ALTER TABLE edges DROP COLUMN source;
ALTER TABLE edges DROP COLUMN confidence;

ALTER TABLE observations DROP COLUMN tags;
ALTER TABLE observations DROP COLUMN source;
ALTER TABLE observations DROP COLUMN confidence;

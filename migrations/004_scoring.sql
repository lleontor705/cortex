-- +migrate Up
-- Importance scoring system for observations
-- Enables automatic scoring based on access patterns, relationships, and type

-- Create importance_scores table
-- Stores scoring metrics and cached score values for each observation
CREATE TABLE importance_scores (
    observation_id INTEGER PRIMARY KEY,
    score REAL NOT NULL DEFAULT 0.0,
    access_count INTEGER NOT NULL DEFAULT 0,
    last_accessed DATETIME,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (observation_id) REFERENCES observations(id) ON DELETE CASCADE
);

-- Indexes for efficient score-based queries
-- DESC ordering for quick "top N" queries
CREATE INDEX idx_importance_score ON importance_scores(score DESC);
CREATE INDEX idx_importance_access ON importance_scores(access_count DESC);

-- Score calculation factors (documented):
-- 1. Access frequency: +0.1 per access
-- 2. Recency bonus: +0.5 if accessed in last 24 hours
-- 3. Edge count: +0.2 per incoming edge (from observation_relationships)
-- 4. Type bonus: +0.5 for decisions, +0.3 for bugfixes
-- 5. Age decay: -0.01 per day (min score: 0)
--
-- Formula: score = base(0.0) + access_bonus + recency_bonus + edge_bonus + type_bonus - age_penalty
-- Where:
--   access_bonus = access_count * 0.1
--   recency_bonus = 0.5 IF (last_accessed >= datetime('now', '-24 hours')) ELSE 0
--   edge_bonus = (SELECT COUNT(*) FROM observation_relationships WHERE target_id = observation_id) * 0.2
--   type_bonus = 0.5 IF type='decision' ELSE 0.3 IF type='bugfix' ELSE 0
--   age_penalty = MIN(julianday('now') - julianday(created_at), 0) * 0.01
--
-- Note: The score can be calculated on-demand or updated via application code
-- using the UPDATE statement pattern shown below.

-- Auto-initialization trigger
-- Creates a score record when a new observation is inserted
CREATE TRIGGER importance_init AFTER INSERT ON observations
BEGIN
    INSERT INTO importance_scores (observation_id, score, updated_at)
    VALUES (new.id, 0.0, CURRENT_TIMESTAMP);
END;

-- Application code pattern for updating scores on access:
-- UPDATE importance_scores 
-- SET access_count = access_count + 1,
--     last_accessed = CURRENT_TIMESTAMP,
--     updated_at = CURRENT_TIMESTAMP
-- WHERE observation_id = ?;

-- +migrate Down
-- Remove importance scoring system

DROP TRIGGER IF EXISTS importance_init;
DROP INDEX IF EXISTS idx_importance_access;
DROP INDEX IF EXISTS idx_importance_score;
DROP TABLE IF EXISTS importance_scores;

-- +migrate Up
-- Implicit search feedback: tracks which observations are accessed after searches.
-- This data enables future Learning-to-Rank (LTR) model training.

CREATE TABLE IF NOT EXISTS search_feedback (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    query TEXT NOT NULL,
    observation_id INTEGER NOT NULL,
    rank_position INTEGER NOT NULL DEFAULT 0,
    clicked_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (observation_id) REFERENCES observations(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_search_feedback_query ON search_feedback(query);
CREATE INDEX IF NOT EXISTS idx_search_feedback_obs ON search_feedback(observation_id);

-- +migrate Down
DROP INDEX IF EXISTS idx_search_feedback_obs;
DROP INDEX IF EXISTS idx_search_feedback_query;
DROP TABLE IF EXISTS search_feedback;

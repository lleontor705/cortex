-- +migrate Up
-- Add CHECK constraints for edges weight and confidence.
-- SQLite does not support ALTER TABLE ADD CONSTRAINT, so we recreate
-- the table with proper constraints and copy data.

CREATE TABLE edges_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_obs_id INTEGER NOT NULL,
    to_obs_id INTEGER NOT NULL,
    relation_type TEXT NOT NULL,
    weight REAL NOT NULL DEFAULT 1.0 CHECK (weight >= 0.0 AND weight <= 10.0),
    confidence REAL NOT NULL DEFAULT 1.0 CHECK (confidence >= 0.0 AND confidence <= 1.0),
    source TEXT,
    reasoning TEXT,
    valid_from TEXT,
    invalid_at TEXT,
    evolution_id INTEGER,
    evolution_type TEXT NOT NULL DEFAULT 'original',
    fact_state TEXT NOT NULL DEFAULT 'current',
    change_reason TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (from_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
    FOREIGN KEY (to_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
    UNIQUE(from_obs_id, to_obs_id, relation_type)
);

INSERT INTO edges_new (
    id, from_obs_id, to_obs_id, relation_type, weight, confidence,
    source, reasoning, valid_from, invalid_at,
    evolution_id, evolution_type, fact_state, change_reason, created_at
)
SELECT
    id, from_obs_id, to_obs_id, relation_type,
    COALESCE(weight, 1.0),
    COALESCE(confidence, 1.0),
    source, reasoning, valid_from, invalid_at,
    evolution_id,
    COALESCE(evolution_type, 'original'),
    COALESCE(fact_state, 'current'),
    change_reason, created_at
FROM edges;
DROP TABLE edges;
ALTER TABLE edges_new RENAME TO edges;

CREATE INDEX IF NOT EXISTS idx_edges_from ON edges(from_obs_id);
CREATE INDEX IF NOT EXISTS idx_edges_to ON edges(to_obs_id);
CREATE INDEX IF NOT EXISTS idx_edges_created_at ON edges(created_at);
CREATE INDEX IF NOT EXISTS idx_edges_evolution_id ON edges(evolution_id);

-- +migrate Down
-- Revert to edges without CHECK constraints.

CREATE TABLE edges_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_obs_id INTEGER NOT NULL,
    to_obs_id INTEGER NOT NULL,
    relation_type TEXT NOT NULL,
    weight REAL NOT NULL DEFAULT 1.0,
    confidence REAL NOT NULL DEFAULT 1.0,
    source TEXT,
    reasoning TEXT,
    valid_from TEXT,
    invalid_at TEXT,
    evolution_id INTEGER,
    evolution_type TEXT NOT NULL DEFAULT 'original',
    fact_state TEXT NOT NULL DEFAULT 'current',
    change_reason TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (from_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
    FOREIGN KEY (to_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
    UNIQUE(from_obs_id, to_obs_id, relation_type)
);

INSERT INTO edges_old SELECT * FROM edges;
DROP TABLE edges;
ALTER TABLE edges_old RENAME TO edges;

CREATE INDEX IF NOT EXISTS idx_edges_from ON edges(from_obs_id);
CREATE INDEX IF NOT EXISTS idx_edges_to ON edges(to_obs_id);
CREATE INDEX IF NOT EXISTS idx_edges_created_at ON edges(created_at);
CREATE INDEX IF NOT EXISTS idx_edges_evolution_id ON edges(evolution_id);

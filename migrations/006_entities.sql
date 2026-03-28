-- +migrate Up
-- Entity linking tables
-- Tracks extracted entities (files, URLs, packages, symbols, concepts) from observations.

CREATE TABLE entity_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    observation_id INTEGER NOT NULL,
    entity_type TEXT NOT NULL,    -- file, url, package, symbol, concept
    entity_value TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (observation_id) REFERENCES observations(id) ON DELETE CASCADE
);

CREATE INDEX idx_entity_obs ON entity_links(observation_id);
CREATE INDEX idx_entity_type ON entity_links(entity_type);
CREATE INDEX idx_entity_value ON entity_links(entity_value);
CREATE UNIQUE INDEX idx_entity_unique ON entity_links(observation_id, entity_type, entity_value);

-- +migrate Down
DROP INDEX IF EXISTS idx_entity_unique;
DROP INDEX IF EXISTS idx_entity_value;
DROP INDEX IF EXISTS idx_entity_type;
DROP INDEX IF EXISTS idx_entity_obs;
DROP TABLE IF EXISTS entity_links;

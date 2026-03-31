-- +migrate Up
-- Track which sync chunks have been imported/exported to prevent duplicates.

CREATE TABLE IF NOT EXISTS sync_chunks (
    chunk_id    TEXT PRIMARY KEY,
    imported_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- +migrate Down
DROP TABLE IF EXISTS sync_chunks;

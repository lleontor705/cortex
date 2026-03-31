-- +migrate Up
-- Add missing indexes for temporal queries on edges.

CREATE INDEX IF NOT EXISTS idx_edges_created_at ON edges(created_at);

-- +migrate Down
DROP INDEX IF EXISTS idx_edges_created_at;

-- +migrate Up
-- Optional vector search support
-- Note: This is optional and can be skipped if vector search is not needed

-- Note: This is a simple embedding storage
-- For production, consider using sqlite-vec extension or external vector DB

CREATE TABLE observation_vectors (
    observation_id INTEGER PRIMARY KEY,
    embedding BLOB,  -- Serialized vector (e.g., 1536 floats for OpenAI)
    embedding_model TEXT,  -- e.g., "text-embedding-ada-002"
    dimensions INTEGER,  -- e.g., 1536
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (observation_id) REFERENCES observations(id) ON DELETE CASCADE
);

CREATE INDEX idx_vectors_model ON observation_vectors(embedding_model);

-- Vector similarity search would be implemented in application code
-- using cosine similarity or other distance metrics

-- Example workflow:
-- 1. Generate embedding for observation content
-- 2. Store as BLOB (serialized []float64 or []float32)
-- 3. On search, compute similarity with query embedding
-- 4. Return top-k most similar observations

-- Note: This is OPTIONAL. If sqlite-vec is available, use:
-- CREATE VIRTUAL TABLE vec_embeddings USING vec0(
--     observation_id INTEGER PRIMARY KEY,
--     embedding FLOAT[1536]
-- );

-- +migrate Down
DROP INDEX IF EXISTS idx_vectors_model;
DROP TABLE IF EXISTS observation_vectors;

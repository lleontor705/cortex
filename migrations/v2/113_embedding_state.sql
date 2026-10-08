-- Migration 113: per-observation embedding state for honest RAG coverage
-- (issue #115 follow-up, issue #119).
--
-- WHY THIS EXISTS
-- The observations table carried no per-observation embedding flag, so the
-- stats plane could not observe the vector replica: GetRAGStats counted the
-- never-populated in-memory HasEmbedding/RAGStatus fields and every
-- observation collapsed into the honest "pending" branch, reporting
-- pending: N / coverage: 0 forever while cortex_vector.embeddings filled up.
--
-- WHAT IT CHANGES
-- observations gains embedding_state (pending | indexed | failed, default
-- pending) and embedded_at. The background embedding worker stamps
-- embedding_state='indexed' through the authorized store after a SUCCESSFUL
-- vector upsert; ListUnembedded filters on the persisted flag so a failed
-- stamp self-heals on the next pass (the row still counts as unembedded).
-- rag/stats coverage derives from this state: covered = indexed.
--
-- GRANTS AND RLS
-- Column additions inherit the table's existing grants and row-level
-- security; no per-column privileges are introduced. The migration role is
-- cortex_migration (BYPASSRLS); runtime writes go through the authorized
-- (principal-bound) transaction path exactly like every other observations
-- DML.
--
-- NO BACKFILL
-- Rows already present in cortex_vector.embeddings are intentionally left
-- 'pending' here. The vector replica table is bootstrapped by the pgvector
-- adapter composition (not guaranteed to exist at migration time), and the
-- upsert path is idempotent: the worker re-embeds pending rows once and
-- stamps them, which is the same self-healing path a failed stamp takes.
-- This keeps the migration free of cross-schema runtime dependencies.

ALTER TABLE observations ADD COLUMN IF NOT EXISTS embedding_state text NOT NULL DEFAULT 'pending'
    CHECK (embedding_state IN ('pending', 'indexed', 'failed'));
ALTER TABLE observations ADD COLUMN IF NOT EXISTS embedded_at timestamptz;

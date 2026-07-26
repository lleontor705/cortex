-- Cortex v2 baseline schema — migrations/v2/001_init.sql
--
-- This is the SINGLE forward-only baseline for Cortex 2.0.0 (REQ-DB-001).
-- It consolidates the final-state schema of v1 migrations 001-014 into one
-- atomic DDL bundle and adds the v2 corrections prescribed by the design:
--
--   * Corrected observation type registry (session_summary, passive now valid)
--   * Bi-temporal valid_until columns on observations and edges (ADR-11)
--   * Transactional index_outbox + index_state for the embedding worker (ADR-04)
--   * audit_events for tamper-evident audit (ADR-17, declared here for W16)
--   * Nullable tenant columns (tenant_id, workspace_id, owner_id) for future
--     server-mode shared-schema multitenancy (ADR-06); NULL in local mode
--   * cortex_meta key-value table recording schema identity (family+version+checksum)
--
-- The entire bundle is applied inside ONE transaction by the v2 migration
-- runner (internal/migration). The schema identity is recorded in cortex_meta
-- within that SAME transaction, and the DB is only marked "ready" after
-- PRAGMA integrity_check passes.
--
-- v1 migrations 001-014 are RETIRED from the v2 line (REQ-DB-001 REMOVED).
-- They MUST NOT run on a v2 database.

-- ===========================================================================
-- 1. Schema identity
-- ===========================================================================

-- cortex_meta stores schema identity and other singleton metadata.
-- Populated transactionally by the runner with:
--   schema_family   = cortex-v2
--   schema_version  = 001
--   schema_checksum = sha256(baseline SQL)
CREATE TABLE cortex_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- ===========================================================================
-- 2. Core tables (sessions, observations, prompts)
-- ===========================================================================

-- Sessions: coding session lifecycle.
CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,
    project      TEXT NOT NULL,
    directory    TEXT NOT NULL,
    started_at   TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at     TEXT,
    summary      TEXT,
    tenant_id    TEXT,  -- v2: nullable (NULL in local mode)
    workspace_id TEXT   -- v2: nullable (NULL in local mode)
);

-- Observations: the central memory records.
-- Includes the CORRECTED type CHECK constraint (session_summary, passive).
CREATE TABLE observations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    sync_id         TEXT,
    session_id      TEXT NOT NULL,
    type            TEXT NOT NULL CHECK (type IN (
                        'manual', 'tool_use', 'decision', 'bugfix', 'pattern',
                        'config', 'discovery', 'learning',
                        'session_summary', 'passive'
                    )),
    title           TEXT NOT NULL,
    content         TEXT NOT NULL,
    tool_name       TEXT,
    project         TEXT,
    scope           TEXT NOT NULL DEFAULT 'project',
    topic_key       TEXT,
    normalized_hash TEXT,
    revision_count  INTEGER NOT NULL DEFAULT 1,
    duplicate_count INTEGER NOT NULL DEFAULT 1,
    last_seen_at    TEXT,
    confidence      REAL NOT NULL DEFAULT 1.0,
    source          TEXT NOT NULL DEFAULT 'manual',
    tags            TEXT,
    valid_until     TEXT,  -- v2: bi-temporal invalidation (close on supersede/archive)
    tenant_id       TEXT,  -- v2: nullable (NULL in local mode)
    workspace_id    TEXT,  -- v2: nullable (NULL in local mode)
    owner_id        TEXT,  -- v2: nullable (NULL in local mode)
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    deleted_at      TEXT,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX idx_obs_session  ON observations(session_id);
CREATE INDEX idx_obs_type     ON observations(type);
CREATE INDEX idx_obs_project  ON observations(project);
CREATE INDEX idx_obs_created  ON observations(created_at DESC);

-- Performance partial indexes (WHERE deleted_at IS NULL).
CREATE INDEX idx_obs_not_deleted        ON observations(id) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_topic_project      ON observations(topic_key, project) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_project_type       ON observations(project, type) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_created_not_deleted ON observations(created_at) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_source             ON observations(source) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_confidence         ON observations(confidence) WHERE deleted_at IS NULL;

-- User prompts: user input history.
CREATE TABLE user_prompts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    sync_id    TEXT,
    session_id TEXT    NOT NULL,
    content    TEXT    NOT NULL,
    project    TEXT,
    tenant_id  TEXT,  -- v2: nullable
    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX idx_prompts_session ON user_prompts(session_id);
CREATE INDEX idx_prompts_project ON user_prompts(project);
CREATE INDEX idx_prompts_created ON user_prompts(created_at DESC);

-- ===========================================================================
-- 3. Full-text search (FTS5)
-- ===========================================================================

-- Contextual FTS5 for observations (Anthropic Contextual Retrieval technique).
-- The content column in the FTS index carries type+topic_key+project prepended
-- to the raw content, enriching recall. The triggers below implement this.
CREATE VIRTUAL TABLE observations_fts USING fts5(
    title,
    content,
    tool_name,
    type,
    project,
    scope,
    topic_key,
    content='observations',
    content_rowid='id',
    tokenize='porter unicode61'
);

CREATE TRIGGER obs_fts_insert AFTER INSERT ON observations BEGIN
    INSERT INTO observations_fts(rowid, title, content, tool_name, type, project, scope, topic_key)
    VALUES (
        new.id,
        new.title,
        COALESCE(new.type, '') || ' ' || COALESCE(new.topic_key, '') || ' ' || COALESCE(new.project, '') || ' ' || new.content,
        new.tool_name, new.type, new.project, new.scope, new.topic_key
    );
END;

CREATE TRIGGER obs_fts_delete AFTER DELETE ON observations BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, title, content, tool_name, type, project, scope, topic_key)
    VALUES ('delete', old.id, old.title, old.content, old.tool_name, old.type, old.project, old.scope, old.topic_key);
END;

CREATE TRIGGER obs_fts_update AFTER UPDATE ON observations BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, title, content, tool_name, type, project, scope, topic_key)
    VALUES ('delete', old.id, old.title, old.content, old.tool_name, old.type, old.project, old.scope, old.topic_key);
    INSERT INTO observations_fts(rowid, title, content, tool_name, type, project, scope, topic_key)
    VALUES (
        new.id,
        new.title,
        COALESCE(new.type, '') || ' ' || COALESCE(new.topic_key, '') || ' ' || COALESCE(new.project, '') || ' ' || new.content,
        new.tool_name, new.type, new.project, new.scope, new.topic_key
    );
END;

-- FTS5 for user prompts.
CREATE VIRTUAL TABLE prompts_fts USING fts5(
    content,
    project,
    content='user_prompts',
    content_rowid='id',
    tokenize='porter unicode61'
);

CREATE TRIGGER prompt_fts_insert AFTER INSERT ON user_prompts BEGIN
    INSERT INTO prompts_fts(rowid, content, project)
    VALUES (new.id, new.content, new.project);
END;

CREATE TRIGGER prompt_fts_delete AFTER DELETE ON user_prompts BEGIN
    INSERT INTO prompts_fts(prompts_fts, rowid, content, project)
    VALUES ('delete', old.id, old.content, old.project);
END;

CREATE TRIGGER prompt_fts_update AFTER UPDATE ON user_prompts BEGIN
    INSERT INTO prompts_fts(prompts_fts, rowid, content, project)
    VALUES ('delete', old.id, old.content, old.project);
    INSERT INTO prompts_fts(rowid, content, project)
    VALUES (new.id, new.content, new.project);
END;

-- ===========================================================================
-- 4. Knowledge graph (edges)
-- ===========================================================================

-- Edges with full temporal metadata + bi-temporal valid_until (v2 ADR-11).
CREATE TABLE edges (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    from_obs_id    INTEGER NOT NULL,
    to_obs_id      INTEGER NOT NULL,
    relation_type  TEXT NOT NULL,
    weight         REAL NOT NULL DEFAULT 1.0 CHECK (weight >= 0.0 AND weight <= 10.0),
    confidence     REAL NOT NULL DEFAULT 1.0 CHECK (confidence >= 0.0 AND confidence <= 1.0),
    source         TEXT,
    reasoning      TEXT,
    valid_from     TEXT,
    invalid_at     TEXT,
    valid_until    TEXT,  -- v2: bi-temporal close on supersede (Graphiti pattern)
    evolution_id   INTEGER,
    evolution_type TEXT NOT NULL DEFAULT 'original',
    fact_state     TEXT NOT NULL DEFAULT 'current',
    change_reason  TEXT,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (from_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
    FOREIGN KEY (to_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
    UNIQUE(from_obs_id, to_obs_id, relation_type)
);

CREATE INDEX idx_edges_from          ON edges(from_obs_id);
CREATE INDEX idx_edges_to            ON edges(to_obs_id);
CREATE INDEX idx_edges_relation      ON edges(relation_type);
CREATE INDEX idx_edges_weight        ON edges(weight DESC);
CREATE INDEX idx_edges_created_at    ON edges(created_at);
CREATE INDEX idx_edges_evolution_id  ON edges(evolution_id);
CREATE INDEX idx_edges_validity      ON edges(invalid_at);
-- v2: partial index for current-state fact queries (excludes closed valid_until).
CREATE INDEX idx_edges_valid         ON edges(from_obs_id) WHERE valid_until IS NULL;

-- ===========================================================================
-- 5. Importance scoring
-- ===========================================================================

CREATE TABLE importance_scores (
    observation_id INTEGER PRIMARY KEY,
    score          REAL NOT NULL DEFAULT 0.0,
    access_count   INTEGER NOT NULL DEFAULT 0,
    last_accessed  DATETIME,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (observation_id) REFERENCES observations(id) ON DELETE CASCADE
);

CREATE INDEX idx_importance_score ON importance_scores(score DESC);
CREATE INDEX idx_importance_access ON importance_scores(access_count DESC);

-- Auto-initialize score on observation insert.
CREATE TRIGGER importance_init AFTER INSERT ON observations
BEGIN
    INSERT INTO importance_scores (observation_id, score, updated_at)
    VALUES (new.id, 0.0, CURRENT_TIMESTAMP);
END;

-- ===========================================================================
-- 6. Vector storage (sqlite_blob default, zero-CGO)
-- ===========================================================================

CREATE TABLE observation_vectors (
    observation_id  INTEGER PRIMARY KEY,
    embedding       BLOB,
    embedding_model TEXT,
    dimensions      INTEGER,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (observation_id) REFERENCES observations(id) ON DELETE CASCADE
);

CREATE INDEX idx_vectors_model ON observation_vectors(embedding_model);

-- ===========================================================================
-- 7. Entity linking
-- ===========================================================================

CREATE TABLE entity_links (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    observation_id INTEGER NOT NULL,
    entity_type    TEXT NOT NULL,
    entity_value   TEXT NOT NULL,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (observation_id) REFERENCES observations(id) ON DELETE CASCADE
);

CREATE INDEX idx_entity_obs    ON entity_links(observation_id);
CREATE INDEX idx_entity_type   ON entity_links(entity_type);
CREATE INDEX idx_entity_value  ON entity_links(entity_value);
CREATE UNIQUE INDEX idx_entity_unique ON entity_links(observation_id, entity_type, entity_value);

-- ===========================================================================
-- 8. Observability (metrics, quality_metrics, temporal_snapshots)
-- ===========================================================================

CREATE TABLE metrics (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id       TEXT,
    operation_type   TEXT NOT NULL,
    duration_ms      INTEGER NOT NULL CHECK (duration_ms >= 0),
    result_count     INTEGER NOT NULL DEFAULT 0,
    success          BOOLEAN NOT NULL,
    error            TEXT,
    memory_usage_bytes INTEGER NOT NULL DEFAULT 0 CHECK (memory_usage_bytes >= 0),
    timestamp        DATETIME NOT NULL,
    observation_count INTEGER NOT NULL DEFAULT 0,
    edge_count       INTEGER NOT NULL DEFAULT 0,
    query_complexity REAL NOT NULL DEFAULT 0.0 CHECK (query_complexity >= 0.0 AND query_complexity <= 1.0),
    confidence_score REAL NOT NULL DEFAULT 0.0 CHECK (confidence_score >= 0.0 AND confidence_score <= 1.0),
    created_at       DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_metrics_session_id      ON metrics(session_id);
CREATE INDEX idx_metrics_timestamp       ON metrics(timestamp);
CREATE INDEX idx_metrics_operation_type  ON metrics(operation_type);

CREATE TABLE quality_metrics (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id          TEXT,
    evaluation_type     TEXT NOT NULL,
    score               REAL NOT NULL CHECK (score >= 0.0 AND score <= 1.0),
    total_queries       INTEGER NOT NULL DEFAULT 0,
    successful_retrievals INTEGER NOT NULL DEFAULT 0,
    average_latency_ms  REAL NOT NULL DEFAULT 0.0 CHECK (average_latency_ms >= 0.0),
    average_relevance   REAL NOT NULL DEFAULT 0.0,
    temporal_accuracy   REAL NOT NULL DEFAULT 0.0,
    knowledge_coverage  REAL NOT NULL DEFAULT 0.0,
    evaluated_at        DATETIME NOT NULL,
    created_at          DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_quality_metrics_session_id   ON quality_metrics(session_id);
CREATE INDEX idx_quality_metrics_evaluated_at ON quality_metrics(evaluated_at);
CREATE INDEX idx_quality_metrics_type         ON quality_metrics(evaluation_type);

CREATE TABLE temporal_snapshots (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    snapshot_key        TEXT NOT NULL,
    timestamp           DATETIME NOT NULL,
    description         TEXT,
    observation_count   INTEGER NOT NULL DEFAULT 0 CHECK (observation_count >= 0),
    edge_count          INTEGER NOT NULL DEFAULT 0 CHECK (edge_count >= 0),
    root_observation_id INTEGER,
    created_at          DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(snapshot_key, timestamp)
);

CREATE INDEX idx_temporal_snapshots_snapshot_key        ON temporal_snapshots(snapshot_key);
CREATE INDEX idx_temporal_snapshots_timestamp           ON temporal_snapshots(timestamp);
CREATE INDEX idx_temporal_snapshots_root_observation_id ON temporal_snapshots(root_observation_id);

-- ===========================================================================
-- 9. Sync tracking
-- ===========================================================================

CREATE TABLE sync_chunks (
    chunk_id    TEXT PRIMARY KEY,
    imported_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- ===========================================================================
-- 10. Search feedback
-- ===========================================================================

CREATE TABLE search_feedback (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    query           TEXT NOT NULL,
    observation_id  INTEGER NOT NULL,
    rank_position   INTEGER NOT NULL DEFAULT 0,
    clicked_at      TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (observation_id) REFERENCES observations(id) ON DELETE CASCADE
);

CREATE INDEX idx_search_feedback_query ON search_feedback(query);
CREATE INDEX idx_search_feedback_obs   ON search_feedback(observation_id);

-- ===========================================================================
-- 11. v2 additions: transactional outbox + index state (ADR-04, W4)
-- ===========================================================================

-- index_outbox: durable embed+upsert intents committed in the same transaction
-- as the observation write. The embedding worker leases and processes these.
CREATE TABLE index_outbox (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    observation_id INTEGER NOT NULL REFERENCES observations(id),
    intent         TEXT NOT NULL,
    model_info     TEXT,
    status         TEXT NOT NULL DEFAULT 'pending',
    attempts       INTEGER NOT NULL DEFAULT 0,
    max_attempts   INTEGER NOT NULL DEFAULT 5,
    next_retry_at  TEXT,
    leased_at      TEXT,
    completed_at   TEXT,
    error          TEXT,
    created_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_outbox_pending ON index_outbox(status, next_retry_at) WHERE status = 'pending';

-- index_state: per-namespace vector coverage/parity tracking.
CREATE TABLE index_state (
    namespace       TEXT PRIMARY KEY,
    coverage        REAL NOT NULL,
    parity          INTEGER DEFAULT 0,
    authority_digest TEXT,
    updated_at      TEXT NOT NULL
);

-- ===========================================================================
-- 12. v2 additions: audit events (ADR-17, W16)
-- ===========================================================================

-- audit_events: append-only, hash-chained audit log for authz decisions,
-- destructive ops, and invariant failures. Declared here so W16 can populate it.
CREATE TABLE audit_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    principal   TEXT,
    resource    TEXT,
    action      TEXT,
    tenant_id   TEXT,
    prev_hash   TEXT,
    hash        TEXT NOT NULL,
    recorded_at TEXT NOT NULL
);

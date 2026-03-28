-- +migrate Up
-- Initial schema setup for Cortex memory server
-- This creates the core tables for sessions, observations, and prompts

-- Sessions table: tracks coding sessions
CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    project    TEXT NOT NULL,
    directory  TEXT NOT NULL,
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at   TEXT,
    summary    TEXT
);

-- Observations table: stores memories from AI coding sessions
CREATE TABLE IF NOT EXISTS observations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    sync_id    TEXT,
    session_id TEXT    NOT NULL,
    type       TEXT    NOT NULL,
    title      TEXT    NOT NULL,
    content    TEXT    NOT NULL,
    tool_name  TEXT,
    project    TEXT,
    scope      TEXT    NOT NULL DEFAULT 'project',
    topic_key  TEXT,
    normalized_hash TEXT,
    revision_count INTEGER NOT NULL DEFAULT 1,
    duplicate_count INTEGER NOT NULL DEFAULT 1,
    last_seen_at TEXT,
    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT    NOT NULL DEFAULT (datetime('now')),
    deleted_at TEXT,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

-- Create indexes for observations
CREATE INDEX IF NOT EXISTS idx_obs_session  ON observations(session_id);
CREATE INDEX IF NOT EXISTS idx_obs_type     ON observations(type);
CREATE INDEX IF NOT EXISTS idx_obs_project  ON observations(project);
CREATE INDEX IF NOT EXISTS idx_obs_created  ON observations(created_at DESC);

-- User prompts table: stores user input history
CREATE TABLE IF NOT EXISTS user_prompts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    sync_id    TEXT,
    session_id TEXT    NOT NULL,
    content    TEXT    NOT NULL,
    project    TEXT,
    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

-- Create indexes for user_prompts
CREATE INDEX IF NOT EXISTS idx_prompts_session ON user_prompts(session_id);
CREATE INDEX IF NOT EXISTS idx_prompts_project ON user_prompts(project);
CREATE INDEX IF NOT EXISTS idx_prompts_created ON user_prompts(created_at DESC);

-- +migrate Down
-- Rollback initial schema

DROP INDEX IF EXISTS idx_prompts_created;
DROP INDEX IF EXISTS idx_prompts_project;
DROP INDEX IF EXISTS idx_prompts_session;
DROP TABLE IF EXISTS user_prompts;

DROP INDEX IF EXISTS idx_obs_created;
DROP INDEX IF EXISTS idx_obs_project;
DROP INDEX IF EXISTS idx_obs_type;
DROP INDEX IF EXISTS idx_obs_session;
DROP TABLE IF EXISTS observations;

DROP TABLE IF EXISTS sessions;

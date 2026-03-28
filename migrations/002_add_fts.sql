-- +migrate Up
-- Add full-text search support using FTS5
-- This enables fast text search across observations and prompts

-- Create FTS5 virtual table for observations
CREATE VIRTUAL TABLE IF NOT EXISTS observations_fts USING fts5(
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

-- Create triggers to keep FTS index synchronized
CREATE TRIGGER IF NOT EXISTS obs_fts_insert AFTER INSERT ON observations BEGIN
    INSERT INTO observations_fts(rowid, title, content, tool_name, type, project, scope, topic_key)
    VALUES (new.id, new.title, new.content, new.tool_name, new.type, new.project, new.scope, new.topic_key);
END;

CREATE TRIGGER IF NOT EXISTS obs_fts_delete AFTER DELETE ON observations BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, title, content, tool_name, type, project, scope, topic_key)
    VALUES ('delete', old.id, old.title, old.content, old.tool_name, old.type, old.project, old.scope, old.topic_key);
END;

CREATE TRIGGER IF NOT EXISTS obs_fts_update AFTER UPDATE ON observations BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, title, content, tool_name, type, project, scope, topic_key)
    VALUES ('delete', old.id, old.title, old.content, old.tool_name, old.type, old.project, old.scope, old.topic_key);
    INSERT INTO observations_fts(rowid, title, content, tool_name, type, project, scope, topic_key)
    VALUES (new.id, new.title, new.content, new.tool_name, new.type, new.project, new.scope, new.topic_key);
END;

-- Create FTS5 virtual table for user prompts
CREATE VIRTUAL TABLE IF NOT EXISTS prompts_fts USING fts5(
    content,
    project,
    content='user_prompts',
    content_rowid='id',
    tokenize='porter unicode61'
);

-- Create triggers to keep FTS index synchronized
CREATE TRIGGER IF NOT EXISTS prompt_fts_insert AFTER INSERT ON user_prompts BEGIN
    INSERT INTO prompts_fts(rowid, content, project)
    VALUES (new.id, new.content, new.project);
END;

CREATE TRIGGER IF NOT EXISTS prompt_fts_delete AFTER DELETE ON user_prompts BEGIN
    INSERT INTO prompts_fts(prompts_fts, rowid, content, project)
    VALUES ('delete', old.id, old.content, old.project);
END;

CREATE TRIGGER IF NOT EXISTS prompt_fts_update AFTER UPDATE ON user_prompts BEGIN
    INSERT INTO prompts_fts(prompts_fts, rowid, content, project)
    VALUES ('delete', old.id, old.content, old.project);
    INSERT INTO prompts_fts(rowid, content, project)
    VALUES (new.id, new.content, new.project);
END;

-- +migrate Down
-- Remove FTS5 support

DROP TRIGGER IF EXISTS prompt_fts_update;
DROP TRIGGER IF EXISTS prompt_fts_delete;
DROP TRIGGER IF EXISTS prompt_fts_insert;
DROP TABLE IF EXISTS prompts_fts;

DROP TRIGGER IF EXISTS obs_fts_update;
DROP TRIGGER IF EXISTS obs_fts_delete;
DROP TRIGGER IF EXISTS obs_fts_insert;
DROP TABLE IF EXISTS observations_fts;

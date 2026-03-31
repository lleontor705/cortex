-- +migrate Up
-- Contextual FTS5 indexing: prepend metadata (type, topic_key, project) to content
-- column in FTS5 index. This enriches search so queries like "auth" find observations
-- with topic_key "architecture/auth" even if content doesn't mention "auth".
-- Based on Anthropic's Contextual Retrieval technique (2024).

DROP TRIGGER IF EXISTS obs_fts_insert;
DROP TRIGGER IF EXISTS obs_fts_update;

CREATE TRIGGER obs_fts_insert AFTER INSERT ON observations BEGIN
    INSERT INTO observations_fts(rowid, title, content, tool_name, type, project, scope, topic_key)
    VALUES (
        new.id,
        new.title,
        COALESCE(new.type, '') || ' ' || COALESCE(new.topic_key, '') || ' ' || COALESCE(new.project, '') || ' ' || new.content,
        new.tool_name, new.type, new.project, new.scope, new.topic_key
    );
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

-- Rebuild FTS index so existing observations get contextual content
INSERT INTO observations_fts(observations_fts) VALUES('rebuild');

-- +migrate Down
-- Restore original triggers without contextual content prepending

DROP TRIGGER IF EXISTS obs_fts_insert;
DROP TRIGGER IF EXISTS obs_fts_update;

CREATE TRIGGER obs_fts_insert AFTER INSERT ON observations BEGIN
    INSERT INTO observations_fts(rowid, title, content, tool_name, type, project, scope, topic_key)
    VALUES (new.id, new.title, new.content, new.tool_name, new.type, new.project, new.scope, new.topic_key);
END;

CREATE TRIGGER obs_fts_update AFTER UPDATE ON observations BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, title, content, tool_name, type, project, scope, topic_key)
    VALUES ('delete', old.id, old.title, old.content, old.tool_name, old.type, old.project, old.scope, old.topic_key);
    INSERT INTO observations_fts(rowid, title, content, tool_name, type, project, scope, topic_key)
    VALUES (new.id, new.title, new.content, new.tool_name, new.type, new.project, new.scope, new.topic_key);
END;

INSERT INTO observations_fts(observations_fts) VALUES('rebuild');

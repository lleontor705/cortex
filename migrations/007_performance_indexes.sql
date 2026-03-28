-- +migrate Up
-- Performance indexes for common query patterns

-- Most queries filter by deleted_at IS NULL (25+ occurrences across stores)
CREATE INDEX IF NOT EXISTS idx_obs_not_deleted ON observations(id) WHERE deleted_at IS NULL;

-- topic_key + project lookups (GetByTopicKey, topic key upsert)
CREATE INDEX IF NOT EXISTS idx_obs_topic_project ON observations(topic_key, project) WHERE deleted_at IS NULL;

-- project + type filtered queries (List, search, context)
CREATE INDEX IF NOT EXISTS idx_obs_project_type ON observations(project, type) WHERE deleted_at IS NULL;

-- created_at for archival age checks and timeline queries
CREATE INDEX IF NOT EXISTS idx_obs_created_not_deleted ON observations(created_at) WHERE deleted_at IS NULL;

-- +migrate Down
DROP INDEX IF EXISTS idx_obs_created_not_deleted;
DROP INDEX IF EXISTS idx_obs_project_type;
DROP INDEX IF EXISTS idx_obs_topic_project;
DROP INDEX IF EXISTS idx_obs_not_deleted;

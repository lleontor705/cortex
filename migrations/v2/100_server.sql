-- Cortex v2 server wave W11.1 (PostgreSQL 16+).
-- Forward-only and idempotent; this file is executed only by the server runner.
-- Apply as cortex_migration (or an explicitly privileged deployment role).

CREATE EXTENSION IF NOT EXISTS pgcrypto;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'cortex_app') THEN
        CREATE ROLE cortex_app NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'cortex_admin') THEN
        CREATE ROLE cortex_admin NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'cortex_migration') THEN
        CREATE ROLE cortex_migration NOLOGIN BYPASSRLS;
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS cortex_tenant_context (
    backend_pid integer NOT NULL,
    transaction_id bigint NOT NULL,
    tenant_id uuid NOT NULL,
    bound_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (backend_pid, transaction_id)
);
REVOKE ALL ON cortex_tenant_context FROM PUBLIC;
-- Remove the pre-W13 caller-selected binder on upgraded databases before
-- installing the principal-derived replacement.
DROP FUNCTION IF EXISTS cortex_set_tenant(uuid);

CREATE OR REPLACE FUNCTION cortex_current_tenant()
RETURNS uuid
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT tenant_id
      FROM public.cortex_tenant_context
     WHERE backend_pid = pg_backend_pid() AND transaction_id = txid_current()
$$;
REVOKE ALL ON FUNCTION cortex_current_tenant() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cortex_current_tenant() TO cortex_app, cortex_admin;

CREATE TABLE IF NOT EXISTS organizations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL UNIQUE,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid,
    updated_by uuid,
    UNIQUE (tenant_id, id)
);
CREATE TABLE IF NOT EXISTS workspaces (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL,
    organization_id bigint NOT NULL,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid, updated_by uuid,
    UNIQUE (tenant_id, id), UNIQUE (tenant_id, public_id),
    FOREIGN KEY (tenant_id, organization_id) REFERENCES organizations(tenant_id, id)
);
CREATE TABLE IF NOT EXISTS projects (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL,
    workspace_id bigint NOT NULL,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid, updated_by uuid,
    UNIQUE (tenant_id, id), UNIQUE (tenant_id, public_id),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id)
);
CREATE TABLE IF NOT EXISTS app_users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL,
    email text NOT NULL,
    display_name text,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid, updated_by uuid,
    UNIQUE (tenant_id, id), UNIQUE (tenant_id, public_id), UNIQUE (tenant_id, email)
);
CREATE TABLE IF NOT EXISTS service_accounts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid, updated_by uuid,
    UNIQUE (tenant_id, id), UNIQUE (tenant_id, public_id)
);
CREATE TABLE IF NOT EXISTS rbac_roles (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL, name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id), UNIQUE (tenant_id, name)
);
CREATE TABLE IF NOT EXISTS memberships (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL, user_id bigint NOT NULL, role_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id), UNIQUE (tenant_id, user_id, role_id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES app_users(tenant_id, id),
    FOREIGN KEY (tenant_id, role_id) REFERENCES rbac_roles(tenant_id, id)
);
CREATE TABLE IF NOT EXISTS api_tokens (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL, token_prefix text NOT NULL, token_digest bytea NOT NULL,
    subject_user_id bigint, subject_service_account_id bigint,
    scopes text[] NOT NULL DEFAULT '{}', workspace_ids uuid[] NOT NULL DEFAULT '{}', rate_limit_tier text NOT NULL DEFAULT 'standard',
    expires_at timestamptz, revoked_at timestamptz, last_used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id), UNIQUE (tenant_id, token_digest), UNIQUE (tenant_id, token_prefix),
    FOREIGN KEY (tenant_id, subject_user_id) REFERENCES app_users(tenant_id, id),
    FOREIGN KEY (tenant_id, subject_service_account_id) REFERENCES service_accounts(tenant_id, id)
);
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS token_prefix text;
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS scopes text[] NOT NULL DEFAULT '{}';
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS workspace_ids uuid[] NOT NULL DEFAULT '{}';
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS rate_limit_tier text NOT NULL DEFAULT 'standard';
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS last_used_at timestamptz;
CREATE INDEX IF NOT EXISTS api_tokens_prefix_idx ON api_tokens(tenant_id, token_prefix);

CREATE TABLE IF NOT EXISTS sessions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL, workspace_id bigint NOT NULL, project_id bigint,
    started_at timestamptz NOT NULL DEFAULT now(), ended_at timestamptz, summary text,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid, updated_by uuid, UNIQUE (tenant_id, id), UNIQUE (tenant_id, public_id),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id)
);
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS project_key text NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS observations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL, session_id bigint NOT NULL, project_id bigint,
    type text NOT NULL, title text NOT NULL, content text NOT NULL, topic_key text,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz,
    created_by uuid, updated_by uuid, UNIQUE (tenant_id, id), UNIQUE (tenant_id, public_id),
    FOREIGN KEY (tenant_id, session_id) REFERENCES sessions(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id)
);
ALTER TABLE observations ADD COLUMN IF NOT EXISTS revision_count integer NOT NULL DEFAULT 1;
ALTER TABLE observations ADD COLUMN IF NOT EXISTS project_key text NOT NULL DEFAULT '';
ALTER TABLE observations ADD COLUMN IF NOT EXISTS scope text NOT NULL DEFAULT 'project';
ALTER TABLE observations ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT 'manual';
ALTER TABLE observations ADD COLUMN IF NOT EXISTS search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', coalesce(title,'') || ' ' || coalesce(content,''))) STORED;
CREATE INDEX IF NOT EXISTS observations_search_vector_gin ON observations USING gin(search_vector);
DROP INDEX IF EXISTS observations_topic_key_active_uq;
CREATE UNIQUE INDEX IF NOT EXISTS observations_topic_key_active_uq ON observations(tenant_id, project_key, topic_key) WHERE topic_key IS NOT NULL AND deleted_at IS NULL;
CREATE TABLE IF NOT EXISTS importance_scores (
    tenant_id uuid NOT NULL,
    workspace_id bigint NOT NULL,
    project_id bigint,
    project_key text NOT NULL DEFAULT '',
    observation_id bigint NOT NULL,
    score double precision NOT NULL DEFAULT 0.5 CHECK (score >= 0.0 AND score <= 5.0),
    access_count integer NOT NULL DEFAULT 0 CHECK (access_count >= 0),
    last_accessed timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, observation_id),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, observation_id) REFERENCES observations(tenant_id, id)
);
CREATE INDEX IF NOT EXISTS importance_scores_workspace_project_score_idx ON importance_scores(tenant_id, workspace_id, project_key, score DESC, observation_id);
CREATE INDEX IF NOT EXISTS importance_scores_observation_idx ON importance_scores(tenant_id, observation_id);
CREATE TABLE IF NOT EXISTS observation_revisions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id uuid NOT NULL,
    observation_id bigint NOT NULL,
    revision integer NOT NULL,
    payload jsonb NOT NULL,
    reason text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, observation_id, revision),
    FOREIGN KEY (tenant_id, observation_id) REFERENCES observations(tenant_id, id)
);
CREATE TABLE IF NOT EXISTS prompts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL, session_id bigint NOT NULL, content text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid, updated_by uuid, UNIQUE (tenant_id, id), UNIQUE (tenant_id, public_id),
    FOREIGN KEY (tenant_id, session_id) REFERENCES sessions(tenant_id, id)
);
ALTER TABLE prompts ADD COLUMN IF NOT EXISTS project_key text NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS edges (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL, from_observation_id bigint NOT NULL, to_observation_id bigint NOT NULL,
    relation_type text NOT NULL, valid_from timestamptz, valid_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid, updated_by uuid, UNIQUE (tenant_id, id), UNIQUE (tenant_id, public_id),
    FOREIGN KEY (tenant_id, from_observation_id) REFERENCES observations(tenant_id, id),
    FOREIGN KEY (tenant_id, to_observation_id) REFERENCES observations(tenant_id, id)
);
ALTER TABLE edges ADD COLUMN IF NOT EXISTS evolution_id bigint;
ALTER TABLE edges ADD COLUMN IF NOT EXISTS evolution_type text NOT NULL DEFAULT 'original';
ALTER TABLE edges ADD COLUMN IF NOT EXISTS fact_state text NOT NULL DEFAULT 'current';
ALTER TABLE edges ADD COLUMN IF NOT EXISTS change_reason text;
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='edges_valid_range') THEN
        ALTER TABLE edges ADD CONSTRAINT edges_valid_range CHECK (valid_until IS NULL OR valid_from IS NULL OR valid_until > valid_from);
    END IF;
END $$;
CREATE TABLE IF NOT EXISTS entities (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL, entity_type text NOT NULL, entity_key text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid, updated_by uuid, UNIQUE (tenant_id, id), UNIQUE (tenant_id, public_id), UNIQUE (tenant_id, entity_type, entity_key)
);
CREATE TABLE IF NOT EXISTS observation_entities (
    tenant_id uuid NOT NULL, observation_id bigint NOT NULL, entity_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(), created_by uuid,
    PRIMARY KEY (tenant_id, observation_id, entity_id),
    FOREIGN KEY (tenant_id, observation_id) REFERENCES observations(tenant_id, id),
    FOREIGN KEY (tenant_id, entity_id) REFERENCES entities(tenant_id, id)
);
CREATE TABLE IF NOT EXISTS index_outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL, observation_id bigint NOT NULL, intent text NOT NULL,
    status text NOT NULL DEFAULT 'pending', attempts integer NOT NULL DEFAULT 0, available_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid, updated_by uuid, UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, observation_id) REFERENCES observations(tenant_id, id)
);
ALTER TABLE index_outbox ADD COLUMN IF NOT EXISTS completed_at timestamptz;
ALTER TABLE index_outbox ADD COLUMN IF NOT EXISTS error text;
ALTER TABLE index_outbox ADD COLUMN IF NOT EXISTS cause text;
ALTER TABLE index_outbox ADD COLUMN IF NOT EXISTS affected_rows integer NOT NULL DEFAULT 0;
ALTER TABLE index_outbox ADD COLUMN IF NOT EXISTS lease_owner text;
ALTER TABLE index_outbox ADD COLUMN IF NOT EXISTS leased_until timestamptz;
CREATE INDEX IF NOT EXISTS index_outbox_lease_recovery ON index_outbox(status, leased_until);

CREATE TABLE IF NOT EXISTS actor_subjects (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id uuid NOT NULL,
    subject text NOT NULL,
    actor_type text NOT NULL,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    active boolean NOT NULL DEFAULT true,
    revoked_at timestamptz,
    grant_version bigint NOT NULL DEFAULT 1,
    grant_digest text NOT NULL DEFAULT '',
    UNIQUE (tenant_id, subject)
);
ALTER TABLE actor_subjects ADD COLUMN IF NOT EXISTS active boolean NOT NULL DEFAULT true;
ALTER TABLE actor_subjects ADD COLUMN IF NOT EXISTS revoked_at timestamptz;
ALTER TABLE actor_subjects ADD COLUMN IF NOT EXISTS grant_version bigint NOT NULL DEFAULT 1;
ALTER TABLE actor_subjects ADD COLUMN IF NOT EXISTS grant_digest text NOT NULL DEFAULT '';

-- The only application binding entry point. It accepts an authenticated
-- actor public ID and a grant digest, never a caller-selected tenant. The
-- SECURITY DEFINER owner is the migration role (BYPASSRLS), so revocation and
-- digest checks are atomic with the transaction context installation.
CREATE OR REPLACE FUNCTION cortex_bind_principal(p_actor_public_id uuid, p_grant_digest text, p_grant_version bigint)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_tenant uuid;
    v_digest text;
BEGIN
    IF p_actor_public_id IS NULL OR NULLIF(p_grant_digest, '') IS NULL OR p_grant_version IS NULL OR p_grant_version <= 0 THEN
        RAISE EXCEPTION 'principal binding is required' USING ERRCODE = '28000';
    END IF;
    SELECT tenant_id, grant_digest INTO v_tenant, v_digest
      FROM public.actor_subjects
     WHERE public_id = p_actor_public_id
       AND active
       AND revoked_at IS NULL
       AND grant_version = p_grant_version
     FOR UPDATE;
    IF v_tenant IS NULL OR (v_digest <> '' AND v_digest <> p_grant_digest) THEN
        RAISE EXCEPTION 'principal grant is revoked or stale' USING ERRCODE = '28000';
    END IF;
    DELETE FROM public.cortex_tenant_context
     WHERE backend_pid = pg_backend_pid() AND transaction_id <> txid_current();
    INSERT INTO public.cortex_tenant_context (backend_pid, transaction_id, tenant_id)
    VALUES (pg_backend_pid(), txid_current(), v_tenant)
    ON CONFLICT (backend_pid, transaction_id) DO UPDATE
      SET tenant_id = EXCLUDED.tenant_id, bound_at = clock_timestamp();
END
$$;
REVOKE ALL ON FUNCTION cortex_bind_principal(uuid,text,bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION cortex_bind_principal(uuid,text,bigint) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION cortex_bind_principal(uuid,text,bigint) TO cortex_app;
CREATE TABLE IF NOT EXISTS audit_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL, actor_public_id uuid, action text NOT NULL, resource_type text NOT NULL,
    resource_public_id uuid, metadata jsonb NOT NULL DEFAULT '{}'::jsonb, previous_hash bytea, event_hash bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id), UNIQUE (tenant_id, public_id)
);

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['organizations','workspaces','projects','app_users','service_accounts','rbac_roles','memberships','api_tokens','sessions','observations','importance_scores','observation_revisions','prompts','edges','entities','observation_entities','index_outbox','audit_events','actor_subjects'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS cortex_tenant_isolation ON %I', t);
        EXECUTE format('CREATE POLICY cortex_tenant_isolation ON %I AS PERMISSIVE FOR ALL TO PUBLIC USING (tenant_id = public.cortex_current_tenant()) WITH CHECK (tenant_id = public.cortex_current_tenant())', t);
    END LOOP;
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON organizations, workspaces, projects, app_users, service_accounts, rbac_roles, memberships, api_tokens, sessions, observations, importance_scores, observation_revisions, prompts, edges, entities, observation_entities, index_outbox, audit_events, actor_subjects TO cortex_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO cortex_app;
GRANT SELECT ON organizations, workspaces, projects, app_users, service_accounts, rbac_roles, memberships, api_tokens, sessions, observations, importance_scores, observation_revisions, prompts, edges, entities, observation_entities, index_outbox, audit_events TO cortex_admin;
GRANT ALL ON organizations, workspaces, projects, app_users, service_accounts, rbac_roles, memberships, api_tokens, sessions, observations, importance_scores, observation_revisions, prompts, edges, entities, observation_entities, index_outbox, audit_events TO cortex_migration;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO cortex_migration;

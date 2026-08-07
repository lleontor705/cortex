-- Bidirectional replication metadata and an append-only tenant change feed.
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS client_id text;
ALTER TABLE observations ADD COLUMN IF NOT EXISTS client_id text;
ALTER TABLE observations ADD COLUMN IF NOT EXISTS confidence double precision NOT NULL DEFAULT 1;
ALTER TABLE observations ADD COLUMN IF NOT EXISTS tags jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE prompts ADD COLUMN IF NOT EXISTS client_id text;
ALTER TABLE edges ADD COLUMN IF NOT EXISTS client_id text;
ALTER TABLE edges ADD COLUMN IF NOT EXISTS deleted_at timestamptz;

UPDATE sessions SET client_id=public_id::text WHERE client_id IS NULL;
UPDATE observations SET client_id=public_id::text WHERE client_id IS NULL;
UPDATE prompts SET client_id=public_id::text WHERE client_id IS NULL;
UPDATE edges SET client_id=public_id::text WHERE client_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS sessions_client_id_uq ON sessions(tenant_id, workspace_id, client_id) WHERE client_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS observations_client_id_uq ON observations(tenant_id, client_id) WHERE client_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS prompts_client_id_uq ON prompts(tenant_id, client_id) WHERE client_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS edges_client_id_uq ON edges(tenant_id, client_id) WHERE client_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS sync_changes (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id uuid NOT NULL,
    workspace_id bigint NOT NULL,
    entity_type text NOT NULL,
    public_id uuid NOT NULL,
    sync_id text NOT NULL,
    deleted boolean NOT NULL DEFAULT false,
    changed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sync_changes_tenant_workspace_sequence_idx
    ON sync_changes(tenant_id, workspace_id, sequence);

ALTER TABLE sync_changes ENABLE ROW LEVEL SECURITY;
ALTER TABLE sync_changes FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS sync_changes_tenant_isolation ON sync_changes;
CREATE POLICY sync_changes_tenant_isolation ON sync_changes
    USING (tenant_id = public.cortex_current_tenant())
    WITH CHECK (tenant_id = public.cortex_current_tenant());
GRANT SELECT, INSERT ON sync_changes TO cortex_app;
GRANT USAGE, SELECT ON SEQUENCE sync_changes_sequence_seq TO cortex_app;

CREATE OR REPLACE FUNCTION cortex_record_sync_change() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_tenant uuid;
    v_workspace bigint;
    v_public_id uuid;
    v_sync_id text;
    v_deleted boolean;
BEGIN
    IF TG_OP = 'DELETE' THEN
        v_public_id := OLD.public_id;
        v_sync_id := COALESCE(OLD.client_id, OLD.public_id::text);
        v_tenant := OLD.tenant_id;
    ELSE
        v_public_id := NEW.public_id;
        v_sync_id := COALESCE(NEW.client_id, NEW.public_id::text);
        v_tenant := NEW.tenant_id;
    END IF;
    IF TG_TABLE_NAME = 'sessions' THEN
        IF TG_OP = 'DELETE' THEN v_workspace := OLD.workspace_id; ELSE v_workspace := NEW.workspace_id; END IF;
    ELSIF TG_TABLE_NAME = 'observations' THEN
        SELECT workspace_id INTO v_workspace FROM sessions
         WHERE tenant_id=v_tenant
           AND id=CASE WHEN TG_OP = 'DELETE' THEN OLD.session_id ELSE NEW.session_id END;
    ELSIF TG_TABLE_NAME = 'prompts' THEN
        SELECT workspace_id INTO v_workspace FROM sessions
         WHERE tenant_id=v_tenant
           AND id=CASE WHEN TG_OP = 'DELETE' THEN OLD.session_id ELSE NEW.session_id END;
    ELSE
        SELECT s.workspace_id INTO v_workspace
          FROM observations o JOIN sessions s ON s.tenant_id=o.tenant_id AND s.id=o.session_id
         WHERE o.tenant_id=v_tenant
           AND o.id=CASE WHEN TG_OP = 'DELETE' THEN OLD.from_observation_id ELSE NEW.from_observation_id END;
    END IF;
    v_deleted := TG_OP = 'DELETE';
    IF TG_OP <> 'DELETE' AND TG_TABLE_NAME IN ('observations', 'edges') THEN
        v_deleted := NEW.deleted_at IS NOT NULL;
    END IF;
    INSERT INTO sync_changes(tenant_id,workspace_id,entity_type,public_id,sync_id,deleted)
    VALUES(v_tenant,v_workspace,TG_TABLE_NAME,v_public_id,v_sync_id,v_deleted);
    RETURN COALESCE(NEW, OLD);
END $$;

DROP TRIGGER IF EXISTS sessions_sync_change ON sessions;
CREATE TRIGGER sessions_sync_change AFTER INSERT OR UPDATE OR DELETE ON sessions FOR EACH ROW EXECUTE FUNCTION cortex_record_sync_change();
DROP TRIGGER IF EXISTS observations_sync_change ON observations;
CREATE TRIGGER observations_sync_change AFTER INSERT OR UPDATE OR DELETE ON observations FOR EACH ROW EXECUTE FUNCTION cortex_record_sync_change();
DROP TRIGGER IF EXISTS prompts_sync_change ON prompts;
CREATE TRIGGER prompts_sync_change AFTER INSERT OR UPDATE OR DELETE ON prompts FOR EACH ROW EXECUTE FUNCTION cortex_record_sync_change();
DROP TRIGGER IF EXISTS edges_sync_change ON edges;
CREATE TRIGGER edges_sync_change AFTER INSERT OR UPDATE OR DELETE ON edges FOR EACH ROW EXECUTE FUNCTION cortex_record_sync_change();

-- Existing rows predate the triggers but must be visible to a new device's
-- initial cursor-zero pull.
INSERT INTO sync_changes(tenant_id,workspace_id,entity_type,public_id,sync_id,deleted)
SELECT tenant_id,workspace_id,'sessions',public_id,COALESCE(client_id,public_id::text),false FROM sessions;
INSERT INTO sync_changes(tenant_id,workspace_id,entity_type,public_id,sync_id,deleted)
SELECT o.tenant_id,s.workspace_id,'observations',o.public_id,COALESCE(o.client_id,o.public_id::text),o.deleted_at IS NOT NULL FROM observations o JOIN sessions s ON s.tenant_id=o.tenant_id AND s.id=o.session_id;
INSERT INTO sync_changes(tenant_id,workspace_id,entity_type,public_id,sync_id,deleted)
SELECT p.tenant_id,s.workspace_id,'prompts',p.public_id,COALESCE(p.client_id,p.public_id::text),false FROM prompts p JOIN sessions s ON s.tenant_id=p.tenant_id AND s.id=p.session_id;
INSERT INTO sync_changes(tenant_id,workspace_id,entity_type,public_id,sync_id,deleted)
SELECT e.tenant_id,s.workspace_id,'edges',e.public_id,COALESCE(e.client_id,e.public_id::text),e.deleted_at IS NOT NULL FROM edges e JOIN observations o ON o.tenant_id=e.tenant_id AND o.id=e.from_observation_id JOIN sessions s ON s.tenant_id=o.tenant_id AND s.id=o.session_id;

-- Cortex v2 server wave: PostgreSQL migration 105 (forward-only).
-- Workspace binding and isolation for observation topics and handoff
-- receipts (AMD-MIG-105, REM-MIG-001). Both backfills derive the workspace
-- from the only durable chain (session -> observation -> receipt); any
-- orphan, durable pending receipt, or unresolved row aborts the whole
-- transaction without a partial ledger entry. Down is not supported: the
-- line is forward-only and fail-closed. Like 104, no IF NOT EXISTS is used
-- on purpose so stale, unledgered artifacts fail closed instead of being
-- silently adopted. A 104-era runtime keeps writing observations through the
-- BEFORE trigger below; durable pending receipts intentionally fail closed
-- because a tenant-wide workspace default would be insecure.

-- 1. Observations gain a workspace column, backfilled strictly from the
--    session of the same tenant. Preflight aborts on orphans or leftovers.
ALTER TABLE observations ADD COLUMN workspace_id bigint;

DO $$
DECLARE
    v_orphans bigint;
BEGIN
    SELECT count(*) INTO v_orphans
      FROM observations o
      LEFT JOIN sessions s ON s.tenant_id = o.tenant_id AND s.id = o.session_id
     WHERE s.id IS NULL;
    IF v_orphans > 0 THEN
        RAISE EXCEPTION 'migration 105 aborted: % observation(s) reference no session in their tenant', v_orphans;
    END IF;
END
$$;

UPDATE observations o
   SET workspace_id = s.workspace_id
  FROM sessions s
 WHERE s.tenant_id = o.tenant_id AND s.id = o.session_id;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM observations WHERE workspace_id IS NULL) THEN
        RAISE EXCEPTION 'migration 105 aborted: observation workspace backfill left unresolved rows';
    END IF;
END
$$;

-- 2. The binding trigger keeps 104-era observation DML working: INSERT or
--    UPDATE statements that omit workspace_id get the session's workspace,
--    and an explicit workspace that disagrees with the session is rejected.
CREATE OR REPLACE FUNCTION cortex_bind_observation_workspace() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_workspace bigint;
BEGIN
    SELECT s.workspace_id INTO v_workspace
      FROM sessions s
     WHERE s.tenant_id = NEW.tenant_id AND s.id = NEW.session_id;
    IF v_workspace IS NULL THEN
        RAISE EXCEPTION 'observation session % does not resolve a workspace in its tenant', NEW.session_id
            USING ERRCODE = '23503';
    END IF;
    IF NEW.workspace_id IS NOT NULL AND NEW.workspace_id <> v_workspace THEN
        RAISE EXCEPTION 'observation workspace % conflicts with session workspace %', NEW.workspace_id, v_workspace
            USING ERRCODE = '23514';
    END IF;
    NEW.workspace_id := v_workspace;
    RETURN NEW;
END
$$;

CREATE TRIGGER observations_bind_workspace
    BEFORE INSERT OR UPDATE OF session_id, workspace_id ON observations
    FOR EACH ROW EXECUTE FUNCTION cortex_bind_observation_workspace();

ALTER TABLE observations ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE observations
    ADD CONSTRAINT observations_tenant_workspace_fkey
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id);

-- 3. Active topic keys become unique per workspace instead of per tenant.
--    Duplicates that would block the new partial index abort up front; the
--    tenant-wide index is retired only after the workspace-scoped
--    replacement exists and is renamed into its place.
DO $$
DECLARE
    v_duplicates bigint;
BEGIN
    WITH keys AS (
        SELECT tenant_id, workspace_id, project_key, topic_key
          FROM observations
         WHERE topic_key IS NOT NULL AND deleted_at IS NULL
    )
    SELECT count(*) INTO v_duplicates FROM (
        SELECT 1 FROM keys
        GROUP BY tenant_id, workspace_id, project_key, topic_key
        HAVING count(*) > 1
    ) d;
    IF v_duplicates > 0 THEN
        RAISE EXCEPTION 'migration 105 aborted: % active topic key(s) collide inside a workspace', v_duplicates;
    END IF;
END
$$;

CREATE UNIQUE INDEX observations_topic_key_active_ws_uq
    ON observations(tenant_id, workspace_id, project_key, topic_key)
    WHERE topic_key IS NOT NULL AND deleted_at IS NULL;

-- 4. Handoff receipts gain a workspace column so idempotent namespaces are
--    isolated per workspace. Durable pending receipts have no observation to
--    derive a workspace from and abort the migration instead of guessing.
ALTER TABLE handoff_receipts ADD COLUMN workspace_id bigint;

DO $$
DECLARE
    v_pending bigint;
BEGIN
    SELECT count(*) INTO v_pending FROM handoff_receipts WHERE state = 'pending';
    IF v_pending > 0 THEN
        RAISE EXCEPTION 'migration 105 aborted: % durable pending handoff receipt(s) cannot resolve a workspace; resolve them before upgrading', v_pending;
    END IF;
END
$$;

UPDATE handoff_receipts r
   SET workspace_id = o.workspace_id
  FROM observations o
 WHERE o.tenant_id = r.tenant_id AND o.id = r.observation_id;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM handoff_receipts WHERE workspace_id IS NULL) THEN
        RAISE EXCEPTION 'migration 105 aborted: handoff receipt workspace backfill left unresolved rows';
    END IF;
END
$$;

-- 5. Receipts become workspace-hardened: NOT NULL, tenant/workspace foreign
--    key, and a workspace-scoped primary key so two workspaces of one tenant
--    hold independent idempotent namespaces for the same (scope, key).
ALTER TABLE handoff_receipts ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE handoff_receipts
    ADD CONSTRAINT handoff_receipts_tenant_workspace_fkey
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id);
ALTER TABLE handoff_receipts DROP CONSTRAINT handoff_receipts_pkey;
ALTER TABLE handoff_receipts ADD PRIMARY KEY (tenant_id, workspace_id, scope, key);

-- 6. Retire the tenant-wide topic index only after its workspace-scoped
--    replacement is in place, then take over its name.
DROP INDEX observations_topic_key_active_uq;
ALTER INDEX observations_topic_key_active_ws_uq RENAME TO observations_topic_key_active_uq;

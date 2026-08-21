-- Cortex v2 server wave: PostgreSQL migration 107 (forward-only, additive).
-- Workspace-safe sync identities, the SEC-03 schema closure after the T01
-- exploit proof. Migration 102 already made a session's client_id unique per
-- (tenant, workspace), but the observation, prompt, and edge client_id
-- indexes stayed unique per TENANT, so a sibling workspace's push collapsed
-- onto another workspace's row (ON CONFLICT (tenant_id, client_id)),
-- overwrote its content, soft-deleted it, or resolved edge endpoints
-- tenant-wide into the other workspace's observations. This migration binds
-- prompts and edges to a workspace (105 already bound observations),
-- backfills strictly from the only durable chains (prompt -> session,
-- edge -> from-observation), and swaps the three tenant-wide client_id
-- unique indexes for workspace-scoped replacements so identical sibling
-- client IDs coexist and collide only inside one workspace. Every preflight
-- runs BEFORE any mutating statement and aborts on orphaned references,
-- unresolved rows, cross-workspace edge endpoints, and duplicate
-- (tenant, workspace, client_id) groups: collisions are never merged,
-- deduplicated, or dropped — resolve the data, then re-run the migration.
-- Down is not supported: the line is forward-only and fail-closed. Like
-- 104/105/106, no IF NOT EXISTS is used on purpose so a stale, unledgered
-- artifact fails closed instead of being silently adopted.

-- 1. Read-only preflights (all before any mutation). Any violation aborts
--    the whole transaction: the ledger row is never written and the schema
--    is left untouched at head 106.
DO $$
DECLARE
    v_orphans bigint;
    v_crossed bigint;
    v_duplicates bigint;
BEGIN
    -- Prompts must resolve a session in their own tenant.
    SELECT count(*) INTO v_orphans
      FROM prompts p
      LEFT JOIN sessions s ON s.tenant_id = p.tenant_id AND s.id = p.session_id
     WHERE s.id IS NULL;
    IF v_orphans > 0 THEN
        RAISE EXCEPTION 'migration 107 aborted: % prompt(s) reference no session in their tenant', v_orphans;
    END IF;
    -- Edge endpoints must resolve observations in their own tenant.
    SELECT count(*) INTO v_orphans
      FROM edges e
      LEFT JOIN observations f ON f.tenant_id = e.tenant_id AND f.id = e.from_observation_id
      LEFT JOIN observations t ON t.tenant_id = e.tenant_id AND t.id = e.to_observation_id
     WHERE f.id IS NULL OR t.id IS NULL;
    IF v_orphans > 0 THEN
        RAISE EXCEPTION 'migration 107 aborted: % edge(s) reference no observation in their tenant', v_orphans;
    END IF;
    -- Edge endpoints must already share one workspace: a cross-workspace
    -- edge has no safe workspace to bind to and is never auto-split.
    SELECT count(*) INTO v_crossed
      FROM edges e
      JOIN observations f ON f.tenant_id = e.tenant_id AND f.id = e.from_observation_id
      JOIN observations t ON t.tenant_id = e.tenant_id AND t.id = e.to_observation_id
     WHERE f.workspace_id <> t.workspace_id;
    IF v_crossed > 0 THEN
        RAISE EXCEPTION 'migration 107 aborted: % edge(s) cross workspaces; resolve them before upgrading', v_crossed;
    END IF;
    -- Identical client IDs may exist in DIFFERENT workspaces (that is the
    -- fix), but duplicates inside ONE workspace would make the workspace-
    -- scoped uniqueness ambiguous and are a hard stop, never a merge.
    SELECT count(*) INTO v_duplicates FROM (
        SELECT 1 FROM observations
         WHERE client_id IS NOT NULL
         GROUP BY tenant_id, workspace_id, client_id HAVING count(*) > 1
    ) d;
    IF v_duplicates > 0 THEN
        RAISE EXCEPTION 'migration 107 aborted: % observation client_id group(s) collide inside a workspace', v_duplicates;
    END IF;
    SELECT count(*) INTO v_duplicates FROM (
        SELECT 1
          FROM prompts p JOIN sessions s ON s.tenant_id = p.tenant_id AND s.id = p.session_id
         WHERE p.client_id IS NOT NULL
         GROUP BY p.tenant_id, s.workspace_id, p.client_id HAVING count(*) > 1
    ) d;
    IF v_duplicates > 0 THEN
        RAISE EXCEPTION 'migration 107 aborted: % prompt client_id group(s) collide inside a workspace', v_duplicates;
    END IF;
    SELECT count(*) INTO v_duplicates FROM (
        SELECT 1
          FROM edges e
          JOIN observations f ON f.tenant_id = e.tenant_id AND f.id = e.from_observation_id
         WHERE e.client_id IS NOT NULL
         GROUP BY e.tenant_id, f.workspace_id, e.client_id HAVING count(*) > 1
    ) d;
    IF v_duplicates > 0 THEN
        RAISE EXCEPTION 'migration 107 aborted: % edge client_id group(s) collide inside a workspace', v_duplicates;
    END IF;
END
$$;

-- 2. Prompts gain a workspace column, backfilled strictly from the session
--    of the same tenant; orphans and leftovers already aborted above.
ALTER TABLE prompts ADD COLUMN workspace_id bigint;

UPDATE prompts p
   SET workspace_id = s.workspace_id
  FROM sessions s
 WHERE s.tenant_id = p.tenant_id AND s.id = p.session_id;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM prompts WHERE workspace_id IS NULL) THEN
        RAISE EXCEPTION 'migration 107 aborted: prompt workspace backfill left unresolved rows';
    END IF;
END
$$;

-- The binding trigger keeps legacy prompt DML working: statements that omit
-- workspace_id get the session's workspace, and an explicit workspace that
-- disagrees with the session is rejected.
CREATE FUNCTION cortex_bind_prompt_workspace() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_workspace bigint;
BEGIN
    SELECT s.workspace_id INTO v_workspace
      FROM sessions s
     WHERE s.tenant_id = NEW.tenant_id AND s.id = NEW.session_id;
    IF v_workspace IS NULL THEN
        RAISE EXCEPTION 'prompt session % does not resolve a workspace in its tenant', NEW.session_id
            USING ERRCODE = '23503';
    END IF;
    IF NEW.workspace_id IS NOT NULL AND NEW.workspace_id <> v_workspace THEN
        RAISE EXCEPTION 'prompt workspace % conflicts with session workspace %', NEW.workspace_id, v_workspace
            USING ERRCODE = '23514';
    END IF;
    NEW.workspace_id := v_workspace;
    RETURN NEW;
END
$$;
REVOKE ALL ON FUNCTION cortex_bind_prompt_workspace() FROM PUBLIC;

CREATE TRIGGER prompts_bind_workspace
    BEFORE INSERT OR UPDATE OF session_id, workspace_id ON prompts
    FOR EACH ROW EXECUTE FUNCTION cortex_bind_prompt_workspace();

ALTER TABLE prompts ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE prompts
    ADD CONSTRAINT prompts_tenant_workspace_fkey
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id);

-- 3. Edges gain a workspace column, backfilled strictly from the
--    from-observation of the same tenant (whose workspace came from 105's
--    session-derived backfill); orphans, leftovers, and cross-workspace
--    endpoints already aborted above.
ALTER TABLE edges ADD COLUMN workspace_id bigint;

UPDATE edges e
   SET workspace_id = o.workspace_id
  FROM observations o
 WHERE o.tenant_id = e.tenant_id AND o.id = e.from_observation_id;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM edges WHERE workspace_id IS NULL) THEN
        RAISE EXCEPTION 'migration 107 aborted: edge workspace backfill left unresolved rows';
    END IF;
END
$$;

-- The binding trigger derives the edge workspace from its from-observation
-- and requires BOTH endpoints to resolve inside that same workspace, so an
-- edge can never span (or smuggle a reference across) workspace boundaries.
CREATE FUNCTION cortex_bind_edge_workspace() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_from_workspace bigint;
    v_to_workspace bigint;
BEGIN
    SELECT o.workspace_id INTO v_from_workspace
      FROM observations o
     WHERE o.tenant_id = NEW.tenant_id AND o.id = NEW.from_observation_id;
    IF v_from_workspace IS NULL THEN
        RAISE EXCEPTION 'edge from-observation % does not resolve a workspace in its tenant', NEW.from_observation_id
            USING ERRCODE = '23503';
    END IF;
    SELECT o.workspace_id INTO v_to_workspace
      FROM observations o
     WHERE o.tenant_id = NEW.tenant_id AND o.id = NEW.to_observation_id;
    IF v_to_workspace IS NULL THEN
        RAISE EXCEPTION 'edge to-observation % does not resolve a workspace in its tenant', NEW.to_observation_id
            USING ERRCODE = '23503';
    END IF;
    IF v_to_workspace <> v_from_workspace THEN
        RAISE EXCEPTION 'edge endpoints must share one workspace (from %, to %)', v_from_workspace, v_to_workspace
            USING ERRCODE = '23514';
    END IF;
    IF NEW.workspace_id IS NOT NULL AND NEW.workspace_id <> v_from_workspace THEN
        RAISE EXCEPTION 'edge workspace % conflicts with from-observation workspace %', NEW.workspace_id, v_from_workspace
            USING ERRCODE = '23514';
    END IF;
    NEW.workspace_id := v_from_workspace;
    RETURN NEW;
END
$$;
REVOKE ALL ON FUNCTION cortex_bind_edge_workspace() FROM PUBLIC;

CREATE TRIGGER edges_bind_workspace
    BEFORE INSERT OR UPDATE OF from_observation_id, to_observation_id, workspace_id ON edges
    FOR EACH ROW EXECUTE FUNCTION cortex_bind_edge_workspace();

ALTER TABLE edges ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE edges
    ADD CONSTRAINT edges_tenant_workspace_fkey
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id);

-- 4. The tenant-wide client_id unique indexes become workspace-scoped. The
--    replacements are created and validated first; only then are the
--    tenant-wide indexes retired and the replacements renamed into their
--    names, exactly like the 105 topic-key swap.
CREATE UNIQUE INDEX observations_client_id_ws_uq
    ON observations(tenant_id, workspace_id, client_id)
    WHERE client_id IS NOT NULL;
CREATE UNIQUE INDEX prompts_client_id_ws_uq
    ON prompts(tenant_id, workspace_id, client_id)
    WHERE client_id IS NOT NULL;
CREATE UNIQUE INDEX edges_client_id_ws_uq
    ON edges(tenant_id, workspace_id, client_id)
    WHERE client_id IS NOT NULL;

DROP INDEX observations_client_id_uq;
DROP INDEX prompts_client_id_uq;
DROP INDEX edges_client_id_uq;
ALTER INDEX observations_client_id_ws_uq RENAME TO observations_client_id_uq;
ALTER INDEX prompts_client_id_ws_uq RENAME TO prompts_client_id_uq;
ALTER INDEX edges_client_id_ws_uq RENAME TO edges_client_id_uq;

-- 5. Workspace-scoped feed indexes for list/search and sync hydration
--    predicates (tenant_id + workspace_id) on the three newly bound tables.
CREATE INDEX observations_tenant_workspace_idx
    ON observations(tenant_id, workspace_id, id);
CREATE INDEX prompts_tenant_workspace_idx
    ON prompts(tenant_id, workspace_id, id);
CREATE INDEX edges_tenant_workspace_idx
    ON edges(tenant_id, workspace_id, id);

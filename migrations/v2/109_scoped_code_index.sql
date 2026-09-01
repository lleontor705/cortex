-- Cortex v2 server migration 109: tenant/workspace/project-scoped AST index.
-- Legacy project-only rows are deliberately not copied: their durable scope
-- cannot be reconstructed safely. This migration is forward-only.

-- Preflight every known collision and the optional legacy pair before the
-- first mutating statement. Stale 109 objects are never adopted.
DO $$
DECLARE
    v_object text;
    v_owner text;
    v_columns integer;
BEGIN
    FOREACH v_object IN ARRAY ARRAY[
        'scoped_code_symbols', 'scoped_code_relations',
        'scoped_code_index_state', 'projects_tenant_workspace_id_uq'
    ] LOOP
        IF to_regclass('public.' || v_object) IS NOT NULL THEN
            RAISE EXCEPTION 'migration 109 aborted: conflicting object public.% already exists', v_object;
        END IF;
    END LOOP;

    IF (to_regclass('public.code_symbols') IS NULL) <> (to_regclass('public.code_relations') IS NULL) THEN
        RAISE EXCEPTION 'migration 109 aborted: incomplete legacy code index';
    END IF;

    IF to_regclass('public.code_symbols') IS NOT NULL THEN
        FOREACH v_object IN ARRAY ARRAY['code_symbols', 'code_relations'] LOOP
            SELECT pg_get_userbyid(c.relowner)
              INTO v_owner
              FROM pg_class c
             WHERE c.oid = to_regclass('public.' || v_object)
               AND c.relkind = 'r';
            -- Trusted bootstrap and legacy runtime owners are accepted only
            -- until this migration transfers both tables to cortex_migration.
            IF v_owner IS NULL OR v_owner NOT IN ('cortex_app', 'cortex_migration', 'postgres', 'cortex_runtime') THEN
                RAISE EXCEPTION 'migration 109 aborted: unexpected owner % for public.%', COALESCE(v_owner, '<none>'), v_object;
            END IF;
        END LOOP;

        SELECT count(*) INTO v_columns
          FROM information_schema.columns
         WHERE table_schema = 'public' AND table_name = 'code_symbols'
           AND column_name = ANY (ARRAY['id','project','file_path','line_number','kind','name']);
        IF v_columns <> 6 THEN
            RAISE EXCEPTION 'migration 109 aborted: unexpected legacy code_symbols shape';
        END IF;
        SELECT count(*) INTO v_columns
          FROM information_schema.columns
         WHERE table_schema = 'public' AND table_name = 'code_relations'
           AND column_name = ANY (ARRAY['id','project','source_id','target_id','relation']);
        IF v_columns <> 5 THEN
            RAISE EXCEPTION 'migration 109 aborted: unexpected legacy code_relations shape';
        END IF;
    END IF;
END $$;

CREATE UNIQUE INDEX projects_tenant_workspace_id_uq
    ON projects(tenant_id, workspace_id, id);

CREATE TABLE scoped_code_symbols (
    tenant_id uuid NOT NULL,
    workspace_id bigint NOT NULL,
    project_id bigint NOT NULL,
    id varchar(64) NOT NULL,
    project varchar(128) NOT NULL,
    file_path text NOT NULL,
    line_number integer NOT NULL CHECK (line_number > 0),
    end_line integer NOT NULL CHECK (end_line >= line_number),
    start_col integer,
    end_col integer,
    kind varchar(32) NOT NULL,
    name varchar(255) NOT NULL,
    package_name varchar(128),
    parent_id varchar(128),
    visibility varchar(32),
    signature text,
    doc_summary text,
    parameters jsonb,
    return_type text,
    complexity integer NOT NULL DEFAULT 1 CHECK (complexity > 0),
    metadata jsonb,
    file_hash varchar(64),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, workspace_id, project_id, id),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, workspace_id, project_id)
        REFERENCES projects(tenant_id, workspace_id, id) ON DELETE RESTRICT
);
CREATE INDEX scoped_code_symbols_project_idx
    ON scoped_code_symbols(tenant_id, workspace_id, project_id, project);
CREATE INDEX scoped_code_symbols_file_idx
    ON scoped_code_symbols(tenant_id, workspace_id, project_id, file_path);
CREATE INDEX scoped_code_symbols_name_idx
    ON scoped_code_symbols(tenant_id, workspace_id, project_id, name);

CREATE TABLE scoped_code_relations (
    tenant_id uuid NOT NULL,
    workspace_id bigint NOT NULL,
    project_id bigint NOT NULL,
    id bigint GENERATED ALWAYS AS IDENTITY,
    project varchar(128) NOT NULL,
    source_id varchar(64) NOT NULL,
    target_id varchar(64) NOT NULL,
    relation varchar(32) NOT NULL,
    confidence real NOT NULL DEFAULT 1.0 CHECK (confidence >= 0 AND confidence <= 1),
    reasoning text,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, workspace_id, project_id, id),
    UNIQUE (tenant_id, workspace_id, project_id, source_id, target_id, relation),
    FOREIGN KEY (tenant_id, workspace_id, project_id)
        REFERENCES projects(tenant_id, workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, workspace_id, project_id, source_id)
        REFERENCES scoped_code_symbols(tenant_id, workspace_id, project_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, workspace_id, project_id, target_id)
        REFERENCES scoped_code_symbols(tenant_id, workspace_id, project_id, id) ON DELETE RESTRICT
);
CREATE INDEX scoped_code_relations_source_idx
    ON scoped_code_relations(tenant_id, workspace_id, project_id, source_id);
CREATE INDEX scoped_code_relations_target_idx
    ON scoped_code_relations(tenant_id, workspace_id, project_id, target_id);

CREATE TABLE scoped_code_index_state (
    tenant_id uuid NOT NULL,
    workspace_id bigint NOT NULL,
    project_id bigint NOT NULL,
    project varchar(128) NOT NULL,
    state varchar(16) NOT NULL DEFAULT 'missing'
        CHECK (state IN ('missing', 'indexing', 'ready', 'failed')),
    symbol_count bigint NOT NULL DEFAULT 0 CHECK (symbol_count >= 0),
    relation_count bigint NOT NULL DEFAULT 0 CHECK (relation_count >= 0),
    index_checksum varchar(64),
    last_error_code varchar(64),
    indexed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, workspace_id, project_id),
    UNIQUE (tenant_id, workspace_id, project),
    FOREIGN KEY (tenant_id, workspace_id, project_id)
        REFERENCES projects(tenant_id, workspace_id, id) ON DELETE RESTRICT
);

ALTER TABLE scoped_code_symbols ENABLE ROW LEVEL SECURITY;
ALTER TABLE scoped_code_symbols FORCE ROW LEVEL SECURITY;
CREATE POLICY cortex_scoped_code_isolation ON scoped_code_symbols FOR ALL TO PUBLIC
    USING (tenant_id = cortex_current_tenant() AND workspace_id = cortex_current_workspace() AND project_id = cortex_current_project())
    WITH CHECK (tenant_id = cortex_current_tenant() AND workspace_id = cortex_current_workspace() AND project_id = cortex_current_project());
ALTER TABLE scoped_code_relations ENABLE ROW LEVEL SECURITY;
ALTER TABLE scoped_code_relations FORCE ROW LEVEL SECURITY;
CREATE POLICY cortex_scoped_code_isolation ON scoped_code_relations FOR ALL TO PUBLIC
    USING (tenant_id = cortex_current_tenant() AND workspace_id = cortex_current_workspace() AND project_id = cortex_current_project())
    WITH CHECK (tenant_id = cortex_current_tenant() AND workspace_id = cortex_current_workspace() AND project_id = cortex_current_project());
ALTER TABLE scoped_code_index_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE scoped_code_index_state FORCE ROW LEVEL SECURITY;
CREATE POLICY cortex_scoped_code_isolation ON scoped_code_index_state FOR ALL TO PUBLIC
    USING (tenant_id = cortex_current_tenant() AND workspace_id = cortex_current_workspace() AND project_id = cortex_current_project())
    WITH CHECK (tenant_id = cortex_current_tenant() AND workspace_id = cortex_current_workspace() AND project_id = cortex_current_project());

REVOKE ALL ON scoped_code_symbols, scoped_code_relations, scoped_code_index_state FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON scoped_code_symbols, scoped_code_relations TO cortex_app;
GRANT SELECT, INSERT, UPDATE ON scoped_code_index_state TO cortex_app;
GRANT SELECT ON scoped_code_symbols, scoped_code_relations, scoped_code_index_state TO cortex_admin;
GRANT ALL ON scoped_code_symbols, scoped_code_relations, scoped_code_index_state TO cortex_migration;
GRANT USAGE, SELECT ON SEQUENCE scoped_code_relations_id_seq TO cortex_app, cortex_migration;

-- Legacy ownership is transferred before privileges are revoked; otherwise
-- a cortex_app-owned table would remain readable through owner rights.
DO $$
BEGIN
    IF to_regclass('public.code_symbols') IS NOT NULL THEN
        ALTER TABLE public.code_symbols OWNER TO cortex_migration;
        ALTER TABLE public.code_relations OWNER TO cortex_migration;
        REVOKE ALL ON code_symbols, code_relations FROM cortex_app;
        REVOKE ALL ON code_symbols, code_relations FROM PUBLIC;
    END IF;
END $$;

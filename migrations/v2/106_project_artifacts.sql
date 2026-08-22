-- Cortex v2 server wave: PostgreSQL migration 106 (forward-only, additive).
-- Project Context artifact ledger (REQ-RET-*, REQ-LIMIT-*): tenant/workspace/
-- project-scoped artifacts with immutable revisions/events, exactly one
-- activation pointer per artifact guarded by a monotonic activation_revision
-- CAS token (the pointer is authoritative; the artifact mirror is synced to
-- it and cannot drift), durable idempotency receipts scoped to the artifact
-- coordinate that store the exact immutable result revision of the SAME
-- coordinate's artifact for exact replay, and transactional storage-usage
-- quota counters keyed by tenant/workspace/project coordinates. The bound
-- workspace/project scope is derived from the verified principal's durable
-- grants (never from arbitrary same-tenant IDs), every principal bind or
-- rebind clears stale scope first, and the application role can no longer
-- rewrite principal grants. A true workspace default is representable: its
-- project is absent, its uniqueness is per workspace, and every scope
-- validation accepts the absent-project form only for defaults. Retention
-- is indefinite in v1: soft delete is a state transition (deleted_at/
-- deleted_by/delete_reason); revisions, events, activations, idempotency
-- evidence, and usage counters are never deleted, anonymized, cascaded,
-- purged, or aged out, and there is no destructive down path. Semantic
-- content/metadata limits (1MiB/64KiB) are enforced by the domain limits
-- package, not by column constraints. Applied transactionally by
-- internal/migration.PostgresServerMigration and ledgered in
-- cortex_server_migrations with the embedded SHA-256 checksum. Like 104,
-- no IF NOT EXISTS is used on purpose so a stale, unledgered artifact fails
-- closed instead of being silently adopted.

-- 1. Scope validation: every scope-bearing row (artifacts, idempotency
--    receipts, usage counters) must resolve against the durable hierarchy of
--    its own tenant. A row with a project must reference a project of the
--    same tenant whose own workspace equals the row's workspace; a
--    workspace-default row (absent project) must reference a workspace of
--    the same tenant. The tenant/workspace/project triple can therefore
--    never disagree with the hierarchy, and defaults cannot smuggle in a
--    foreign project. Tenant identity itself always comes from the verified
--    principal binding, never from client input.
CREATE FUNCTION cortex_validate_project_scope() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_workspace bigint;
BEGIN
    IF NEW.project_id IS NULL THEN
        IF NOT EXISTS (
            SELECT 1 FROM workspaces w
             WHERE w.tenant_id = NEW.tenant_id AND w.id = NEW.workspace_id
        ) THEN
            RAISE EXCEPTION 'workspace % does not exist in tenant', NEW.workspace_id
                USING ERRCODE = '23503';
        END IF;
    ELSE
        SELECT p.workspace_id INTO v_workspace
          FROM projects p
         WHERE p.tenant_id = NEW.tenant_id AND p.id = NEW.project_id;
        IF v_workspace IS NULL THEN
            RAISE EXCEPTION 'project % does not exist in tenant', NEW.project_id
                USING ERRCODE = '23503';
        END IF;
        IF v_workspace <> NEW.workspace_id THEN
            RAISE EXCEPTION 'workspace % conflicts with project workspace %', NEW.workspace_id, v_workspace
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END
$$;
REVOKE ALL ON FUNCTION cortex_validate_project_scope() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cortex_validate_project_scope() TO cortex_app, cortex_migration;

-- 2. Immutable history guard: revisions and events are append-only; any
--    UPDATE or DELETE fails closed instead of rewriting evidence.
CREATE FUNCTION cortex_forbid_project_history_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'project artifact history table % is immutable', TG_TABLE_NAME
        USING ERRCODE = 'P0001';
END
$$;
REVOKE ALL ON FUNCTION cortex_forbid_project_history_mutation() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cortex_forbid_project_history_mutation() TO cortex_app, cortex_migration;

-- 3. Ledger retention and monotonicity guards. Indefinite retention means
--    activation pointers, idempotency receipts, and usage counters are
--    append/fold-only evidence: they are never removed, a receipt commits
--    exactly once and stays immutable afterwards, usage counters never
--    decrease (soft delete retains history, so stored bytes never leave the
--    ledger and quota cannot be reset), and activation CAS tokens only move
--    forward.
CREATE FUNCTION cortex_forbid_project_ledger_deletion() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'project ledger table % retains its rows indefinitely', TG_TABLE_NAME
        USING ERRCODE = 'P0001';
END
$$;
REVOKE ALL ON FUNCTION cortex_forbid_project_ledger_deletion() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cortex_forbid_project_ledger_deletion() TO cortex_app, cortex_migration;

CREATE FUNCTION cortex_project_idempotency_commit_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_workspace bigint;
    v_project bigint;
BEGIN
    IF OLD.state = 'pending' AND NEW.state = 'committed'
       AND NEW.tenant_id = OLD.tenant_id
       AND NEW.workspace_id IS NOT DISTINCT FROM OLD.workspace_id
       AND NEW.project_id IS NOT DISTINCT FROM OLD.project_id
       AND NEW.scope = OLD.scope
       AND NEW.idem_key = OLD.idem_key
       AND NEW.payload_hash = OLD.payload_hash
       AND NEW.created_at = OLD.created_at
       AND EXISTS (
           SELECT 1 FROM project_artifact_revisions r
            WHERE r.tenant_id = NEW.tenant_id
              AND r.artifact_id = NEW.artifact_id
              AND r.revision = NEW.result_revision
       )
       AND EXISTS (
           SELECT 1 FROM project_artifacts a
            WHERE a.tenant_id = NEW.tenant_id
              AND a.id = NEW.artifact_id
              AND a.workspace_id = NEW.workspace_id
              AND a.project_id IS NOT DISTINCT FROM NEW.project_id
       ) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'idempotency receipt commits exactly once, stays immutable, and references the exact revision of the same coordinate'
        USING ERRCODE = 'P0001';
END
$$;
REVOKE ALL ON FUNCTION cortex_project_idempotency_commit_guard() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cortex_project_idempotency_commit_guard() TO cortex_app, cortex_migration;

-- Committed receipts cannot be fabricated by direct INSERT either: the
-- result must reference an existing revision of an artifact of the SAME
-- coordinate.
CREATE FUNCTION cortex_project_idempotency_result_guard_on_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.state = 'committed'
       AND NOT (
           EXISTS (
               SELECT 1 FROM project_artifact_revisions r
                WHERE r.tenant_id = NEW.tenant_id
                  AND r.artifact_id = NEW.artifact_id
                  AND r.revision = NEW.result_revision
           )
           AND EXISTS (
               SELECT 1 FROM project_artifacts a
                WHERE a.tenant_id = NEW.tenant_id
                  AND a.id = NEW.artifact_id
                  AND a.workspace_id = NEW.workspace_id
                  AND a.project_id IS NOT DISTINCT FROM NEW.project_id
           )
       ) THEN
        RAISE EXCEPTION 'committed receipts must reference the exact revision of the same coordinate'
            USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END
$$;
REVOKE ALL ON FUNCTION cortex_project_idempotency_result_guard_on_insert() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cortex_project_idempotency_result_guard_on_insert() TO cortex_app, cortex_migration;

-- Usage-counter coordinates are frozen at insert (REQ-QUOTA-001): a usage
-- row may never move to another tenant, workspace, or project (including
-- the project-to-workspace-default transition), so accumulated quota can
-- never be relocated away or shadowed by a fresh coordinate. The guard
-- fires before the monotonic counter guard (alphabetical trigger order)
-- and leaves legal nondecreasing counter folds and updated_at untouched.
CREATE FUNCTION cortex_project_usage_coordinate_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id <> OLD.tenant_id
       OR NEW.workspace_id <> OLD.workspace_id
       OR NEW.project_id IS DISTINCT FROM OLD.project_id THEN
        RAISE EXCEPTION 'storage usage coordinates are immutable after insert'
            USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END
$$;
REVOKE ALL ON FUNCTION cortex_project_usage_coordinate_immutable() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cortex_project_usage_coordinate_immutable() TO cortex_app, cortex_migration;

CREATE FUNCTION cortex_project_usage_monotonic() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.content_bytes < OLD.content_bytes
       OR NEW.metadata_bytes < OLD.metadata_bytes
       OR NEW.event_bytes < OLD.event_bytes THEN
        RAISE EXCEPTION 'storage usage counters never decrease'
            USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END
$$;
REVOKE ALL ON FUNCTION cortex_project_usage_monotonic() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cortex_project_usage_monotonic() TO cortex_app, cortex_migration;

CREATE FUNCTION cortex_project_activation_cas_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.activation_revision < OLD.activation_revision
       OR (NEW.revision IS DISTINCT FROM OLD.revision
           AND NEW.activation_revision <= OLD.activation_revision) THEN
        RAISE EXCEPTION 'activation_revision must increase to move the activation pointer'
            USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END
$$;
REVOKE ALL ON FUNCTION cortex_project_activation_cas_guard() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cortex_project_activation_cas_guard() TO cortex_app, cortex_migration;

CREATE FUNCTION cortex_project_activation_token_monotonic() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.activation_revision < OLD.activation_revision THEN
        RAISE EXCEPTION 'project artifact activation_revision is monotonic'
            USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END
$$;
REVOKE ALL ON FUNCTION cortex_project_activation_token_monotonic() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cortex_project_activation_token_monotonic() TO cortex_app, cortex_migration;

-- The activation pointer is the AUTHORITATIVE token. Pointer creation and
-- moves sync the artifact mirror to the pointer's token in the same
-- statement, and the artifact mirror may only ever be set to the pointer's
-- current token, so the two columns can never drift.
CREATE FUNCTION cortex_project_sync_activation_mirror() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE project_artifacts
       SET activation_revision = NEW.activation_revision
     WHERE tenant_id = NEW.tenant_id
       AND id = NEW.artifact_id
       AND activation_revision <> NEW.activation_revision;
    RETURN NEW;
END
$$;
REVOKE ALL ON FUNCTION cortex_project_sync_activation_mirror() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cortex_project_sync_activation_mirror() TO cortex_app, cortex_migration;

CREATE FUNCTION cortex_project_activation_mirror_authoritative() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.activation_revision IS DISTINCT FROM OLD.activation_revision
       AND NOT EXISTS (
           SELECT 1 FROM project_artifact_activations x
            WHERE x.tenant_id = NEW.tenant_id
              AND x.artifact_id = NEW.id
              AND x.activation_revision = NEW.activation_revision
       ) THEN
        RAISE EXCEPTION 'activation_revision must equal the activation pointer token'
            USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END
$$;
REVOKE ALL ON FUNCTION cortex_project_activation_mirror_authoritative() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cortex_project_activation_mirror_authoritative() TO cortex_app, cortex_migration;

-- 4. Artifacts: soft-delete state machine with per-scope active uniqueness.
--    project_id is absent exactly for workspace defaults; a project artifact
--    always carries its project. activation_revision is the artifact-level
--    CAS token (0 before the first activation, then strictly forward).
CREATE TABLE project_artifacts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL,
    workspace_id bigint NOT NULL,
    project_id bigint,
    kind text NOT NULL CHECK (kind IN ('skill', 'rule')),
    key text NOT NULL,
    source_scope text NOT NULL CHECK (source_scope IN ('project', 'workspace_default')),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deleted')),
    current_revision integer NOT NULL CHECK (current_revision >= 1),
    activation_revision integer NOT NULL DEFAULT 0 CHECK (activation_revision >= 0),
    content_bytes bigint NOT NULL CHECK (content_bytes >= 0),
    metadata_bytes bigint NOT NULL CHECK (metadata_bytes >= 0),
    digest text NOT NULL CHECK (length(digest) = 64),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    deleted_by uuid,
    delete_reason text,
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id) ON DELETE RESTRICT,
    CHECK (
        (source_scope = 'project' AND project_id IS NOT NULL)
        OR (source_scope = 'workspace_default' AND project_id IS NULL)
    ),
    CHECK (
        (status = 'active'
            AND deleted_at IS NULL
            AND deleted_by IS NULL
            AND delete_reason IS NULL)
        OR
        (status = 'deleted'
            AND deleted_at IS NOT NULL
            AND deleted_by IS NOT NULL
            AND delete_reason IS NOT NULL)
    )
);
CREATE TRIGGER project_artifacts_validate_scope
    BEFORE INSERT OR UPDATE OF tenant_id, workspace_id, project_id ON project_artifacts
    FOR EACH ROW EXECUTE FUNCTION cortex_validate_project_scope();
CREATE TRIGGER project_artifacts_activation_revision_monotonic
    BEFORE UPDATE OF activation_revision ON project_artifacts
    FOR EACH ROW EXECUTE FUNCTION cortex_project_activation_token_monotonic();
CREATE TRIGGER project_artifacts_activation_mirror_authoritative
    BEFORE UPDATE OF activation_revision ON project_artifacts
    FOR EACH ROW EXECUTE FUNCTION cortex_project_activation_mirror_authoritative();
-- Active uniqueness per coordinate: one active project artifact per
-- (tenant, workspace, project, kind, key) and one active workspace default
-- per (tenant, workspace, kind, key). Soft-deleted history may coexist.
CREATE UNIQUE INDEX project_artifacts_project_active_key_uq
    ON project_artifacts(tenant_id, workspace_id, project_id, kind, key)
    WHERE source_scope = 'project' AND status = 'active';
CREATE UNIQUE INDEX project_artifacts_workspace_default_active_key_uq
    ON project_artifacts(tenant_id, workspace_id, kind, key)
    WHERE source_scope = 'workspace_default' AND status = 'active';
CREATE INDEX project_artifacts_project_active_list_idx
    ON project_artifacts(tenant_id, workspace_id, project_id, updated_at DESC, id)
    WHERE source_scope = 'project' AND status = 'active';
CREATE INDEX project_artifacts_project_list_idx
    ON project_artifacts(tenant_id, workspace_id, project_id, updated_at DESC, id)
    WHERE source_scope = 'project';
CREATE INDEX project_artifacts_workspace_default_active_list_idx
    ON project_artifacts(tenant_id, workspace_id, kind, updated_at DESC, id)
    WHERE source_scope = 'workspace_default' AND status = 'active';
CREATE INDEX project_artifacts_workspace_default_list_idx
    ON project_artifacts(tenant_id, workspace_id, kind, updated_at DESC, id)
    WHERE source_scope = 'workspace_default';

-- 5. Immutable revision history.
CREATE TABLE project_artifact_revisions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL,
    artifact_id bigint NOT NULL,
    revision integer NOT NULL CHECK (revision >= 1),
    content text NOT NULL,
    content_bytes bigint NOT NULL CHECK (content_bytes >= 0),
    metadata text NOT NULL,
    metadata_bytes bigint NOT NULL CHECK (metadata_bytes >= 0),
    digest text NOT NULL CHECK (length(digest) = 64),
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid NOT NULL,
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, artifact_id, revision),
    FOREIGN KEY (tenant_id, artifact_id) REFERENCES project_artifacts(tenant_id, id) ON DELETE RESTRICT
);
CREATE INDEX project_artifact_revisions_idx
    ON project_artifact_revisions(tenant_id, artifact_id, revision DESC);
CREATE TRIGGER project_artifact_revisions_immutable
    BEFORE UPDATE OR DELETE ON project_artifact_revisions
    FOR EACH ROW EXECUTE FUNCTION cortex_forbid_project_history_mutation();

-- 6. Immutable event history, including the activation CAS token produced
--    by activated/deactivated transitions.
CREATE TABLE project_artifact_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id uuid NOT NULL,
    artifact_id bigint NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('created', 'revised', 'activated', 'deactivated', 'deleted', 'restored')),
    revision integer CHECK (revision IS NULL OR revision >= 1),
    activation_revision integer CHECK (activation_revision IS NULL OR activation_revision >= 1),
    actor uuid NOT NULL,
    payload text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, artifact_id) REFERENCES project_artifacts(tenant_id, id) ON DELETE RESTRICT
);
CREATE INDEX project_artifact_events_idx
    ON project_artifact_events(tenant_id, artifact_id, created_at DESC, id);
CREATE TRIGGER project_artifact_events_immutable
    BEFORE UPDATE OR DELETE ON project_artifact_events
    FOR EACH ROW EXECUTE FUNCTION cortex_forbid_project_history_mutation();

-- 7. Exactly one activation pointer per artifact; the pointer must reference
--    an existing revision of that same artifact and carries the current
--    activation_revision CAS token (>= 1 after the first activation).
--    Moving the pointer requires a strictly greater token, tokens never
--    regress, and the pointer is retained evidence: it is never removed.
CREATE TABLE project_artifact_activations (
    tenant_id uuid NOT NULL,
    artifact_id bigint NOT NULL,
    revision integer NOT NULL CHECK (revision >= 1),
    activation_revision integer NOT NULL CHECK (activation_revision >= 1),
    activated_at timestamptz NOT NULL DEFAULT now(),
    activated_by uuid NOT NULL,
    PRIMARY KEY (tenant_id, artifact_id),
    FOREIGN KEY (tenant_id, artifact_id) REFERENCES project_artifacts(tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, artifact_id, revision) REFERENCES project_artifact_revisions(tenant_id, artifact_id, revision) ON DELETE RESTRICT
);
CREATE TRIGGER project_artifact_activations_cas_guard
    BEFORE UPDATE ON project_artifact_activations
    FOR EACH ROW EXECUTE FUNCTION cortex_project_activation_cas_guard();
CREATE TRIGGER project_artifact_activations_sync_mirror
    AFTER INSERT OR UPDATE ON project_artifact_activations
    FOR EACH ROW EXECUTE FUNCTION cortex_project_sync_activation_mirror();
CREATE TRIGGER project_artifact_activations_no_delete
    BEFORE DELETE ON project_artifact_activations
    FOR EACH ROW EXECUTE FUNCTION cortex_forbid_project_ledger_deletion();

-- 8. Durable idempotency receipts scoped to the artifact namespace: one
--    namespace per (tenant, workspace, project) for project mutations and
--    per (tenant, workspace) for workspace-default mutations (absent
--    project). The committed state stores the resulting artifact, initial
--    status, and the EXACT result revision exactly once, so a replay returns
--    the original revision. The only legal transition is pending ->
--    committed with the namespace, payload hash, and creation time frozen;
--    everything else (including any later mutation or removal) fails closed.
CREATE TABLE project_artifact_idempotency (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id uuid NOT NULL,
    workspace_id bigint NOT NULL,
    project_id bigint,
    scope text NOT NULL,
    idem_key text NOT NULL,
    payload_hash bytea NOT NULL CHECK (length(payload_hash) = 32),
    state text NOT NULL CHECK (state IN ('pending', 'committed')),
    artifact_id bigint,
    initial_status text CHECK (initial_status IS NULL OR initial_status IN ('created', 'replayed', 'updated')),
    result_revision integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    committed_at timestamptz,
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, artifact_id) REFERENCES project_artifacts(tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, artifact_id, result_revision) REFERENCES project_artifact_revisions(tenant_id, artifact_id, revision) ON DELETE RESTRICT,
    CHECK (
        (state = 'pending'
            AND artifact_id IS NULL
            AND initial_status IS NULL
            AND result_revision IS NULL
            AND committed_at IS NULL)
        OR
        (state = 'committed'
            AND artifact_id IS NOT NULL
            AND initial_status IS NOT NULL
            AND result_revision IS NOT NULL
            AND result_revision >= 1
            AND committed_at IS NOT NULL)
    )
);
CREATE UNIQUE INDEX project_artifact_idempotency_project_uq
    ON project_artifact_idempotency(tenant_id, workspace_id, project_id, scope, idem_key)
    WHERE project_id IS NOT NULL;
CREATE UNIQUE INDEX project_artifact_idempotency_workspace_default_uq
    ON project_artifact_idempotency(tenant_id, workspace_id, scope, idem_key)
    WHERE project_id IS NULL;
CREATE INDEX project_artifact_idempotency_artifact_idx
    ON project_artifact_idempotency(tenant_id, artifact_id);
CREATE TRIGGER project_artifact_idempotency_validate_scope
    BEFORE INSERT OR UPDATE OF tenant_id, workspace_id, project_id ON project_artifact_idempotency
    FOR EACH ROW EXECUTE FUNCTION cortex_validate_project_scope();
CREATE TRIGGER project_artifact_idempotency_commit_guard
    BEFORE UPDATE ON project_artifact_idempotency
    FOR EACH ROW EXECUTE FUNCTION cortex_project_idempotency_commit_guard();
CREATE TRIGGER project_artifact_idempotency_result_guard_on_insert
    BEFORE INSERT ON project_artifact_idempotency
    FOR EACH ROW EXECUTE FUNCTION cortex_project_idempotency_result_guard_on_insert();
CREATE TRIGGER project_artifact_idempotency_no_delete
    BEFORE DELETE ON project_artifact_idempotency
    FOR EACH ROW EXECUTE FUNCTION cortex_forbid_project_ledger_deletion();

-- 9. Transactional storage-usage quota counters keyed by tenant/workspace
--    coordinates: one row per (tenant, workspace, project) plus one row per
--    (tenant, workspace) for workspace defaults (absent project). Writers
--    fold the counters in the SAME transaction as the revision/event insert
--    and check the operator-configured quota before write; over-quota
--    writes fail closed before insert and never purge. Retention is
--    indefinite, so the counters are monotonic: they never decrease and the
--    rows are never removed.
CREATE TABLE project_storage_usage (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id uuid NOT NULL,
    workspace_id bigint NOT NULL,
    project_id bigint,
    content_bytes bigint NOT NULL DEFAULT 0 CHECK (content_bytes >= 0),
    metadata_bytes bigint NOT NULL DEFAULT 0 CHECK (metadata_bytes >= 0),
    event_bytes bigint NOT NULL DEFAULT 0 CHECK (event_bytes >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX project_storage_usage_project_uq
    ON project_storage_usage(tenant_id, workspace_id, project_id)
    WHERE project_id IS NOT NULL;
CREATE UNIQUE INDEX project_storage_usage_workspace_default_uq
    ON project_storage_usage(tenant_id, workspace_id)
    WHERE project_id IS NULL;
CREATE TRIGGER project_storage_usage_validate_scope
    BEFORE INSERT OR UPDATE OF tenant_id, workspace_id, project_id ON project_storage_usage
    FOR EACH ROW EXECUTE FUNCTION cortex_validate_project_scope();
CREATE TRIGGER project_storage_usage_coordinate_immutable
    BEFORE UPDATE ON project_storage_usage
    FOR EACH ROW EXECUTE FUNCTION cortex_project_usage_coordinate_immutable();
CREATE TRIGGER project_storage_usage_monotonic
    BEFORE UPDATE ON project_storage_usage
    FOR EACH ROW EXECUTE FUNCTION cortex_project_usage_monotonic();
CREATE TRIGGER project_storage_usage_no_delete
    BEFORE DELETE ON project_storage_usage
    FOR EACH ROW EXECUTE FUNCTION cortex_forbid_project_ledger_deletion();

-- 10. Trusted principal-derived project scope. Workspace/project identity is
--     never taken from row input alone, and same-tenant membership alone is
--     NOT authorization: the application binds a scope into the SAME
--     transaction context that cortex_bind_principal (migration 100)
--     installed for the verified principal, and the binding succeeds only
--     when the bound actor holds an explicit workspace grant and, for a
--     project binding, an explicit project grant with the platform's
--     wildcard semantics (project grant_value '*' or scope grant
--     'project:*' = every project of that workspace; exact public IDs and
--     'project:<public_id>' scope grants = that one project). Every bind or
--     rebind of a principal clears any stale scope first, so scope can
--     never outlive or cross the principal it was derived from. The RLS
--     policies below refuse every row outside the bound scope even for the
--     table owner; without a binding, no artifact row is visible or
--     writable.
ALTER TABLE cortex_tenant_context ADD COLUMN workspace_id bigint;
ALTER TABLE cortex_tenant_context ADD COLUMN project_id bigint;
ALTER TABLE cortex_tenant_context ADD COLUMN scope_bound_at timestamptz;
ALTER TABLE cortex_tenant_context ADD COLUMN actor_public_id uuid;

-- Identity mediation (REQ-IDP-003..009): every privileged identity
-- operation becomes a migration-owned definer routine with a fixed search
-- path, PUBLIC/admin execution revoked, strict input validation, and
-- transaction-atomic effects. The application role never touches
-- actor_subjects or principal_grants rows directly again; caller identity
-- is derived only from the principal context bound earlier in the SAME
-- transaction, and privileged mutations append a non-secret audit row
-- whose failure rolls the whole mutation back.
CREATE FUNCTION cortex_actor_admin_caller()
RETURNS TABLE(tenant_id uuid, caller_public_id uuid)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_tenant uuid;
    v_caller uuid;
BEGIN
    SELECT c.tenant_id, c.actor_public_id INTO v_tenant, v_caller
      FROM public.cortex_tenant_context c
     WHERE c.backend_pid = pg_backend_pid()
       AND c.transaction_id = txid_current();
    IF v_tenant IS NULL OR v_caller IS NULL THEN
        RAISE EXCEPTION 'bound principal is required' USING ERRCODE = '28000';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.actor_subjects a
         WHERE a.tenant_id = v_tenant
           AND a.public_id = v_caller
           AND a.active
           AND a.revoked_at IS NULL
    ) THEN
        RAISE EXCEPTION 'bound principal is revoked or inactive' USING ERRCODE = '28000';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.principal_grants g
         WHERE g.tenant_id = v_tenant
           AND g.actor_public_id = v_caller
           AND g.grant_type = 'role'
           AND g.grant_value IN ('owner', 'admin')
    ) THEN
        RAISE EXCEPTION 'caller is not an owner or admin' USING ERRCODE = '42501';
    END IF;
    RETURN QUERY SELECT v_tenant, v_caller;
END
$$;
REVOKE ALL ON FUNCTION cortex_actor_admin_caller() FROM PUBLIC;
REVOKE ALL ON FUNCTION cortex_actor_admin_caller() FROM cortex_app;
REVOKE ALL ON FUNCTION cortex_actor_admin_caller() FROM cortex_admin;

-- Owner/admin-authorized actor provisioning. The target must already exist
-- as a same-tenant app_users or service_accounts row of the matching type;
-- grants are validated against the 101 allowlist, canonicalized by type
-- then value, and hashed inside SQL. Actor, grants, and the non-secret
-- audit row commit atomically or not at all.
CREATE FUNCTION cortex_provision_actor(p_actor_public_id uuid, p_subject text, p_actor_type text, p_grants jsonb, p_reason text)
RETURNS TABLE(grant_version bigint, grant_digest text)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_tenant uuid;
    v_caller uuid;
    v_subject text;
    v_digest text;
    v_canonical text;
    v_count integer;
    v_metadata jsonb;
BEGIN
    SELECT d.tenant_id, d.caller_public_id INTO v_tenant, v_caller
      FROM public.cortex_actor_admin_caller() d;
    IF p_actor_public_id IS NULL
       OR p_subject IS NULL OR NULLIF(btrim(p_subject), '') IS NULL
       OR p_actor_type IS NULL OR p_actor_type NOT IN ('user', 'service_account') THEN
        RAISE EXCEPTION 'provision arguments are invalid' USING ERRCODE = '22023';
    END IF;
    v_subject := btrim(p_subject);
    IF p_grants IS NULL OR jsonb_typeof(p_grants) <> 'array' THEN
        RAISE EXCEPTION 'provision grants must be a JSON array' USING ERRCODE = '22023';
    END IF;
    SELECT count(*) INTO v_count FROM jsonb_array_elements(p_grants);
    IF v_count < 1 THEN
        RAISE EXCEPTION 'provision requires at least one grant' USING ERRCODE = '22023';
    END IF;
    IF EXISTS (
        SELECT 1 FROM jsonb_array_elements(p_grants) AS obj
         WHERE jsonb_typeof(obj) <> 'object'
    ) THEN
        RAISE EXCEPTION 'provision grants must be type/value objects' USING ERRCODE = '22023';
    END IF;
    IF EXISTS (
        SELECT 1 FROM jsonb_array_elements(p_grants) AS obj
         WHERE (SELECT count(*) FROM jsonb_object_keys(obj)) <> 2
            OR obj->>'type' IS NULL
            OR obj->>'value' IS NULL
            OR (obj->>'type') NOT IN ('role', 'workspace', 'project', 'classification', 'scope')
            OR NULLIF(btrim(obj->>'value'), '') IS NULL
    ) THEN
        RAISE EXCEPTION 'provision grants must be non-empty allowlisted type/value objects' USING ERRCODE = '22023';
    END IF;
    IF EXISTS (
        SELECT 1
          FROM (
            SELECT obj->>'type' AS grant_type, obj->>'value' AS grant_value
              FROM jsonb_array_elements(p_grants) AS obj
          ) q
         GROUP BY q.grant_type, q.grant_value
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'provision grants must be unique' USING ERRCODE = '22023';
    END IF;
    IF p_actor_type = 'user' THEN
        IF NOT EXISTS (
            SELECT 1 FROM public.app_users u
             WHERE u.tenant_id = v_tenant AND u.public_id = p_actor_public_id
        ) THEN
            RAISE EXCEPTION 'provision target user does not exist in tenant' USING ERRCODE = '23503';
        END IF;
    ELSE
        IF NOT EXISTS (
            SELECT 1 FROM public.service_accounts s
             WHERE s.tenant_id = v_tenant AND s.public_id = p_actor_public_id
        ) THEN
            RAISE EXCEPTION 'provision target service account does not exist in tenant' USING ERRCODE = '23503';
        END IF;
    END IF;
    IF EXISTS (
        SELECT 1 FROM public.actor_subjects a
         WHERE a.tenant_id = v_tenant
           AND (a.public_id = p_actor_public_id OR a.subject = v_subject)
    ) THEN
        RAISE EXCEPTION 'actor is already provisioned in tenant' USING ERRCODE = '23505';
    END IF;
    SELECT string_agg(q.grant_type || ':' || q.grant_value, E'\n' ORDER BY q.grant_type, q.grant_value)
      INTO v_canonical
      FROM (
        SELECT DISTINCT obj->>'type' AS grant_type, obj->>'value' AS grant_value
          FROM jsonb_array_elements(p_grants) AS obj
      ) q;
    v_digest := encode(public.digest(convert_to(v_canonical, 'UTF8'), 'sha256'), 'hex');
    INSERT INTO public.actor_subjects
        (tenant_id, subject, actor_type, public_id, active, revoked_at, grant_version, grant_digest)
    VALUES
        (v_tenant, v_subject, p_actor_type, p_actor_public_id, true, NULL, 1, v_digest);
    INSERT INTO public.principal_grants
        (tenant_id, actor_public_id, grant_type, grant_value, created_by, updated_by)
    SELECT v_tenant, p_actor_public_id, q.grant_type, q.grant_value, v_caller, v_caller
      FROM (
        SELECT DISTINCT obj->>'type' AS grant_type, obj->>'value' AS grant_value
          FROM jsonb_array_elements(p_grants) AS obj
      ) q
     ORDER BY q.grant_type, q.grant_value;
    v_metadata := jsonb_build_object('reason', COALESCE(NULLIF(p_reason, ''), ''), 'allowed', true, 'grant_count', v_count);
    INSERT INTO public.audit_events
        (tenant_id, actor_public_id, action, resource_type, resource_public_id, metadata, event_hash, reason, allowed)
    VALUES
        (v_tenant, v_caller, 'identity.actor.provision', 'actor', p_actor_public_id,
         v_metadata, public.digest(v_metadata::text, 'sha256'), COALESCE(NULLIF(p_reason, ''), ''), true);
    RETURN QUERY SELECT 1::bigint, v_digest;
END
$$;
REVOKE ALL ON FUNCTION cortex_provision_actor(uuid,text,text,jsonb,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION cortex_provision_actor(uuid,text,text,jsonb,text) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION cortex_provision_actor(uuid,text,text,jsonb,text) TO cortex_app;

-- Owner/admin-authorized activation state change. A true no-op keeps the
-- grant_version stable; a real transition bumps it exactly once,
-- synchronizes the matching app_users/service_accounts row, revokes every
-- live token when disabling (reactivation never revives them), and writes
-- the non-secret audit row. Any failure rolls the mutation back.
CREATE FUNCTION cortex_set_actor_active(p_target_actor_public_id uuid, p_active boolean, p_reason text)
RETURNS bigint
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_tenant uuid;
    v_caller uuid;
    v_current boolean;
    v_version bigint;
    v_type text;
    v_metadata jsonb;
BEGIN
    SELECT d.tenant_id, d.caller_public_id INTO v_tenant, v_caller
      FROM public.cortex_actor_admin_caller() d;
    IF p_target_actor_public_id IS NULL OR p_active IS NULL THEN
        RAISE EXCEPTION 'activation arguments are invalid' USING ERRCODE = '22023';
    END IF;
    SELECT a.active, a.grant_version, a.actor_type
      INTO v_current, v_version, v_type
      FROM public.actor_subjects a
     WHERE a.tenant_id = v_tenant
       AND a.public_id = p_target_actor_public_id
      FOR UPDATE;
    IF v_version IS NULL THEN
        RAISE EXCEPTION 'actor does not exist in tenant' USING ERRCODE = '23503';
    END IF;
    IF v_current IS NOT DISTINCT FROM p_active THEN
        RETURN v_version;
    END IF;
    v_version := v_version + 1;
    UPDATE public.actor_subjects
       SET active = p_active,
           revoked_at = CASE WHEN p_active THEN NULL ELSE clock_timestamp() END,
           grant_version = v_version
     WHERE tenant_id = v_tenant
       AND public_id = p_target_actor_public_id;
    IF v_type = 'user' THEN
        UPDATE public.app_users
           SET active = p_active,
               disabled_at = CASE WHEN p_active THEN NULL ELSE clock_timestamp() END,
               updated_at = now()
         WHERE tenant_id = v_tenant AND public_id = p_target_actor_public_id;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'activation target user does not exist in tenant' USING ERRCODE = '23503';
        END IF;
    ELSIF v_type = 'service_account' THEN
        UPDATE public.service_accounts
           SET active = p_active,
               disabled_at = CASE WHEN p_active THEN NULL ELSE clock_timestamp() END,
               updated_at = now()
         WHERE tenant_id = v_tenant AND public_id = p_target_actor_public_id;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'activation target service account does not exist in tenant' USING ERRCODE = '23503';
        END IF;
    ELSE
        RAISE EXCEPTION 'actor type is unknown' USING ERRCODE = '22023';
    END IF;
    IF NOT p_active THEN
        UPDATE public.api_tokens t
           SET revoked_at = clock_timestamp(),
               updated_at = now()
          FROM public.app_users u
         WHERE t.tenant_id = v_tenant
           AND u.tenant_id = v_tenant
           AND u.public_id = p_target_actor_public_id
           AND t.subject_user_id = u.id
           AND t.revoked_at IS NULL;
        UPDATE public.api_tokens t
           SET revoked_at = clock_timestamp(),
               updated_at = now()
          FROM public.service_accounts s
         WHERE t.tenant_id = v_tenant
           AND s.tenant_id = v_tenant
           AND s.public_id = p_target_actor_public_id
           AND t.subject_service_account_id = s.id
           AND t.revoked_at IS NULL;
    END IF;
    v_metadata := jsonb_build_object('reason', COALESCE(NULLIF(p_reason, ''), ''), 'allowed', true, 'active', p_active);
    INSERT INTO public.audit_events
        (tenant_id, actor_public_id, action, resource_type, resource_public_id, metadata, event_hash, reason, allowed)
    VALUES
        (v_tenant, v_caller, 'identity.actor.active_changed', 'actor', p_target_actor_public_id,
         v_metadata, public.digest(v_metadata::text, 'sha256'), COALESCE(NULLIF(p_reason, ''), ''), true);
    RETURN v_version;
END
$$;
REVOKE ALL ON FUNCTION cortex_set_actor_active(uuid,boolean,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION cortex_set_actor_active(uuid,boolean,text) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION cortex_set_actor_active(uuid,boolean,text) TO cortex_app;

-- Token verification without exposing api_tokens.token_digest or any
-- sensitive actor/grant state: the routine locks the matched token row,
-- requires a live subject and actor, enforces revocation, expiry, and the
-- required token scope, aggregates the durable grants deterministically,
-- folds last_used_at in the same transaction, and mints the one-time
-- binding provenance consumed by cortex_bind_principal. Unknown or inert
-- principals return no row; revoked, expired, and scope failures raise
-- stable authentication errors the repository maps compatibly.
CREATE FUNCTION cortex_verify_token_principal(p_token_prefix text, p_token_digest bytea, p_required_scope text)
RETURNS TABLE(
    token_public_id uuid,
    token_name text,
    token_prefix text,
    subject_public_id uuid,
    principal_type text,
    tenant_id uuid,
    token_scopes text[],
    token_workspace_ids uuid[],
    expires_at timestamptz,
    revoked_at timestamptz,
    last_used_at timestamptz,
    roles text[],
    workspaces text[],
    projects text[],
    classification text[],
    grant_scopes text[],
    grant_version bigint,
    binding_provenance text
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_token_id uuid;
    v_tenant uuid;
    v_name text;
    v_prefix text;
    v_expires timestamptz;
    v_revoked timestamptz;
    v_last_used timestamptz;
    v_scopes text[];
    v_workspace_ids uuid[];
    v_subject uuid;
    v_subject_active boolean;
    v_is_service boolean;
    v_version bigint;
    v_provenance text;
BEGIN
    IF NULLIF(p_token_prefix, '') IS NULL OR p_token_digest IS NULL THEN
        RETURN;
    END IF;
    SELECT t.public_id, t.tenant_id, t.name, t.token_prefix, t.expires_at, t.revoked_at,
           t.last_used_at, t.scopes, t.workspace_ids,
           COALESCE(u.public_id, s.public_id), COALESCE(u.active, s.active), s.id IS NOT NULL
      INTO v_token_id, v_tenant, v_name, v_prefix, v_expires, v_revoked, v_last_used,
           v_scopes, v_workspace_ids, v_subject, v_subject_active, v_is_service
      FROM public.api_tokens t
      LEFT JOIN public.app_users u
        ON u.tenant_id = t.tenant_id AND u.id = t.subject_user_id
      LEFT JOIN public.service_accounts s
        ON s.tenant_id = t.tenant_id AND s.id = t.subject_service_account_id
     -- Callers present the bearer's textual 12-character head; the
     -- reserved bootstrap token stores that head plus a deterministic
     -- digest-derived suffix so same-head bearers stay unique under
     -- UNIQUE (tenant_id, token_prefix). Matching on the stored prefix's
     -- head plus EXACT digest equality resolves exactly one row: the
     -- tenant-scoped digest is unique, so a head match can never select a
     -- foreign token, and ordinary tokens whose prefix IS the head keep
     -- matching unchanged.
     WHERE left(t.token_prefix, 12) = p_token_prefix
       AND t.token_digest = p_token_digest
     ORDER BY t.id
     LIMIT 1
       FOR UPDATE OF t;
    IF v_token_id IS NULL OR v_subject IS NULL OR v_subject_active IS NOT TRUE THEN
        RETURN;
    END IF;
    SELECT a.grant_version INTO v_version
      FROM public.actor_subjects a
     WHERE a.tenant_id = v_tenant
       AND a.public_id = v_subject
       AND a.active
       AND a.revoked_at IS NULL;
    IF v_version IS NULL THEN
        RETURN;
    END IF;
    IF v_revoked IS NOT NULL THEN
        RAISE EXCEPTION 'token is revoked' USING ERRCODE = '28000';
    END IF;
    IF v_expires IS NOT NULL AND v_expires <= clock_timestamp() THEN
        RAISE EXCEPTION 'token is expired' USING ERRCODE = '28000';
    END IF;
    IF NULLIF(p_required_scope, '') IS NOT NULL
       AND NOT p_required_scope = ANY(v_scopes) THEN
        RAISE EXCEPTION 'token is missing required scope' USING ERRCODE = '42501';
    END IF;
    UPDATE public.api_tokens
       SET last_used_at = clock_timestamp(),
           updated_at = now()
     WHERE api_tokens.public_id = v_token_id
       AND api_tokens.revoked_at IS NULL;
    v_provenance := 'v1:' || v_token_id::text || ':' || encode(
        public.hmac(
            convert_to(
                v_tenant::text || ':' || v_subject::text || ':' || v_version::text || ':' || v_token_id::text,
                'UTF8'),
            p_token_digest, 'sha256'),
        'hex');
    RETURN QUERY
    SELECT v_token_id, v_name, v_prefix, v_subject,
           CASE WHEN v_is_service THEN 'service_account'::text ELSE 'user'::text END,
           v_tenant, v_scopes, v_workspace_ids, v_expires, v_revoked, v_last_used,
           COALESCE((SELECT array_agg(g.grant_value ORDER BY g.grant_value)
                       FROM public.principal_grants g
                      WHERE g.tenant_id = v_tenant
                        AND g.actor_public_id = v_subject
                        AND g.grant_type = 'role'), '{}'),
           COALESCE((SELECT array_agg(g.grant_value ORDER BY g.grant_value)
                       FROM public.principal_grants g
                      WHERE g.tenant_id = v_tenant
                        AND g.actor_public_id = v_subject
                        AND g.grant_type = 'workspace'), '{}'),
           COALESCE((SELECT array_agg(g.grant_value ORDER BY g.grant_value)
                       FROM public.principal_grants g
                      WHERE g.tenant_id = v_tenant
                        AND g.actor_public_id = v_subject
                        AND g.grant_type = 'project'), '{}'),
           COALESCE((SELECT array_agg(g.grant_value ORDER BY g.grant_value)
                       FROM public.principal_grants g
                      WHERE g.tenant_id = v_tenant
                        AND g.actor_public_id = v_subject
                        AND g.grant_type = 'classification'), '{}'),
           COALESCE((SELECT array_agg(g.grant_value ORDER BY g.grant_value)
                       FROM public.principal_grants g
                      WHERE g.tenant_id = v_tenant
                        AND g.actor_public_id = v_subject
                        AND g.grant_type = 'scope'), '{}'),
           v_version, v_provenance;
END
$$;
REVOKE ALL ON FUNCTION cortex_verify_token_principal(text,bytea,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION cortex_verify_token_principal(text,bytea,text) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION cortex_verify_token_principal(text,bytea,text) TO cortex_app;

-- Owner/admin-authorized grant read-back for user listing; the only
-- principal_grants read path left to the application role.
CREATE FUNCTION cortex_read_actor_grants(p_actor_public_id uuid)
RETURNS TABLE(grant_type text, grant_value text)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_tenant uuid;
    v_caller uuid;
BEGIN
    SELECT d.tenant_id, d.caller_public_id INTO v_tenant, v_caller
      FROM public.cortex_actor_admin_caller() d;
    IF p_actor_public_id IS NULL THEN
        RAISE EXCEPTION 'actor is required' USING ERRCODE = '22023';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.actor_subjects a
         WHERE a.tenant_id = v_tenant AND a.public_id = p_actor_public_id
    ) THEN
        RAISE EXCEPTION 'actor does not exist in tenant' USING ERRCODE = '23503';
    END IF;
    RETURN QUERY
    SELECT g.grant_type, g.grant_value
      FROM public.principal_grants g
     WHERE g.tenant_id = v_tenant
       AND g.actor_public_id = p_actor_public_id
     ORDER BY g.grant_type, g.grant_value;
END
$$;
REVOKE ALL ON FUNCTION cortex_read_actor_grants(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION cortex_read_actor_grants(uuid) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION cortex_read_actor_grants(uuid) TO cortex_app;

-- Owner/admin-authorized grant-version read-back (REQ-IDP-009): the same
-- authorization surface as the grant read-back, returning only the actor's
-- current grant_version so listing can reject stale binders without any
-- direct actor_subjects state read by the application role.
CREATE FUNCTION cortex_actor_grant_version(p_actor_public_id uuid)
RETURNS bigint
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_tenant uuid;
    v_caller uuid;
    v_version bigint;
BEGIN
    SELECT d.tenant_id, d.caller_public_id INTO v_tenant, v_caller
      FROM public.cortex_actor_admin_caller() d;
    IF p_actor_public_id IS NULL THEN
        RAISE EXCEPTION 'actor is required' USING ERRCODE = '22023';
    END IF;
    SELECT a.grant_version INTO v_version
      FROM public.actor_subjects a
     WHERE a.tenant_id = v_tenant
       AND a.public_id = p_actor_public_id;
    IF v_version IS NULL THEN
        RAISE EXCEPTION 'actor does not exist in tenant' USING ERRCODE = '23503';
    END IF;
    RETURN v_version;
END
$$;
REVOKE ALL ON FUNCTION cortex_actor_grant_version(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION cortex_actor_grant_version(uuid) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION cortex_actor_grant_version(uuid) TO cortex_app;

-- Mediated api_tokens lifecycle (REQ-IDP-007): issue, rotate, and revoke
-- execute only through these migration-owned definer routines. Each one
-- authorizes the bound owner/admin caller, derives the stored digest ONLY
-- inside SQL as the tenant-keyed HMAC-SHA256 of the caller-presented
-- one-time secret (a caller-chosen digest byte string can never be
-- stored), never returns token_digest or the secret, and appends a
-- non-secret audit row whose failure rolls the whole mutation back. The
-- application role loses every direct api_tokens write below, so a
-- compromised query path can no longer forge a same-tenant token for a
-- victim subject and ride verification into a bound principal.
CREATE FUNCTION cortex_issue_api_token(p_subject_public_id uuid, p_name text, p_secret text, p_scopes text[], p_workspace_ids uuid[], p_expires_at timestamptz, p_reason text)
RETURNS TABLE(token_public_id uuid, token_prefix text, expires_at timestamptz)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_tenant uuid;
    v_caller uuid;
    v_user bigint;
    v_service bigint;
    v_prefix text;
    v_digest bytea;
    v_new_id uuid;
    v_expires timestamptz;
    v_metadata jsonb;
BEGIN
    SELECT d.tenant_id, d.caller_public_id INTO v_tenant, v_caller
      FROM public.cortex_actor_admin_caller() d;
    IF p_subject_public_id IS NULL
       OR p_secret IS NULL OR length(p_secret) < 12 THEN
        RAISE EXCEPTION 'token issue arguments are invalid' USING ERRCODE = '22023';
    END IF;
    SELECT u.id, s.id INTO v_user, v_service
      FROM (SELECT 1) seed
      LEFT JOIN public.app_users u
        ON u.tenant_id = v_tenant AND u.public_id = p_subject_public_id AND u.active
      LEFT JOIN public.service_accounts s
        ON s.tenant_id = v_tenant AND s.public_id = p_subject_public_id AND s.active;
    IF v_user IS NULL AND v_service IS NULL THEN
        RAISE EXCEPTION 'token subject does not exist in tenant' USING ERRCODE = '23503';
    END IF;
    v_prefix := left(p_secret, 12);
    v_digest := public.hmac(convert_to(p_secret, 'UTF8'), convert_to(v_tenant::text, 'UTF8'), 'sha256');
    INSERT INTO public.api_tokens AS t
        (tenant_id, name, token_prefix, token_digest, subject_user_id, subject_service_account_id, scopes, workspace_ids, expires_at, created_by)
    VALUES
        (v_tenant, COALESCE(p_name, ''), v_prefix, v_digest, v_user, v_service,
         COALESCE(p_scopes, '{}'), COALESCE(p_workspace_ids, '{}'), p_expires_at, v_caller)
    RETURNING t.public_id, t.expires_at INTO v_new_id, v_expires;
    v_metadata := jsonb_build_object('reason', COALESCE(NULLIF(p_reason, ''), ''), 'allowed', true, 'subject', p_subject_public_id::text);
    INSERT INTO public.audit_events
        (tenant_id, actor_public_id, action, resource_type, resource_public_id, metadata, event_hash, reason, allowed)
    VALUES
        (v_tenant, v_caller, 'identity.token.issued', 'token', v_new_id,
         v_metadata, public.digest(v_metadata::text, 'sha256'), COALESCE(NULLIF(p_reason, ''), ''), true);
    RETURN QUERY SELECT v_new_id, v_prefix, v_expires;
END
$$;
REVOKE ALL ON FUNCTION cortex_issue_api_token(uuid,text,text,text[],uuid[],timestamptz,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION cortex_issue_api_token(uuid,text,text,text[],uuid[],timestamptz,text) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION cortex_issue_api_token(uuid,text,text,text[],uuid[],timestamptz,text) TO cortex_app;

-- Rotation revokes the locked live token and re-issues an exact copy with
-- a fresh in-SQL digest in the same transaction; the caller learns every
-- copied attribute except the stored digest.
CREATE FUNCTION cortex_rotate_api_token(p_token_public_id uuid, p_secret text, p_reason text)
RETURNS TABLE(token_public_id uuid, token_prefix text, token_name text, subject_public_id uuid, principal_type text, token_scopes text[], token_workspace_ids uuid[], expires_at timestamptz)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_tenant uuid;
    v_caller uuid;
    v_name text;
    v_scopes text[];
    v_workspace_ids uuid[];
    v_expires timestamptz;
    v_user bigint;
    v_service bigint;
    v_subject uuid;
    v_is_service boolean;
    v_prefix text;
    v_digest bytea;
    v_new_id uuid;
    v_metadata jsonb;
BEGIN
    SELECT d.tenant_id, d.caller_public_id INTO v_tenant, v_caller
      FROM public.cortex_actor_admin_caller() d;
    IF p_token_public_id IS NULL
       OR p_secret IS NULL OR length(p_secret) < 12 THEN
        RAISE EXCEPTION 'token rotation arguments are invalid' USING ERRCODE = '22023';
    END IF;
    SELECT t.name, t.scopes, t.workspace_ids, t.expires_at,
           u.id, s.id, COALESCE(u.public_id, s.public_id), s.id IS NOT NULL
      INTO v_name, v_scopes, v_workspace_ids, v_expires, v_user, v_service, v_subject, v_is_service
      FROM public.api_tokens t
      LEFT JOIN public.app_users u
        ON u.tenant_id = t.tenant_id AND u.id = t.subject_user_id
      LEFT JOIN public.service_accounts s
        ON s.tenant_id = t.tenant_id AND s.id = t.subject_service_account_id
     WHERE t.tenant_id = v_tenant
       AND t.public_id = p_token_public_id
       AND t.revoked_at IS NULL
       FOR UPDATE OF t;
    IF v_user IS NULL AND v_service IS NULL THEN
        RAISE EXCEPTION 'token does not exist in tenant or is revoked' USING ERRCODE = '23503';
    END IF;
    UPDATE public.api_tokens
       SET revoked_at = clock_timestamp(),
           updated_at = now()
     WHERE tenant_id = v_tenant
       AND public_id = p_token_public_id
       AND revoked_at IS NULL;
    v_prefix := left(p_secret, 12);
    v_digest := public.hmac(convert_to(p_secret, 'UTF8'), convert_to(v_tenant::text, 'UTF8'), 'sha256');
    INSERT INTO public.api_tokens AS t
        (tenant_id, name, token_prefix, token_digest, subject_user_id, subject_service_account_id, scopes, workspace_ids, expires_at, created_by)
    VALUES
        (v_tenant, v_name, v_prefix, v_digest, v_user, v_service, v_scopes, v_workspace_ids, v_expires, v_caller)
    RETURNING t.public_id INTO v_new_id;
    v_metadata := jsonb_build_object('reason', COALESCE(NULLIF(p_reason, ''), ''), 'allowed', true, 'rotated_from', p_token_public_id::text);
    INSERT INTO public.audit_events
        (tenant_id, actor_public_id, action, resource_type, resource_public_id, metadata, event_hash, reason, allowed)
    VALUES
        (v_tenant, v_caller, 'identity.token.rotated', 'token', v_new_id,
         v_metadata, public.digest(v_metadata::text, 'sha256'), COALESCE(NULLIF(p_reason, ''), ''), true);
    RETURN QUERY SELECT v_new_id, v_prefix, v_name, v_subject,
        CASE WHEN v_is_service THEN 'service_account'::text ELSE 'user'::text END,
        v_scopes, v_workspace_ids, v_expires;
END
$$;
REVOKE ALL ON FUNCTION cortex_rotate_api_token(uuid,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION cortex_rotate_api_token(uuid,text,text) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION cortex_rotate_api_token(uuid,text,text) TO cortex_app;

-- Revocation is idempotent per call: an actual transition appends the
-- audit row, an already-revoked token returns false without new audit,
-- and a token unknown in the principal's tenant fails closed.
CREATE FUNCTION cortex_revoke_api_token(p_token_public_id uuid, p_reason text)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_tenant uuid;
    v_caller uuid;
    v_metadata jsonb;
BEGIN
    SELECT d.tenant_id, d.caller_public_id INTO v_tenant, v_caller
      FROM public.cortex_actor_admin_caller() d;
    IF p_token_public_id IS NULL THEN
        RAISE EXCEPTION 'token is required' USING ERRCODE = '22023';
    END IF;
    UPDATE public.api_tokens
       SET revoked_at = COALESCE(revoked_at, clock_timestamp()),
           updated_at = now()
     WHERE tenant_id = v_tenant
       AND public_id = p_token_public_id
       AND revoked_at IS NULL;
    IF NOT FOUND THEN
        IF NOT EXISTS (
            SELECT 1 FROM public.api_tokens t
             WHERE t.tenant_id = v_tenant
               AND t.public_id = p_token_public_id
        ) THEN
            RAISE EXCEPTION 'token does not exist in tenant' USING ERRCODE = '23503';
        END IF;
        RETURN false;
    END IF;
    v_metadata := jsonb_build_object('reason', COALESCE(NULLIF(p_reason, ''), ''), 'allowed', true);
    INSERT INTO public.audit_events
        (tenant_id, actor_public_id, action, resource_type, resource_public_id, metadata, event_hash, reason, allowed)
    VALUES
        (v_tenant, v_caller, 'identity.token.revoked', 'token', p_token_public_id,
         v_metadata, public.digest(v_metadata::text, 'sha256'), COALESCE(NULLIF(p_reason, ''), ''), true);
    RETURN true;
END
$$;
REVOKE ALL ON FUNCTION cortex_revoke_api_token(uuid,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION cortex_revoke_api_token(uuid,text) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION cortex_revoke_api_token(uuid,text) TO cortex_app;

-- cortex_bind_principal is replaced additively (same three-argument
-- signature, same migration ownership, search path, and grants): it still
-- persists the bound actor identity and clears any stale workspace/project
-- scope on EVERY bind or rebind. EVERY bind — for actors whose stored
-- grant_digest is non-empty and for legacy empty-digest actors alike —
-- requires the unforgeable, authentication-bound token provenance
-- v1:<token uuid>:<hexmac> minted by cortex_verify_token_principal from a
-- live token of THIS actor: the MAC is recomputed here under the locked
-- token and the actor's current grant version. The deterministic grant
-- digest is integrity metadata only (stored by provisioning and returned
-- to operators); it is never an authenticator — knowing or recomputing an
-- actor's digest, including the identical digest shared by a different
-- actor with identical grants, satisfies nothing. Guessed strings, stale
-- versions, revoked or expired tokens, and cross-actor or cross-tenant
-- proofs all fail closed with no context installed. Call contract for
-- the application role: verify first, then bind with the minted
-- provenance and its grant version (never a configured digest).
CREATE OR REPLACE FUNCTION cortex_bind_principal(p_actor_public_id uuid, p_grant_digest text, p_grant_version bigint)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_tenant uuid;
    v_version bigint;
    v_token_public_id uuid;
    v_mac text;
    v_token_tenant uuid;
    v_token_digest bytea;
    v_token_revoked timestamptz;
    v_token_expires timestamptz;
    v_subject uuid;
    v_subject_active boolean;
    v_expected text;
BEGIN
    IF p_actor_public_id IS NULL OR p_grant_digest IS NULL OR NULLIF(p_grant_digest, '') IS NULL
       OR p_grant_version IS NULL OR p_grant_version <= 0 THEN
        RAISE EXCEPTION 'principal binding is required' USING ERRCODE = '28000';
    END IF;
    IF p_grant_digest !~ '^v1:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:[0-9a-f]{64}$' THEN
        RAISE EXCEPTION 'principal binding requires token-bound provenance' USING ERRCODE = '28000';
    END IF;
    SELECT tenant_id, grant_version
      INTO v_tenant, v_version
      FROM public.actor_subjects
     WHERE public_id = p_actor_public_id
       AND active
       AND revoked_at IS NULL
       AND grant_version = p_grant_version
       FOR UPDATE;
    IF v_tenant IS NULL THEN
        RAISE EXCEPTION 'principal grant is revoked or stale' USING ERRCODE = '28000';
    END IF;
    v_token_public_id := substring(p_grant_digest FROM 4 FOR 36)::uuid;
    v_mac := substring(p_grant_digest FROM 41);
    SELECT t.tenant_id, t.token_digest, t.revoked_at, t.expires_at,
           COALESCE(u.public_id, s.public_id), COALESCE(u.active, s.active)
      INTO v_token_tenant, v_token_digest, v_token_revoked, v_token_expires,
           v_subject, v_subject_active
      FROM public.api_tokens t
      LEFT JOIN public.app_users u
        ON u.tenant_id = t.tenant_id AND u.id = t.subject_user_id
      LEFT JOIN public.service_accounts s
        ON s.tenant_id = t.tenant_id AND s.id = t.subject_service_account_id
     WHERE t.public_id = v_token_public_id
      FOR UPDATE OF t;
    IF v_token_tenant IS NULL
       OR v_token_tenant <> v_tenant
       OR v_subject IS DISTINCT FROM p_actor_public_id
       OR v_subject_active IS NOT TRUE
       OR v_token_revoked IS NOT NULL
       OR (v_token_expires IS NOT NULL AND v_token_expires <= clock_timestamp())
       THEN
        RAISE EXCEPTION 'principal binding proof is stale, revoked, or foreign' USING ERRCODE = '28000';
    END IF;
    v_expected := encode(
        public.hmac(
            convert_to(
                v_tenant::text || ':' || p_actor_public_id::text || ':' || v_version::text || ':' || v_token_public_id::text,
                'UTF8'),
            v_token_digest, 'sha256'),
        'hex');
    IF v_mac <> v_expected THEN
        RAISE EXCEPTION 'principal binding proof is invalid' USING ERRCODE = '28000';
    END IF;
    DELETE FROM public.cortex_tenant_context
     WHERE backend_pid = pg_backend_pid() AND transaction_id <> txid_current();
    INSERT INTO public.cortex_tenant_context
        (backend_pid, transaction_id, tenant_id, actor_public_id, workspace_id, project_id, scope_bound_at)
    VALUES (pg_backend_pid(), txid_current(), v_tenant, p_actor_public_id, NULL, NULL, NULL)
    ON CONFLICT (backend_pid, transaction_id) DO UPDATE
      SET tenant_id = EXCLUDED.tenant_id,
          actor_public_id = EXCLUDED.actor_public_id,
          workspace_id = NULL,
          project_id = NULL,
          scope_bound_at = NULL,
          bound_at = clock_timestamp();
END
$$;
REVOKE ALL ON FUNCTION cortex_bind_principal(uuid,text,bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION cortex_bind_principal(uuid,text,bigint) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION cortex_bind_principal(uuid,text,bigint) TO cortex_app;

-- Grants-derived scope binding. The workspace and project are resolved by
-- public ID strictly inside the principal-derived tenant and are then
-- authorized against the bound actor's principal_grants rows (exact
-- workspace grant; exact, scoped, or wildcard project grant). Revoked or
-- absent grants fail closed even though the coordinates are same-tenant.
CREATE FUNCTION cortex_bind_project_scope(p_workspace_public_id uuid, p_project_public_id uuid)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_tenant uuid;
    v_actor uuid;
    v_workspace bigint;
    v_project bigint;
    v_project_workspace bigint;
    v_granted boolean;
BEGIN
    IF p_workspace_public_id IS NULL THEN
        RAISE EXCEPTION 'workspace binding is required' USING ERRCODE = '28000';
    END IF;
    v_tenant := public.cortex_current_tenant();
    IF v_tenant IS NULL THEN
        RAISE EXCEPTION 'principal tenant binding is required before scoping' USING ERRCODE = '28000';
    END IF;
    SELECT c.actor_public_id INTO v_actor
      FROM public.cortex_tenant_context c
     WHERE c.backend_pid = pg_backend_pid() AND c.transaction_id = txid_current();
    IF v_actor IS NULL THEN
        RAISE EXCEPTION 'principal tenant binding is required before scoping' USING ERRCODE = '28000';
    END IF;
    SELECT w.id INTO v_workspace
      FROM public.workspaces w
     WHERE w.tenant_id = v_tenant AND w.public_id = p_workspace_public_id;
    IF v_workspace IS NULL THEN
        RAISE EXCEPTION 'workspace is not part of the principal tenant' USING ERRCODE = '42501';
    END IF;
    -- Workspace grants are exact: the bound actor must hold the workspace's
    -- public ID as a workspace grant.
    IF NOT EXISTS (
        SELECT 1 FROM public.principal_grants g
         WHERE g.tenant_id = v_tenant
           AND g.actor_public_id = v_actor
           AND g.grant_type = 'workspace'
           AND g.grant_value = p_workspace_public_id::text
    ) THEN
        RAISE EXCEPTION 'principal is not granted workspace' USING ERRCODE = '42501';
    END IF;
    IF p_project_public_id IS NOT NULL THEN
        SELECT p.id, p.workspace_id INTO v_project, v_project_workspace
          FROM public.projects p
         WHERE p.tenant_id = v_tenant AND p.public_id = p_project_public_id;
        IF v_project IS NULL OR v_project_workspace <> v_workspace THEN
            RAISE EXCEPTION 'project is not part of the bound workspace' USING ERRCODE = '42501';
        END IF;
        -- Explicit wildcard semantics: project grant '*' or scope grant
        -- 'project:*' cover every project of the workspace; exact project
        -- grants and 'project:<public_id>' scope grants cover one.
        IF NOT EXISTS (
            SELECT 1 FROM public.principal_grants g
             WHERE g.tenant_id = v_tenant
               AND g.actor_public_id = v_actor
               AND (
                   (g.grant_type = 'project'
                       AND g.grant_value IN ('*', p_project_public_id::text))
                   OR (g.grant_type = 'scope'
                       AND g.grant_value IN ('project:*', 'project:' || p_project_public_id::text))
               )
        ) THEN
            RAISE EXCEPTION 'principal is not granted project' USING ERRCODE = '42501';
        END IF;
    END IF;
    UPDATE public.cortex_tenant_context
       SET workspace_id = v_workspace,
           project_id = v_project,
           scope_bound_at = clock_timestamp()
     WHERE backend_pid = pg_backend_pid() AND transaction_id = txid_current();
    IF NOT FOUND THEN
        RAISE EXCEPTION 'principal tenant binding is required before scoping' USING ERRCODE = '28000';
    END IF;
END
$$;
REVOKE ALL ON FUNCTION cortex_bind_project_scope(uuid, uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION cortex_bind_project_scope(uuid, uuid) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION cortex_bind_project_scope(uuid, uuid) TO cortex_app;

-- Identity least privilege (REQ-IDP-002, REQ-IDP-007): the application
-- role loses every direct table privilege on actor_subjects and
-- principal_grants. Identity reads and mutations flow exclusively through
-- the definer routines above; graph labels keep a column-only read on the
-- three non-sensitive actor columns. api_tokens loses every direct write
-- too: issue, rotate, and revoke run only through the audited definer
-- lifecycle routines, so the application role keeps a column read that
-- can never select token_digest and holds no INSERT, UPDATE, or DELETE
-- on the table. Grant revocation and re-issue flow through grant_version
-- bumps on actor_subjects (verified by cortex_bind_principal), never
-- through direct identity-table mutation by the application role. PUBLIC
-- was never granted these tables and stays empty-handed.
REVOKE ALL ON public.actor_subjects, public.principal_grants FROM cortex_app;
GRANT SELECT (tenant_id, public_id, subject) ON public.actor_subjects TO cortex_app;
REVOKE SELECT ON public.api_tokens FROM cortex_app;
REVOKE INSERT, UPDATE, DELETE ON public.api_tokens FROM cortex_app;
GRANT SELECT (id, public_id, tenant_id, token_prefix, subject_user_id, subject_service_account_id, scopes, workspace_ids, rate_limit_tier, expires_at, revoked_at, last_used_at, created_at, updated_at, name, created_by) ON public.api_tokens TO cortex_app;

CREATE FUNCTION cortex_current_workspace()
RETURNS bigint
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT workspace_id
      FROM public.cortex_tenant_context
     WHERE backend_pid = pg_backend_pid() AND transaction_id = txid_current()
$$;
REVOKE ALL ON FUNCTION cortex_current_workspace() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cortex_current_workspace() TO cortex_app, cortex_admin;

CREATE FUNCTION cortex_current_project()
RETURNS bigint
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT project_id
      FROM public.cortex_tenant_context
     WHERE backend_pid = pg_backend_pid() AND transaction_id = txid_current()
$$;
REVOKE ALL ON FUNCTION cortex_current_project() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cortex_current_project() TO cortex_app, cortex_admin;

-- 11. Row level security: ENABLE and FORCE on every artifact table. The
--     policy binds the verified tenant context AND the trusted
--     principal-derived workspace/project scope, so a same-tenant grant
--     cannot cross workspace or project boundaries even for the table
--     owner; child tables resolve their scope through their artifact.
ALTER TABLE project_artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_artifacts FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS cortex_project_isolation ON project_artifacts;
CREATE POLICY cortex_project_isolation ON project_artifacts AS PERMISSIVE FOR ALL TO PUBLIC
    USING (
        tenant_id = public.cortex_current_tenant()
        AND workspace_id = public.cortex_current_workspace()
        AND (
            (source_scope = 'project'
                AND project_id IS NOT DISTINCT FROM public.cortex_current_project())
            OR (source_scope = 'workspace_default' AND project_id IS NULL)
        )
    )
    WITH CHECK (
        tenant_id = public.cortex_current_tenant()
        AND workspace_id = public.cortex_current_workspace()
        AND (
            (source_scope = 'project'
                AND project_id IS NOT DISTINCT FROM public.cortex_current_project())
            OR (source_scope = 'workspace_default' AND project_id IS NULL)
        )
    );

ALTER TABLE project_artifact_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_artifact_revisions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS cortex_project_isolation ON project_artifact_revisions;
CREATE POLICY cortex_project_isolation ON project_artifact_revisions AS PERMISSIVE FOR ALL TO PUBLIC
    USING (
        tenant_id = public.cortex_current_tenant()
        AND EXISTS (
            SELECT 1 FROM project_artifacts a
             WHERE a.tenant_id = project_artifact_revisions.tenant_id
               AND a.id = project_artifact_revisions.artifact_id
        )
    )
    WITH CHECK (
        tenant_id = public.cortex_current_tenant()
        AND EXISTS (
            SELECT 1 FROM project_artifacts a
             WHERE a.tenant_id = project_artifact_revisions.tenant_id
               AND a.id = project_artifact_revisions.artifact_id
        )
    );

ALTER TABLE project_artifact_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_artifact_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS cortex_project_isolation ON project_artifact_events;
CREATE POLICY cortex_project_isolation ON project_artifact_events AS PERMISSIVE FOR ALL TO PUBLIC
    USING (
        tenant_id = public.cortex_current_tenant()
        AND EXISTS (
            SELECT 1 FROM project_artifacts a
             WHERE a.tenant_id = project_artifact_events.tenant_id
               AND a.id = project_artifact_events.artifact_id
        )
    )
    WITH CHECK (
        tenant_id = public.cortex_current_tenant()
        AND EXISTS (
            SELECT 1 FROM project_artifacts a
             WHERE a.tenant_id = project_artifact_events.tenant_id
               AND a.id = project_artifact_events.artifact_id
        )
    );

ALTER TABLE project_artifact_activations ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_artifact_activations FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS cortex_project_isolation ON project_artifact_activations;
CREATE POLICY cortex_project_isolation ON project_artifact_activations AS PERMISSIVE FOR ALL TO PUBLIC
    USING (
        tenant_id = public.cortex_current_tenant()
        AND EXISTS (
            SELECT 1 FROM project_artifacts a
             WHERE a.tenant_id = project_artifact_activations.tenant_id
               AND a.id = project_artifact_activations.artifact_id
        )
    )
    WITH CHECK (
        tenant_id = public.cortex_current_tenant()
        AND EXISTS (
            SELECT 1 FROM project_artifacts a
             WHERE a.tenant_id = project_artifact_activations.tenant_id
               AND a.id = project_artifact_activations.artifact_id
        )
    );

ALTER TABLE project_artifact_idempotency ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_artifact_idempotency FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS cortex_project_isolation ON project_artifact_idempotency;
CREATE POLICY cortex_project_isolation ON project_artifact_idempotency AS PERMISSIVE FOR ALL TO PUBLIC
    USING (
        tenant_id = public.cortex_current_tenant()
        AND workspace_id = public.cortex_current_workspace()
        AND (
            (project_id IS NOT NULL
                AND project_id IS NOT DISTINCT FROM public.cortex_current_project())
            OR project_id IS NULL
        )
    )
    WITH CHECK (
        tenant_id = public.cortex_current_tenant()
        AND workspace_id = public.cortex_current_workspace()
        AND (
            (project_id IS NOT NULL
                AND project_id IS NOT DISTINCT FROM public.cortex_current_project())
            OR project_id IS NULL
        )
    );

ALTER TABLE project_storage_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_storage_usage FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS cortex_project_isolation ON project_storage_usage;
CREATE POLICY cortex_project_isolation ON project_storage_usage AS PERMISSIVE FOR ALL TO PUBLIC
    USING (
        tenant_id = public.cortex_current_tenant()
        AND workspace_id = public.cortex_current_workspace()
        AND (
            (project_id IS NOT NULL
                AND project_id IS NOT DISTINCT FROM public.cortex_current_project())
            OR project_id IS NULL
        )
    )
    WITH CHECK (
        tenant_id = public.cortex_current_tenant()
        AND workspace_id = public.cortex_current_workspace()
        AND (
            (project_id IS NOT NULL
                AND project_id IS NOT DISTINCT FROM public.cortex_current_project())
            OR project_id IS NULL
        )
    );

-- 12. Grants: least privilege. The application may read and append evidence
--     and mutate only live artifact/activation/commit state; it holds no
--     DELETE on any artifact table (retention is indefinite), no
--     UPDATE on immutable history, and no way to reset quota counters
--     (the monotonic guard aborts decreases and the rows cannot be
--     removed). Admin stays read-only, the migration role owns the tables,
--     and PUBLIC gets nothing.
REVOKE ALL ON project_artifacts, project_artifact_revisions, project_artifact_events, project_artifact_activations, project_artifact_idempotency, project_storage_usage FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON project_artifacts TO cortex_app;
GRANT SELECT, INSERT ON project_artifact_revisions, project_artifact_events TO cortex_app;
GRANT SELECT, INSERT, UPDATE ON project_artifact_activations TO cortex_app;
GRANT SELECT, INSERT, UPDATE ON project_artifact_idempotency TO cortex_app;
GRANT SELECT, INSERT, UPDATE ON project_storage_usage TO cortex_app;
GRANT SELECT ON project_artifacts, project_artifact_revisions, project_artifact_events, project_artifact_activations, project_artifact_idempotency, project_storage_usage TO cortex_admin;
GRANT ALL ON project_artifacts, project_artifact_revisions, project_artifact_events, project_artifact_activations, project_artifact_idempotency, project_storage_usage TO cortex_migration;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO cortex_app, cortex_migration;

-- 13. Migration-role-only service bootstrap reconciler (REQ-BPR-003,
--     REQ-BPR-004, REQ-BPR-008): privileged startup calls this exactly
--     once per Open on the still-privileged migration connection with the
--     configured tenant/workspace/principal identity, the canonical
--     durable grant set, the reserved bootstrap token name, and the
--     configured bearer. The call serializes on a transaction-scoped
--     advisory lock over the (tenant, actor) pair taken before any
--     bootstrap identity row is read or inserted, then atomically
--     validates the existing tenant/workspace, creates or validates the
--     service_accounts row and the active actor subject, reconciles the
--     exact canonical principal_grants set (folding the integrity digest
--     and bumping grant_version exactly once per real transition),
--     creates or reconciles the reserved-name bootstrap api_token whose
--     stored digest is the tenant-keyed HMAC-SHA256 of the configured
--     bearer derived ONLY inside SQL, and appends non-secret audit
--     evidence for real state transitions only; an unchanged restart
--     writes nothing and audits nothing. Any conflict, inactive/revoked
--     actor, audit failure, or token failure rolls back every effect.
--     EXECUTE is limited to cortex_migration: PUBLIC, cortex_app, and
--     cortex_admin are revoked, so the application runtime can never
--     re-run reconciliation or rotate credentials by itself. A configured
--     bearer matching a revoked reserved bootstrap token fails closed and
--     is never resurrected; an explicitly different bearer recovers by
--     minting a fresh token while the revoked history is preserved. The
--     reserved token's stored prefix is the bearer's textual head plus a
--     deterministic digest-derived suffix, so bearers sharing a textual
--     head stay unique under UNIQUE (tenant_id, token_prefix) while
--     verification keeps matching the head plus exact digest equality,
--     and the routine itself is created schema-qualified in public with
--     ownership transferred to cortex_migration.
CREATE FUNCTION public.cortex_bootstrap_service_principal(p_tenant_id uuid, p_workspace_public_id uuid, p_actor_public_id uuid, p_subject text, p_service_name text, p_grants jsonb, p_token_name text, p_token_secret text, p_reason text)
RETURNS TABLE(token_public_id uuid, grant_version bigint, bootstrap_action text)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_subject text;
    v_service_name text;
    v_existing_name text;
    v_token_name text;
    v_reason text;
    v_count integer;
    v_canonical text;
    v_stored_canonical text;
    v_digest text;
    v_service_id bigint;
    v_service_active boolean;
    v_version bigint;
    v_stored_digest text;
    v_active boolean;
    v_revoked timestamptz;
    v_actor_type text;
    v_subject_row text;
    v_fresh boolean := false;
    v_grants_reconciled boolean := false;
    v_token_transition boolean := false;
    v_token_digest bytea;
    v_stored_token_digest bytea;
    v_prefix text;
    v_token_id uuid;
    v_new_token_id uuid;
    v_active_named integer;
    v_action text;
    v_metadata jsonb;
BEGIN
    IF p_tenant_id IS NULL
       OR p_workspace_public_id IS NULL
       OR p_actor_public_id IS NULL THEN
        RAISE EXCEPTION 'bootstrap arguments are invalid' USING ERRCODE = '22023';
    END IF;
    v_subject := NULLIF(btrim(p_subject), '');
    v_service_name := NULLIF(btrim(p_service_name), '');
    v_token_name := NULLIF(btrim(p_token_name), '');
    v_reason := COALESCE(NULLIF(btrim(p_reason), ''), '');
    IF v_subject IS NULL
       OR v_service_name IS NULL
       OR v_token_name IS NULL
       OR p_token_secret IS NULL OR length(p_token_secret) < 12 THEN
        RAISE EXCEPTION 'bootstrap arguments are invalid' USING ERRCODE = '22023';
    END IF;
    IF p_grants IS NULL OR jsonb_typeof(p_grants) <> 'array' THEN
        RAISE EXCEPTION 'bootstrap grants must be a JSON array' USING ERRCODE = '22023';
    END IF;
    SELECT count(*) INTO v_count FROM jsonb_array_elements(p_grants);
    IF v_count < 1 THEN
        RAISE EXCEPTION 'bootstrap requires at least one grant' USING ERRCODE = '22023';
    END IF;
    IF EXISTS (
        SELECT 1 FROM jsonb_array_elements(p_grants) AS obj
         WHERE jsonb_typeof(obj) <> 'object'
    ) THEN
        RAISE EXCEPTION 'bootstrap grants must be type/value objects' USING ERRCODE = '22023';
    END IF;
    IF EXISTS (
        SELECT 1 FROM jsonb_array_elements(p_grants) AS obj
         WHERE (SELECT count(*) FROM jsonb_object_keys(obj)) <> 2
            OR obj->>'type' IS NULL
            OR obj->>'value' IS NULL
            OR (obj->>'type') NOT IN ('role', 'workspace', 'project', 'classification', 'scope')
            OR NULLIF(btrim(obj->>'value'), '') IS NULL
    ) THEN
        RAISE EXCEPTION 'bootstrap grants must be non-empty allowlisted type/value objects' USING ERRCODE = '22023';
    END IF;
    IF EXISTS (
        SELECT 1
          FROM (
            SELECT obj->>'type' AS grant_type, obj->>'value' AS grant_value
              FROM jsonb_array_elements(p_grants) AS obj
          ) q
         GROUP BY q.grant_type, q.grant_value
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'bootstrap grants must be unique' USING ERRCODE = '22023';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM jsonb_array_elements(p_grants) AS g
         WHERE g->>'type' = 'workspace'
           AND g->>'value' = p_workspace_public_id::text
    ) THEN
        RAISE EXCEPTION 'bootstrap grants must include the configured workspace grant' USING ERRCODE = '22023';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM jsonb_array_elements(p_grants) AS g
         WHERE g->>'type' = 'role'
           AND g->>'value' IN ('owner', 'admin')
    ) THEN
        RAISE EXCEPTION 'bootstrap grants must include the owner or admin role' USING ERRCODE = '22023';
    END IF;
    SELECT string_agg(q.grant_type || ':' || q.grant_value, E'\n' ORDER BY q.grant_type, q.grant_value)
      INTO v_canonical
      FROM (
        SELECT DISTINCT obj->>'type' AS grant_type, obj->>'value' AS grant_value
          FROM jsonb_array_elements(p_grants) AS obj
      ) q;
    SELECT count(*) INTO v_count
      FROM (
        SELECT DISTINCT obj->>'type' AS grant_type, obj->>'value' AS grant_value
          FROM jsonb_array_elements(p_grants) AS obj
      ) q;
    v_digest := encode(public.digest(convert_to(v_canonical, 'UTF8'), 'sha256'), 'hex');
    -- Serialization: exactly one reconciler per (tenant, actor) pair,
    -- locked before any bootstrap identity row is read or inserted.
    PERFORM pg_advisory_xact_lock(hashtextextended(p_tenant_id::text || ':' || p_actor_public_id::text, 0));
    IF NOT EXISTS (
        SELECT 1 FROM public.organizations o
         WHERE o.tenant_id = p_tenant_id
    ) THEN
        RAISE EXCEPTION 'bootstrap tenant does not exist' USING ERRCODE = '23503';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.workspaces w
         WHERE w.tenant_id = p_tenant_id
           AND w.public_id = p_workspace_public_id
    ) THEN
        RAISE EXCEPTION 'bootstrap workspace does not exist in tenant' USING ERRCODE = '23503';
    END IF;
    SELECT s.id, s.name, s.active INTO v_service_id, v_existing_name, v_service_active
      FROM public.service_accounts s
     WHERE s.tenant_id = p_tenant_id
       AND s.public_id = p_actor_public_id
       FOR UPDATE;
    IF v_service_id IS NULL THEN
        INSERT INTO public.service_accounts (tenant_id, public_id, name)
        VALUES (p_tenant_id, p_actor_public_id, v_service_name)
        RETURNING id INTO v_service_id;
    ELSIF v_existing_name <> v_service_name THEN
        RAISE EXCEPTION 'bootstrap service account name conflicts with the configured service' USING ERRCODE = '22023';
    ELSIF NOT v_service_active THEN
        RAISE EXCEPTION 'bootstrap service account is inactive' USING ERRCODE = '28000';
    END IF;
    SELECT a.grant_version, a.grant_digest, a.active, a.revoked_at, a.actor_type, a.subject
      INTO v_version, v_stored_digest, v_active, v_revoked, v_actor_type, v_subject_row
      FROM public.actor_subjects a
     WHERE a.tenant_id = p_tenant_id
       AND a.public_id = p_actor_public_id
       FOR UPDATE;
    IF v_version IS NULL THEN
        INSERT INTO public.actor_subjects
            (tenant_id, subject, actor_type, public_id, active, revoked_at, grant_version, grant_digest)
        VALUES
            (p_tenant_id, v_subject, 'service_account', p_actor_public_id, true, NULL, 1, v_digest);
        v_version := 1;
        v_fresh := true;
    ELSE
        IF v_actor_type <> 'service_account' OR v_subject_row <> v_subject THEN
            RAISE EXCEPTION 'bootstrap actor conflicts with an existing subject' USING ERRCODE = '22023';
        END IF;
        IF NOT v_active OR v_revoked IS NOT NULL THEN
            RAISE EXCEPTION 'bootstrap actor is revoked or inactive' USING ERRCODE = '28000';
        END IF;
    END IF;
    SELECT string_agg(g.grant_type || ':' || g.grant_value, E'\n' ORDER BY g.grant_type, g.grant_value)
      INTO v_stored_canonical
      FROM public.principal_grants g
     WHERE g.tenant_id = p_tenant_id
       AND g.actor_public_id = p_actor_public_id;
    IF v_fresh THEN
        INSERT INTO public.principal_grants
            (tenant_id, actor_public_id, grant_type, grant_value, created_by, updated_by)
        SELECT p_tenant_id, p_actor_public_id, q.grant_type, q.grant_value, p_actor_public_id, p_actor_public_id
          FROM (
            SELECT DISTINCT obj->>'type' AS grant_type, obj->>'value' AS grant_value
              FROM jsonb_array_elements(p_grants) AS obj
          ) q
         ORDER BY q.grant_type, q.grant_value;
    ELSIF v_stored_canonical IS DISTINCT FROM v_canonical OR v_stored_digest IS DISTINCT FROM v_digest THEN
        DELETE FROM public.principal_grants WHERE tenant_id = p_tenant_id AND actor_public_id = p_actor_public_id;
        INSERT INTO public.principal_grants
            (tenant_id, actor_public_id, grant_type, grant_value, created_by, updated_by)
        SELECT p_tenant_id, p_actor_public_id, q.grant_type, q.grant_value, p_actor_public_id, p_actor_public_id
          FROM (
            SELECT DISTINCT obj->>'type' AS grant_type, obj->>'value' AS grant_value
              FROM jsonb_array_elements(p_grants) AS obj
          ) q
         ORDER BY q.grant_type, q.grant_value;
        v_version := v_version + 1;
        UPDATE public.actor_subjects
           SET grant_digest = v_digest,
               grant_version = v_version
          WHERE tenant_id = p_tenant_id AND public_id = p_actor_public_id;
        v_grants_reconciled := true;
        v_metadata := jsonb_build_object('action', 'reconciled', 'reason', v_reason, 'allowed', true, 'grant_count', v_count);
        INSERT INTO public.audit_events
            (tenant_id, actor_public_id, action, resource_type, resource_public_id, metadata, event_hash, reason, allowed)
        VALUES
            (p_tenant_id, p_actor_public_id, 'identity.bootstrap.reconciled', 'actor', p_actor_public_id,
             v_metadata, public.digest(v_metadata::text, 'sha256'), v_reason, true);
    END IF;
    -- Reserved-name bootstrap token: the stored digest is derived ONLY
    -- here, inside SQL, from the plaintext configured bearer with the
    -- existing tenant-keyed HMAC; it is never returned or audited. The
    -- reserved name is bound to exactly one bootstrap identity: a token
    -- row already holding the name for any other subject (user or
    -- service account) is a fail-closed conflict, never adopted.
    v_token_digest := public.hmac(convert_to(p_token_secret, 'UTF8'), convert_to(p_tenant_id::text, 'UTF8'), 'sha256');
    -- Deterministic secret-derived prefix: the bearer's textual head plus
    -- the first 16 hex characters of the tenant-keyed digest, so bearers
    -- sharing a textual head (a common configured-bearer pattern) stay
    -- unique under UNIQUE (tenant_id, token_prefix) across rotation and
    -- revoked history. Verification stays caller-compatible: it matches
    -- the stored prefix's 12-character head plus exact digest equality.
    v_prefix := left(p_token_secret, 12) || ':' || substring(encode(v_token_digest, 'hex') FROM 1 FOR 16);
    IF EXISTS (
        SELECT 1 FROM public.api_tokens t
         WHERE t.tenant_id = p_tenant_id
           AND t.name = v_token_name
           AND (t.subject_user_id IS NOT NULL
                OR t.subject_service_account_id IS DISTINCT FROM v_service_id)
    ) THEN
        RAISE EXCEPTION 'bootstrap token name is reserved to another subject' USING ERRCODE = '28000';
    END IF;
    SELECT count(*) INTO v_active_named
      FROM public.api_tokens t
     WHERE t.tenant_id = p_tenant_id
       AND t.name = v_token_name
       AND t.subject_service_account_id = v_service_id
       AND t.revoked_at IS NULL;
    IF v_active_named > 1 THEN
        RAISE EXCEPTION 'multiple active bootstrap tokens exist for the service actor' USING ERRCODE = '23505';
    END IF;
    IF v_active_named = 1 THEN
        SELECT t.public_id, t.token_digest INTO v_token_id, v_stored_token_digest
          FROM public.api_tokens t
         WHERE t.tenant_id = p_tenant_id
           AND t.name = v_token_name
           AND t.subject_service_account_id = v_service_id
           AND t.revoked_at IS NULL
         ORDER BY t.id
         LIMIT 1
           FOR UPDATE OF t;
        IF v_stored_token_digest IS DISTINCT FROM v_token_digest THEN
            UPDATE public.api_tokens
               SET revoked_at = clock_timestamp(), updated_at = now()
             WHERE tenant_id = p_tenant_id
               AND public_id = v_token_id
               AND revoked_at IS NULL;
            INSERT INTO public.api_tokens
                (tenant_id, name, token_prefix, token_digest, subject_service_account_id, scopes, workspace_ids, expires_at, created_by)
            VALUES
                (p_tenant_id, v_token_name, v_prefix, v_token_digest, v_service_id, '{}', '{}', NULL, p_actor_public_id)
            RETURNING public_id INTO v_new_token_id;
            v_metadata := jsonb_build_object('action', 'token_rotated', 'reason', v_reason, 'allowed', true, 'grant_count', v_count, 'token', v_new_token_id::text, 'rotated_from', v_token_id::text);
            INSERT INTO public.audit_events
                (tenant_id, actor_public_id, action, resource_type, resource_public_id, metadata, event_hash, reason, allowed)
            VALUES
                (p_tenant_id, p_actor_public_id, 'identity.bootstrap.token_rotated', 'token', v_new_token_id,
                 v_metadata, public.digest(v_metadata::text, 'sha256'), v_reason, true);
            v_token_id := v_new_token_id;
            v_token_transition := true;
        END IF;
    ELSE
        IF EXISTS (
            SELECT 1 FROM public.api_tokens t
             WHERE t.tenant_id = p_tenant_id
               AND t.name = v_token_name
               AND t.subject_service_account_id = v_service_id
               AND t.revoked_at IS NOT NULL
               AND t.token_digest = v_token_digest
        ) THEN
            RAISE EXCEPTION 'configured bootstrap bearer matches a revoked bootstrap token' USING ERRCODE = '28000';
        END IF;
        INSERT INTO public.api_tokens
            (tenant_id, name, token_prefix, token_digest, subject_service_account_id, scopes, workspace_ids, expires_at, created_by)
        VALUES
            (p_tenant_id, v_token_name, v_prefix, v_token_digest, v_service_id, '{}', '{}', NULL, p_actor_public_id)
        RETURNING public_id INTO v_token_id;
        IF NOT v_fresh THEN
            v_token_transition := true;
            v_metadata := jsonb_build_object('action', 'token_rotated', 'reason', v_reason, 'allowed', true, 'grant_count', v_count, 'token', v_token_id::text);
            INSERT INTO public.audit_events
                (tenant_id, actor_public_id, action, resource_type, resource_public_id, metadata, event_hash, reason, allowed)
            VALUES
                (p_tenant_id, p_actor_public_id, 'identity.bootstrap.token_rotated', 'token', v_token_id,
                 v_metadata, public.digest(v_metadata::text, 'sha256'), v_reason, true);
        END IF;
    END IF;
    IF v_fresh THEN
        v_action := 'provisioned';
        v_metadata := jsonb_build_object('action', 'provisioned', 'reason', v_reason, 'allowed', true, 'grant_count', v_count, 'token', v_token_id::text);
        INSERT INTO public.audit_events
            (tenant_id, actor_public_id, action, resource_type, resource_public_id, metadata, event_hash, reason, allowed)
        VALUES
            (p_tenant_id, p_actor_public_id, 'identity.bootstrap.provisioned', 'actor', p_actor_public_id,
             v_metadata, public.digest(v_metadata::text, 'sha256'), v_reason, true);
    ELSIF v_token_transition THEN
        v_action := 'token_rotated';
    ELSIF v_grants_reconciled THEN
        v_action := 'reconciled';
    ELSE
        v_action := 'unchanged';
    END IF;
    RETURN QUERY SELECT v_token_id, v_version, v_action;
END
$$;
-- Ownership is transferred explicitly to the migration role so the
-- definer context is deterministic no matter which privileged role
-- applied the file, and every privilege statement is schema-qualified.
ALTER FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) OWNER TO cortex_migration;
REVOKE ALL ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) FROM cortex_app;
REVOKE ALL ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) TO cortex_migration;
-- Least-privilege definer prerequisites: the reconciler executes as its
-- owner cortex_migration, and baselines 100/101 deliberately leave that
-- role without any direct actor_subjects access (only cortex_app holds
-- the pinned label-triple column read above). The definer body needs
-- exactly three table privileges here: SELECT covers the FOR UPDATE row
-- locks that serialize reconciliation, INSERT covers the fresh service
-- actor row, and UPDATE covers the grant_digest/grant_version fold. No
-- broader authority is granted: no row removal, table rewriting,
-- references, or triggers, no schema-wide grant, and every other
-- identity table the body touches (organizations, workspaces,
-- service_accounts, principal_grants, api_tokens, audit_events) already
-- carries its exact 100/101 migration-role grant, with sequence
-- USAGE/SELECT for the identity columns covered above. Runtime roles
-- gain nothing: cortex_app and cortex_admin keep no direct DML on
-- actor_subjects and still cannot EXECUTE this reconciler.
GRANT SELECT, INSERT, UPDATE ON public.actor_subjects TO cortex_migration;

-- Migration 112: static bind contract for the configuration-derived
-- single-tenant principal.
--
-- WHY THIS EXISTS
-- cortex_bind_principal (100, replaced by 106 and then 108) installs the
-- transaction's RLS context: it inserts/updates public.cortex_tenant_context
-- (tenant_id, actor_public_id, cleared workspace/project scope) for the
-- current backend_pid + transaction_id. Every tenant-isolation policy created
-- by 100_server.sql:326-331 reads public.cortex_current_tenant(), which is
-- resolved from exactly that row, so a caller that skips the binder leaves
-- RLS with no tenant and all tenant-scoped statements fail closed. The binder
-- therefore cannot be skipped: the single-tenant composition must obtain a
-- dialect of the binder that installs the SAME context.
--
-- WHAT IT CHANGES
-- The replaced routine keeps the three-argument signature, the SECURITY
-- DEFINER search path, the canonical shared advisory gate, the FOR SHARE
-- grant revalidation, the error taxonomy, the cortex_app-only EXECUTE matrix,
-- and the exact context-writing tail. It accepts TWO provenance shapes:
--
--   v1:<token uuid>:<hex mac>  -- the authentication-bound provenance minted
--                                by token verification (migrations 106/108/
--                                110/111). Behaviour on this path is byte-for-
--                                 byte the migration-108 contract: the MAC is
--                                recomputed under the actor's live, unexpired,
--                                same-tenant token and the matching grant
--                                version. It remains the default and the ONLY
--                                path multi-tenant deployments may use.
--
--   static:<hex mac>           -- the single-tenant contract. There is no
--                                live token to bind, so the configured digest
--                                plays the role the token digest plays on the
--                                v1 path: the presented MAC is recomputed here
--                                as HMAC-SHA256 over
--                                `tenant:actor:grant_version` keyed by the
--                                actor's PERSISTED grant digest, re-read under
--                                the shared gate with the version the caller
--                                must already match. A caller cannot bind an
--                                arbitrary tenant (the key and the tenant both
--                                come from the actor's own row), cannot bind a
--                                different actor (the MAC message is the
--                                presented actor's public id), and cannot
--                                present a stale version (the FOR SHARE
--                                revalidation precedes the MAC check).
--
-- SCOPE / RISK NOTE
-- The static digest is deterministic in the actor's grants, so it is NOT a
-- bearer-independent secret in the way a token digest is. It is accepted only
-- because the self-hosted pivot composes a single trusted tenant whose
-- HTTP clients hold no database capability; the SECURITY DEFINER binder is
-- reachable only by cortex_app, the caller identity (actor, version) is
-- revalidated from the row rather than trusted from the argument, and the v1
-- path stays the strong contract for any multi-tenant deployment. The
-- migration is purely additive (CREATE OR REPLACE plus owner/ACL reassertion,
-- no destructive operation), idempotent-shaped like its 106/108 siblings, and
-- forward-only (no down path).
CREATE OR REPLACE FUNCTION cortex_bind_principal(p_actor_public_id uuid, p_grant_digest text, p_grant_version bigint)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_tenant uuid;
    v_version bigint;
    v_stored_digest text;
    v_static boolean;
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
    v_static := p_grant_digest ~ '^static:[0-9a-f]{64}$';
    IF NOT v_static
       AND p_grant_digest !~ '^v1:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:[0-9a-f]{64}$' THEN
        RAISE EXCEPTION 'principal binding requires token-bound provenance or a static digest' USING ERRCODE = '28000';
    END IF;
    -- Lock-free resolve: the tenant is derived WITHOUT any row lock, only
    -- to derive the canonical key.
    SELECT tenant_id INTO v_tenant
      FROM public.actor_subjects
     WHERE public_id = p_actor_public_id
       AND active
       AND revoked_at IS NULL;
    IF v_tenant IS NULL THEN
        RAISE EXCEPTION 'principal grant is revoked or stale' USING ERRCODE = '28000';
    END IF;
    -- Canonical shared actor gate BEFORE any row lock.
    PERFORM pg_advisory_xact_lock_shared(public.cortex_principal_key(v_tenant, p_actor_public_id));
    -- Revalidation under the gate: grant version and stored digest are
    -- re-read FOR SHARE; a committed invalidation is observed here.
    SELECT grant_version, grant_digest INTO v_version, v_stored_digest
      FROM public.actor_subjects
     WHERE tenant_id = v_tenant
       AND public_id = p_actor_public_id
       AND active
       AND revoked_at IS NULL
       FOR SHARE;
    IF v_version IS NULL OR v_version IS DISTINCT FROM p_grant_version THEN
        RAISE EXCEPTION 'principal grant is revoked or stale' USING ERRCODE = '28000';
    END IF;
    IF v_static THEN
        -- Static branch: the MAC is keyed by the actor's persisted grant
        -- digest and covers the actor's own tenant, public id, and the
        -- revalidated grant version. An empty stored digest has no key and
        -- fails closed without a comparison.
        IF v_stored_digest IS NULL OR v_stored_digest = '' THEN
            RAISE EXCEPTION 'principal binding proof is invalid' USING ERRCODE = '28000';
        END IF;
        v_expected := encode(
            public.hmac(
                convert_to(
                    v_tenant::text || ':' || p_actor_public_id::text || ':' || v_version::text,
                    'UTF8'),
                convert_to(v_stored_digest, 'UTF8'), 'sha256'),
            'hex');
        IF substring(p_grant_digest FROM 8) <> v_expected THEN
            RAISE EXCEPTION 'principal binding proof is invalid' USING ERRCODE = '28000';
        END IF;
    ELSE
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
          FOR SHARE OF t;
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

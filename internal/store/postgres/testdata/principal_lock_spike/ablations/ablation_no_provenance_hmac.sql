-- PREREGISTERED ABLATION A5 (R1R7, task-54669929dd0c44adad895410d81be320):
-- single-mechanism ablation of the migration 108 read path.
-- REMOVED COMPONENT: the HMAC provenance computation. Verify mints a
-- deterministic non-cryptographic stand-in (v1:<token>:<64 hex zeros>) and
-- bind validates against the SAME stand-in, so the verify+bind flow still
-- succeeds end-to-end while both pgcrypto hmac computations are gone.
-- KEPT: the shared actor advisory gates, both lock-free resolves, the FOR
-- SHARE revalidations, the telemetry usage advisory + conditional UPDATE,
-- and the tenant-context upsert.
-- UNSAFE PRODUCTION SEMANTICS: the binding proof is no longer
-- token-bound cryptography. This variant exists ONLY to attribute the
-- PgBouncer-only same-principal c32 deficit to the provenance computation.
-- Applied by test onto a FRESH throwaway database after the full 100..108
-- migration line; never applied to any certified database.
CREATE OR REPLACE FUNCTION cortex_verify_token_principal(p_token_prefix text, p_token_digest bytea, p_required_scope text)
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
      WHERE left(t.token_prefix, 12) = p_token_prefix
        AND t.token_digest = p_token_digest
      ORDER BY t.id
      LIMIT 1;
    IF v_token_id IS NULL OR v_tenant IS NULL OR v_subject IS NULL OR v_subject_active IS NOT TRUE THEN
        RETURN;
    END IF;
    PERFORM pg_advisory_xact_lock_shared(public.cortex_principal_key(v_tenant, v_subject));
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
      WHERE left(t.token_prefix, 12) = p_token_prefix
        AND t.token_digest = p_token_digest
      ORDER BY t.id
      LIMIT 1;
    IF v_token_id IS NULL OR v_tenant IS NULL OR v_subject IS NULL OR v_subject_active IS NOT TRUE THEN
        RETURN;
    END IF;
    SELECT a.grant_version INTO v_version
      FROM public.actor_subjects a
     WHERE a.tenant_id = v_tenant
       AND a.public_id = v_subject
       AND a.active
       AND a.revoked_at IS NULL
       FOR SHARE OF a;
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
    IF pg_try_advisory_xact_lock(hashtextextended('cortex:principal-usage:' || v_tenant::text || ':' || v_token_id::text, 0)) THEN
        BEGIN
            UPDATE public.api_tokens
               SET last_used_at = clock_timestamp(),
                   updated_at = now()
             WHERE api_tokens.tenant_id = v_tenant
               AND api_tokens.public_id = v_token_id
               AND api_tokens.revoked_at IS NULL
               AND (api_tokens.last_used_at IS NULL
                    OR api_tokens.last_used_at <= clock_timestamp() - interval '30 seconds');
        EXCEPTION
            WHEN OTHERS THEN NULL;
        END;
    END IF;
    -- ABLATION: the HMAC provenance computation is replaced by a constant
    -- stand-in (bind's matching ablation validates the same stand-in).
    v_provenance := 'v1:' || v_token_id::text || ':' || repeat('0', 64);
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
    SELECT tenant_id INTO v_tenant
      FROM public.actor_subjects
     WHERE public_id = p_actor_public_id
       AND active
       AND revoked_at IS NULL;
    IF v_tenant IS NULL THEN
        RAISE EXCEPTION 'principal grant is revoked or stale' USING ERRCODE = '28000';
    END IF;
    PERFORM pg_advisory_xact_lock_shared(public.cortex_principal_key(v_tenant, p_actor_public_id));
    SELECT grant_version INTO v_version
      FROM public.actor_subjects
     WHERE tenant_id = v_tenant
       AND public_id = p_actor_public_id
       AND active
       AND revoked_at IS NULL
       FOR SHARE;
    IF v_version IS NULL OR v_version IS DISTINCT FROM p_grant_version THEN
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
    -- ABLATION: the HMAC proof recomputation is replaced by the matching
    -- constant stand-in minted by the ablated verify.
    v_expected := repeat('0', 64);
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

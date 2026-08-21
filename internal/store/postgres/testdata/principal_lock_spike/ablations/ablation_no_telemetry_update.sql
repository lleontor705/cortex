-- PREREGISTERED ABLATION A1 (R1R7, task-54669929dd0c44adad895410d81be320):
-- single-component ablation of the migration 108 verify read path.
-- REMOVED COMPONENT: the last_used_at telemetry UPDATE statement ONLY.
-- KEPT: the token-usage try-advisory acquisition (including its exception
-- scaffolding), the shared actor advisory, both lock-free resolves, the
-- FOR SHARE revalidation, and the HMAC provenance computation.
-- UNSAFE PRODUCTION SEMANTICS: this variant exists ONLY to attribute the
-- PgBouncer-only same-principal c32 deficit to a single mechanism. It must
-- never ship: it acquires the usage advisory without advancing telemetry.
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
    -- ABLATION: the usage try-advisory is still acquired (with its original
    -- exception scaffolding) but the telemetry UPDATE statement is removed.
    IF pg_try_advisory_xact_lock(hashtextextended('cortex:principal-usage:' || v_tenant::text || ':' || v_token_id::text, 0)) THEN
        BEGIN
            NULL;
        EXCEPTION
            WHEN OTHERS THEN NULL;
        END;
    END IF;
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

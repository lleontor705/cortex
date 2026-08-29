-- Cortex v2 server wave: PostgreSQL migration 111 (forward-only).
--
-- SaaS data-plane authentication. The HTTP server must never accept a tenant
-- identifier from a client. This SECURITY DEFINER routine derives candidate
-- tenants only from the opaque token-prefix head, recomputes the existing
-- tenant-bound HMAC for each candidate, and delegates all liveness, grants,
-- rate-limit, and binding-provenance checks to the canonical verifier.
--
-- Prefix collisions are deliberately fail-closed: a bearer that verifies in
-- more than one tenant is rejected instead of being attributed by query order.

CREATE FUNCTION cortex_verify_token_principal_global(
    p_secret text,
    p_required_scope text
)
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
    rate_limit_tier text,
    binding_provenance text
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_candidate record;
    v_token_id uuid;
    v_matches integer := 0;
BEGIN
    IF NULLIF(p_secret, '') IS NULL OR length(p_secret) < 12 THEN
        RETURN;
    END IF;

    -- Do not trust a tenant sent by the caller. A token head is only an
    -- index hint; exact HMAC verification remains inside the canonical
    -- verifier and is required before any principal is returned.
    FOR v_candidate IN
        SELECT DISTINCT t.tenant_id
          FROM public.api_tokens AS t
         WHERE left(t.token_prefix, 12) = left(p_secret, 12)
    LOOP
        SELECT v.token_public_id
          INTO v_token_id
          FROM public.cortex_verify_token_principal_v2(
              left(p_secret, 12),
              public.hmac(
                  convert_to(p_secret, 'UTF8'),
                  convert_to(v_candidate.tenant_id::text, 'UTF8'),
                  'sha256'
              ),
              p_required_scope
          ) AS v;
        IF FOUND THEN
            v_matches := v_matches + 1;
            IF v_matches > 1 THEN
                RETURN;
            END IF;
        END IF;
    END LOOP;

    IF v_matches <> 1 THEN
        RETURN;
    END IF;

    FOR v_candidate IN
        SELECT DISTINCT t.tenant_id
          FROM public.api_tokens AS t
         WHERE left(t.token_prefix, 12) = left(p_secret, 12)
    LOOP
        RETURN QUERY
        SELECT v.*
          FROM public.cortex_verify_token_principal_v2(
              left(p_secret, 12),
              public.hmac(
                  convert_to(p_secret, 'UTF8'),
                  convert_to(v_candidate.tenant_id::text, 'UTF8'),
                  'sha256'
              ),
              p_required_scope
          ) AS v
         WHERE v.token_public_id = v_token_id;
        IF FOUND THEN
            RETURN;
        END IF;
    END LOOP;
END
$$;

ALTER FUNCTION public.cortex_verify_token_principal_global(text, text) OWNER TO cortex_migration;
REVOKE ALL ON FUNCTION public.cortex_verify_token_principal_global(text, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.cortex_verify_token_principal_global(text, text) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION public.cortex_verify_token_principal_global(text, text) TO cortex_app;

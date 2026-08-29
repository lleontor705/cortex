-- PostgreSQL server migration 110: expose the token-owned rate-limit tier
-- through the mediated verification result. The existing verifier remains
-- byte- and signature-stable; this additive v2 wrapper joins the verified
-- token public ID back to its same-tenant row under SECURITY DEFINER.

CREATE FUNCTION cortex_verify_token_principal_v2(
    p_token_prefix text,
    p_token_digest bytea,
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
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT v.token_public_id, v.token_name, v.token_prefix,
           v.subject_public_id, v.principal_type, v.tenant_id,
           v.token_scopes, v.token_workspace_ids, v.expires_at,
           v.revoked_at, v.last_used_at, v.roles, v.workspaces,
           v.projects, v.classification, v.grant_scopes,
           v.grant_version, t.rate_limit_tier, v.binding_provenance
      FROM public.cortex_verify_token_principal(
               p_token_prefix, p_token_digest, p_required_scope) AS v
      JOIN public.api_tokens AS t
        ON t.tenant_id = v.tenant_id
       AND t.public_id = v.token_public_id
$$;

GRANT EXECUTE ON FUNCTION public.cortex_verify_token_principal(text,bytea,text) TO cortex_migration;

ALTER FUNCTION public.cortex_verify_token_principal_v2(text,bytea,text) OWNER TO cortex_migration;
REVOKE ALL ON FUNCTION cortex_verify_token_principal_v2(text,bytea,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION cortex_verify_token_principal_v2(text,bytea,text) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION cortex_verify_token_principal_v2(text,bytea,text) TO cortex_app;

-- Migration 108: canonical SRW principal read/write gating (PG-00/PG-01/
-- PG-02, MIG-01). This file is FORWARD-ONLY and purely additive: it creates
-- ONE canonical transaction-scoped advisory key helper and additively
-- replaces the identity routines installed by migrations 100/106 with the
-- protocol proven by the T01 spike (testdata/principal_lock_spike/
-- canonical_protocol.sql, pinned SHA-256
-- 40d2b76f54562c62a44c2fb38bdbf823babc760b10c6b89be6d3dd87f87518a2):
--
--   * ONE canonical 64-bit key namespace for principal identity:
--       hashtextextended('cortex:principal:' || tenant || ':' || actor, 0)
--     Readers and writers derive the SAME key for the same (tenant, actor).
--   * Readers (cortex_verify_token_principal, cortex_bind_principal)
--     resolve token/actor lock-free, take pg_advisory_xact_lock_shared,
--     then re-read the identity row FOR SHARE. Verification re-reads the
--     token row WITHOUT any row lock (every api_tokens writer is
--     exclusively gated, so the shared gate already serializes the read
--     against invalidation) and takes FOR SHARE only on actor_subjects:
--     concurrent verifies of one token never wait on any token-row lock.
--   * EVERY identity invalidator (provisioning, activation change, token
--     issue/rotate/revoke, bootstrap reconciliation) takes the matching
--     EXCLUSIVE pg_advisory_xact_lock BEFORE any identity row lock, after a
--     lock-free lookup used ONLY to derive the key.
--   * Verification telemetry (last_used_at) is non-authoritative: at most
--     one concurrent verifier of a token wins a dedicated usage advisory
--     and advances a throttled monotonic timestamp WITHOUT ever waiting on
--     a peer verifier row lock and without ever failing authentication
--     (best-effort only; no verifier or binder takes a token-row lock the
--     telemetry winner could be holding).
--   * ONLY transaction-scoped advisory calls exist here: safe under
--     transaction-mode poolers (PgBouncer); no session advisory locks, no
--     SET-based session state, no backend affinity.
--
-- No table, index, trigger, or data is created, dropped, or rewritten; the
-- replaced functions keep their exact signatures, return shapes, owners,
-- search paths, and EXECUTE matrices, and every public error code and
-- message of the replaced routines is preserved.

-- ---------------------------------------------------------------------------
-- The canonical principal advisory key.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION public.cortex_principal_key(p_tenant uuid, p_actor uuid)
RETURNS bigint LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $fn$
    SELECT hashtextextended('cortex:principal:' || p_tenant::text || ':' || p_actor::text, 0)
$fn$;
ALTER FUNCTION public.cortex_principal_key(uuid, uuid) OWNER TO cortex_migration;
REVOKE ALL ON FUNCTION public.cortex_principal_key(uuid, uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.cortex_principal_key(uuid, uuid) FROM cortex_app;
REVOKE ALL ON FUNCTION public.cortex_principal_key(uuid, uuid) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION public.cortex_principal_key(uuid, uuid) TO cortex_migration;

-- ---------------------------------------------------------------------------
-- Readers: verification. Same signature, row shape, error taxonomy, and
-- EXECUTE matrix as migration 106. The same-token FOR UPDATE serialization
-- and the unconditional last_used_at write are gone; the token row is
-- re-read WITHOUT any row lock (PG-02: no verifier token-row lock waits)
-- and only the actor identity row is locked FOR SHARE.
-- ---------------------------------------------------------------------------
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
    -- Lock-free resolve (SRW reader protocol): the token and its subject
    -- are resolved WITHOUT any row lock; the shared actor gate below is the
    -- serialization point against every identity invalidator.
    -- Callers present the bearer's textual 12-character head; the reserved
    -- bootstrap token stores that head plus a deterministic digest-derived
    -- suffix, so matching on the stored prefix's head plus EXACT digest
    -- equality keeps resolving exactly one row.
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
    -- Canonical shared actor gate BEFORE any row lock: concurrent verifiers
    -- and binders of one principal overlap; an invalidator drains the
    -- in-flight readers, excludes new arrivals, and commits its mutation.
    PERFORM pg_advisory_xact_lock_shared(public.cortex_principal_key(v_tenant, v_subject));
    -- Revalidation under the gate: the token and its subject are re-read
    -- WITHOUT any token-row lock (every api_tokens writer is exclusively
    -- gated, so the shared gate already serializes this read against
    -- invalidation), and the actor identity row is locked FOR SHARE; any
    -- invalidation that committed between the lock-free resolve and the
    -- gate is observed here and fails closed. Taking no token-row lock is
    -- what lets concurrent verifies of one token overlap completely.
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
    -- Non-authoritative last_used_at telemetry: at most one concurrent
    -- verifier of this token wins the dedicated usage advisory; the winner
    -- advances the throttled monotonic timestamp WITHOUT ever waiting on a
    -- peer verifier row lock and WITHOUT ever failing authentication
    -- (best-effort only). No verifier takes any token-row lock, and every
    -- api_tokens invalidator is excluded by the shared actor gate, so the
    -- winner's conditional UPDATE cannot be blocked by, nor block, another
    -- verifier. The value is approximate, never an authorization input.
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

-- ---------------------------------------------------------------------------
-- Readers: principal binding. Same three-argument signature, proof
-- contract, error taxonomy, and EXECUTE matrix as migration 106. The
-- exclusive FOR UPDATE row locks are replaced by the shared gate and FOR
-- SHARE revalidation.
-- ---------------------------------------------------------------------------
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
    -- Revalidation under the gate: grant version and token state are
    -- re-read FOR SHARE; a committed invalidation is observed here.
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

-- ---------------------------------------------------------------------------
-- Invalidators: provisioning. Same signature, validation, audit, and EXECUTE
-- matrix as migration 106, gated by the exclusive canonical advisory before
-- any identity row lock.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION cortex_provision_actor(p_actor_public_id uuid, p_subject text, p_actor_type text, p_grants jsonb, p_reason text)
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
    -- Canonical exclusive actor gate BEFORE any identity row lock:
    -- provisioning an identity excludes concurrent principal readers and
    -- identity writers of the same (tenant, actor).
    PERFORM pg_advisory_xact_lock(public.cortex_principal_key(v_tenant, p_actor_public_id));
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

-- ---------------------------------------------------------------------------
-- Invalidators: activation change (direct actor revoke/reactivate). Same
-- signature, transition semantics, audit, and EXECUTE matrix as migration
-- 106, gated by the exclusive canonical advisory before the locked re-read.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION cortex_set_actor_active(p_target_actor_public_id uuid, p_active boolean, p_reason text)
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
    -- Canonical exclusive actor gate BEFORE any identity row lock: a direct
    -- revoke or reactivation drains in-flight principal readers and
    -- excludes new arrivals before the locked re-read below.
    PERFORM pg_advisory_xact_lock(public.cortex_principal_key(v_tenant, p_target_actor_public_id));
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

-- ---------------------------------------------------------------------------
-- Invalidators: token issue. Same signature, in-SQL digest derivation,
-- audit, and EXECUTE matrix as migration 106; the subject is resolved
-- lock-free, gated exclusively, then re-read FOR SHARE and revalidated
-- before the credential insert.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION cortex_issue_api_token(p_subject_public_id uuid, p_name text, p_secret text, p_scopes text[], p_workspace_ids uuid[], p_expires_at timestamptz, p_reason text)
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
    -- Canonical exclusive actor gate BEFORE any identity row lock: minting
    -- a credential for the subject excludes concurrent principal readers
    -- and identity writers of the same (tenant, actor).
    PERFORM pg_advisory_xact_lock(public.cortex_principal_key(v_tenant, p_subject_public_id));
    -- Locked re-read and revalidation of the subject under the gate. The
    -- nullable LEFT JOIN lookup is split into two single-table lockable
    -- queries: PostgreSQL rejects row locks on the nullable side of an
    -- outer join (SQLSTATE 0A000), and each statement below locks only a
    -- preserved (non-nullable) side, so the subject row that exists is
    -- revalidated FOR SHARE with identical fail-closed semantics.
    SELECT u.id INTO v_user
      FROM public.app_users u
     WHERE u.tenant_id = v_tenant
       AND u.public_id = p_subject_public_id
       AND u.active
       FOR SHARE OF u;
    SELECT s.id INTO v_service
      FROM public.service_accounts s
     WHERE s.tenant_id = v_tenant
       AND s.public_id = p_subject_public_id
       AND s.active
       FOR SHARE OF s;
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

-- ---------------------------------------------------------------------------
-- Invalidators: token rotation (token-ID writer). Same signature, in-SQL
-- digest derivation, audit, and EXECUTE matrix as migration 106; the token
-- and subject are resolved lock-free ONLY to derive the canonical key, then
-- the exclusive gate runs before the locked re-read that revalidates the
-- token and its subject and rejects any drift.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION cortex_rotate_api_token(p_token_public_id uuid, p_secret text, p_reason text)
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
    v_locked_subject uuid;
    v_subject_public uuid;
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
    -- Lock-free resolve: the token and its subject are read WITHOUT any row
    -- lock, ONLY to derive the canonical key.
    SELECT COALESCE(u.public_id, s.public_id)
      INTO v_subject
      FROM public.api_tokens t
      LEFT JOIN public.app_users u
        ON u.tenant_id = t.tenant_id AND u.id = t.subject_user_id
      LEFT JOIN public.service_accounts s
        ON s.tenant_id = t.tenant_id AND s.id = t.subject_service_account_id
     WHERE t.tenant_id = v_tenant
       AND t.public_id = p_token_public_id
       AND t.revoked_at IS NULL;
    IF v_subject IS NULL THEN
        RAISE EXCEPTION 'token does not exist in tenant or is revoked' USING ERRCODE = '23503';
    END IF;
    -- Canonical exclusive actor gate BEFORE any identity row lock.
    PERFORM pg_advisory_xact_lock(public.cortex_principal_key(v_tenant, v_subject));
    -- Locked re-read and revalidation under the gate: the token row is
    -- locked as the ONLY preserved side of the former outer join, and the
    -- subject is revalidated FOR SHARE through split single-table
    -- lockable lookups (PostgreSQL rejects row locks on the nullable
    -- side of an outer join, SQLSTATE 0A000; only the row that actually
    -- exists is locked). Any drift between the resolve and the locked
    -- read fails closed.
    SELECT t.name, t.scopes, t.workspace_ids, t.expires_at,
           t.subject_user_id, t.subject_service_account_id
      INTO v_name, v_scopes, v_workspace_ids, v_expires, v_user, v_service
      FROM public.api_tokens t
     WHERE t.tenant_id = v_tenant
       AND t.public_id = p_token_public_id
       AND t.revoked_at IS NULL
       FOR UPDATE OF t;
    v_locked_subject := NULL;
    v_subject_public := NULL;
    v_is_service := false;
    IF v_user IS NOT NULL THEN
        SELECT u.public_id INTO v_subject_public
          FROM public.app_users u
         WHERE u.tenant_id = v_tenant
           AND u.id = v_user
         FOR SHARE OF u;
        v_locked_subject := v_subject_public;
    END IF;
    IF v_service IS NOT NULL THEN
        SELECT s.public_id INTO v_subject_public
          FROM public.service_accounts s
         WHERE s.tenant_id = v_tenant
           AND s.id = v_service
         FOR SHARE OF s;
        IF v_subject_public IS NOT NULL THEN
            v_is_service := true;
            IF v_locked_subject IS NULL THEN
                v_locked_subject := v_subject_public;
            END IF;
        END IF;
    END IF;
    IF (v_user IS NULL AND v_service IS NULL)
       OR v_locked_subject IS DISTINCT FROM v_subject THEN
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
    RETURN QUERY SELECT v_new_id, v_prefix, v_name, v_locked_subject,
        CASE WHEN v_is_service THEN 'service_account'::text ELSE 'user'::text END,
        v_scopes, v_workspace_ids, v_expires;
END
$$;
REVOKE ALL ON FUNCTION cortex_rotate_api_token(uuid,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION cortex_rotate_api_token(uuid,text,text) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION cortex_rotate_api_token(uuid,text,text) TO cortex_app;

-- ---------------------------------------------------------------------------
-- Invalidators: token revocation (token-ID writer). Same signature,
-- idempotent transition semantics, audit, and EXECUTE matrix as migration
-- 106, with the lock-free key resolve, the exclusive gate, and the locked
-- re-read in front of the mutation.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION cortex_revoke_api_token(p_token_public_id uuid, p_reason text)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_tenant uuid;
    v_caller uuid;
    v_subject uuid;
    v_revoked timestamptz;
    v_metadata jsonb;
BEGIN
    SELECT d.tenant_id, d.caller_public_id INTO v_tenant, v_caller
      FROM public.cortex_actor_admin_caller() d;
    IF p_token_public_id IS NULL THEN
        RAISE EXCEPTION 'token is required' USING ERRCODE = '22023';
    END IF;
    -- Lock-free resolve: the token's subject is read WITHOUT any row lock,
    -- ONLY to derive the canonical key.
    SELECT COALESCE(u.public_id, s.public_id)
      INTO v_subject
      FROM public.api_tokens t
      LEFT JOIN public.app_users u
        ON u.tenant_id = t.tenant_id AND u.id = t.subject_user_id
      LEFT JOIN public.service_accounts s
        ON s.tenant_id = t.tenant_id AND s.id = t.subject_service_account_id
     WHERE t.tenant_id = v_tenant
       AND t.public_id = p_token_public_id;
    IF v_subject IS NULL THEN
        RAISE EXCEPTION 'token does not exist in tenant' USING ERRCODE = '23503';
    END IF;
    -- Canonical exclusive actor gate BEFORE any identity row lock.
    PERFORM pg_advisory_xact_lock(public.cortex_principal_key(v_tenant, v_subject));
    -- Locked re-read under the gate, then the idempotent transition.
    SELECT t.revoked_at INTO v_revoked
      FROM public.api_tokens t
     WHERE t.tenant_id = v_tenant
       AND t.public_id = p_token_public_id
       FOR UPDATE OF t;
    UPDATE public.api_tokens
       SET revoked_at = COALESCE(revoked_at, clock_timestamp()),
           updated_at = now()
     WHERE tenant_id = v_tenant
       AND public_id = p_token_public_id
       AND revoked_at IS NULL;
    IF NOT FOUND THEN
        IF v_revoked IS NULL THEN
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

-- ---------------------------------------------------------------------------
-- Invalidators: the bootstrap reconciler. Identical body to migration 106
-- (argument/grant validation, in-SQL digest derivation, the four lifecycle
-- actions, audit evidence) except the serialization advisory now uses the
-- ONE canonical principal key namespace, so bootstrap/grant
-- re-provisioning drains in-flight verify/bind readers exactly like every
-- other identity invalidator.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION public.cortex_bootstrap_service_principal(p_tenant_id uuid, p_workspace_public_id uuid, p_actor_public_id uuid, p_subject text, p_service_name text, p_grants jsonb, p_token_name text, p_token_secret text, p_reason text)
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
    -- Canonical exclusive gate BEFORE any identity row lock: bootstrap and
    -- grant re-provisioning share ONE principal advisory namespace with
    -- every reader and invalidator (drains in-flight readers, excludes new
    -- arrivals, and invalidates stale bindings through the grant_version
    -- fold).
    PERFORM pg_advisory_xact_lock(public.cortex_principal_key(p_tenant_id, p_actor_public_id));
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
       AND (s.public_id = p_actor_public_id OR s.name = v_service_name)
     ORDER BY (s.public_id = p_actor_public_id) DESC
     LIMIT 1
       FOR UPDATE;
    IF v_service_id IS NULL THEN
        INSERT INTO public.service_accounts (tenant_id, public_id, name)
        VALUES (p_tenant_id, p_actor_public_id, v_service_name)
        RETURNING id INTO v_service_id;
    ELSE
        UPDATE public.service_accounts
           SET public_id = p_actor_public_id, name = v_service_name, active = true
         WHERE id = v_service_id;
    END IF;
    SELECT a.grant_version, a.grant_digest, a.active, a.revoked_at, a.actor_type, a.subject
      INTO v_version, v_stored_digest, v_active, v_revoked, v_actor_type, v_subject_row
      FROM public.actor_subjects a
     WHERE a.tenant_id = p_tenant_id
       AND (a.public_id = p_actor_public_id OR a.subject = v_subject)
     ORDER BY (a.public_id = p_actor_public_id) DESC
     LIMIT 1
       FOR UPDATE;
    IF v_version IS NULL THEN
        INSERT INTO public.actor_subjects
            (tenant_id, subject, actor_type, public_id, active, revoked_at, grant_version, grant_digest)
        VALUES
            (p_tenant_id, v_subject, 'service_account', p_actor_public_id, true, NULL, 1, v_digest);
        v_version := 1;
        v_fresh := true;
    ELSE
        UPDATE public.actor_subjects
           SET actor_type = 'service_account',
               public_id = p_actor_public_id,
               subject = v_subject,
               active = true,
               revoked_at = NULL
         WHERE tenant_id = p_tenant_id
           AND (public_id = p_actor_public_id OR subject = v_subject);
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
-- Ownership and the EXECUTE matrix are reasserted exactly as in migration
-- 106: the definer context stays deterministic and the reconciler stays
-- executable only by the migration role.
ALTER FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) OWNER TO cortex_migration;
REVOKE ALL ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) FROM cortex_app;
REVOKE ALL ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) FROM cortex_admin;
GRANT EXECUTE ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) TO cortex_migration;

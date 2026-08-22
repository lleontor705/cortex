-- T01 principal-lock spike: CANONICAL SRW protocol fixture (pinned).
--
-- This file is the single normative artifact for migration 108's locking
-- protocol. The Go test principal_lock_spike_integration_test.go pins its
-- SHA-256, parses the advisory order out of each routine body, and executes
-- every statement against a fresh isolated migration-head database.
--
-- Canonical decisions recorded here (a written PASS selects these):
--   1. ONE canonical 64-bit advisory key namespace for principal identity:
--        hashtextextended('cortex:principal:' || tenant || ':' || actor, 0)
--      Readers and writers derive the SAME key for the same (tenant, actor);
--      the domain prefix separates the namespace from every other advisory
--      user (migrations, bootstrap locks).
--   2. Readers (verify/bind) resolve token/actor lock-free, then take the
--      transaction-scoped SHARED advisory lock, then re-read rows FOR SHARE.
--   3. Every identity invalidator (direct actor revoke, token revoke,
--      rotate, bootstrap/grant re-provision) takes the transaction-scoped
--      EXCLUSIVE advisory lock BEFORE any row lock, in ascending key order
--      when more than one actor is touched.
--   4. Only pg_advisory_xact_lock* calls exist here: transaction-scoped
--      only, safe under transaction-mode poolers; no session advisory
--      locks, no SET-based session state, no backend affinity.
-- @statement
CREATE OR REPLACE FUNCTION spike_principal_key(p_tenant uuid, p_actor uuid)
RETURNS bigint LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $fn$
    SELECT hashtextextended('cortex:principal:' || p_tenant::text || ':' || p_actor::text, 0)
$fn$;
-- @statement
CREATE OR REPLACE FUNCTION spike_srw_bind(p_actor uuid, p_provenance text, p_grant_version bigint)
RETURNS uuid LANGUAGE plpgsql AS $fn$
DECLARE
    v_tenant uuid; v_version bigint; v_token uuid; v_mac text;
    v_token_tenant uuid; v_token_digest bytea; v_token_revoked timestamptz; v_token_expires timestamptz;
    v_subject uuid; v_subject_active boolean; v_expected text;
BEGIN
    IF p_actor IS NULL OR p_provenance IS NULL OR NULLIF(p_provenance, '') IS NULL
       OR p_grant_version IS NULL OR p_grant_version <= 0 THEN
        RAISE EXCEPTION 'principal binding is required' USING ERRCODE = '28000';
    END IF;
    IF p_provenance !~ '^v1:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:[0-9a-f]{64}$' THEN
        RAISE EXCEPTION 'principal binding requires token-bound provenance' USING ERRCODE = '28000';
    END IF;
    -- Lock-free resolve (verify path): no row lock before the gate.
    SELECT tenant_id INTO v_tenant FROM actor_subjects
     WHERE public_id = p_actor AND active AND revoked_at IS NULL;
    IF v_tenant IS NULL THEN
        RAISE EXCEPTION 'principal grant is revoked or stale' USING ERRCODE = '28000';
    END IF;
    -- Canonical shared gate BEFORE any row lock.
    PERFORM pg_advisory_xact_lock_shared(spike_principal_key(v_tenant, p_actor));
    -- Revalidation under the gate.
    SELECT grant_version INTO v_version FROM actor_subjects
     WHERE tenant_id = v_tenant AND public_id = p_actor AND active AND revoked_at IS NULL
       FOR SHARE;
    IF v_version IS DISTINCT FROM p_grant_version THEN
        RAISE EXCEPTION 'principal grant is revoked or stale' USING ERRCODE = '28000';
    END IF;
    v_token := substring(p_provenance FROM 4 FOR 36)::uuid;
    v_mac := substring(p_provenance FROM 41);
    SELECT t.tenant_id, t.token_digest, t.revoked_at, t.expires_at,
           COALESCE(u.public_id, s.public_id), COALESCE(u.active, s.active)
      INTO v_token_tenant, v_token_digest, v_token_revoked, v_token_expires, v_subject, v_subject_active
      FROM api_tokens t
      LEFT JOIN app_users u ON u.tenant_id = t.tenant_id AND u.id = t.subject_user_id
      LEFT JOIN service_accounts s ON s.tenant_id = t.tenant_id AND s.id = t.subject_service_account_id
     WHERE t.public_id = v_token
       FOR SHARE OF t;
    IF v_token_tenant IS NULL OR v_token_tenant <> v_tenant OR v_subject IS DISTINCT FROM p_actor
       OR v_subject_active IS NOT TRUE OR v_token_revoked IS NOT NULL
       OR (v_token_expires IS NOT NULL AND v_token_expires <= clock_timestamp()) THEN
        RAISE EXCEPTION 'principal binding proof is stale, revoked, or foreign' USING ERRCODE = '28000';
    END IF;
    v_expected := encode(public.hmac(convert_to(v_tenant::text || ':' || p_actor::text || ':' || v_version::text || ':' || v_token::text, 'UTF8'), v_token_digest, 'sha256'), 'hex');
    IF v_mac <> v_expected THEN
        RAISE EXCEPTION 'principal binding proof is invalid' USING ERRCODE = '28000';
    END IF;
    DELETE FROM cortex_tenant_context WHERE backend_pid = pg_backend_pid() AND transaction_id <> txid_current();
    INSERT INTO cortex_tenant_context (backend_pid, transaction_id, tenant_id, actor_public_id, workspace_id, project_id, scope_bound_at)
    VALUES (pg_backend_pid(), txid_current(), v_tenant, p_actor, NULL, NULL, NULL)
    ON CONFLICT (backend_pid, transaction_id) DO UPDATE
      SET tenant_id = EXCLUDED.tenant_id, actor_public_id = EXCLUDED.actor_public_id,
          workspace_id = NULL, project_id = NULL, scope_bound_at = NULL, bound_at = clock_timestamp();
    RETURN v_tenant;
END
$fn$;
-- @statement
CREATE OR REPLACE FUNCTION spike_srw_revoke_actor(p_actor uuid)
RETURNS void LANGUAGE plpgsql AS $fn$
DECLARE v_tenant uuid;
BEGIN
    -- Lock-free resolve of the canonical key inputs.
    SELECT tenant_id INTO v_tenant FROM actor_subjects WHERE public_id = p_actor;
    IF v_tenant IS NULL THEN
        RAISE EXCEPTION 'unknown actor' USING ERRCODE = '23503';
    END IF;
    -- Canonical exclusive gate BEFORE any row lock (drains in-flight
    -- shared readers; queues new arrivals behind this writer).
    PERFORM pg_advisory_xact_lock(spike_principal_key(v_tenant, p_actor));
    PERFORM 1 FROM actor_subjects WHERE tenant_id = v_tenant AND public_id = p_actor FOR UPDATE;
    UPDATE actor_subjects SET active = false, revoked_at = clock_timestamp()
     WHERE tenant_id = v_tenant AND public_id = p_actor;
END
$fn$;
-- @statement
CREATE OR REPLACE FUNCTION spike_srw_revoke_token(p_token uuid)
RETURNS void LANGUAGE plpgsql AS $fn$
DECLARE v_tenant uuid; v_actor uuid;
BEGIN
    -- Lock-free resolve of (tenant, actor) from the token's subject.
    SELECT t.tenant_id, COALESCE(u.public_id, s.public_id)
      INTO v_tenant, v_actor
      FROM api_tokens t
      LEFT JOIN app_users u ON u.tenant_id = t.tenant_id AND u.id = t.subject_user_id
      LEFT JOIN service_accounts s ON s.tenant_id = t.tenant_id AND s.id = t.subject_service_account_id
     WHERE t.public_id = p_token;
    IF v_tenant IS NULL OR v_actor IS NULL THEN
        RAISE EXCEPTION 'unknown token' USING ERRCODE = '23503';
    END IF;
    -- Canonical exclusive gate BEFORE any row lock.
    PERFORM pg_advisory_xact_lock(spike_principal_key(v_tenant, v_actor));
    PERFORM 1 FROM api_tokens WHERE tenant_id = v_tenant AND public_id = p_token FOR UPDATE;
    UPDATE api_tokens SET revoked_at = clock_timestamp(), updated_at = clock_timestamp()
     WHERE tenant_id = v_tenant AND public_id = p_token AND revoked_at IS NULL;
END
$fn$;
-- @statement
CREATE OR REPLACE FUNCTION spike_srw_rotate_token(p_token uuid, p_new_prefix text, p_new_digest bytea)
RETURNS uuid LANGUAGE plpgsql AS $fn$
DECLARE v_tenant uuid; v_actor uuid; v_new uuid;
BEGIN
    SELECT t.tenant_id, COALESCE(u.public_id, s.public_id)
      INTO v_tenant, v_actor
      FROM api_tokens t
      LEFT JOIN app_users u ON u.tenant_id = t.tenant_id AND u.id = t.subject_user_id
      LEFT JOIN service_accounts s ON s.tenant_id = t.tenant_id AND s.id = t.subject_service_account_id
     WHERE t.public_id = p_token;
    IF v_tenant IS NULL OR v_actor IS NULL THEN
        RAISE EXCEPTION 'unknown token' USING ERRCODE = '23503';
    END IF;
    -- Canonical exclusive gate BEFORE any row lock.
    PERFORM pg_advisory_xact_lock(spike_principal_key(v_tenant, v_actor));
    PERFORM 1 FROM api_tokens WHERE tenant_id = v_tenant AND public_id = p_token FOR UPDATE;
    UPDATE api_tokens SET revoked_at = clock_timestamp(), updated_at = clock_timestamp()
     WHERE tenant_id = v_tenant AND public_id = p_token;
    INSERT INTO api_tokens (tenant_id, public_id, name, token_prefix, token_digest,
                            subject_user_id, subject_service_account_id, scopes, workspace_ids)
    SELECT tenant_id, gen_random_uuid(), name, p_new_prefix, p_new_digest,
           subject_user_id, subject_service_account_id, scopes, workspace_ids
      FROM api_tokens WHERE tenant_id = v_tenant AND public_id = p_token
    RETURNING public_id INTO v_new;
    RETURN v_new;
END
$fn$;
-- @statement
CREATE OR REPLACE FUNCTION spike_srw_bootstrap_actor(p_actor uuid, p_grant_digest text)
RETURNS bigint LANGUAGE plpgsql AS $fn$
DECLARE v_tenant uuid; v_version bigint;
BEGIN
    -- Lock-free resolve of the canonical key inputs.
    SELECT tenant_id INTO v_tenant FROM actor_subjects WHERE public_id = p_actor;
    IF v_tenant IS NULL THEN
        RAISE EXCEPTION 'unknown actor' USING ERRCODE = '23503';
    END IF;
    -- Canonical exclusive gate BEFORE any row lock; re-provisioning the
    -- actor's grants invalidates every in-flight or stale binding.
    PERFORM pg_advisory_xact_lock(spike_principal_key(v_tenant, p_actor));
    SELECT grant_version INTO v_version FROM actor_subjects
     WHERE tenant_id = v_tenant AND public_id = p_actor FOR UPDATE;
    UPDATE actor_subjects
       SET grant_version = grant_version + 1, grant_digest = p_grant_digest,
           active = true, revoked_at = NULL
     WHERE tenant_id = v_tenant AND public_id = p_actor
    RETURNING grant_version INTO v_version;
    RETURN v_version;
END
$fn$;

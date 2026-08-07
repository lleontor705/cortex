-- Cortex v2 server identity and heterogeneous graph expansion.
-- Forward-only and additive: migration 100 is immutable once applied.

ALTER TABLE app_users ADD COLUMN IF NOT EXISTS active boolean NOT NULL DEFAULT true;
ALTER TABLE app_users ADD COLUMN IF NOT EXISTS disabled_at timestamptz;
ALTER TABLE service_accounts ADD COLUMN IF NOT EXISTS active boolean NOT NULL DEFAULT true;
ALTER TABLE service_accounts ADD COLUMN IF NOT EXISTS disabled_at timestamptz;

ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS name text NOT NULL DEFAULT '';
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS created_by uuid;

DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'actor_subjects_tenant_public_id_key') THEN
        ALTER TABLE actor_subjects ADD CONSTRAINT actor_subjects_tenant_public_id_key UNIQUE (tenant_id, public_id);
    END IF;
END $$;

INSERT INTO actor_subjects(public_id, tenant_id, subject, actor_type, grant_version, grant_digest)
SELECT public_id, tenant_id, public_id::text, 'user', 1, '' FROM app_users
ON CONFLICT DO NOTHING;

INSERT INTO actor_subjects(public_id, tenant_id, subject, actor_type, grant_version, grant_digest)
SELECT public_id, tenant_id, public_id::text, 'service_account', 1, '' FROM service_accounts
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS principal_grants (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id uuid NOT NULL,
    actor_public_id uuid NOT NULL,
    grant_type text NOT NULL,
    grant_value text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid,
    updated_by uuid,
    UNIQUE (tenant_id, actor_public_id, grant_type, grant_value),
    FOREIGN KEY (tenant_id, actor_public_id) REFERENCES actor_subjects(tenant_id, public_id),
    CHECK (grant_type IN ('role', 'workspace', 'project', 'classification', 'scope')),
    CHECK (grant_value <> '')
);

ALTER TABLE edges ADD COLUMN IF NOT EXISTS weight double precision NOT NULL DEFAULT 1.0;
ALTER TABLE edges ADD COLUMN IF NOT EXISTS confidence double precision NOT NULL DEFAULT 1.0;
ALTER TABLE edges ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT 'manual';
ALTER TABLE edges ADD COLUMN IF NOT EXISTS reasoning text NOT NULL DEFAULT '';
ALTER TABLE edges ADD COLUMN IF NOT EXISTS invalid_at timestamptz;
ALTER TABLE edges ADD COLUMN IF NOT EXISTS tx_from timestamptz NOT NULL DEFAULT now();
ALTER TABLE edges ADD COLUMN IF NOT EXISTS tx_until timestamptz;
ALTER TABLE edges ADD COLUMN IF NOT EXISTS assertion_kind text NOT NULL DEFAULT 'asserted';
ALTER TABLE edges ADD COLUMN IF NOT EXISTS assertion_status text NOT NULL DEFAULT 'accepted';

ALTER TABLE entities ADD COLUMN IF NOT EXISTS normalized_value text NOT NULL DEFAULT '';
ALTER TABLE entities ADD COLUMN IF NOT EXISTS provenance text NOT NULL DEFAULT 'deterministic-regex';
ALTER TABLE observation_entities ADD COLUMN IF NOT EXISTS confidence double precision NOT NULL DEFAULT 1.0;

DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'edges_weight_range') THEN
        ALTER TABLE edges ADD CONSTRAINT edges_weight_range CHECK (weight >= 0 AND weight <= 10);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'edges_confidence_range') THEN
        ALTER TABLE edges ADD CONSTRAINT edges_confidence_range CHECK (confidence >= 0 AND confidence <= 1);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'observation_entities_confidence_range') THEN
        ALTER TABLE observation_entities ADD CONSTRAINT observation_entities_confidence_range CHECK (confidence >= 0 AND confidence <= 1);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'edges_assertion_kind') THEN
        ALTER TABLE edges ADD CONSTRAINT edges_assertion_kind CHECK (assertion_kind IN ('asserted', 'deterministic', 'suggested', 'derived', 'imported'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'edges_assertion_status') THEN
        ALTER TABLE edges ADD CONSTRAINT edges_assertion_status CHECK (assertion_status IN ('proposed', 'accepted', 'rejected', 'deprecated', 'superseded'));
    END IF;
END $$;

UPDATE entities
   SET normalized_value = entity_type || ':' || lower(trim(entity_key))
 WHERE normalized_value = '';

CREATE INDEX IF NOT EXISTS principal_grants_actor_idx ON principal_grants(tenant_id, actor_public_id, grant_type);
CREATE INDEX IF NOT EXISTS edges_from_idx ON edges(tenant_id, from_observation_id);
CREATE INDEX IF NOT EXISTS edges_to_idx ON edges(tenant_id, to_observation_id);
CREATE INDEX IF NOT EXISTS edges_relation_idx ON edges(tenant_id, relation_type);
CREATE INDEX IF NOT EXISTS edges_current_idx ON edges(tenant_id, relation_type, created_at DESC)
    WHERE valid_until IS NULL AND invalid_at IS NULL AND assertion_status = 'accepted';
CREATE INDEX IF NOT EXISTS entities_normalized_idx ON entities(tenant_id, normalized_value);

ALTER TABLE principal_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE principal_grants FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS cortex_tenant_isolation ON principal_grants;
CREATE POLICY cortex_tenant_isolation ON principal_grants AS PERMISSIVE FOR ALL TO PUBLIC
    USING (tenant_id = public.cortex_current_tenant())
    WITH CHECK (tenant_id = public.cortex_current_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON principal_grants TO cortex_app;
GRANT SELECT ON principal_grants TO cortex_admin;
GRANT ALL ON principal_grants TO cortex_migration;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO cortex_app, cortex_migration;

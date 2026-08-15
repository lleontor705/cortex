-- Cortex v2 server wave: PostgreSQL migration 104 (forward-only, additive).
-- Durable handoff receipt ledger for exactly-once handoffs (REM-MIG-001).
-- Applied transactionally by internal/migration.PostgresServerMigration and
-- ledgered in cortex_server_migrations with the embedded SHA-256 checksum.
-- Down is not supported for applied migrations: the line is forward-only and
-- fail-closed. The table is created WITHOUT IF NOT EXISTS on purpose so a
-- stale, unledgered artifact fails closed instead of being silently adopted.

CREATE TABLE handoff_receipts (
    tenant_id uuid NOT NULL,
    scope text NOT NULL,
    key text NOT NULL,
    payload_hash bytea NOT NULL CHECK (length(payload_hash) = 32),
    canonical_payload bytea NOT NULL,
    state text NOT NULL CHECK (state IN ('pending', 'committed')),
    observation_id bigint,
    initial_status text CHECK (initial_status IS NULL OR initial_status IN ('created', 'replayed', 'updated')),
    created_at timestamptz NOT NULL DEFAULT now(),
    committed_at timestamptz,
    PRIMARY KEY (tenant_id, scope, key),
    FOREIGN KEY (tenant_id, observation_id) REFERENCES observations(tenant_id, id) ON DELETE RESTRICT,
    CHECK (
        (state = 'pending'
            AND observation_id IS NULL
            AND initial_status IS NULL
            AND committed_at IS NULL)
        OR
        (state = 'committed'
            AND observation_id IS NOT NULL
            AND initial_status IS NOT NULL
            AND committed_at IS NOT NULL)
    )
);

CREATE INDEX handoff_receipts_observation
    ON handoff_receipts(tenant_id, observation_id);

ALTER TABLE handoff_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE handoff_receipts FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS cortex_tenant_isolation ON handoff_receipts;
CREATE POLICY cortex_tenant_isolation ON handoff_receipts AS PERMISSIVE FOR ALL TO PUBLIC
    USING (tenant_id = public.cortex_current_tenant())
    WITH CHECK (tenant_id = public.cortex_current_tenant());

REVOKE ALL ON handoff_receipts FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON handoff_receipts TO cortex_app;
GRANT SELECT ON handoff_receipts TO cortex_admin;
GRANT ALL ON handoff_receipts TO cortex_migration;

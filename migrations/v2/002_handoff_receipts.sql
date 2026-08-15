-- Immutable SQLite handoff receipt ledger (REQ-MIGRATION-001).
-- Applied transactionally by internal/migration.V2MigrationRunner.
CREATE TABLE handoff_receipts (
    scope             TEXT NOT NULL,
    key               TEXT NOT NULL,
    payload_hash      BLOB NOT NULL CHECK (length(payload_hash) = 32),
    canonical_payload BLOB NOT NULL,
    state             TEXT NOT NULL CHECK (state IN ('pending', 'committed')),
    observation_id    INTEGER,
    initial_status    TEXT CHECK (initial_status IN ('created', 'replayed', 'updated')),
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    committed_at      TEXT,
    PRIMARY KEY (scope, key),
    FOREIGN KEY (observation_id) REFERENCES observations(id) ON DELETE RESTRICT,
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

CREATE INDEX idx_handoff_receipts_observation
    ON handoff_receipts(observation_id);

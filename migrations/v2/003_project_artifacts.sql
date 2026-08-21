-- Immutable SQLite project context artifacts migration (003, forward-only).
-- Project Context Protocol storage (REQ-RET-*, REQ-LIMIT-*): artifacts with
-- immutable revisions and events, exactly one activation pointer per
-- artifact guarded by a monotonic activation_revision CAS token (the pointer
-- is authoritative and the artifact mirror is synced to it, so the two can
-- never drift), durable idempotency receipts that store the exact immutable
-- result revision of the SAME coordinate's artifact for exact replay, and
-- transactional storage-usage quota counters keyed by scope coordinates
-- (local mode has no tenant/workspace, so a workspace default is represented
-- by an absent project). Applied transactionally by
-- internal/migration.V2Baseline follow-up line with its own SHA-256
-- checksum recorded in the additive cortex_v2_migrations ledger; the
-- 001/002 identity is unchanged. The line is forward-only: no down path,
-- no purge, no expiry, and no cascade anywhere. Retention is indefinite:
-- soft delete is a state transition that records deleted_at/deleted_by/
-- delete_reason and MUST NOT remove or anonymize revisions, events,
-- activations, idempotency evidence, or usage counters; committed
-- receipts, activation pointers, and counter rows are append/fold-only
-- and cannot be reset or removed after commit, and quota counter
-- coordinates are frozen at insert. Semantic
-- content/metadata limits (1MiB/64KiB) are enforced by the domain limits
-- package, not by column constraints. Like 002, no IF NOT EXISTS is used
-- so a stale, unledgered artifact fails closed instead of being adopted.

CREATE TABLE project_artifacts (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    public_id           TEXT NOT NULL UNIQUE,
    project             TEXT,
    kind                TEXT NOT NULL CHECK (kind IN ('skill', 'rule')),
    key                 TEXT NOT NULL,
    source_scope        TEXT NOT NULL CHECK (source_scope IN ('project', 'workspace_default')),
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deleted')),
    current_revision    INTEGER NOT NULL CHECK (current_revision >= 1),
    activation_revision INTEGER NOT NULL DEFAULT 0 CHECK (activation_revision >= 0),
    content_bytes       INTEGER NOT NULL CHECK (content_bytes >= 0),
    metadata_bytes      INTEGER NOT NULL CHECK (metadata_bytes >= 0),
    digest              TEXT NOT NULL CHECK (length(digest) = 64),
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at          TEXT NOT NULL DEFAULT (datetime('now')),
    deleted_at          TEXT,
    deleted_by          TEXT,
    delete_reason       TEXT,
    CHECK (
        (source_scope = 'project' AND project IS NOT NULL)
        OR (source_scope = 'workspace_default' AND project IS NULL)
    ),
    CHECK (
        (status = 'active'
            AND deleted_at IS NULL
            AND deleted_by IS NULL
            AND delete_reason IS NULL)
        OR
        (status = 'deleted'
            AND deleted_at IS NOT NULL
            AND deleted_by IS NOT NULL
            AND delete_reason IS NOT NULL)
    )
);

-- Active resolution uniqueness: exactly one active artifact per coordinate.
-- A project artifact is unique per (project, kind, key); a workspace default
-- (absent project) is unique per (kind, key) across the local workspace.
-- Soft-deleted history may coexist under the same coordinates so a deleted
-- artifact can be re-created under the same coordinates.
CREATE UNIQUE INDEX idx_project_artifacts_project_active_key
    ON project_artifacts(project, kind, key)
    WHERE source_scope = 'project' AND status = 'active';
CREATE UNIQUE INDEX idx_project_artifacts_workspace_default_active_key
    ON project_artifacts(kind, key)
    WHERE source_scope = 'workspace_default' AND status = 'active';

-- Stable list ordering (updated_at DESC, id) for the default (active-only)
-- and include_deleted variants of both scopes.
CREATE INDEX idx_project_artifacts_project_active_list
    ON project_artifacts(project, updated_at DESC, id)
    WHERE source_scope = 'project' AND status = 'active';
CREATE INDEX idx_project_artifacts_project_list_all
    ON project_artifacts(project, updated_at DESC, id)
    WHERE source_scope = 'project';
CREATE INDEX idx_project_artifacts_workspace_default_active_list
    ON project_artifacts(kind, updated_at DESC, id)
    WHERE source_scope = 'workspace_default' AND status = 'active';
CREATE INDEX idx_project_artifacts_workspace_default_list_all
    ON project_artifacts(kind, updated_at DESC, id)
    WHERE source_scope = 'workspace_default';

-- Activation CAS token on the artifact itself: the pointer row is the
-- AUTHORITATIVE token. The sync trigger below copies pointer moves onto the
-- artifact mirror, and the guards here reject any mirror regression and any
-- mirror value that is not the pointer's current token, so the two columns
-- can never drift.
CREATE TRIGGER project_artifacts_activation_revision_monotonic
BEFORE UPDATE OF activation_revision ON project_artifacts
WHEN (NEW.activation_revision < OLD.activation_revision)
BEGIN
    SELECT RAISE(ABORT, 'project_artifacts.activation_revision is monotonic');
END;

CREATE TRIGGER project_artifacts_activation_mirror_authoritative
BEFORE UPDATE OF activation_revision ON project_artifacts
WHEN NOT EXISTS (
    SELECT 1 FROM project_artifact_activations x
     WHERE x.artifact_id = NEW.id AND x.activation_revision = NEW.activation_revision
)
BEGIN
    SELECT RAISE(ABORT, 'project_artifacts.activation_revision must equal the activation pointer token');
END;

-- Immutable revision history: append-only rows, never updated or deleted.
CREATE TABLE project_artifact_revisions (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    public_id      TEXT NOT NULL UNIQUE,
    artifact_id    INTEGER NOT NULL,
    revision       INTEGER NOT NULL CHECK (revision >= 1),
    content        TEXT NOT NULL,
    content_bytes  INTEGER NOT NULL CHECK (content_bytes >= 0),
    metadata       TEXT NOT NULL,
    metadata_bytes INTEGER NOT NULL CHECK (metadata_bytes >= 0),
    digest         TEXT NOT NULL CHECK (length(digest) = 64),
    created_at     TEXT NOT NULL DEFAULT (datetime('now')),
    created_by     TEXT NOT NULL,
    UNIQUE (artifact_id, revision),
    FOREIGN KEY (artifact_id) REFERENCES project_artifacts(id) ON DELETE RESTRICT
);

CREATE INDEX idx_project_artifact_revisions
    ON project_artifact_revisions(artifact_id, revision DESC);

-- SQLite triggers accept a single event each, so immutability is enforced by
-- a pair of fail-closed guards per history table (no UPDATE, no DELETE).
CREATE TRIGGER project_artifact_revisions_no_update
BEFORE UPDATE ON project_artifact_revisions
BEGIN
    SELECT RAISE(ABORT, 'project_artifact_revisions are immutable');
END;

CREATE TRIGGER project_artifact_revisions_no_delete
BEFORE DELETE ON project_artifact_revisions
BEGIN
    SELECT RAISE(ABORT, 'project_artifact_revisions are immutable');
END;

-- Immutable event history: append-only lifecycle evidence per artifact,
-- including the activation CAS token produced by activated/deactivated
-- transitions.
CREATE TABLE project_artifact_events (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    artifact_id         INTEGER NOT NULL,
    event_type          TEXT NOT NULL CHECK (event_type IN (
                            'created', 'revised', 'activated', 'deactivated',
                            'deleted', 'restored'
                        )),
    revision            INTEGER CHECK (revision IS NULL OR revision >= 1),
    activation_revision INTEGER CHECK (activation_revision IS NULL OR activation_revision >= 1),
    actor               TEXT NOT NULL,
    payload             TEXT,
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (artifact_id) REFERENCES project_artifacts(id) ON DELETE RESTRICT
);

CREATE INDEX idx_project_artifact_events
    ON project_artifact_events(artifact_id, created_at DESC, id);

CREATE TRIGGER project_artifact_events_no_update
BEFORE UPDATE ON project_artifact_events
BEGIN
    SELECT RAISE(ABORT, 'project_artifact_events are immutable');
END;

CREATE TRIGGER project_artifact_events_no_delete
BEFORE DELETE ON project_artifact_events
BEGIN
    SELECT RAISE(ABORT, 'project_artifact_events are immutable');
END;

-- Exactly one activation pointer per artifact (PRIMARY KEY on artifact_id);
-- the pointer must reference an existing revision of that same artifact and
-- carries the current activation_revision CAS token (>= 1 after the first
-- activation). Moving the pointer requires a strictly greater token and the
-- token never regresses, so concurrent activations fail closed instead of
-- lost-updating each other. The pointer is retained evidence: it is never
-- removed.
CREATE TABLE project_artifact_activations (
    artifact_id         INTEGER PRIMARY KEY,
    revision            INTEGER NOT NULL CHECK (revision >= 1),
    activation_revision INTEGER NOT NULL CHECK (activation_revision >= 1),
    activated_at        TEXT NOT NULL DEFAULT (datetime('now')),
    activated_by        TEXT NOT NULL,
    FOREIGN KEY (artifact_id) REFERENCES project_artifacts(id) ON DELETE RESTRICT,
    FOREIGN KEY (artifact_id, revision) REFERENCES project_artifact_revisions(artifact_id, revision) ON DELETE RESTRICT
);

CREATE TRIGGER project_artifact_activations_cas_guard
BEFORE UPDATE ON project_artifact_activations
WHEN (
    NEW.activation_revision < OLD.activation_revision
    OR (
        NEW.revision IS NOT OLD.revision
        AND NEW.activation_revision <= OLD.activation_revision
    )
)
BEGIN
    SELECT RAISE(ABORT, 'activation_revision must increase to move the activation pointer');
END;

-- The pointer is authoritative: creating or moving it syncs the artifact
-- mirror to the pointer's token in the same statement.
CREATE TRIGGER project_artifact_activations_sync_mirror_on_insert
AFTER INSERT ON project_artifact_activations
BEGIN
    UPDATE project_artifacts
       SET activation_revision = NEW.activation_revision
     WHERE id = NEW.artifact_id
       AND activation_revision <> NEW.activation_revision;
END;

CREATE TRIGGER project_artifact_activations_sync_mirror_on_update
AFTER UPDATE ON project_artifact_activations
BEGIN
    UPDATE project_artifacts
       SET activation_revision = NEW.activation_revision
     WHERE id = NEW.artifact_id
       AND activation_revision <> NEW.activation_revision;
END;

CREATE TRIGGER project_artifact_activations_no_delete
BEFORE DELETE ON project_artifact_activations
BEGIN
    SELECT RAISE(ABORT, 'project_artifact_activations are retained indefinitely');
END;

-- Durable idempotency receipts for artifact mutations: a pending claim
-- reserves the (project, scope, idem_key) coordinate namespace durably
-- (absent project for workspace defaults); the committed state records the
-- resulting artifact, initial status, and the EXACT result revision of that
-- SAME coordinate's artifact exactly once, so a replay returns the original
-- revision and the result can never reference another coordinate's
-- artifact or a revision that does not exist. The only legal transition is
-- pending -> committed with the namespace, payload hash, and creation time
-- frozen; everything else (including any later mutation or removal of a
-- committed receipt) fails closed.
CREATE TABLE project_artifact_idempotency (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    project          TEXT,
    scope            TEXT NOT NULL,
    idem_key         TEXT NOT NULL,
    payload_hash     BLOB NOT NULL CHECK (length(payload_hash) = 32),
    state            TEXT NOT NULL CHECK (state IN ('pending', 'committed')),
    artifact_id      INTEGER,
    initial_status   TEXT CHECK (initial_status IS NULL OR initial_status IN ('created', 'replayed', 'updated')),
    result_revision  INTEGER,
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    committed_at     TEXT,
    FOREIGN KEY (artifact_id) REFERENCES project_artifacts(id) ON DELETE RESTRICT,
    FOREIGN KEY (artifact_id, result_revision) REFERENCES project_artifact_revisions(artifact_id, revision) ON DELETE RESTRICT,
    CHECK (
        (state = 'pending'
            AND artifact_id IS NULL
            AND initial_status IS NULL
            AND result_revision IS NULL
            AND committed_at IS NULL)
        OR
        (state = 'committed'
            AND artifact_id IS NOT NULL
            AND initial_status IS NOT NULL
            AND result_revision IS NOT NULL
            AND result_revision >= 1
            AND committed_at IS NOT NULL)
    )
);

-- One namespace per coordinate: (project, scope, idem_key) for project
-- receipts and (scope, idem_key) for workspace-default receipts (absent
-- project).
CREATE UNIQUE INDEX idx_project_artifact_idempotency_project
    ON project_artifact_idempotency(project, scope, idem_key)
    WHERE project IS NOT NULL;
CREATE UNIQUE INDEX idx_project_artifact_idempotency_workspace_default
    ON project_artifact_idempotency(scope, idem_key)
    WHERE project IS NULL;

CREATE INDEX idx_project_artifact_idempotency_artifact
    ON project_artifact_idempotency(artifact_id);

CREATE TRIGGER project_artifact_idempotency_commit_guard
BEFORE UPDATE ON project_artifact_idempotency
WHEN NOT (
    OLD.state = 'pending'
    AND NEW.state = 'committed'
    AND NEW.project IS OLD.project
    AND NEW.scope = OLD.scope
    AND NEW.idem_key = OLD.idem_key
    AND NEW.payload_hash = OLD.payload_hash
    AND NEW.created_at = OLD.created_at
    AND EXISTS (
        SELECT 1 FROM project_artifact_revisions r
         WHERE r.artifact_id = NEW.artifact_id
           AND r.revision = NEW.result_revision
    )
    AND EXISTS (
        SELECT 1 FROM project_artifacts a
         WHERE a.id = NEW.artifact_id
           AND a.project IS NEW.project
    )
)
BEGIN
    SELECT RAISE(ABORT, 'project_artifact_idempotency receipts commit exactly once, stay immutable, and reference the exact revision of the same coordinate');
END;

CREATE TRIGGER project_artifact_idempotency_commit_guard_on_insert
BEFORE INSERT ON project_artifact_idempotency
WHEN (
    NEW.state = 'committed'
    AND NOT (
        EXISTS (
            SELECT 1 FROM project_artifact_revisions r
             WHERE r.artifact_id = NEW.artifact_id
               AND r.revision = NEW.result_revision
        )
        AND EXISTS (
            SELECT 1 FROM project_artifacts a
             WHERE a.id = NEW.artifact_id
               AND a.project IS NEW.project
        )
    )
)
BEGIN
    SELECT RAISE(ABORT, 'committed receipts must reference the exact revision of the same coordinate');
END;

CREATE TRIGGER project_artifact_idempotency_no_delete
BEFORE DELETE ON project_artifact_idempotency
BEGIN
    SELECT RAISE(ABORT, 'project_artifact_idempotency evidence is retained indefinitely');
END;

-- Transactional storage-usage quota counters per scope coordinate: one row
-- per project plus one row for the local workspace defaults (absent
-- project). Writers fold the counters in the SAME transaction as the
-- revision/event insert and check the operator-configured quota before
-- write; over-quota fails closed with zero effects and never triggers a
-- purge. Retention is indefinite, so history never leaves the ledger and
-- the counters are monotonic: they never decrease, and rows are never
-- removed. The (source_scope, project) quota coordinate is frozen at
-- insert, so accumulated usage can never be relocated to another
-- coordinate. Rebuild/verify diagnostics are read-only; there is no
-- counter reset path.
CREATE TABLE project_storage_usage (
    source_scope   TEXT NOT NULL CHECK (source_scope IN ('project', 'workspace_default')),
    project        TEXT,
    content_bytes  INTEGER NOT NULL DEFAULT 0 CHECK (content_bytes >= 0),
    metadata_bytes INTEGER NOT NULL DEFAULT 0 CHECK (metadata_bytes >= 0),
    event_bytes    INTEGER NOT NULL DEFAULT 0 CHECK (event_bytes >= 0),
    updated_at     TEXT NOT NULL DEFAULT (datetime('now')),
    CHECK (
        (source_scope = 'project' AND project IS NOT NULL)
        OR (source_scope = 'workspace_default' AND project IS NULL)
    )
);

CREATE UNIQUE INDEX idx_project_storage_usage_project
    ON project_storage_usage(project)
    WHERE source_scope = 'project';
CREATE UNIQUE INDEX idx_project_storage_usage_workspace_default
    ON project_storage_usage(source_scope)
    WHERE source_scope = 'workspace_default';

-- Quota coordinates are frozen at insert (REQ-QUOTA-001): a usage row may
-- never move to another project or to/from the workspace-default
-- coordinate, so accumulated quota can never be relocated away or reset by
-- a move-then-reinsert (the row cannot be removed either, and the occupied
-- coordinate rejects a second row via the unique indexes above). SQLite
-- fires same-event triggers in an unspecified order, so the coordinate
-- freeze and the monotonic counter validation share ONE BEFORE UPDATE
-- guard whose body runs the coordinate check first: a competing update
-- that both moves the coordinate and decreases counters deterministically
-- reports the coordinate violation, never the counter violation. Legal
-- usage updates — updated_at refreshes, nondecreasing counter folds, and
-- same-value coordinate rewrites — stay untouched.
CREATE TRIGGER project_storage_usage_update_guard
BEFORE UPDATE ON project_storage_usage
WHEN (
    NEW.source_scope IS NOT OLD.source_scope
    OR NEW.project IS NOT OLD.project
    OR NEW.content_bytes < OLD.content_bytes
    OR NEW.metadata_bytes < OLD.metadata_bytes
    OR NEW.event_bytes < OLD.event_bytes
)
BEGIN
    SELECT RAISE(ABORT, 'project_storage_usage coordinates are immutable after insert')
     WHERE NEW.source_scope IS NOT OLD.source_scope
        OR NEW.project IS NOT OLD.project;
    SELECT RAISE(ABORT, 'project_storage_usage counters never decrease')
     WHERE NEW.content_bytes < OLD.content_bytes
        OR NEW.metadata_bytes < OLD.metadata_bytes
        OR NEW.event_bytes < OLD.event_bytes;
END;

CREATE TRIGGER project_storage_usage_no_delete
BEFORE DELETE ON project_storage_usage
BEGIN
    SELECT RAISE(ABORT, 'project_storage_usage counters are retained and cannot be reset');
END;

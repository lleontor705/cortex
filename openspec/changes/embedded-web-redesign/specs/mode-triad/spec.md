# Delta for mode-triad

## ADDED Requirements

### Requirement: REQ-MODE-001: formal mode triad composition
The triad local/server/hybrid MUST be explicit: hybrid keeps its current meaning — local SQLite composition plus remote sync/replication — formalized with a named composition path, parse validation, and guards documented in `internal/platform/mode.go` and `internal/app`; no new mode semantics are introduced.

#### Scenario: hybrid boots SQLite plus sync (Happy path)
- GIVEN `--mode hybrid` with sync configured
- WHEN the app opens
- THEN the local SQLite composition starts and the replication loop runs, exactly as today's CORTEX_MODE=hybrid path

#### Scenario: hybrid without sync config (Edge case)
- GIVEN `--mode hybrid` but sync disabled/unconfigured
- WHEN startup runs
- THEN it degrades to local behavior with an explicit warning naming the missing sync configuration

#### Scenario: unknown mode rejected (Error state)
- GIVEN `--mode legacy`
- WHEN ParseMode runs
- THEN startup fails closed listing the three valid modes

### Requirement: REQ-MODE-002: startup and status reporting
Both serve paths MUST print and expose a status summary naming the active mode, the backing store (SQLite/PostgreSQL), whether sync replication is active, and whether the embedded web UI is mounted with its surface URL.

#### Scenario: local serve banner (Happy path)
- GIVEN a normal local serve boot
- WHEN startup completes
- THEN the console shows mode=local, store=sqlite, sync state, and web mount state with URL

#### Scenario: status on non-interactive stdout (Edge case)
- GIVEN output is piped (no TTY)
- WHEN the banner is produced
- THEN it remains machine-readable single-line key=value pairs

#### Scenario: web mount failure (Error state)
- GIVEN the web key store fails to load
- WHEN serve boots
- THEN the process exits non-zero with a clear reason instead of silently serving an API-only surface

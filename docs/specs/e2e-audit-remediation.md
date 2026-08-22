# E2E Audit Remediation Specification

## Goal

Ensure that Cortex release artifacts, the web application, authenticated synchronization, and process startup are covered by deterministic gates that match production composition.

## Scope

### Phase 1: Release Gates

- Make `reindex` avoid Ollama process and model management when `search.ollama_auto_start` is disabled.
- Return successfully without contacting the embedding provider when a vector-enabled build has no observations to reindex.
- Keep the default zero-CGO build's vector-store guard unchanged.
- Make the installer's out-of-`PATH` warning non-fatal.
- Run the full `cortex_vectors` Go suite in CI and before release approval.
- Run `npm ci`, Vitest, and the production web build in CI and before release approval.

### Phase 2: Composition E2E

- Add a `postgres_integration` round trip using two real SQLite databases, authenticated HTTP, the server `AuthorizedStore`, and PostgreSQL.
- Verify unauthorized sync rejection, push/pull identity preservation, and retry idempotency.
- Add an Ubuntu process smoke test that builds the release variant, starts `cortex --mode server`, waits for a loopback TCP endpoint, checks `/health` and authenticated API behavior, then verifies clean shutdown.

## Non-Goals

- Changing the intentionally distinct CI, PR-validation, or release branch filters.
- Expanding the race-detector package list in this change.
- Adding migrations, endpoints, external vector services, Ollama, or network downloads to tests.
- Exposing raw PostgreSQL repositories from the server runtime.
- Moving web commands to the root `package.json`, whose test script intentionally fails.

## Design

### Reindex

`runReindex` keeps the provider and vector-store guards, parses the project filter, and lists observations before command-specific Ollama preparation. It prints the normal summary and returns zero immediately for an empty list. Ollama process startup and model pulls run only when the provider is `ollama`, auto-start is enabled, and work exists.

The application composition remains responsible for its existing global auto-start behavior. Tests that require no provider contact configure `ollama_auto_start: false`.

### Installer

Define `warn` next to the existing output helpers. It writes a warning without exiting. A portable Go contract test verifies that the definition precedes every use; shell syntax remains validated separately.

### CI And Release

Add independent `vector-tests` and `web-tests` jobs so they can run in parallel. The vector job runs `go test -v -count=1 -tags cortex_vectors ./...`. The web job uses Node 24 with npm cache rooted at `web/package-lock.json`, then runs all commands from `web/`.

Release approval depends on both new jobs. Existing branch filters, PostgreSQL gates, coverage threshold, baseline contracts, and race scope remain unchanged.

### Sync E2E

The phase 2 test will live under `internal/platform/server` with `postgres_integration`. It will open the real server composition, serve on loopback TCP, reject a request without a token, push from a migrated SQLite source through `RemoteSyncer`, and pull into a second migrated SQLite database. Assertions use sync IDs and stable fields rather than local numeric IDs. A second round trip proves idempotency.

### Process Smoke

The phase 2 smoke will build `cmd/cortex` with `cortex_vectors`, launch that binary against the PostgreSQL integration fixture, poll `/health` with a fixed deadline, exercise one protected API route without and with credentials, and terminate the process with bounded cleanup.

## Acceptance Criteria

1. `scripts/install.sh` defines `warn` before use and the out-of-`PATH` path no longer fails with `command not found`.
2. A vector-enabled `reindex` against an empty database returns zero without requiring Ollama.
3. Command-specific Ollama startup and model pulls require `ollama_auto_start: true`.
4. The default build still reports that the vector store is unavailable.
5. `go test -v -count=1 -tags cortex_vectors ./...` passes and is a CI and release gate.
6. `npm ci`, `npm test`, and `npm run build` pass under `web/` and are CI and release gates.
7. Release approval explicitly depends on vector and web jobs.
8. Existing branch filters and race-detector scope do not change.
9. Phase 2 proves SQLite to authenticated HTTP to PostgreSQL to SQLite synchronization without mocks at those boundaries.
10. Phase 2 proves real process startup, TCP health, API authentication, and bounded shutdown.
11. No schema, endpoint, or production authorization-boundary changes are introduced.

## Verification

```bash
go test -v -count=1 ./scripts
go test -v -count=1 ./internal/cli -run 'Test.*Reindex'
go test -v -count=1 -tags cortex_vectors ./internal/cli -run 'Test.*Reindex'
go test -v -count=1 -tags cortex_vectors ./...
go test -v -count=1 ./...
bash -n scripts/install.sh
cd web && npm ci && npm test && npm run build
golangci-lint run ./...
```

Phase 2 additionally requires the PostgreSQL 16 fixture and DSNs documented in `AGENTS.md`.

## Risks

- The full vector suite increases CI time; a separate parallel job limits critical-path impact.
- npm dependencies using `latest` can make installs drift despite the lockfile; `npm ci` preserves the committed resolution.
- TCP/process tests can become flaky; phase 2 requires dynamic loopback ports, explicit deadlines, bounded logs, and unconditional cleanup.
- PostgreSQL E2E data can collide across runs; phase 2 must use unique tenant/workspace identifiers.

## Delivery

- Phase 1 is implemented with release-aligned vector and web gates, reindex ordering, and installer validation.
- Phase 2 is implemented under `postgres_integration` in `internal/platform/server/e2e_postgres_integration_test.go`: it covers the authenticated sync round trip and Linux process smoke.
- PostgreSQL jobs serialize packages with `GOFLAGS=-p=1` because the migration rollback conformance test and E2E tests share the CI database. The rollback test restores the complete migration sequence.
- Race expansion is evaluated separately using measured duration and flake data from phase 2.

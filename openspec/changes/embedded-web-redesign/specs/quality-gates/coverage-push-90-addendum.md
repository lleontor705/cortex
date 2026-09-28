# Delta for quality-gates — Addendum: Coverage Push 90 (Wave 9)

Planner-routed decomposition of blocked `ci-coverage-gate-90` (submission f62f347d). Measured evidence: local lower-bound total 69.3% under `-coverpkg=./...` (default suite, all packages green); DSN-gated packages add only a few points in CI. The 90.0 threshold is NOT lowered and no cortex-owned product package is excluded from measurement.

> **Adjudication update (attempt 2, submission d92cda9d):** the orchestrator replaced the 90.0-literal gate with a two-tier strategy. The ACTIVE enforced threshold is 80.0 (owned by `ci-coverage-gate-80-hygiene`); 90.0 is a documented roadmap target only. REQ-QA-004 scope-integrity clauses remain fully in force. See "Orchestrator adjudication: two-tier coverage strategy" below.

## ADDED Requirements

### Requirement: REQ-QA-004: Coverage measurement scope integrity
The CI coverage job MUST enforce the active two-tier threshold of 80.0% (raised from the current 70.0) and MUST measure every cortex-owned package under `internal/...`, `cmd/...`, `bench/...`, and `testutil/...`; the ONLY permitted `-coverpkg` exclusion is vendored third-party Go code under `web/node_modules/` (today: `web/node_modules/flatted/golang/pkg/flatted`, instrumented at 0.0% because the single root `go.mod` makes `./...` match the Next.js dependency tree). The exclusion MUST be implemented as a computed package list (e.g. `go list ./... | grep -v '/node_modules/' | paste -sd, -` fed to `-coverpkg`), never as a threshold change and never as a cortex-owned package-scope reduction. The 90.0 figure MUST NOT appear as an enforced threshold anywhere in active CI; it is retained only as the documented roadmap target defined in the adjudication section below. Ownership of the ci.yml edit transfers from the superseded `ci-coverage-gate-90` to `ci-coverage-gate-80-hygiene`.

`bench/dmr` (12.1%) and `bench/longmemeval` (26.2%) were assessed for scope exclusion and REJECTED: their runners contain real offline-testable logic — `Run()` completes with synthetic fixtures in `t.TempDir()` via in-memory `common.NewBenchStores()` and nil-judge keyword scoring (F1/RougeL), plus dataset parse fallbacks. They receive test batches instead of exclusion; excluding them would be threshold gaming.

#### Scenario: vendored-only exclusion (Happy)
- GIVEN the gate job computes its coverpkg list
- WHEN `go list ./... | grep -v '/node_modules/'` is used
- THEN only third-party vendored packages drop out of measurement and the 80.0 threshold check still fails any lower total

#### Scenario: DSN-gated packages remain measured (Edge)
- GIVEN `internal/store/postgres`, `internal/migration`, `internal/platform/server`, `testutil/postgrestest` require PostgreSQL DSNs
- WHEN CI runs with `-tags postgres_integration`
- THEN these packages stay inside the measurement scope and their batches' tests count toward the total

#### Scenario: gaming attempt (Error)
- GIVEN a proposed change lowers the active threshold below 80.0, re-introduces 90.0 as an enforced value, or excludes a cortex-owned package
- WHEN review evaluates it
- THEN the change is rejected as REQ-QA-004 violation and CI remains red

## Orchestrator adjudication: two-tier coverage strategy (authoritative)

Authority: orchestrator adjudication superseding the 90.0-literal gate. `ci-coverage-gate-90` (blocked, revision 7, attempt 2, submission d92cda9d) is superseded in place by `cortex_ia_work_decompose`; no successor board is created.

### Tier 1 — Active gate (enforced now)
- Threshold: 70.0 -> **80.0** in the Global Coverage enforcement step of `.github/workflows/ci.yml`.
- Scope: `-coverpkg=./...` replaced by the computed list `go list ./... | grep -v '/node_modules/'` (78 packages measured, only vendored `web/node_modules` excluded).
- Messages: the numeric awk check and both success/failure printf messages are updated consistently to 80.0; no other CI job is touched (web Vitest coverage stays as REQ-QA-002 defines).
- Owner: `ci-coverage-gate-80-hygiene`, wired after the two prerequisite batches `go-cov-cli` and `go-cov-mcp`.

### Measured basis (attempt 2, computed coverpkg, submission d92cda9d)
Total: **75.0%** (25382/33828 statements; 78 packages; vendored web/node_modules excluded; all measured packages green).

| Pool | Baseline % | Statements | Disposition |
|---|---|---|---|
| internal/platform/server | 65.9 | 3644 | Further batches DEFERRED — registered as roadmap (Tier 2) |
| internal/store/postgres | 37.9 | 3121 | DSN-gated; CI uplift approx. +5 pts — registered as roadmap (Tier 2) |
| internal/cli | 66.3 | 2079 | Prerequisite batch `go-cov-cli` -> >= 75 |
| internal/mcp | 74.0 | 1922 | Prerequisite batch `go-cov-mcp` -> >= 80 |

### Tier 2 — Documented roadmap (NOT enforced)
- Global 90.0% requires additional waves over `internal/platform/server` and the DSN-side `internal/store/postgres` pool; both waves are registered and deferred.
- `internal/platform/server` residual (approx. 334 statements of remaining uplift) is registered as a roadmap item.
- Per-package floor intent: **>= 85% for new/security packages**. `internal/web` at 90.5% already meets the intent; `internal/webkey` at 79.2% is flagged as a roadmap item.
- No Tier 2 item may be promoted into an enforced gate, and the active threshold may never drop below 80.0, without a new orchestrator adjudication.

## Wave 9 batch plan (per-package test batches, new test files only)

| Task ID | Packages (baseline %) | Package target | Local verification |
|---|---|---|---|
| go-cov-app | internal/app (44.1) | >= 80 | go test -v -count=1 -cover ./internal/app |
| go-cov-tui | internal/tui (51.7) | >= 70 | go test -v -count=1 -cover ./internal/tui |
| go-cov-cmd | cmd/cortex (54.7) | >= 70 | go test -v -count=1 -cover ./cmd/cortex |
| go-cov-setup | internal/setup (58.1) | >= 80 | go test -v -count=1 -cover ./internal/setup |
| go-cov-lifecycle | internal/domain/lifecycle (27.0) | >= 85 | go test -v -count=1 -cover ./internal/domain/lifecycle |
| go-cov-sandbox | internal/domain/sandbox (63.9) | >= 80 | go test -v -count=1 -cover ./internal/domain/sandbox |
| go-cov-server-surface | internal/platform/server (59.4) | >= 75 | go test -v -count=1 -cover ./internal/platform/server |
| go-cov-postgres-dsn | internal/store/postgres (37.9), internal/migration, testutil/postgrestest | >= 75 in CI (DSN) | go vet -tags postgres_integration (local ceiling) |
| go-cov-bench-dmr | bench/dmr (12.1) | >= 75 | go test -v -count=1 -cover ./bench/dmr |
| go-cov-bench-lmm | bench/longmemeval (26.2) | >= 75 | go test -v -count=1 -cover ./bench/longmemeval |
| go-cov-cli (attempt 2) | internal/cli (66.3, 2079 stmts) | >= 75 | go test -v -count=1 -cover ./internal/cli |
| go-cov-mcp (attempt 2) | internal/mcp (74.0, 1922 stmts) | >= 80 | go test -v -count=1 -cover ./internal/mcp |

Rules for every batch: new modular test files only (each <= 250 LOC, task total <= 1200 under flexible policy; the two attempt-2 batches go-cov-cli and go-cov-mcp are single new modular test files bounded to <= 400 LOC each, a dispatch-adjudicated exception sized for their 2079/1922-statement pools); no production-code edits; no weakening of existing assertions; no added skips; anti-decomposition (pure-test tasks are fixed or defect-blocked, never split); uncovered-branch honesty (assert real behavior, not tautologies).

## Supersession & wiring (same board, in place)

- `ci-coverage-gate-90` (blocked, revision 7) is superseded atomically by `cortex_ia_work_decompose` into the sequential chain: **go-cov-cli -> go-cov-mcp -> ci-coverage-gate-80-hygiene**. Upstream dependencies (7.1, 7.2, 7.3) are preserved onto the chain head and the downstream edge of `playwright-ci-job` (7.7) binds to the chain tail.
- The ten original Wave 9 batches stay created-ready with empty dependencies (mutually disjoint new files; max 3 concurrent writers orchestrator-side); the two attempt-2 batches are the chain head/middle and run before the gate.
- The earlier wiring paragraph forbidding decomposition is void: the orchestrator adjudication explicitly authorizes in-place supersession, and no successor board (`-v2`, `-run2`) may be created.
- The re-dispatched gate envelope must carry artifact_refs to this addendum and to `.cortex-ia/discovery.md`, plus requirement REQ-QA-001, REQ-QA-002, REQ-QA-004.

## LOCAL-MODE PARITY (empirically derived by playwright-specs-flows, registered for orchestrator decision)

Roadmap registration only: this section records durable empirical findings and creates NO task, NO successor, and NO code change. Evidence source: the three Playwright flow specs executed against embedded local serve by `playwright-specs-flows` (dashboard-memory-search, graph-smoke, settings).

- The local serve mux (`internal/http/server.go`) exposes only observations, sessions, prompts, search, graph-edges, scores, and export.
- `/api/me`, `/api/agent/projects`, and `/api/graph/project-graph` exist only in `internal/platform/server/http.go`.
- Search: the project selector stays disabled under embedded local serve because the login handshake swallows the 404 for `/api/me`.
- Settings: `/settings` renders `Acceso Restringido` because `principal` is null and the developer-role fallback applies.
- Graph: Sigma WebGL never mounts because `rawSubgraph` stays null (`/api/graph/project-graph` is unavailable).
- The exported shell ships no `/config.js` tag, so endpoint injection works through Playwright test seeding only.
- No key-file-location surface exists in `web/src`; the location appears only in the serve banner `key_file=` line and in `cortex web key show`.

Each item requires an authz-model decision (whether principal-style endpoints must also exist in local mode) before any implementation work is scheduled. That decision belongs to the orchestrator roadmap and is a declared NON-GOAL of the playwright supersession chain; no task in this initiative may implement these endpoints.

**SUPERSEDED (self-hosted-pivot):** the local parity routes (GET /api/me, /api/stats, /api/projects, /api/agent/projects, /api/graph/project-graph) are delivered by `openspec/changes/self-hosted-pivot` (proposal, design, specs/api-parity) behind `withAuth` in `internal/http/server.go`. This section remains as the empirical findings record and creates no task on the closed board.

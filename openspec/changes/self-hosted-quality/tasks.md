# Tasks: Self-Hosted Quality

Board: self-hosted-quality | 36 tasks in 9 sections (waves W0-W8) | change_id `self-hosted-quality` | spec_plane hybrid | workload_policy flexible | workflow sdd-full. Wave order from the orchestrator: W0 -> W1 -> W2 -> W3 -> W4 -> W5 -> W6 -> W7 -> W8; every wave is file-disjoint internally, sequential same-file contacts carry explicit dependency edges (see the contested-file ledger), and the orchestrator dispatches at most 3 concurrent writers. No successor boards: blocked tasks decompose in place with `cortex_ia_work_decompose`. Task IDs are the durable Cortex-IA work item IDs; section numbers are `<section>.<n>` and are what the Depends fields reference. All verification commands are raw, standalone command lines; Docker, PostgreSQL-DSN, Playwright, and race gates that cannot execute locally are reported BLOCKED with compile-level ceilings, with `.github/workflows/ci.yml` as the authoritative executor.

Rules carried by every task: `allowed_files` is 1-3 files and edits outside it are forbidden (escalate via in-place decomposition) — the sole exception is task 1.2, which mutates no file content (git index and commit only) and therefore intentionally omits `allowed_files`; pure-test tasks (6.1, 6.2) are anti-decomposition — on failure fix or simplify assertions, never split a test into more tests; fast-TDD-eligible code tasks (4.1, 4.2, 4.3, 4.6, 4.7, 4.8, 4.9, 4.10, 4.11, 4.12, 5.1, 7.1, 7.2, 7.3, 7.4, 7.5, 7.6, 8.1, 8.2) carry the Mutation Evidence Gate of `cortex-work-protocol.md` §4/§8 — a SURVIVED mutant blocks transition to `in_review`; declarative tasks (1.1, 4.4, 4.5) are verified by parser/contract assertions, never by ad-hoc lexers; source <= 700 LOC and tests <= 1200 LOC per task under the flexible policy; documentation files are exempt from the source-logic budget but must still pass `go test -v -count=1 ./bench`.

## 1. Wave 0 — Git hygiene and committed baseline (gates everything)

### 1.1 sh-gitignore-hardening
Requirements: REQ-SQ-SEC-006
Files: .gitignore
Verify: git check-ignore -v tools/e2e-web/node_modules tools/e2e-web/playwright-report tools/e2e-web/test-results
Depends: none

### 1.2 sh-git-commit-landed-pivot
Requirements: REQ-SQ-SEC-006
Files: (omitted by design — git index and commit only; no file content is modified)
Verify: git status --porcelain
Depends: 1.1

## 2. Wave 1 — Documentation A: the two new canonical documents + HIGH staleness

### 2.1 sq-cli-reference-doc
Requirements: REQ-SQ-CR-001, REQ-SQ-CR-002, REQ-SQ-CR-003, REQ-SQ-CR-004, REQ-SQ-CR-005, REQ-SQ-CR-006
Files: docs/CLI-REFERENCE.md, docs/CLI.md
Verify: go test -v -count=1 ./bench
Depends: none

### 2.2 sq-tui-guide-doc
Requirements: REQ-SQ-TG-001, REQ-SQ-TG-002, REQ-SQ-TG-003, REQ-SQ-TG-004
Files: docs/TUI-GUIDE.md
Verify: go test -v -count=1 ./bench
Depends: none

### 2.3 sq-stale-wave1-install-server-railway
Requirements: REQ-SQ-DOC-001
Files: docs/INSTALLATION.md, docs/SERVER.md, docs/RAILWAY_DEPLOYMENT.md
Verify: go test -v -count=1 ./bench
Depends: none

## 3. Wave 2 — Documentation B: matrices, indexes, profiles, coverage truth, SVGs

### 3.1 sq-stale-matrices
Requirements: REQ-SQ-DOC-002
Files: docs/verification-matrix.md, docs/capability-matrix.md
Verify: go test -v -count=1 ./bench
Depends: none

### 3.2 sq-docs-index-readmes
Requirements: REQ-SQ-DOC-003
Files: docs/README.md, README.md
Verify: go test -v -count=1 ./bench
Depends: 2.1, 2.2

### 3.3 sq-profile-conflicts
Requirements: REQ-SQ-DOC-004
Files: docs/MCP.md, AGENTS.md, docs/ARCHITECTURE.md
Verify: go test -v -count=1 ./bench
Depends: none

### 3.4 sq-coverage-threshold-doc-drift
Requirements: REQ-SQ-DOC-007
Files: docs/COVERAGE.md, AGENTS.md
Verify: go test -v -count=1 ./bench
Depends: 3.3

### 3.5 sq-config-graph-docs
Requirements: REQ-SQ-DOC-005
Files: docs/CONFIGURATION.md, docs/GRAPH_INTELLIGENCE.md
Verify: go test -v -count=1 ./bench
Depends: none

### 3.6 sq-svg-multitenant-labels
Requirements: REQ-SQ-DOC-006
Files: docs/assets/architecture.svg, docs/assets/memory_lifecycle.svg
Verify: go test -v -count=1 ./bench
Depends: none

## 4. Wave 3 — Security chain (HIGH-1 first, then MEDIUMs, then consolidation)

### 4.1 sq-sec-grant-version-propagation
Requirements: REQ-SQ-SEC-001, REQ-SQ-SEC-010
Files: internal/platform/server/server.go, internal/platform/server/static_verifier.go, internal/platform/server/static_verifier_test.go
Verify: go test -v -count=1 ./internal/platform/server
Depends: none

### 4.2 sq-sec-logout-clear-list
Requirements: REQ-SQ-SEC-002
Files: internal/config/config.go, internal/config/config_test.go, internal/cli/cli.go
Verify: go test -v -count=1 ./internal/config ./internal/cli
Depends: none

### 4.3 sq-sec-loopback-empty-host
Requirements: REQ-SQ-SEC-003
Files: internal/cli/cli.go, internal/cli/cli_test.go
Verify: go test -v -count=1 ./internal/cli
Depends: 4.2

### 4.4 sq-sec-compose-bootstrap-defaults
Requirements: REQ-SQ-SEC-004
Files: docker-compose.yml, docker/docker-compose.prod.yml, internal/config/compose_defaults_test.go
Verify: go test -v -count=1 ./internal/config -run TestComposeBootstrapDevelopmentDefaults
Depends: none

### 4.5 sq-sec-entrypoint-bearer-echo
Requirements: REQ-SQ-SEC-004
Files: docker/server-entrypoint.sh, internal/config/compose_defaults_test.go
Verify: go test -v -count=1 ./internal/config
Depends: 4.4

### 4.6 sq-sec-bfs-project-filter
Requirements: REQ-SQ-SEC-005
Files: internal/http/parity_graph.go, internal/http/parity_graph_test.go
Verify: go test -v -count=1 ./internal/http
Depends: none

### 4.7 sq-sec-httpauth-contract
Requirements: REQ-SQ-SEC-007
Files: internal/httpauth/httpauth.go, internal/httpauth/httpauth_test.go
Verify: go test -v -count=1 ./internal/httpauth
Depends: none

### 4.8 sq-sec-httpauth-local-adoption
Requirements: REQ-SQ-SEC-007
Files: internal/http/server.go, internal/http/server_test.go
Verify: go test -v -count=1 ./internal/http
Depends: 4.7

### 4.9 sq-sec-httpauth-server-adoption
Requirements: REQ-SQ-SEC-007, REQ-SQ-SEC-010
Files: internal/platform/server/authentication.go, internal/platform/server/static_verifier.go, internal/platform/server/static_verifier_test.go
Verify: go test -v -count=1 ./internal/platform/server
Depends: 4.1, 4.7

### 4.10 sq-sec-principal-contract
Requirements: REQ-SQ-SEC-008
Files: internal/domain/synthetic_principal.go, internal/domain/synthetic_principal_test.go
Verify: go test -v -count=1 ./internal/domain
Depends: none

### 4.11 sq-sec-principal-adoption-local
Requirements: REQ-SQ-SEC-008
Files: internal/http/me_local.go, internal/http/parity_ops.go, internal/http/parity_ops_test.go
Verify: go test -v -count=1 ./internal/http
Depends: 4.10

### 4.12 sq-sec-principal-adoption-server
Requirements: REQ-SQ-SEC-008
Files: internal/platform/server/static_verifier.go, internal/platform/server/static_verifier_test.go
Verify: go test -v -count=1 ./internal/platform/server
Depends: 4.1, 4.9, 4.10

### 4.13 sq-sec-bearer-revocation-contract
Requirements: REQ-SQ-SEC-009
Files: docs/HTTP-API.md
Verify: go test -v -count=1 ./bench
Depends: 4.1, 4.9

## 5. Wave 4 — Runtime string and probe fixes

### 5.1 sq-code-mode-doctor-strings
Requirements: REQ-SQ-CR-007
Files: internal/cli/cli.go, internal/config/config.go
Verify: go test -v -count=1 ./internal/cli ./internal/config
Depends: 4.2, 4.3

## 6. Wave 5 — Performance baseline (before any optimization)

### 6.1 sq-perf-baseline-oracles-graph
Requirements: REQ-SQ-PERF-001
Files: internal/http/perf_bench_test.go, internal/mcp/perf_bench_test.go
Verify: go test -count=1 -bench=. -benchtime=1x -run '^$' ./internal/http ./internal/mcp ./internal/store/search ./internal/store/sqlite ./internal/store/graph ./internal/store/bundle ./internal/retrieval
Depends: none

### 6.2 sq-perf-baseline-oracles-boot
Requirements: REQ-SQ-PERF-001
Files: internal/app/perf_bench_test.go, internal/tui/perf_view_bench_test.go
Verify: go test -count=1 -bench=. -benchtime=1x -run '^$' ./internal/app ./internal/tui
Depends: none

## 7. Wave 6 — Performance quick wins (file-disjoint)

### 7.1 sq-perf-tui-tick-gating
Requirements: REQ-SQ-PERF-002, REQ-SQ-PERF-005
Files: internal/tui/model.go, internal/tui/update.go, internal/tui/update_test.go
Verify: go test -v -count=1 ./internal/tui
Depends: 6.2

### 7.2 sq-perf-stats-cache-ttl
Requirements: REQ-SQ-PERF-002, REQ-SQ-PERF-005
Files: internal/http/parity_ops.go, internal/http/parity_ops_test.go
Verify: go test -v -count=1 ./internal/http
Depends: 4.11, 6.1

### 7.3 sq-perf-parity-graph-sort-hoist
Requirements: REQ-SQ-PERF-002, REQ-SQ-PERF-005
Files: internal/http/parity_graph.go, internal/http/parity_graph_test.go
Verify: go test -v -count=1 ./internal/http
Depends: 4.6, 6.1

### 7.4 sq-perf-web-etag-precompressed
Requirements: REQ-SQ-PERF-002, REQ-SQ-PERF-005
Files: internal/web/server.go, internal/web/server_test.go, Makefile
Verify: go test -v -count=1 ./internal/web
Depends: none

### 7.5 sq-perf-quota-windows-prune
Requirements: REQ-SQ-PERF-002, REQ-SQ-PERF-005
Files: internal/platform/server/agent_limits.go, internal/platform/server/agent_limits_test.go
Verify: go test -v -count=1 ./internal/platform/server
Depends: none

### 7.6 sq-perf-graph-tools-label-projection
Requirements: REQ-SQ-PERF-002, REQ-SQ-PERF-005
Files: internal/mcp/tools_cortex.go, internal/mcp/tools_test.go
Verify: go test -v -count=1 ./internal/mcp
Depends: 6.1

## 8. Wave 7 — MCP graph batch (proposal 2, the only implementation-grade proposal)

### 8.1 sq-perf-mcp-batch-local
Requirements: REQ-SQ-PERF-003, REQ-SQ-PERF-005
Files: internal/mcp/tools_cortex.go, internal/mcp/tools_test.go
Verify: go test -v -count=1 ./internal/mcp
Depends: 7.6

### 8.2 sq-perf-mcp-batch-server
Requirements: REQ-SQ-PERF-003, REQ-SQ-PERF-006
Files: internal/platform/server/http.go, internal/platform/server/http_test.go
Verify: go test -v -count=1 ./internal/platform/server
Depends: none

## 9. Wave 8 — Performance roadmap document

### 9.1 sq-perf-roadmap-doc
Requirements: REQ-SQ-PERF-004
Files: docs/PERFORMANCE-ROADMAP.md
Verify: go test -v -count=1 ./bench
Depends: 6.1, 6.2, 7.1, 7.2, 7.3, 7.4, 7.5, 7.6, 8.1, 8.2

## Contested-file ledger

| File | Order | Edge |
|---|---|---|
| `.gitignore` | 1.1 -> 1.2 | commit after ignore rules |
| `internal/cli/cli.go` | 4.2 -> 4.3 -> 5.1 | chain |
| `internal/config/config.go` | 4.2 -> 5.1 | chain |
| `internal/config/compose_defaults_test.go` | 4.4 -> 4.5 | chain |
| `internal/platform/server/static_verifier.go` (+ test) | 4.1 -> 4.9 -> 4.12 | chain |
| `internal/http/parity_graph.go` (+ test) | 4.6 -> 7.3 | chain |
| `internal/http/parity_ops.go` (+ test) | 4.11 -> 7.2 | chain |
| `internal/mcp/tools_cortex.go` (+ `tools_test.go`) | 7.6 -> 8.1 | chain |
| `AGENTS.md` | 3.3 -> 3.4 | chain |
| `docs/README.md`, `README.md` | 2.1, 2.2 -> 3.2 | chain |

## Traceability

| Task | Wave | Requirements | Files |
|---|---|---|---|
| sh-gitignore-hardening | W0 | REQ-SQ-SEC-006 | 1 |
| sh-git-commit-landed-pivot | W0 | REQ-SQ-SEC-006 | 0 |
| sq-cli-reference-doc | W1 | REQ-SQ-CR-001..006 | 2 |
| sq-tui-guide-doc | W1 | REQ-SQ-TG-001..004 | 1 |
| sq-stale-wave1-install-server-railway | W1 | REQ-SQ-DOC-001 | 3 |
| sq-stale-matrices | W2 | REQ-SQ-DOC-002 | 2 |
| sq-docs-index-readmes | W2 | REQ-SQ-DOC-003 | 2 |
| sq-profile-conflicts | W2 | REQ-SQ-DOC-004 | 3 |
| sq-coverage-threshold-doc-drift | W2 | REQ-SQ-DOC-007 | 2 |
| sq-config-graph-docs | W2 | REQ-SQ-DOC-005 | 2 |
| sq-svg-multitenant-labels | W2 | REQ-SQ-DOC-006 | 2 |
| sq-sec-grant-version-propagation | W3 | REQ-SQ-SEC-001, 010 | 3 |
| sq-sec-logout-clear-list | W3 | REQ-SQ-SEC-002 | 3 |
| sq-sec-loopback-empty-host | W3 | REQ-SQ-SEC-003 | 2 |
| sq-sec-compose-bootstrap-defaults | W3 | REQ-SQ-SEC-004 | 3 |
| sq-sec-entrypoint-bearer-echo | W3 | REQ-SQ-SEC-004 | 2 |
| sq-sec-bfs-project-filter | W3 | REQ-SQ-SEC-005 | 2 |
| sq-sec-httpauth-contract | W3 | REQ-SQ-SEC-007 | 2 |
| sq-sec-httpauth-local-adoption | W3 | REQ-SQ-SEC-007 | 2 |
| sq-sec-httpauth-server-adoption | W3 | REQ-SQ-SEC-007, 010 | 3 |
| sq-sec-principal-contract | W3 | REQ-SQ-SEC-008 | 2 |
| sq-sec-principal-adoption-local | W3 | REQ-SQ-SEC-008 | 3 |
| sq-sec-principal-adoption-server | W3 | REQ-SQ-SEC-008 | 2 |
| sq-sec-bearer-revocation-contract | W3 | REQ-SQ-SEC-009 | 1 |
| sq-code-mode-doctor-strings | W4 | REQ-SQ-CR-007 | 2 |
| sq-perf-baseline-oracles-graph | W5 | REQ-SQ-PERF-001 | 2 |
| sq-perf-baseline-oracles-boot | W5 | REQ-SQ-PERF-001 | 2 |
| sq-perf-tui-tick-gating | W6 | REQ-SQ-PERF-002, 005 | 3 |
| sq-perf-stats-cache-ttl | W6 | REQ-SQ-PERF-002, 005 | 2 |
| sq-perf-parity-graph-sort-hoist | W6 | REQ-SQ-PERF-002, 005 | 2 |
| sq-perf-web-etag-precompressed | W6 | REQ-SQ-PERF-002, 005 | 3 |
| sq-perf-quota-windows-prune | W6 | REQ-SQ-PERF-002, 005 | 2 |
| sq-perf-graph-tools-label-projection | W6 | REQ-SQ-PERF-002, 005 | 2 |
| sq-perf-mcp-batch-local | W7 | REQ-SQ-PERF-003, 005 | 2 |
| sq-perf-mcp-batch-server | W7 | REQ-SQ-PERF-003, 006 | 2 |
| sq-perf-roadmap-doc | W8 | REQ-SQ-PERF-004 | 1 |

## Deferred (roadmap register, no task)

- Perf proposals: 1 (per-request audit+bind elimination, REQ-SQ-PERF-006), 3 (two-phase vector search), 4 (ProjectGraph one-pass SQL), 7 (search round-trip reduction), 9 (CLI startup fast path), 10 (watch daemon batching) — carried by `docs/PERFORMANCE-ROADMAP.md` (task 9.1).
- Architecture debts: `internal/platform/server/http.go` decomposition (2,574 LOC, degree 393), `internal/http` parity sub-package split, Port capability interfaces before endpoints grow (incl. `/api/graph/{id}/subgraph` and `/api/code/*` 404 locally per `web/src/lib/api.ts`), migration 111 physical drop as 113, test-suite consolidation (28 server test files; `multitenant_branch_removal_test.go` overlaps `saas_authentication_test.go`), `multitenant_branch_removal_test.go` ownership normalization, webkey Windows ACL hardening, `cortex_save` memory-plane writer reconciliation (obs #710/#668).
- Static-bearer TTL revocation (MEDIUM-2 implementation; documented by task 4.13 only).
- `docs/server-saas.md` rename — accepted naming, explicitly not tasked.
- Playwright Chromium provisioning in CI and the `ci-coverage-gate-90` push — untouched, remain under their existing gates.

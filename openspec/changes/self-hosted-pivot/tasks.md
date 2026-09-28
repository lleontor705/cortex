# Tasks: Self-Hosted Pivot

Board: self-hosted-pivot | 27 tasks in 8 waves | change_id `self-hosted-pivot` | spec_plane hybrid | workload_policy flexible | workflow sdd-full. Wave order from the orchestrator: W1 -> W2 -> {W4, W3} -> W5 -> {W6, W7} -> W8; every wave is file-disjoint internally, sequential same-file contacts are covered by explicit dependency edges, and the orchestrator dispatches at most 3 concurrent writers. No successor boards: blocked tasks decompose in place with `cortex_ia_work_decompose`. Task IDs are the durable Cortex-IA work item IDs. All verification commands are raw, standalone command lines; DSN, Docker, and Playwright gates that cannot execute locally are reported BLOCKED with compile-level local ceilings, with CI as the authoritative executor.

Rules carried by every task: `allowed_files` is 1-3 files and edits outside it are forbidden (escalate via in-place decomposition); pure-test tasks (1.3, 6.1, 6.2, 7.2, 7.3, 7.4) are anti-decomposition and defect-blocked — on failure fix or simplify assertions, never split a test into more tests; fast-TDD-eligible code tasks (1.1, 1.2, 2.1, 2.2, 2.3, 4.1-4.4, 5.1-5.3) carry the Mutation Evidence Gate of `cortex-work-protocol.md` §4/§8 — a SURVIVED mutant blocks transition to `in_review`; source <= 700 LOC and tests <= 1200 LOC per task under the flexible policy.

## 1. Wave 1 — Single-tenant auth middleware

### 1.1 sh-static-bearer-principal
Requirements: REQ-SH-001
Files: internal/platform/server/static_verifier.go, internal/platform/server/static_verifier_test.go, internal/platform/server/server.go
Verify: go test -v -count=1 ./internal/platform/server
Depends: none

### 1.2 sh-multitenant-branch-removal
Requirements: REQ-SH-002
Files: internal/platform/server/server.go, internal/platform/server/http.go, internal/platform/server/agent_http.go
Verify: go test -v -count=1 ./internal/platform/server
Depends: 1.1

### 1.3 sh-saas-auth-tests-rework
Requirements: REQ-SH-001, REQ-SH-002
Files: internal/platform/server/saas_authentication_test.go, internal/platform/server/http_handlers_coverage_test.go
Verify: go test -v -count=1 ./internal/platform/server
Depends: 1.1, 1.2

### 1.4 sh-delete-multitenant-verifier
Requirements: REQ-SH-002
Files: internal/store/postgres/token_repository.go, internal/store/postgres/coverage_integration_test.go
Verify: go vet -tags postgres_integration ./internal/store/postgres
Depends: 1.2

## 2. Wave 2 — Store/policy simplification

### 2.1 sh-workspace-fixed-default
Requirements: REQ-SH-002, REQ-SH-003
Files: internal/platform/server/authentication.go, internal/platform/server/saas_authentication_test.go, internal/platform/server/http_handlers_coverage_test.go
Verify: go test -v -count=1 ./internal/platform/server
Depends: 1.3

### 2.2 sh-authz-policy-defatten
Requirements: REQ-SH-003
Files: internal/authz/policy.go, internal/authz/policy_test.go, internal/authz/policy_boundary_test.go
Verify: go test -v -count=1 ./internal/authz
Depends: none

### 2.3 sh-request-vector-removal
Requirements: REQ-SH-002
Files: internal/server/external/scoped_vector.go, internal/server/external/scoped_vector_test.go
Verify: go test -v -count=1 ./internal/server/external
Depends: 1.2

## 3. Wave 3 — Migration roadmap documentation (parallel with wave 4)

### 3.1 sh-migration-111-roadmap-docs
Requirements: REQ-SH-020
Files: migrations/v2/embed.go, internal/migration/postgres.go, docs/project-context-protocol-identity-privilege.md
Verify: go test -v -count=1 ./internal/migration
Depends: 1.4

## 4. Wave 4 — Parity package extraction

### 4.1 sh-api-port-me
Requirements: REQ-SH-010
Files: internal/api/port.go, internal/api/me.go, internal/api/me_test.go
Verify: go test -v -count=1 ./internal/api
Depends: none

### 4.2 sh-api-stats-projects
Requirements: REQ-SH-010
Files: internal/api/stats.go, internal/api/projects.go, internal/api/projects_test.go
Verify: go test -v -count=1 ./internal/api
Depends: 4.1

### 4.3 sh-api-project-graph
Requirements: REQ-SH-010
Files: internal/api/graph.go, internal/api/graph_test.go
Verify: go test -v -count=1 ./internal/api
Depends: 4.1

### 4.4 sh-server-api-delegation
Requirements: REQ-SH-011, REQ-SH-013
Files: internal/platform/server/http.go, internal/platform/server/agent_http.go, internal/platform/server/http_test.go
Verify: go test -v -count=1 ./internal/platform/server
Depends: 1.2, 2.1, 4.2, 4.3

## 5. Wave 5 — Local store adapters and route registration

### 5.1 sh-local-parity-ops
Requirements: REQ-SH-012
Files: internal/http/parity_ops.go, internal/http/parity_ops_test.go
Verify: go test -v -count=1 ./internal/http
Depends: 4.1

### 5.2 sh-local-parity-graph
Requirements: REQ-SH-012
Files: internal/http/parity_graph.go, internal/http/parity_graph_test.go
Verify: go test -v -count=1 ./internal/http
Depends: 4.1

### 5.3 sh-local-parity-routes
Requirements: REQ-SH-012, REQ-SH-013, REQ-SH-014
Files: internal/http/server.go, internal/http/me_local.go, internal/http/parity_routes_test.go
Verify: go test -v -count=1 ./internal/http ./internal/app
Depends: 5.1, 5.2

## 6. Wave 6 — Playwright parity validation (parallel with wave 7)

### 6.1 sh-playwright-selector-settings
Requirements: REQ-SH-012
Files: tools/e2e-web/tests/dashboard-memory-search.spec.ts, tools/e2e-web/tests/settings.spec.ts
Verify: cd tools/e2e-web && npx playwright test tests/dashboard-memory-search.spec.ts tests/settings.spec.ts
Depends: 4.4, 5.3

### 6.2 sh-playwright-graph-mount
Requirements: REQ-SH-012
Files: tools/e2e-web/tests/graph-smoke.spec.ts
Verify: cd tools/e2e-web && npx playwright test tests/graph-smoke.spec.ts
Depends: 4.4, 5.3

## 7. Wave 7 — Test and CI matrix prune/rework

### 7.1 sh-compose-e2e-rescope
Requirements: REQ-SH-031
Files: e2e/docker_compose_test.go, docker-compose.yml, e2e/docker-compose.e2e.yml
Verify: go vet -tags docker_e2e ./e2e
Depends: 2.1

### 7.2 sh-pg-suite-rework-a
Requirements: REQ-SH-031
Files: internal/store/postgres/workspace_sync_integration_test.go, internal/store/postgres/workspace_list_search_integration_test.go, internal/store/postgres/save_effect_integration_test.go
Verify: go vet -tags postgres_integration ./internal/store/postgres
Depends: 1.4

### 7.3 sh-pg-suite-rework-b
Requirements: REQ-SH-031
Files: internal/store/postgres/token_repository_integration_test.go, internal/store/postgres/agent_graph_integration_test.go, internal/store/postgres/handoff_receipt_integration_test.go
Verify: go vet -tags postgres_integration ./internal/store/postgres
Depends: 1.4

### 7.4 sh-doc-contract-coverage-gate
Requirements: REQ-SH-030, REQ-SH-032
Files: bench/documentation_contract_test.go, .github/workflows/ci.yml
Verify: go test -v -count=1 ./bench
Depends: 7.1, 7.2, 7.3

## 8. Wave 8 — Docs and positioning sweep

### 8.1 sh-selfhost-doc-replace
Requirements: REQ-SH-040
Files: docs/server-saas.md, AGENTS.md, docs/SERVER.md
Verify: go test -v -count=1 ./bench
Depends: 4.4, 5.3, 7.4

### 8.2 sh-readme-capability-sweep
Requirements: REQ-SH-041
Files: README.md, docs/embedded-web.md, docs/capability-matrix.md
Verify: go test -v -count=1 ./bench
Depends: 4.4, 5.3, 7.4

### 8.3 sh-config-multitenant-field-removal
Requirements: REQ-SH-002, REQ-SH-041
Files: internal/config/config.go, internal/config/config_test.go, internal/platform/mode.go
Verify: go test -v -count=1 ./internal/config ./internal/platform
Depends: 1.2, 1.3

### 8.4 sh-matrices-config-arch-sweep
Requirements: REQ-SH-041
Files: docs/verification-matrix.md, docs/CONFIGURATION.md, docs/ARCHITECTURE.md
Verify: go test -v -count=1 ./bench
Depends: 7.4, 8.3

### 8.5 sh-api-deploy-docs-sweep
Requirements: REQ-SH-041
Files: docs/HTTP-API.md, docs/RAILWAY_DEPLOYMENT.md, docs/INSTALLATION.md
Verify: go test -v -count=1 ./bench
Depends: 4.4, 5.3, 7.4

### 8.6 sh-addendum-parity-pointer
Requirements: REQ-SH-042
Files: openspec/changes/embedded-web-redesign/specs/quality-gates/coverage-push-90-addendum.md
Verify: go test -v -count=1 ./bench -run TestEngramCompatibilitySurfaceRemoved
Depends: 5.3

## Traceability

| Task | Wave | Requirements | Design decision | Files |
|---|---|---|---|---|
| sh-static-bearer-principal | 1 | REQ-SH-001 | D1 | 3 |
| sh-multitenant-branch-removal | 1 | REQ-SH-002 | D2 | 3 |
| sh-saas-auth-tests-rework | 1 | REQ-SH-001, REQ-SH-002 | D1, D2 | 2 |
| sh-delete-multitenant-verifier | 1 | REQ-SH-002 | D2 | 2 |
| sh-workspace-fixed-default | 2 | REQ-SH-002, REQ-SH-003 | D2, C2 | 3 |
| sh-authz-policy-defatten | 2 | REQ-SH-003 | D1, C2 | 3 |
| sh-request-vector-removal | 2 | REQ-SH-002 | C1 | 2 |
| sh-migration-111-roadmap-docs | 3 | REQ-SH-020 | D2 | 3 |
| sh-api-port-me | 4 | REQ-SH-010 | D3 | 3 |
| sh-api-stats-projects | 4 | REQ-SH-010 | D3, D4 | 3 |
| sh-api-project-graph | 4 | REQ-SH-010 | D3, D4 | 2 |
| sh-server-api-delegation | 4 | REQ-SH-011, REQ-SH-013 | D3 | 3 |
| sh-local-parity-ops | 5 | REQ-SH-012 | D3, D4 | 2 |
| sh-local-parity-graph | 5 | REQ-SH-012 | D3, D4 | 2 |
| sh-local-parity-routes | 5 | REQ-SH-012, REQ-SH-013, REQ-SH-014 | D4 | 3 |
| sh-playwright-selector-settings | 6 | REQ-SH-012 | D4 | 2 |
| sh-playwright-graph-mount | 6 | REQ-SH-012 | D4 | 1 |
| sh-compose-e2e-rescope | 7 | REQ-SH-031 | D5 | 3 |
| sh-pg-suite-rework-a | 7 | REQ-SH-031 | D5 | 3 |
| sh-pg-suite-rework-b | 7 | REQ-SH-031 | D5 | 3 |
| sh-doc-contract-coverage-gate | 7 | REQ-SH-030, REQ-SH-032 | D5 | 2 |
| sh-selfhost-doc-replace | 8 | REQ-SH-040 | D6 | 3 |
| sh-readme-capability-sweep | 8 | REQ-SH-041 | D6 | 3 |
| sh-config-multitenant-field-removal | 8 | REQ-SH-002, REQ-SH-041 | D2, D6 | 3 |
| sh-matrices-config-arch-sweep | 8 | REQ-SH-041 | D6 | 3 |
| sh-api-deploy-docs-sweep | 8 | REQ-SH-041 | D6 | 3 |
| sh-addendum-parity-pointer | 8 | REQ-SH-042 | D6 | 1 |

**Deferred (roadmap, no task):** migration 112 compensating drop of 111 (D2); `/api/audit`, `/api/system/metrics`, `/api/rag/stats` local parity (D4/REQ-SH-014); coverage 90.0 Tier-2 roadmap from the coverage addendum (REQ-QA-004 unchanged).

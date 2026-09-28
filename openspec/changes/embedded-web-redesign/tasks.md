# Tasks: Embedded Web Redesign

Board: embedded-web-redesign | 40 tasks in 9 waves (4 superseded in place: 7.4 plus the playwright chain 7.5-7.6-7.7). Wave 9 adds ten pure-test coverage batches deconstructed in place from the blocked ci-coverage-gate-90, plus the attempt-2 supersession chain go-cov-cli -> go-cov-mcp -> ci-coverage-gate-80-hygiene under the orchestrator two-tier adjudication. The playwright chain is superseded under the contract-pin drift adjudication recorded in the 7.5-7.7 amendments. Task IDs are the durable Cortex-IA work item IDs. All verification commands are raw and standalone; postgres/DSN and Docker gates stay CI-only.

## 1. Wave 1 — Foundations (Go config + key)

### 1.1 web-cfg-namespace
Requirements: REQ-CFG-001
Files: internal/config/web.go, internal/config/web_test.go
Verify: go test -v -count=1 ./internal/config -run '^TestWeb'
Depends: none

### 1.2 web-key-package
Requirements: REQ-KEY-001, REQ-KEY-002, REQ-KEY-004
Files: internal/webkey/webkey.go, internal/webkey/webkey_test.go
Verify: go test -v -count=1 ./internal/webkey
Depends: none

### 1.3 web-key-cli
Requirements: REQ-KEY-003
Files: internal/cli/web.go, internal/cli/web_test.go, internal/cli/cli.go
Verify: go test -v -count=1 ./internal/cli -run '^TestWebKeyCLI'
Depends: 1.1, 1.2

## 2. Wave 2 — Web build migration

### 2.1 web-static-export
Requirements: REQ-WEB-001, REQ-WEB-002, REQ-WEB-003
Files: web/next.config.ts, web/src/lib/server-endpoint.ts, web/src/app/health/route.ts
Verify: cd web && npm run build
Depends: none

### 2.2 web-build-orchestration
Requirements: REQ-WEB-007, REQ-HYG-002
Files: Makefile, .gitignore, internal/web/dist/index.html
Verify: make web-build
Depends: 2.1

## 3. Wave 3 — Go embedded server

### 3.1 web-embed-package
Requirements: REQ-WEB-003, REQ-WEB-004
Files: internal/web/embed.go, internal/web/server.go, internal/web/server_test.go
Verify: go test -v -count=1 ./internal/web
Depends: 1.2, 2.2

### 3.2 web-mount-local-serve
Requirements: REQ-WEB-005, REQ-KEY-001, REQ-MODE-002
Files: internal/http/server.go, internal/cli/cli.go
Verify: go test -v -count=1 ./internal/http ./internal/cli
Depends: 1.1, 1.3, 3.1

### 3.3 web-mount-server-mode
Requirements: REQ-WEB-006
Files: internal/platform/server/http.go, cmd/cortex/main.go
Verify: go build ./cmd/cortex
Depends: 1.2, 3.1

## 4. Wave 4 — Mode triad formalization

### 4.1 mode-triad-formalize
Requirements: REQ-MODE-001
Files: internal/platform/mode.go, internal/app/app.go, internal/platform/mode_test.go
Verify: go test -v -count=1 ./internal/platform ./internal/app
Depends: none

## 5. Wave 5 — UX/UI (parallel, disjoint files)

### 5.1 web-route-boundaries
Requirements: REQ-UX-002
Files: web/src/app/error.tsx, web/src/app/loading.tsx, web/src/app/not-found.tsx
Verify: cd web && npm test
Depends: none

### 5.2 web-toast-system
Requirements: REQ-UX-003
Files: web/src/lib/toast.tsx, web/src/app/layout.tsx, web/src/lib/toast.test.ts
Verify: cd web && npm test
Depends: none

### 5.3 web-onboarding-key-entry
Requirements: REQ-UX-001
Files: web/src/lib/web-key.ts, web/src/lib/web-key.test.ts, web/src/components/AppShell.tsx
Verify: cd web && npm test
Depends: 2.1

### 5.4 web-empty-states-a11y
Requirements: REQ-UX-004
Files: web/src/components/shared/EmptyState.tsx, web/.eslintrc.json, web/package.json
Verify: cd web && npm run lint
Depends: 2.2

## 6. Wave 6 — Cleanup

### 6.1 purge-docs-dead-files
Requirements: REQ-HYG-001
Files: llms.txt, llms-full.txt, review/judgment-day/cortex-baseline-round1/fixes/round1-fix-report.md, review/judgment-day/cortex-baseline-round1/fixes/round2-authz-remediation-report.md
Verify: git ls-files llms.txt llms-full.txt review
Depends: none

### 6.2 purge-infra-dead-files
Requirements: REQ-HYG-001, REQ-HYG-002
Files: docker/nginx-ollama/Dockerfile, docker/nginx-ollama/default.conf.template, .gitignore
Verify: git ls-files docker/nginx-ollama
Depends: 2.2

## 7. Wave 7 — Quality gates

### 7.1 go-cov-lightest-batch-1
Requirements: REQ-QA-001
Files: internal/ollama/ollama_coverage_test.go, internal/update/update_coverage_test.go
Verify: go test -v -count=1 ./internal/ollama ./internal/update
Depends: none

### 7.2 go-cov-lightest-batch-2
Requirements: REQ-QA-001
Files: internal/transportpolicy/transportpolicy_coverage_test.go, internal/project/project_coverage_test.go
Verify: go test -v -count=1 ./internal/transportpolicy ./internal/project
Depends: none

### 7.3 go-cov-web-packages
Requirements: REQ-QA-001, REQ-QA-002
Files: internal/web/server_extra_test.go, internal/webkey/webkey_extra_test.go
Verify: go test -v -count=1 ./internal/web ./internal/webkey
Depends: 1.2, 3.1

### 7.4 ci-coverage-gate-90 (SUPERSEDED)
Requirements: REQ-QA-001, REQ-QA-002, REQ-QA-004
Files: .github/workflows/ci.yml
Verify: go test -count=1 -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
Depends: 7.1, 7.2, 7.3
Supersession amendment (Wave 9, orchestrator two-tier adjudication): after attempt 2 (submission d92cda9d) measured 75.0% under the computed coverpkg scope, the orchestrator adjudicated a two-tier strategy and this task was atomically superseded in place by cortex_ia_work_decompose (revision 8, status superseded, replaced_by go-cov-cli, go-cov-mcp, ci-coverage-gate-80-hygiene). The 90.0 threshold is retired from active gating and recorded as documented roadmap in specs/quality-gates/coverage-push-90-addendum.md. Upstream dependencies 7.1, 7.2, 7.3 now bind to the chain head (go-cov-cli), and the downstream edge of 7.7 rebinds to the chain tail (ci-coverage-gate-80-hygiene). Do not re-create, unblock, or re-dispatch this id.

### 7.5 playwright-harness (SUPERSEDED)
Requirements: REQ-QA-003
Files: tools/e2e-web/package.json, tools/e2e-web/playwright.config.ts, tools/e2e-web/tests/first-boot-key.spec.ts
Verify: cd tools/e2e-web && npx playwright test tests/first-boot-key.spec.ts
Depends: 3.2

Supersession amendment (pin-drift adjudication, task revision 7): attempt 2 finished COMPLETE and GREEN on disk - acceptance `cd tools/e2e-web && npx playwright test tests/first-boot-key.spec.ts` exits 0 with 1 passed, two real defects were fixed (cross-process pid coupling and serverUrl seeding via addInitScript), the oracle is mutation-proven, and the companion package-lock.json was reserved-accepted and disclosed - but the in_review transition was refused by the contract-pin drift guard because 2 of 14 pins moved (.cortex-ia/discovery.md d67791e8 -> abc2f170 via the mandated discovery refresh; tasks.md ae5d43d6 -> d419dcb9 via the sanctioned two-tier supersession) and no pin-update primitive exists. This id was atomically superseded in place by cortex_ia_work_decompose (status superseded, replaced_by playwright-harness-r2), whose objective is verify-and-transition only over the unchanged deliverables tools/e2e-web/{package.json, playwright.config.ts, tests/first-boot-key.spec.ts}; no re-authoring, no production or test edits. Do not re-create, unblock, or re-dispatch this id.

### 7.6 playwright-specs-flows (SUPERSEDED)
Requirements: REQ-QA-003
Files: tools/e2e-web/tests/dashboard-memory-search.spec.ts, tools/e2e-web/tests/graph-smoke.spec.ts, tools/e2e-web/tests/settings.spec.ts
Verify: cd tools/e2e-web && npx playwright test
Depends: 7.5

Supersession amendment (pin-drift adjudication): never started (backlog) and inherited the same drifted 14-pin contract, so it could never have transitioned either. Superseded in place by cortex_ia_work_decompose, replaced_by playwright-specs-flows-r2, which carries the identical objective, acceptance criteria, verification command and allowed_files (tools/e2e-web/tests/dashboard-memory-search.spec.ts, tools/e2e-web/tests/graph-smoke.spec.ts, tools/e2e-web/tests/settings.spec.ts) under fresh pins, with its dependency rebound from playwright-harness to playwright-harness-r2. Do not re-create, unblock, or re-dispatch this id.

Executed supersession record (planner phase=decompose, contract-pin drift): attempt 1 finished COMPLETE and GREEN on disk (dashboard-memory-search 115 LOC, graph-smoke 62 LOC, settings 86 LOC; `cd tools/e2e-web && npx playwright test` exit 0 with 5 passed on 6 consecutive full runs, mutation probe proven) and the in_review transition was refused solely by the contract-pin drift guard (.cortex-ia/discovery.md d67791e8 -> abc2f170 and tasks.md ae5d43d6 -> 28b898e8; the other 11 pins byte-identical). The id is now ACTUALLY superseded in place by cortex_ia_work_decompose into playwright-specs-flows-r2 on this same board: verify-and-transition disposition over the three unchanged spec files, verification `cd tools/e2e-web && npx playwright test`, dependency playwright-harness-r2 (DONE, revision 5), fresh 14-pin contract recomputed from exact file bytes with cortex_ia_content_hash. No re-authoring of the deliverables and no local-parity endpoint tasks. Do not re-create, unblock, or re-dispatch this id.

### 7.7 playwright-ci-job (SUPERSEDED)
Requirements: REQ-QA-003
Files: .github/workflows/ci.yml
Verify: npx js-yaml .github/workflows/ci.yml
Depends: 9.13, 7.6

Supersession amendment (pin-drift adjudication): never started (backlog) and inherited the same drifted 14-pin contract. Superseded in place by cortex_ia_work_decompose, replaced_by playwright-ci-job-r2, which adds the dedicated Node 24 + headless-chromium Playwright job to .github/workflows/ci.yml (non-optional, no continue-on-error, no other job touched, verification `npx js-yaml .github/workflows/ci.yml`) and additionally carries the MANDATORY inherited fix: the ci.yml docker-e2e job still probes cortex-cortex-ui-1 and localhost:3000/health, stale since retire-web-image-compose, and must be corrected to probe the cortex container embedded surface (health and asset probes following the retire-web-image-compose plus release-e2e pattern). Dependencies rebound to ci-coverage-gate-80-hygiene (DONE, revision 4) and playwright-specs-flows-r2. Do not re-create, unblock, or re-dispatch this id.



Pin-drift supersession amendment (7.5, 7.6, 7.7 - fresh contract pins): the three superseded ids are replaced in place on the same board by playwright-harness-r2, playwright-specs-flows-r2 and playwright-ci-job-r2, each carrying a fresh 14-pin contract whose digests were recomputed from the exact file bytes with cortex_ia_content_hash before pinning. Fresh pins: .cortex-ia/discovery.md = abc2f1709a30d9c5ba16f28b704bbc3daeb7dad667fff291c1554b843a2eb962 (previously d67791e8, mandated discovery refresh); specs/quality-gates/spec.md = a657b484363af8fbc70833c9a0ab3ef22a65d67336bfeeaeccb06c9e86049c05 (unchanged); specs/quality-gates/coverage-push-90-addendum.md = be1b7692f05998e0b847f0ca003cc378e986a4bf618146145093d52c516018f3 (new pin); tasks.md = the digest of these amended bytes, recorded in every successor contract and deliberately not mirrored here because a file cannot contain its own hash (the pre-amendment digest d419dcb945933ccdf65b6259e33bdb624fc35653b0319fe00d32fffff94404d8 was verified from file bytes first). The other ten pins (design.md, proposal.md, specs/docs/spec.md, specs/mode-triad/spec.md, specs/release-packaging/spec.md, specs/repo-hygiene/spec.md, specs/web-config/spec.md, specs/web-embedding/spec.md, specs/web-key/spec.md, specs/web-ux/spec.md) were re-verified byte-identical to the original contract and carry forward unchanged. This amendment is written BEFORE the successors are pinned so the tasks.md pin is final.

Addendum re-pin after the LOCAL-MODE PARITY roadmap registration: specs/quality-gates/coverage-push-90-addendum.md moved from be1b7692f05998e0b847f0ca003cc378e986a4bf618146145093d52c516018f3 to 21a816e77f9c73637d59dc29ed1885b63b25587ece90a8354d814b112fe695a3 (9977 bytes). The already-approved successors playwright-harness-r2-preflight and playwright-harness-r2 keep the historical be1b7692 digest inside their APPROVED bindings; only the successor created after this registration (playwright-specs-flows-r2) carries the new digest. .cortex-ia/discovery.md = abc2f1709a30d9c5ba16f28b704bbc3daeb7dad667fff291c1554b843a2eb962 and specs/quality-gates/spec.md = a657b484363af8fbc70833c9a0ab3ef22a65d67336bfeeaeccb06c9e86049c05 are unchanged; tasks.md = the digest of these amended bytes, recorded only in the successor contract. This amendment is written BEFORE playwright-specs-flows-r2 is created so its tasks.md pin is final.

Roadmap-only registration (NO task, NO successor, NO code change): the LOCAL-MODE PARITY findings empirically derived by 7.6 are recorded in specs/quality-gates/coverage-push-90-addendum.md under "LOCAL-MODE PARITY" - the local serve mux (internal/http/server.go) exposes only observations/sessions/prompts/search/graph-edges/scores/export, while /api/me, /api/agent/projects and /api/graph/project-graph exist only in internal/platform/server/http.go, so under embedded local serve the search project-selector stays disabled (login handshake swallows the 404), /settings renders Acceso Restringido (principal=null -> developer role) and Sigma WebGL never mounts (rawSubgraph null); additionally the exported shell ships no /config.js tag (endpoint injection works via Playwright test seeding only) and no key-file-location surface exists in web/src (the location appears only in the serve banner key_file= line and in `cortex web key show`). Each item needs an authz-model decision (whether principal-style endpoints exist in local mode) and is an explicit NON-GOAL of this initiative: orchestrator roadmap decision, deliberately not tasked here.



Rewired chain: playwright-harness-r2 inherits web-mount-local-serve; playwright-specs-flows-r2 depends on playwright-harness-r2; playwright-ci-job-r2 depends on ci-coverage-gate-80-hygiene and playwright-specs-flows-r2. Superseded ids playwright-harness, playwright-specs-flows and playwright-ci-job must never be re-created, unblocked or re-dispatched.

## 8. Wave 8 — Release, retirement, docs

### 8.1 release-embed-ordering
Requirements: REQ-REL-001
Files: .goreleaser.yaml, .github/workflows/release.yml
Verify: npx js-yaml .goreleaser.yaml
Depends: 2.2, 3.1

### 8.2 retire-web-image-compose
Requirements: REQ-REL-002
Files: docker-compose.yml, docker/Dockerfile.web, docker/docker-compose.prod.yml
Verify: npx js-yaml docker-compose.yml
Depends: 3.2, 3.3

### 8.3 retire-web-image-release-e2e
Requirements: REQ-REL-002
Files: .github/workflows/release.yml, e2e/docker_compose_test.go
Verify: go vet -tags docker_e2e ./e2e
Depends: 8.1, 8.2

### 8.4 docs-embedded-web
Requirements: REQ-DOC-001
Files: README.md, docs/embedded-web.md
Verify: git grep -q "cortex web key" -- README.md docs/embedded-web.md
Depends: 3.3, 4.1, 5.3

### 8.5 docs-saas-review
Requirements: REQ-DOC-002
Files: docs/server-saas.md, AGENTS.md
Verify: git grep -q "control plane" -- docs/server-saas.md
Depends: 4.1

## 9. Wave 9 — Coverage push (blocked-gate decomposition, in place)

Planner-routed replacement work for blocked ci-coverage-gate-90: ten pure-test batches toward an honest total, alongside the REQ-QA-004 vendored-scope hygiene decision. Addendum: specs/quality-gates/coverage-push-90-addendum.md. All ten batches (9.1-9.10) start ready in parallel (mutually disjoint new test files; max 3 concurrent writers orchestrator-side). None may be DAG-decomposed on failure: fix, simplify, or defect-block.

9.x amendment (gate re-scoped): the orchestrator two-tier adjudication supersedes the 90.0-literal gate. Tier 1 (active, enforced) is an 80.0 threshold plus the computed -coverpkg scope; Tier 2 (documented roadmap, not enforced) is the global 90.0 target, which requires further waves over internal/platform/server (65.9/3644, further batches deferred and registered) and internal/store/postgres (37.9/3121, DSN-gated, CI uplift ~+5, registered), with per-package floor intent >= 85 for new/security packages (internal/web 90.5 met; internal/webkey 79.2 roadmap-flagged) and the internal/platform/server residual ~334 statements registered. Measured basis: attempt 2, submission d92cda9d, total 75.0% under computed coverpkg (78 packages, vendored web/node_modules excluded).

### 9.1 go-cov-app
Requirements: REQ-QA-001
Files: internal/app/app_coverage_test.go
Verify: go test -v -count=1 -cover ./internal/app
Depends: none

### 9.2 go-cov-tui
Requirements: REQ-QA-001
Files: internal/tui/views_coverage_test.go, internal/tui/theme_coverage_test.go, internal/tui/keys_coverage_test.go
Verify: go test -v -count=1 -cover ./internal/tui
Depends: none

### 9.3 go-cov-cmd
Requirements: REQ-QA-001
Files: cmd/cortex/main_coverage_test.go
Verify: go test -v -count=1 -cover ./cmd/cortex
Depends: none

### 9.4 go-cov-setup
Requirements: REQ-QA-001
Files: internal/setup/setup_coverage_test.go
Verify: go test -v -count=1 -cover ./internal/setup
Depends: none

### 9.5 go-cov-lifecycle
Requirements: REQ-QA-001
Files: internal/domain/lifecycle/archival_coverage_test.go
Verify: go test -v -count=1 -cover ./internal/domain/lifecycle
Depends: none

### 9.6 go-cov-sandbox
Requirements: REQ-QA-001
Files: internal/domain/sandbox/sandbox_coverage_test.go
Verify: go test -v -count=1 -cover ./internal/domain/sandbox
Depends: none

### 9.7 go-cov-server-surface
Requirements: REQ-QA-001
Files: internal/platform/server/http_handlers_coverage_test.go, internal/platform/server/mcp_gating_coverage_test.go
Verify: go test -v -count=1 -cover ./internal/platform/server
Depends: none

### 9.8 go-cov-postgres-dsn
Requirements: REQ-QA-001, REQ-QA-004
Files: internal/store/postgres/coverage_integration_test.go, internal/migration/coverage_integration_test.go
Verify: go vet -tags postgres_integration ./internal/store/postgres ./internal/migration ./testutil/postgrestest
Depends: none

### 9.9 go-cov-bench-dmr
Requirements: REQ-QA-001
Files: bench/dmr/runner_coverage_test.go
Verify: go test -v -count=1 -cover ./bench/dmr
Depends: none

### 9.10 go-cov-bench-lmm
Requirements: REQ-QA-001
Files: bench/longmemeval/runner_coverage_test.go
Verify: go test -v -count=1 -cover ./bench/longmemeval
Depends: none

### 9.11 go-cov-cli
Requirements: REQ-QA-001, REQ-QA-004
Files: internal/cli/cli_coverage_test.go
Verify: go test -v -count=1 -cover ./internal/cli
Depends: 7.1, 7.2, 7.3
Attempt-2 supersession chain head (inherits upstream deps of ci-coverage-gate-90). Pure-test batch: internal/cli 2079 statements at 66.3 -> >= 75; single new modular test file <= 400 LOC; no production edits; anti-decomposition (fix, simplify, or defect-block).

### 9.12 go-cov-mcp
Requirements: REQ-QA-001, REQ-QA-004
Files: internal/mcp/mcp_coverage_test.go
Verify: go test -v -count=1 -cover ./internal/mcp
Depends: 9.11
Attempt-2 supersession chain middle. Pure-test batch: internal/mcp 1922 statements at 74.0 -> >= 80; single new modular test file <= 400 LOC; no production edits; anti-decomposition (fix, simplify, or defect-block).

### 9.13 ci-coverage-gate-80-hygiene
Requirements: REQ-QA-001, REQ-QA-002, REQ-QA-004
Files: .github/workflows/ci.yml
Verify: go test -count=1 -covermode=atomic -coverpkg=$(go list ./... | grep -v /node_modules/) -coverprofile=coverage.out ./...
Depends: 9.12
Attempt-2 supersession chain tail (active gate). Raise the Global Coverage enforcement from 70.0 to 80.0, replace -coverpkg=./... with the computed list excluding only web/node_modules, and update the numeric check plus both printf messages consistently; no other CI job touched and no 90.0 enforced value remains. If the computed-scope total is still below 80.0, transition blocked with the measured percentage instead of diluting.

## Traceability

All 28 requirement IDs (REQ-CFG-001; REQ-KEY-001..004; REQ-WEB-001..007; REQ-MODE-001..002; REQ-UX-001..004; REQ-QA-001..004; REQ-REL-001..002; REQ-HYG-001..002; REQ-DOC-001..002) are covered by at least one task above. Post-approval (outside this DAG): discovery role refreshes .cortex-ia/discovery.md readiness section; planner archives via cortex_ia_change_archive.

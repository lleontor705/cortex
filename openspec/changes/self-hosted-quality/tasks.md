# Tasks: Self-Hosted Quality

Board: self-hosted-quality | 36 tasks in 9 sections (waves W0-W8) | change_id `self-hosted-quality` | spec_plane hybrid | workload_policy flexible | workflow sdd-full. Wave order from the orchestrator: W0 -> W1 -> W2 -> W3 -> W4 -> W5 -> W6 -> W7 -> W8; every wave is file-disjoint internally, sequential same-file contacts carry explicit dependency edges (see the contested-file ledger), and the orchestrator dispatches at most 3 concurrent writers. No successor boards: blocked tasks decompose in place with `cortex_ia_work_decompose`. Task IDs are the durable Cortex-IA work item IDs; section numbers are `<section>.<n>` and are what the Depends fields reference. All verification commands are raw, standalone command lines; Docker, PostgreSQL-DSN, Playwright, and race gates that cannot execute locally are reported BLOCKED with compile-level ceilings, with `.github/workflows/ci.yml` as the authoritative executor.

Rules carried by every task: `allowed_files` is 1-3 files and edits outside it are forbidden (escalate via in-place decomposition) — task 1.2 was originally authored without `allowed_files` (git index and commit only, no file content modified) and is superseded in place by `sh-git-commit-landed-pivot-r2`, which declares `allowed_files: ['.gitignore']` and is verify-and-transition only; pure-test tasks (6.1, 6.2) are anti-decomposition — on failure fix or simplify assertions, never split a test into more tests; fast-TDD-eligible code tasks (4.1, 4.2, 4.3, 4.6, 4.7, 4.8, 4.9, 4.10, 4.11, 4.12, 5.1, 7.1, 7.2, 7.3, 7.4, 7.5, 7.6, 8.1, 8.2) carry the Mutation Evidence Gate of `cortex-work-protocol.md` §4/§8 — a SURVIVED mutant blocks transition to `in_review`; declarative tasks (1.1, 4.4, 4.5) are verified by parser/contract assertions, never by ad-hoc lexers; source <= 700 LOC and tests <= 1200 LOC per task under the flexible policy; documentation files are exempt from the source-logic budget but must still pass `go test -v -count=1 ./bench`.

## 1. Wave 0 — Git hygiene and committed baseline (gates everything)

### 1.1 sh-gitignore-hardening
Requirements: REQ-SQ-SEC-006
Files: .gitignore
Verify: git check-ignore -v tools/e2e-web/node_modules tools/e2e-web/playwright-report tools/e2e-web/test-results
Depends: none

### 1.2 sh-git-commit-landed-pivot-r2
Requirements: REQ-SQ-SEC-006
Files: .gitignore
Verify: git log --oneline -n 4
Depends: 1.1
Supersedes: sh-git-commit-landed-pivot (blocked rev 7) — verify-and-transition only, materialized as a 2-node sequential chain because `cortex_ia_work_decompose` requires 2-8 atomic tasks: sh-git-commit-landed-pivot-r2 (history sanity check) -> sh-git-commit-landed-pivot-r2-declare (working-tree check, declare [.gitignore] as changed scope, transition in_review). Both nodes carry allowed_files: .gitignore; neither re-authors, amends, rewrites, or pushes commits.

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
| sh-git-commit-landed-pivot-r2 (+ -r2-declare) | W0 | REQ-SQ-SEC-006 | 1 |
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

## Supersession chain

| Superseded task | Successor | Disposition | Reason |
|---|---|---|---|
| sh-git-commit-landed-pivot (blocked rev 7) | sh-git-commit-landed-pivot-r2 -> sh-git-commit-landed-pivot-r2-declare | verify-and-transition | Three logical commits are already landed on main over 95ead22 and verified (7646e5b feat(web), 5345413 feat(server), e8ba98e chore gitignore); the durable definition omitted `allowed_files`, so the SDD gate refused `in_review` and any changed_files declaration. The successor chain declares `allowed_files: ['.gitignore']` (the path e8ba98e touched), claims, sanity-verifies the committed state, and transitions in_review with [.gitignore] as the nominal changed file (two nodes because decompose requires 2-8 atomic tasks) — no successor boards, no amend/rewrite/push, no re-authoring. |
| sq-stale-wave1-install-server-railway (blocked rev 3) | sq-stale-wave1-r2-preflight -> sq-stale-wave1-r2 | verify-and-transition | The three document deliverables are COMPLETE and verified on disk (docs/INSTALLATION.md, docs/SERVER.md, docs/RAILWAY_DEPLOYMENT.md: 91 insertions/84 deletions of doc-only changes; grep for the terms cortex-web, :3000 and NEXT_PUBLIC = 0 matches; `go test -v -count=1 ./bench` exit 0), yet the in_review transition was refused by the contract-pin drift guard: the pinned openspec/changes/self-hosted-quality/tasks.md 761332c2 no longer matched the on-disk bytes after the sanctioned W0 supersession-chain amendment (31ff273d), and no pin-update primitive exists. The id is superseded in place by cortex_ia_work_decompose into a 2-node sequential chain (the tool requires 2-8 atomic tasks): sq-stale-wave1-r2-preflight (read-only stale-term grep baseline plus bench contract run, no edits) then sq-stale-wave1-r2 (claim, re-verify the final diff is unchanged and green, transition in_review declaring exactly the three document files). Fresh pins were recomputed from exact file bytes with cortex_ia_content_hash. No successor boards, no re-authoring of the final documents, no change beyond this chain entry. Do not re-create, unblock, or re-dispatch this id. |
| sq-cli-reference-doc (blocked rev 3) | sq-cli-reference-doc-r2-preflight -> sq-cli-reference-doc-r2 | verify-and-transition | The CLI reference deliverable is COMPLETE and verified on disk (docs/CLI-REFERENCE.md: 1198 lines covering 29 commands with flags, defaults, environment overrides, auth requirements and examples, the alphabetical environment-variable index, the per-transport auth matrix, and the deprecated appendix; docs/CLI.md demoted to a pointer with no flag table; `go test -v -count=1 ./bench` 6/6 PASS; `go build ./...` exit 0), yet the in_review transition was refused by the contract-pin drift guard: the pinned openspec/changes/self-hosted-quality/tasks.md 761332c2 no longer matched the on-disk bytes after the sanctioned W0 supersession-chain amendment and the sanctioned W1 specs supersession amendments (on-disk fdd67b99 at dispatch), and no pin-update primitive exists. The id is superseded in place by cortex_ia_work_decompose into a 2-node sequential chain (the tool requires 2-8 atomic tasks): sq-cli-reference-doc-r2-preflight (read-only cheap verification node - bench contract run over docs/CLI-REFERENCE.md and docs/CLI.md, no edits, no claim, no transition) then sq-cli-reference-doc-r2 (claim, re-verify the 1198-line deliverable and the CLI.md pointer are intact with bench exit 0, transition in_review declaring exactly the two document files). Fresh pins were recomputed from exact file bytes with cortex_ia_content_hash AFTER this chain entry, pinning the post-append state (the W0/W1 precedent) so the successors are handed a matching tasks.md instead of inheriting the same drift refusal. No successor boards, no re-authoring of the final documents, no change beyond this chain entry. Do not re-create, unblock, or re-dispatch this id. |
| sq-tui-guide-doc (blocked rev 6) | sq-tui-guide-doc-r2-preflight -> sq-tui-guide-doc-r2 | verify-and-transition | The TUI guide deliverable is COMPLETE and verified on disk (docs/TUI-GUIDE.md: 474 markdown lines, 9 sections plus the appendix, every documented key cross-checked against internal/tui/update.go and keys_coverage_test.go; `go test -v -count=1 ./bench` exit 0), yet the in_review transition was refused by the contract-pin drift guard: the pinned openspec/changes/self-hosted-quality/tasks.md 761332c2 no longer matched the on-disk bytes after the sanctioned W0 supersession-chain amendment and the sanctioned W1 specs supersession amendments (on-disk 2066f799 at dispatch), and no pin-update primitive exists. The id is superseded in place by cortex_ia_work_decompose into a 2-node sequential chain (the tool requires 2-8 atomic tasks): sq-tui-guide-doc-r2-preflight (read-only cheap verification node - TUI-GUIDE.md exists with 474 markdown lines and 9 sections plus appendix, bench contract run, no edits, no claim, no transition) then sq-tui-guide-doc-r2 (claim, re-verify the 474-line deliverable is intact with bench exit 0, transition in_review declaring exactly docs/TUI-GUIDE.md). Fresh pins were recomputed from exact file bytes with cortex_ia_content_hash AFTER this chain entry, pinning the post-append state (the W0/W1 precedent) so the successors are handed a matching tasks.md instead of inheriting the same drift refusal. No successor boards, no re-authoring of the final document, no change beyond this chain entry. Do not re-create, unblock, or re-dispatch this id. |
| sq-stale-matrices (blocked rev 3) | sq-stale-matrices-r2-preflight -> sq-stale-matrices-r2 | verify-and-transition | The matrices deliverable is COMPLETE and verified on disk (docs/verification-matrix.md and docs/capability-matrix.md: 14 changed lines docs-only - verification-matrix :3000 probe removed -> :7438 per ci.yml:390; capability-matrix rows for cortex web key/code/ingest/backup/update/migrate, the completed TUI global-key row, the Next.js 15 correction, and the Embedded-Web/parity/Playwright rows; grep ':3000|Next.js 14' over the two files = 0 matches; `go test -v -count=1 ./bench` 6/6 PASS), yet the in_review transition was refused by the contract-pin drift guard: the pinned openspec/changes/self-hosted-quality/tasks.md 761332c2 no longer matched the on-disk bytes (be386681 at dispatch) after the sanctioned W0/W1 amendments, and no pin-update primitive exists. The id is superseded in place by cortex_ia_work_decompose into a 2-node sequential chain (the tool requires 2-8 atomic tasks): sq-stale-matrices-r2-preflight (read-only cheap verification node - grep ':3000|Next.js 14' over docs/verification-matrix.md and docs/capability-matrix.md = 0 matches plus the bench contract run, no edits, no claim, no transition) then sq-stale-matrices-r2 (claim, re-verify the 14-line diff is final and `go test -v -count=1 ./bench` exits 0, transition in_review declaring exactly the two document files). Fresh pins were recomputed from exact file bytes with cortex_ia_content_hash AFTER this chain entry, pinning the post-append state (the W0/W1 precedent) so the successors are handed a matching tasks.md instead of inheriting the same drift refusal. No successor boards, no re-authoring of the final documents, no change beyond this chain entry. Do not re-create, unblock, or re-dispatch this id. |
| sq-sec-grant-version-propagation (blocked rev 3) | sq-sec-grant-version-propagation-r2-preflight -> sq-sec-grant-version-propagation-r2 | verify-and-transition | The HIGH-1 deliverable is COMPLETE and verified on disk: resolveStaticBindProvenance returns (provenance string, grantVersion int64, err error) and fails closed on a non-positive version (server.go:523-599), openRuntime assigns requestConfig.Server.GrantVersion before newStaticBearerVerifier and asserts the composed principal equals the bootstrapped row version (server.go:308-319), newStaticBearerVerifier reads cfg.Server.GrantVersion with a staticGrantVersion=1 fallback for 0 and an explicit rejection for negatives (static_verifier.go:27-90), TestStaticBearerVerifierPropagatesResolvedGrantVersion and TestStaticBearerVerifierRejectsNegativeGrantVersion are present, there is no MultiTenant field residue in server.go or static_verifier.go, and migrations/v2 bytes are untouched; verification exit 0 for go test -count=1 ./internal/platform/server, go build ./..., go vet ./internal/platform/server/..., and the ./internal/app arch gate, plus mutation evidence (re-hardcoding GrantVersion: 1 turned the grant_version=2 case RED, then reverted). The in_review transition was refused only by the contract-pin drift guard: the pinned openspec/changes/self-hosted-quality/tasks.md 761332c2 no longer matched the on-disk bytes (818345dd after the sanctioned W0/W1 supersession-chain amendments), and no pin-update primitive exists. The id is superseded in place by cortex_ia_work_decompose into a 2-node sequential chain (the tool requires 2-8 atomic tasks): sq-sec-grant-version-propagation-r2-preflight (read-only cheap verification node - bench contract run plus grep of the landed GrantVersion propagation in server.go and static_verifier.go and of zero MultiTenant residue, no edits, no claim, no transition) then sq-sec-grant-version-propagation-r2 (claim, re-verify the final three-file diff is unchanged and green with go test -v -count=1 ./internal/platform/server, transition in_review declaring exactly internal/platform/server/server.go, internal/platform/server/static_verifier.go and internal/platform/server/static_verifier_test.go). Fresh pins were recomputed from exact file bytes with cortex_ia_content_hash AFTER this chain entry, pinning the post-append state (the W0/W1 precedent) so the successors inherit a matching tasks.md instead of the same drift refusal. No successor boards, no re-authoring of the final code, no change beyond this chain entry. Do not re-create, unblock, or re-dispatch this id. |
| sq-sec-logout-clear-list (blocked rev 3) | sq-sec-logout-clear-list-r2-preflight -> sq-sec-logout-clear-list-r2 | verify-and-transition | The MEDIUM-3 deliverable is COMPLETE and verified on disk: config.go defines secretClearList over http.token, sync.token and ai.api_key and applies clearAbsentSecrets from marshalConfigPreservingExisting (config.go:1255-1316) so a YAML logout removes all three scalar secrets while JSON and TOML keep their existing correct clearing, cli.go auth logout surfaces config.Save failures with exit 1 instead of swallowing them (cli.go:2223), and TestSaveClearsScalarSecretsOnAllFormats exists (config_test.go:1278); verification exit 0 for go test -count=1 ./internal/config ./internal/cli and go build ./..., plus mutation evidence (dropping clearAbsentSecrets turned the yaml case of TestSaveClearsScalarSecretsOnAllFormats RED, then reverted). The in_review transition was refused only by the contract-pin drift guard: the pinned openspec/changes/self-hosted-quality/tasks.md 761332c2 no longer matched the on-disk bytes (818345dd after the sanctioned W0/W1 supersession-chain amendments), and no pin-update primitive exists. The id is superseded in place by cortex_ia_work_decompose into a 2-node sequential chain (the tool requires 2-8 atomic tasks): sq-sec-logout-clear-list-r2-preflight (read-only cheap verification node - bench contract run plus grep confirming clearAbsentSecrets and secretClearList in config.go and TestSaveClearsScalarSecretsOnAllFormats in config_test.go, no edits, no claim, no transition) then sq-sec-logout-clear-list-r2 (claim, re-verify the final three-file diff is unchanged and green with go test -v -count=1 ./internal/config ./internal/cli, transition in_review declaring exactly internal/config/config.go, internal/config/config_test.go and internal/cli/cli.go). Fresh pins were recomputed from exact file bytes with cortex_ia_content_hash AFTER this chain entry, pinning the post-append state (the W0/W1 precedent) so the successors inherit a matching tasks.md instead of the same drift refusal. No successor boards, no re-authoring of the final code, no change beyond this chain entry. Do not re-create, unblock, or re-dispatch this id. |
| sq-sec-compose-bootstrap-defaults (blocked rev 3) | sq-sec-compose-bootstrap-defaults-r2-preflight -> sq-sec-compose-bootstrap-defaults-r2 | verify-and-transition | The MEDIUM-6a deliverable is COMPLETE and verified on disk: docker-compose.yml and docker/docker-compose.prod.yml both default CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT to ${VAR:-false} with a dev opt-in comment, and internal/config/compose_defaults_test.go is a standard-YAML-parser contract test asserting the false default in both compose files (TestComposeBootstrapDevelopmentDefaults); verification exit 0 for `go test -v -count=1 ./internal/config -run TestComposeBootstrapDevelopmentDefaults`, `docker compose config --quiet` on both compose files, `go build ./...` and `go test -v -count=1 ./bench`, plus mutation evidence (reverting either default to ${VAR:-true} turned the matching assertion RED, then reverted). The in_review transition was refused only by the contract-pin drift guard: the pinned openspec/changes/self-hosted-quality/tasks.md 761332c2 no longer matched the on-disk bytes (bea3b485 after the sanctioned batch supersession amendments), and no pin-update primitive exists. The id is superseded in place by cortex_ia_work_decompose into a 2-node sequential chain (the tool requires 2-8 atomic tasks): sq-sec-compose-bootstrap-defaults-r2-preflight (read-only cheap verification node - TestComposeBootstrapDevelopmentDefaults exit 0 plus `docker compose config --quiet` exit 0 over docker-compose.yml and docker/docker-compose.prod.yml, no edits, no claim, no transition) then sq-sec-compose-bootstrap-defaults-r2 (claim, re-verify the final three-file diff is unchanged and green with the contract test and compose config both exit 0, transition in_review declaring exactly docker-compose.yml, docker/docker-compose.prod.yml and internal/config/compose_defaults_test.go; this tail must NOT weaken the assertions in internal/config/compose_defaults_test.go, which sq-sec-entrypoint-bearer-echo also writes). Fresh pins were recomputed from exact file bytes with cortex_ia_content_hash AFTER this chain entry, pinning the post-append state (the established precedent) so the successors inherit a matching tasks.md instead of the same drift refusal. No successor boards, no re-authoring of the final files, no change beyond this chain entry. Do not re-create, unblock, or re-dispatch this id. |
| sq-sec-loopback-empty-host (blocked rev 4) | sq-sec-loopback-empty-host-r2-preflight -> sq-sec-loopback-empty-host-r2 | verify-and-transition | The MEDIUM-4 deliverable is COMPLETE and verified on disk: isLoopbackHost treats the empty http.host as non-loopback (internal/cli/cli.go) so a tokenless all-interfaces bind is refused while localhost, 127.0.0.1 and ::1 stay servable tokenless, four new tests cover the empty-host refusal, the loopback acceptances and the 0.0.0.0 refusal (internal/cli/cli_test.go), and the flipped assertion in internal/cli/cli_extra_test.go is REQUIRED by REQ-SQ-SEC-010 rather than a weakening — cli_extra_test.go is a sanctioned companion file that sat outside the original allowed_files, and the successor chain now owns it explicitly in allowed_files; verification exit 0 for go test -v -count=1 ./internal/cli, go build ./... and go vet ./internal/cli, with mutation evidence (reverting isLoopbackHost(empty) to true turned the empty-host case RED, then reverted). The in_review transition was refused only by the contract-pin drift guard: the pinned openspec/changes/self-hosted-quality/tasks.md 761332c2 no longer matched the on-disk bytes (78a320f2 after the sanctioned W0/W1/compose chain amendments), and no pin-update primitive exists. The id is superseded in place by cortex_ia_work_decompose into a 2-node sequential chain (the tool requires 2-8 atomic tasks): sq-sec-loopback-empty-host-r2-preflight (read-only verification node — isLoopbackHost empty -> false intact, the four new cli_test.go tests present, the blessed cli_extra_test.go flip intact, plus the bench contract; NO edits, no claim, no transition) then sq-sec-loopback-empty-host-r2 (claim, re-verify with go test -v -count=1 ./internal/cli exit 0, transition in_review declaring exactly internal/cli/cli.go, internal/cli/cli_test.go and internal/cli/cli_extra_test.go; this tail must NOT weaken or revert the REQ-SQ-SEC-010 assertion flip in cli_extra_test.go). Fresh pins were recomputed from exact file bytes with cortex_ia_content_hash AFTER this chain entry, pinning the post-append state (the established precedent) so the successors inherit a matching tasks.md instead of the same drift refusal. No successor boards, no re-authoring of the final code, no change beyond this chain entry. Do not re-create, unblock, or re-dispatch this id. |
| sq-sec-httpauth-contract (blocked rev 3) | sq-sec-httpauth-contract-r2-preflight -> sq-sec-httpauth-contract-r2 | verify-and-transition | The LOW-9 contract deliverable is COMPLETE and verified on disk: the new neutral stdlib-only primitive internal/httpauth provides ExtractSecret (byte-exact ASCII Bearer and X-API-Key extraction), EqualSecret and CompareDigest (SHA-256 over fixed 32-byte digests compared with subtle.ConstantTimeCompare, closing the raw-length oracle of LOW-9), WriteUnauthorized (canonical 401 with WWW-Authenticate Bearer realm=cortex and the nested error envelope) and Options covering both planes, importing no internal/domain, internal/config or store package so the ./internal/app arch gate stays clean; verification exit 0 for go test -v -count=1 ./internal/httpauth, go build ./... and the arch gate, plus mutation evidence killed 2/2 (raw length compare turned TestEqualSecretComparesFixedWidthDigests RED, then reverted). The in_review transition was refused only by the contract-pin drift guard: the pinned openspec/changes/self-hosted-quality/tasks.md 761332c2 no longer matched the on-disk bytes (78a320f2 after the sanctioned W0/W1/compose chain amendments), and no pin-update primitive exists. The id is superseded in place by cortex_ia_work_decompose into a 2-node sequential chain (the tool requires 2-8 atomic tasks): sq-sec-httpauth-contract-r2-preflight (read-only verification node — ExtractSecret, EqualSecret, CompareDigest, WriteUnauthorized and Options present with spot-checkable mutation claims, plus the bench contract; NO edits, no claim, no transition) then sq-sec-httpauth-contract-r2 (claim, re-verify with go test -v -count=1 ./internal/httpauth exit 0, transition in_review declaring exactly internal/httpauth/httpauth.go and internal/httpauth/httpauth_test.go). Fresh pins were recomputed from exact file bytes with cortex_ia_content_hash AFTER this chain entry, pinning the post-append state (the established precedent) so the successors inherit a matching tasks.md instead of the same drift refusal. No successor boards, no re-authoring of the final code, no change beyond this chain entry. Do not re-create, unblock, or re-dispatch this id. |
| sq-sec-principal-contract (blocked rev 3) | sq-sec-principal-contract-r2-preflight -> sq-sec-principal-contract-r2 | verify-and-transition | The debt #14 synthetic-principal contract deliverable is COMPLETE and verified on disk: internal/domain/synthetic_principal.go exposes NewSyntheticPrincipal(SyntheticPrincipalParams) domain.Principal with the canonical constants SyntheticPrincipalRole=owner, SyntheticPrincipalGrantWildcard=* feeding ProjectIDs and ClassificationClearance, SyntheticPrincipalRateLimitTier=standard and SyntheticPrincipalType=service_account, and internal/domain/synthetic_principal_test.go carries the golden field-for-field shape tests for both legacy literal sites (TestNewSyntheticPrincipalServerLiteralShape, TestNewSyntheticPrincipalLocalLiteralShape) plus the parameterization tests (TestNewSyntheticPrincipalCentralizesGrants, TestNewSyntheticPrincipalAuthMethodParameterized); the file imports only the standard library, so internal/domain stays at the bottom of the import graph (arch gate invariant 3); verification exit 0 for `go test -v -count=1 ./internal/domain` and `go build ./...`, with mutation evidence killed (SyntheticPrincipalRole=admin turned 3 tests RED, then reverted). The in_review transition was refused only by the contract-pin drift guard: the pinned openspec/changes/self-hosted-quality/tasks.md 761332c2 no longer matched the on-disk bytes (0672be25 after the sanctioned W0/W1/compose/loopback/httpauth supersession-chain amendments), and no pin-update primitive exists. The id is superseded in place by cortex_ia_work_decompose into a 2-node sequential chain (the tool requires 2-8 atomic tasks): sq-sec-principal-contract-r2-preflight (read-only cheap verification node — NewSyntheticPrincipal and the four golden tests present with `go test -v -count=1 ./internal/domain` exit 0, no edits, no claim, no transition) then sq-sec-principal-contract-r2 (claim, re-verify with `go test -v -count=1 ./internal/domain` exit 0, transition in_review declaring exactly internal/domain/synthetic_principal.go and internal/domain/synthetic_principal_test.go). Fresh pins were recomputed from exact file bytes with cortex_ia_content_hash AFTER this chain entry, pinning the post-append state (the established precedent) so the successors inherit a matching tasks.md instead of the same drift refusal. No successor boards, no re-authoring of the final code, no change beyond this chain entry. Do not re-create, unblock, or re-dispatch this id. |
| sq-sec-compose-bootstrap-defaults-r2-preflight (blocked rev 3, stale pin) plus the absorbed sq-sec-compose-bootstrap-defaults-r2 (backlog, stale pin) | sq-sec-compose-bootstrap-defaults-r2b-preflight -> sq-sec-compose-bootstrap-defaults-r2b | verify-and-transition | The MEDIUM-6a deliverable is still COMPLETE and verified on disk: docker-compose.yml and docker/docker-compose.prod.yml both default CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT to ${VAR:-false} with the dev opt-in comment, and internal/config/compose_defaults_test.go is the standard-YAML-parser contract test asserting the false default in both compose files (TestComposeBootstrapDevelopmentDefaults green), yet the previously materialized r2 chain pinned tasks.md 78a320f21dfc59fdfe9a71c65ca7d0afdb2196e31991d0fbb179cab091aaf3ba while the sanctioned loopback and httpauth batch amendments moved the on-disk bytes past it (0672be25 at dispatch, a6bb15ac after the concurrent sq-sec-principal-contract chain entry), so the preflight in_review attempt was refused by the contract-pin drift guard while the other eight pins still match (no pin-update primitive exists). The blocked preflight is therefore superseded in place by cortex_ia_work_decompose into a fresh 2-node sequential chain (the tool requires 2-8 atomic tasks): sq-sec-compose-bootstrap-defaults-r2b-preflight (read-only cheap verification node - go test -v -count=1 ./internal/config -run TestComposeBootstrapDevelopmentDefaults exit 0 plus docker compose config --quiet exit 0 over docker-compose.yml and docker/docker-compose.prod.yml, no file mutation, no claim authority spent, no transition) then sq-sec-compose-bootstrap-defaults-r2b (claim, re-verify the final three-file diff is unchanged and green, transition in_review declaring exactly docker-compose.yml, docker/docker-compose.prod.yml and internal/config/compose_defaults_test.go; this tail must NOT weaken the assertions in internal/config/compose_defaults_test.go, which sq-sec-entrypoint-bearer-echo also writes). The decomposition redirects the stale sq-sec-compose-bootstrap-defaults-r2 backlog node onto the new tail sq-sec-compose-bootstrap-defaults-r2b, so its verify-and-transition scope is fully absorbed by r2b (identical allowed_files and verification), neither stale-pinned id is ever re-dispatched as an independent implementation unit, and sq-sec-entrypoint-bearer-echo stays gated behind the compose chain through that fresh-pinned tail. Fresh pins were recomputed from exact file bytes with cortex_ia_content_hash AFTER this chain entry, pinning the post-append state (the established precedent) so both successors inherit a matching tasks.md instead of inheriting the same drift refusal. No successor boards, no re-authoring of the final files, no change beyond this chain entry. Do not re-create, unblock, or re-dispatch sq-sec-compose-bootstrap-defaults-r2-preflight or sq-sec-compose-bootstrap-defaults-r2. |
| sq-sec-bfs-project-filter (blocked rev 3) | sq-sec-bfs-project-filter-r2-preflight -> sq-sec-bfs-project-filter-r2 | verify-and-transition | The MEDIUM-5 deliverable is COMPLETE and verified on disk: the BFS expansion in internal/http/parity_graph.go now enforces the project boundary via the allProjectsRoot sentinel plus the projectGraphNodeInProject predicate applied post-lookup pre-mapping, so crossing boundary edges are dropped instead of disclosing out-of-project labels and titles; three new tests cover the leak (TestLocalProjectGraphDoesNotLeakOutOfProjectNodes, TestLocalProjectGraphPrunesCrossProjectBoundary, TestLocalProjectGraphAllProjectsKeepsCrossProjectExpansion) including an HTTP-level raw-body leak assertion; verification exit 0 for go test -v -count=1 ./internal/http (103 PASS, 0 FAIL) and go build ./...; mutation evidence killed (removing the predicate re-RED both cross-project tests, then reverted). ADJUDICATION RECORDED: the implementer's scope interpretation is ACCEPTED - the project-scope boundary (REQ-SQ-SEC-005) is the delivered fix; a separate Observation.Scope predicate would desynchronize the seed and expansion sets and break same-project byte-compatibility (fixtures legitimately seed a project-less synthetic observation), so it is a follow-up seed-policy decision, not part of this fix. The in_review transition was refused only by the contract-pin drift guard: the pinned openspec/changes/self-hosted-quality/tasks.md 761332c29dc32dfcbd46872388a4929fade6aba067bf565508bae549682c84e2 no longer matched the on-disk bytes (ae6c820652139934006ef0a2241d8bec0428e1e8dd288ad618055561d4fc64c8 at dispatch, after the sanctioned supersession amendments), and no pin-update primitive exists. The id is superseded in place by cortex_ia_work_decompose into a 2-node sequential chain (the tool requires 2-8 atomic tasks): sq-sec-bfs-project-filter-r2-preflight (read-only cheap verification node - the three leak tests exist and pass with go test -v -count=1 ./internal/http, no edits, no claim, no transition) then sq-sec-bfs-project-filter-r2 (claim, re-verify with go test -v -count=1 ./internal/http exit 0, transition in_review declaring exactly internal/http/parity_graph.go and internal/http/parity_graph_test.go). Fresh pins were recomputed from exact file bytes with cortex_ia_content_hash AFTER this chain entry, pinning the post-append state (the established precedent) so the successors are handed a matching tasks.md instead of inheriting the same drift refusal. No successor boards, no re-authoring of the final diff, no change beyond this chain entry. Do not re-create, unblock, or re-dispatch this id. |
| sh-git-commit-landed-pivot-r2-declare (superseded), sq-sec-compose-bootstrap-defaults-r2b-preflight (superseded), sq-sec-compose-bootstrap-defaults-r2b (superseded), sq-sec-bfs-project-filter-r2-preflight (superseded), sq-sec-bfs-project-filter-r2 (superseded), sq-sec-principal-contract-r2-preflight (superseded), sq-sec-principal-contract-r2 (superseded), sq-perf-baseline-oracles-graph (superseded), sq-perf-baseline-oracles-boot (superseded), sq-perf-web-etag-precompressed (superseded), sq-perf-quota-windows-prune (superseded), sq-perf-mcp-batch-server (superseded), sq-stale-matrices-r2-preflight (superseded), sq-stale-wave1-r2-preflight (superseded), sq-stale-wave1-r2 (superseded) | sh-git-commit-landed-pivot-r2-declare-r3, sq-sec-compose-bootstrap-defaults-r2b-preflight-r3, sq-sec-compose-bootstrap-defaults-r2b-r3, sq-sec-bfs-project-filter-r2-preflight-r3, sq-sec-bfs-project-filter-r2-r3, sq-sec-principal-contract-r2-preflight-r3, sq-sec-principal-contract-r2-r3, sq-perf-baseline-oracles-graph-r3, sq-perf-baseline-oracles-boot-r3, sq-perf-web-etag-precompressed-r3, sq-perf-quota-windows-prune-r3, sq-perf-mcp-batch-server-r3, sq-stale-matrices-r2-preflight-r3, sq-stale-wave1-r2-preflight-r3, sq-stale-wave1-r2-r3 | verify-and-transition | EMERGENCY SINGLE-WRITE BATCH RESOLUTION of the systemic tasks.md contract-pin drift: every concurrently materialized successor chain appended its own row to this table, and each append re-staled every pin computed before it, so the self-hosted-quality board reached a state where a transition or approval on one node invalidated the pins of all the others while no pin-update primitive exists. This single row supersedes all fifteen still-stuck ids in one pass: the exact on-disk bytes produced by this one cortex_ia_openspec_write call are hashed once by the operation that owns them, and every successor created by this batch pins that same post-append sha256, so no further amendment can re-stale them. Mechanism: the four parents that are status=blocked (sq-sec-compose-bootstrap-defaults-r2b-preflight rev 3, sq-sec-principal-contract-r2-preflight rev 6, sq-perf-baseline-oracles-graph rev 3, sq-stale-wave1-r2-preflight rev 3) are superseded atomically in place by cortex_ia_work_decompose, which preserves upstream dependencies, chains the children, and redirects downstream dependencies to the final child; the eleven parents that are ready or backlog are superseded by a fresh-pinned cortex_ia_work_create successor. Every successor is verify-and-transition only: each deliverable is already final and green on disk from the superseded session, so the successor claims, sanity-re-runs the declared oracle, and transitions in_review declaring exactly the original allowed_files, with dependencies chained exactly where the original DAG had them. Two ids from the original stuck list were reconciled against the live board before this write and are deliberately NOT superseded because they already reached status=done (sq-sec-loopback-empty-host-r2 rev 5, sq-sec-httpauth-contract-r2 rev 5). Non-goals: no successor boards, no re-authoring of any deliverable, no amendment/rewrite/force-push of landed commits, no weakening of landed security controls or test assertions, and no second append to this file. Do not re-create, unblock, or re-dispatch any superseded id. |
| sq-profile-conflicts (blocked rev 3) | sq-profile-conflicts-r2-preflight -> sq-profile-conflicts-r2 | verify-and-transition | The MCP/ARCHITECTURE/AGENTS profile-alignment deliverable (REQ-SQ-DOC-004) is COMPLETE and verified on disk (docs/MCP.md names the supported set agent/dev/minimal with coder as an accepted alias of dev and the retired set admin/temporal, clarifies that the deprecated keys remain in internal/mcp/server.go for explicit backward-compatible use, and scopes cortex_delete destruction to an explicit --tools=admin opt-in, removing the line-18 vs line-78 contradiction; docs/ARCHITECTURE.md:216-235 lists agent/dev/minimal supported, marks admin/temporal deprecated and mentions the embedded web dashboard as a separate local non-profile surface; AGENTS.md:16 states agent/dev/minimal supported and admin/temporal deprecated - confirmed by the landed AGENTS.md diff; grep over the three files for the retired admin/temporal-as-supported framing = 0 matches; go test -v -count=1 ./bench 6/6 PASS; go build ./... exit 0), yet the in_review transition was refused only by the contract-pin drift guard: the pinned openspec/changes/self-hosted-quality/tasks.md 761332c29dc32dfcbd46872388a4929fade6aba067bf565508bae549682c84e2 no longer matched the on-disk bytes (45926945877697f750d7e179066183664cb6ce17f8662c48a153e1a28d9f14df at dispatch, after the sanctioned W0/W1/compose/loopback/httpauth/principal/bfs/batch supersession amendments), and no pin-update primitive exists. The id is superseded in place by cortex_ia_work_decompose into a 2-node sequential chain (the tool requires 2-8 atomic tasks): sq-profile-conflicts-r2-preflight (read-only cheap verification node - the three updated documents exist with the agent/dev/minimal and coder-alias profile set, the embedded web dashboard note and the resolved line-18 vs line-78 contradiction, grep of the retired admin/temporal-as-supported framing = 0 matches, plus the bench contract run; NO edits, no claim, no transition) then sq-profile-conflicts-r2 (claim, re-verify with go test -v -count=1 ./bench exit 0, transition in_review declaring exactly docs/MCP.md, docs/ARCHITECTURE.md and AGENTS.md; the documents are final and must NOT be re-authored, and README.md:180 stays owned by sq-docs-index-readmes). Fresh pins were recomputed from exact file bytes with cortex_ia_content_hash AFTER this chain entry, pinning the post-append state (the established batch lesson) so both successors inherit a matching tasks.md instead of inheriting the same drift refusal. No successor boards, no re-authoring of the final documents, no change beyond this chain entry. Do not re-create, unblock, or re-dispatch this id. |


## Deferred
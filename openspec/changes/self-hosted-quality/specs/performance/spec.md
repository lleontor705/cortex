# Delta for performance — Measurement-first quick wins and MCP batch

Adds the performance contract from the ranked top-10 investigation: baseline capture before optimization, six quick wins, proposal 2 as the only implementation-grade proposal, and a roadmap document for the rest.

## ADDED Requirements

### Requirement: REQ-SQ-PERF-001: Baseline is captured before any optimization
Before any quick win lands, the Step-1 micro-benchmark suites MUST be executed and their numbers recorded in task receipts (`internal/store/search`, `internal/store/sqlite`, `internal/retrieval`, `internal/store/graph`, `internal/store/bundle`), and the five missing benchmark oracles MUST exist: `BenchmarkProjectGraph` and `BenchmarkStatsHandler` (`internal/http`), `BenchmarkGraphTools` with a query-count assertion (`internal/mcp`), `BenchmarkOpen` (`internal/app`), and `BenchmarkView` (`internal/tui`).

#### Scenario: oracles compile and run (Happy)
- GIVEN the oracle tasks complete
- WHEN `go test -count=1 -bench=. -benchtime=1x -run '^$' ./internal/http ./internal/mcp ./internal/store/search ./internal/store/sqlite ./internal/store/graph ./internal/store/bundle ./internal/retrieval` runs
- THEN the command exits 0 and all five new benchmarks execute

#### Scenario: baseline recorded (Edge)
- GIVEN the capture task receipt
- WHEN it is read
- THEN per-benchmark ns/op numbers for the Step-1 packages are present for before/after comparison

#### Scenario: oracle missing (Error)
- GIVEN a named oracle absent from its package
- WHEN the verification command runs
- THEN the task fails until the benchmark exists

#### Scenario: optimization precedes baseline (Error)
- GIVEN a quick-win task dispatched before 6.1/6.2 complete
- WHEN the orchestrator checks the DAG
- THEN dispatch is blocked by the dependency edge

### Requirement: REQ-SQ-PERF-002: Quick wins land with oracle or test evidence
The quick wins MUST land as separate tasks with disjoint files: TUI idle tick gating (`internal/tui/model.go:143-147`, `update.go:139-141`, ~8.3Hz -> 0 at idle), agent-quota window prune (`internal/platform/server/agent_limits.go:73-87`, unbounded growth), comparator hoist (`internal/http/parity_graph.go:146-149`), stats cache TTL (`internal/http/parity_ops.go:75-200`), embedded-web ETag/304 plus precompressed `.gz` assets (`internal/web/server.go:219-247`, `Makefile`), and graph-tool label projection (drop observation content from large graph payloads in `internal/mcp/tools_cortex.go`).

#### Scenario: idle CPU reduced (Happy)
- GIVEN tick gating landed
- WHEN the TUI sits idle
- THEN no periodic tick message is produced and `go test -v -count=1 ./internal/tui` passes

#### Scenario: transfer reduced (Edge)
- GIVEN ETag/304 support
- WHEN a conditional request repeats for an unchanged asset
- THEN the server answers 304 with no body and `go test -v -count=1 ./internal/web` passes

#### Scenario: behavior preserved (Error)
- GIVEN any quick win
- WHEN a JSON envelope, auth result, or graph semantics changes
- THEN the task fails review as a contract regression

### Requirement: REQ-SQ-PERF-003: MCP graph queries batched (proposal 2)
The observation-graph N+1 MUST be reduced to a bounded number of queries on both transports: local `internal/mcp/tools_cortex.go:1266-1297` MUST replace the per-observation `GetEdgesForObservation` loop with a batched fetch (500 -> 2 queries), and the server-side handler at `internal/platform/server/http.go:2193-2320` MUST apply the equivalent batching, with output byte-identical to today for equivalent inputs.

#### Scenario: query count bounded (Happy)
- GIVEN a 500-observation project
- WHEN the graph tool runs under `BenchmarkGraphTools`
- THEN the recorded query count is at most 2 and the benchmark exits 0

#### Scenario: identical payload (Edge)
- GIVEN the batched implementation
- WHEN its output is compared with the pre-change output fixture
- THEN nodes and edges match exactly

#### Scenario: query count regresses (Error)
- GIVEN a batched implementation still issuing per-node queries
- WHEN the query-count assertion runs
- THEN the task fails until the batch is real

#### Scenario: server transport diverges (Edge)
- GIVEN local batching landed but server transport untouched
- WHEN the roadmap documents status
- THEN the server half is listed as outstanding rather than claimed complete

### Requirement: REQ-SQ-PERF-004: docs/PERFORMANCE-ROADMAP.md carries proposals 1, 3, 4 and the remainder
`docs/PERFORMANCE-ROADMAP.md` MUST document the landed quick wins and MCP batch with their measured before/after numbers, the deferred proposals in rank order (1 per-request audit+bind elimination, 3 two-phase vector search, 4 ProjectGraph one-pass SQL, 7 search round-trip reduction, 9 CLI startup fast path, 10 watch batching, 5 stats TTL follow-through, 6 ETag follow-through), each with file/line anchors, expected gain, sizing, and risk, plus the full measurement plan (baseline benches, new oracles, profiling step, server-track CI `pg_stat_statements` diff).

#### Scenario: every top-10 item accounted for (Happy)
- GIVEN the roadmap
- WHEN it is cross-checked against the investigation's ranked top-10
- THEN each item is either marked landed with numbers or deferred with sizing and a reason

#### Scenario: measurement plan executable (Edge)
- GIVEN the measurement plan section
- WHEN its commands are run
- THEN they are the same raw commands used by the baseline and oracle tasks

#### Scenario: landed claims carry numbers (Edge)
- GIVEN each landed quick win
- WHEN its roadmap entry is read
- THEN before/after measurements from the oracles are cited

#### Scenario: item silently dropped (Error)
- GIVEN a top-10 item absent from the roadmap
- WHEN the cross-check runs
- THEN the task fails until the item is added

### Requirement: REQ-SQ-PERF-005: Performance work preserves all contracts
No performance task may change response JSON shapes, authentication results, retrieval relevance, or the offline retrieval baseline; `go test -v -count=1 ./bench ./bench/common ./bench/cortex ./bench/fixtures/cortex-native ./bench/cortex/cmd/baseline` MUST stay green after every perf task.

#### Scenario: retrieval gate green (Happy)
- GIVEN any perf task landed
- WHEN the offline retrieval gate runs
- THEN the suite exits 0

#### Scenario: race gate considered (Edge)
- GIVEN shared-state edits (quota windows, stats TTL, tick gating)
- WHEN `go test -race -count=1 ./internal/store/search ./internal/store/bundle ./internal/mcp` runs in CI
- THEN no new race is reported (local execution BLOCKED without CGO/gcc is reported as such)

#### Scenario: JSON shape drift (Error)
- GIVEN a perf diff that renames or nests a response field
- WHEN contract tests run
- THEN the task fails until the original shape is restored

#### Scenario: relevance drift (Error)
- GIVEN a retrieval-path edit
- WHEN the offline baseline gate runs
- THEN a metric regression blocks the task

### Requirement: REQ-SQ-PERF-006: Server-track optimization stays roadmap-only
Proposal 1 (per-request audit+bind transaction, `platform/server/server.go:282-298`, `authz/policy.go:124-134`, `store/postgres/audit.go:29-43`, migration-112 SQL) MUST NOT be implemented in this change because it is security-adjacent and touches the migration-112 contract; it is documented with its 30-60% p50 expectation and a CI-authoritative measurement path.

#### Scenario: no server auth-path edits for perf (Happy)
- GIVEN the perf waves complete
- WHEN the diff for `internal/authz`, `internal/store/postgres/audit.go`, and `migrations/v2` is inspected
- THEN no perf-motivated change exists there

#### Scenario: proposal still quantified (Edge)
- GIVEN the roadmap entry for proposal 1
- WHEN it is read
- THEN the expected 30-60% p50 gain, sizing, and risk are stated

#### Scenario: unauthorized implementation (Error)
- GIVEN a diff touching the authz/audit request path for performance
- WHEN review checks it against this requirement
- THEN the task is rejected and reverted to roadmap status

#### Scenario: measurement deferred to CI (Edge)
- GIVEN the server track cannot be measured locally
- WHEN the roadmap describes verification
- THEN CI `pg_stat_statements` diff is named as the authoritative measurement

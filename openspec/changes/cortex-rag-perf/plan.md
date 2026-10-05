# Plan: cortex-rag-perf RAG and Vector Performance Roadmap

## Intent

Consolidate the Top 8 product-audit improvements (evidence phase PASS) with the paper-derived RAG/Adaptive-RAG improvements (2023-2026 literature review, phase PASS) into one dependency-safe, maximally parallelizable execution DAG for the cortex Go repository. Overlapping items are merged into a single coherent vector-engine track: audit item 4 (O(N) cosine BLOB scan, post-filter only) is absorbed into the scan modernization and filtered-search requirements; audit item 8 (degraded-mode surfacing) is absorbed into the default-on requirement. No duplicate tasks target the same seam.

Hard constraints (apply to every task): Zero-CGO and no external services in local mode (enforced by internal/app/arch_test.go); workload policy flexible (source <= 700 LOC Go, tests <= 1200 LOC, modular test files <= 250 LOC); SQLite v2 migration SQL 001/002 is immutable; MCP namespace stays cortex_*; admin/temporal profiles stay retired from standard discovery.

Non-goals (explicitly out of scope):
1. No production code edits by planning; this plan only specifies, implementation happens via dispatched minions.
2. P4 CoRAG iterative reformulate-retrieve loop is DEFERRED: it conflicts with the SkewRoute task on the Adaptive-RAG seam, has the lowest value/effort ratio of the paper set, and its adaptive-stopping logic depends on the eval baseline. Re-propose after the eval task lands.
3. No PostgreSQL/vector external-adapter changes (qdrant/pgvector suites untouched).
4. No server/auth composition changes (internal/platform/server untouched).
5. No v1-to-v2 database upgrade path work.
6. No web frontend (web/) changes.

## Requirements


### Requirement: REQ-MCP-001 MCP profile metadata single source of truth (audit item 1)
Requirements: REQ-MCP-001

The MCP layer MUST derive every supported-profile listing from exactly one exported declaration in internal/mcp/server.go. internal/mcp/tools_cortex.go status output and AGENTS.md MUST agree with that declaration, and retired admin/temporal profiles MUST NOT appear in standard discovery or advertised profile lists.

#### Scenario: consistent profile advertisement (Happy path)
- GIVEN internal/mcp/server.go:133-140 declares Profiles agent/dev/coder/minimal plus retired admin/temporal while internal/mcp/tools_cortex.go:1596 advertises agent/admin/temporal and AGENTS.md documents agent/dev/minimal
- WHEN any code path or tool output needs the supported profile list
- THEN it derives from the single exported declaration in internal/mcp/server.go and the MCP status tool output agrees with the declaration

#### Scenario: legacy coder alias resolution (Edge case)
- GIVEN a legacy client resolves the historical coder profile alias retained for compatibility
- WHEN the alias is resolved through the shared declaration
- THEN coder maps to the dev toolset deterministically and no third copy of the mapping exists

#### Scenario: retired profile requested (Error state)
- GIVEN a client requests the retired admin or temporal profile
- WHEN profile resolution runs
- THEN the request is rejected with an actionable error, the profile never appears in advertised lists, and a regression test rejects admin/temporal in advertised profiles

### Requirement: REQ-CI-001 Re-land lint fix with CI enforcement (audit item 3)
Requirements: REQ-CI-001

The CI lint stage MUST enforce errcheck/staticcheck with the reverted fix e762f82 re-applied, so regressions fail the lint job.

#### Scenario: lint gate fails on regression (Happy path)
- GIVEN commit e762f82 errcheck/staticcheck fixes were reverted and CI currently tolerates the violations
- WHEN a change reintroduces any violation after the fix is re-landed
- THEN the CI lint job fails with the offending file and rule named

#### Scenario: deterministic scope of enforcement (Edge case)
- GIVEN the re-landed configuration runs golangci-lint across all first-party packages
- WHEN the lint stage executes
- THEN enforcement scope is explicit and deterministic with no per-developer divergence between local make lint and CI

#### Scenario: linter unavailable in runner (Error state)
- GIVEN the golangci-lint binary or pinned version is unavailable in the CI runner
- WHEN the lint job executes
- THEN the job fails closed with a tooling error instead of silently passing

### Requirement: REQ-VEC-DEFAULT-001 Vector search default-on with loud degradation (audit items 2 and 8)
Requirements: REQ-VEC-DEFAULT-001

The default build MUST provide functional local vector search without extra tags, and degraded vector mode MUST be surfaced loudly via cortex doctor and MCP status guidance instead of failing silently.

#### Scenario: out-of-the-box semantic search (Happy path)
- GIVEN make build (Makefile lines 66-70) compiles without -tags cortex_vectors and currently ships the degraded ErrVectorSearchDisabled stub silently
- WHEN a user builds with the default target and runs semantic search
- THEN the default binary serves functional zero-CGO sqlite_blob vector search with no extra build flags

#### Scenario: degraded mode surfaced by doctor (Edge case)
- GIVEN the vector index is degraded or disabled in the running composition
- WHEN a user runs cortex doctor or queries MCP status guidance
- THEN the output names the degraded state and the remediation explicitly

#### Scenario: silent corruption stays fixed (Error state)
- GIVEN a vector dimension mismatch or disabled-index error occurs during upsert or search
- WHEN the vector path processes the request
- THEN the error surfaces loudly to the caller with the preserved dimension-mismatch rejection semantics and never degrades to silent zero scores

### Requirement: REQ-LME-001 LongMemEval design pack (paper P1)
Requirements: REQ-LME-001

Retrieval MUST add fact-augmented FTS5 key expansion, time-aware query expansion over timestamped events, session decomposition for long sessions, and a Chain-of-Note reading stage fused into final ranking (target +5-11 percent recall/QA on LongMemEval).

#### Scenario: fact-augmented key recall (Happy path)
- GIVEN FTS5 keys currently index session text only
- WHEN a memory is saved and later queried by a fact stated in passing within the session
- THEN the extracted-fact keys match the query and the target session is recalled

#### Scenario: time-aware range pruning (Edge case)
- GIVEN a timestamped events table exists and the query implies a temporal constraint
- WHEN time-aware query expansion runs
- THEN the inferred time range prunes candidate events before scoring and recall on time-targeted questions improves

#### Scenario: malformed timestamps fall back safely (Error state)
- GIVEN event timestamps are missing or malformed for some sessions
- WHEN time-aware expansion runs
- THEN expansion falls back to unpruned retrieval for those sessions without erroring or dropping candidates

### Requirement: REQ-ROUTE-001 SkewRoute-style training-free routing (paper P3)
Requirements: REQ-ROUTE-001

The 4-tier Adaptive-RAG classifier in internal/retrieval/adaptive.go MUST consume fusion-quality features (score skewness and top-1 margin from RRF fusion) to cut large-LLM calls by about 50 percent at no more than 1 percent F1 loss, verified against the offline baseline gate.

#### Scenario: peaked distribution routes cheap (Happy path)
- GIVEN RRF fusion produces a peaked score distribution with a large top-1 margin
- WHEN the Adaptive-RAG classifier routes the query
- THEN the query routes to a cheap tier and the large-LLM call is skipped with comparable answer quality

#### Scenario: flat distribution escalates (Edge case)
- GIVEN RRF fusion produces a flat distribution with high skewness-derived ambiguity
- WHEN the classifier routes the query
- THEN the query escalates to a heavier tier preserving answer quality within the 1 percent F1 budget

#### Scenario: missing fusion features fall back (Error state)
- GIVEN fusion features are unavailable for a query path
- WHEN the classifier routes the query
- THEN routing falls back to the existing heuristic without crashing or misclassifying

### Requirement: REQ-HIPPORAG2-001 HippoRAG 2 upgrades (paper P2)
Requirements: REQ-HIPPORAG2-001

The graph retrieval engine MUST treat passages as first-class graph nodes, fuse dense retrieval scores as PPR seed weights, and prune post-PPR candidates with a recognition-memory filter (target +7 F1 multi-hop without exceeding the 12x-cheaper indexing bound).

#### Scenario: multi-hop retrieval with passage nodes (Happy path)
- GIVEN the current PPR implementation in internal/domain/graph treats passages implicitly
- WHEN a multi-hop query runs
- THEN passages participate as first-class nodes, dense scores seed the PPR run, and multi-hop F1 improves on the offline oracle

#### Scenario: recognition filter prunes post-PPR (Edge case)
- GIVEN PPR returns a large candidate set with many weakly related passages
- WHEN the recognition-memory filter runs
- THEN weak candidates are pruned before final fusion while the requested top-k remains filled with relevant passages

#### Scenario: empty or sparse graph fallback (Error state)
- GIVEN the memory graph is empty or too sparse for meaningful PPR
- WHEN retrieval runs
- THEN the engine falls back to the dense-plus-lexical fusion path without erroring

### Requirement: REQ-VEC-SCAN-001 Vector scan modernization (paper P5 plus audit item 4)
Requirements: REQ-VEC-SCAN-001

The zero-CGO vector scan in internal/vector/sqlite_blob/adapter.go and internal/store/sqlite MUST use MRL Matryoshka prefix scan with int8 quantization two-pass refinement, pre-normalized vectors, and goroutine-parallel row scanning (target 8-48x storage reduction and 2-4x throughput), preserving dimension-mismatch rejection semantics.

#### Scenario: coarse-to-fine scan returns accurate results (Happy path)
- GIVEN the scan delegates today to an O(N) post-filter cosine BLOB scan in internal/store/sqlite/vector_store*.go
- WHEN vector search executes over N embeddings
- THEN the coarse MRL prefix pass shortlists candidates and the int8 refinement pass returns results within the accepted recall tolerance of the exact scan

#### Scenario: parallel scan keeps deterministic ordering (Edge case)
- GIVEN the row scan is distributed across goroutines
- WHEN many rows are scanned concurrently
- THEN results preserve deterministic score ordering and no data race is reported under the CI race gate packages

#### Scenario: dimension mismatch rejection preserved (Error state)
- GIVEN an upsert carries a vector whose dimension differs from the declared ModelInfo.Dimension
- WHEN the modernized scan path processes the upsert
- THEN the vector is rejected with domain.ErrDimensionMismatch and never stored, matching existing REQ-VEC-001 semantics

### Requirement: REQ-VEC-FILTER-001 Filtered-search cardinality switch (paper P7)
Requirements: REQ-VEC-FILTER-001

Filtered vector search MUST switch between exact full scan and constrained scan based on a deterministic filter-selectivity cardinality heuristic, eliminating post-filter starvation.

#### Scenario: selective filter uses exact scan (Happy path)
- GIVEN metadata-filtered vector searches starve today under pure post-filtering
- WHEN a search runs with a highly selective filter
- THEN the engine picks the exact scan and returns the full requested top-k when matching candidates exist

#### Scenario: low-selectivity filter uses constrained scan (Edge case)
- GIVEN a filter matches a large fraction of rows
- WHEN the cardinality heuristic evaluates selectivity
- THEN the engine picks the constrained scan path and meets latency targets while filling top-k

#### Scenario: zero matching candidates (Error state)
- GIVEN no rows match the filter
- WHEN a filtered search runs
- THEN the engine returns an empty result with a clear no-candidates signal rather than an ambiguous starvation artifact

### Requirement: REQ-DRIVER-001 modernc.org/sqlite bump plus sqlite-vec port (paper P6)
Requirements: REQ-DRIVER-001

The local composition MUST run on the current pure-Go modernc.org/sqlite release with the built-in sqlite-vec port blank-imported, keeping zero-CGO, and all local composition, migration preflight/post-apply, and checksum gates MUST pass unchanged.

#### Scenario: bump and blank import succeed (Happy path)
- GIVEN go.mod pins modernc.org/sqlite v1.47.0
- WHEN the dependency is upgraded to the current release and modernc.org/sqlite/vec is blank-imported at the composition root (internal/database/database.go)
- THEN the zero-CGO built-in sqlite-vec SIMD brute-force becomes available and the full default test gate passes

#### Scenario: existing v2 databases open unchanged (Edge case)
- GIVEN an existing v2 SQLite database with recorded checksums opens under the new driver
- WHEN app startup performs the read-only compatibility probe
- THEN the database opens, migration ledger checks pass, and no mutation occurs during the probe

#### Scenario: incompatible database refused without mutation (Error state)
- GIVEN a v1, foreign, corrupt, or checksum-mismatched database is presented
- WHEN startup validation runs under the bumped driver
- THEN the database is refused without mutation exactly as before the bump

### Requirement: REQ-EVAL-001 Published eval harness (paper P8)
Requirements: REQ-EVAL-001

The offline bench suite MUST publish LongMemEval and LOCOMO scores with the fixed-judge protocol and per-task before/after comparison, gated by the existing baseline contracts.

#### Scenario: before/after score publication (Happy path)
- GIVEN bench/ already contains longmemeval, locomo, and dmr runners plus the fixed-judge protocol in bench/common/llm_judge.go
- WHEN the retrieval and vector improvements land
- THEN the offline suite publishes LongMemEval and LOCOMO scores with per-task before/after comparison under the fixed-judge protocol

#### Scenario: judge unavailable fails blocked (Edge case)
- GIVEN the fixed-judge model or endpoint is unavailable in the environment
- WHEN the eval gate executes
- THEN the gate reports BLOCKED with an actionable message instead of silently skipping or fabricating scores

#### Scenario: gate threshold regression (Error state)
- GIVEN a landed change regresses published scores below the recorded baseline gate
- WHEN the eval suite runs
- THEN the suite exits nonzero with the regressing task and delta identified

## Design

Seam map: MCP metadata targets internal/mcp/server.go (Profiles map) plus internal/mcp/tools_cortex.go (status output); CI enforcement targets .github/workflows/ci.yml plus Makefile; vector default-on targets Makefile plus internal/cli/cli.go (doctor); FTS keys, time-aware expansion, and Chain-of-Note target internal/store/search/store.go plus internal/retrieval/retrieval.go and late_interaction.go; routing targets internal/retrieval/adaptive.go; graph upgrades target internal/domain/graph/ppr.go and graph.go plus internal/retrieval/crag.go; vector scan targets internal/vector/sqlite_blob/adapter.go plus internal/store/sqlite/vector_store.go; driver bump targets go.mod, go.sum, and the internal/database/database.go blank import; evaluation targets bench/longmemeval, bench/locomo, and bench/common.

Wave structure: Wave 1 runs quick wins and independent engine tracks in parallel (T01, T02, T03, T07, T09 with mutually disjoint writable files). Wave 2 runs the retrieval-quality slices in parallel (T04, T05, T06) and executes T08 after T07 because both mutate the same sqlite_blob scan seam. Wave 3 runs the evaluation harness (T10) after T04, T05, and T07 because it measures their deltas.

Trade-off: T08 is serialized behind T07 instead of parallelized solely to avoid a writable-file collision on the adapter seam; every other edge in the DAG is a genuine data or measurement prerequisite, maximizing parallelism.

Verification strategy: every task carries one raw standalone command. Retrieval changes respect the offline baseline gate (make test-baseline); migration-adjacent changes respect the internal/migration suite; race-sensitive scan changes run under the CI race gate packages. Only deterministic command exit status counts as evidence.

## Tasks

- [ ] rag-t01-mcp-profile-metadata [mcp] Single source of truth for MCP profile metadata
  Requirements: REQ-MCP-001
  Allowed files target: internal/mcp/server.go; internal/mcp/tools_cortex.go
  Verification: go test -v -count=1 ./internal/mcp
  Forecast: <= 120 changed lines Go, <= 700 Go cap.

- [ ] rag-t02-lint-reland [ci] Re-land e762f82 lint fix with CI enforcement
  Requirements: REQ-CI-001
  Allowed files target: .github/workflows/ci.yml; Makefile
  Verification: golangci-lint run ./...
  Forecast: <= 100 changed lines config, fixes bounded by lint output.

- [ ] rag-t03-vector-default-on-doctor [cli] Default-on local vector search with loud degraded-mode surfacing
  Requirements: REQ-VEC-DEFAULT-001
  Allowed files target: Makefile; internal/cli/cli.go; internal/cli/vector_build_tag_test.go
  Verification: make build && go test -v -count=1 ./internal/cli
  Forecast: <= 200 changed lines Go/Make, <= 700 Go cap.

- [ ] rag-t04-longmemeval-pack [retrieval] LongMemEval design pack: fact keys, time-aware expansion, session decomposition, Chain-of-Note
  Requirements: REQ-LME-001
  Allowed files target: internal/store/search/store.go; internal/retrieval/retrieval.go; internal/retrieval/late_interaction.go
  Verification: go test -v -count=1 ./internal/store/search ./internal/retrieval
  Forecast: <= 500 changed lines Go, <= 700 Go cap.

- [ ] rag-t05-skewroute-routing [retrieval] SkewRoute fusion-skew features into the Adaptive-RAG classifier
  Requirements: REQ-ROUTE-001
  Allowed files target: internal/retrieval/adaptive.go; internal/retrieval/adaptive_test.go
  Verification: go test -v -count=1 ./internal/retrieval -run TestAdaptive
  Forecast: <= 200 changed lines Go, <= 700 Go cap.

- [ ] rag-t06-hipporag2 [graph] HippoRAG 2: passages as nodes, dense seed weights, recognition-memory filter
  Requirements: REQ-HIPPORAG2-001
  Allowed files target: internal/domain/graph/ppr.go; internal/domain/graph/graph.go; internal/retrieval/crag.go
  Verification: go test -v -count=1 ./internal/domain/graph ./internal/retrieval
  Forecast: <= 500 changed lines Go, <= 700 Go cap.

- [ ] rag-t07-vector-scan-modernization [vector] Vector scan modernization: MRL prefix scan, int8 quantization, parallel scan
  Requirements: REQ-VEC-SCAN-001
  Allowed files target: internal/vector/sqlite_blob/adapter.go; internal/store/sqlite/vector_store.go; internal/vector/sqlite_blob/adapter_vectors_test.go
  Verification: go test -v -count=1 -tags cortex_vectors ./internal/vector/sqlite_blob
  Forecast: <= 600 changed lines Go, <= 700 Go cap.

- [ ] rag-t08-filtered-cardinality [vector] Filtered-search cardinality switch for vector search
  Requirements: REQ-VEC-FILTER-001
  Allowed files target: internal/vector/sqlite_blob/adapter.go; internal/store/sqlite/vector_store.go
  Verification: go test -v -count=1 -tags cortex_vectors ./internal/vector/sqlite_blob
  Dependencies: rag-t07-vector-scan-modernization
  Forecast: <= 250 changed lines Go, <= 700 Go cap.

- [ ] rag-t09-driver-bump-sqlite-vec [deps] Bump modernc.org/sqlite and blank-import the built-in sqlite-vec port
  Requirements: REQ-DRIVER-001
  Allowed files target: go.mod; go.sum; internal/database/database.go
  Verification: go build ./... && go test -count=1 ./internal/migration ./internal/database
  Forecast: dependency bump plus <= 30 changed lines Go.

- [ ] rag-t10-eval-harness [bench] Publish LongMemEval and LOCOMO scores with the fixed-judge protocol
  Requirements: REQ-EVAL-001
  Allowed files target: bench/longmemeval/runner.go; bench/locomo/runner.go; bench/common/report.go
  Verification: make test-baseline
  Dependencies: rag-t04-longmemeval-pack, rag-t05-skewroute-routing, rag-t07-vector-scan-modernization
  Forecast: <= 400 changed lines Go, <= 700 Go cap.

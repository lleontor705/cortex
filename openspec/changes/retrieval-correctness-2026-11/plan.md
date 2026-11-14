# Retrieval Correctness 2026-11 (Plan)

- Change ID: retrieval-correctness-2026-11
- Date: 2026-11-14
- Basis: project review of 2026-11-14 (architecture, functionality, competitors, performance, papers).
- Branch: fix/retrieval-correctness-2026-11

## Problem

Three retrieval defects were reproduced on main with a throwaway probe test against
`internal/store/search`:

1. Recency annihilates relevance. `recencyFactor` is `0.995^hours` (half-life ~5.8 days) applied
   as a final multiplier. A canonical 60-day-old decision scored 2.4e-05 and ranked below a
   one-hour-old note that only mentioned the topic, although RRF had ordered them correctly.
2. The CRAG absolute floor (`MinScoreFloor=0.005`) then dropped 2 of the 3 results in the MCP
   search handlers, keeping only the noise.
3. Natural-language queries return nothing. `sanitizeFTS` AND-joins every term including
   stopwords; `searchRelaxedKeywords` exists but has no caller.

Related findings: the MCP PPR block boosts `Rank` but never re-sorts and seeds PPR with
mixed-scale ranks; `handleSearch` and `handleSearchHybrid` duplicate the graph/CRAG block;
`TokenSimilarity` rebuilds a bigram map per token pair; docs drift from the code.

## Phase 1 — executed in this change (file-disjoint, parallel)

| ID | Task | Files | Verify |
|---|---|---|---|
| rc-101 | Bounded recency: `floor + (1-floor) * 0.5^(hours/halfLife)` with half-life 720 h and floor 0.8 | internal/store/search/store.go, rrf_test.go, store_test.go | go test -count=1 ./internal/store/search |
| rc-102 | Relaxed keyword rescue when the strict tier is empty, with a store-level off switch | internal/store/search/store.go + new test file | go test -count=1 ./internal/store/search |
| rc-103 | Shared MCP graph/CRAG stage: positional PPR seeds, position-only fusion of the PPR order, re-sort, no absolute-floor drops | internal/mcp/search_pipeline.go (new), tools_memory.go, tools_cortex.go | go test -count=1 ./internal/mcp |
| rc-104 | Late-interaction scorer: precompute query-token bigrams once, output byte-identical | internal/retrieval/late_interaction.go + test | go test -count=1 ./internal/retrieval |
| rc-105 | Docs truthfulness: Go version, MCP profiles, competitive matrix, unevidenced latency claims (EN + ES) | docs/capability-matrix*.md, docs/BENCHMARKS*.md | go test -count=1 ./bench -run TestRetrievalBaselineDocumentationContract |

Gate after merge of all tasks: `go build -tags cortex_vectors ./...`, `go vet ./...`,
`golangci-lint run ./...`, `go test -count=1 ./...`, and the offline retrieval gate
`go test -count=1 ./bench ./bench/common ./bench/cortex ./bench/fixtures/cortex-native ./bench/cortex/cmd/baseline`.

## Phase 2 — designed, not executed here

| ID | Task | Why deferred |
|---|---|---|
| rc-201 | Re-measure LOCOMO and LongMemEval after rc-101/rc-102 | Needs the Ollama judge; rc-102 touches the seam that the 2026-10 note says moved LOCOMO 0.56 -> 0.51-0.53 at n=100 |
| rc-202 | PostgreSQL lexical relaxation (`websearch_to_tsquery('simple', ...)` -> stopword-pruned OR fallback) | Verifiable only with the PostgreSQL integration DSNs (CI) |
| rc-203 | Move fact-key reconcile off the read path (`ensureFactKeys` takes a global mutex and scans `observations` twice per search) | Needs a design for write-path or worker reconcile that keeps immediate consistency for the bench |
| rc-204 | One retrieval pipeline for bench and production (`ExecuteAdaptiveSearch` has no production caller) | Structural; must be gated by rc-201 |
| rc-205 | In-memory vector corpus cache and binary-quantized coarse pass for `sqlite_blob` | Needs an invalidation design and a recall gate |
| rc-206 | Raw-RAG baseline in the bench harness; LongMemEval-S as the headline metric | New harness work |

## Phase 3 — product gaps (from competitor and paper review)

- Automatic capture through more harness hooks with an observer step.
- Automatic invalidation of contradicted facts (bi-temporal, invalidate-not-delete).
- Background consolidation with delta-only updates.
- Progressive-disclosure search (index -> timeline -> detail) under a token budget.
- Contextual prefixes on indexed chunks; round-level values with fact keys.

## Non-goals

- No edit to checksum-pinned migration SQL.
- No change to the position-only RRF contract (REQ-RET-002); recency stays a final multiplier.
- No default flip for embeddings, rerank, or HyDE.

## Risks

- R-1: rc-101 changes pinned recency values; tests that pin `0.995^hours` are updated with the
  new formula, and cursor pagination comments that reference the old factor are revised.
- R-2: rc-102 can change tier routing in the bench through prior-stage fusion scores; the off
  switch exists so rc-201 can A/B it.
- R-3: rc-103 changes the order and count of MCP search results when PPR runs or when old
  memories were previously dropped by the floor.

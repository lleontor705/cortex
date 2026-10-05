# Plan: embed-provider-tiers — Two-Tier Embedding & Rerank Provider Tiers

Change ID: embed-provider-tiers
Workflow: sdd-lite (integrated contract) | Spec plane: hybrid | Workload policy: flexible
Board: cortex-embed-tiers | Status: APPROVED (orchestrator/user-ratified design)
Evidence: seam probe observation #210 (project cortex).

## Intent

Introduce a two-tier embedding/rerank provider model for cortex search quality. Tier 1 (local, default, unchanged): Ollama nomic-embed-text remains the default local embedding provider with no new local runtime; local mode stays zero-CGO with no external services. Tier 2 (server-only): a generic OpenAI-compatible embedding provider (target: nan.builders qwen3-embedding-8B, 4096-dim, RPM 60, batch 32) plus a NEW Reranker port with an HTTP /v1/rerank server-only implementation (target: nan.builders qwen3-Reranker-8B). Rationale: prior ablation showed ColBERT+CoN delivered ~0.000 marginal LOCOMO gain while embeddings-on moved LOCOMO 0.35 to 0.44; a cross-encoder reranker post-fusion attacks exactly this remaining gap.

Hard constraints (apply to every task): zero-CGO and no external services in local mode (enforced by internal/app/arch_test.go); workload policy flexible (source <= 700 LOC Go, tests <= 1200 LOC, modular test files <= 250 LOC); MCP namespace stays cortex_*; API keys resolve exclusively from env at runtime and are never committed.

## Non-Goals

1. No local-mode external providers: remote/openai-compatible construction paths exist ONLY in server mode (NewSecure + OutboundPolicy); the local default stays ollama.
2. No behavior change when unset: with no provider configured, output must be byte-identical to today (provider=ollama default, rerank_provider=none default, zero rerank machinery constructed).
3. No changes to RRF fusion weights, fuseInputs semantics, or the ai.* LLM namespace (strict embedding/LLM separation preserved).
4. No new local runtime, no vector DB adapter changes (qdrant/pgvector suites untouched), no SQLite migration SQL edits.
5. No API keys in code, config files, docs, or committed env files: env resolution only.
6. No automatic corpus re-index triggers beyond the existing outbox-worker re-ingestion flow.

## Requirements

### Requirement: REQ-EMB-001 Generic OpenAI-compatible embedding preset (Tier 2)
Requirements: REQ-EMB-001

The embedding factory (internal/embedding/service.go newWithClient) and the secure allowlist (internal/embedding/secure.go NewSecure) MUST accept a distinct openai-compatible provider preset that reuses the existing OpenAI client path ({BaseURL}/embeddings, Bearer auth, data[].embedding parsing) under its own naming so policy, telemetry, and docs can differentiate it from first-party OpenAI. Server composition (internal/platform/server/server.go:786-818) MUST include the provider validation branch and a default base URL resolution branch for the preset.

#### Scenario: server-mode construction reaches a compatible endpoint (Happy path)
- GIVEN provider openai-compatible with EmbeddingBaseURL and an API key resolved from env
- WHEN the server builds the embedder through NewSecure with policy.ApproveDestination
- THEN embedding requests POST to {BaseURL}/embeddings with Bearer auth and the returned 4096-dim vectors are accepted

#### Scenario: local mode refuses the remote preset (Edge case)
- GIVEN provider openai-compatible in LOCAL mode
- WHEN NewSecure resolves the provider
- THEN construction is refused fail-closed because remote providers are server-only, and the local default remains ollama

#### Scenario: unknown provider stays hard-rejected (Error state)
- GIVEN an unknown provider string outside ollama|openai|openai-compatible|none|empty
- WHEN either the factory or NewSecure runs
- THEN the current hard-reject behavior is preserved with no silent fallback

### Requirement: REQ-EMB-002 Live dimensions replace the static server mirror map (drift fix)
Requirements: REQ-EMB-002

The static embeddingDimensions(provider) mirror map in internal/platform/server/server.go:775-784 MUST be replaced with live service-reported dimensions (dims cached from the first response / service.Dimensions()), so the server ModelInfo and admin AI status report the real provider dimension and cannot drift from provider defaults.

#### Scenario: 4096-dim endpoint reports live dimensions (Happy path)
- GIVEN a first embedding response of 4096 dims from the openai-compatible endpoint
- WHEN the server composes ModelInfo or admin AI status
- THEN the reported dimension is 4096 from live service state, not a static-map guess

#### Scenario: dimension change is guarded by the domain error (Edge case)
- GIVEN an existing corpus indexed at 768 dims
- WHEN a 4096-dim upsert arrives
- THEN domain.ErrDimensionMismatch guards the write and the documented re-ingestion flow via the durable outbox worker applies

#### Scenario: status report reflects an unconfigured provider (Error state)
- GIVEN no embedding provider configured (none/empty)
- WHEN the admin AI status is queried
- THEN the status reports configured=false without panicking on a live service that was never constructed

### Requirement: REQ-CFG-001 Configuration resolution extension for compatible embeddings and rerank
Requirements: REQ-CFG-001

Config resolution (internal/config/config.go:367-383) MUST extend search.embedding_provider acceptance to the openai-compatible preset and introduce rerank keys (search.rerank_provider default none; search.rerank_model; search.rerank_base_url) with CORTEX_RERANK_* env mirrors alongside the existing CORTEX_EMBEDDING_* handling. Rerank API keys MUST resolve only from env at runtime. The ai.* LLM namespace stays untouched.

#### Scenario: YAML binds the compatible preset (Happy path)
- GIVEN YAML search.embedding_provider: openai-compatible with embedding_base_url set
- WHEN config resolves
- THEN provider/model/base_url bind and the API key comes exclusively from env

#### Scenario: rerank defaults to disabled (Edge case)
- GIVEN no rerank keys are set in any config source
- WHEN config resolves
- THEN search.rerank_provider equals none and zero rerank machinery is constructed anywhere

#### Scenario: secrets never serialize into config files (Error state)
- GIVEN an operator supplies credentials via env only
- WHEN cortex config writes or validates files
- THEN no API key material appears in any serialized YAML/JSON/TOML config file

### Requirement: REQ-RNK-001 Reranker port with local default and HTTP implementation
Requirements: REQ-RNK-001

A Reranker interface MUST be defined in internal/retrieval with (1) a default local implementation wrapping the existing dead-code ReRankWithLateInteraction (late_interaction.go:115-186, rank 60% / MaxSim 40% blend) reviving it into production use without semantic change, and (2) an HTTP implementation POSTing to {BaseURL}/v1/rerank (OpenAI-compatible rerank contract) constructed ONLY via server-mode NewSecure paths with OutboundPolicy destination approval, enforcing batch <= 32 and RPM <= 60 throttling.

#### Scenario: disabled rerank constructs nothing (Happy path)
- GIVEN search.rerank_provider=none (default)
- WHEN retrieval runs
- THEN no Reranker is constructed and result ordering is byte-identical to the unmodified baseline

#### Scenario: local default reranks with the revived blend (Edge case)
- GIVEN the local default late-interaction Reranker on fused results
- WHEN reranking N candidates
- THEN ordering derives from the revived 60/40 rank/MaxSim blend on existing vectors with zero network calls

#### Scenario: HTTP reranker respects provider limits (Error state)
- GIVEN the HTTP Reranker with an approved destination and 64 candidates
- WHEN reranking runs
- THEN requests are split into batches of at most 32 documents and throttled to at most 60 requests per minute, and destination approval failures fail closed

### Requirement: REQ-RNK-002 Post-fusion pre-limit rerank wiring gated on config
Requirements: REQ-RNK-002

Reranking MUST be applied as a post-fusion pre-limit step on []*domain.SearchResult at the FuseResultsWithOptions call sites (internal/mcp/tools_cortex.go:729, internal/mcp/tools_memory.go:1072) and the server analog at the internal/store/search rrfFuse output (:789, :1322), gated on search.rerank_provider. Mutation evidence is REQUIRED: with rerank disabled the full local MCP + store/search suite must produce results byte-identical to the pre-change baseline.

#### Scenario: disabled gate is byte-identical (Happy path)
- GIVEN search.rerank_provider=none on the wired code
- WHEN the local MCP search and store/search fusion suites run
- THEN every returned result matches the unmodified baseline byte-identically, proven by mutation evidence recorded for the gate

#### Scenario: rerank runs before the final limit (Edge case)
- GIVEN a configured Reranker and fusion output larger than the requested limit
- WHEN the pipeline executes
- THEN rerank orders candidates before final limit truncation (pre-limit), never after

#### Scenario: reranker failure degrades to fusion order (Error state)
- GIVEN a configured Reranker whose endpoint errors or times out
- WHEN retrieval executes
- THEN the pipeline fails closed or degrades to unreranked fusion order per the implemented policy and never silently drops results

### Requirement: REQ-DOC-001 Configuration documentation for tiers, keys, and re-index flow
Requirements: REQ-DOC-001

Documentation MUST cover: the openai-compatible embedding preset with a nan.builders example (base URL + env keys, placeholder credentials only), the rerank config keys with the none default, and the dimension-change re-ingestion flow (ErrDimensionMismatch leading to corpus re-ingestion via the outbox worker). Targets: docs/CONFIGURATION.md (+ docs/CONFIGURATION.es.md sync where feasible), the AGENTS.md configuration section, and the distribution/config documentation page.

#### Scenario: operator follows the documented example (Happy path)
- GIVEN an operator reads docs/CONFIGURATION.md
- WHEN they follow the nan.builders openai-compatible example
- THEN every credential slot references an env variable and no literal key appears in any committed file

#### Scenario: dimension change path is documented (Edge case)
- GIVEN an operator switches embedding models with different dimensions
- WHEN they consult the docs
- THEN the ErrDimensionMismatch guard and the outbox-worker re-ingestion procedure are described

#### Scenario: disabled defaults are stated (Error state)
- GIVEN the docs describe rerank and compatible providers
- WHEN the reader checks default behavior
- THEN the docs state that unset keys preserve the previous ollama-only, rerank-off behavior exactly

### Requirement: REQ-EVL-001 Published A/B evaluation artifacts with honest blocking
Requirements: REQ-EVL-001

Published A/B eval artifacts MUST be produced at bench/reports/embed-ab-locomo.json and bench/reports/embed-ab-longmemeval.json covering (a) the ollama/nomic baseline (measured: LOCOMO 0.44; LongMemEval vector row +0.09), (b) the openai-compatible provider via operator-supplied env (CORTEX_EMBEDDING_BASE_URL/CORTEX_EMBEDDING_API_KEY supplied at run time, never committed), and (c) rerank if the endpoint supports it. RPM 60 throttling MUST be respected between batches. If credentials or the endpoint are absent, the task MUST transition to blocked honestly with an operator-facing reason instead of fabricating results.

#### Scenario: credentials present produce provenance-bearing artifacts (Happy path)
- GIVEN operator-supplied env credentials and the eval runners
- WHEN the A/B evaluation executes
- THEN both JSON artifacts are published with baseline and candidate results plus provenance metadata

#### Scenario: missing credentials blocks honestly (Edge case)
- GIVEN the API key or base URL env is absent when the eval task starts
- WHEN the task begins
- THEN it blocks with an explicit missing-credential reason and publishes nothing

#### Scenario: rate limits are honored (Error state)
- GIVEN the external endpoint enforces RPM 60
- WHEN the eval runner emits embedding or rerank batches
- THEN requests are throttled between batches to remain within the limit and 429 responses trigger backoff rather than retries that exceed the limit

## Tasks

- [ ] emb-t01-openai-compatible-allowlist [embedding] Provider allowlist: openai-compatible preset plus config resolution extension
  Requirements: REQ-EMB-001, REQ-CFG-001
  Allowed files target: internal/embedding/service.go; internal/embedding/secure.go; internal/config/config.go; internal/embedding/service_test.go; internal/embedding/secure_test.go; internal/config/config_test.go
  Verification: go test -v -count=1 -tags cortex_vectors ./internal/embedding ./internal/config
  Forecast: <= 300 changed lines Go, <= 700 Go cap. newWithClient accepts openai-compatible (same client as openai, distinct naming), NewSecure allowlist extended, config keys/env extended, keys env-only.

- [ ] emb-t02-server-live-dimensions [server] Server wiring: default-URL branch plus live Dimensions() replacing the static mirror map
  Requirements: REQ-EMB-002
  Allowed files target: internal/platform/server/server.go; internal/platform/server/server_test.go
  Verification: go test -v -count=1 ./internal/platform/server
  Dependencies: emb-t01-openai-compatible-allowlist
  Forecast: <= 150 changed lines Go, <= 700 Go cap. Replace embeddingDimensions(provider) static map at server.go:775-784 with live dims; add openai-compatible branch and default base URL; ErrDimensionMismatch re-ingestion flow test.

- [ ] rnk-t01-reranker-port [retrieval] Reranker port: interface, revived late-interaction default, HTTP /v1/rerank implementation
  Requirements: REQ-RNK-001
  Allowed files target: internal/retrieval/reranker.go; internal/retrieval/late_interaction.go; internal/retrieval/reranker_test.go; internal/config/config.go
  Verification: go test -v -count=1 ./internal/retrieval ./internal/config
  Dependencies: emb-t01-openai-compatible-allowlist
  Forecast: <= 350 changed lines Go, <= 700 Go cap. Default impl wraps ReRankWithLateInteraction 60/40 blend unchanged; HTTP impl POSTs {BaseURL}/v1/rerank with batch<=32 and RPM<=60 throttle; server-only construction via NewSecure + OutboundPolicy; rerank config keys added here.

- [ ] rnk-t02-rerank-wiring-gate [mcp] Rerank wiring post-fusion pre-limit with disabled-by-default byte-identical gate
  Requirements: REQ-RNK-002
  Allowed files target: internal/mcp/tools_cortex.go; internal/mcp/tools_memory.go; internal/mcp/tiered_search_test.go; internal/store/search/store.go
  Verification: go test -v -count=1 ./internal/mcp ./internal/store/search
  Dependencies: rnk-t01-reranker-port
  Forecast: <= 250 changed lines Go, <= 700 Go cap. Wire at FuseResultsWithOptions call sites tools_cortex.go:729 and tools_memory.go:1072 plus store/search rrfFuse output :789/:1322; gated on search.rerank_provider=none default; mutation evidence required (disabled = byte-identical).

- [ ] doc-t01-config-docs [docs] Configuration docs: openai-compatible preset, rerank keys, and re-index flow
  Requirements: REQ-DOC-001
  Allowed files target: docs/CONFIGURATION.md; docs/CONFIGURATION.es.md; AGENTS.md
  Verification: grep -c "openai-compatible" docs/CONFIGURATION.md AGENTS.md
  Dependencies: rnk-t02-rerank-wiring-gate
  Forecast: <= 400 changed lines Markdown. nan.builders example with placeholder credentials, env keys, rerank defaults, dimension-change re-ingestion procedure.

- [ ] evl-t01-embed-ab-reports [bench] Publish embed A/B eval artifacts with honest blocking on missing credentials
  Requirements: REQ-EVL-001
  Allowed files target: bench/reports/embed-ab-locomo.json; bench/reports/embed-ab-longmemeval.json
  Verification: test -s bench/reports/embed-ab-locomo.json && test -s bench/reports/embed-ab-longmemeval.json
  Dependencies: rnk-t02-rerank-wiring-gate
  Forecast: data artifacts only. Runs (a) ollama/nomic baseline, (b) openai-compatible via operator env, (c) + rerank if supported; RPM 60 throttle; BLOCKED honestly if CORTEX_EMBEDDING_BASE_URL/CORTEX_EMBEDDING_API_KEY are absent; never commit keys.

## Design Summary

Verified seams (from observation #210): internal/embedding/service.go:51-96 factory supports ollama|openai and openAIService already speaks {BaseURL}/embeddings via BaseURL override; secure.go:57-70 NewSecure hard-rejects outside ollama|openai|none; server.go:786-818 composes the embedder with policy.ApproveDestination; config.go:367-383 resolves search.embedding_* and CORTEX_EMBEDDING_* env; domain.ErrDimensionMismatch guards upserts (errors.go:41-93, sqlite_blob adapter.go:46,138); ReRankWithLateInteraction (late_interaction.go:115-186) is dead in production with no /rerank HTTP seam; wire points are the FuseResultsWithOptions call sites and store/search rrfFuse; embeddings run async via the durable outbox worker (app.go:189-198, worker.go:71-129).

New interface contract: type Reranker interface { Rerank(query string, results []*domain.SearchResult) ([]*domain.SearchResult, error) } in internal/retrieval, with DefaultLateInteractionReranker (zero network, revived 60/40 blend) and HTTPOpenAIReranker (server-only, batch <= 32, RPM <= 60, OutboundPolicy approved).

## Verification Strategy

Three layers with deterministic oracles: Layer 1 contracts (emb-t01, rnk-t01) with unit oracles on allowlist, config, batching, and throttling; Layer 2 wiring (emb-t02, rnk-t02) with the byte-identical mutation-evidence gate and live-dimension server tests; Layer 3 (doc-t01, evl-t01) with grep oracles for docs and artifact-existence oracles for evals. Whole-change acceptance: all six tasks done, openspec artifacts validated, mutation evidence recorded for the rerank gate, eval artifacts published or evl-t01 honestly blocked with an operator-facing reason.

## Risks

1. Dimension drift/corpus mismatch (768 vs 4096): guarded by domain.ErrDimensionMismatch with the outbox-worker re-ingestion flow documented in doc-t01.
2. Provider rate limits (RPM 60, batch 32): enforced in HTTPOpenAIReranker and the eval runner with backoff on 429.
3. Secret leakage: fail-closed env-only key resolution; docs show placeholders; reviewer greps committed files for literal keys.
4. Dead-code revival regression: ReRankWithLateInteraction is revived without semantic change, verified by existing late_interaction tests.
5. Byte-identity risk: the mutation evidence gate blocks rnk-t02 transition to in_review until the disabled-path identity is proven.

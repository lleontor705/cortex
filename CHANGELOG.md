# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## Unreleased

### Security

- Upgrade gRPC to v1.82.1 and `golang.org/x/text` to v0.39.0 to remediate reachable vulnerabilities.
- Require Go toolchain 1.26.5 across modules, CI, release builds, and Docker builds.

## [2.4.7] - 2026-10-07

### 🐛 Bug Fixes

- *(server)* Rag/stats reports live vector state instead of hardcoded pgvector/hnsw (#114)

### ⚙️ Miscellaneous Tasks

- *(changelog)* Refresh release history [skip ci]

## [2.4.6] - 2026-10-07

### 🐛 Bug Fixes

- *(server)* Report live embedding dimensions in GET /api/settings (#112)

### ⚙️ Miscellaneous Tasks

- *(changelog)* Refresh release history [skip ci]

## [2.4.5] - 2026-10-07

### 🐛 Bug Fixes

- *(web)* Settings page reflects server runtime configuration (#111)

## [2.4.4] - 2026-10-07

### 🐛 Bug Fixes

- *(web)* Health poll uses same-origin URL (localhost:7438 hardcoded) (#110)

### ⚙️ Miscellaneous Tasks

- *(changelog)* Refresh release history [skip ci]
- *(changelog)* Refresh release history [skip ci]

## [2.4.3] - 2026-10-07

### 🐛 Bug Fixes

- *(web)* Copy webbuilder export into the Go builder stage (#109)

## [2.4.2] - 2026-10-07

### 🐛 Bug Fixes

- *(server)* Dual-stack dial fallback for agent chat (#108)

### 📚 Documentation

- *(server)* Audit CORTEX_* env set, one-command prod compose, Railway docs refresh (#106)

### ⚙️ Miscellaneous Tasks

- *(changelog)* Refresh release history [skip ci]

## [2.4.1] - 2026-10-07

### 🐛 Bug Fixes

- *(ci)* Push changelog to protected main via deploy-key ruleset bypass (#105)

### ⚙️ Miscellaneous Tasks

- Fix coverage/E2E flake timeouts (#103)
- *(changelog)* Verify deploy-key ruleset bypass on protected main [skip ci]
- *(changelog)* Refresh release history [skip ci]

### 💼 Other

- Align workflow contract pins with coverage -timeout 20m (#104)

## [2.4.0] - 2026-10-07

### 🚀 Features

- *(web)* Embed Next.js UI in the binary - static export, web-key auth, parity routes
- *(server)* Single-tenant self-hosted pivot - multi-tenant plane removed, docs repositioned
- Self-hosted pivot + embedded web + quality audit — 89 tasks done across 3 initiatives (embedded-web-redesign, self-hosted-pivot, self-hosted-quality)
- *(retrieval)* RAG performance overhaul — HippoRAG 2, SkewRoute, MRL/int8 vector scan, cardinality switch
- *(embed)* Openai-compatible embed tier, live dimensions, and the Reranker port

### 🐛 Bug Fixes

- *(test)* Resolve errcheck and staticcheck lint warnings
- *(server)* Drop mcp-go/util dependency to unblock Dependabot go_modules updater (#84)
- *(mcp)* Prevent sandbox execution timeout under race instrumentation (#86)
- *(postgres)* Resolve durable grant resolution, token scopes clone, and handoff error annotation (#88)
- *(server)* Correct bearer auth token lifecycle regressions (#89)
- *(server)* Grant owner role to fresh-volume bootstrap principal (#93)
- *(server)* Grant wildcard project/classification to auto-bootstrap principal (#96)
- *(lint)* Resolve errcheck and staticcheck warnings in test suites (#57)
- *(vector)* Adapt pgvector test fake to pgx 5.11.0 Rows interface (#99)
- *(release)* Refresh embedded web dist + stale-dist guard (#100)
- *(web)* Pin deterministic Next buildId for reproducible embedded dist (#101)

### 📚 Documentation

- *(ops)* Publish honest-gain evidence, operator runbook, and distribution packaging

### 🧪 Testing

- Break store/sqlite test import cycle for vector-tests gate
- *(ci)* Make rerank config tests hermetic and fix webkey ENOTDIR postcondition (#85)
- *(migration)* Restore tampered ledger checksum between shared-DB tests (#90)
- *(sandbox)* Stabilize TestProcessExecution under vector-tagged CI (#94)
- *(postgres)* Align migration head pin with shipped migration 112 (#91)
- *(bench)* Align toolchain contract pins with grpc v1.83.1 security bump (#95)

### ⚙️ Miscellaneous Tasks

- Gitignore Playwright e2e artifacts + landed-work housekeeping
- *(docs)* SHA-pin actions, harden the release pipeline, and ship the bilingual docs site
- *(codeql)* Use autobuild build-mode for Go analysis (#83)
- *(docker)* Fix compose smoke health-wait budget for cold-start builds (#87)
- *(toolchain)* Align to Go 1.27.1 + golangci-lint v2.14.0 (#97)
- *(toolchain)* Align release.yml and ci.yml Go pins to 1.27.1 (#98)
- *(changelog)* Refresh release history [skip ci]
- Re-trigger main CI after changelog refresh

### 💼 Other

- *(deps)* Bump node from 22-alpine to 26-alpine in /web (#67)
- *(deps)* Bump the docker-server-minor-patch group (#68)
- *(deps)* Bump the go-minor-patch group across 1 directory with 6 updates (#70)
- *(deps)* Bump docker/build-push-action from 6.19.2 to 7.4.0 (#76)
- *(deps)* Bump actions/dependency-review-action from 4.9.0 to 5.0.0 (#77)
- *(deps)* Bump docker/setup-buildx-action from 3.12.0 to 4.4.1 (#78)
- *(deps)* Bump docker/metadata-action from 5.10.0 to 6.2.0 (#79)
- *(deps)* Bump actions/github-script from 7.1.0 to 9.0.0 (#80)
- *(deps)* Bump the web-minor-patch group in /web with 9 updates (#71)
- *(deps-dev)* Bump vitest from 4.1.10 to 5.0.3 in /web (#72)
- *(deps-dev)* Bump vitest from 4.1.10 to 5.0.3 in /plugin/opencode (#69)
- *(deps)* Bump tailwind-merge from 2.6.0 to 3.7.0 in /web (#75)
- *(deps)* Bump eslint from 8.57.1 to 10.12.0 in /web (#73)
- *(deps-dev)* Bump typescript from 5.9.3 to 7.0.2 in /web (#74)

## [2.3.12] - 2026-09-20

### 🚀 Features

- Initialize Cortex core architecture with CLI, MCP server, TUI, and web frontend

## [2.3.11] - 2026-09-13

### 🚀 Features

- Add Next.js frontend web application with UI components and Docker configurations

## [2.3.10] - 2026-09-13

### 🚀 Features

- Implement core cortex application with CLI, TUI, MCP server, and storage layers

### 🎨 Styling

- Fix golangci-lint issues and refine TUI configuration layout

## [2.3.9] - 2026-09-11

### 🚀 Features

- Implement MCP memory and tools integration with comprehensive testing

## [2.3.8] - 2026-09-10

### 🚀 Features

- Implement sqlite memory store with session support and topic-based observation upserting
- Implement core runtime architecture, MCP integration, and domain services for retrieval and code analysis

### ⚙️ Miscellaneous Tasks

- Remove unused configuration file

## [2.3.7] - 2026-09-08

### 🚀 Features

- Implement shared MCP memory contract and tool schema definitions

## [2.3.6] - 2026-09-06

### 🐛 Bug Fixes

- *(opencode)* Allow tokenless local loopback delivery

## [2.3.5] - 2026-09-04

### 🚀 Features

- Implement robust configuration system, server foundation, and embedding service infrastructure
- Implement Cortex MCP tools for graph, search, scoring, and knowledge consolidation management

## [2.3.4] - 2026-09-02

### 🐛 Bug Fixes

- *(web,rag)* Dynamically resolve configured embedding model and sanitize railway docs

### 📚 Documentation

- Update railway deployment guide with ghcr images and clean variables

## [2.3.3] - 2026-09-02

### 🚀 Features

- Implement infrastructure for deployment and verify Go toolchain consistency via contracts
- Add Docker server bootstrap, backend configuration service, and CI/CD pipelines

### 🐛 Bug Fixes

- *(cli)* Use tagged switch for auth response status in doctor

## [2.3.2] - 2026-09-01

### 📚 Documentation

- Add server deployment documentation and include CI release workflow configuration

## [2.3.1] - 2026-09-01

### ⚙️ Miscellaneous Tasks

- Add GitHub Actions workflow for automated testing and releases

## [2.3.0] - 2026-09-01

### 🚀 Features

- *(rag)* Implement SOTA RAG engines, HippoRAG graph PPR, LightRAG summaries, CRAG gating, ColBERT re-ranking, cortex watch, and Railway deployment
- *(web)* Add healthcheck route for Railway deployment
- *(ast)* High-density zero-CGO polyglot code intelligence and two-pass cross-file resolution
- Add AST diagnostic capabilities, gold standard test data, and automated benchgate evaluation infrastructure
- Implement conversational agent platform, adaptive RAG, and security hardening with multi-tenant isolation.
- Implement remote synchronization architecture with multi-platform server support and AST-based retrieval features
- Implement background file watcher for incremental indexing and add infrastructure for asynchronous embedding workers

### 🐛 Bug Fixes

- *(postgres)* Resolve audit_events jsonb type casting and pgvector unconstrained hnsw index guard
- *(postgres)* Align project_artifacts queries with migration 106 schema
- *(web)* Add null-safe optional chaining for AST code explorer metrics and tabs
- *(lint)* Clean up unused regexes and implicit types in ast extractors
- *(mcp)* Support type architecture and enrich write output schema with error payload
- *(plugin/opencode)* Poll daemon readiness with backoff to prevent early unavailable warnings

### 📚 Documentation

- *(plugin)* Document Adaptive-RAG modes and HippoRAG in Claude Code memory skill

### 🧪 Testing

- *(scoring)* Update test cases for TypeArchitecture

### ⚙️ Miscellaneous Tasks

- *(railway)* Remove root railway.json to allow per-service monorepo builds

### 💼 Other

- *(web)* Add standalone multi-stage Dockerfile and dockerignore for cortex-web

## [2.2.4] - 2026-08-23

### 🚀 Features

- Hybrid mode detection, graph node detail inspector, memory modal and server RAG env wiring

## [2.2.3] - 2026-08-23

### 🚀 Features

- *(ast)* Full zero-cgo polyglot AST extractor with CLI ingest command

## [2.2.2] - 2026-08-23

### 🐛 Bug Fixes

- *(ast)* Handle root dot directory in isIgnoredDir

## [2.2.1] - 2026-08-23

### 🚀 Features

- *(cli)* Add cortex ingest command for polyglot AST extraction

## [2.2.0] - 2026-08-23

### 🐛 Bug Fixes

- *(opencode)* Prioritize cortex.yaml http.token in plugin resolver

## [2.1.9] - 2026-08-23

### 🐛 Bug Fixes

- *(sync)* Support raw token in token_env and robust fallback in app/cli

## [2.1.8] - 2026-08-23

### 🐛 Bug Fixes

- *(opencode)* Auto-resolve token from ~/.cortex/cortex.yaml in plugin

## [2.1.7] - 2026-08-23

### 🐛 Bug Fixes

- *(mcp)* Clear InputSchema on cortex_handoff to prevent JSON marshal schema conflict

## [2.1.6] - 2026-08-23

### 🐛 Bug Fixes

- *(module)* Migrate module path to github.com/lleontor705/cortex/v2

## [2.1.4] - 2026-08-22

### 🚀 Features

- Add rules & AST MCP tools, TUI hybrid modal, web hybrid config, and self-update engine

## [2.1.3] - 2026-08-22

### 🚀 Features

- *(tui)* Fix clean install identity, local sync default, and add server connect modal

## [2.1.2] - 2026-08-22

### 🚀 Features

- *(ast)* Add zero-CGO AST extraction support for .NET (C#, F#, VB), Java, Kotlin, Rust, C/C++, PHP, Ruby, Swift

### 📚 Documentation

- Restore Homebrew and Go installation instructions in README and INSTALLATION

## [2.1.1] - 2026-08-22

### 🚀 Features

- Add server URL normalization and enhance styles
- Enhance HTTP API with session and prompt management, update plugin installation, and improve CORS handling
- *(ci)* Add vector and web tests to CI workflows
- Refactor session stop scripts for improved delivery handling and add UTF-8 truncation utility
- Add authentication context and configuration exporter
- Add Vitest for testing and implement initial tests for API client and config exporters
- Implement project protocol, vector hydration benchmarking, and robust PostgreSQL-backed workspace gating architecture
- Implement Postgres project artifact persistence and add batch hydration tests for SQLite store
- *(web)* Complete responsive design and theme variable consistency across all pages
- *(web)* Overhaul projects & skills UI, add gemini provider, background AI tasks, fix graph expansion and search parsing
- *(graph)* Native Graphify capabilities in Go with Louvain clustering, blast radius, AST extractor, and web UI
- *(graph)* Project-scoped knowledge and code graph with repository scanner and project diagnostics
- *(mcp)* Expose cortex_ingest_code tool and support incremental refactoring updates
- *(ui, docs)* Improve graph explorer with legend and JSON export, persist embeddings/vectors config, add SVG architecture diagrams
- *(projects, graph)* Standardize select combo styling and add direct button to inspect project graph
- *(graph)* Add Domain Layer selector (Unified, Knowledge/Memory, Code/AST)
- *(server,web)* Add server AI status, live test endpoints for LLM and embeddings, and UI test controls
- *(server)* Add dimension detection for qwen3-embedding models
- *(web)* Migrate graph visualization to Sigma.js WebGL and remove code scan
- *(web)* Add separated graph layers and Obsidian zip export to Sigma.js graph view
- *(web)* Redesign Admin page with tabbed architecture, KPI summary cards, and interactive MCP hub
- Enhance user profile info, restrict admin views for developer, personalize dashboard, and add folder project resolution in MCP
- *(projects)* Redesign Projects & Skills page with strict role separation for Developer vs Admin
- *(projects)* Add AI project deduplication and merge engine, and enforce BOLA ownership deletion in memory view
- *(web)* Redesign AI Extraction and Synthesis page with multi-mode tabs, presets, draft reviewer, and project synthesis report
- *(extract)* Use centralized global LLM configuration instead of in-page duplicates
- *(plugin/opencode)* Synchronize CORTEX_TOOLS and instructions with corporate context, code intelligence, and skills tools
- *(plugin/opencode)* Detect runtime mode (server vs local) and inject mode-aware MCP instructions

### 🐛 Bug Fixes

- Reorder token expiration check in integration test for clarity
- *(server)* Reconcile legacy bootstrap subject and update Dockerfile base image
- *(migration)* Make bootstrap service account and actor reconciliation idempotent in 108
- *(server)* Reconcile canonical bootstrap service principal on startup
- *(migration)* Restore immutable 108 migration SQL matching pinned checksum
- *(migration)* Match migration 108 pin with original checksum
- *(web)* Add .dockerignore and bind HOSTNAME to 0.0.0.0 for web container
- *(lint)* Close deferred files safely and restore deleteEdge route
- *(lint)* Simplify slice append in projectGraph handler
- *(docker)* Add root Dockerfile.web for Railway cortex-web monorepo builds
- *(graph)* Resolve canvas rendering, ResizeObserver, node matching and auto-fit view
- *(server)* Wrap response body close in defer func to satisfy errcheck
- *(web)* Display dynamic embedding dimensions on badge
- *(server)* Fall back to pgvector/qdrant dimension in server bootstrap
- *(pgvector)* Guard index creation for vector dimensions exceeding 2000
- *(config)* Support independent LLM and Search Embedding providers
- *(pgvector)* Safely ignore index creation errors on bootstrap
- *(config)* Ensure ServerLLMFromEnv signature is preserved
- *(web)* Fix background workers to use listObservations and enhance logs
- *(authz)* Support RoleDeveloper and RoleAgent and ensure default projects and clearance in createUser
- *(server)* Assign default scopes by role in createUser
- *(llm)* Support google, gemini, ollama in server LLM config, update CORS and role UI in web
- *(authz)* Grant ResourceWorkspaces ActionRead to developer, member, and agent roles
- *(dashboard)* Personalize dashboard metrics and sessions for user and grant project access in user token verification
- *(dashboard)* Strict user scoping for observations, sessions, and dashboard KPIs for non-admin users
- *(merge)* Correct SQL schema join for project deduplication and merge
- *(graph)* Resolve type filters not applying changes via dynamic refs and effective kind mapping
- *(web)* Update preferences test suite with embedding and vector fields
- *(plugin/claude-code)* Set executable mode on hook scripts and add chmod in harness
- *(obsidian)* Correct macOS var alias path trimming in writer test
- *(graph)* Relax wall-clock timing assertion in TestV2JoinedStarPathBatchThreeSQLStatements for CI runners
- *(e2e,postgres)* Grant wildcard scopes to bootstrap owner fixture and fallback spike DSNs
- *(postgres)* Grant database connect to cortex_rw_app and relax concurrency floor for CI
- *(e2e,postgres)* Default empty clearance on user creation and handle optional pgbouncer console
- *(ci,postgres)* Skip c32 ablation suite on non-dedicated CI and relax e2e concurrency floor
- *(postgres)* Correct preflight DSN check order and pre-repetition boundary assertion
- *(postgres)* Actively terminate lingering idle backends in application drain probe
- *(release)* Sync spike environment DSNs and error logging in coverage job

### 📚 Documentation

- Update MCP, HTTP-API, and add comprehensive Graph Intelligence guide
- Restore Homebrew installation instructions

### ⚙️ Miscellaneous Tasks

- Trigger release workflow on main and master branches
- Remove master branch trigger from release workflow
- Add push trigger to ci.yml for main and develop
- Add failure log extraction and annotations to test jobs
- Print failure logs directly to step summary and action output
- Format error annotation title with failed test names
- Capture detailed line annotations for coverage and e2e failures

## [2.1.0] - 2026-08-02

### 🚀 Features

- Initialize web application with React, Vite, and TypeScript

### 🐛 Bug Fixes

- *(husky)* Enhance pre-push script to check Go and golangci-lint availability

## [2.0.0] - 2026-07-30

### 🚀 Features

- *(bench)* Add retrieval quality and correctness metrics
- *(bench)* Define retrieval evidence report contract
- *(bench)* Capture current production retrieval baseline
- *(bench)* Add baseline reproducibility analysis
- *(bench)* Add preregistered retrieval gate registry
- *(bench)* Adapt DMR evidence reporting
- *(bench)* Adapt LongMemEval evidence reporting
- *(bench)* Adapt LOCOMO evidence reporting
- *(bench)* Bind gates to approved baseline evidence
- *(bench)* Harden baseline evidence reports
- *(bench)* Compare representative baseline variance
- *(bench)* Define process resource evidence contract
- *(bench)* Refuse unmeasured representative runs
- *(bench)* Collect Linux baseline resources
- *(bench)* Collect Windows baseline resources
- *(bench)* Identity and ingestion for native baseline evidence
- *(bench)* Orchestrate runner and evidence report
- *(bench)* Orchestrate native baseline evidence
- *(bench)* Add portable baseline evidence command
- *(domain)* Add ValidationError taxonomy for passive classification
- *(domain)* Accept session_summary and passive observation types
- *(domain)* Add Storage/Tx/UnitOfWork/VectorIndex ports and Principal/TenantContext
- *(platform)* Add mode seam with --mode dispatch (local wired, server inert)
- *(store)* TxParticipant seam and BusyRetryConfig with arch gate evolution (W2.1)
- *(store)* Atomic UnitOfWork cross-store save with BUSY bounds (W2.1)
- *(migration)* V2 baseline schema and embed bundle (W3.1)
- *(migration)* Forward-only v2 baseline runner with integrity gate (W3.1)
- *(migration)* Read-only compatibility probe and old-DB refusal wiring (W3.2)
- *(embedding)* Add transactional outbox store with retry and dead-letter
- *(embedding)* Add bounded worker pool with retry, dead-letter, and drain
- *(app)* Wire embedding worker lifecycle
- *(bundle)* Expose worker handle on Stores for lifecycle wiring
- *(mcp)* Rename all public tools to cortex_* namespace (W6.1)
- *(vector)* Adopt VectorIndex port with sqlite adapter
- *(config)* Add vector provider config section (W8.2)
- *(vector)* Add qdrant external VectorIndex adapter (W8.2)
- *(config)* Add pgvector provider config section (W8.3)
- *(vector)* Add pgvector external VectorIndex adapter (W8.3)
- *(retrieval)* Select vector filtering by capabilities
- *(server)* Add explicit VectorIndex factory and ban from local (W8.4)
- *(server)* Add reindex replay and explicit health semantics (W8.4)
- *(graph)* Add bi-temporal v2 graph and canonical entities
- *(obsidian)* Add export-only vault projection
- *(server)* Add PostgreSQL tenant schema and RLS
- *(migration)* Add PostgreSQL server runner and checksums
- *(store)* Add PostgreSQL server repositories
- *(platform)* Add PostgreSQL server composition root
- *(identity)* Add api token and oauth resource verification
- *(storage)* Extend server token grants and usage metadata
- *(storage)* Persist PostgreSQL API tokens
- *(authz)* Add tenant-scoped RBAC and BOLA enforcement

### 🐛 Bug Fixes

- *(bench)* Rewrite llm_judge.go to Ollama-only (P0)
- *(ci)* Validate benchmark documentation
- *(docs)* Remove unsupported benchmark claims
- *(bench)* Classify production baseline traces
- *(bench)* Unblock baseline evidence workflow
- *(bench)* Remove self-referential corpus/commit equality from evidence identity
- *(bench)* Resolve golangci lint findings
- *(store)* Deterministic BUSY-cap defect pin and IsSQLiteBusy accuracy (W2.3 review)
- *(embedding)* Unify outbox saturation on worker and bound finalize context
- *(search)* Remove shared search-query field, add request-scoped SearchID feedback
- *(search)* Correct ranked-list RRF (k=60) + formal dual-level fusion + multiplicative re-rank
- *(mcp)* Wire dedup taxonomy, fix passive source, surface real failures (W6.2)
- *(embedding)* Reuse and close HTTP clients
- *(vector)* Align tagged tests and embedding dimensions
- *(vector)* Validate qdrant capabilities and configuration
- *(vector)* Validate pgvector index tuning and timestamps
- *(graph)* Expose temporal fields on v2 reads
- *(graph)* Remediate W9.3 temporal and entity invariants
- *(obsidian)* Make vault export atomic and safe
- Remediate W10 export and retry blockers
- *(obsidian)* Harden Windows reparse containment
- *(obsidian)* Detect case-insensitive vault collisions
- *(obsidian)* Harden Windows device filename handling
- *(obsidian)* Enforce exact ownership and canonical vault roots
- *(store)* Cast server repository references
- *(store)* Enforce postgres transaction and opaque IDs
- *(migration)* Harden postgres schema lifecycle
- *(graph)* Hide internal IDs and enforce scoped updates
- *(migration)* Make postgres rollback driver-safe
- *(domain)* Preserve internal graph request decoding
- *(server)* Complete archival scoring and concurrent shutdown
- *(identity)* Fall back to RFC8414 issuer metadata
- *(identity)* Harden principal and JWT credential boundaries
- *(storage)* Make token rotation tenant-safe and atomic
- *(authz)* Close W13.3 tenant binding gaps
- *(authz)* Harden authorized storage operations
- *(authz)* Close high severity server access gaps
- *(authz)* Close W13 critical capability leaks
- *(ci)* Bootstrap postgres authorization roles in order

### 📚 Documentation

- *(bench)* Publish retrieval baseline methodology
- *(review)* Record baseline round 1 fixes
- *(mcp)* Update v2 tool namespace test comments
- Record W10 obsidian remediation
- Record W10 repeat-review verification
- *(remediation)* Record W11.2 evidence
- *(remediation)* Record fresh W11.2 evidence
- *(remediation)* Record graph JSON compatibility fix
- Record final W12 commit set
- *(authz)* Record remediation evidence
- Remove trailing whitespace from authz review

### 🚜 Refactor

- *(bench)* Remove temporary evidence stub
- *(tui)* Remove unused observation helpers
- *(mcp)* Replace detached embed goroutine with transactional outbox
- *(search)* Unify retrieval helpers, SQLite candidate revalidation, deterministic pagination
- *(migration)* Remove Engram compatibility surface (W7 GREEN)
- *(search)* Share vector candidate revalidation

### 🎨 Styling

- *(vector)* Format W8.1 migrated consumers
- *(vector)* Format W8.4 files

### 🧪 Testing

- *(bench)* Define versioned retrieval corpus contract
- *(bench)* Add adversarial isolation fixtures
- *(bench)* Add authoritative retrieval fixtures
- *(bench)* Validate native baseline inputs
- *(bench)* Preregister native baseline protocol
- *(bench)* Add native retrieval evidence corpus
- *(bench)* Add native baseline evidence RED contract
- Raise global statement coverage above 70 percent
- *(app)* Add W1 architecture gate (no local→server import, no seam adoption)
- *(store)* Register p95 save-latency envelope with saturation gate (W2.2)
- *(store)* Make saturation p95 diagnostic deterministic
- *(mcp)* Pin temporal search profile membership
- *(bench)* Pin Engram removal grep contract (W7 RED)
- *(arch)* Ban pgvector-go from local composition
- *(vector)* Add shared adapter conformance suite (W8.4)
- *(embedding)* Add vector-upsert failure injection tests (W8.4)
- *(vector)* Run conformance suite for sqlite adapter
- *(obsidian)* Pin Windows reserved-name near misses
- *(postgres)* Add docker conformance harness
- *(postgres)* Cover RLS and atomic UoW conformance
- *(store)* Make edge validation ranges deterministic
- *(identity)* Cover credential rejection paths
- Complete identity and PostgreSQL token coverage
- Finalize W12 coverage and verification report
- *(authz)* Complete W13 authorization verification

### ⚙️ Miscellaneous Tasks

- *(bench)* Validate retrieval baseline contracts
- *(git)* Ignore generated coverage profiles
- *(config)* Default version 2.0.0 for v2 database line (W3)
- Add race detector gate for concurrent stores
- Enforce global coverage threshold
- *(store)* Satisfy PostgreSQL lint
- Include PostgreSQL integration in coverage gate
- *(toolchain)* Pin Go 1.26.5 across verification builds

### 💼 Other

- *(vector)* Add qdrant Go client dependency
- *(vector)* Add pgx and pgvector-go dependencies
- *(deps)* Remediate reachable grpc text and Go TLS vulnerabilities

## [1.3.0] - 2026-06-23

### 🚀 Features

- Fix hybrid search bugs + comprehensive TUI improvements
- Professional TUI polish — status bar, help screen, reindex detection, visual upgrades

## [1.2.2] - 2026-04-13

### 🐛 Bug Fixes

- Reload config from canonical path + reload on screen open
- Track loaded config file path for consistent save/reload

## [1.2.1] - 2026-04-12

### 🐛 Bug Fixes

- Reload embedding service after config save in TUI

## [1.2.0] - 2026-04-12

### 🚀 Features

- Hybrid search fixes + comprehensive TUI overhaul
- Advanced TUI overhaul — viewport, command palette, sparkline, mouse, responsive
- Bubbles/list migration, split pane preview, lipgloss tables

## [1.1.1] - 2026-04-12

### 🐛 Bug Fixes

- Enable cortex_vectors build tag in GoReleaser

## [1.1.0] - 2026-04-12

### 🚀 Features

- Add TUI embedding config screen and Ollama auto-launcher

## [1.0.0] - 2026-04-11

### 🚀 Features

- *(a2a)* Add A2A Protocol integration - message format, transport layer, agent registry, and MCP tools
- Update CLAUDE.md and README.md for A2A Protocol integration and enhancements; add new logo

### 🐛 Bug Fixes

- *(ci)* Add Trivy security scanning via reusable workflow
- *(ci)* Revert to ats-deploy-public for reusable workflows
- Resolve lint issues (errcheck, staticcheck, unused vars)
- Remaining staticcheck QF1012 issues
- Resolve all golangci-lint issues (staticcheck QF1012, errcheck, unused)

### 🚜 Refactor

- *(ci)* Rename master branch references to main (gitflow standardization)

### ⚙️ Miscellaneous Tasks

- Re-trigger after ats-deploy made public
- Verify standard pipeline execution
- Use ats-deploy-public for quality/security workflows
- Final verification
- Configure husky pre-push hooks for lint and test
- Migrate reusable workflows from ats-deploy-public to ats-deploy

### 💼 Other

- Resolve conflicts with develop (keep feature branch changes)

## [0.2.7] - 2026-03-31

### 🚀 Features

- Enhance edges migration to handle NULL values with COALESCE

## [0.2.6] - 2026-03-31

### 🚀 Features

- Update Cortex memory protocol with new tools and enhancements for v0.2.1

## [0.2.5] - 2026-03-31

### 🚀 Features

- Add temporal graph semantics and observability features
- Enhanced search pipeline, Engram feature port, and release preparation

### 🐛 Bug Fixes

- Resolve 9 golangci-lint issues (errcheck + staticcheck)
- Make RecencyBoost test deterministic for CI

## [0.2.3] - 2026-03-28

### 🐛 Bug Fixes

- Eliminar sync-develop redundante del release pipeline
- Detectar versión automáticamente con go install

## [0.2.2] - 2026-03-28

### 🚀 Features

- Extend entity extraction with 6 new patterns
- Agregar check pasivo de actualizaciones via GitHub releases

### 🐛 Bug Fixes

- Corregir BFS loop infinito, GetRelationships incompleto y duplicados en graph
- Corregir rollback de migración 008 y validación de confidence
- Manejar errores de time.Parse en todos los stores
- Implementar logging en scoring y lifecycle en vez de continue silencioso
- Corregir truncamiento Unicode y validar weight/confidence en MCP
- Corregir weight validation, campos faltantes en graph store y docs incorrectas
- Corregir 59 errores de golangci-lint v2
- Corregir errcheck restantes con helpers writef/writeln en CLI
- Corregir 9 errcheck restantes en cli, scoring, session stores
- Corregir últimos 3 errcheck en search y sqlite stores
- Corregir todos los defer Close sin errcheck en el proyecto
- Cambiar trigger de release a push on master

### ⚡ Performance

- Implement 17 performance optimizations across the codebase

### ⚙️ Miscellaneous Tasks

- Agregar gate de aprobación manual antes de release
- Implementar Gitflow branching model

### 💼 Other

- Agregar io.LimitReader, validar límites y permisos de archivo
- Agregar usuario non-root y HEALTHCHECK al Dockerfile

## [0.2.1] - 2026-03-28

### 🐛 Bug Fixes

- Wire GoReleaser ldflags version to CLI output

## [0.2.0] - 2026-03-28

### 🚀 Features

- Add workflow_dispatch trigger to release workflow

## [0.1.1] - 2026-03-28

### 🚀 Features

- Revamp README with badges and add install/migration scripts
- Centralize data storage under ~/.cortex/ directory

### 🐛 Bug Fixes

- Resolve CI test failures and build errors
- Remove unused net/http import from server_test.go
- Address code review findings

### 📚 Documentation

- Rewrite README and add full documentation

## [0.1.0] - 2026-03-28

### 🚀 Features

- *(ci)* Add GitHub Actions, GoReleaser, and marketplace config

# Cortex v2 Capability Matrix

## Overview

This document provides a concise, source-traceable capability matrix for Cortex v2. All capabilities are verified against repository architecture boundaries, source implementations, and configuration contracts.

### Status Taxonomy

- **Core (Zero-CGO)**: Shipped in the default pure-Go distribution; requires no CGO, external vector databases, or daemon dependencies.
- **Conditional (Build Tag)**: Functional capability compiled in with explicit Go build tags (for example, `-tags cortex_vectors`). Release binaries enable `cortex_vectors`.
- **Server (Multi-Tenant)**: Distributed server capability requiring PostgreSQL 16+, migration ledger alignment, and bearer token authorization.
- **External Adapter**: Plug-in adapter for external infrastructure (for example, Qdrant or pgvector); gated by build tags and server runtime configuration.
- **Degraded Stub**: Safe fallback implementation wired by default to maintain zero-CGO and zero-crash guarantees when optional adapters are absent.
- **Client Plugin**: External agent integration embedded in the binary or distributed as a lifecycle harness.

### Specification & Evaluation Dimensions

Every capability in this matrix is evaluated across four strict dimensions:
1. **Scope**: The runtime boundary, component responsibility, or operational tier where the capability functions.
2. **Prerequisites**: Toolchain version, compilation tags, backing infrastructure, or configuration required for activation.
3. **Source Evidence**: Repository-relative links to verified source code, migrations, or tests implementing the capability.
4. **Limitations & Constraints**: Concrete behavioral bounds, degradation paths, or non-capabilities.

> [!IMPORTANT]
> **Truthful Reporting Notice**:
> Per dispatch constraints, no test suites were executed during this documentation maintenance session. Gate evaluations and test results not executed in this session are reported as `ND/pending` (not determined) or refer to authoritative CI. No unrun runtime `PASS` claims are made.

---

## 1. Runtime Modes & Architectural Boundaries

| Capability | Status / Tier | Scope | Prerequisites | Source Evidence | Limitations & Constraints |
|---|---|---|---|---|---|
| **Local Mode (Default)** | Core (Zero-CGO) | Local CLI, TUI, local HTTP (`cortex serve`), and stdio MCP server | Go 1.26.5; local filesystem | [`cmd/cortex/main.go`](../cmd/cortex/main.go), [`internal/cli`](../internal/cli), [`internal/app/app.go`](../internal/app/app.go) | Single-tenant embedded SQLite execution wired through `internal/store/bundle`. Local code must remain zero-CGO. |
| **Server Mode** | Server (Multi-Tenant) | Distributed multi-tenant service, Streamable HTTP MCP (`/mcp`), REST API, SSE streaming | Go 1.26.5; PostgreSQL 16+ with RLS bootstrap; dual DSN configuration | [`cmd/cortex/main.go`](../cmd/cortex/main.go), [`internal/platform/server`](../internal/platform/server) | Requires external PostgreSQL; cannot run embedded SQLite database concurrently in the same process. |
| **Architecture Isolation Gate** | Core (Zero-CGO) | Static dependency and import isolation verification | Go 1.26.5 | [`internal/app/arch_test.go`](../internal/app/arch_test.go), [`AGENTS.md`](../AGENTS.md) | Enforces that local composition packages never import PostgreSQL, authz/identity, Qdrant/pgvector, or server packages. |
| **Command Bridge Constraint** | Core (Zero-CGO) | Production binary composition entrypoint | Go 1.26.5 | [`cmd/cortex/main.go`](../cmd/cortex/main.go) | `cmd/cortex` is the sole allowed bridge package between local composition and `internal/platform/server`. |

---

## 2. Persistence & Migration Engine

| Capability | Status / Tier | Scope | Prerequisites | Source Evidence | Limitations & Constraints |
|---|---|---|---|---|---|
| **SQLite v2 Baseline** | Core (Zero-CGO) | Local embedded persistence schema | Go 1.26.5; SQLite 3 | [`migrations/v2/001_init.sql`](../migrations/v2/001_init.sql), [`internal/app/app.go`](../internal/app/app.go) | Forward-only baseline. Read-only startup probe verifies SHA-256 in `cortex_meta`. Refuses v1, foreign, or corrupt databases without mutation. |
| **SQLite Migration Follow-ups** | Core (Zero-CGO) | Schema follow-up migrations (002, 003) | Go 1.26.5; applied 001 baseline | [`internal/migration/v2.go`](../internal/migration/v2.go), [`migrations/v2/003_project_artifacts.sql`](../migrations/v2/003_project_artifacts.sql) | Read-only ledger preflight (`PreflightFollowUp`) and post-apply check (`VerifyFollowUpApplied`). Append-only, forward-only, indefinite retention. |
| **PostgreSQL Schema Ledger** | Server (Multi-Tenant) | Multi-tenant schema migrations (100–109) | PostgreSQL 16+; privileged migration DSN | [`migrations/v2/100_server.sql`](../migrations/v2/100_server.sql) through `109_scoped_code_index.sql`, [`internal/migration/postgres.go`](../internal/migration/postgres.go) | Tracked in `cortex_migration_ledger`. Historical checksums (100–105) are immutable; any unexpected recorded checksum aborts rollout. |
| **Dual DSN Security** | Server (Multi-Tenant) | PostgreSQL connection pool privilege separation | Runtime non-superuser role and privileged migration role | [`internal/config/config.go`](../internal/config/config.go), [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) | Runtime role (`server.storage.dsn`) cannot execute DDL; migration role (`server.storage.migration_dsn`) cannot service API queries. |
| **Cross-Store Transactions** | Core (Zero-CGO) | Atomic multi-store local writes | Go 1.26.5; SQLite backend | [`internal/domain/interfaces.go`](../internal/domain/interfaces.go) (line 317), [`internal/store/bundle/bundle.go`](../internal/store/bundle/bundle.go) | Multi-store writes orchestrated by `domain.UnitOfWork` across memory, sessions, prompts, and graph edges with bounded busy retry. |

---

## 3. Retrieval & RAG Intelligence Stack

| Capability | Status / Tier | Scope | Prerequisites | Source Evidence | Limitations & Constraints |
|---|---|---|---|---|---|
| **Lexical Search (FTS5 / PostgreSQL)** | Core (Zero-CGO) / Server | Full-text indexing and BM25/tsvector ranking | Go 1.26.5 (local); PostgreSQL 16+ (server) | [`internal/store/search/store.go`](../internal/store/search/store.go), [`internal/store/postgres/extras.go`](../internal/store/postgres/extras.go), [`internal/store/postgres/authorized_operations.go`](../internal/store/postgres/authorized_operations.go) | Pure-Go SQLite FTS5 locally; PostgreSQL full-text search (`tsvector`/`tsquery`) in server mode; bounded candidate set. |
| **Dense Vector (SQLite BLOB)** | Conditional (Build Tag) | Local in-memory vector similarity scan | Go 1.26.5; build tag `-tags cortex_vectors` | [`internal/vector/sqlite_blob/adapter.go`](../internal/vector/sqlite_blob/adapter.go), [`internal/store/sqlite/vector_store_enabled.go`](../internal/store/sqlite/vector_store_enabled.go) | In-memory cosine scanning of float32 BLOBs in SQLite; enabled in release binaries; rejects dimension mismatches. |
| **Dense Vector Stub** | Degraded Stub | Default vector fallback when build tag omitted | Go 1.26.5 (without `cortex_vectors`) | [`internal/vector/sqlite_blob/adapter.go`](../internal/vector/sqlite_blob/adapter.go), [`internal/store/sqlite/vector_store.go`](../internal/store/sqlite/vector_store.go) | Zero-CGO fallback emitting degraded health warning and returning `ErrVectorSearchDisabled`; falls back safely to lexical search. |
| **Qdrant Vector Adapter** | External Adapter | Distributed vector search for server cluster | Go 1.26.5 with `-tags qdrant_integration`; running Qdrant cluster | [`internal/vector/qdrant`](../internal/vector/qdrant), [`AGENTS.md`](../AGENTS.md) | Server-only; isolated by immutable tenant/workspace cell boundary; fails explicitly if Qdrant endpoint is unavailable. |
| **pgvector Adapter** | External Adapter | In-database PostgreSQL vector indexing | Go 1.26.5 with `-tags pgvector_integration`; PostgreSQL with `vector` extension | [`internal/vector/pgvector`](../internal/vector/pgvector), [`AGENTS.md`](../AGENTS.md) | Server-only; enforces tenant/workspace cell boundary to prevent cross-tenant vector leakage. |
| **Reciprocal Rank Fusion (RRF)** | Core (Zero-CGO) | Hybrid rank fusion over lexical and vector results | Go 1.26.5 | [`internal/retrieval/retrieval.go`](../internal/retrieval/retrieval.go) (`FuseResults`, line 167), [`internal/store/search/store.go`](../internal/store/search/store.go) | Combines ranked lists using canonical constant $k=60$; operates on true ranked positions; supports optional exponential temporal decay. |
| **Adaptive-RAG** | Core (Zero-CGO) | Dynamic query complexity classification and routing | Go 1.26.5 | [`internal/retrieval/adaptive.go`](../internal/retrieval/adaptive.go) | 4-tier complexity classifier routing queries across lexical-only, hybrid, graph-expanded, and multi-hop retrieval paths. |
| **HippoRAG (PPR)** | Core (Zero-CGO) | Associative retrieval via Personalized PageRank | Go 1.26.5 | [`internal/domain/graph/ppr.go`](../internal/domain/graph/ppr.go) (`ComputePersonalizedPageRank`) | Power iteration Personalized PageRank (damping 0.85, max 20 iterations, tolerance 1e-6) over memory and code symbol graphs. |
| **LightRAG** | Core (Zero-CGO) | Macroscopic architectural community summaries | Go 1.26.5 | [`internal/domain/graph/community_summaries.go`](../internal/domain/graph/community_summaries.go) (`GenerateCommunitySummaries`) | Generates structured Markdown summaries for cohesive graph communities; bounded to connected subgraphs. |
| **CRAG (Corrective RAG)** | Core (Zero-CGO) | Retrieval confidence evaluation and query refinement | Go 1.26.5 | [`internal/retrieval/crag.go`](../internal/retrieval/crag.go) | Evaluates retrieval confidence; triggers at most one bounded deterministic local query reformulation on low confidence. |
| **ColBERT MaxSim Reranking** | Core (Zero-CGO) | Token-level late-interaction reranking | Go 1.26.5 | [`internal/retrieval/late_interaction.go`](../internal/retrieval/late_interaction.go) (`ComputeWeightedMaxSimScore`) | Specificity-weighted token maximum similarity scoring over top candidate evidence passages; zero external models required. |

---

## 4. Code Intelligence & Graph Analytics

| Capability | Status / Tier | Scope | Prerequisites | Source Evidence | Limitations & Constraints |
|---|---|---|---|---|---|
| **Zero-CGO AST Parser** | Core (Zero-CGO) | Static multi-language code analysis | Go 1.26.5 | [`internal/domain/ast/ast.go`](../internal/domain/ast/ast.go), [`internal/domain/ast`](../internal/domain/ast) | Static extraction of symbols and relations without LLM calls or API tokens. Supports Go, C#, F#, VB.NET, Java, Kotlin, Rust, C/C++, PHP, Ruby, Swift, TypeScript, JavaScript, Python, and SQL. |
| **Structural Relations** | Core (Zero-CGO) | Directed code entity relationship extraction | Go 1.26.5 | [`internal/domain/ast/ast.go`](../internal/domain/ast/ast.go) (`CodeRelationship`, line 39) | Extracts typed structural edges: `defines`, `imports`, `calls`, `implements`, `uses`, `contains`, `extends`, `instantiates`. |
| **Continuous Code Watcher** | Core (Zero-CGO) | Filesystem change watcher daemon (`cortex watch`) | Go 1.26.5; active process | [`internal/domain/code/watcher.go`](../internal/domain/code/watcher.go) | Incremental AST indexing on file modifications; debounced file events; skips gitignored files and non-source assets. |
| **Community Detection** | Core (Zero-CGO) | Functional cluster discovery in codebase graph | Go 1.26.5 | [`internal/domain/graph/analytics.go`](../internal/domain/graph/analytics.go) (`DetectCommunities`, line 117) | Partitions codebase graph into cohesive functional clusters with automated Hub Node labeling based on degree centrality. |
| **Architectural Bottlenecks (God Nodes)** | Core (Zero-CGO) | High-centrality node detection | Go 1.26.5 | [`internal/domain/graph/analytics.go`](../internal/domain/graph/analytics.go) (`FindGodNodes`, line 42) | Detects disproportionately connected hubs (`in_degree` + `out_degree`) while filtering utility types (`string`, `error`, `context.Context`). |
| **Dependency Cycles (Tarjan SCC)** | Core (Zero-CGO) | Circular dependency and import loop detection | Go 1.26.5 | [`internal/domain/graph/analytics.go`](../internal/domain/graph/analytics.go) (`FindCycles`, line 334) | Identifies circular dependencies and import loops across modules via Strongly Connected Components. |
| **Blast Radius Analysis** | Core (Zero-CGO) | Change impact propagation across call graph | Go 1.26.5 | [`internal/domain/graph/analytics.go`](../internal/domain/graph/analytics.go) (`CalculateBlastRadius`, line 392) | Computes upstream callers, downstream dependents, affected files, and graph impact percentage up to bounded hop count. |
| **Scoped Code Indexing (Migration 109)** | Server (Multi-Tenant) | Partitioned AST symbol and relation storage | PostgreSQL 16+; migration 109 applied | [`migrations/v2/109_scoped_code_index.sql`](../migrations/v2/109_scoped_code_index.sql) | Partitioned by composite `(tenant_id, workspace_id, project_id)` keys under forced RLS; legacy project-only rows not backfilled. |

---

## 5. User Interfaces & Integration Transports

| Capability | Status / Tier | Scope | Prerequisites | Source Evidence | Limitations & Constraints |
|---|---|---|---|---|---|
| **CLI Command Suite** | Core (Zero-CGO) | Interactive and scriptable command-line interface | Go 1.26.5 | [`internal/cli`](../internal/cli), [`cmd/cortex/main.go`](../cmd/cortex/main.go) | 20+ commands: `setup`, `search`, `save`, `context`, `stats`, `timeline`, `revisions`, `tui`, `serve`, `mcp`, `doctor`, `reindex`, `gc`, `config`, `auth`, `export`, `import`, `sync`, `merge-projects`, `code`, `watch`, `migrate status`. |
| **Terminal UI (TUI)** | Core (Zero-CGO) | Interactive Bubble Tea terminal dashboard | Go 1.26.5; interactive TTY | [`internal/tui`](../internal/tui) | Dynamic theme engine (`t` key); authentication modal (`L` key); keyboard navigation. |
| **Local MCP Server (stdio)** | Core (Zero-CGO) | Model Context Protocol server over stdio | Go 1.26.5; stdio transport | [`internal/mcp/server.go`](../internal/mcp/server.go) | Namespace is `cortex_*`. Profiles: `agent`, `admin`, and `temporal`. Operates on integer observation IDs. |
| **Remote MCP Stdio Proxy** | Core (Zero-CGO) | Stdio proxy forwarding MCP calls to remote server | Go 1.26.5; configured `mcp.remote.enabled: true` | [`internal/mcp/proxy.go`](../internal/mcp/proxy.go) | Forwards local stdio MCP calls to remote Cortex server (`/mcp`) via bearer token authentication. |
| **Server Streamable HTTP MCP** | Server (Multi-Tenant) | Authenticated Streamable HTTP MCP at `/mcp` | PostgreSQL 16+; bearer token authentication | [`internal/platform/server/http.go`](../internal/platform/server/http.go) | Exposes 16+ tools operating on public UUIDs via `AuthorizedStore` under tenant/workspace RLS binding. |
| **Local HTTP API** | Core (Zero-CGO) | Local REST service (`cortex serve`) | Go 1.26.5; SQLite backend | [`internal/http/server.go`](../internal/http/server.go) | Refuses non-loopback binding without `http.token`. Public `/health` endpoint. |
| **Server REST API** | Server (Multi-Tenant) | Distributed multi-tenant REST service | PostgreSQL 16+; bearer token authentication | [`internal/platform/server/http.go`](../internal/platform/server/http.go) | Bearer-authenticated endpoints for memory, graph analytics, blast radius, AST ingestion, and administrative stats. |
| **Conversational Project Agent** | Server (Multi-Tenant) | Web assistant (`POST /api/agent/answer`, `/api/agent/stream`) | Server runtime; configured LLM provider; authorized principal | [`internal/domain/agent`](../internal/domain/agent), [`web/src/app/agent`](../web/src/app/agent), [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) | Read-only capability scope; content-free retrieval trace; server-validated citation handles; ephemeral browser memory (max 6 turns). |
| **Web Dashboard (Next.js)** | Core (Node/TS) | Next.js 14 control room application | Node >= 24; modern web browser | [`web/src`](../web/src) | BOLA authorization enforcement; graph visualization; Louvain cluster view; interactive blast radius; Obsidian vault exporter. |

---

## 6. Client Plugins & Ecosystem Integrations

| Capability | Status / Tier | Scope | Prerequisites | Source Evidence | Limitations & Constraints |
|---|---|---|---|---|---|
| **OpenCode Event Plugin** | Client Plugin | OpenCode IDE extension and event hooks | Node >= 24; OpenCode runtime | [`plugin/opencode/cortex.ts`](../plugin/opencode/cortex.ts), [`plugin/opencode/embed.go`](../plugin/opencode/embed.go) | Embedded via `//go:embed`. Auto-starts server, tracks sessions, suppresses subagent session pollution, injects memory protocol on compaction. |
| **Claude Code Lifecycle Hooks** | Client Plugin | Claude Code CLI lifecycle integration | Claude Code CLI; bash, jq, python3, timeout | [`plugin/claude-code`](../plugin/claude-code), [`docs/PLUGINS.md`](PLUGINS.md) | 5 lifecycle hooks (`SessionStart`, `UserPromptSubmit`, `SubagentStop`, `Stop`); automated context injection and session tracking. |
| **Obsidian Markdown Projection** | Core (Zero-CGO) | Read-only vault exporter with `[[WikiLinks]]` | Go 1.26.5; export directory | [`internal/projection/obsidian`](../internal/projection/obsidian) | Read-only exporter; neutralizes Windows reserved device names (`CON`, `PRN`, `AUX`, `NUL`, `COM1-9`, `LPT1-9`); prevents case-insensitive collisions. |
| **Observation Chunk Synchronization** | Core (Zero-CGO) | Local-to-remote observation synchronization | Go 1.26.5; remote endpoint credentials | [`internal/sync`](../internal/sync) | Deterministic chunk synchronization between local SQLite stores and remote endpoints (`cortex sync`); privacy preflight validated. |

---

## 7. Security, Privacy & Authorization Boundaries

| Capability | Status / Tier | Scope | Prerequisites | Source Evidence | Limitations & Constraints |
|---|---|---|---|---|---|
| **Tenant Isolation & RLS** | Server (Multi-Tenant) | Multi-tenant data segregation | PostgreSQL 16+; `AuthorizedStore` | [`internal/authz`](../internal/authz), [`internal/store/postgres`](../internal/store/postgres) | Tenant and workspace derive from verified principal grants, never client input. Postgres operations run under forced RLS via `AuthorizedContext`. |
| **Zero-Secret Logging Policy** | Core / Server | Logging and telemetry across all packages | Go 1.26.5 | [`internal/config/config.go`](../internal/config/config.go), [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) | DSNs, bearer tokens, API keys, grant digests, and passphrases are strictly masked and never written to logs or stdout. |
| **Privacy Tag Stripping & Redaction** | Core / Client Plugin | Ingestion text sanitation | Go 1.26.5; `internal/domain/privacy` | [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go), [`plugin/opencode/cortex.ts`](../plugin/opencode/cortex.ts) | Redacts content enclosed in `<private>...</private>` tags; replaces with deterministic `[REDACTED]`; requires public residual content. |
| **AST Non-Interference (`REQ-PRIV-007`)** | Core (Zero-CGO) | Code intelligence structural integrity | Go 1.26.5 | [`internal/domain/ast/ast.go`](../internal/domain/ast/ast.go), [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go), [`AGENTS.md`](../AGENTS.md) | Code structural intelligence (symbols, signatures, doc summaries, relationships, reasoning) is structural metadata and is NEVER mutated or redacted by privacy routines. |

---

## 8. Build Identity & Binary Provenance

| Capability | Status / Tier | Scope | Prerequisites | Source Evidence | Limitations & Constraints |
|---|---|---|---|---|---|
| **Binary Identity Contract** | Core (Zero-CGO) | Statistical campaign executable provenance | Go 1.26.5 | [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go) (`BinaryIdentity`, line 29) | Schema version `binary-identity/v1`. Validates non-zero lowercase SHA-256 for binary, source tree, tool, and argv. |
| **Toolchain Pinning** | Core (Zero-CGO) | Compilation environment determinism | Go 1.26.5 (`go1.26.5`) | [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go) (line 20), [`.github/workflows/ci.yml`](../.github/workflows/ci.yml) | Tool version must match portable Go pattern (`^go[0-9]+\.[0-9]+\.[0-9]+$`). CI enforces exact `go1.26.5`. |
| **Approved Build Identity** | Core (Zero-CGO) | Reproducible compilation configuration | Go 1.26.5 with `-trimpath` | [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go) (line 23) | Pinned to `ApprovedBuildIdentity = "go-test-c-trimpath-v1"`. Binaries built without approved flags fail identity validation. |
| **Publication Digest Binding** | Core (Zero-CGO) | Statistical publication artifact integrity | Go 1.26.5 | [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go) (`PublicationBinding`, line 98) | Binds binary identity digest and protocol identity digest. Disallows unknown or duplicate JSON fields during unmarshaling. |

---

## 9. Data & Log Retention Policy

| Capability | Status / Tier | Scope | Prerequisites | Source Evidence | Limitations & Constraints |
|---|---|---|---|---|---|
| **Indefinite Artifact Retention (`REQ-RET-001`)** | Core / Server | Project Context Protocol artifact versions | Applied migration 003 (SQLite) or 106 (PostgreSQL) | [`internal/domain/projectprotocol/doc.go`](../internal/domain/projectprotocol/doc.go) (line 18), [`migrations/v2/003_project_artifacts.sql`](../migrations/v2/003_project_artifacts.sql) | Skill and rule revisions, activations, and audit events are retained indefinitely. History never leaves the ledger. |
| **Non-Destructive Soft Deletion** | Core / Server | Artifact lifecycle state transitions | SQLite or PostgreSQL persistence | [`internal/domain/projectprotocol/doc.go`](../internal/domain/projectprotocol/doc.go) (line 19), [`internal/domain/projectprotocol/ports_test.go`](../internal/domain/projectprotocol/ports_test.go) | Package defines NO hard-delete or purge operations. Deletion is exclusively a soft-delete state transition. |
| **No-Purge SQL Schema Invariant** | Server (Multi-Tenant) | PostgreSQL database triggers and grants | PostgreSQL 16+; `cortex_app` role | [`migrations/v2/106_project_artifacts.sql`](../migrations/v2/106_project_artifacts.sql) (line 1567), [`internal/migration/postgres_integration_test.go`](../internal/migration/postgres_integration_test.go) | Grants explicitly omit `DELETE` privilege on artifact tables. Attempts to execute SQL DELETE trigger transaction aborts. |
| **Monotonic Accounting Invariant** | Core (Zero-CGO) | Storage and byte usage tracking | Go 1.26.5 | [`internal/migration/v2_test.go`](../internal/migration/v2_test.go) (line 1436) | Byte totals and usage counters monotonically increase; cannot be reset, cleared, or decremented. |

---

## 10. Privacy Matrix & Component Protections

| Subsystem / Interface | Protection Mechanism | Fields Protected | Error Behavior | Source Evidence |
|---|---|---|---|---|
| **Domain Privacy Engine** | Exact/case-insensitive `<private>...</private>` parsing and `[REDACTED]` replacement | Text prose, custom fields, multi-field envelopes | Rejects malformed tags (`ErrCodeInvalidMarker`), empty residual (`ErrCodeRequiredEmpty`), invalid UTF-8 (`ErrCodeInvalidUTF8`) | [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go) |
| **CLI (`save`, `import`, `export`)** | Input sanitization and residual validation | Observation titles, content, prompt text | Rejects inputs lacking residual public content; preserves sanitized output without logging secrets | [`internal/cli`](../internal/cli), [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go) |
| **MCP Tools (`cortex_save`)** | Pre-persistence privacy filter | Observation content, tags, notes | Returns validation error to calling agent if private tags malformed or residual empty | [`internal/mcp/tools_memory.go`](../internal/mcp/tools_memory.go), [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go) |
| **HTTP Server (`/api/memory`)** | Request body privacy filter | Memory observation content, session summaries | HTTP 400 Bad Request with payload-free sanitized error message | [`internal/http/server.go`](../internal/http/server.go), [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go) |
| **Store Bundle (UnitOfWork)** | Transactional privacy enforcement | Multi-store records enlisted in atomic save | Fails transaction closed before committing dirty buffers if privacy validation fails | [`internal/store/bundle/bundle.go`](../internal/store/bundle/bundle.go), [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go) |
| **Sync Protocol (`cortex sync`)** | Chunk privacy preflight | Observation chunks in transit | Halts synchronization before network transmission on any privacy violation | [`internal/sync/sync.go`](../internal/sync/sync.go), [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go) |
| **Code Intelligence (AST)** | Non-interference boundary (`REQ-PRIV-007`) | Code symbols, signatures, doc summaries, relationships, reasoning | AST structures are structural code metadata and are NEVER mutated or redacted by privacy routines | [`internal/domain/ast/ast.go`](../internal/domain/ast/ast.go), [`AGENTS.md`](../AGENTS.md) |

---

## 11. Synthetic & Safe Benchmark Fixtures

| Fixture / Corpus | Origin & Review | Scope & Coverage | Safety Guarantees | Source Evidence |
|---|---|---|---|---|
| **Cortex Native Retrieval Corpus** | `origin: "cortex-authored-synthetic"`, `privacy_review: "synthetic-only-no-real-prompts-vendor-rows-private-data-or-secrets"` | Multi-project isolation collision queries, temporal filters, lifecycle states | 100% synthetic records; zero real user prompts, zero vendor data rows, zero API credentials, zero personal data (PII) | [`bench/evidence/cortex-native/v1/corpus.json`](../bench/evidence/cortex-native/v1/corpus.json), [`bench/common/corpus_test.go`](../bench/common/corpus_test.go) |
| **Synthetic Authority Fixture** | Authored synthetic test dataset | Cross-project and cross-tenant isolation boundaries | Synthetic project keys (`project-a`, `project-b`); verifies zero cross-boundary leakage under adversarial queries | [`bench/fixtures/cortex-native/authority.jsonl`](../bench/fixtures/cortex-native/authority.jsonl), [`bench/fixtures/cortex-native/authority_fixtures_test.go`](../bench/fixtures/cortex-native/authority_fixtures_test.go) |
| **Synthetic Collision Fixture** | Authored synthetic test dataset | Lexical token collision and hard-negative ranking decoys | Evaluates ranking discrimination against deceptive lexical decoys without proprietary or external passages | [`bench/fixtures/cortex-native/collision.jsonl`](../bench/fixtures/cortex-native/collision.jsonl), [`bench/fixtures/cortex-native/collision_fixtures_test.go`](../bench/fixtures/cortex-native/collision_fixtures_test.go) |

---

## 12. Evidence Limits & Evaluation Protocol

| Evidence Policy | Classification | Operational Rules & Thresholds | Source Evidence |
|---|---|---|---|
| **Universal Correctness Gates** | Mandatory Release Invariant | Exactly zero isolation violations permitted (any cross-tenant or cross-project leak blocks release); exact match with authoritative eligible stable-ID set. | [`docs/BENCHMARKS.md`](BENCHMARKS.md), [`bench/common/gates.go`](../bench/common/gates.go) |
| **Relevance Ground Truth Bounds** | Metric Classification | Retrieval claims must evaluate against verified stable-ID sets (episode/fact IDs) or half-open byte spans. Raw answer-token F1 / ROUGE-L alone cannot substantiate retrieval relevance. | [`docs/BENCHMARKS.md`](BENCHMARKS.md) |
| **Semantic Workload Limits** | Code Review Boundary | Source logic changes bounded to $\le 350$ LOC (Go/Rust/Java) or $\le 250$ LOC (TS/Python); tests bounded to $\le 600$ LOC total with modular test files $\le 250$ LOC. | [`AGENTS.md`](../AGENTS.md) |
| **Conversational Agent Ephemeral Limits** | Runtime Architectural Boundary | Web agent retains at most 6 turn pairs in memory; cleared on project switch; retrieval traces are content-free projections; provider citations must resolve to verified server handles. | [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) |
| **Gate Preregistration Invariant** | Statistical Methodology | Release gates, metric directions, sample sizes, and tolerances must be preregistered before observing candidate results. Held-out decision evidence must never be reused for calibration. | [`docs/BENCHMARKS.md`](BENCHMARKS.md), [`bench/common/gates_test.go`](../bench/common/gates_test.go) |

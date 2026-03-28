# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Cortex is a memory server for AI coding assistants, built as a single Go binary with SQLite + FTS5. It aims for 100% API compatibility with Engram while adding knowledge graph, importance scoring, auto-archival, and optional vector search. Interfaces: MCP server, HTTP API, CLI, TUI.

## Branching Model (Gitflow)

- **`master`** — Production-ready code. Only receives merges from `release/*` or `hotfix/*` branches. Every merge triggers the release pipeline (tests → approval → GoReleaser).
- **`develop`** — Integration branch. All feature work merges here first.
- **`feat/*`** — Feature branches, created from `develop`, merged back to `develop` via PR.
- **`release/*`** — Release prep branches, created from `develop`, merged to both `master` and `develop`.
- **`hotfix/*`** — Urgent fixes, created from `master`, merged to both `master` and `develop` via separate PRs.

PRs to `develop`: run CI (unit + e2e + lint).
PRs to `master`: run CI + release pipeline with manual approval gate via `production` environment.

Note: No auto-sync of master → develop after release. Since releases come from merging develop into master, they're already in sync. Only hotfixes require manually merging master back to develop.

## Build & Development Commands

```bash
make build            # Build binary -> bin/cortex
make test             # Run all tests (go test -v ./...)
make test-coverage    # Generate HTML coverage report in coverage/
make lint             # golangci-lint run
make fmt              # gofmt -s -w
make tidy             # go mod tidy
make dev              # build then run
make watch            # Hot reload with air
make security         # govulncheck
make generate-mocks   # go generate ./...
```

Run a single test:
```bash
go test -v -run TestFunctionName ./internal/domain/memory/
```

Run benchmarks:
```bash
go test -bench=. ./path/to/package/
```

Vector search is behind a build tag:
```bash
go build -tags cortex_vectors ./cmd/cortex
```

Docker build:
```bash
docker build -t cortex .
```

## Architecture

**Domain-Driven Design with Repository Pattern:**

```
cmd/cortex/          -> CLI entry point (main.go)
internal/
  app/               -> App struct: wires config, DB, migrator, stores, and MCP server
  cli/               -> CLI command dispatch (Run function, subcommands)
  setup/             -> Agent integration setup (Claude Code, OpenCode, etc.)
  config/            -> Viper-based YAML + env var config (8 sections)
  database/          -> SQLite connection manager (WAL mode, modernc.org/sqlite)
  domain/
    models.go        -> Core types: Observation, Session, Edge, Prompt, ImportanceScore
    interfaces.go    -> Repository interfaces (ObservationRepo, SessionRepo, SearchRepo, GraphRepo, ScoringRepo, VectorRepo, EntityRepo)
    errors.go        -> Domain-specific sentinel errors
    memory/          -> Observation CRUD service (validation, topic key upsert, dedup)
    scoring/         -> Importance scoring (base + type bonus + access + recency + edges - age)
    search/          -> FTS5 query sanitization, result limiting
    graph/           -> Knowledge graph with BFS traversal (max depth 10)
    session/         -> Session lifecycle management
    lifecycle/       -> Auto-archival service (periodic check, soft-delete old low-score obs)
    entity/          -> Entity extraction (files, URLs, packages, symbols) + linking service
  store/
    sqlite/          -> Observation store (topic key upsert, SHA-256 dedup, soft delete)
    session/         -> Session store
    search/          -> FTS5 search store (BM25 ranking)
    prompt/          -> Prompt store
    graph/           -> Knowledge graph store (edges, BFS traversal)
    scoring/         -> Importance scoring store (scores, access tracking, edge counts)
    entity/          -> Entity link store (save, query by observation or entity)
  migration/         -> Version-tracked migration framework (up/down with transactions)
  mcp/               -> MCP server + tool handlers (tools_memory.go: 14 Engram tools, tools_cortex.go: 5 exclusive tools)
  http/              -> REST API server (net/http stdlib, JSON endpoints for all stores)
  tui/               -> Terminal UI (simplified browser, full BubbleTea TUI pending dependency)
migrations/          -> SQL migration files (001-006)
testutil/            -> Test helpers: in-memory DB setup, fixtures, custom assertions
```

**Pending full implementation**: TUI uses a simplified browser; full BubbleTea-based TUI requires adding the `charmbracelet/bubbletea` dependency.

**Data flow**: `main.go` -> `cli.Run()` -> `app.Open()` wires dependencies -> domain services depend on repository interfaces -> store implementations satisfy those interfaces using SQLite.

**Dependency wiring**: `app.Open()` creates Config, database Manager, Migrator, all seven stores (sqlite, session, search, prompt, graph, scoring, entity), vector store, and starts auto-archival if enabled. The `App` struct bundles these for use by CLI commands, MCP server, and HTTP API.

## MCP Tools

**14 Engram-compatible** (tools_memory.go): mem_save, mem_search, mem_context, mem_session_summary, mem_get_observation, mem_save_prompt, mem_update, mem_suggest_topic_key, mem_session_start, mem_session_end, mem_stats, mem_delete, mem_timeline, mem_capture_passive.

**5 Cortex-exclusive** (tools_cortex.go): mem_relate (create graph edge), mem_graph (traverse graph), mem_score (importance scoring), mem_archive (soft-delete), mem_search_hybrid (FTS5 + optional vector RRF fusion).

## Key Design Decisions

- **Pure Go SQLite** via `modernc.org/sqlite` — no CGo, no external dependencies
- **MCP via mcp-go**: Uses `github.com/mark3labs/mcp-go` for MCP protocol; server runs over stdio
- **FTS5 auto-sync**: Triggers in migration 002 keep the FTS index in sync on INSERT/UPDATE/DELETE
- **Topic key upsert**: Observations with a `topic_key` update-or-create within the same project scope
- **Content deduplication**: Normalized SHA-256 hash prevents duplicate observations within a time window
- **Soft delete by default**: `deleted_at` field; hard delete requires explicit flag
- **Optional vector search**: Gated by `cortex_vectors` build tag; disabled build returns `ErrVectorSearchDisabled`
- **Importance score formula**: `base(0.5) + typeBonus + accessBonus + recencyBonus + edgeBonus - agePenalty`, clamped to [0.0, 5.0]
- **Auto-archival**: `lifecycle.ArchivalService` periodically soft-deletes observations older than `auto_archive_days` with score below `min_archive_score`

## Testing

- Table-driven tests throughout; target 70%+ coverage
- Test utilities in `testutil/`: `NewTestDB()` provides an in-memory SQLite with migrations applied
- `testutil/fixtures.go` has helpers for creating test observations, sessions, edges, importance scores
- `testutil/assertions.go` has domain-specific assertion helpers
- Store tests use `setupTestStore()` with inline migration SQL in a `migration.Registry`
- Integration tests tagged with `// +build integration`
- Tests are collocated with source (`*_test.go` next to implementation)

## SQL Migrations

Six migrations in `migrations/`: init schema (001), FTS5 (002), graph edges (003), scoring (004), vectors (005), entity links (006). Each has `-- +migrate Up` and `-- +migrate Down` sections. The migration framework tracks versions in a `_migrations` table.

## Configuration

Loaded from `cortex.yaml` / env vars (`CORTEX_<SECTION>_<KEY>`). Eight sections: `server`, `database` (path, pragma), `mcp`, `http` (port, host), `logging` (level, format), `search` (default_limit, max_limit, fts5, vector, fusion_k), `memory` (max_observation_length, dedupe_window, auto_archive_days, importance_decay_half_life, min_archive_score), `lifecycle` (enable_auto_archive, archive_check_interval).

**Centralized data directory**: All data is stored under `~/.cortex/` by default. The database defaults to `~/.cortex/cortex.db`, and config is searched in `~/.cortex/cortex.yaml` (among other paths). The directory is auto-created on first run.

## CLI Commands

`mcp`, `search`, `save`, `timeline` (with --before/--after), `context`, `stats`, `setup`, `import` (--from-engram, --from-json), `export` (--project, --output), `migrate` (up/down/status), `serve` (HTTP REST API), `tui`, `version`, `help`.

## Agent Skills

See `AGENTS.md` for 12 task-specific skill files in `skills/` that document patterns and rules for specific subsystems (architecture, MCP parity, graph, search, lifecycle, storage, config, testing, migration, HTTP, CLI, TUI).

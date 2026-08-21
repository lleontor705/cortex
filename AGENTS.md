# Cortex Agent Guide

## Sources Of Truth

- This is a Go repository; `package.json` exists only to install Husky. `npm test` intentionally fails. Use the Makefile, Go commands, and `.github/workflows/ci.yml` for verification.
- Use Go 1.26.5 (pinned by `go.mod` and CI) and golangci-lint v2.11.4 (pinned by CI).
- `AGENTS.md` is the canonical engineering guide. `CLAUDE.md` only points here; current product references live under `docs/`.

## Runtime Boundaries

- `cmd/cortex/main.go` is the production entrypoint. Default/local mode dispatches to `internal/cli`, whose commands open the SQLite composition in `internal/app`.
- `internal/domain` owns models, ports, and business services; `internal/store/*` implements persistence; `internal/store/bundle` is the dependency bundle used by local CLI, MCP, HTTP, and TUI paths.
- Preserve the architecture gate in `internal/app/arch_test.go`: local code must remain zero-CGO and must not import PostgreSQL, authz/identity, Qdrant/pgvector, or `internal/platform/server`. `cmd/cortex` is the sole allowed bridge to server composition.
- `cortex --mode server` serves the PostgreSQL composition over authenticated HTTP (`/api/*`) and Streamable HTTP MCP (`/mcp`); it is distinct from the SQLite-backed local `serve` command.
- Server persistence must remain behind `AuthorizedStore`/`AuthorizedContext`. Tenant and workspace come from verified principal grants, never client input; do not expose raw PostgreSQL repositories or transaction/scoring accessors from the server runtime.
- Local MCP tools and profiles are defined in `internal/mcp/server.go`; server MCP is a separate authenticated subset in `internal/platform/server/http.go`. The supported namespace is `cortex_*`; tests explicitly reject legacy `mem_*` names and Engram framing. Profiles are local-only: `agent`, `admin`, and `temporal`.

## Schema And Vectors

- Local startup is driven by the embedded, forward-only `migrations/v2/001_init.sql` baseline. The root `migrations/001-014` files are retired v1 history and do not drive startup.
- `app.Open` performs a read-only compatibility probe before opening SQLite for writes. Existing v1, Engram, foreign, corrupt, or checksum-mismatched databases must be refused without mutation; there is no automatic v1 upgrade.
- The raw v2 SQL is SHA-256 identified in `cortex_meta`. Editing `migrations/v2/001_init.sql` changes the identity and makes existing v2 databases fail validation. PostgreSQL uses the separate embedded `migrations/v2/100_server.sql` and migration ledger.
- Rollout of SQLite follow-up 003 (version 2003) and PostgreSQL 106 is governed by the read-only ledger preflight and post-apply checks in `internal/migration` (`V2Baseline.PreflightFollowUp`/`VerifyFollowUpApplied`, `PostgresServerMigration.Preflight`/`VerifyApplied`) and the runbook in `docs/project-context-protocol-identity-privilege.md`. Expected rollout state is unledgered; ANY recorded checksum for the target version stops the rollout (prior checksums escalate as tamper-class), ledgers are append-only, and rollback is forward-only/non-destructive. Historical SQL checksums are pinned in `v2_test.go`/`postgres_test.go`: 001/002 and 100-105 are immutable forever; the unshipped 003/106 pins move with reviewed bytes until release. Migration changes must pass `go test -v -count=1 ./internal/migration`.
- Default builds wire a degraded `sqlite_blob` vector stub. Build/test with `-tags cortex_vectors` for the functional SQLite BLOB cosine-scan adapter. Release artifacts enable this tag; ordinary `make build` does not.
- External vector adapters are server-only. Their opt-in suites use `qdrant_integration` and `pgvector_integration`; do not silently fall back between providers.

## Commands

```bash
go mod download
make build                 # bin/cortex; default zero-CGO vector stub
make fmt                   # gofmt -s -w .
golangci-lint run ./...
go test -v -count=1 ./...  # CI unit/default gate
```

- Run one package/test with `go test -v -count=1 ./internal/<pkg> -run '^TestName$'`.
- Run a benchmark with `go test -bench=. -run '^$' ./path/to/package`.
- The Husky pre-push order is lint, then `go test -v ./...`. Before broad changes, also use the CI race gate: `go test -race -count=1 ./internal/store/search ./internal/store/bundle ./internal/mcp`.
- Retrieval changes must pass the offline gate: `go test -v -count=1 ./bench ./bench/common ./bench/cortex ./bench/fixtures/cortex-native ./bench/cortex/cmd/baseline`. It needs no model, dataset download, or network.
- Obsidian/path changes need the Windows/macOS portability gate: `go test -v -count=1 ./internal/projection/obsidian -run 'Test(SafeSlug|WindowsDeviceNameNearMisses|CanonicalPathKey|ExportCanonicalCollision|ExportRejectsCaseInsensitiveCollision)'`.
- Plugin gates: in `plugin/opencode` run `npm ci && npm test` (Vitest harness, Node >= 24, own lockfile); run `bash plugin/claude-code/scripts/hooks_test.sh` for the Claude hooks contract harness, which needs bash, jq, python3, and timeout and exits 127 when a tool is missing.

## Integration Tests

- Complete tagged CI suite: `go test -v -count=1 -tags "integration postgres_integration" ./...`. The meaningful build constraint is `postgres_integration`; MCP and sync files named `integration_test.go` already run in the default suite.
- PostgreSQL integration requires PostgreSQL 16 plus `CORTEX_TEST_POSTGRES_DSN`, `CORTEX_TEST_POSTGRES_MIGRATION_DSN`, and `CORTEX_TEST_POSTGRES_AUTHZ_ADMIN_DSN`. Missing DSNs fail rather than skip. Bootstrap the non-superuser/RLS roles with `scripts/postgres/bootstrap-authz.sql` as CI does.
- CI's coverage gate includes PostgreSQL: `go test -tags postgres_integration -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...`; total coverage must be at least 70%.
- Optional adapter checks: `go test -v -count=1 -tags cortex_vectors ./internal/vector/sqlite_blob`, `go test -v -count=1 -tags qdrant_integration ./internal/vector/qdrant`, and `go test -v -count=1 -tags pgvector_integration ./internal/vector/pgvector`.
- Gates that cannot run locally are BLOCKED, not silently skipped: PostgreSQL suites fail without the three DSNs, the race gate needs CGO with a working gcc, and the Claude harness needs jq. On workstations missing these (typical Windows setups), report BLOCKED and rely on `.github/workflows/ci.yml` (ubuntu-latest), which is the authoritative executor; release re-runs the same suite before its manual approval gate.

## Change Conventions

- Use repository interfaces for domain services and concrete stores only at composition boundaries. Cross-store local writes use `domain.UnitOfWork` through `internal/store/bundle`.
- Store tests commonly use `testutil.NewTestDBWithMigrations` with an inline `migration.Registry`; `testutil.NewTestDB` alone opens an empty in-memory database.
- Local HTTP refuses non-loopback binding without `http.token`; `/health` remains unauthenticated. Preserve this when changing server or auth behavior.
- Configuration standards: The configuration system supports YAML, JSON, and TOML with atomic writes. Enforce the Zero-Bloat model: local configuration files must never serialize internal SQLite pragmas or server-specific multi-tenant/vector parameters. Use the unified `ai.` namespace (`ai.provider`, `ai.model`, `ai.base_url`) for LLM/embedding configuration.
- CLI standards: Expose `cortex config` (`get`, `set`, `init`, `wizard`, `show`, `validate`, `path`) and `cortex auth` (`login`, `status`, `logout`) for scriptable operations.
- TUI standards: Use the dynamic theme engine in `internal/tui/styles.go` (`ApplyTheme`/`ToggleTheme` with `t` key) and auth modal (`L` key).
- Web Frontend standards: `web/` is a Next.js application. Enforce BOLA authorization rules (members can only delete observations matching their token subject; admins retain tenant-wide management), project-level cloud sync toggles, role-based route gating, and CSS variable light/dark themes.
- PR validation targets `develop` and `master`: the body must contain `Closes #N`, `Fixes #N`, or `Resolves #N` for an issue labeled `status:approved`, and the PR needs exactly one `type:*` label. Note that ordinary CI currently targets `main` and `develop`, while release runs on pushes to `master`; inspect workflows instead of assuming branch names are consistent.

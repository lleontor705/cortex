# Architecture

## Runtime Modes

`cmd/cortex/main.go` is the only production entrypoint.

- Local mode is the default. `internal/cli` opens `internal/app`, which wires SQLite, domain services, stores, MCP stdio, local HTTP, and TUI.
- Server mode is selected with `cortex --mode server`. `internal/platform/server` applies the PostgreSQL schema, creates an authorized store, and serves authenticated HTTP plus Streamable HTTP MCP.
- `cmd/cortex` is the only package allowed to bridge local composition to `internal/platform/server`.
- Local packages must remain zero-CGO and must not import PostgreSQL, authz/identity, Qdrant/pgvector, or server composition. `internal/app/arch_test.go` enforces this.

## Local Data Flow

```text
cmd/cortex -> internal/cli -> internal/app -> SQLite database
                                      -> store/bundle
                                      -> domain services
                                      -> MCP stdio / local HTTP / TUI
```

`internal/domain` owns models, ports, and business services. `internal/store/*`
implements persistence. `internal/store/bundle` coordinates cross-store local
writes through `domain.UnitOfWork`.

## Server Data Flow

```text
HTTP or MCP request
  -> bearer authentication
  -> configured service principal and grants
  -> authz.AuthorizedContext
  -> postgres.AuthorizedStore operations
  -> PostgreSQL transaction with tenant binding and RLS
```

Server transports receive operation capabilities only. They must not receive raw
PostgreSQL repositories, transactions, scoring primitives, or client-selected
tenant authority.

The current server transport represents one configured service account; the
bearer secret authenticates callers as that account. Tenant comes from its
configured organization. Workspace, project, role, scope, ownership, and
classification are checked as grants. PostgreSQL remains
behind `AuthorizedStore`; architecture tests reject raw accessors.

## Storage And Migrations

Local startup is driven by the embedded forward-only baseline
`migrations/v2/001_init.sql`:

1. `app.Open` probes an existing file read-only.
2. v1, Engram, foreign, corrupt, partial, and checksum-mismatched databases are refused without mutation.
3. The v2 baseline is applied atomically and records its SHA-256 identity in `cortex_meta`.
4. Existing v2 databases must retain the same baseline checksum.

The root `migrations/001-014` files are retired v1 history. They do not drive
local startup and must not be edited as a way to change the v2 schema. PostgreSQL
uses the separate embedded `migrations/v2/100_server.sql` and its server ledger.

The v2 baseline is forward-only. Do not document or implement destructive local
rollback as a normal upgrade path.

## MCP

The supported namespace is `cortex_*`. Legacy `mem_*` names and Engram framing
are intentionally rejected by tests.

Profiles are defined in `internal/mcp/server.go`:

- `agent` contains ordinary memory, graph, scoring, revision, and project tools.
- `admin` contains destructive and curation tools.
- `temporal` contains temporal graph and observability tools.

Local MCP uses stdio. Server MCP uses Streamable HTTP at `/mcp` and requires the
server bearer token.

## HTTP

Local `cortex serve` uses SQLite stores and binds to the configured local HTTP
address. It refuses non-loopback binding without `http.token`; `/health` stays
public.

Server HTTP uses PostgreSQL authorized operations. `/health` is public; `/api/*`
and `/mcp` require a bearer token. Request bodies and result limits are bounded.
The web dashboard uses read-only authorized operations for workspace statistics,
sessions, visible project keys, and audit events. Project grants remain server-side
principal authority and cannot be changed through the dashboard.

## Vectors

The default local build wires a degraded `sqlite_blob` stub so it remains
zero-CGO. Build with `-tags cortex_vectors` for SQLite BLOB cosine scanning.
Release artifacts enable this tag. Qdrant and pgvector are external,
server-only adapters with separate integration build tags.

## Configuration

Configuration is YAML plus `CORTEX_*` environment overrides. Local defaults use
`~/.cortex/cortex.db`. Server storage has separate fields for:

- `server.storage.dsn`: non-superuser runtime connection.
- `server.storage.migration_dsn`: privileged schema migration connection; falls back to `dsn` when omitted.

Never log DSNs, API keys, bearer tokens, grant digests, or other secrets.

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

## Conversational Project Agent

The Web `/agent` experience is a read-only server feature. It is not the MCP
`agent` profile and it does not grant an identity permission to mutate Cortex.
Access is capability based: project discovery requires authorized search, each
memory result is read-authorized, and code evidence requires
`ResourceCode/ActionRead` for the same verified tenant, workspace, and project.
The browser can select only project identifiers returned by
`GET /api/agent/projects`; an arbitrary or stale identifier is rejected without
revealing whether the project exists.

```text
Web /agent
  -> authenticated JSON or SSE adapter
  -> principal-derived tenant/workspace/project scope
  -> one transport-neutral internal/domain/agent service
       -> authorized hybrid memory retrieval
       -> authorized scoped AST metadata retrieval
       -> Adaptive-RAG tier selection
       -> RRF + ColBERT MaxSim fusion and reranking
       -> HippoRAG graph expansion / LightRAG community summaries
       -> one bounded CRAG refinement when confidence is low
       -> fixed prompt policy and bounded untrusted history/evidence
       -> administrator-configured hardened LLM provider
       -> server validation of citation handles
       -> canonical answer + confidence + degradation + authorized sources
```

Retrieval is adaptive rather than a single vector lookup. Direct factual
questions can remain on the cheapest lexical/code path; semantic questions add
dense retrieval and reciprocal-rank fusion; relationship questions add bounded
Personalized PageRank over the memory/code graph; architectural questions can
add cached community summaries. ColBERT-style late interaction reranks candidate
evidence, and CRAG may perform at most one deterministic local reformulation.
Every stage receives the same server-resolved scope and evidence budget. A
degraded or unavailable stage may reduce confidence, but it can never broaden
tenant, workspace, project, classification, or personal-ownership visibility.

The public retrieval trace is a content-free projection: canonical tier,
unique ordered stage names, status and bounded counts, at most one refinement,
and allowlisted degradation codes. Queries, evidence, graph node identifiers,
principal data, checksums, prompts, and provider details are not trace fields.
JSON and the terminal SSE object serialize the same canonical answer.

Both `POST /api/agent/answer` and `POST /api/agent/stream` invoke the same
service and return the same canonical answer semantics. The SSE adapter emits
`meta`, `delta`, zero or more `citation`, then `done`; failures after streaming
starts use a sanitized `error` event. Request cancellation and transport-owned
deadlines propagate into retrieval and the provider.

Conversation state is deliberately ephemeral. The Web retains at most six
user/assistant turn pairs in React memory and clears them on project change,
logout, or a new conversation. It does not persist transcripts in browser
storage. History, questions, and retrieved text are untrusted prompt data;
none can select tools, a model, a provider URL, credentials, tenant, workspace,
or project scope. The completion port has no mutation or tool interface.

PostgreSQL migration 109 owns the server AST boundary. Its
`scoped_code_symbols`, `scoped_code_relations`, and `scoped_code_index_state`
tables use composite tenant/workspace/project identities and forced RLS. Legacy
project-only AST rows are never backfilled because their authority cannot be
reconstructed. Code evidence is limited to symbols, signatures, documentation
summaries, paths, positions, and relations; source-file bodies are outside the
MVP.

The server issues opaque citation handles per request and resolves only handles
present in the authorized evidence set. Provider-supplied paths, citations, or
destinations are not trusted. Quotas and deadlines are server-owned. The
current limiter is process-local, so horizontally scaled production requires a
shared admission layer. Agent audit types intentionally have no fields for
questions, history, answers, evidence, embeddings, secrets, or provider URLs;
production enablement requires a durable metadata-only sink and a fail-closed
pre-provider authorization record.

## Storage And Migrations

Local startup is driven by the embedded forward-only baseline
`migrations/v2/001_init.sql`:

1. `app.Open` probes an existing file read-only.
2. v1, Engram, foreign, corrupt, partial, and checksum-mismatched databases are refused without mutation.
3. The v2 baseline is applied atomically and records its SHA-256 identity in `cortex_meta`.
4. Existing v2 databases must retain the same baseline checksum.

The root `migrations/001-014` files are retired v1 history. They do not drive
local startup and must not be edited as a way to change the v2 schema. PostgreSQL
uses the separate embedded migration line from `migrations/v2/100_server.sql`
through `migrations/v2/109_scoped_code_index.sql` and its server ledger.

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

Server composition wraps every external `VectorIndex` in an immutable cell
boundary. The wrapper overwrites caller metadata on writes and caller filters
on reads with the configured tenant and workspace, so adapters remain reusable
for migration while the production runtime is fail-closed. PostgreSQL lexical
retrieval remains available whenever scoped vector coverage is absent or
unhealthy. Deployment therefore proceeds runtime/schema first, non-destructive
reindex per project second, coverage and sibling-workspace canary verification
third. The production caller is the synchronous
`cortex --mode server reindex --project-id <public UUID>` command. It authenticates
the configured administrative bearer, derives tenant/workspace from verified
authority, resolves the durable project identity in PostgreSQL, and records a
metadata-only start plus one terminal outcome. No HTTP reindex endpoint exists.

The server embedding client is distinct from the permissive local constructor.
It derives an exact destination allowlist from administrator configuration and
enforces scheme, host, port, resolved-IP, redirect, response-size, timeout and
concurrency limits before hybrid search or an AI administration probe can make
an outbound request. Invalid destinations fail server startup.

## Configuration

Configuration is YAML plus `CORTEX_*` environment overrides. Local defaults use
`~/.cortex/cortex.db`. Server storage has separate fields for:

- `server.storage.dsn`: non-superuser runtime connection.
- `server.storage.migration_dsn`: privileged schema migration connection, required outside explicit development bootstrap.

Before opening PostgreSQL, server composition parses both DSNs and requires distinct role names. The DSNs may address the same database. Setting `server.bootstrap_development: true` is the only supported way to omit `migration_dsn`; this development-only mode reuses the runtime DSN. Configuration loading never synthesizes the fallback.

Never log DSNs, API keys, bearer tokens, grant digests, or other secrets.

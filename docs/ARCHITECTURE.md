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
  -> static bearer verification (constant-time compare to http.token)
  -> synthetic constant principal (configured tenant, workspace, subject, owner role)
  -> authz.AuthorizedContext
  -> postgres.AuthorizedStore operations
  -> PostgreSQL transaction bound to the constant tenant with RLS
```

Server transports receive operation capabilities only. They must not receive raw
PostgreSQL repositories, transactions, scoring primitives, or client-selected
tenant authority.

The server is a single-tenant self-hosted deployment. Exactly one synthetic
constant principal is assembled at composition from `server.principal_subject`,
`server.tenant_id`, and `server.workspace_id`; the configured `http.token` is the
single static bearer every caller must present, verified with a constant-time
comparison. Tenant and workspace are configuration constants and are never derived
from client input: the request header `X-Cortex-Workspace` cannot select a
workspace, and workspace selection collapses to the fixed configured default.
Request identity is bound to that constant principal, and `AuthorizedStore`
operations execute inside a PostgreSQL transaction bound to the constant tenant,
where row level security enforces isolation. PostgreSQL remains behind
`AuthorizedStore`; architecture tests reject raw accessors.

## Shared API Handlers (`internal/api`)

The five web-critical read endpoints (`GET /api/me`, `GET /api/stats`,
`GET /api/projects`, `GET /api/agent/projects`, `GET /api/graph/project-graph`) are
served by one transport-neutral handler package, `internal/api`, on both the local
and server muxes. It imports only `internal/domain` and the standard library, so
neither mux crosses the server-track architecture boundary by consuming it
(REQ-SH-010).

The handlers depend on a narrow five-method `Port`:

- `CurrentPrincipal` feeds the `/api/me` identity envelope.
- `ServerStats` feeds the `/api/stats` counters.
- `Projects` feeds the `/api/projects` identifier list.
- `AgentProjects` feeds the `/api/agent/projects` id/label corpus.
- `ProjectGraph` feeds the `/api/graph/project-graph` subgraph.

`New(port)` binds the handlers to one `Port`. Adapters translate their own error
vocabulary into the package's `ErrUnauthenticated` (401) and `ErrForbidden` (403)
sentinels, so the emitted envelopes stay identical on both muxes without
`internal/api` importing `internal/authz`.

Two adapters implement the Port:

- **Server — `serverAPIPort`** (`internal/platform/server/http.go`). It wraps the
  request-scoped `Operations`, which resolve the principal-scoped
  `AuthorizedStore`, and is constructed per request, so no operation is reachable
  without a verified principal. `translatePortError` maps authorization denials onto
  the api sentinels. Its `ProjectGraph` deliberately ignores the handler's
  `depth`/`max_nodes` bounds and reproduces the pre-extraction traversal (a 150-row
  project read plus one depth-1, 30-node subgraph lookup per observation) so
  responses stay byte-compatible (REQ-SH-011).
- **Local — `localParityPort`** (`internal/http`). A composite of `*localOpsPort`
  and `localProjectGraph`: the operations half derives principal, stats, projects,
  and agent projects from the SQLite `bundle.Stores`, and the graph half computes
  `ProjectGraph` as a bounded breadth-first walk over `graphstore` edges (depth
  default 2 within 1..10, `max_nodes` default 100 within 1..200, 150-row seed bound).
  The serve mux registers the five routes behind the existing token gate and hands
  `api.New(newLocalParityPort(deps))` this bundle-backed port. `CurrentPrincipal`
  returns a synthetic single-user local principal (`local-owner`, owner role,
  wildcard grants), so the web client observes a real identity instead of a null
  principal (REQ-SH-012).

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
local startup and must not be edited as a way to change the v2 schema.

The embedded SQL set is frozen. No change edits, moves, or deletes any file under
`migrations/v2/`, any ledger entry, any checksum pin, or the retired root history
(REQ-SH-020). The local line is the immutable `migrations/v2/001_init.sql` baseline
(whose identity is recorded in `cortex_meta`) plus its additive SQLite follow-ups.
The PostgreSQL line runs `migrations/v2/100_server.sql` through
`migrations/v2/112_static_bind_contract.sql`, recording every applied version and
its SHA-256 in the `cortex_server_migrations` ledger:

- Versions 100-105 are byte-identical forever; their checksums are pinned as
  historical literals so any drift is visible in unit tests on every platform.
- Versions 106-112 carry reviewed pins that move only with reviewed bytes until
  release.
- Version 111 (`ServerMultiTenantVerifierSQL`) stays embedded but is dead: after the
  retired multi-tenant token verifier was deleted, its only references are the embed
  declaration and the migration plumbing that registers the version, so it has no
  runtime caller. The
  live single-tenant verifier is version 110's `cortex_verify_token_principal_v2`.
- Version 112 (`static_bind_contract`) is the active head. It additively replaces
  `cortex_bind_principal` so the configuration-derived synthetic principal installs
  the same RLS tenant/actor context as a token-verified one: the migration-108
  `v1:<token>` provenance branch is preserved verbatim and a `static:<hmac>` branch
  keyed by the actor's persisted grant digest is added.
- The runtime head is 112. A ledger recording a version beyond the head fails closed
  (`ErrFutureMigration`), because such a database was written by a newer runtime.
  Physically dropping the dead 111 remains roadmap only.

The v2 baseline is forward-only. Do not document or implement destructive local
rollback as a normal upgrade path.

## MCP

The supported namespace is `cortex_*`. Legacy `mem_*` names and Engram framing
are intentionally rejected by tests.

Profiles are defined in `internal/mcp/server.go`. The supported local profiles are
`agent` (default, the canonical coding-agent suite), `dev` (the golden suite for
local software development), and `minimal` (the core memory tools for
ultra-low-token contexts):

- `agent` contains ordinary memory, graph, scoring, revision, and project tools.
- `dev` contains the golden software-development tool suite (`coder` is an accepted
  alias).
- `minimal` contains the five core memory tools only.
- `admin` (destructive deletion and curation) and `temporal` (temporal graph and
  observability) are deprecated non-agentic profiles: their keys remain for
  backward compatibility but they are retired from standard agent discovery.

MCP profiles are local-only. Local MCP uses stdio; server MCP uses Streamable HTTP
at `/mcp` and requires the server bearer token. The embedded web dashboard — served
by the local binary alongside the CLI and TUI (see [HTTP](#http)) — is a separate
local surface, not an MCP profile.

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
the configured administrative bearer, binds tenant/workspace from the synthetic
constant principal, resolves the durable project identity in PostgreSQL, and records a
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

Server identity is configuration-constant: `server.tenant_id`, `server.workspace_id`,
and `server.principal_subject` are required UUIDs that build the synthetic principal,
and `http.token` is the static bearer. `server.grant_digest` and `server.grant_version`
are deprecated compatibility fields; grant integrity is calculated by PostgreSQL in
`cortex_bootstrap_service_principal`. The removed `multi_tenant` key is unknown to the
schema and tolerated on load without branching.

Before opening PostgreSQL, server composition parses both DSNs and requires distinct role names. The DSNs may address the same database. Setting `server.bootstrap_development: true` is the only supported way to omit `migration_dsn`; this development-only mode reuses the runtime DSN. Configuration loading never synthesizes the fallback.

Never log DSNs, API keys, bearer tokens, grant digests, or other secrets.

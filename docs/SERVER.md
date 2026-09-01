# Server Deployment

Server mode is the PostgreSQL composition selected with `cortex --mode server`.

## Docker Deployment (GHCR Official Images)

Cortex publishes official multi-platform images on GitHub Container Registry:
- Backend Server: `ghcr.io/lleontor705/cortex:latest`
- Web UI Control Room: `ghcr.io/lleontor705/cortex-web:latest`

```bash
# Pull and start PostgreSQL, Cortex Server, and Web UI
docker compose up -d
```

The Compose file creates PostgreSQL, applies the embedded server schema, bootstraps a development organization/workspace/principal, and starts Cortex Server on port `7438` and Web UI on port `3000`. To rebuild locally from source code, use `docker compose up --build -d`.

## Production Requirements

- PostgreSQL 16 or newer supported by the deployment policy.
- Separate privileged migration DSN and non-privileged runtime DSN.
- Verified principal grants for tenant, workspace, projects, roles, scopes, and classification.
- A secret-managed bearer token or an upstream authenticated gateway.
- Explicit `http.allowed_origins` for browser clients.
- TLS at the deployment boundary.

Server persistence remains behind `AuthorizedStore`; transports never receive raw repositories, transactions, or client-selected tenant authority. `/health` is public, while `/api/*` and `/mcp` require bearer authentication.

### PostgreSQL role boundary

Production-like server startup requires both connection strings and validates their parsed PostgreSQL role names before opening a database handle:

- `server.storage.dsn` / `CORTEX_SERVER_STORAGE_DSN` uses the long-lived, non-superuser `cortex_app` role with no `BYPASSRLS`.
- `server.storage.migration_dsn` / `CORTEX_SERVER_STORAGE_MIGRATION_DSN` uses the privileged `cortex_migration` role for migrations and startup bootstrap reconciliation.
- The two roles must differ. They may target the same PostgreSQL host and database.

DSNs and passwords are never included in boundary-validation errors. The runtime closes the migration handle before creating its long-lived application pool. Moving migrations and bootstrap reconciliation into a standalone one-shot job remains a required follow-up.

Only local development may set `server.bootstrap_development: true` and omit `migration_dsn`; that explicit switch permits Cortex to reuse the runtime DSN for migration/bootstrap convenience. Never enable this fallback in production.

## Conversational Agent Configuration

The conversational agent uses administrator-owned server LLM configuration;
clients cannot send a provider, model, URL, API key, tool list, or output limit.
At minimum configure a supported provider and model, plus a secret-managed API
key when the provider requires one:

```text
CORTEX_LLM_PROVIDER=openai
CORTEX_LLM_MODEL=gpt-4o-mini
CORTEX_LLM_API_KEY=<from-secret-manager>
```

`CORTEX_LLM_BASE_URL` is optional for provider presets. Custom destinations
must be absolute HTTPS URLs without embedded credentials. The hardened outbound
policy also supports `CORTEX_LLM_ALLOWED_HOSTS`, `CORTEX_LLM_ALLOWED_PORTS`,
`CORTEX_LLM_MAX_CONCURRENT`, `CORTEX_LLM_MAX_REDIRECTS`,
`CORTEX_LLM_MAX_RESPONSE_BODY_BYTES`, `CORTEX_LLM_MAX_ERROR_BODY_BYTES`,
`CORTEX_LLM_TIMEOUT`, and `CORTEX_LLM_CA_FILE`. The loopback switches
`CORTEX_LLM_ALLOW_LOOPBACK` and `CORTEX_LLM_ALLOW_LOOPBACK_HTTP` are for explicit
local development only. Invalid destination policy fails server startup; an
unconfigured provider leaves the agent endpoints fail-closed with a sanitized
unavailable response.

### Authenticated API

- `GET /api/agent/projects` returns only projects for which the verified
  principal has searchable memory or a ready, readable code index.
- `POST /api/agent/answer` returns one canonical JSON answer.
- `POST /api/agent/stream` returns SSE events and disables proxy buffering.

All three endpoints require server bearer authentication and set
`Cache-Control: no-store`. A request body has this closed shape; unknown fields
are rejected:

```json
{
  "project_id": "server-issued-project-id",
  "question": "¿Qué se desarrolló para aislar los vectores?",
  "history": [
    {"role": "user", "content": "Resume el modo server"},
    {"role": "assistant", "content": "..."}
  ]
}
```

The JSON response contains `answer`, validated `sources`, `confidence`, and a
content-free `retrieval` trace: `tier`, unique ordered `stages` with
status/count, optional `refinement_count`, allowlisted `degraded` codes, and
invalid-citation count. A trusted corpus generation may be added when the
retrieval port carries one; clients must not synthesize it from request data or
internal checksums. Streaming emits `meta`, `delta`, zero or more `citation`,
and a terminal `done` carrying the same canonical object; a failure after the
stream starts terminates it with a sanitized `error` event. Reverse proxies
must preserve client disconnects, use a timeout greater than 60 seconds, and
disable response buffering for `/api/agent/stream`.

Requests are bounded to an 8 KiB question, at most 12 history messages (six
turn pairs), 4 KiB per message, and 24 KiB aggregate history. The standard tier
currently allows 30 requests and 60,000 reserved/reconciled output tokens per
minute, two concurrent requests per tenant, a 1,200-token default, a 4,096-token
hard cap, a 30-second JSON deadline, and a 60-second SSE deadline. Exhaustion
returns `429 quota_exceeded` with bounded `Retry-After`; timeouts return
`504 agent_timeout`. These counters are process-local, so multiple replicas
must sit behind a shared limiter before general production enablement.

Agent audit records must contain only correlation and actor identifiers,
tenant/workspace/project, transport, result class, duration, token/source
counts, confidence, and degradation flags. They must never contain questions,
history, answers, retrieved memory/code, embeddings, secrets, or provider URLs.
The pre-provider authorization record is mandatory and fail-closed
(`503 audit_unavailable`); outcome delivery is best effort with content-free
failure telemetry. Treat a deployment without a durable wired agent audit sink
as not ready for production.

### MVP limits

- Read-only answers: no tools, writes, administrative actions, or repository
  commands.
- Code context is AST metadata (symbols, signatures, summaries, paths,
  positions, and relations), not complete source-file bodies.
- Browser conversation history is memory-only and is cleared on logout,
  project change, unmount, or New conversation.
- Missing vector or code evidence may produce a degraded answer from other
  authorized evidence. No authorized evidence returns a sanitized unavailable
  result without broadening scope.
- Adaptive retrieval uses four bounded tiers: direct factual, semantic hybrid,
  multi-hop graph, and architectural global. Dense retrieval is fused with
  lexical results through RRF and reranked with MaxSim; graph tiers use bounded
  HippoRAG-style PPR and LightRAG community summaries. CRAG can reformulate
  locally at most once. A second low-confidence result abstains with
  `crag_insufficient_confidence`.
- Community-summary caches are scoped by tenant, workspace and project identity,
  fingerprint both memory and AST inputs, and have a bounded process-local
  cardinality. Cancellation and unavailable/truncated graphs fail closed
  instead of serving stale summaries.

## Rollout Order And Stage Gates

The server schema is embedded in the binary (`migrations/v2/100_server.sql` …
`109_scoped_code_index.sql`). Rollout always advances in this exact order; each
stage has explicit advance, abort, and retry criteria, and no stage starts
before the previous one advances.

### Stage 1 — Migrations (database)

1. Provision PostgreSQL 16 and bootstrap the non-superuser application and authorization roles with `scripts/postgres/bootstrap-authz.sql` through a privileged DSN.
2. Configure the runtime and migration DSNs simultaneously, using distinct roles, then start the new runtime. Each migration applies in strict version order inside one transaction guarded by the `pg_advisory_xact_lock(hashtext('cortex:v2:server-migrations'))` lock and is recorded in the `cortex_server_migrations` ledger with its SHA-256 checksum.

- **Advance when** the ledger records every expected version (100…109) with matching checksums, migration 109 post-apply verification confirms scoped tables/RLS/grants, and startup completes without `ErrFutureMigration`.
- **Abort when** any preflight fails (orphan rows, unledgered artifacts, checksum mismatch, future ledger version): the transaction rolls back, the ledger keeps no partial entry, and the runtime refuses to serve.
- **Retry** by restarting the same binary: applied migrations are checksum-verified and skipped, so a retry is always idempotent. Never hand-edit schema or ledger rows to "get past" an abort.

### Stage 2 — Server (traffic)

1. Confirm the long-lived pool uses the non-privileged runtime DSN; the startup migration handle uses the separately configured migration DSN and is closed before traffic is served.
2. Put the deployment behind TLS at the boundary and wire the secret-managed bearer token or authenticated gateway.

- **Advance when** `/health` answers 200 and an authenticated `/api/me` round-trip returns the expected verified principal.
- **Abort when** startup validation or authentication smoke checks fail: the process exits fail-closed without serving; the database is never mutated at this stage.
- **Retry** by restarting after fixing configuration; re-entering Stage 1 is unnecessary because migrations are already ledgered.

### Stage 3 — Proxy/local clients

1. Point local SQLite clients (`cortex serve`, MCP profiles `agent`/`admin`/`temporal`) and any HTTP proxies at the deployment.
2. Local HTTP still refuses non-loopback binding without `http.token`, and `app.Open` refuses v1/Engram/foreign databases without mutation.

- **Advance when** the local unit/build gates (`go build ./...`, `go test -v -count=1 ./...`) and the focused portability gates pass on the client release.
- **Abort when** the read-only compatibility probe refuses a local database: keep the old local database untouched and investigate; the probe never mutates.
- **Retry** freely; all client checks are read-only.

### Stage 4 — Plugins

1. Distribute the OpenCode plugin (`cortex setup opencode`) and the Claude Code hooks package.
2. Both plugins talk to the server through the same authenticated HTTP surface as any other client.

- **Advance when** `plugin/opencode` passes `npm ci && npm test` (Vitest) and the Claude harness `bash plugin/claude-code/scripts/hooks_test.sh` exits 0.
- **Abort when** the harness exits 127 (missing bash/jq/python3/timeout): that is a BLOCKED toolchain, not a plugin defect — fix the toolchain and rerun. Plugin HTTP failures are classified, never silently dropped.
- **Retry** by rerunning the harness, which is fully offline/deterministic. Live hook delivery is NOT idempotent by itself: a retried `subagent-stop` capture that POSTs to `/api/observations` can create a duplicate observation, because retries do not deduplicate. Treat a retry as safe only after the prior attempt's failure classification confirms the request never reached the server, or after checking the evidence (for example, searching for the observation before re-sending). When the idempotent handoff receipt path is available and configured, prefer it for retries — handoff writes are deduplicated server-side by receipt identity.

### Commit evidence per stage

Every stage rollout records durable evidence before the next stage starts: the CI workflow run URL (the `ubuntu-latest` executor is authoritative for PostgreSQL/race/plugin gates), the coverage threshold result (≥ 70%), the migration ledger head reached, and a Cortex observation or ForgeSpec task note linking the artifacts. Release additionally requires the manual approval gate to have observed all twelve stage-1 gates green on the exact commit being shipped.

### Workspace-scoped vector rollout

Vector replicas are optional and PostgreSQL lexical search remains authoritative. Roll out vector isolation in this order:

1. Deploy the runtime and adapter schema/filter support first. Confirm every server vector operation is wrapped with the configured tenant and workspace boundary and that project-selected searches require the server-resolved public `project_id` UUID.
2. Reindex one project at a time, stamping `tenant_id`, `workspace_id`, and `project_id` on every point; the project label is display metadata only. Do not rewrite or delete legacy vectors during rollout: points missing `project_id` cannot satisfy a project-selected query and remain fail-closed until reindexed.
3. Verify coverage for that exact project UUID: indexed observations must match the authoritative PostgreSQL count, and same-label sibling projects plus sibling workspaces must contribute zero candidates.
4. Only then enable or advertise semantic recall for that workspace. Missing metadata, incomplete coverage, unhealthy adapters, or embedding failures keep `/api/search/hybrid` on authorized lexical results.

Rollback disables the vector provider or redeploys the previous runtime while retaining the authorization, CORS and DSN boundaries. It never removes PostgreSQL data or weakens tenant/workspace filtering.

Run the scoped vector replica reindex synchronously from the server composition:

```bash
cortex --mode server reindex --project-id <public-project-uuid> --config /path/to/server.yaml
```

The command verifies the configured `http.token` through the durable token
repository, requires audited `ResourceAdmin/ActionManage`, and resolves the
project UUID plus canonical label inside the principal-bound PostgreSQL
tenant/workspace. There are deliberately no tenant, workspace, internal ID or
label override flags. It refuses absent or unhealthy embedding/vector
providers, an empty corpus, skipped observations, or coverage where upserted
points do not equal the authoritative corpus. Audit rows contain only project
identity, counts, status and duration; cancellation stops provider/vector work
but still attempts one bounded terminal audit. The command is idempotent
because the external replica upserts by observation ID. It does not expose a
long-running HTTP endpoint and does not modify or delete legacy points.

### Conversational agent rollout

Migration 109 is additive and forward-only. It creates the scoped AST tables and
revokes `cortex_app` access to legacy unscoped `code_symbols` and
`code_relations`; it intentionally copies no legacy rows. Roll out the agent in
these waves:

1. Apply 109 with `cortex_migration`. Verify the ledger checksum, composite
   foreign keys, forced RLS, and that `cortex_app` cannot read legacy AST.
2. Deploy the 109-aware runtime while keeping agent traffic disabled at the
   ingress or tenant-release layer. Confirm `/health`, `/api/me`, and the normal
   search surfaces remain healthy.
3. Run the authenticated administrative AST reindex for one canary project.
   Tenant and workspace must come from trusted server composition and the
   project from durable grants; never accept them as an operator-provided SQL
   mapping. The job must be idempotent and record only counts/checksum/status.
4. Confirm `scoped_code_index_state` reaches `ready`, counts match the approved
   checkout, and sibling project/workspace/tenant isolation probes return zero
   rows. States `missing`, `indexing`, or `failed` are not ready.
5. Enable JSON for the canary, then SSE. Verify citation handles, cancellation,
   timeouts, quota rejection, content-free audit delivery, and proxy streaming.
6. Reindex and enable remaining projects/tenants only after each project has
   equivalent readiness evidence.

Until a project is `ready`, legacy AST is excluded. The agent may answer from
authorized memory and report code degradation; when no authorized corpus is
available it must fail without calling the provider. The current repository
does not provide a production administrative caller for the scoped AST reindex,
so this remains a deployment blocker rather than permission to use direct SQL.

Rollback disables agent routes at ingress/tenant release and scoped AST
ingestion, then redeploys the previous compatible runtime if needed. Preserve
migration 109 and indexed data. Do not restore runtime access to legacy AST,
drop scoped tables, rewrite the ledger, weaken RLS/CORS/SSRF controls, or delete
PostgreSQL/vector data.

Migration `105_workspace_binding.sql` is additive: it adds `workspace_id` to observations and handoff receipts, backfills strictly from the durable `session -> observation -> receipt` chain, and aborts the whole transaction (no partial ledger entry) on any orphan or unresolved row. Like 104, it omits `IF NOT EXISTS` on purpose so stale, unledgered artifacts fail closed instead of being silently adopted.

### Rollback Policy

The migration line is forward-only; there is no `Down`.

- Rollback means redeploying the previous runtime binary. Migration 105 is additive, so a 104-era runtime keeps working: it keeps writing observations through the compatibility `BEFORE` trigger, while durable pending receipts intentionally fail closed rather than accept an insecure tenant-wide workspace default.
- Never "undo" a migration with `DROP`/`DELETE` against schema objects or ledger rows; that forks the migration line and makes the database unverifiable.
- Never rewrite a checksum in `cortex_server_migrations`. A checksum mismatch fails closed, and a ledger recording a version beyond the runtime's head (`ErrFutureMigration`, REM-ROLLOUT-001) means a newer runtime owns the line: this runtime refuses to read or write any migration row before operating.

## Local vs CI Gates

PostgreSQL-dependent gates (integration, e2e, scoped AST isolation, and the
global coverage threshold of at least 70%) fail — they do not skip — without
`CORTEX_TEST_POSTGRES_DSN`, `CORTEX_TEST_POSTGRES_MIGRATION_DSN`, and
`CORTEX_TEST_POSTGRES_AUTHZ_ADMIN_DSN`. Run the agent isolation gate with:

```bash
go test -v -count=1 -tags postgres_integration ./internal/migration ./internal/store/postgres ./internal/platform/server
```

The race gate requires CGO with a working gcc, and the Claude plugin harness
requires jq; on typical Windows workstations these report BLOCKED locally.
`.github/workflows/ci.yml` (ubuntu-latest with a PostgreSQL 16 service) is the
authoritative executor for them, and the release workflow re-runs the same suite
before its manual approval gate. The Web gate is `npm test && npm run build`
inside `web/`.

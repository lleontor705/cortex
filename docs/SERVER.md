# Server Deployment

Server mode is the PostgreSQL composition selected with `cortex --mode server`.

## Docker Development

```bash
docker compose up --build
```

The development Compose file creates PostgreSQL, applies the embedded server schema, bootstraps a development organization/workspace/principal, and starts Cortex on port `7438`. It is not a production secret or identity configuration.

## Production Requirements

- PostgreSQL 16 or newer supported by the deployment policy.
- Separate privileged migration DSN and non-privileged runtime DSN.
- Verified principal grants for tenant, workspace, projects, roles, scopes, and classification.
- A secret-managed bearer token or an upstream authenticated gateway.
- Explicit `http.allowed_origins` for browser clients.
- TLS at the deployment boundary.

Server persistence remains behind `AuthorizedStore`; transports never receive raw repositories, transactions, or client-selected tenant authority. `/health` is public, while `/api/*` and `/mcp` require bearer authentication.

## Rollout Order And Stage Gates

The server schema is embedded in the binary (`migrations/v2/100_server.sql` … `105_workspace_binding.sql`). Rollout always advances in this exact order; each stage has explicit advance, abort, and retry criteria, and no stage starts before the previous one advances.

### Stage 1 — Migrations (database)

1. Provision PostgreSQL 16 and bootstrap the non-superuser application and authorization roles with `scripts/postgres/bootstrap-authz.sql` through a privileged DSN.
2. Start the new runtime once with the privileged migration DSN. Each migration applies in strict version order inside one transaction guarded by the `pg_advisory_xact_lock(hashtext('cortex:v2:server-migrations'))` lock and is recorded in the `cortex_server_migrations` ledger with its SHA-256 checksum.

- **Advance when** the ledger records every expected version (100…105) with matching checksums and startup completes without `ErrFutureMigration`.
- **Abort when** any preflight fails (orphan rows, unledgered artifacts, checksum mismatch, future ledger version): the transaction rolls back, the ledger keeps no partial entry, and the runtime refuses to serve.
- **Retry** by restarting the same binary: applied migrations are checksum-verified and skipped, so a retry is always idempotent. Never hand-edit schema or ledger rows to "get past" an abort.

### Stage 2 — Server (traffic)

1. Switch the runtime to the non-privileged runtime DSN; the runtime DSN never owns migration authority.
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

Migration `105_workspace_binding.sql` is additive: it adds `workspace_id` to observations and handoff receipts, backfills strictly from the durable `session -> observation -> receipt` chain, and aborts the whole transaction (no partial ledger entry) on any orphan or unresolved row. Like 104, it omits `IF NOT EXISTS` on purpose so stale, unledgered artifacts fail closed instead of being silently adopted.

### Rollback Policy

The migration line is forward-only; there is no `Down`.

- Rollback means redeploying the previous runtime binary. Migration 105 is additive, so a 104-era runtime keeps working: it keeps writing observations through the compatibility `BEFORE` trigger, while durable pending receipts intentionally fail closed rather than accept an insecure tenant-wide workspace default.
- Never "undo" a migration with `DROP`/`DELETE` against schema objects or ledger rows; that forks the migration line and makes the database unverifiable.
- Never rewrite a checksum in `cortex_server_migrations`. A checksum mismatch fails closed, and a ledger recording a version beyond the runtime's head (`ErrFutureMigration`, REM-ROLLOUT-001) means a newer runtime owns the line: this runtime refuses to read or write any migration row before operating.

## Local vs CI Gates

PostgreSQL-dependent gates (integration, e2e, and the global coverage threshold of at least 70%) fail — they do not skip — without the three `CORTEX_TEST_POSTGRES_*` DSNs. The race gate requires CGO with a working gcc, and the Claude plugin harness requires jq; on typical Windows workstations these report BLOCKED locally. `.github/workflows/ci.yml` (ubuntu-latest with a PostgreSQL 16 service) is the authoritative executor for them, and the release workflow re-runs the same suite before its manual approval gate.

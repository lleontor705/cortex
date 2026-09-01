# Migration and Rollout

## Forward-Only Migration 109

Migration 109 MUST be additive and ledgered; migrations 100–108 remain byte-identical. It creates scoped AST symbol/relation storage with `tenant_id`, `workspace_id`, `project`, composite identities/foreign keys, indexes and forced RLS based on bound principal scope. Runtime DDL is removed and `cortex_app` receives no access to legacy unscoped tables.

SQL MUST preflight ownership, expected legacy shape and absence of conflicting 109 objects before mutation. Any drift or recorded checksum mismatch aborts atomically. The application refuses code retrieval if migration 109 is absent or its post-apply verification fails.

## Backfill

Legacy AST rows cannot be safely mapped from `project` alone. They MUST NOT be SQL-backfilled or assigned from request input. After schema deployment, an authenticated administrative reindex job derives tenant/workspace from trusted composition and project from durable grants, parses approved project checkouts, and writes scoped metadata idempotently. It records counts/checksums only, not source content.

Per project state is `missing|indexing|ready|failed`. Until `ready`, conversational retrieval excludes legacy rows, continues with authorized memory when available and reports `code_index_unavailable`; if no authorized corpus remains, it returns `503 retrieval_unavailable` without calling the LLM.

## Deployment Waves

1. Apply 109 with migration role; verify schema, RLS, grants and ledger.
2. Deploy server capable of scoped AST reads/writes, agent endpoints disabled by default.
3. Reindex one canary tenant/workspace/project; verify counts and isolation probes.
4. Enable JSON for canary, then SSE; observe quota, timeout, cancellation and citation metrics.
5. Reindex remaining projects and enable per tenant only after readiness evidence.
6. Retain legacy tables read-denied for a defined recovery window; archive/drop only in a separate approved migration.

## Scoped Vector Replica Command

Operators rebuild the optional vector replica one project at a time with
`cortex --mode server reindex --project-id <public UUID>`. The configured
administrative bearer is verified before the project is resolved. Tenant,
workspace, actor, internal project ID and canonical label come only from
durable authority and PostgreSQL; none is accepted as an operator mapping.
The synchronous command records metadata-only start and terminal audit events
and fails unless every authoritative observation is upserted. Missing
providers, an empty corpus, skipped embeddings, incomplete coverage or a
canceled run never produce a success receipt. This command rebuilds memory
vectors; the scoped AST ingestion/reindex described above remains a separate
approved-checkout workflow.

## Rollback

Disable the agent feature and scoped AST ingestion; preserve migration 109 and indexed data. Existing Web/search/MCP/TUI remain operational. Rollback MUST NOT restore runtime access to legacy AST, weaken RLS/CORS/SSRF, or rewrite the migration ledger.

## Archive Criteria

Archive this change only when every DAG task is complete; default Go and Web gates pass; tagged PostgreSQL isolation tests pass in CI; JSON/SSE parity and zero-write oracles pass; migration 109 is ledgered and post-apply verified; at least one canary project is reindexed with recorded readiness; and the operations/security runbooks describe enablement, monitoring and rollback. Lack of live PostgreSQL evidence is `BLOCKED`, never silently skipped.

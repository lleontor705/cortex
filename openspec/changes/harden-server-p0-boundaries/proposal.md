# Proposal: Harden Server P0 Boundaries

## Why

Server mode already derives tenant/workspace from authenticated principals and uses authorized PostgreSQL stores, but four P0 gaps remain: `/api/admin/ai/*` performs no explicit management authorization; its probes construct ad-hoc outbound clients; server CORS treats an empty list or `*` as allow-all; migration credentials may silently equal runtime credentials; and vector retrieval filters tenant but not workspace.

## What Changes

- Require `ResourceAdmin` + `ActionManage` before every AI administration status or probe operation.
- Inject narrow AI probes composed from the same hardened LLM and embedding dependencies used by production; handlers MUST NOT read provider credentials or create outbound clients.
- Make server CORS an exact-origin allowlist. Empty means no cross-origin access; wildcard configuration is rejected.
- Permit `migration_dsn` fallback to the runtime DSN only when `server.bootstrap_development=true`. Non-development startup requires distinct PostgreSQL role names.
- Stamp and filter `tenant_id` plus `workspace_id` for server vectors in pgvector, Qdrant and reindex paths. Legacy vectors without both fields are excluded and search degrades to lexical results.

## Success Criteria

- Member/viewer tokens receive deterministic `403 forbidden` and cannot execute any AI probe.
- Outbound AI tests use only preconfigured production transports/policies.
- Disallowed origins never receive CORS approval; invalid wildcard configuration prevents server startup.
- Production-like startup fails before migration when DSNs are missing or resolve to the same database role.
- Same-tenant sibling-workspace vectors cannot affect candidates or ranking.

## Compatibility and Rollout

Deploy authorization, CORS, DSN, schema/filter support and lexical fallback first. Then reindex each tenant/workspace and measure scoped coverage before treating vector recall as available. Existing clients retain REST response shapes; browser deployments must configure explicit origins. Development bootstrap keeps its documented single-DSN convenience.

## Non-Goals

- A standalone migration job/init container (mandatory follow-up).
- Shared-runtime multi-tenant redesign.
- Rate limiting, immutable audit chain, SIEM/logging and listener separation (P1).
- Editing immutable historical SQL migrations.


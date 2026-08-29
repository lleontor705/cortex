# Planning Join

## Grounded Evidence

- `internal/platform/server/http.go` is the authenticated REST/MCP boundary and already composes hardened AI probes and hybrid retrieval dependencies.
- `internal/store/postgres/authorized_operations.go` exposes code operations without authorization today.
- `internal/store/postgres/code_store.go` creates global `code_symbols`/`code_relations` at runtime and scopes only by project.
- `internal/authz/policy.go` has no `ResourceCode`; project grants and capability scopes are otherwise reusable.
- `web/src/lib/api.ts`, `web/src/components/AppShell.tsx` and existing pages establish client, auth cancellation, navigation and CSS-variable theme conventions.
- P0 contracts in `openspec/changes/harden-server-p0-boundaries` remain prerequisites.

## Contract-to-Task Join

| Requirements | Tasks |
|---|---|
| REQ-CODE-001..002 | 1.1–1.3, 5.1 |
| REQ-AGENT-001..003 | 2.1–3.2, 5.2 |
| REQ-TRANSPORT-001..002 | 3.1–3.3, 5.2 |
| REQ-WEB-001..002 | 4.1–4.2, 5.3 |
| REQ-OPS-001..002 | 2.2, 3.1–3.3, 5.2 |

## Work-Control Status

`cortex-ia work list` failed with `attempt to write a readonly database (8)`. Therefore no Cortex-IA task IDs were materialized and no leases were taken. `tasks.md` is the authoritative dependency DAG for later orchestration. Local Cortex AST/search evidence was also unavailable because no `.bin/cortex.exe` exists; decomposition instead uses bounded source inspection and the repository architecture guide.

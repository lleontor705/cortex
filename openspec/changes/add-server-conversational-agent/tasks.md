# Tasks: Server Conversational Project Agent

Each task targets ≤350 changed lines and may write only the listed files.

## Phase 1 — Scoped code foundation

- [x] **1.1 Migration 109** — Add scoped AST tables, RLS/grants, embed/register/checksum/post-apply tests. **Files:** `migrations/v2/109_scoped_code_index.sql`, `migrations/v2/embed.go`, `internal/migration/postgres.go`, `internal/migration/postgres_test.go`, `internal/migration/postgres_integration_test.go`. **Oracle:** `go test -count=1 ./internal/migration`; tagged PostgreSQL gate. **Depends:** none.
- [x] **1.2 Code capability** — Add `ResourceCode`, role/capability matrix and negative grant tests. **Files:** `internal/authz/policy.go`, `policy_test.go`, `bola_test.go`. **Oracle:** `go test -count=1 ./internal/authz`. **Depends:** none.
- [x] **1.3 Scoped AST store/reindex** — Replace runtime DDL, bind trusted scope, authorize reads/writes, track readiness and reindex idempotently. **Files:** `internal/store/postgres/code_store.go`, `internal/store/postgres/code_store_test.go`, `internal/store/postgres/authorized_operations.go`, `internal/store/postgres/authorized_operations_test.go`, `internal/server/external/code_reindex.go`, `internal/server/external/code_reindex_test.go`. **Oracle:** `go test -count=1 ./internal/store/postgres ./internal/server/external`. **Depends:** 1.1, 1.2.

## Phase 2 — RAG domain

- [x] **2.1 Agent contracts/service** — Add read-only ports, validation, bounded context, citation resolution, confidence/degradation and prompt hierarchy. **Files:** `internal/domain/agent/*.go`. **Oracle:** `go test -count=1 ./internal/domain/agent`. **Depends:** 1.2.
- [x] **2.2 Quota/audit policy** — Add tier budgets, metadata-only audit and cancellation/timeout tests. **Files:** `internal/domain/agent/limits.go`, `internal/domain/agent/limits_test.go`, `internal/domain/agent/audit.go`, `internal/domain/agent/audit_test.go`, `internal/platform/server/agent_limits.go`, `internal/platform/server/agent_limits_test.go`. **Oracle:** `go test -count=1 ./internal/domain/agent ./internal/platform/server`. **Depends:** 2.1.

## Phase 3 — Server integration

- [x] **3.1 Authorized retrieval adapters** — Wire hybrid memory, scoped AST and hardened configured LLM. **Files:** `internal/platform/server/agent.go`, `internal/platform/server/agent_test.go`, `internal/platform/server/server.go`, `internal/platform/server/server_test.go`, `internal/platform/server/request_operations.go`, `internal/platform/server/request_operations_test.go`. **Oracle:** `go test -count=1 ./internal/platform/server`. **Depends:** 1.3, 2.2.
- [x] **3.2 JSON endpoint/projects** — Add `/api/agent/projects` and `/answer`, stable errors and no-store. **Files:** `internal/platform/server/http.go`, `internal/platform/server/http_test.go`, `internal/platform/server/agent_http.go`, `internal/platform/server/agent_http_test.go`. **Oracle:** `go test -count=1 ./internal/platform/server`. **Depends:** 3.1.
- [x] **3.3 SSE endpoint** — Add equivalent event stream, flush, disconnect cancellation and proxy-safe headers. **Files:** `internal/platform/server/agent_http.go`, `internal/platform/server/agent_http_test.go`. **Oracle:** `go test -count=1 ./internal/platform/server`. **Depends:** 3.2.
- [x] **3.4 Audited vector reindex command** — Add synchronous server-mode `reindex --project-id <public UUID>`, durable project resolution, scoped PostgreSQL source, metadata-only start/terminal audit and exact corpus coverage gate; no HTTP job endpoint or tenant/workspace override. **Files:** `cmd/cortex/main.go`, `cmd/cortex/main_test.go`, `internal/platform/server/reindex_command.go`, `internal/platform/server/reindex_command_test.go`, `internal/platform/server/server.go`, `internal/store/postgres/reindex_source.go`, `internal/store/postgres/code_store_integration_test.go`, `docs/SERVER.md`, `docs/ARCHITECTURE.md`. **Oracle:** `go test -count=1 ./cmd/cortex ./internal/platform/server ./internal/store/postgres ./internal/server/external`; tagged PostgreSQL source isolation test. **Depends:** 1.2, 3.1.

## Phase 4 — Web

- [x] **4.1 Client/state** — Add typed JSON/SSE client, six-turn ephemeral reducer, abort/logout clearing and tests. **Files:** `web/src/lib/api.ts`, `web/src/lib/api.test.ts`, `web/src/lib/agent-chat.ts`, `web/src/lib/agent-chat.test.ts`. **Oracle:** `npm test`. **Depends:** 3.3.
- [x] **4.2 Accessible `/agent` UI** — Add granted selector, chat, stop/retry/new, confidence/degraded and source cards; add navigation. **Files:** `web/src/app/agent/page.tsx`, `page.test.tsx`, `web/src/components/AppShell.tsx`. **Oracle:** `npm test && npm run build`. **Depends:** 4.1.

## Phase 5 — Gates and rollout

- [x] **5.1 Isolation integration** — Prove sibling tenant/workspace/project, legacy exclusion and RLS. **Files:** `internal/migration/postgres_integration_test.go`, `internal/store/postgres/code_store_integration_test.go`, `internal/platform/server/e2e_postgres_integration_test.go`. **Oracle:** `go test -v -count=1 -tags postgres_integration ./internal/migration ./internal/store/postgres ./internal/platform/server`. **Depends:** 1.3, 3.3.
- [x] **5.2 Security/contract gate** — Injection, forged citation, quota, timeout, cancellation, JSON/SSE parity and zero-write tests. **Files:** `internal/domain/agent/service_test.go`, `internal/platform/server/agent_http_test.go`, `internal/platform/server/agent_security_test.go`. **Oracle:** `go test -v -count=1 ./...`. **Depends:** 3.3.
- [x] **5.3 Web/docs gate** — Accessibility/state tests and operator runbook. **Files:** `web/src/app/agent/page.test.tsx`, `web/src/lib/agent-chat.test.ts`, `docs/SERVER.md`, `docs/ARCHITECTURE.md`. **Oracle:** `npm test && npm run build`; `git diff --check`. **Depends:** 4.2, 5.1, 5.2.

Parallel groups: `{1.1,1.2}` → `{1.3,2.1}` → `{2.2}` → `{3.1}` → `{3.2,3.4}` → `{3.3}` → `{4.1,5.1,5.2}` → `{4.2}` → `{5.3}`.

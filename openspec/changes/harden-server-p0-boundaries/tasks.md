# Tasks: Harden Server P0 Boundaries

All tasks target ≤350 changed lines. Only listed files are writable. Dependencies form the execution DAG.

## Phase 1 — Security foundations

- [ ] **1.1 Admin authorization port** — Add narrow `AuthorizeAdminManage`, request-context forwarding and audited store implementation. **Files:** `internal/platform/server/http.go`, `request_operations.go`, `request_operations_test.go`, `internal/store/postgres/authorized_operations.go`, `authorized_operations_test.go`. **Oracle:** `go test -count=1 ./internal/platform/server ./internal/store/postgres`. **Depends:** none.
- [ ] **1.2 Safe probe abstraction** — Define injected status/LLM/embedding probes backed by composed extraction/embedding dependencies; remove handler-side env/client use. **Files:** `internal/platform/server/http.go`, `server.go`, `http_test.go`, `server_test.go`. **Oracle:** `go test -count=1 ./internal/platform/server`. **Depends:** 1.1.
- [ ] **1.3 Exact server CORS** — Validate canonical origins, reject wildcard, make empty allowlist deny cross-origin, preserve auth. **Files:** `internal/platform/server/http.go`, `http_test.go`, `server.go`, `server_test.go`. **Oracle:** `go test -count=1 ./internal/platform/server`. **Depends:** none.
- [ ] **1.4 DSN role boundary** — Remove global fallback; allow it only for development bootstrap; parse and compare roles before connection. **Files:** `internal/config/config.go`, `config_test.go`, `internal/platform/server/server.go`, `server_test.go`. **Oracle:** `go test -count=1 ./internal/config ./internal/platform/server`. **Depends:** none.

## Phase 2 — Vector isolation

- [ ] **2.1 Scoped adapter contract** — Add pgvector workspace column/evolution/index/filter and Qdrant workspace filter coverage; legacy records must not match. **Files:** `internal/vector/pgvector/adapter.go`, `adapter_test.go`, `adapter_integration_test.go`, `internal/vector/qdrant/adapter.go`, `adapter_test.go`, `adapter_integration_test.go`, `internal/vector/conformance/conformance.go`. **Oracle:** `go test -count=1 ./internal/vector/...`. **Depends:** none.
- [ ] **2.2 Trusted reindex boundary** — Require tenant/workspace in external reindex options and stamp every point; reject missing scope. **Files:** `internal/server/external/reindex.go`, `reindex_test.go`. **Oracle:** `go test -count=1 ./internal/server/external`. **Depends:** 2.1.
- [ ] **2.3 Server query wiring** — Derive workspace from authenticated principal, require both vector filters, and degrade legacy/unhealthy vector paths to lexical. **Files:** `internal/platform/server/http.go`, `http_test.go`, `server.go`, `server_test.go`. **Oracle:** `go test -count=1 ./internal/platform/server`. **Depends:** 2.1, 2.2.

## Phase 3 — Verification and rollout

- [ ] **3.1 Cross-boundary regression gate** — Add non-admin probe, sibling-workspace, legacy-vector, CORS and DSN startup cases. **Files:** tests already listed above; `internal/platform/server/e2e_postgres_integration_test.go`. **Oracle:** `go test -v -count=1 ./...`; with DSNs, `go test -v -count=1 -tags postgres_integration ./internal/platform/server ./internal/store/postgres`. **Depends:** 1.2, 1.3, 1.4, 2.3.
- [ ] **3.2 Runbook and coverage gate** — Document explicit origins/roles, deploy-first/reindex-second sequence, per-workspace coverage verification, lexical fallback, rollback and standalone migration-job follow-up. **Files:** `docs/SERVER.md`, `docs/ARCHITECTURE.md`. **Oracle:** `go test -count=1 ./internal/config ./internal/platform/server ./internal/server/external ./internal/vector/...`. **Depends:** 3.1.
- [ ] **3.3 Production scoped-reindex caller** — Add an authenticated, audited server job/command that derives tenant/workspace from trusted composition, invokes `external.Reindex`, reports per-workspace coverage and resumes idempotently. Until this exists, production vector rollout remains blocked after deploy and before semantic enablement. **Depends:** 3.2.

Parallel groups: `{1.1,1.3,1.4,2.1}` → `{1.2,2.2}` → `{2.3}` → `{3.1}` → `{3.2}`.

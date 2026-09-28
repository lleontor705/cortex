# Design: Self-Hosted Pivot

**Change ID:** self-hosted-pivot | **Workflow:** sdd-full | **Spec plane:** hybrid

This design records the adjudicated decisions D1-D6 with their measured basis, the resulting component interfaces, the consequence decisions taken while refining them, and the dependency-safe task DAG.

## 1. Decisions and measured basis

### D1 — Keep `internal/authz`, supply a synthetic constant principal

Authentication in server mode becomes configuration-driven. A new `staticBearerVerifier` implements the existing `principalVerifier` interface (`internal/platform/server/authentication.go:14-16`):

```go
type staticBearerVerifier struct{ token string; principal domain.Principal }

func (v staticBearerVerifier) VerifyToken(_ context.Context, secret, _ string) (domain.Principal, error) {
    if subtle.ConstantTimeCompare([]byte(secret), []byte(v.token)) != 1 { // pattern from internal/http/server.go:990-1001
        return domain.Principal{}, errInvalidBearer
    }
    return v.principal, nil
}
```

The synthetic principal is assembled once at composition from configuration:

```go
domain.Principal{
    Subject: cfg.Server.PrincipalSubject, OrgID: cfg.Server.TenantID,
    Roles: []string{"owner"}, ProjectIDs: []string{"*"},
    ClassificationClearance: []string{"*"}, WorkspaceIDs: []string{cfg.Server.WorkspaceID},
    RateLimitTier: "standard", GrantDigest: cfg.Server.GrantDigest, GrantVersion: 1,
}
```

**Basis:** startup already validates the configured bearer with `validateBearerToken` (`server.go:602,668-680`) and composes the audit sink from `principal.Subject/GrantDigest/GrantVersion` (`server.go:146`); `bootstrapServicePrincipal` (`server.go:492`) keeps provisioning the DB service principal, its canonical grants, and RLS binding, so `AuthorizedStore`, `cortex_bind_principal`, RLS, audit, and rate tiers are untouched. Startup fails closed when `PrincipalSubject`, `TenantID`, or `WorkspaceID` is empty (new validation; today an empty tenant surfaces later as an opaque verify error).

### D2 — Migration path (b): code-only removal, embedded SQL frozen

| Artifact | Disposition |
|---|---|
| `cfg.Server.MultiTenant` branches (server.go:214-218, 277-279, 293-299, 305; http.go:796; agent_http.go:139) | DELETED |
| `MultiTenantTokenPrincipalVerifier` (token_repository.go:141-166) + its coverage case | DELETED |
| Migration 111 (`ServerMultiTenantVerifierSQL`, `migrations/v2/embed.go:183-188`) | KEPT embedded, now dead; documented (wave 3) |
| Migration 110 (`cortex_verify_token_principal_v2`) | KEPT — it is the single-tenant verifier |
| Migrations 100-105 (immutable), 106-109, 106/110 pins | KEPT, untouched |
| Compensating migration 112 (physical drop of 111) | ROADMAP only, not tasked |
| `TokenPrincipalVerifier` (single-tenant, token_repository.go:178+) | KEPT (e2e/bootstrap-capable path; D2 deletes only the multi-tenant verifier) |

**Landmine to document, not fix:** ledgered databases and the `ErrFutureMigration` class must be described in `internal/migration/postgres.go` and the migration runbook; no checksum, ledger, or embedded-SQL change is permitted.

### D3 — Neutral `internal/api` package

```go
// internal/api/port.go — the whole server/local surface this package needs
type Port interface {
    CurrentPrincipal(ctx context.Context) (Principal, error)   // synthetic principal (server) / local principal (local)
    ServerStats(ctx context.Context) (*domain.ServerStats, error)
    Projects(ctx context.Context) ([]string, error)
    AgentProjects(ctx context.Context) (map[string]string, error)
    ProjectGraph(ctx context.Context, projectID string, depth, maxNodes int) (*domain.GraphSubgraph, error)
}

type Handlers struct { port Port }
func New(port Port) *Handlers
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request)
func (h *Handlers) Stats(w http.ResponseWriter, r *http.Request)
func (h *Handlers) Projects(w http.ResponseWriter, r *http.Request)
func (h *Handlers) AgentProjects(w http.ResponseWriter, r *http.Request)
func (h *Handlers) ProjectGraph(w http.ResponseWriter, r *http.Request)
```

- **Server adapter** (`internal/platform/server`): thin struct binding the request `Operations` (`http.go:49-98`, implemented by `postgres.AuthorizedStore`) and `principalFromContext`; the existing `a.me`, `a.stats`, `a.projects`, `a.agentProjects`, `a.projectGraph` bodies become delegations so route patterns and JSON envelopes do not move.
- **Local adapter** (`internal/http`): bundle-backed. `ServerStats` is recomputed from `bundle.Observations`, `bundle.Sessions`, `bundle.Metrics` (no SQLite `GetServerStats` exists); `Projects`/`AgentProjects` are derived from sessions + observations + `bundle.Code`; `ProjectGraph` is a BFS over `bundle.Graph` edges returning `domain.GraphSubgraph`.
- **Neutrality rule:** `internal/api` imports only `internal/domain` (and stdlib). It must not import `internal/platform/server` or `internal/store/postgres`; `internal/http` must not import server packages (`internal/app/arch_test.go` invariant 1).

### D4 — Parity scope and response contracts

| Route | Server source | Local source | Status |
|---|---|---|---|
| `GET /api/me` | `http.go:437-458` (`principalResponse`) | synthetic local principal (owner, single-user local mode) | IN SCOPE |
| `GET /api/stats` | `http.go:621` via `GetServerStats` | recomputed from bundle Observations/Sessions/Metrics | IN SCOPE |
| `GET /api/projects` | `http.go:657` via `ListProjects` | derived from sessions+observations+code | IN SCOPE |
| `GET /api/agent/projects` | `agent_http.go` via `ListAgentProjects` | derived, capability+corpus filtered | IN SCOPE |
| `GET /api/graph/project-graph` | `http.go:1056` via `GetGraphSubgraph` | BFS over graphstore edges | IN SCOPE |
| `GET /api/audit`, `GET /api/system/metrics`, `GET /api/rag/stats` | server-only | no local store (audit), no metrics/rag surface | DEFERRED (roadmap, documented) |

The `principalResponse` mapping moves/copies into `internal/api` behind a golden JSON contract test so the web client sees identical keys for identical states.

### D5 — Test & CI matrix

- **Keep:** the 3-DSN trio + `scripts/postgres/bootstrap-authz.sql` + `bench/documentation_contract_test.go` harness pins (`postgres_integration` tag, fail-don't-skip, Makefile integration target) + the RLS-exploit probe suites: `internal/store/postgres/authz_negative_integration_test.go` (cross-tenant RLS denial) and `internal/store/postgres/workspace_security_integration_test.go` (`TestWorkspaceSecurityExploit*`).
- **Rework/delete:** cases exercising the removed application plane only (multi-tenant verifier, per-request workspace selection, per-request tenant derivation, multi-workspace product features) inside the tagged `postgres_integration` suites; classification rule and grep anchors are encoded in the wave-7 task objectives. SQL-level tenant/workspace isolation probes remain valid under constant-tenant RLS and are kept.
- **Pins move in this change:** if any DSN/role/build-tag text moves, `bench/documentation_contract_test.go` moves in the same task.
- **Coverage:** the active enforced threshold stays `>= 80.0` and the computed `-coverpkg` list keeps every cortex-owned package (REQ-QA-004); wave 7 ends with a re-check task.

### D6 — Docs and positioning

Retire `docs/server-saas.md` (replaced by a self-host deployment guide alongside `docs/SERVER.md`), rewrite the AGENTS.md server clauses (the `AuthorizedStore`/`AuthorizedContext` clause survives with a synthetic-principal note), sweep `README.md:123`, `docs/embedded-web.md:122,135`, capability/verification matrices, `CONFIGURATION`, `RAILWAY_DEPLOYMENT`, `SERVER`, `HTTP-API`, `ARCHITECTURE`, `INSTALLATION`, remove `mode.go:26,43` and `config.go:95-96` MultiTenant surface (+ `config_test.go`), fix `docs/capability-matrix.md:47` (`cortex_migration_ledger` misname), and edit the closed `coverage-push-90-addendum.md` LOCAL-MODE PARITY section minimally: its NON-GOAL is superseded by this change's `api-parity` delta (pointer only, old board stays closed).

## 2. Consequence decisions (refinements of locked decisions)

- **C1 — request-scoped vector wrapper removed.** After D2 deletes `server.go:214-218`, `external.NewRequestScopedVectorIndex` has no caller; `internal/server/external/scoped_vector.go` drops it and keeps `NewServerScopedVectorIndex` (single-tenant scope from config). No SQL impact.
- **C2 — `DenyWorkspace` / `DenyTenantMismatch` KEPT.** Recommendation adopted: both remain in `internal/authz/policy.go` (they are harmless with constant tenant/workspace, are pinned by `bola_test.go` and `policy_boundary_test.go`, and removing them would weaken the default-deny error contract for zero benefit). Only branches reachable exclusively through multi-tenant principals are de-fattened; documented in-code.
- **C3 — single-tenant `TokenPrincipalVerifier` retained** in `internal/store/postgres` (D2 deletes only the multi-tenant verifier); it stays the DB-side verification capability exercised by e2e/bootstrap tests.
- **C4 — bootstrap, RLS, audit, rate tiers unchanged**; only the source of the principal moves from a DB row to configuration.

## 3. Sequence flow (per request, after the change)

```
client -> mux -> (server) requestAuthenticator.middleware
             -> staticBearerVerifier.VerifyToken(constant-time compare vs cfg.HTTP.Token)
             -> synthetic constant principal -> workspaceSelector (fixed default)
             -> principalOperationsFactory -> AuthorizedStore(requestContext) -> handler
local client -> internal/http mux -> withAuth(token) -> internal/api.Handlers
             -> local bundle-backed Port -> recomputed/derived response
```

## 4. Task DAG (waves)

```
W1  sh-static-bearer-principal -> sh-multitenant-branch-removal -> {sh-saas-auth-tests-rework, sh-delete-multitenant-verifier}
W2  sh-saas-auth-tests-rework -> sh-workspace-fixed-default
    sh-authz-policy-defatten (parallel)   sh-request-vector-removal (after branch removal)
W3  sh-migration-111-roadmap-docs (after verifier deletion)      W4  sh-api-port-me -> {sh-api-stats-projects, sh-api-project-graph} -> sh-server-api-delegation
W5  sh-api-port-me -> {sh-local-parity-ops, sh-local-parity-graph} -> sh-local-parity-routes
W6  {sh-playwright-selector-settings, sh-playwright-graph-mount} (after W4+W5)
W7  sh-compose-e2e-rescope | sh-pg-suite-rework-a | sh-pg-suite-rework-b -> sh-doc-contract-coverage-gate
W8  {sh-selfhost-doc-replace, sh-readme-capability-sweep, sh-api-deploy-docs-sweep, sh-config-multitenant-field-removal, sh-addendum-parity-pointer} -> sh-matrices-config-arch-sweep
```

Parallelism: file-disjoint within every wave; the orchestrator dispatches at most 3 concurrent writers. Sequential same-file contacts (server.go, http.go, agent_http.go, platform/server test files) are covered by explicit dependency edges, never by concurrent claims.

## 5. Workload and quality budgets (flexible policy)

- Source logic <= 700 LOC (Go) per task; test/fixture <= 1200 LOC; new modular test files <= 250 LOC each; no new suites appended to test files exceeding 300 LOC.
- `allowed_files` 1-3 files per task, disjoint inside parallel waves.
- Fast-TDD-eligible tasks (verifier, parity adapters, extracted handlers, workspace/authz changes) carry the Mutation Evidence Gate of `cortex-work-protocol.md` §4/§8: a `SURVIVED` mutant blocks transition to `in_review`.
- Pure-test tasks (SaaS test rework, postgres suite rework, Playwright specs, documentation contract) carry the anti-decomposition rule: fix/simplify assertions or revert to the standard oracle; never split a test into more tests; defect-blocked routing applies only to production defects.

## 6. Verification strategy

| Layer | Gate |
|---|---|
| Unit | `go test -v -count=1 ./internal/platform/server ./internal/authz ./internal/server/external ./internal/http ./internal/api ./internal/config ./internal/app` |
| Store (tagged, local ceiling) | `go vet -tags postgres_integration ./internal/store/postgres` (DSN execution is CI-authoritative) |
| Migration | `go test -v -count=1 ./internal/migration` (embedded SQL pins) |
| Docs/contract | `go test -v -count=1 ./bench` (documentation contract + CI pins) |
| Compose | `go vet -tags docker_e2e ./e2e` locally; Docker smoke CI-authoritative |
| UI parity | `cd tools/e2e-web && npx playwright test` (Chromium; BLOCKED locally if unprovisioned) |
| Coverage | `go test -tags postgres_integration -covermode=atomic -coverpkg=<computed> -coverprofile=coverage.out ./...` with the `>= 80.0` check unchanged |
| Architecture | `internal/app/arch_test.go` + `internal/platform/server/arch_test.go` |

## 7. Trade-offs

- **Synthetic principal over DB-verified tokens (D1):** simplifies self-hosted operation (one configured bearer, no token bootstrap for the HTTP plane) at the cost of no per-request revocation list; revocation/rotation of the configured bearer remains the operator control. Accepted by adjudication.
- **Code-only migration path (b) over physical cleanup:** leaves migration 111 embedded forever (documented), but guarantees zero checksum/ledger risk against shipped databases. Accepted by adjudication.
- **Neutral package + adapters over duplicated handlers:** one JSON contract, two compositions; costs an adapter layer but preserves the architecture gate instead of weakening it.

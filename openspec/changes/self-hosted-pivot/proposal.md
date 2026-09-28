# Proposal: Self-Hosted Pivot

**Change ID:** self-hosted-pivot | **Workflow:** sdd-full | **Spec plane:** hybrid | **Board:** self-hosted-pivot | **Workload policy:** flexible

## Why

Server mode still carries a SaaS multi-tenant request plane even though the shipped default is single-tenant (`cfg.Server.MultiTenant=false`): per-request workspace selection (`workspaceSelector.allowRequestSelection`, `internal/platform/server/authentication.go:46-80`), the multi-tenant token verifier (`MultiTenantTokenPrincipalVerifier`, `internal/store/postgres/token_repository.go:141-166`, backed by migration 111), request-scoped vector wrapping, and six `cfg.Server.MultiTenant` branch sites (`internal/platform/server/server.go:214-218,277-279,293-299,305`; `http.go:796`; `agent_http.go:139`). The positioning artifacts (`docs/server-saas.md`, SaaS clauses in `AGENTS.md`, `README.md`, `docs/*`) still frame the product as a multi-tenant SaaS control plane.

Independently, the local `serve` mux exposes only observations, sessions, prompts, search, graph-edges, scores, and export (`internal/http/server.go:99-145`), while the embedded web UI depends on five server-only endpoints (`/api/me`, `/api/stats`, `/api/projects`, `/api/agent/projects`, `/api/graph/project-graph`; registered in `internal/platform/server/http.go:357-403` and `agent_http.go`). Measured consequence (LOCAL-MODE PARITY evidence in `openspec/changes/embedded-web-redesign/specs/quality-gates/coverage-push-90-addendum.md`, registered by Cortex decision #688): the project selector stays disabled because login swallows the 404 for `/api/me`, `/settings` renders "Acceso Restringido" because `principal` is null, and Sigma never mounts because `rawSubgraph` stays null.

## What Changes

- **D1 — Single-tenant authentication with a synthetic constant principal.** A new `staticBearerVerifier` performs a constant-time compare of the presented bearer against the configured token (pattern: `internal/http/server.go:990-1001`) and returns a synthetic constant principal assembled from configuration: `{Subject: cfg.Server.PrincipalSubject, OrgID: cfg.Server.TenantID, Roles: [owner], ProjectIDs: ["*"], ClassificationClearance: ["*"], WorkspaceIDs: [cfg.Server.WorkspaceID], RateLimitTier: "standard", GrantDigest: configured constant, GrantVersion: 1}`. The `internal/authz` enforce seam, role matrix, classification/ownership, `AuthorizedStore`, `cortex_bind_principal`, RLS, the audit sink, and rate tiers all stay.
- **D2 — Multi-tenant request-plane removal, migration path (b) only.** Delete the six `MultiTenant` branch sites and `MultiTenantTokenPrincipalVerifier`. No SQL byte changes: the embedded migration set (100-111) is frozen; migration 111 becomes dead-but-embedded, migration 110 (the single-tenant `cortex_verify_token_principal_v2` verifier) stays, and a compensating migration 112 to physically drop 111 is registered as roadmap only.
- **D3 — Neutral API handler package.** Extract `internal/api` implementing the parity handlers over a narrow `Port`; the server mux consumes it through an `Operations`-backed adapter (AuthorizedStore stays the server implementation), the local mux through a bundle-backed adapter. `internal/app/arch_test.go` invariants hold: no local -> server imports, `cmd/cortex` remains the only bridge.
- **D4 — Parity scope.** `/api/me` (synthetic principal response), `/api/stats` (recomputed from bundle Observations/Sessions/Metrics; no SQLite `GetServerStats` exists), `/api/projects` + `/api/agent/projects` (derived from sessions+observations+code), `/api/graph/project-graph` (reimplemented over graphstore edges). `/api/audit`, `/api/system/metrics`, `/api/rag/stats` are deferred to roadmap and documented as such.
- **D5 — Test & CI matrix.** Keep the 3-DSN trio, `scripts/postgres/bootstrap-authz.sql`, and the RLS-exploit probe suites (valid security tests under constant-tenant RLS); rework/delete multi-tenant-plane cases in a dedicated wave; `bench/documentation_contract_test.go` pins move inside this same change if any DSN/role/tag text moves; coverage stays `>= 80.0` with no cortex-owned package exclusion.
- **D6 — Docs & positioning.** Retire/replace `docs/server-saas.md` with a self-host deployment guide, rewrite the AGENTS.md server clauses (AuthorizedStore clause survives with a synthetic-principal note), sweep README/docs touchpoints, remove the `mode.go`/ `config.go` MultiTenant surface, fix the `docs/capability-matrix.md:47` `cortex_migration_ledger` misname, and add a supersession pointer in the closed embedded-web-redesign addendum's LOCAL-MODE PARITY section.

## Impact

- **Specs (new deltas):** `server-authn`, `api-parity`, `migrations`, `ci-tests`, `docs-positioning` (REQ-SH-001..003, 010..014, 020, 030..032, 040..042).
- **Code:** `internal/platform/server`, `internal/store/postgres`, `internal/authz`, `internal/server/external`, new `internal/api`, `internal/http`, `internal/config`, `internal/platform`; comment-only edits in `migrations/v2/embed.go` and `internal/migration`.
- **Tests:** platform/server, store/postgres (tagged), authz, external vector wrapper, e2e compose stack, `tools/e2e-web` Playwright flows, `bench` documentation contract, `internal/config`.
- **Docs:** `docs/server-saas.md` (retired), `AGENTS.md`, `README.md`, `docs/embedded-web.md`, `capability-matrix`, `verification-matrix`, `CONFIGURATION`, `RAILWAY_DEPLOYMENT`, `SERVER`, `HTTP-API`, `ARCHITECTURE`, `INSTALLATION`; minimal edit to the closed `coverage-push-90-addendum.md`.
- **Architecture gate:** `internal/app/arch_test.go` invariants (no local->server import, ports compile in isolation, zero-CGO local build, bundle vector interface) must remain green; `cmd/cortex` stays the sole server bridge.

## Non-Goals

- No implementation of any batch by the planner; no successor boards. Board `embedded-web-redesign` is complete (42 done / 4 superseded) and gains no new tasks except the single supersession pointer edit authorized by D6.
- No coverage-gate lowering, no re-introduction of 90.0 as an enforced threshold, and no cortex-owned package exclusion from `-coverpkg` (REQ-QA-004 stays in force).
- No LOCAL-MODE PARITY implementation tasks inside the old embedded-web addendum: this change's api-parity delta supersedes that roadmap section in the new change's spec plane.
- No SQL edits: `migrations/v2/*.sql` bytes, the embedded set (100-111), the ledger, and the retired root v1 files are never modified; migration 112 is roadmap, not tasked.
- No multi-tenant revival: no per-request tenant/workspace derivation, no billing/entitlements/SSO/provisioning control plane, no per-token rate-tier productization.
- Deferred parity surface: `/api/audit`, `/api/system/metrics`, `/api/rag/stats` are documented roadmap items, not tasks.
- No `web/` UI redesign, no `bench/` logic rewrite, no plugin harness changes.

## Risks

- **JSON contract drift:** extracted handlers must stay byte-compatible with what `web/` consumes → golden/contract assertions in `internal/api` plus the three Playwright flows.
- **Synthetic principal vs database grants:** the synthetic `Subject` must equal the provisioned service principal subject so RLS, `cortex_bind_principal`, and the audit sink keep working → startup fail-closed validation of `PrincipalSubject`/`TenantID`/`WorkspaceID`.
- **Migration 111 landmine:** DBs with a recorded ledger entry for 111 and the `ErrFutureMigration` class of failures must be documented, never "fixed" by editing embedded SQL → docs-only task (wave 3).
- **Coverage gate erosion:** deleting multi-tenant tests can drop measured coverage below 80.0 → dedicated gate re-check task at the end of wave 7.
- **Environment limits:** Playwright (Chromium) and Docker/DSN suites may be BLOCKED on this Windows workstation → compile-level local gates (`go vet -tags ...`) with CI as the authoritative executor.
- **Sequential same-file edits:** `server.go`, `http.go`, `agent_http.go`, and the platform/server test files are touched by more than one wave → dependency edges enforce ordering; parallel writers inside a wave stay file-disjoint (max 3 concurrent).

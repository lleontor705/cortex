# Delta for server-authn — Single-tenant server authentication with a synthetic constant principal

Adds the single-tenant authentication contract for `--mode server` after removal of the multi-tenant request plane (design decisions D1, D2, consequence decisions C2-C4).

## ADDED Requirements

### Requirement: REQ-SH-001: Static bearer verification and synthetic constant principal
The server composition MUST authenticate every request bearer through a `staticBearerVerifier` that compares the presented secret to the configured bearer using a constant-time comparison (`subtle.ConstantTimeCompare`, pattern of `internal/http/server.go:990-1001`) and MUST derive the principal exclusively from configuration as the synthetic constant `{Subject: cfg.Server.PrincipalSubject, OrgID: cfg.Server.TenantID, Roles: [owner], ProjectIDs: ["*"], ClassificationClearance: ["*"], WorkspaceIDs: [cfg.Server.WorkspaceID], RateLimitTier: "standard", GrantDigest: configured constant, GrantVersion: 1}`. Startup MUST fail closed when the configured bearer is empty/non-canonical (existing `validateBearerToken`) or when `PrincipalSubject`, `TenantID`, or `WorkspaceID` is empty. There MUST be no configured-token bypass in the request path: the startup bearer and every request bearer take the same verification function.

#### Scenario: valid bearer yields the synthetic principal (Happy)
- GIVEN a server composition with canonical `http.token` and complete `server.principal_subject`, `server.tenant_id`, `server.workspace_id`
- WHEN a request presents `Authorization: Bearer <http.token>`
- THEN the handler receives a principal whose subject, org, owner role, wildcard projects/clearance, workspace list, rate tier, grant digest, and grant version equal the configured constants

#### Scenario: wrong or padded bearer (Error)
- GIVEN the same composition
- WHEN a request presents a secret that differs from the configured bearer in any byte or carries surrounding whitespace
- THEN verification fails closed with the standard 401 unauthorized envelope and no principal enters the request context

#### Scenario: incomplete single-tenant configuration (Edge)
- GIVEN `server.principal_subject`, `server.tenant_id`, or `server.workspace_id` is empty
- WHEN the server runtime is constructed
- THEN construction returns an error naming the missing key before any pool or handler is created

### Requirement: REQ-SH-002: Multi-tenant request plane removal
All `cfg.Server.MultiTenant` branch sites (`internal/platform/server/server.go:214-218,277-279,293-299,305`; `http.go:796`; `agent_http.go:139`), the `MultiTenantTokenPrincipalVerifier` (`internal/store/postgres/token_repository.go:141-166`) and its dedicated coverage case, the `allowRequestSelection` workspace-selection semantics, and the `cfg.Server.MultiTenant` configuration field MUST be removed, leaving constant single-tenant behavior: server-scoped vector wrapping, fixed default workspace, and the static bearer verifier. The request header `X-Cortex-Workspace` MUST NOT select a workspace. `workspaceSelector` MUST collapse to the fixed configured default workspace.

#### Scenario: vector scoping is constant (Happy)
- GIVEN the server runtime constructs a vector provider
- WHEN composition completes
- THEN exactly the server-scoped wrapper (`NewServerScopedVectorIndex` with `cfg.Server.TenantID` and `cfg.Server.WorkspaceID`) is applied and `NewRequestScopedVectorIndex` has no caller in production code

#### Scenario: workspace header cannot redirect scope (Edge)
- GIVEN a request authenticated with the configured bearer
- WHEN it carries `X-Cortex-Workspace: <uuid other than the configured default>`
- THEN the request is rejected with the workspace-not-granted error and no non-default workspace enters the request context

#### Scenario: removed field is unrecoverable (Error)
- GIVEN the change is applied
- WHEN `multi_tenant` appears in configuration or `cfg.Server.MultiTenant` appears in any Go file
- THEN no code path consumes it, `internal/config` no longer declares the field, and `go test ./internal/config ./internal/platform/server` stays green

### Requirement: REQ-SH-003: Authorization seam retention
The `internal/authz` enforce seam, role matrix, classification and ownership checks, `AuthorizedStore`/`AuthorizedContext`, `cortex_bind_principal`, PostgreSQL RLS, the audit sink, and rate tiers MUST be retained unchanged in behavior for the single-tenant server. `DenyTenantMismatch` and `DenyWorkspace` MUST be kept as default-deny reasons (consequence decision C2); only branches reachable exclusively through multi-tenant principals may be de-fattened, and no default-deny error contract may change.

#### Scenario: owner synthetic principal is authorized (Happy)
- GIVEN the synthetic constant principal and the configured tenant/workspace
- WHEN `policy.Authorize` evaluates a workspace-read or admin-manage request
- THEN the decision is allowed through the same code path as before the change

#### Scenario: tenant mismatch still denies (Error)
- GIVEN a request whose resource tenant differs from the principal org
- WHEN `Enforce` evaluates it
- THEN the result is denied with reason `tenant_mismatch` exactly as pinned by `internal/authz/bola_test.go`

#### Scenario: audit sink provenance is constant (Edge)
- GIVEN the audit sink is composed at startup
- WHEN it records the first authorization event
- THEN the actor is `cfg.Server.PrincipalSubject` with the configured grant digest and grant version 1

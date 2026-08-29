# Delta for Vector Workspace Isolation

## ADDED Requirements

### Requirement: REQ-VECWS-001: Trusted Tenant and Workspace Boundary
Every server-mode vector write and query MUST carry non-empty `tenant_id` and `workspace_id` derived from trusted server/principal context. Client input MUST NOT override either value.

#### Scenario: Scoped vector retrieval
- GIVEN vectors from two workspaces in one tenant
- WHEN an authenticated principal searches its workspace
- THEN pgvector or Qdrant receives exact tenant and workspace filters
- AND only that workspace can contribute vector candidates.

#### Scenario: Reindex one workspace
- GIVEN an administrator starts reindex with a trusted tenant/workspace boundary
- WHEN points are generated or copied
- THEN every point is stamped with both identifiers
- AND progress remains attributable to that boundary.

#### Scenario: Missing trusted boundary
- GIVEN either tenant or workspace is empty or invalid
- WHEN a server vector write, query or reindex is attempted
- THEN the vector operation is rejected or skipped before adapter access
- AND authorized lexical search remains available.

### Requirement: REQ-VECWS-002: Adapter and Legacy Fail-Closed Behavior
pgvector and Qdrant MUST persist/filter `workspace_id` alongside `tenant_id`. Existing vectors lacking either value MUST NOT match a server query and MUST require non-destructive scoped reindexing.

#### Scenario: Newly indexed point
- GIVEN a valid scoped point
- WHEN it is upserted and queried through either adapter
- THEN both metadata values are persisted and used as mandatory match conditions.

#### Scenario: Legacy point
- GIVEN a legacy pgvector row with empty workspace or a Qdrant point without workspace payload
- WHEN a scoped server query runs
- THEN that point is excluded
- AND results fall back to authorized lexical candidates if no scoped vectors remain.

#### Scenario: Adapter or reindex failure
- GIVEN schema evolution, vector health or scoped reindex fails
- WHEN hybrid search executes
- THEN no unscoped query is attempted and no vector candidate is returned
- AND the failure does not delete legacy data or cross a workspace boundary.


# Delta for Code Index Isolation

## ADDED Requirements

### Requirement: REQ-CODE-001: Scoped PostgreSQL AST storage
Server AST symbols and relations MUST be keyed and filtered by principal-derived tenant, workspace and project; PostgreSQL MUST force RLS, and runtime code MUST NOT create schema or read legacy unscoped tables.

#### Scenario: Granted scoped graph
- GIVEN scoped symbols and relations for two sibling workspaces
- WHEN a principal reads a granted project
- THEN only rows matching its bound tenant, workspace and project are returned
- AND identities and relationship endpoints remain scope-consistent

#### Scenario: Legacy index during rollout
- GIVEN legacy rows containing only a project name and scoped reindex state `missing`
- WHEN code retrieval runs
- THEN legacy rows are excluded
- AND the caller receives `code_index_unavailable` degradation

#### Scenario: Missing or forged scope
- GIVEN absent principal binding or request-supplied tenant/workspace metadata
- WHEN AST storage is queried or written
- THEN the operation fails closed with a deterministic authorization error
- AND no row existence or count is disclosed

### Requirement: REQ-CODE-002: Capability-based code access
The system MUST authorize AST reads with `ResourceCode/ActionRead` and the exact project grant; possession of role `agent` MUST NOT bypass either check.

#### Scenario: Non-agent principal with grants
- GIVEN a viewer or service account has `code:read` and the selected project grant
- WHEN it requests symbols or relationships
- THEN access is allowed within the verified boundary

#### Scenario: Partial corpus capability
- GIVEN a principal can search and read memories but lacks `code:read`
- WHEN the conversational service retrieves evidence
- THEN code retrieval is omitted and reported as `code_not_authorized`
- AND authorized memory retrieval may continue

#### Scenario: Agent role without project grant
- GIVEN a principal has role `agent` but not the selected project grant
- WHEN it requests AST or agent retrieval for that project
- THEN access is rejected with `project_not_granted`
- AND no code metadata reaches the prompt

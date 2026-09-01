# Delta for Server Database Roles

## ADDED Requirements

### Requirement: REQ-DBROLE-001: Distinct Migration and Runtime Roles
When `server.bootstrap_development` is false, server startup MUST require explicit runtime and migration DSNs whose parsed PostgreSQL role names differ, and MUST validate this before opening or migrating the database.

#### Scenario: Production role separation
- GIVEN explicit DSNs resolving to `cortex_app` and `cortex_migration`
- WHEN server mode starts
- THEN migrations use only the migration DSN
- AND the long-lived pool uses only the runtime DSN.

#### Scenario: Equivalent endpoints with distinct roles
- GIVEN both DSNs target the same database but use distinct role names
- WHEN startup validation runs
- THEN validation succeeds
- AND passwords remain absent from errors and logs.

#### Scenario: Missing or same role
- GIVEN non-development mode with a missing migration DSN or equal parsed roles
- WHEN startup is attempted
- THEN startup fails before any database connection or migration
- AND the error identifies the violated role boundary without echoing a DSN.

### Requirement: REQ-DBROLE-002: Development-Only Fallback
Runtime-to-migration DSN fallback MAY occur only when `server.bootstrap_development=true`; configuration loading MUST NOT silently create that fallback for other modes.

#### Scenario: Development convenience
- GIVEN development bootstrap is enabled and only a runtime DSN is supplied
- WHEN server mode starts
- THEN the runtime DSN may be used for migration/bootstrap
- AND the behavior is documented as development-only.

#### Scenario: Explicit development migration role
- GIVEN development bootstrap is enabled and both DSNs are supplied
- WHEN server mode starts
- THEN the explicit migration DSN is used.

#### Scenario: Accidental production omission
- GIVEN development bootstrap is disabled and the migration DSN is omitted
- WHEN configuration is loaded and server startup runs
- THEN no fallback is synthesized
- AND startup fails closed.


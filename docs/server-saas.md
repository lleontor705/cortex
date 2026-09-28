# Self-Hosted Server Deployment

`cortex --mode server` is a single-tenant PostgreSQL composition. One deployment owns exactly one tenant and one workspace, both fixed by trusted administrator configuration; there is no request-time tenant or workspace selection, no tenant or workspace creation API, and no hosted control plane. Every authenticated request acts as one synthetic service principal assembled from those configured constants.

For the general server guide — container images, rollout stages, agent configuration, and client surfaces — see [SERVER.md](SERVER.md).

## Configuration constants

Startup validates the configuration and fails closed, naming the missing or malformed key, before any database pool or HTTP handler exists. The three identity constants are required and must be valid UUIDs.

| Configuration key | Environment variable | Purpose |
| --- | --- | --- |
| `server.tenant_id` | `CORTEX_SERVER_TENANT_ID` | Tenant UUID owned by this deployment; installed as the row-level-security tenant for every transaction. |
| `server.workspace_id` | `CORTEX_SERVER_WORKSPACE_ID` | Workspace UUID owned by this deployment; the default and only workspace scope. |
| `server.principal_subject` | `CORTEX_SERVER_PRINCIPAL_SUBJECT` | Subject UUID of the synthetic service principal every authenticated request acts as. |
| `server.grant_digest` | `CORTEX_SERVER_GRANT_DIGEST` | Optional and deprecated. Grant integrity is derived inside PostgreSQL from the bootstrapped actor row; a configured value is accepted only for backward compatibility. |
| `http.token` | `CORTEX_HTTP_TOKEN` | The single static bearer. Required, secret-managed, canonical (no leading or trailing whitespace and no control characters), and at least 12 characters. |

Startup additionally requires `server.storage.driver: postgres`, a runtime DSN, and a migration DSN using a distinct role (see [PostgreSQL bootstrap](#postgresql-bootstrap-three-dsns)).

## Static bearer authentication

The server composes one static bearer verifier:

- It compares the presented `Authorization: Bearer` secret against `http.token` with a constant-time comparison. A mismatch, an empty secret, or a whitespace-padded secret returns the same opaque `401`, so no rejection path echoes credential material.
- A matching secret yields one synthetic principal built once from `tenant_id`, `workspace_id`, and `principal_subject`: the subject is `principal_subject`, the tenant is `tenant_id`, and the workspace grant list contains only `workspace_id`.
- The verifier holds no database capability. There is no per-request tenant or workspace derivation from request fields; `tenant_id` and `workspace_id` come only from configuration, never from client input.

Rotating the configured bearer is the operator control. The verifier assembles its principal once at startup, so a rotation takes effect after a restart.

The embedded web control room authenticates against the server with the same static bearer. The first-boot web access key managed by `cortex web key show|regenerate` is a local-mode credential and is never accepted by server `/api/*`; the two must not be conflated.

## PostgreSQL bootstrap (three DSNs)

A self-hosted deployment uses three distinct connection paths during setup. The runtime and migration DSNs are configuration keys; the administrative path is used once to create the non-superuser roles before any migration runs.

1. **Administrative DSN** — a privileged login that executes the shared role bootstrap:

   ```bash
   psql "$CORTEX_BOOTSTRAP_DSN" -f scripts/postgres/bootstrap-authz.sql
   ```

   `scripts/postgres/bootstrap-authz.sql` idempotently creates the non-login owner role `cortex_admin`, the LOGIN runtime role `cortex_test` (`NOSUPERUSER NOBYPASSRLS`), and the LOGIN migration role `cortex_admin_login` (`NOSUPERUSER NOBYPASSRLS`) that inherits `cortex_admin`.
2. **Migration DSN** — `server.storage.migration_dsn` / `CORTEX_SERVER_STORAGE_MIGRATION_DSN`, using the privileged migration login. The migration handle applies the embedded server schema in strict version order and reconciles the bootstrap service principal, then closes before traffic is served.
3. **Runtime DSN** — `server.storage.dsn` / `CORTEX_SERVER_STORAGE_DSN`, using the long-lived non-superuser application login with no `BYPASSRLS`.

The runtime and migration roles must differ; startup rejects a deployment where both DSNs name the same role. DSN contents and passwords are never included in boundary-validation errors. Only local development may set `server.bootstrap_development: true` to reuse the runtime DSN for migrations and omit the migration DSN; never enable that switch in a shared deployment.

On startup the migration handle reconciles the configured service principal, its canonical grants, and the reserved bootstrap token through `cortex_bootstrap_service_principal`, keyed by `tenant_id`, `workspace_id`, `principal_subject`, and the configured bearer. The bearer reaches PostgreSQL only as a bound parameter and never appears in error text.

## Row-level-security posture

Tenant isolation is enforced by PostgreSQL, not by the HTTP layer:

- Every core server table forces row-level security and is keyed on `cortex_current_tenant()`, which reads the transaction's bound tenant from `cortex_tenant_context`.
- `cortex_bind_principal` installs that context for the current backend and transaction. After migration 112 it accepts two provenance shapes: the `v1:` token-bound proof minted by token verification, and the `static:<hex>` proof the single-tenant composition derives from the bootstrapped actor's persisted grant digest. The tenant, actor, and grant version are re-read from the actor's own row under the shared advisory gate, so a caller cannot bind an arbitrary tenant, a different actor, or a stale version.
- The application role holds only the EXECUTE matrix granted to `cortex_app` and no direct identity-table privileges; the migration role alone reconciles the bootstrap principal.

Because the tenant and workspace are deployment constants, the SQL-level tenant and workspace isolation probes and the `cortex_app`-only EXECUTE boundary remain the verified posture. Redeploying the previous runtime binary is the rollback story: the migration line is forward-only and no schema or ledger row is hand-edited.

## See also

- [SERVER.md](SERVER.md) — server deployment, container images, rollout stages, and client surfaces.
- [CONFIGURATION.md](CONFIGURATION.md) — the full configuration reference.
- [project-context-protocol-identity-privilege.md](project-context-protocol-identity-privilege.md) — migration ledger rollout runbook.

# Server SaaS data plane

`cortex --mode server` keeps its existing single-tenant deployment as the default. Set `server.multi_tenant: true` (or `CORTEX_SERVER_MULTI_TENANT=true`) to enable the SaaS data plane after PostgreSQL migration 111 is available.

```yaml
server:
  multi_tenant: true
  tenant_id: <bootstrap-tenant-uuid>
  workspace_id: <bootstrap-workspace-uuid>
  principal_subject: <bootstrap-service-principal-uuid>
http:
  token: <bootstrap-service-bearer>
```

The configured tenant, workspace, subject, and bearer remain required. They provision and authenticate the server's **bootstrap service principal** and allow startup/reindex operations. They are not a global administrator credential and must never be given to a browser or used as a cross-tenant API token.

## Request boundary

1. A bearer is verified by `cortex_verify_token_principal_global`. PostgreSQL derives the candidate tenant from the stored token-prefix head and proves the tenant-bound HMAC before returning a principal. No HTTP field selects a tenant. Ambiguous prefix matches fail closed.
2. `X-Cortex-Workspace` is considered only after bearer verification. It must be an exact UUID in the verified principal's workspace grants. Missing headers default to the principal's first grant; invalid, whitespace-padded, or foreign selections return `403 workspace_not_granted`.
3. The authenticated request creates an `AuthorizedContext` with the verified tenant and selected workspace. Server HTTP, MCP, graph operations, and project RAG use that context rather than configured workspace values.
4. The vector adapter receives the same context at its final boundary. Caller-supplied `tenant_id` and `workspace_id` filters are overwritten; a vector operation without request scope fails closed. Reindexing binds its already-authorized source scope before indexing.

The web client obtains `workspace_id` from `/api/me`, sends the selector on normal HTTP and agent SSE requests, and only displays the token's returned workspace grants. The browser is a convenience layer, not the authority.

## Operational rollout

- Apply the normal server migration flow with the migration DSN; migration 111 is forward-only and ledgered.
- Start with one tenant and test a second tenant using separate bearer tokens. Verify that a token from tenant A cannot request tenant B's workspace UUID.
- Monitor `workspace_not_granted`, authentication failures, vector health, RAG degradation, and tenant/workspace dimensions in the existing audit/structured logs. Never log bearer tokens or prompt text.
- Keep the runtime PostgreSQL role separate from the migration role. Only the migration role may provision/reconcile the bootstrap service principal.

## Deliberate next control-plane increment

This release is a secure SaaS **data-plane** cut. Tenant and workspace provisioning, billing/quotas, domain/SSO lifecycle, workspace display names, and a cross-tenant operator control plane are intentionally not exposed through the tenant API or the web UI yet.

Before enabling self-service tenant creation, add a separate control-plane service and database role, normalized tenant/workspace memberships, lifecycle/audit retention policies, and transaction-level workspace binding plus core-table RLS coverage. Do not emulate a global administrator by minting a tenant bearer with broad browser access.
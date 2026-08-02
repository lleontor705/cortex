# Server Deployment

Server mode is the PostgreSQL composition selected with `cortex --mode server`.

## Docker Development

```bash
docker compose up --build
```

The development Compose file creates PostgreSQL, applies the embedded server schema, bootstraps a development organization/workspace/principal, and starts Cortex on port `7438`. It is not a production secret or identity configuration.

## Production Requirements

- PostgreSQL 16 or newer supported by the deployment policy.
- Separate privileged migration DSN and non-privileged runtime DSN.
- Verified principal grants for tenant, workspace, projects, roles, scopes, and classification.
- A secret-managed bearer token or an upstream authenticated gateway.
- Explicit `http.allowed_origins` for browser clients.
- TLS at the deployment boundary.

Server persistence remains behind `AuthorizedStore`; transports never receive raw repositories, transactions, or client-selected tenant authority. `/health` is public, while `/api/*` and `/mcp` require bearer authentication.

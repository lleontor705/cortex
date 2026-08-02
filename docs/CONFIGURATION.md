# Configuration

Cortex reads YAML configuration and applies `CORTEX_*` environment overrides. The default local file is `~/.cortex/cortex.yaml`; the default SQLite database is `~/.cortex/cortex.db`.

## Common Keys

| Key | Environment | Notes |
|---|---|---|
| `database.path` | `CORTEX_DATABASE_PATH` | Local SQLite path |
| `http.enabled` | `CORTEX_HTTP_ENABLED` | Enable local/server HTTP composition |
| `http.host` | `CORTEX_HTTP_HOST` | Bind address |
| `http.port` | `CORTEX_HTTP_PORT` | Default `7438` |
| `http.token` | `CORTEX_HTTP_TOKEN` | Required for protected/non-loopback HTTP |
| `http.allowed_origins` | `CORTEX_HTTP_ALLOWED_ORIGINS` | Comma-separated browser origins |
| `search.embedding_provider` | `CORTEX_SEARCH_EMBEDDING_PROVIDER` | `none`, `ollama`, or `openai` |
| `search.embedding_model` | `CORTEX_SEARCH_EMBEDDING_MODEL` | Provider-specific model |
| `vector.provider` | `CORTEX_VECTOR_PROVIDER` | Local stub/BLOB or server adapter |

`CORTEX_PORT` is not a Cortex configuration key. Use `CORTEX_HTTP_PORT`.

## Server-only Keys

| Key | Purpose |
|---|---|
| `server.storage.dsn` | Non-privileged runtime PostgreSQL connection |
| `server.storage.migration_dsn` | Privileged migration/bootstrap connection |
| `server.tenant_id` | Configured organization UUID |
| `server.workspace_id` | Configured workspace UUID |
| `server.principal_subject` | Verified service-account subject |
| `server.roles` | Granted roles |
| `server.scopes` | Granted scopes |
| `server.project_ids` | Granted project UUIDs/keys |
| `server.classification_clearance` | Classification clearance |

Tenant and workspace authority come from the verified server principal, not request JSON.

## Vector Availability

| Build | Local vector search |
|---|---|
| `make build` | Degraded stub |
| `go build` | Degraded stub |
| `go build -tags cortex_vectors` | SQLite BLOB cosine scan |
| Release artifacts | Functional tag enabled |
| Qdrant/pgvector | Server-only, opt-in adapter suites |

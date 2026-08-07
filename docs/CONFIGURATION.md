# Configuration

Cortex reads YAML configuration and applies `CORTEX_*` environment overrides. The default local file is `~/.cortex/cortex.yaml`; the default SQLite database is `~/.cortex/cortex.db`.

## Choose A Configuration Method

| Method | Best for | How |
|---|---|---|
| TUI | Interactive local setup | Run `cortex tui`, choose **Local settings**, edit, then restart Cortex and your agent |
| YAML | Reviewable, complete configuration | Edit `~/.cortex/cortex.yaml` using `cortex.yaml.example` as reference |
| Environment | Containers, CI, temporary overrides | Set the corresponding `CORTEX_*` variable |

Precedence is **defaults → YAML → environment**. An environment variable always wins over the value shown in YAML or saved by the TUI. The TUI preserves existing YAML comments and custom keys, validates before writing, and uses an atomic restricted-permission file replacement.

The Local settings screen intentionally does not apply database, listener, MCP transport, or sync changes in place. Restart Cortex and any running agent after saving.

## Local-First Setup

For the normal local-first mode, MCP and the plugin use SQLite while a background worker exchanges changes with Cortex Server:

```yaml
database:
  path: ~/.cortex/cortex-v2.db

mcp:
  remote:
    enabled: false

sync:
  enabled: true
  url: https://cortex.example.com
  token_env: CORTEX_REMOTE_TOKEN
  interval: 30s
  timeout: 30s
```

Set `CORTEX_REMOTE_TOKEN` outside YAML. `token_env` is the **name** of the environment variable, never the token itself.

Use `mcp.remote.enabled: true` only when you want `cortex mcp` to bypass SQLite and forward every MCP call directly to the remote `/mcp` endpoint. Remote MCP proxy and local-first sync are different modes.

## Common Keys

| Key | Environment | Notes |
|---|---|---|
| `database.path` | `CORTEX_DATABASE_PATH` | Local SQLite path |
| `http.enabled` | `CORTEX_HTTP_ENABLED` | Enable local/server HTTP composition |
| `http.host` | `CORTEX_HTTP_HOST` | Bind address |
| `http.port` | `CORTEX_HTTP_PORT` | Default `7438` |
| `http.token` | `CORTEX_HTTP_TOKEN` | Required for protected/non-loopback HTTP |
| `http.allowed_origins` | `CORTEX_HTTP_ALLOWED_ORIGINS` | Comma-separated browser origins |
| `mcp.remote.enabled` | `CORTEX_MCP_REMOTE_ENABLED` | Proxy `cortex mcp` to a remote Streamable HTTP server |
| `mcp.remote.url` | `CORTEX_MCP_REMOTE_URL` | Remote MCP endpoint, including `/mcp` |
| `mcp.remote.token_env` | `CORTEX_MCP_REMOTE_TOKEN_ENV` | Name of the environment variable holding the bearer token |
| `mcp.remote.timeout` | `CORTEX_MCP_REMOTE_TIMEOUT` | Remote request timeout, default `30s` |
| `sync.enabled` | `CORTEX_SYNC_ENABLED` | Keep SQLite local and enable bidirectional server replication |
| `sync.url` | `CORTEX_SYNC_URL` | Cortex Server base URL, without `/mcp` |
| `sync.token_env` | `CORTEX_SYNC_TOKEN_ENV` | Environment variable containing the bearer token |
| `sync.interval` | `CORTEX_SYNC_INTERVAL` | Background replication interval, default `30s` |
| `sync.timeout` | `CORTEX_SYNC_TIMEOUT` | Per-request timeout, default `30s` |
| `search.embedding_provider` | `CORTEX_SEARCH_EMBEDDING_PROVIDER` | `none`, `ollama`, or `openai` |
| `search.embedding_model` | `CORTEX_SEARCH_EMBEDDING_MODEL` | Provider-specific model |
| `vector.provider` | `CORTEX_VECTOR_PROVIDER` | Local stub/BLOB or server adapter |

`CORTEX_PORT` is not a Cortex configuration key. Use `CORTEX_HTTP_PORT`.

With `sync.enabled`, local HTTP, MCP, CLI, and plugin writes continue to use SQLite. Cortex retries idempotent pushes and incrementally pulls server changes in the background; `cortex sync --remote` forces an immediate cycle. Set `mcp.remote.enabled: false` for local-first MCP operation.

## TUI Fields

The **Local settings** screen manages the most common local runtime keys:

| Group | Fields |
|---|---|
| Local database | `database.path` |
| Local HTTP | `http.enabled`, `http.host`, `http.port` |
| MCP transport | `mcp.remote.enabled`, `mcp.remote.url`, `mcp.remote.token_env` |
| Replication | `sync.enabled`, `sync.url`, `sync.token_env`, `sync.interval` |

The screen is organized as **Local → HTTP → MCP → Sync → Review**. It shows the resulting mode (`LOCAL ONLY`, `LOCAL-FIRST + SYNC`, or `REMOTE MCP`) before anything is written.

Keyboard controls: `h/l`, `tab`, or left/right move between sections; `j/k` or up/down move between fields; `space` toggles; `enter` edits; `s` validates and saves from any section; `r` resets staged changes; and `esc` returns. URLs and token environment names may remain empty while their feature is disabled.

## Troubleshooting

- A TUI value appears unchanged after restart: check for a matching `CORTEX_*` environment override.
- Local memories do not appear remotely: run `cortex sync --remote` and inspect the reported cursor/error.
- MCP does not use local SQLite: ensure `mcp.remote.enabled` is `false`.
- Sync authentication fails: verify that the variable named by `sync.token_env` exists in the environment of the Cortex process.
- A database path points to a Cortex v1 file: Cortex v2 refuses it without mutation; use an explicit migration/import rather than replacing it automatically.

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

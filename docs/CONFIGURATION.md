# Configuration

Cortex supports multi-format configuration (**YAML**, **JSON**, **TOML**) and applies `CORTEX_*` environment overrides. The default configuration file is `~/.cortex/cortex.yaml` (or `cortex.json` / `cortex.toml`); the default SQLite database is `~/.cortex/cortex.db`.

## Choose A Configuration Method

| Method | Best for | How |
|---|---|---|
| CLI Direct | Instant programmatic & scriptable updates | `cortex config set <key> <value>` / `cortex config get <key>` |
| TUI Visual Center | Interactive navigation & theme switching | Run `cortex tui`, press `t` for themes, `L` for auth, or choose **Local settings** |
| Multi-Format File | Reviewable, version-controlled config | Edit `cortex.yaml`, `cortex.json`, or `cortex.toml` using minimal clean schema |
| Environment | Containers, CI, temporary overrides | Set the corresponding `CORTEX_*` variable |

Precedence is **defaults → config file → environment**. An environment variable always wins over the value shown in the file or saved by the TUI. The configuration loader automatically trims default/empty sections to guarantee a **Zero-Bloat configuration file** (under 15 lines for typical local setups).

## Zero-Bloat Local Setup

```yaml
ai:
  provider: ollama
  model: qwen3-embedding:8b
  base_url: http://localhost:11434

database:
  path: ~/.cortex/cortex-v2.db

http:
  enabled: true
  port: 7438
```

## CLI Configuration Management

```bash
# Initialize a minimal clean configuration
cortex config init --format=yaml --force

# Get / Set properties instantly
cortex config get ai.provider
cortex config set ai.provider ollama
cortex config set ai.model qwen3-embedding:8b
cortex config set http.port 8080

# Interactive CLI Wizard & Path lookup
cortex config wizard
cortex config path
```

## CLI Authentication Management

```bash
# Authenticate session with Bearer Token
cortex auth login --token=ctx_secret_token_123456

# Inspect current authentication status and active role
cortex auth status

# End session / logout
cortex auth logout
```

## Common Keys

| Key | Environment | Notes |
|---|---|---|
| `ai.provider` | `CORTEX_AI_PROVIDER` | Unified AI provider (`none`, `ollama`, `openai`, `anthropic`, `openrouter`, `groq`, `deepseek`, `custom`) |
| `ai.model` | `CORTEX_AI_MODEL` | Unified embedding/LLM model name |
| `ai.base_url` | `CORTEX_AI_BASE_URL` | Endpoint URL for Ollama or custom providers |
| `database.path` | `CORTEX_DATABASE_PATH` | Local SQLite path |
| `http.enabled` | `CORTEX_HTTP_ENABLED` | Enable local/server HTTP composition |
| `http.host` | `CORTEX_HTTP_HOST` | Bind address |
| `http.port` | `CORTEX_HTTP_PORT` | Default `7438` |
| `http.token` | `CORTEX_HTTP_TOKEN` | Required for protected/non-loopback HTTP |
| `http.allowed_origins` | `CORTEX_HTTP_ALLOWED_ORIGINS` | Comma-separated browser origins |
| `mcp.remote.enabled` | `CORTEX_MCP_REMOTE_ENABLED` | Proxy `cortex mcp` to a remote Streamable HTTP server |
| `mcp.remote.url` | `CORTEX_MCP_REMOTE_URL` | Remote MCP endpoint, including `/mcp` |
| `mcp.remote.token_env` | `CORTEX_MCP_REMOTE_TOKEN_ENV` | Environment variable holding the bearer token |
| `sync.enabled` | `CORTEX_SYNC_ENABLED` | Enable bidirectional server replication |
| `sync.url` | `CORTEX_SYNC_URL` | Cortex Server base URL, without `/mcp` |
| `sync.token_env` | `CORTEX_SYNC_TOKEN_ENV` | Environment variable containing the bearer token |
| `sync.interval` | `CORTEX_SYNC_INTERVAL` | Background replication interval, default `30s` |

`CORTEX_PORT` is not a Cortex configuration key. Use `CORTEX_HTTP_PORT`.

With `sync.enabled`, local HTTP, MCP, CLI, and plugin writes continue to use SQLite. Cortex retries idempotent pushes and incrementally pulls server changes in the background; `cortex sync --remote` forces an immediate cycle. Set `mcp.remote.enabled: false` for local-first MCP operation.

## Bearer transport policy

`mcp.remote.url` and `sync.url` are Bearer destinations and share one transport policy (implemented in `internal/transportpolicy`):

- HTTPS is required for every non-loopback destination.
- Plain HTTP is accepted only on strict loopback: an IPv4 literal in `127.0.0.0/8`, the IPv6 literal `[::1]`, or the exact dotless name `localhost`. Any other plain-HTTP URL is rejected at configuration load time and by the sync client before any credential is attached. Hostnames are never resolved to decide this.
- Redirects are followed only when they keep the scheme — an HTTPS-to-HTTP downgrade is always rejected, even towards loopback — and keep the exact origin (scheme + host + port). Otherwise the request fails before the token is forwarded.

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
- A `sync.url` or `mcp.remote.url` using plain HTTP towards a non-loopback host is rejected at startup: switch the destination to HTTPS. A local development server on `127.0.0.1` (any `127.x` address), `[::1]`, or `localhost` may keep plain HTTP.
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

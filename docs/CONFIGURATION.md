# Configuration Guide & Reference

Cortex provides a unified, multi-format configuration architecture supporting **YAML**, **JSON**, **TOML**, and **Environment Variables (`CORTEX_*`)**. 

---

## 1. Precedence Model

Configuration values are resolved strictly in the following order (highest precedence wins):

```text
┌───────────────────────────────────────────────────────────┐
│ 1. Explicit CLI Flags (e.g. --config, --mode, --token)    │
├───────────────────────────────────────────────────────────┤
│ 2. Environment Variables (CORTEX_* / Process Environment) │
├───────────────────────────────────────────────────────────┤
│ 3. Active Configuration File (cortex.yaml, .json, .toml) │
├───────────────────────────────────────────────────────────┤
│ 4. Built-in Compiled Defaults (Zero-Bloat Baseline)      │
└───────────────────────────────────────────────────────────┘
```

* **Environment Variable Matching**: Viper automatically translates dot-notated paths to uppercase snake_case prefixed by `CORTEX_` (e.g. `ai.provider` $\rightarrow$ `CORTEX_AI_PROVIDER`, `http.port` $\rightarrow$ `CORTEX_HTTP_PORT`).
* **Environment Overrides**: An environment variable always overrides the corresponding value in the configuration file or TUI state.
* **Zero-Bloat Model**: The configuration manager automatically trims default and empty sections when writing to disk, keeping local configuration files clean (typically under 15 lines) and preventing serialization of internal SQLite pragmas or server-specific multi-tenant parameters.

---

## 2. Configuration Methods

### A. Environment Files (`.env` / Docker)

Copy the provided template to configure containers, CI/CD, or local deployments:

```bash
cp .env.example .env
```

### B. CLI Configuration Commands

Programmatically inspect and update configuration keys:

```bash
# Initialize a clean configuration file
cortex config init --format=yaml --force

# Inspect active configuration values
cortex config get ai.provider
cortex config get http.port

# Update configuration properties
cortex config set ai.provider ollama
cortex config set ai.model qwen3-embedding:8b
cortex config set http.port 7438

# Interactive setup wizard and file path lookup
cortex config wizard
cortex config path
```

### C. CLI Authentication Management

```bash
# Authenticate session with Bearer Token
cortex auth login --token=ctx_secret_token_123456

# Inspect active authentication status and subject identity
cortex auth status

# Logout and clear credentials
cortex auth logout
```

### D. TUI Visual Center

Launch the interactive Terminal User Interface:

```bash
cortex tui
```
* Press `t` to cycle dynamic themes (Dark, Light, High Contrast).
* Press `L` to open the authentication modal.
* Navigate to **Local settings** to configure database, HTTP, MCP, and replication parameters interactively.

---

## 3. Comprehensive Configuration Reference

### Infrastructure & Server Storage

| Configuration Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `server.storage.driver` | `CORTEX_SERVER_STORAGE_DRIVER` | `postgres` | Persistence driver: `postgres` (server) or `sqlite` (local). |
| `server.storage.dsn` | `CORTEX_SERVER_STORAGE_DSN` | *(None)* | Non-privileged runtime PostgreSQL DSN (`NOSUPERUSER`, `NOBYPASSRLS`). Falls back to `DATABASE_URL` / `POSTGRES_URL`. |
| `server.storage.migration_dsn` | `CORTEX_SERVER_STORAGE_MIGRATION_DSN` | *(None)* | Privileged migration DSN. Applied during startup preflight and closed immediately. |
| `server.storage.max_conns` | `CORTEX_SERVER_STORAGE_MAX_CONNS` | `10` | Maximum open database pool connections. |

### Multi-Tenancy & Identity Privileges

| Configuration Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `server.auto_bootstrap` | `CORTEX_SERVER_AUTO_BOOTSTRAP` | `false` | When `true`, auto-generates tenant, workspace, owner subject, and Bearer token on first boot. |
| `server.bootstrap_development` | `CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT` | `false` | Allows development tenant provisioning without dedicated migration role separation. |
| `server.multi_tenant` | `CORTEX_SERVER_MULTI_TENANT` | `false` | Enables SaaS multi-tenant isolation. When `true`, tenant and workspace come from verified Bearer grants. |
| `server.tenant_id` | `CORTEX_SERVER_TENANT_ID` / `CORTEX_TENANT_ID` | *(None)* | Configured tenant UUID (UUIDv4). |
| `server.workspace_id` | `CORTEX_SERVER_WORKSPACE_ID` / `CORTEX_WORKSPACE_ID` | *(None)* | Configured workspace UUID (UUIDv4). |
| `server.principal_subject` | `CORTEX_SERVER_PRINCIPAL_SUBJECT` / `CORTEX_PRINCIPAL_SUBJECT` | *(None)* | Verified service account subject UUID. |
| `server.roles` | `CORTEX_SERVER_ROLES` | `[]` | Comma-separated roles granted to the principal (e.g. `owner,admin`). |
| `server.scopes` | `CORTEX_SERVER_SCOPES` | `[]` | Comma-separated authorized scopes (e.g. `workspaces:read,workspaces:write`). |

### HTTP API, MCP Transport & Logging

| Configuration Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `http.enabled` | `CORTEX_HTTP_ENABLED` | `true` | Enables HTTP REST API (`/api/*`) and Streamable HTTP MCP (`/mcp`). |
| `http.host` | `CORTEX_HTTP_HOST` | `localhost` | Network interface to bind (`0.0.0.0` for containers/all interfaces). |
| `http.port` | `CORTEX_HTTP_PORT` | `7438` | Port to listen on. *(Note: `CORTEX_PORT` is intentionally rejected).* |
| `http.token` | `CORTEX_HTTP_TOKEN` | `""` | Authentication Bearer token. Required for non-loopback bindings. |
| `http.allowed_origins` | `CORTEX_HTTP_ALLOWED_ORIGINS` | `[]` | Comma-separated list of allowed CORS browser origins. |
| `logging.level` | `CORTEX_LOGGING_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error`. |
| `logging.format` | `CORTEX_LOGGING_FORMAT` | `json` | Log output format: `json`, `text`, `plain`. |

### Embeddings (Semantic Search & Vector Indexing)

| Configuration Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `search.embedding_provider` | `CORTEX_EMBEDDING_PROVIDER` / `CORTEX_SEARCH_EMBEDDING_PROVIDER` | `none` | Embedding model provider: `none` (native BM25), `openai`, `ollama`, `gemini`. |
| `search.embedding_model` | `CORTEX_EMBEDDING_MODEL` / `CORTEX_SEARCH_EMBEDDING_MODEL` | `text-embedding-3-small` | Embedding model identifier. |
| `search.embedding_base_url` | `CORTEX_EMBEDDING_BASE_URL` / `CORTEX_SEARCH_EMBEDDING_BASE_URL` | `""` | Custom or Ollama base endpoint URL. |
| `search.ollama_auto_start` | `CORTEX_SEARCH_OLLAMA_AUTO_START` | `false` | Automatically starts local Ollama daemon when needed. |
| `search.fusion_k` | `CORTEX_SEARCH_FUSION_K` | `60` | Reciprocal Rank Fusion (RRF) rank constant. |

### LLM (Agent Reasoning, Extraction & Synthesis)

| Configuration Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `llm.provider` | `CORTEX_LLM_PROVIDER` | `openai` | Generative provider: `openai`, `anthropic`, `gemini`, `ollama`, `groq`, `deepseek`. |
| `llm.model` | `CORTEX_LLM_MODEL` | `gpt-4o-mini` | LLM model identifier. |
| `llm.base_url` | `CORTEX_LLM_BASE_URL` | `""` | Custom provider base URL. |
| `llm.api_key` | `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `GEMINI_API_KEY` | *(Secret)* | Outbound provider API credentials (read from environment only). |

### Outbound Network & Security Bounds (SEC-02)

| Environment Variable | Default | Constraint / Description |
| :--- | :--- | :--- |
| `CORTEX_LLM_PROVIDER` | `""` | Preset: `openai`, `anthropic`, `google`, `gemini`, `ollama`, `generic`. |
| `CORTEX_LLM_MODEL` | `""` | Model identifier override. |
| `CORTEX_LLM_API_KEY` | `""` | Outbound authentication key (falls back to `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`). |
| `CORTEX_LLM_BASE_URL` | `""` | Destination endpoint. Must be HTTPS unless loopback HTTP switch is enabled. |
| `CORTEX_LLM_ALLOWED_HOSTS` | `[]` | Comma-separated list of approved destination hostnames (max 64). |
| `CORTEX_LLM_ALLOWED_PORTS` | `[443]` | Comma-separated list of approved TCP ports (max 16). |
| `CORTEX_LLM_ALLOW_LOOPBACK` | `false` | Explicit switch permitting loopback HTTPS destinations. |
| `CORTEX_LLM_ALLOW_LOOPBACK_HTTP` | `false` | Explicit switch permitting plain HTTP to strict loopback (`127.0.0.1`, `localhost`). |
| `CORTEX_LLM_MAX_CONCURRENT` | `4` | Maximum concurrent outbound provider requests (1–64). |
| `CORTEX_LLM_MAX_REDIRECTS` | `3` | Maximum allowed HTTP redirect hops (1–10). |
| `CORTEX_LLM_MAX_RESPONSE_BODY_BYTES` | `4194304` (4MB) | Maximum accepted response payload size (up to 64MB). |
| `CORTEX_LLM_MAX_ERROR_BODY_BYTES` | `4096` (4KB) | Maximum error response payload retained for diagnostics. |
| `CORTEX_LLM_TIMEOUT` | `45s` | Outbound request timeout (max 5m). |
| `CORTEX_LLM_CA_FILE` | `""` | Path to custom PEM CA root certificates for enterprise TLS proxies. |

### External Vector Adapters (Server-Only)

| Configuration Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `server.provider.vector` | `CORTEX_SERVER_PROVIDER_VECTOR` | `none` | Vector provider: `none` (FTS5/BM25 only), `pgvector`, `qdrant`. |
| `vector.pgvector.dsn` | `CORTEX_VECTOR_PGVECTOR_DSN` | *(None)* | Runtime connection string for pgvector table. |
| `vector.pgvector.schema` | `CORTEX_VECTOR_PGVECTOR_SCHEMA` | `cortex_vector` | PostgreSQL schema name for vector storage. |
| `vector.pgvector.table` | `CORTEX_VECTOR_PGVECTOR_TABLE` | `embeddings` | Table storing dense vector embeddings. |
| `vector.pgvector.index_type`| `CORTEX_VECTOR_PGVECTOR_INDEX_TYPE` | `hnsw` | Vector index type: `hnsw` or `ivfflat`. |
| `vector.qdrant.host` | `CORTEX_VECTOR_QDRANT_HOST` | `localhost` | Qdrant gRPC/HTTP host address. |
| `vector.qdrant.port` | `CORTEX_VECTOR_QDRANT_PORT` | `6334` | Qdrant port. |
| `vector.qdrant.collection` | `CORTEX_VECTOR_QDRANT_COLLECTION` | `cortex` | Target Qdrant collection name. |
| `vector.qdrant.api_key` | `CORTEX_VECTOR_QDRANT_API_KEY` | `""` | Qdrant API key for authenticated cloud/cluster deployments. |

### Local Client, Sync Replication & Remote MCP

| Configuration Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `database.path` | `CORTEX_DATABASE_PATH` | `~/.cortex/cortex.db` | Local SQLite database file path. |
| `sync.enabled` | `CORTEX_SYNC_ENABLED` | `false` | Enables background bidirectional SQLite-to-Server replication. |
| `sync.url` | `CORTEX_SYNC_URL` | `""` | Cortex Server base URL (strictly HTTPS off-loopback). |
| `sync.token_env` | `CORTEX_SYNC_TOKEN_ENV` | `CORTEX_REMOTE_TOKEN` | Name of environment variable holding the replication Bearer token. |
| `sync.interval` | `CORTEX_SYNC_INTERVAL` | `30s` | Background replication synchronization frequency. |
| `mcp.remote.enabled` | `CORTEX_MCP_REMOTE_ENABLED` | `false` | Proxies local stdio MCP commands to a remote Streamable HTTP server. |
| `mcp.remote.url` | `CORTEX_MCP_REMOTE_URL` | `""` | Remote MCP endpoint URL (including `/mcp`). |
| `mcp.remote.token_env` | `CORTEX_MCP_REMOTE_TOKEN_ENV` | `CORTEX_REMOTE_TOKEN` | Name of environment variable containing the remote MCP Bearer token. |

---

## 4. Bearer Transport Policy

Every remote destination transmitting credentials (`sync.url`, `mcp.remote.url`, `server.llm.base_url`) enforces strict transport security (`internal/transportpolicy`):

1. **HTTPS Required Off-Loopback**: Non-loopback endpoints must use HTTPS. Any plain HTTP connection to a public/remote address is rejected at startup before credentials can be sent.
2. **Strict Loopback Exceptions**: Plain HTTP is permitted only for:
   * IPv4 loopback literal (`127.0.0.0/8`)
   * IPv6 loopback literal (`[::1]`)
   * Exact hostname `localhost`
3. **Downgrade Rejection**: HTTP redirects are never followed if they downgrade an HTTPS connection to plain HTTP or change the origin (scheme + host + port).

---

## 5. Troubleshooting & Diagnostics

* **Configuration validation**: Run `cortex doctor` to inspect configuration health, database compatibility, and vector indexing status.
* **Inspect loaded path**: Run `cortex config path` to identify which configuration file is currently being read.
* **Overrides not reflected**: Remember that `CORTEX_*` environment variables take precedence over settings saved in `cortex.yaml`. Check running process environment variables.
* **Port binding issues**: Ensure you use `CORTEX_HTTP_PORT` (e.g. `7438`). Legacy `CORTEX_PORT` is rejected to prevent configuration ambiguity.

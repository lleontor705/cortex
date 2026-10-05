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

* **Environment Variable Matching**: Viper automatically translates dot-notated paths to uppercase snake_case prefixed by `CORTEX_` (e.g. `llm.provider` $\rightarrow$ `CORTEX_LLM_PROVIDER`, `http.port` $\rightarrow$ `CORTEX_HTTP_PORT`).
* **Environment Overrides**: An environment variable always overrides the corresponding value in the configuration file or TUI state.
* **Zero-Bloat Model**: The configuration manager automatically trims default and empty sections when writing to disk, keeping local configuration files clean (typically under 15 lines) and preventing serialization of internal SQLite pragmas or server-specific storage parameters.

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
| `server.storage.dsn` | `CORTEX_SERVER_STORAGE_DSN` | *(None)* | Non-privileged runtime PostgreSQL DSN (`NOSUPERUSER`, `NOBYPASSRLS`). |
| `server.storage.migration_dsn` | `CORTEX_SERVER_STORAGE_MIGRATION_DSN` | *(None)* | Privileged migration DSN. Applied during startup preflight and closed immediately. |
| `server.storage.max_conns` | `CORTEX_SERVER_STORAGE_MAX_CONNS` | `10` | Maximum open database pool connections. |

### Single-Tenant Server Identity & Privileges

Server mode (`cortex --mode server`) runs one self-hosted tenant. Every request is
authenticated as a single synthetic constant principal assembled from configuration,
and tenant/workspace scope is a configuration constant that client input can never
override.

| Configuration Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `server.bootstrap_development` | `CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT` | `false` | Development-only mode that reuses the runtime DSN when a dedicated migration role is not separated. |
| `server.tenant_id` | `CORTEX_SERVER_TENANT_ID` | *(None)* | Configured tenant UUID (UUIDv4) bound to the synthetic principal. Required: startup fails closed when empty. |
| `server.workspace_id` | `CORTEX_SERVER_WORKSPACE_ID` | *(None)* | Configured default workspace UUID (UUIDv4) bound to the synthetic principal. Required: startup fails closed when empty. |
| `server.principal_subject` | `CORTEX_SERVER_PRINCIPAL_SUBJECT` | *(None)* | Subject UUID of the synthetic constant principal bound to every authenticated request. Required: startup fails closed when empty. |
| `server.roles` | `CORTEX_SERVER_ROLES` | `[]` | Comma-separated roles for the configured principal (the synthetic server principal carries `owner`). |
| `server.scopes` | `CORTEX_SERVER_SCOPES` | `[]` | Comma-separated authorized scopes (e.g. `workspaces:read,workspaces:write`). |
| `server.grant_digest` | `CORTEX_SERVER_GRANT_DIGEST` | `""` | **Deprecated.** Retained for backward compatibility only; grant integrity is calculated dynamically by PostgreSQL in `cortex_bootstrap_service_principal`. |
| `server.grant_version` | `CORTEX_SERVER_GRANT_VERSION` | `0` | **Deprecated.** Retained for backward compatibility only; the grant version is provisioned dynamically by PostgreSQL. |

A configuration file that still carries the removed `multi_tenant` key loads without
error: the key is unknown to the schema, no code path branches on it, and it is dropped
by the zero-bloat writer on the next save.

### HTTP API, MCP Transport & Logging

| Configuration Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `http.enabled` | `CORTEX_HTTP_ENABLED` | `true` | Enables HTTP REST API (`/api/*`) and Streamable HTTP MCP (`/mcp`). |
| `http.host` | `CORTEX_HTTP_HOST` | `localhost` | Network interface to bind (`0.0.0.0` for containers/all interfaces). |
| `http.port` | `CORTEX_HTTP_PORT` | `7438` | Port to listen on. *(Note: `CORTEX_PORT` is intentionally rejected).* |
| `http.token` | `CORTEX_HTTP_TOKEN` | `""` | Static Bearer token. Authenticates local HTTP/MCP and, in server mode, is the single credential verified on every request before the synthetic constant principal is bound. Required for non-loopback bindings. |
| `http.allowed_origins` | `CORTEX_HTTP_ALLOWED_ORIGINS` | `[]` | Comma-separated list of allowed CORS browser origins. |
| `logging.level` | `CORTEX_LOGGING_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error`. |
| `logging.format` | `CORTEX_LOGGING_FORMAT` | `json` | Log output format: `json`, `text`, `plain`. |

### Embedded Web UI (`web.*`)

Cortex serves the compiled Next.js operator UI from the same binary and the same
HTTP listener as the API: there is no separate web container and no second port.
The `web.*` namespace overrides that surface; when a value is unset it inherits the
HTTP listener (`http.host` / `http.port`), so a default configuration mounts the UI
wherever the API listens. See [embedded-web.md](embedded-web.md) for the key
lifecycle, at-rest format, and rotation semantics.

| Configuration Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `web.enabled` | `CORTEX_WEB_ENABLED` | `true` | Enable the embedded web UI. Only an explicit `false` disables it, and the code-owned default is never written back to a saved config file. |
| `web.host` | `CORTEX_WEB_HOST` | inherits `http.host` (`localhost`) | Listener host for the web surface. Empty inherits `http.host`; accepts an IP literal or bare hostname (no scheme, path, userinfo, or port). |
| `web.port` | `CORTEX_WEB_PORT` | inherits `http.port` (`7438`) | Listener port for the web surface. `0` inherits `http.port`; valid range `1`–`65535`. |
| `web.key_file` | `CORTEX_WEB_KEY_FILE` | `~/.cortex/web.key` | Path to the embedded web access-key store. Empty or blank resolves to the default inside the Cortex config directory. |

The web access key is an independent `ctx_`-prefixed credential: it is never used
as, defaulted to, or derived from `http.token`, and the `/api/*` endpoints never
accept it. On first boot Cortex mints the key and prints the plaintext exactly once;
afterwards the plaintext is never shown again. Manage it with
`cortex web key show` and `cortex web key regenerate` (both accept
`--key-file PATH`).

### Standardized AI Configuration (Strict Separation)

Cortex strictly separates configuration and API credentials between **Embeddings** and **LLM** so they never unintentionally cross-pollinate or override each other:
1. **Embedding Tier**: `CORTEX_EMBEDDING_*` / `CORTEX_EMBEDDING_API_KEY` for semantic search and vector indexing.
2. **LLM Tier**: `CORTEX_LLM_*` / `CORTEX_LLM_API_KEY` for agent reasoning, synthesis, and extraction.
3. **Strict Decoupling**: Third-party vendor variables (such as `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`), legacy variables (such as `CORTEX_SEARCH_EMBEDDING_*`), and unified `CORTEX_AI_*` variables are **not accepted**. Each subsystem requires its own explicit Cortex credential.

### Embeddings (Semantic Search & Vector Indexing)

| Configuration Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `embedding.provider` / `search.embedding_provider` | `CORTEX_EMBEDDING_PROVIDER` | *(Unset ⇒ embeddings disabled)* | Embedding provider preset: `none` (disabled), `ollama`, `openai`, `openai-compatible`. Any other value is rejected at configuration load. |
| `embedding.model` / `search.embedding_model` | `CORTEX_EMBEDDING_MODEL` | `""` (provider default) | Embedding model identifier. Provider defaults: `nomic-embed-text` (`ollama`), `text-embedding-3-small` (`openai` / `openai-compatible`). |
| `embedding.base_url` / `search.embedding_base_url` | `CORTEX_EMBEDDING_BASE_URL` | `""` (provider default) | Endpoint override. Defaults: `http://localhost:11434` (`ollama`), `https://api.openai.com/v1` (`openai`); **explicit URL required** for `openai-compatible`. |
| *(Credential)* | `CORTEX_EMBEDDING_API_KEY` | `""` | **Embedding API credential, environment-only.** Never read from YAML/JSON/TOML files. |
| `search.ollama_auto_start` | `CORTEX_SEARCH_OLLAMA_AUTO_START` | `false` | Automatically starts local Ollama daemon when needed. |
| `search.fusion_k` | `CORTEX_SEARCH_FUSION_K` | `60` | Reciprocal Rank Fusion (RRF) rank constant. |

When no embedding provider is configured (unset or `none`), no embedder is
constructed: search runs on FTS5/BM25 alone, the embedding outbox worker is not
started, and the local save path stays byte-for-byte identical to a
zero-embedding build.

### Embedding providers

`search.embedding_provider` accepts exactly four presets. Unknown values fail
closed at load time (`invalid search.embedding_provider`) instead of silently
disabling embeddings.

| Provider | Modes | Default base URL | Default model | Credential |
| :--- | :--- | :--- | :--- | :--- |
| `none` / *(unset)* | local, server | — | — | — |
| `ollama` | local, server | `http://localhost:11434` | `nomic-embed-text` (768 dims) | none |
| `openai` | local, server | `https://api.openai.com/v1` | `text-embedding-3-small` (1536 dims) | `CORTEX_EMBEDDING_API_KEY` |
| `openai-compatible` | **server only** | *(none — explicit URL required)* | `text-embedding-3-small` unless `search.embedding_model` is set | `CORTEX_EMBEDDING_API_KEY` |

* **`ollama` (local default)** — the local-mode embedding provider: it dials the
  loopback daemon and needs no credential. `search.ollama_auto_start` can launch
  it, and `search.embedding_base_url` overrides the endpoint.
* **`openai`** — first-party OpenAI embeddings over `{base_url}/embeddings` with
  Bearer authentication; the key comes only from `CORTEX_EMBEDDING_API_KEY`.
* **`openai-compatible` (server-mode, Tier 2)** — any OpenAI-compatible
  `/embeddings` endpoint. The local composition refuses it fail-closed (no
  embedder, no outbound dial); it is constructed only in server mode through the
  outbound policy allowlist. It **requires an explicit `search.embedding_base_url`**
  because a generic compatible endpoint has no safe default host — a missing URL
  fails at construction (`embedding: openai-compatible requires an explicit base
  URL`).
* **`none`** — embeddings disabled; native BM25/FTS5 only.
* API keys are resolved exclusively from `CORTEX_EMBEDDING_API_KEY` at runtime
  and must never appear in a configuration file.

Server-mode construction additionally approves the destination through the
outbound policy: HTTPS is required off-loopback, the host and port are
allowlisted, and redirect hops and response size are bounded.

#### Worked example: nan.builders (OpenAI-compatible endpoint)

```yaml
search:
  embedding_provider: openai-compatible
  embedding_model: qwen3-embedding-8B
  embedding_base_url: https://api.nan.builders/v1
```

```bash
# Placeholder only — export the real credential at run time; never commit it.
export CORTEX_EMBEDDING_API_KEY="<nan-builders-api-key>"
```

The same three keys are readable/settable as `search.embedding_provider`,
`search.embedding_model`, and `search.embedding_base_url` (aliases:
`embedding.provider`, `embedding.model`, `embedding.base_url`) and are mirrored
by `CORTEX_EMBEDDING_PROVIDER`, `CORTEX_EMBEDDING_MODEL`, and
`CORTEX_EMBEDDING_BASE_URL`.

#### Vector dimensions and corpus re-ingestion

* **Dimensions are reported from live service state.** The dimension is cached
  from the first embedding response, and server ModelInfo / admin AI status read
  that live value (`liveEmbeddingDimensions`) instead of a static
  provider-to-dimension map, so status cannot drift from the endpoint. Before
  the first response, `ollama` reports `768`, `openai` reports `1536`, and
  `openai-compatible` reports no guess (`0`) precisely because an arbitrary
  endpoint has no trustworthy default.
* **Mismatched dimensions are rejected, never stored.** A vector whose length
  differs from the declared model dimension fails with
  `domain.ErrDimensionMismatch` (`vector dimension mismatch`): it is neither
  written to the vector index nor scored as zero. The embedding outbox worker
  classifies validation failures such as a dimension mismatch as terminal,
  non-retryable, and dead-letters the intent instead of consuming retries.
* **Changing the model dimension requires corpus re-ingestion.** Switching from
  a 768-dim model (`nomic-embed-text`) to a 4096-dim model
  (`qwen3-embedding-8B`) leaves existing vectors at the old dimension, so every
  observation must be re-embedded through the durable outbox worker. In the TUI,
  save the embedding configuration (a provider/model change raises the reindex
  warning) and press `x` to **Reindex all embeddings**; the worker drains the
  outbox asynchronously and reports progress.

### Reranking

Reranking reorders the fused candidate set **before** the final result limit is
applied (post-fusion, pre-limit). It is resolved once per process/store from
`search.rerank_*` and is **disabled by default**: with no rerank keys set,
`search.rerank_provider` resolves to `none`, zero rerank machinery is
constructed, and search output is byte-identical to a build without the feature.

| Configuration Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `search.rerank_provider` | `CORTEX_RERANK_PROVIDER` | `none` | Rerank preset: `none` (disabled), `late-interaction` (local), `openai-compatible` (server only). Unknown values are rejected at load. |
| `search.rerank_model` | `CORTEX_RERANK_MODEL` | `""` | Rerank model identifier (e.g. `qwen3-Reranker-8B`). |
| `search.rerank_base_url` | `CORTEX_RERANK_BASE_URL` | `""` | Endpoint base URL. **Required** when `search.rerank_provider` is `openai-compatible`, and validated as a bearer destination at load. |
| *(Credential)* | `CORTEX_RERANK_API_KEY` | `""` | **Rerank credential, environment-only.** Never read from configuration files; without it the HTTP reranker cannot be constructed. |

* **`none` (default)** — no reranker is constructed anywhere; results are
  byte-identical to the unreranked fusion order.
* **`late-interaction`** — the local, zero-network reranker: a 60% rank / 40%
  MaxSim late-interaction blend over the vectors already in the store. Valid in
  both local and server mode.
* **`openai-compatible` (server only)** — the HTTP `/v1/rerank` reranker. It
  requires an explicit `search.rerank_base_url` plus `CORTEX_RERANK_API_KEY`, and
  the destination is approved through the outbound policy. Local mode refuses to
  construct it (`rerank: openai-compatible is server-mode only`) and degrades to
  rerank-off with a warning instead of failing the query.
* **Provider budgets are enforced client-side, not assumed**: candidate
  documents are sent in batches of at most **32**, requests are paced to at most
  **60 requests/minute**, `429` responses are retried at most 3 times, redirects
  are capped at 3 hops, and responses are capped at 4 MiB.
* **Failures degrade, never corrupt**: a rerank error, a timeout, or a provider
  response that changes the candidate set keeps the unreranked fusion order (with
  a warning) and never drops or fails a search.

### LLM (Agent Reasoning, Extraction & Synthesis)

| Configuration Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `llm.provider` | `CORTEX_LLM_PROVIDER` | `openai` | Generative provider: `openai`, `anthropic`, `gemini`, `ollama`, `groq`, `deepseek`. |
| `llm.model` | `CORTEX_LLM_MODEL` | `gpt-4o-mini` | LLM model identifier. |
| `llm.base_url` | `CORTEX_LLM_BASE_URL` | `""` | Custom provider base URL. |
| *(Credential)* | `CORTEX_LLM_API_KEY` | *(Secret)* | **Standard LLM API credential** (strictly isolated; vendor keys like `OPENAI_API_KEY` are ignored). |

### Outbound Network & Security Bounds (SEC-02)

| Environment Variable | Default | Constraint / Description |
| :--- | :--- | :--- |
| `CORTEX_LLM_PROVIDER` | `""` | Preset: `openai`, `anthropic`, `google`, `gemini`, `ollama`, `generic`. |
| `CORTEX_LLM_MODEL` | `""` | Model identifier override. |
| `CORTEX_LLM_API_KEY` | `""` | Outbound authentication key. Must be explicitly provided. |
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

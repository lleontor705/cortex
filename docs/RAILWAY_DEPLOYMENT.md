# Deploying Cortex Server on Railway

This guide covers deploying **Cortex Server (single-tenant self-hosted PostgreSQL mode + Streamable MCP + embedded web UI)** to [Railway.app](https://railway.app) using the public GitHub Container Registry (`ghcr.io`) image.

---

## 1. Railway Architecture

```
┌───────────────────────────────────────────────────────────┐
│                      RAILWAY PROJECT                      │
├───────────────────────────────────────────────────────────┤
│ 1. PostgreSQL Service (Postgres 16)                       │
│    • Relational database with RLS enabled.                │
│                                                           │
│ 2. Cortex Server Service (ghcr.io/lleontor705/cortex)     │
│    • HippoRAG + Adaptive-RAG + LightRAG + CRAG.           │
│    • REST /api/*, MCP Streamable /mcp, and the embedded   │
│      web UI at / on one published port.                   │
│                                                           │
│ 3. Ollama Service (Optional - Internal Service)           │
│    • Private local embeddings via *.railway.internal      │
└───────────────────────────────────────────────────────────┘
```

There is no separate web service. The same container serves the UI, the API, and MCP on the port configured by `CORTEX_HTTP_PORT`, so a single public domain is enough.

---

## 2. Deploying with the Railway CLI

You can connect the service directly to the published GHCR image:

```bash
railway service source connect --image ghcr.io/lleontor705/cortex:latest --service cortex-server
```

Railway injects its own `PORT` variable, which Cortex does not read. Set `CORTEX_HTTP_PORT` explicitly and point the service's public domain at that port; both the embedded UI and the API answer on it.

---

## 3. Environment Variables on Railway

### A. Cortex Server variables

| Variable | Description / Recommendation | Example |
|---|---|---|
| `CORTEX_HTTP_PORT` | HTTP port the server listens on (UI, API, and MCP share it) | `7438` |
| `CORTEX_HTTP_HOST` | Host to bind | `0.0.0.0` |
| `CORTEX_HTTP_TOKEN` | Secret authentication token for API/MCP | `cortex_admin_secret_token_12345` |
| `CORTEX_HTTP_ALLOWED_ORIGINS` | Allowed CORS origins; the embedded UI is same-origin, so this only matters for other browser clients | `https://cortex-server-production.up.railway.app` |
| `CORTEX_SERVER_STORAGE_DRIVER` | Persistence driver | `postgres` |
| `CORTEX_SERVER_STORAGE_DSN` | DSN for the non-privileged runtime role (`cortex_app`, with RLS) | `postgresql://cortex_app:password@postgres.railway.internal:5432/railway?sslmode=require` |
| `CORTEX_SERVER_STORAGE_MIGRATION_DSN`| DSN for the privileged migration role (`cortex_migration`) | `postgresql://cortex_migration:password@postgres.railway.internal:5432/railway` |
| `CORTEX_SERVER_TENANT_ID` | Deployment constant: UUID of the single tenant | `00000000-0000-0000-0000-000000000001` |
| `CORTEX_SERVER_WORKSPACE_ID` | Deployment constant: UUID of the default workspace | `00000000-0000-0000-0000-000000000002` |
| `CORTEX_SERVER_PRINCIPAL_SUBJECT` | Deployment constant: subject of the bearer's synthetic principal | `00000000-0000-0000-0000-000000000003` |
| `CORTEX_EMBEDDING_PROVIDER` | Embeddings provider | `ollama` / `openai` / `gemini` / `none` |
| `CORTEX_EMBEDDING_MODEL` | Embeddings model | `qwen3-embedding:4b` / `text-embedding-3-small` |
| `CORTEX_EMBEDDING_BASE_URL` | Embeddings provider URL | `http://ollama.railway.internal:11434` |
| `CORTEX_SERVER_RAILWAY_INTERNAL_EMBEDDING_HOST` | Authorized private hostname for internal HTTP | `ollama.railway.internal` |
| `CORTEX_LLM_PROVIDER` | Server LLM provider | `openai` / `anthropic` / `google` |
| `CORTEX_LLM_MODEL` | LLM model | `gpt-4o-mini` / `claude-3-5-sonnet` |
| `CORTEX_EMBEDDING_API_KEY` | Credential for the embeddings provider | `sk-...` |
| `CORTEX_LLM_BASE_URL` | LLM provider base URL (optional) | `https://api.openai.com/v1` |
| `CORTEX_LLM_API_KEY` | Credential for the LLM provider | `sk-...` |

### B. Web UI access key

The embedded UI needs no build-time variables. It resolves its API origin at runtime (the Go server serves `/config.js`), so the UI is same-origin by default.

On first boot the server mints one web access key and prints it exactly once in the deploy logs:

```text
cortex: web https://<domain>/
cortex: web key file /home/cortex/.cortex/web.key
cortex: web access key ctx_...
```

Open the service's public domain and paste that key once to unlock the surface. The key is independent of `CORTEX_HTTP_TOKEN`: it never authenticates `/api/*`, and the API bearer never unlocks the UI. See [embedded-web.md](embedded-web.md) for the full lifecycle.

Attach a Railway volume mounted at `/home/cortex/.cortex` so `web.key` survives redeploys. Without a volume, each fresh filesystem mints a new key (printed again in the logs) and browsers must paste it again.

---

## 4. Connecting AI Assistants to Cortex on Railway

Once your service is deployed on Railway, you can connect your AI assistants:

### A. Claude Desktop / Claude Code (`claude.json` / `config.json`)
```json
{
  "mcpServers": {
    "cortex-remote": {
      "url": "https://cortex-server.up.railway.app/mcp",
      "headers": {
        "Authorization": "Bearer cortex_admin_secret_token_12345"
      }
    }
  }
}
```

### B. Cursor / Windsurf / OpenCode (`opencode.json`)
```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "cortex": {
      "type": "remote",
      "url": "https://cortex-server.up.railway.app/mcp",
      "headers": {
        "Authorization": "Bearer cortex_admin_secret_token_12345"
      }
    }
  }
}
```

---

## 5. Diagnostics and Monitoring

### CLI diagnostics
```bash
# Diagnose the live Railway server
CORTEX_HTTP_TOKEN="cortex_admin_secret_token_12345" cortex doctor --server https://cortex-server.up.railway.app
```

### Health and UI URLs
- `GET https://cortex-server.up.railway.app/health` (returns `{"status":"ok"}`).
- `https://cortex-server.up.railway.app/` serves the embedded web UI shell; paste the web access key to unlock it.

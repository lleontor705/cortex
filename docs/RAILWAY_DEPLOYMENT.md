# Despliegue de Cortex Server en Railway

Esta guía detalla el despliegue de **Cortex Server (Modo PostgreSQL Multi-Tenant + Streamable MCP + Next.js Web Dashboard)** en [Railway.app](https://railway.app) utilizando las imágenes Docker públicas de GitHub Container Registry (`ghcr.io`).

---

## 1. Arquitectura en Railway

```
┌────────────────────────────────────────────────────────┐
│                   PROYECTO EN RAILWAY                  │
├────────────────────────────────────────────────────────┤
│ 1. Servicio PostgreSQL (Postgres 16)                   │
│    • Base de datos relacional con RLS activado.        │
│                                                        │
│ 2. Servicio Cortex Server (ghcr.io/lleontor705/cortex) │
│    • HippoRAG + Adaptive-RAG + LightRAG + CRAG.        │
│    • Endpoints: REST /api/* y MCP Streamable /mcp.     │
│                                                        │
│ 3. Servicio Cortex Web (ghcr.io/lleontor705/cortex-web)│
│    • Control Room Next.js 15, Visualizador Sigma.js.   │
│                                                        │
│ 4. Servicio Ollama (Opcional - Servicio Interno)       │
│    • Embeddings locales privados vía *.railway.internal│
└────────────────────────────────────────────────────────┘
```

---

## 2. Despliegue con Railway CLI

Puedes conectar tus servicios directamente a las imágenes publicadas en GHCR:

```bash
# 1. Conectar Cortex Server a la imagen pública
railway service source connect --image ghcr.io/lleontor705/cortex:latest --service cortex-server

# 2. Conectar Cortex Web Control Room a la imagen pública
railway service source connect --image ghcr.io/lleontor705/cortex-web:latest --service cortex-web
```

---

## 3. Variables de Entorno en Railway

### A. Variables de Cortex Server

| Variable | Descripción / Recomendación | Ejemplo |
|---|---|---|
| `PORT` / `CORTEX_HTTP_PORT` | Puerto HTTP asignado por Railway | `7438` |
| `CORTEX_HTTP_HOST` | Host para escuchar peticiones | `0.0.0.0` |
| `CORTEX_HTTP_TOKEN` | Token secreto de autenticación API/MCP | `cortex_admin_secret_token_12345` |
| `CORTEX_HTTP_ALLOWED_ORIGINS` | Orígenes CORS permitidos | `https://cortex-web.up.railway.app` |
| `CORTEX_SERVER_STORAGE_DRIVER` | Driver de persistencia | `postgres` |
| `CORTEX_SERVER_STORAGE_DSN` | DSN del rol runtime sin privilegios (`cortex_app`, con RLS) | `postgresql://cortex_app:password@postgres.railway.internal:5432/railway?sslmode=require` |
| `CORTEX_SERVER_STORAGE_MIGRATION_DSN`| DSN del rol privilegiado para migraciones (`cortex_migration`) | `postgresql://cortex_migration:password@postgres.railway.internal:5432/railway` |
| `CORTEX_SERVER_MULTI_TENANT` | Activa aislamiento multi-tenant | `true` |
| `CORTEX_SERVER_TENANT_ID` | UUID del tenant principal | `00000000-0000-0000-0000-000000000001` |
| `CORTEX_SERVER_WORKSPACE_ID` | UUID del workspace por defecto | `00000000-0000-0000-0000-000000000002` |
| `CORTEX_SERVER_PRINCIPAL_SUBJECT` | Subject del token administrador | `00000000-0000-0000-0000-000000000003` |
| `CORTEX_EMBEDDING_PROVIDER` | Proveedor de embeddings | `ollama` / `openai` / `gemini` / `none` |
| `CORTEX_EMBEDDING_MODEL` | Modelo de embeddings | `qwen3-embedding:4b` / `text-embedding-3-small` |
| `CORTEX_EMBEDDING_BASE_URL` | URL del proveedor de embeddings | `http://ollama.railway.internal:11434` |
| `CORTEX_SERVER_RAILWAY_INTERNAL_EMBEDDING_HOST` | Hostname privado autorizado para HTTP interno | `ollama.railway.internal` |
| `CORTEX_LLM_PROVIDER` | Proveedor de LLM del servidor | `openai` / `anthropic` / `google` |
| `CORTEX_LLM_MODEL` | Modelo de LLM | `gpt-4o-mini` / `claude-3-5-sonnet` |
| `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `GEMINI_API_KEY` | Llave de API del proveedor | `sk-...` |

### B. Variables de Cortex Web

| Variable | Descripción | Ejemplo |
|---|---|---|
| `PORT` | Puerto HTTP | `3000` |
| `HOSTNAME` | Host de enlace | `0.0.0.0` |
| `NEXT_PUBLIC_CORTEX_SERVER_URL` | URL pública del servidor Cortex | `https://cortex-server.up.railway.app` |
| `NEXT_PUBLIC_CORTEX_MANAGED_ENDPOINT` | Bloquea la URL al endpoint administrado | `true` |

---

## 4. Conexión de Asistentes de IA a Cortex en Railway

Una vez desplegado tu servicio en Railway, puedes conectar tus asistentes de IA:

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

## 5. Diagnóstico y Monitoreo

### Diagnóstico con CLI
```bash
# Diagnosticar el servidor de Railway en vivo
CORTEX_HTTP_TOKEN="cortex_admin_secret_token_12345" cortex doctor --server https://cortex-server.up.railway.app
```

### Healthcheck URL
- `GET https://cortex-server.up.railway.app/health` (Retorna `{"status":"ok"}`).
- `GET https://cortex-web.up.railway.app/health` (Retorna `{"status":"ok"}`).

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
railway service source connect --image ghcr.io/lleontor705/cortex:latest --service cortex-server-clean

# 2. Conectar Cortex Web Control Room a la imagen pública
railway service source connect --image ghcr.io/lleontor705/cortex-web:latest --service cortex-web-clean
```

---

## 3. Variables de Entorno en Railway

### A. Variables de Cortex Server (`cortex-server-clean`)

| Variable | Descripción / Recomendación | Ejemplo |
|---|---|---|
| `PORT` / `CORTEX_HTTP_PORT` | Puerto HTTP asignado por Railway | `7438` |
| `CORTEX_HTTP_HOST` | Host para escuchar peticiones | `0.0.0.0` |
| `CORTEX_HTTP_TOKEN` | Token secreto de autenticación API/MCP | `cortex_admin_...` |
| `CORTEX_HTTP_ALLOWED_ORIGINS` | Orígenes CORS permitidos | `https://cortex-web-clean-production.up.railway.app` |
| `CORTEX_SERVER_STORAGE_DRIVER` | Driver de persistencia | `postgres` |
| `CORTEX_SERVER_STORAGE_DSN` | DSN del rol runtime sin privilegios (`cortex_runtime`, con RLS) | `postgresql://cortex_runtime:...@postgres-ygek.railway.internal:5432/railway?sslmode=require` |
| `CORTEX_SERVER_STORAGE_MIGRATION_DSN`| DSN del rol privilegiado para migraciones (`postgres`) | `postgresql://postgres:...@postgres-ygek.railway.internal:5432/railway` |
| `CORTEX_SERVER_MULTI_TENANT` | Activa aislamiento multi-tenant | `true` |
| `CORTEX_SERVER_TENANT_ID` | UUID del tenant principal | `1e6b5778-c113-462d-9697-f84348665f9c` |
| `CORTEX_SERVER_WORKSPACE_ID` | UUID del workspace por defecto | `20df6613-f69b-4e37-bdc3-d21a85ef2046` |
| `CORTEX_SERVER_PRINCIPAL_SUBJECT` | Subject del token administrador | `186e99b0-b568-4450-916c-a021d782c1e5` |
| `CORTEX_EMBEDDING_PROVIDER` | Proveedor de embeddings | `ollama` / `openai` / `gemini` / `none` |
| `CORTEX_EMBEDDING_MODEL` | Modelo de embeddings | `qwen3-embedding:4b` |
| `CORTEX_EMBEDDING_BASE_URL` | URL del proveedor de embeddings | `http://ollama.railway.internal:11434` |
| `CORTEX_SERVER_RAILWAY_INTERNAL_EMBEDDING_HOST` | Hostname privado autorizado para HTTP interno | `ollama.railway.internal` |
| `CORTEX_LLM_PROVIDER` | Proveedor de LLM del servidor | `google` / `openai` / `anthropic` |
| `CORTEX_LLM_MODEL` | Modelo de LLM | `gemini-3.1-flash-lite` |
| `GEMINI_API_KEY` / `OPENAI_API_KEY` | Llave de API del proveedor | `AIzaSy...` |

### B. Variables de Cortex Web (`cortex-web-clean`)

| Variable | Descripción | Ejemplo |
|---|---|---|
| `PORT` | Puerto HTTP | `3000` |
| `HOSTNAME` | Host de enlace | `0.0.0.0` |
| `NEXT_PUBLIC_CORTEX_SERVER_URL` | URL pública del servidor Cortex | `https://cortex-server-clean-production.up.railway.app` |
| `NEXT_PUBLIC_CORTEX_MANAGED_ENDPOINT` | Bloquea la URL al endpoint administrado | `true` |

---

## 4. Conexión de Asistentes de IA a Cortex en Railway

Una vez desplegado tu servicio en Railway, puedes conectar tus asistentes de IA:

### A. Claude Desktop / Claude Code (`claude.json` / `config.json`)
```json
{
  "mcpServers": {
    "cortex-remote": {
      "url": "https://cortex-server-clean-production.up.railway.app/mcp",
      "headers": {
        "Authorization": "Bearer cortex_admin_..."
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
      "url": "https://cortex-server-clean-production.up.railway.app/mcp",
      "headers": {
        "Authorization": "Bearer cortex_admin_..."
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
CORTEX_HTTP_TOKEN="cortex_admin_..." cortex doctor --server https://cortex-server-clean-production.up.railway.app
```

### Healthcheck URL
- `GET https://cortex-server-clean-production.up.railway.app/health` (Retorna `{"status":"ok"}`).
- `GET https://cortex-web-clean-production.up.railway.app/health` (Retorna `{"status":"ok"}`).

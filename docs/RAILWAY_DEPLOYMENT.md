# Despliegue de Cortex Server en Railway

Esta guía detalla el despliegue de **Cortex Server (Modo PostgreSQL Multi-Tenant + Streamable MCP + Next.js Web Dashboard)** en [Railway.app](https://railway.app).

---

## 1. Arquitectura en Railway

```
┌────────────────────────────────────────────────────────┐
│                   PROYECTO EN RAILWAY                  │
├────────────────────────────────────────────────────────┤
│ 1. Servicio PostgreSQL (Database Postgres 16)         │
│    • Base de datos relacional con RLS activado.        │
│                                                        │
│ 2. Servicio Cortex Server (Backend Go Zero-CGO)        │
│    • HippoRAG + Adaptive-RAG + LightRAG + CRAG.        │
│    • Endpoints: REST /api/* y MCP Streamable /mcp.     │
│                                                        │
│ 3. Servicio Cortex Web (Next.js Dashboard - Opcional) │
│    • Control Room, Visualizador Sigma.js, Pipeline RAG │
└────────────────────────────────────────────────────────┘
```

---

## 2. Variables de Entorno Requeridas en Railway

Configura las siguientes variables en el panel de **Cortex Server**:

| Variable | Valor Recomendado / Descripción | Ejemplo |
|---|---|---|
| `PORT` | Puerto HTTP asignado por Railway | `7438` |
| `CORTEX_HTTP_HOST` | Host para escuchar peticiones | `0.0.0.0` |
| `CORTEX_HTTP_PORT` | Puerto de Cortex | `${{PORT}}` o `7438` |
| `CORTEX_HTTP_TOKEN` | Token secreto de autenticación API/MCP | `cortex_live_sec_...` |
| `CORTEX_SERVER_STORAGE_DRIVER` | Driver de persistencia | `postgres` |
| `CORTEX_SERVER_STORAGE_DSN` | Conexión Postgres de la aplicación | `${{Postgres.DATABASE_URL}}` |
| `CORTEX_SERVER_STORAGE_MIGRATION_DSN`| Conexión Postgres para migraciones | `${{Postgres.DATABASE_URL}}` |
| `CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT` | Auto-creación de tenant inicial | `true` |
| `CORTEX_SERVER_TENANT_ID` | UUID del tenant principal | `00000000-0000-0000-0000-000000000001` |
| `CORTEX_SERVER_WORKSPACE_ID` | UUID del workspace por defecto | `00000000-0000-0000-0000-000000000002` |
| `CORTEX_SERVER_PRINCIPAL_SUBJECT` | Subject del token administrador | `00000000-0000-0000-0000-000000000003` |
| `CORTEX_SERVER_GRANT_DIGEST` | Firma de autorización | `railway-prod-grant` |
| `CORTEX_SERVER_GRANT_VERSION` | Versión de esquema de permisos | `1` |
| `CORTEX_AI_PROVIDER` | Proveedor de embeddings (opcional) | `ollama` / `openai` / `openrouter` |
| `CORTEX_AI_BASE_URL` | URL del proveedor de AI | `https://api.openai.com/v1` |
| `CORTEX_AI_API_KEY` | API Key de OpenAI / OpenRouter | `sk-...` |

---

## 3. Conexión de Agentes de IA a Cortex en Railway

Una vez desplegado tu servicio en `https://cortex-production.up.railway.app`, puedes conectar tus asistentes de IA:

### A. Claude Desktop / Claude Code (`claude.json` / `config.json`)
```json
{
  "mcpServers": {
    "cortex-remote": {
      "url": "https://cortex-production.up.railway.app/mcp",
      "headers": {
        "Authorization": "Bearer cortex_live_sec_..."
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
      "url": "https://cortex-production.up.railway.app/mcp",
      "headers": {
        "Authorization": "Bearer cortex_live_sec_..."
      }
    }
  }
}
```

---

## 4. Healthcheck y Monitoreo en Railway

- **Healthcheck URL**: `GET /health` (Retorna `{"status":"ok"}` en $< 1\text{ms}$).
- **Diagnóstico RAG**: `GET /api/rag/stats` (Requiere `Authorization: Bearer <TOKEN>`).
- **Analítica de Grafos**: `GET /api/graph/analytics` (Retorna métricas de modularidad, god nodes y HippoRAG).

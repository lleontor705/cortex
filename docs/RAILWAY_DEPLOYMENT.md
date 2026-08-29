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
| `CORTEX_SERVER_STORAGE_DSN` | DSN del rol runtime sin privilegios (`cortex_app`, sin `BYPASSRLS`) | Secret Reference con el DSN de `cortex_app` |
| `CORTEX_SERVER_STORAGE_MIGRATION_DSN`| DSN del rol privilegiado y distinto (`cortex_migration`) | Secret Reference con el DSN de `cortex_migration` |
| `CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT` | Fallback de DSN exclusivamente local | `false` |
| `CORTEX_SERVER_TENANT_ID` | UUID del tenant principal | `00000000-0000-0000-0000-000000000001` |
| `CORTEX_SERVER_WORKSPACE_ID` | UUID del workspace por defecto | `00000000-0000-0000-0000-000000000002` |
| `CORTEX_SERVER_PRINCIPAL_SUBJECT` | Subject del token administrador | `00000000-0000-0000-0000-000000000003` |
| `CORTEX_SERVER_GRANT_DIGEST` | Firma de autorización | `railway-prod-grant` |
| `CORTEX_SERVER_GRANT_VERSION` | Versión de esquema de permisos | `1` |
| `CORTEX_AI_PROVIDER` | Proveedor de embeddings (opcional) | `ollama` / `openai` / `openrouter` |
| `CORTEX_AI_BASE_URL` | URL del proveedor de AI | `https://api.openai.com/v1` |
| `CORTEX_AI_API_KEY` | API Key de OpenAI / OpenRouter | `sk-...` |

No asignes `${{Postgres.DATABASE_URL}}` a ambas variables: Cortex rechaza en producción dos DSNs que resuelvan al mismo rol, antes de conectarse o migrar. Aprovisiona `cortex_app` y `cortex_migration` con `scripts/postgres/bootstrap-authz.sql`, guarda cada DSN como un secreto independiente de Railway y permite que ambos apunten al mismo host/base de datos. El proceso usa el DSN de migración solo durante migraciones y reconciliación de bootstrap, cierra ese handle y mantiene el pool de servicio con `cortex_app`.

`CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT=true` permite omitir el DSN de migración y reutilizar el runtime únicamente para entornos efímeros de desarrollo. No lo uses en un despliegue público. Separar la migración en un job one-shot todavía es una mejora pendiente y no forma parte de este flujo.

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

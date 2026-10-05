# Despliegue de Cortex Server en Railway

Esta guía cubre el despliegue de **Cortex Server (modo PostgreSQL self-hosted
single-tenant + Streamable MCP + web UI embebida)** en
[Railway.app](https://railway.app) usando la imagen del GitHub Container Registry
público (`ghcr.io`).

---

## 1. Arquitectura en Railway

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

No hay un servicio web separado. El mismo contenedor sirve la UI, la API y MCP en
el puerto configurado por `CORTEX_HTTP_PORT`, así que un único dominio público
es suficiente.

---

## 2. Despliegue con la CLI de Railway

Puedes conectar el servicio directamente a la imagen publicada de GHCR:

```bash
railway service source connect --image ghcr.io/lleontor705/cortex:latest --service cortex-server
```

Railway inyecta su propia variable `PORT`, que Cortex no lee. Configura
`CORTEX_HTTP_PORT` explícitamente y apunta el dominio público del servicio a ese
puerto; tanto la UI embebida como la API responden en él.

---

## 3. Variables de entorno en Railway

### A. Variables del Cortex Server

| Variable | Descripción / Recomendación | Ejemplo |
|---|---|---|
| `CORTEX_HTTP_PORT` | Puerto HTTP en el que escucha el servidor (UI, API y MCP lo comparten) | `7438` |
| `CORTEX_HTTP_HOST` | Host al que hacer bind | `0.0.0.0` |
| `CORTEX_HTTP_TOKEN` | Token de autenticación secreto para API/MCP | `cortex_admin_secret_token_12345` |
| `CORTEX_HTTP_ALLOWED_ORIGINS` | Orígenes CORS permitidos; la UI embebida es same-origin, así que solo importa para otros clientes de navegador | `https://cortex-server-production.up.railway.app` |
| `CORTEX_SERVER_STORAGE_DRIVER` | Driver de persistencia | `postgres` |
| `CORTEX_SERVER_STORAGE_DSN` | DSN para el rol de runtime no privilegiado (`cortex_app`, con RLS) | `postgresql://cortex_app:password@postgres.railway.internal:5432/railway?sslmode=require` |
| `CORTEX_SERVER_STORAGE_MIGRATION_DSN`| DSN para el rol de migración privilegiado (`cortex_migration`) | `postgresql://cortex_migration:password@postgres.railway.internal:5432/railway` |
| `CORTEX_SERVER_TENANT_ID` | Constante de despliegue: UUID del tenant único | `00000000-0000-0000-0000-000000000001` |
| `CORTEX_SERVER_WORKSPACE_ID` | Constante de despliegue: UUID del workspace por defecto | `00000000-0000-0000-0000-000000000002` |
| `CORTEX_SERVER_PRINCIPAL_SUBJECT` | Constante de despliegue: subject del principal sintético del bearer | `00000000-0000-0000-0000-000000000003` |
| `CORTEX_EMBEDDING_PROVIDER` | Proveedor de embeddings | `ollama` / `openai` / `openai-compatible` / `none` |
| `CORTEX_EMBEDDING_MODEL` | Modelo de embeddings | `qwen3-embedding:4b` / `text-embedding-3-small` |
| `CORTEX_EMBEDDING_BASE_URL` | URL del proveedor de embeddings | `http://ollama.railway.internal:11434` |
| `CORTEX_SERVER_RAILWAY_INTERNAL_EMBEDDING_HOST` | Hostname privado autorizado para HTTP interno | `ollama.railway.internal` |
| `CORTEX_LLM_PROVIDER` | Proveedor LLM del servidor | `openai` / `anthropic` / `google` |
| `CORTEX_LLM_MODEL` | Modelo LLM | `gpt-4o-mini` / `claude-3-5-sonnet` |
| `CORTEX_EMBEDDING_API_KEY` | Credencial para el proveedor de embeddings | `sk-...` |
| `CORTEX_LLM_BASE_URL` | URL base del proveedor LLM (opcional) | `https://api.openai.com/v1` |
| `CORTEX_LLM_API_KEY` | Credencial para el proveedor LLM | `sk-...` |

### B. Clave de acceso a la web UI

La UI embebida no necesita variables en build-time. Resuelve su origen de API en
runtime (el servidor Go sirve `/config.js`), así que la UI es same-origin por
defecto.

En el primer arranque el servidor acuña una clave de acceso web y la imprime una
sola vez en los logs de deploy:

```text
cortex: web https://<domain>/
cortex: web key file /home/cortex/.cortex/web.key
cortex: web access key ctx_...
```

Abre el dominio público del servicio y pega esa clave una vez para desbloquear la
superficie. La clave es independiente de `CORTEX_HTTP_TOKEN`: nunca autentica
`/api/*`, y el bearer de la API nunca desbloquea la UI. Consulta
[embedded-web.md](embedded-web.md) para el ciclo de vida completo.

Adjunta un volumen de Railway montado en `/home/cortex/.cortex` para que `web.key`
sobreviva a los redeployes. Sin volumen, cada sistema de ficheros nuevo acuña una
nueva clave (impresa otra vez en los logs) y los navegadores deben pegarla de
nuevo.

---

## 4. Conectar asistentes de IA a Cortex en Railway

Una vez que tu servicio esté desplegado en Railway, puedes conectar tus
asistentes de IA:

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

## 5. Diagnóstico y monitorización

### Diagnóstico CLI
```bash
# Diagnose the live Railway server
CORTEX_HTTP_TOKEN="cortex_admin_secret_token_12345" cortex doctor --server https://cortex-server.up.railway.app
```

### URLs de health y UI
- `GET https://cortex-server.up.railway.app/health` (devuelve
  `{"status":"ok"}`).
- `https://cortex-server.up.railway.app/` sirve el shell de la web UI embebida;
  pega la clave de acceso web para desbloquearla.

# Deploying Cortex Server on Railway

This guide documents **exactly what is deployed in production today** — a digest-pinned Cortex Server image (`v2.4.1`) on Railway, backed by **Postgres 18** with **pgvector**, using the **Nan** gateway for LLM, embeddings, and rerank — and how to bring up the **same stack locally in one command** with Docker Compose.

> Image pin: `ghcr.io/lleontor705/cortex@sha256:ca4282e3…` (the `v2.4.1` tag resolves to this digest; service `cortex-server`). Pinning by digest makes redeployments bit-identical — resolve the full digest with
> `docker buildx imagetools inspect ghcr.io/lleontor705/cortex:v2.4.1`.

---

## 1. Railway Architecture

```
┌───────────────────────────────────────────────────────────────┐
│                       RAILWAY PROJECT                         │
├───────────────────────────────────────────────────────────────┤
│ 1. PostgreSQL Service (Postgres 18, pgvector enabled)         │
│    • Two-role model: cortex_app (NOBYPASSRLS, runtime)        │
│      / cortex_migration (BYPASSRLS, migrations + bootstrap).  │
│    • Reached over Railway private networking                  │
│      (*.railway.internal) with sslmode=require.               │
│                                                               │
│ 2. Cortex Server Service (ghcr.io/lleontor705/cortex, pinned) │
│    • REST /api/*, Streamable MCP /mcp, and the embedded web   │
│      UI at / on ONE published port: 7438.                     │
│    • Volume mounted at /home/cortex/.cortex (bootstrap state  │
│      + web access key survive redeploys).                     │
│    • Outbound AI via Nan: LLM + embeddings + rerank.          │
└───────────────────────────────────────────────────────────────┘
```

There is no separate web service: the same container serves the UI, the API, and MCP on `CORTEX_HTTP_PORT`, so a single public domain is enough.

---

## 2. The Deployed Environment Variables (service `cortex-server`)

All 23 variables below were audited against `internal/config` and the server composition (`internal/platform/server`); every one is read and valid, with one deliberate legacy no-op (marked).

| # | Variable | Deployed value | Secret? | Verdict |
|---|---|---|---|---|
| 1 | `CORTEX_SERVER_STORAGE_DRIVER` | `postgres` | no | ✅ valid (composition hard-requires `postgres`) |
| 2 | `CORTEX_SERVER_STORAGE_DSN` | `postgresql://cortex_app:<pw>@cortex-postgres.railway.internal:5432/cortex?sslmode=require` | **yes — contains password** | ✅ valid |
| 3 | `CORTEX_SERVER_STORAGE_MIGRATION_DSN` | `postgresql://cortex_migration:<pw>@cortex-postgres.railway.internal:5432/cortex?sslmode=require` | **yes — contains password** | ✅ valid (must name a *distinct* role) |
| 4 | `CORTEX_HTTP_ENABLED` | `true` | no | ✅ valid (required `true` in server mode) |
| 5 | `CORTEX_HTTP_HOST` | `0.0.0.0` | no | ✅ valid |
| 6 | `CORTEX_HTTP_PORT` | `7438` | no | ✅ valid |
| 7 | `CORTEX_SERVER_AUTO_BOOTSTRAP` | `true` | no | ✅ valid (Docker entrypoint bootstrap trigger) |
| 8 | `CORTEX_SERVER_MULTI_TENANT` | `false` | no | ⚠️ **legacy no-op** — the config struct has no `MultiTenant` field anymore; it is a tolerated unknown that is read by nothing. Kept for documentation symmetry; can be deleted. |
| 9 | `CORTEX_LLM_PROVIDER` | `openai` | no | ✅ valid enum |
| 10 | `CORTEX_LLM_BASE_URL` | `https://api.nan.builders/v1` | no | ✅ valid (HTTPS, no userinfo) |
| 11 | `CORTEX_LLM_MODEL` | `glm5.3-flash` | no | ✅ valid |
| 12 | `CORTEX_LLM_API_KEY` | `REPLACE_WITH_NAN_API_KEY` | **SECRET** | ✅ placeholder — must hold the real Nan key in Railway |
| 13 | `CORTEX_LLM_TIMEOUT` | `90s` | no | ✅ valid (≤ 5m cap) |
| 14 | `CORTEX_EMBEDDING_PROVIDER` | `openai-compatible` | no | ✅ valid enum |
| 15 | `CORTEX_EMBEDDING_BASE_URL` | `https://api.nan.builders/v1` | no | ✅ valid (required for `openai-compatible`) |
| 16 | `CORTEX_EMBEDDING_MODEL` | `qwen3-embedding` | no | ✅ valid |
| 17 | `CORTEX_EMBEDDING_API_KEY` | `REPLACE_WITH_NAN_API_KEY` | **SECRET** | ✅ placeholder |
| 18 | `CORTEX_RERANK_PROVIDER` | `openai-compatible` | no | ✅ valid enum |
| 19 | `CORTEX_RERANK_BASE_URL` | `https://api.nan.builders/v1` | no | ✅ valid (required for `openai-compatible`) |
| 20 | `CORTEX_RERANK_MODEL` | `qwen3-reranker` | no | ✅ valid |
| 21 | `CORTEX_RERANK_API_KEY` | `REPLACE_WITH_NAN_API_KEY` | **SECRET** | ✅ placeholder |
| 22 | `CORTEX_LOGGING_LEVEL` | `info` | no | ✅ valid enum |
| 23 | `CORTEX_LOGGING_FORMAT` | `json` | no | ✅ valid enum |

**Secrets summary:** the three API keys (`CORTEX_LLM_API_KEY`, `CORTEX_EMBEDDING_API_KEY`, `CORTEX_RERANK_API_KEY`) plus both DSN passwords. Everything else is non-sensitive configuration.

---

## 3. Required Variables NOT in the Deployed Set (covered automatically)

These are mandatory for a functioning server startup but are **generated on first start** by the Docker entrypoint because `CORTEX_SERVER_AUTO_BOOTSTRAP=true`; they persist in `/home/cortex/.cortex/server-bootstrap.env` on the volume:

| Variable | Requirement | How it is satisfied |
|---|---|---|
| `CORTEX_HTTP_TOKEN` | Required bearer (≥ 12 chars, no whitespace/control chars); every request is verified against it | Generated once (`cortex_admin_…`), printed **once** to deploy logs, persisted on the volume |
| `CORTEX_SERVER_TENANT_ID` | Must be a UUID | Generated UUID on first start |
| `CORTEX_SERVER_WORKSPACE_ID` | Must be a UUID | Generated UUID on first start |
| `CORTEX_SERVER_PRINCIPAL_SUBJECT` | Must be a UUID | Generated UUID on first start |

Two more implicit behaviors worth knowing:

- **Vector provider:** `CORTEX_VECTOR_PROVIDER` is intentionally unset. When any embedding provider is configured, the server auto-selects the **pgvector** adapter against the same Postgres instance (migration DSN inherited). This is why Postgres must have the pgvector extension (see the gotcha below).
- **CORS:** `CORTEX_HTTP_ALLOWED_ORIGINS` is unset; the embedded UI is same-origin, so the empty allowlist is correct for a single-domain deployment.

---

## 4. The Two-Role Postgres Model

`resolveServerDSNs` fails closed unless the runtime and migration DSNs use **distinct PostgreSQL roles**:

- **`cortex_app`** — `LOGIN`, `NOSUPERUSER`, `NOBYPASSRLS`. The request plane. RLS policies on every table restrict it to tenant-scoped rows; it only gets `EXECUTE` on the allowlisted SQL functions.
- **`cortex_migration`** — `LOGIN`, `NOSUPERUSER`, `BYPASSRLS`. Runs the embedded forward-only server migrations and the startup bootstrap reconciler (`cortex_bootstrap_service_principal`), which is `EXECUTE`-restricted to this role. It owns the database.
- **`cortex_admin`** — `NOLOGIN` group role holding limited read/execute grants.

Never point `CORTEX_SERVER_STORAGE_DSN` at the migration role: startup refuses it, and even if it were allowed, the runtime would bypass row-level security.

---

## 5. Production Gotchas (each one bit us)

1. **Postgres 18 data directory**: the official PG18 image refuses to `initdb` into the volume mount root itself. Point `PGDATA` at a **subdirectory** of the mounted volume (`PGDATA=/var/lib/postgresql/data/pgdata`). This workaround is verified in production.
2. **Postgres must include pgvector**: with any embedding provider configured, the server auto-selects the pgvector vector provider and runs `CREATE EXTENSION vector` at startup. The **stock `postgres:18` image does not ship it** and the server fails startup; Railway's managed Postgres 18 provides pgvector. Locally, use the `pgvector/pgvector:pg18` image (what the compose file uses).
3. **Port 7438**: Railway injects its own `PORT` variable, which Cortex **does not read**. Set `CORTEX_HTTP_PORT=7438` explicitly and point the service's public domain at that port. UI, `/api/*`, and `/mcp` all answer on it.
4. **One-time tenant owner bearer**: on first start the entrypoint prints
   `cortex: tenant_owner_bearer=…` **exactly once** to the deploy logs and then persists it to `/home/cortex/.cortex/server-bootstrap.env`. Copy it immediately from **Deployments → logs** of the first deploy — it authenticates `/api/*` and MCP (`Authorization: Bearer …`) and is not re-printed. Rotating it later means replacing the token in the DB or re-running bootstrap on a fresh volume.
5. **Digest-pinned image**: the service is pinned to `@sha256:ca4282e3…`, not `:latest`. Redeploys are reproducible; bumps are an explicit act.
6. **Volume is state**: `/home/cortex/.cortex` holds the bootstrap state file **and** the web access key. Attach a Railway volume there or every redeploy mints a new tenant identity and web key.

---

## 6. Web UI Access

On first boot the server also mints one **web access key** and prints it exactly once in the deploy logs:

```text
cortex: web https://<domain>/
cortex: web key file /home/cortex/.cortex/web.key
cortex: web access key ctx_...
```

Open the service's public domain and paste that key once to unlock the surface. It is independent of `CORTEX_HTTP_TOKEN`: it never authenticates `/api/*`, and the API bearer never unlocks the UI. See [embedded-web.md](embedded-web.md).

---

## 7. One-Command Local Bring-Up (Compose)

`docker/docker-compose.prod.yml` reproduces the deployed stack — cortex-server `:latest` from GHCR, pgvector-enabled Postgres 18, and the Nan provider set — with healthchecks and named volumes:

```bash
cp docker/.env.example docker/.env     # replace the three REPLACE_WITH_NAN_API_KEY placeholders
docker compose -f docker/docker-compose.prod.yml up -d

# one-time tenant owner bearer from first-start logs:
docker compose -f docker/docker-compose.prod.yml logs cortex-server | grep tenant_owner_bearer

# health:
curl http://localhost:7438/health
```

Notes:

- Compose reads `docker/.env` (the compose file's directory); every variable has a `${VAR:-default}` default matching production, so only the three secrets must be filled in.
- Inside the compose network the DSNs use `sslmode=disable` (plain docker bridge). Production uses Railway private networking with `sslmode=require`.
- `docker/postgres-init.sh` provisions the two-role model (`cortex_app` NOBYPASSRLS / `cortex_migration` BYPASSRLS / `cortex_admin` NOLOGIN) once per fresh volume and makes the migration role the database owner.
- Postgres role passwords are set by that init script at first start; changing `POSTGRES_*_PASSWORD` afterwards requires `ALTER ROLE` in the database too.

---

## 8. Connecting AI Assistants to Cortex

### A. Claude Desktop / Claude Code (`claude.json` / `config.json`)
```json
{
  "mcpServers": {
    "cortex-remote": {
      "url": "https://cortex-server.up.railway.app/mcp",
      "headers": {
        "Authorization": "Bearer <tenant_owner_bearer from deploy logs>"
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
        "Authorization": "Bearer <tenant_owner_bearer from deploy logs>"
      }
    }
  }
}
```

---

## 9. Diagnostics and Monitoring

### CLI diagnostics
```bash
CORTEX_HTTP_TOKEN="<tenant_owner_bearer>" cortex doctor --server https://cortex-server.up.railway.app
```

### Health and UI URLs
- `GET https://cortex-server.up.railway.app/health` (returns `{"status":"ok"}`).
- `https://cortex-server.up.railway.app/` serves the embedded web UI shell; paste the web access key to unlock it.

### Deployed variable audit trail
Each variable's read/validation site (for future audits):

- Storage driver/DSNs — `internal/config/config.go` (`server.storage.*` defaults + env reads) and `internal/platform/server/server.go` (`validateRuntimeConfig`, `resolveServerDSNs`, distinct-role check).
- HTTP — viper keys `http.enabled|host|port|token` in `internal/config/config.go`; port/enabled re-validated in `internal/platform/server/server.go`.
- Auto-bootstrap — `internal/platform/server/server.go` (`dockerAutoBootstrapRequested`, `bootstrapServicePrincipal`) and `docker/server-entrypoint.sh`.
- LLM — `internal/config/server.go` (`ServerLLMFromEnv`/`ValidateServerLLM`: provider enum, HTTPS/no-userinfo URL policy, 5-minute timeout cap).
- Embeddings/rerank — env mirrors in `internal/config/config.go` (`CORTEX_EMBEDDING_*`, `CORTEX_RERANK_*` + load-time provider enums and base-URL transport policy); keys resolved strictly from env in `ResolveEmbeddingAPIKey`/`ResolveRerankAPIKey`.
- Logging — level/format enums validated in `internal/config/config.go` (`validate`).

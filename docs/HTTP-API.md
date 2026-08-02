# HTTP API

## Authentication

Local HTTP protects non-loopback bindings with `http.token`. Server mode requires `Authorization: Bearer <token>` for `/api/*` and `/mcp`. `/health` is public. Browser clients must use an origin listed in `http.allowed_origins`.

## Local SQLite

Local HTTP is implemented in `internal/http`. It provides local CRUD, search, graph, scoring, temporal, prompt, project, sync, and health operations. Its integer IDs are local database identifiers.

## Server PostgreSQL

Server mode exposes only authorized operation boundaries and public UUIDs:

| Method | Route | Purpose |
|---|---|---|
| `GET` | `/health` | Database health |
| `GET` | `/api/observations` | List observations |
| `POST` | `/api/observations` | Create observation |
| `GET` | `/api/observations/{uuid}` | Read observation |
| `PUT` | `/api/observations/{uuid}` | Update observation |
| `DELETE` | `/api/observations/{uuid}` | Delete observation |
| `POST` | `/api/sessions` | Create session |
| `GET` | `/api/sessions` | List authorized sessions |
| `GET` | `/api/search` | Search observations |
| `POST` | `/api/graph/edges` | Create graph edge |
| `GET` | `/api/graph/{uuid}/related` | Related observations |
| `DELETE` | `/api/graph/edges/{uuid}` | Delete graph edge |
| `GET` | `/api/scores/{uuid}` | Importance score |
| `GET` | `/api/stats` | Workspace counters |
| `GET` | `/api/projects` | Visible project keys |
| `GET` | `/api/audit` | Admin-only audit entries |
| `POST` | `/mcp` | Streamable HTTP MCP |

Request tenant/workspace authority is never accepted from clients. Server edge metadata is limited to fields persisted by the PostgreSQL schema.

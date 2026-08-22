# HTTP API

## Authentication

Local HTTP protects non-loopback bindings with `http.token`. Server mode requires `Authorization: Bearer <token>` for `/api/*` and `/mcp`. `/health` is public. Browser clients must use an origin listed in `http.allowed_origins`.

## Local SQLite

Local HTTP is implemented in `internal/http`. Observation, prompt, and edge IDs are local integers. Session IDs are opaque strings supplied by the agent. These local identifiers are not server UUIDs.

| Method | Route | Purpose |
|---|---|---|
| `GET` | `/health` | Local process health; always public |
| `GET/POST` | `/api/sessions` | List or create local sessions |
| `GET` | `/api/sessions/{id}` | Read one session by opaque agent ID |
| `POST` | `/api/sessions/{id}/end` | End a local session |
| `POST` | `/api/prompts` | Save user input in `user_prompts` |
| `GET/POST` | `/api/observations` | List or create observations |

`POST /api/prompts` accepts `session_id`, `content`, and `project`. Browser access uses exact origins from `http.allowed_origins`; CORS does not replace API-token authentication.

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
| `GET` | `/api/graph/{uuid}/subgraph` | Heterogeneous subgraph |
| `GET` | `/api/graph/project-graph` | Full project code & knowledge graph |
| `GET` | `/api/graph/analytics` | Structural health report (Louvain communities, God nodes, cycles) |
| `GET` | `/api/graph/blast-radius` | Blast radius impact calculation |
| `POST` | `/api/graph/ingest-code` | Ingest AST code entities into project graph |
| `POST` | `/api/graph/resolve` | Dynamic conflict resolution (`supersedes`) |
| `DELETE` | `/api/graph/edges/{uuid}` | Delete graph edge |
| `GET` | `/api/scores/{uuid}` | Importance score |
| `GET` | `/api/stats` | Workspace counters |
| `GET` | `/api/projects` | Visible project keys |
| `GET` | `/api/projects/context` | Corporate rules & system prompt |
| `GET` | `/api/projects/artifacts` | List project artifacts |
| `POST` | `/api/projects/artifacts` | Save project artifact |
| `DELETE` | `/api/projects/artifacts/{id}` | Delete project artifact |
| `GET` | `/api/audit` | Admin-only audit entries |
| `POST` | `/mcp` | Streamable HTTP MCP |

Request tenant/workspace authority is never accepted from clients. Server edge metadata is limited to fields persisted by the PostgreSQL schema.

# MCP Tools

Cortex uses the `cortex_*` namespace. Local stdio and server Streamable HTTP are intentionally different catalogs.

## Local Profiles

Cortex MCP is strictly focused on **agentic capabilities** — tools that an AI coding agent can autonomously reason about, provide valid inputs for, and consume within its cognitive loop:

| Profile | Description | Tools | Count |
|---|---|---|---|
| `agent` | Canonical AI coding agent suite (default) | `cortex_save`, `cortex_update`, `cortex_get_observation`, `cortex_context`, `cortex_session_summary`, `cortex_search`, `cortex_get_agent_context`, `cortex_relate`, `cortex_graph`, `cortex_graph_path`, `cortex_get_rules`, `cortex_save_rule`, `cortex_ingest_code`, `cortex_get_blast_radius`, `cortex_code_tests`, `cortex_get_code_symbols`, `cortex_detect_cycles`, `cortex_analyze_architecture`, `cortex_code_map`, `cortex_get_status`, `cortex_revision_history`, `cortex_handoff` | 22 |
| `dev` | Golden suite for local software development | `cortex_save`, `cortex_search`, `cortex_context`, `cortex_session_summary`, `cortex_get_observation`, `cortex_get_agent_context`, `cortex_relate`, `cortex_get_rules`, `cortex_ingest_code`, `cortex_get_blast_radius`, `cortex_code_tests` | 11 |
| `minimal` | Ultra-low footprint for fast inference | `cortex_save`, `cortex_search`, `cortex_context`, `cortex_session_summary`, `cortex_get_observation` | 5 |

Use `cortex mcp` (defaults to `agent`), or specify `--tools=dev` or `--tools=minimal`. Local observations, prompts, and edges use integer IDs; local sessions use opaque agent-provided strings.

### Architectural Note: Retiring Non-Agentic Tools from MCP
The `admin` (destructive deletion, project merging, compaction) and `temporal` (execution duration, memory telemetry, manual RFC3339 timestamps) toolsets are **deprecated and retired from standard agent discovery**:
- **Why?** An autonomous agent should never be exposed to destructive operations (`cortex_delete`) or asked to record infrastructure memory telemetry (`cortex_temporal_record_operation`). Exposing 40+ tools imposes a massive ~6,000-token prompt tax and degrades tool-calling accuracy.
- **Where did they go?**
  - Administrative operations belong to the **CLI** (`cortex gc`, `cortex merge-projects`, `cortex doctor`), the **TUI**, and the **Web Dashboard**.
  - Telemetry and health checks belong to internal middleware and the `/health` REST endpoint.
  - Temporal evolution is handled natively and automatically via `topic_key` upserts in `cortex_save` and `cortex_revision_history`.

## Global Remote Proxy

The global `~/.cortex/cortex.yaml` can make the installed stdio command proxy a published Cortex server:

```yaml
mcp:
  enabled: true
  remote:
    enabled: true
    url: https://cortex.example/mcp
    token_env: CORTEX_REMOTE_TOKEN
    timeout: 30s
```

Set the named environment variable to a valid server bearer token, then restart the agent process. In remote mode, `cortex mcp` does not open SQLite: it negotiates the remote catalog and forwards tool calls/results over the local stdio transport. The remote server controls the available tools, so local `--tools` profiles do not filter this catalog. The proxy fails closed if configuration, authentication, or remote initialization fails.

## Server Tools

Server MCP exposes the authenticated catalog:

| Tool | Purpose | Key Parameters |
|---|---|---|
| `cortex_save` | Save durable observation | `title`, `content`, `session_id`, `project`, `type`, `source` |
| `cortex_handoff` | Idempotent durable memory handoff | `idempotency_key`, `observation`, `relation` |
| `cortex_session_start` | Start memory session | `project`, `summary` |
| `cortex_search` | Search observations (hybrid semantic + FTS5) | `query`, `type`, `project`, `scope`, `limit` |
| `cortex_get_observation` | Get observation by public UUID | `id` |
| `cortex_update` | Update observation fields | `id`, `title`, `content`, `type`, `project`, `scope` |
| `cortex_delete` | Delete observation | `id` |
| `cortex_relate` | Create semantic graph edge | `from_id`, `to_id`, `relation_type`, `weight`, `confidence`, `reasoning` |
| `cortex_graph` | Get related observations | `observation_id`, `depth` |
| `cortex_graph_subgraph` | Get heterogeneous bounded graph | `observation_id`, `depth`, `max_nodes` |
| `cortex_get_blast_radius` | Calculate blast radius & impacted files | `node_id`, `depth` |
| `cortex_ingest_code` | Ingest codebase AST symbols and dependencies | `path`, `project`, `max_files` |
| `cortex_get_code_symbols` | Query indexed AST symbols with regex | `project`, `file`, `kind`, `package`, `query`, `limit` |
| `cortex_get_code_graph` | Structural code graph with callers/callees | `project` |
| `cortex_analyze_architecture` | Full graph architecture & Louvain communities | `project` |
| `cortex_detect_cycles` | Detect circular dependencies (Tarjan SCC) | `project` |
| `cortex_get_agent_context` | Structured context pack for agent prompts | `project`, `format`, `max_tokens` |
| `cortex_get_compact_context` | Bounded high-density prompt context pack | `project`, `max_tokens` |
| `cortex_score` | Get observation importance score | `observation_id` |
| `cortex_get_project_context` | Get corporate governance & project rules | `project` |
| `cortex_list_skills` | List corporate and project skills | `project` |
| `cortex_get_skill` | Get skill instructions & rules by key | `key`, `project` |
| `cortex_resolve_query` | Intelligently resolve query in Server mode | `query`, `project`, `limit` |
| `cortex_get_status` | Operational mode (Server PostgreSQL) & capabilities | - |

Server tools use public UUIDs and operate through `AuthorizedStore`. `cortex_graph_subgraph` returns the bounded heterogeneous projection of observations, entities, actors, sessions, and projects. Server REST-only capabilities such as stats, sessions, projects, and audit are documented in [HTTP-API.md](HTTP-API.md).

Agents must use the schema returned by `tools/list`: numeric IDs from a local catalog are not interchangeable with server UUIDs. Switching `mcp.remote.enabled` changes both the catalog and its ID schema.

## Safety

`cortex_delete` is destructive in the local admin profile and soft-delete-only in the current server subset. Check `tools/list` for the exact transport catalog and schema. Unknown profile/tool names should be treated as configuration errors, not assumed to be available.

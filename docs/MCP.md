# MCP Tools

Cortex uses the `cortex_*` namespace. Local stdio and server Streamable HTTP are intentionally different catalogs.

## Local Profiles

| Profile | Tools |
|---|---|
| `agent` | `save`, `search`, `context`, `session_summary`, `session_start`, `session_end`, `get_observation`, `suggest_topic_key`, `capture_passive`, `save_prompt`, `update`, `relate`, `graph`, `graph_relationships`, `graph_path`, `score`, `search_hybrid`, `revision_history`, `consolidate`, `project_dna` |
| `admin` | `delete`, `stats`, `timeline`, `archive`, `merge_projects` |
| `temporal` | `temporal_create_edge`, `temporal_get_edges`, `temporal_get_relevant`, `temporal_create_snapshot`, `temporal_record_operation`, `temporal_evaluate_quality`, `temporal_system_metrics`, `temporal_health_check`, `temporal_evolution_path`, `temporal_fact_state`, `search_temporal` |

Use `cortex mcp --tools=agent`, `--tools=admin`, or `--tools=temporal`. An empty `--tools` value loads all local tools. Local observations, prompts, and edges use integer IDs; local sessions use opaque agent-provided strings.

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

Server MCP currently exposes the authenticated subset:

`cortex_save`, `cortex_session_start`, `cortex_search`, `cortex_get_observation`, `cortex_update`, `cortex_delete`, `cortex_relate`, `cortex_graph`, `cortex_graph_subgraph`, `cortex_score`.

Server tools use public UUIDs and operate through `AuthorizedStore`. `cortex_graph_subgraph` returns the bounded heterogeneous projection of observations, entities, actors, sessions, and projects. They do not expose local admin or temporal profiles. Server REST-only capabilities such as stats, sessions, projects, and audit are documented in [HTTP-API.md](HTTP-API.md).

Agents must use the schema returned by `tools/list`: numeric IDs from a local catalog are not interchangeable with server UUIDs. Switching `mcp.remote.enabled` changes both the catalog and its ID schema.

## Safety

`cortex_delete` is destructive in the local admin profile and soft-delete-only in the current server subset. Check `tools/list` for the exact transport catalog and schema. Unknown profile/tool names should be treated as configuration errors, not assumed to be available.

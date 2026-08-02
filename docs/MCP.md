# MCP Tools

Cortex uses the `cortex_*` namespace. Local stdio and server Streamable HTTP are intentionally different catalogs.

## Local Profiles

| Profile | Tools |
|---|---|
| `agent` | `save`, `search`, `context`, `session_summary`, `session_start`, `session_end`, `get_observation`, `suggest_topic_key`, `capture_passive`, `save_prompt`, `update`, `relate`, `graph`, `score`, `search_hybrid`, `revision_history`, `consolidate`, `project_dna` |
| `admin` | `delete`, `stats`, `timeline`, `archive`, `merge_projects` |
| `temporal` | `temporal_create_edge`, `temporal_get_edges`, `temporal_get_relevant`, `temporal_create_snapshot`, `temporal_record_operation`, `temporal_evaluate_quality`, `temporal_system_metrics`, `temporal_health_check`, `temporal_evolution_path`, `temporal_fact_state`, `search_temporal` |

Use `cortex mcp --tools=agent`, `--tools=admin`, or `--tools=temporal`. An empty `--tools` value loads all local tools. Local tools use local integer IDs.

## Server Tools

Server MCP currently exposes the authenticated subset:

`cortex_save`, `cortex_session_start`, `cortex_search`, `cortex_get_observation`, `cortex_update`, `cortex_delete`, `cortex_relate`, `cortex_graph`, `cortex_score`.

Server tools use public UUIDs and operate through `AuthorizedStore`. They do not expose local admin or temporal profiles. Server REST-only capabilities such as stats, sessions, projects, and audit are documented in [HTTP-API.md](HTTP-API.md).

## Safety

`cortex_delete` is destructive in the local admin profile and soft-delete-only in the current server subset. Check `tools/list` for the exact transport catalog and schema. Unknown profile/tool names should be treated as configuration errors, not assumed to be available.

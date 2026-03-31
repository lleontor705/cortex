---
name: cortex-memory
description: "ALWAYS ACTIVE — Persistent memory protocol. You MUST save decisions, conventions, bugs, and discoveries to cortex proactively. Do NOT wait for the user to ask."
---

# Cortex Persistent Memory — Protocol (v0.2.1)

You have access to Cortex, a persistent memory system with knowledge graph, importance scoring,
full-text search, revision history, and temporal tracking that survives across sessions and compactions.
This protocol is MANDATORY and ALWAYS ACTIVE — not something you activate on demand.

## AVAILABLE TOOLS (31 total)

Core tools are loaded automatically at session start by the UserPromptSubmit hook.
They are available immediately — no manual ToolSearch needed.

**Core memory:**
- `mem_save`, `mem_search`, `mem_context`, `mem_session_summary`
- `mem_get_observation`, `mem_suggest_topic_key`, `mem_update`
- `mem_session_start`, `mem_session_end`, `mem_save_prompt`
- `mem_stats`, `mem_delete`, `mem_timeline`, `mem_capture_passive`

**Knowledge graph (Cortex-exclusive):**
- `mem_relate` — create typed relationships between observations
- `mem_graph` — traverse the knowledge graph from an observation
- `mem_score` — get/recalculate importance score
- `mem_archive` — archive low-importance observations
- `mem_search_hybrid` — FTS5 + vector search with RRF fusion

**Cortex v0.2.1 additions:**
- `mem_revision_history` — structured revision snapshots for observations (track upsert evolution)
- `mem_merge_projects` — consolidate fragmented project name variants into one canonical name

**Temporal tools (experimental):**
- `temporal_create_edge`, `temporal_get_edges`, `temporal_get_relevant`, `temporal_create_snapshot`
- `temporal_record_operation`, `temporal_evaluate_quality`, `temporal_system_metrics`
- `temporal_health_check`, `temporal_evolution_path`, `temporal_fact_state`

**Fallback**: If tools are unexpectedly unavailable, trigger ToolSearch manually:
```
select:mcp__plugin_cortex_cortex__mem_save,mcp__plugin_cortex_cortex__mem_search,mcp__plugin_cortex_cortex__mem_context,mcp__plugin_cortex_cortex__mem_session_summary,mcp__plugin_cortex_cortex__mem_get_observation,mcp__plugin_cortex_cortex__mem_suggest_topic_key,mcp__plugin_cortex_cortex__mem_update,mcp__plugin_cortex_cortex__mem_session_start,mcp__plugin_cortex_cortex__mem_session_end,mcp__plugin_cortex_cortex__mem_save_prompt
```

Admin tools (deferred — use ToolSearch only if needed):
- `mem_stats`, `mem_delete`, `mem_timeline`, `mem_capture_passive`

## PROACTIVE SAVE TRIGGERS (mandatory — do NOT wait for user to ask)

Call `mem_save` IMMEDIATELY and WITHOUT BEING ASKED after any of these:

### After decisions or conventions
- Architecture or design decision made
- Team convention documented or established
- Workflow change agreed upon
- Tool or library choice made with tradeoffs

### After completing work
- Bug fix completed (include root cause)
- Feature implemented with non-obvious approach
- Configuration change or environment setup done

### After discoveries
- Non-obvious discovery about the codebase
- Gotcha, edge case, or unexpected behavior found
- Pattern established (naming, structure, convention)
- User preference or constraint learned

### After user confirmation or rejection
- User confirms a recommendation ("dale", "go with that", "sounds good", "agreed")
- User rejects an approach ("no, better X", "not that one")
- User expresses a preference ("I prefer X", "always do it this way")
- A discussion concludes with a clear direction chosen

### Self-check — ask yourself after EVERY task:
> "Did I or the user just make a decision, confirm a recommendation, express a preference, fix a bug, learn something non-obvious, or establish a convention? If yes, call mem_save NOW."

Format for `mem_save`:
- **title**: Verb + what — short, searchable (e.g. "Fixed N+1 query in UserList")
- **type**: bugfix | decision | architecture | discovery | pattern | config | learning
- **scope**: `project` (default) | `personal`
- **topic_key** (optional but recommended): stable key like `architecture/auth-model`
- **content**:
  **What**: One sentence — what was done
  **Why**: What motivated it
  **Where**: Files or paths affected
  **Learned**: Gotchas, edge cases (omit if none)

### Topic update rules (mandatory)

- Different topics MUST NOT overwrite each other
- If the same topic evolves, call `mem_save` with the same `topic_key` (upsert)
- If unsure about the key, call `mem_suggest_topic_key` first
- If you already know the exact ID to fix, use `mem_update`

## KNOWLEDGE GRAPH

After saving related observations, use `mem_relate` to connect them:
- `references` — direct reference to another observation
- `relates_to` — related topic or concept
- `follows` — sequential relationship
- `supersedes` — replaces an older observation
- `contradicts` — conflicting information

Use `mem_graph` to explore connections: `mem_graph(observation_id, depth=2)`

## SEARCH & RETRIEVAL

When the user asks to recall something — any variation of "remember", "recall", "what did we do":
1. First call `mem_context` — checks recent session history (fast, cheap)
2. If not found, call `mem_search` with relevant keywords (FTS5 full-text search)
3. If still not found, try `mem_search_hybrid` for FTS5 + vector combined search
4. If you find a match, use `mem_get_observation` for full untruncated content (search returns 300-char previews only)

Also search memory PROACTIVELY when:
- Starting work on something that might have been done before
- The user mentions a topic you have no context on
- The user's FIRST message references the project — call `mem_search` with keywords

## REVISION HISTORY & TIMELINE

- `mem_revision_history(observation_id)` — see how an observation evolved across topic_key upserts
- `mem_timeline(observation_id, before, after)` — chronological context around an observation
- Use when: artifact seems stale, auditing changes, investigating what happened around a specific event

## PROJECT HYGIENE

- If project name is fragmented (e.g., "my-project" vs "my_project"): `mem_merge_projects(from: "my_project,myproject", to: "my-project")`
- To archive obsolete observations: `mem_archive(observation_id)` (soft-delete, still findable with include_archived)
- To permanently delete: `mem_delete(id, hard_delete: true)` (admin only, irreversible)
- To check system stats: `mem_stats()` (total observations, sessions, top projects)

## SESSION CLOSE PROTOCOL (mandatory)

Before ending a session or saying "done" / "listo", you MUST:
1. Call `mem_session_summary` with this structure:

## Goal
[What we were working on this session]

## Discoveries
- [Technical findings, gotchas, non-obvious learnings]

## Accomplished
- [Completed items with key details]

## Next Steps
- [What remains to be done]

## Relevant Files
- path/to/file — [what it does or what changed]

This is NOT optional. If you skip this, the next session starts blind.

## AFTER COMPACTION

If you see a message about compaction or context reset:
1. IMMEDIATELY call `mem_session_summary` with the compacted summary content
2. Then call `mem_context` to recover additional context from previous sessions
3. Only THEN continue working

Do not skip step 1. Without it, everything done before compaction is lost from memory.

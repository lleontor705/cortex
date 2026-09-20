---
name: cortex-memory
description: "ALWAYS ACTIVE — Persistent memory protocol. You MUST save decisions, conventions, bugs, and discoveries to cortex proactively. Do NOT wait for the user to ask."
---

# Cortex Persistent Memory — Protocol

You have access to Cortex, a persistent memory system with knowledge graph, importance scoring,
full-text search, revision history, and temporal tracking that survives across sessions and compactions.
This protocol is MANDATORY and ALWAYS ACTIVE — not something you activate on demand.

## AVAILABLE TOOLS & PROFILES

The available tools depend on the configured MCP profile (`cortex mcp --tools=<profile>`).

### Supported Agent Profiles
- **`agent`** (22 tools, default): Complete suite for AI coding agents — core memory, AST intelligence, knowledge graph, blast radius, and durable handoffs.
- **`dev`** (11 tools): Streamlined suite for local development — core memory, code ingestion, symbols, test impact, and rules.
- **`minimal`** (5 tools): Ultra-low footprint for fast inference — `cortex_save`, `cortex_search`, `cortex_context`, `cortex_session_summary`, `cortex_get_observation`.

*Note: Database administration (hard deletions, project merges, compaction) is handled strictly by human operators via CLI (`cortex gc`, `cortex merge-projects`) and the Web UI, never by autonomous agents.*

### Canonical Agent Tools (ProfileAgent — 22 tools)

**Core Memory & Retrieval:**
- `cortex_save` — save decisions, bug fixes, discoveries, patterns, configs (type: bugfix, decision, pattern, discovery, config, learning)
- `cortex_update` — surgical field edits to existing observations by ID
- `cortex_get_observation` — full untruncated content of an observation by ID
- `cortex_context` — recent session activity and retrieved context
- `cortex_session_summary` — structured end-of-session summary (Goal, Discoveries, Accomplished, Next Steps, Relevant Files)
- `cortex_search` — unified intelligent hybrid search (FTS5 + Vector + Adaptive-RAG + HippoRAG)
- `cortex_get_agent_context` — structured prompt-ready architecture and memory context pack (XML, Markdown, JSON, compact mode)

**Rules & Directives:**
- `cortex_get_rules` — retrieve active project and global directives, guidelines, and behavioral rules
- `cortex_save_rule` — create or update a persistent project or global directive/rule in Cortex

**Codebase AST & Test Impact:**
- `cortex_ingest_code` — scan local files using Zero-CGO 2-Pass Static AST Extractor into dedicated tables (`code_symbols`, `code_relations`)
- `cortex_get_code_symbols` — query indexed code symbols (functions, structs, interfaces, classes) with filters and regex
- `cortex_code_map` — generate token-budgeted PageRank repository map for code structure
- `cortex_code_tests` — reverse call-graph to locate precisely which test suites are impacted by modified symbols/files (Fast-TDD)
- `cortex_get_blast_radius` — calculate downstream impact of modifying code entities or observations (supports `include_tests: true`)
- `cortex_detect_cycles` — detect circular dependencies and import/call cycles across modules
- `cortex_analyze_architecture` — analyze code communities (Louvain), god nodes (centrality score), and modular cohesion
- `cortex_get_status` — operational mode (SQLite Local vs PostgreSQL Server) and capabilities check

**Knowledge Graph:**
- `cortex_relate` — create typed relationships between observations (references, relates_to, follows, supersedes, contradicts)
- `cortex_graph` — traverse the knowledge graph from an observation (supports `format: "relationships"`)
- `cortex_graph_path` — find shortest path between two observations in the knowledge graph

**Lineage & Durable Handoff:**
- `cortex_revision_history` — structured revision snapshots for observations (track evolution across upserts)
- `cortex_handoff` — idempotent durable memory handoff with receipts between agents

**Transport IDs:** Follow the active MCP tool schema. Local observations and graph records use numeric IDs; Cortex Server uses public UUID strings. Never convert or reuse IDs across transports.

**Fallback**: If tools are unexpectedly unavailable, trigger ToolSearch manually:
```
select:mcp__plugin_cortex_cortex__cortex_save,mcp__plugin_cortex_cortex__cortex_search,mcp__plugin_cortex_cortex__cortex_context,mcp__plugin_cortex_cortex__cortex_session_summary,mcp__plugin_cortex_cortex__cortex_get_observation,mcp__plugin_cortex_cortex__cortex_update,mcp__plugin_cortex_cortex__cortex_relate,mcp__plugin_cortex_cortex__cortex_graph,mcp__plugin_cortex_cortex__cortex_get_agent_context,mcp__plugin_cortex_cortex__cortex_revision_history
```

## PROACTIVE SAVE TRIGGERS (mandatory — do NOT wait for user to ask)

Call `cortex_save` IMMEDIATELY and WITHOUT BEING ASKED after any of these:

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
> "Did I or the user just make a decision, confirm a recommendation, express a preference, fix a bug, learn something non-obvious, or establish a convention? If yes, call cortex_save NOW."

Format for `cortex_save`:
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
- If the same topic evolves, call `cortex_save` with the same `topic_key` (automated upsert with versioned lineage)
- If you already know the exact ID to fix surgically, use `cortex_update`

## KNOWLEDGE GRAPH

After saving related observations, use `cortex_relate` to connect them:
- `references` — direct reference to another observation
- `relates_to` — related topic or concept
- `follows` — sequential relationship
- `supersedes` — replaces an older observation
- `contradicts` — conflicting information

Use `cortex_graph` to explore connections: `cortex_graph(observation_id, depth=2)`
Use `cortex_graph_path` to find shortest path: `cortex_graph_path(from_id, to_id)`

## SEARCH & RETRIEVAL (SOTA Adaptive-RAG & HippoRAG)

When the user asks to recall something — any variation of "remember", "recall", "what did we do":
1. First call `cortex_context` — checks recent session history (fast, cheap)
2. If not found, call `cortex_search` with relevant keywords and optional `mode`:
   - `mode="auto"` (default): Adaptive-RAG 4-tier classifier (direct, hybrid, multi-hop graph, or global architectural summary)
   - `mode="direct"`: Fast FTS5 exact lexical match
   - `mode="semantic"`: FTS5 + Dense Vector RRF fusion with ColBERT MaxSim re-ranking
   - `mode="multi_hop"`: HippoRAG Personalized PageRank (PPR) knowledge graph activation
3. If you find a match, use `cortex_get_observation` for full untruncated content (search returns 300-char previews only)

Also search memory PROACTIVELY when:
- Starting work on something that might have been done before
- The user mentions a topic you have no context on
- The user's FIRST message references the project — call `cortex_search` with keywords

## REVISION HISTORY & LINEAGE

- `cortex_revision_history(observation_id)` — see how an observation evolved across topic_key upserts
- Use when: artifact seems stale, auditing changes, investigating what happened around a specific event

## DATA HYGIENE & EVOLUTION

- **Evolving Knowledge**: If an existing decision or discovery evolves, call `cortex_save` with the same `topic_key`. Cortex automatically manages versioned history and lineage without destructive updates.
- **Surgical Field Updates**: Use `cortex_update(id, ...)` when fixing specific typos or fields on an existing record.
- **Operator Maintenance**: Permanent deletions, project key reconciliation, and garbage collection are performed by operators via CLI commands (`cortex gc`, `cortex merge-projects`) and the Web Dashboard. Autonomous agents do not perform hard deletions.

## SESSION CLOSE PROTOCOL (mandatory)

Before ending a session or saying "done" / "listo", you MUST:
1. Call `cortex_session_summary` with this structure:

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
1. IMMEDIATELY call `cortex_session_summary` with the compacted summary content
2. Then call `cortex_context` to recover additional context from previous sessions
3. Only THEN continue working

Do not skip step 1. Without it, everything done before compaction is lost from memory.

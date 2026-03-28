# Architecture

## How It Works

```
1. Agent completes significant work (bugfix, architecture decision, etc.)
2. Agent calls mem_save → title, type, What/Why/Where/Learned
3. Cortex persists to SQLite with FTS5 indexing + entity extraction
4. Importance score auto-calculated (access + recency + edges + type - age)
5. Optional: Agent calls mem_relate to create knowledge graph edges
6. Next session: agent searches memory, gets relevant context + related observations
```

## Session Lifecycle

```
Session Start
    ↓
Agent works → proactive mem_save after decisions, bugs, discoveries
    ↓
Agent uses mem_search / mem_context to recall prior work
    ↓
Agent calls mem_relate to connect related observations
    ↓
Before ending: mem_session_summary (mandatory)
    ↓
Session End
```

## Knowledge Graph

Observations can be connected with typed relationships:

| Relation | Meaning |
|----------|---------|
| `references` | Direct reference to another observation |
| `relates_to` | Related topic or concept |
| `follows` | Sequential relationship |
| `supersedes` | Replaces an older observation |
| `contradicts` | Conflicting information |

Use `mem_graph` to traverse connections from any observation with configurable depth (1-10).

## Importance Scoring

Each observation has a computed importance score (0.0 to 5.0):

```
score = base(0.5) + accessBonus + recencyBonus + edgeBonus + typeBonus - agePenalty

accessBonus:  +0.1 per access (max 1.0)
recencyBonus: +0.5 if accessed in last 24 hours
edgeBonus:    +0.2 per incoming edge (max 1.0)
typeBonus:    decision(+0.5), bugfix(+0.3), pattern(+0.2), discovery(+0.15)
agePenalty:   -0.01 per day (max 0.5)
```

## Entity Linking

When observations are saved, Cortex extracts entities from the content:

| Entity Type | Examples |
|-------------|----------|
| `file` | `src/auth/middleware.ts`, `internal/store/store.go` |
| `url` | `https://api.example.com/docs` |
| `package` | `github.com/spf13/viper`, `@scope/package` |
| `symbol` | `func HandleSave`, `type Config` |

## Progressive Disclosure (3-Layer Pattern)

Token-efficient memory retrieval:

1. **`mem_search`** — titles + 300-char previews (low token cost)
2. **`mem_timeline`** — chronological context around a result (medium)
3. **`mem_get_observation`** — full untruncated content (high)

## Memory Hygiene

- **Deduplication**: SHA-256 hash prevents duplicate observations within a time window
- **Soft delete**: `deleted_at` field; hard delete requires explicit `hard_delete=true`
- **Topic key upsert**: Same `topic_key` in same project updates existing observation
- **Scope filtering**: `project` (shared) vs `personal` (private)
- **Auto-archival**: Old observations with low importance scores are archived periodically

## MCP Tools

### Agent Profile (15 tools)

| Tool | Purpose | Loading |
|------|---------|---------|
| `mem_save` | Save observation | Eager |
| `mem_search` | Full-text search | Eager |
| `mem_context` | Recent session context | Eager |
| `mem_session_summary` | End-of-session save | Eager |
| `mem_get_observation` | Full content by ID | Eager |
| `mem_save_prompt` | Save user prompt | Eager |
| `mem_update` | Update by ID | Deferred |
| `mem_suggest_topic_key` | Stable key for upserts | Deferred |
| `mem_session_start` | Register session start | Deferred |
| `mem_session_end` | Mark session complete | Deferred |
| `mem_capture_passive` | Extract learnings | Deferred |
| `mem_relate` | Create graph edge | Deferred |
| `mem_graph` | Traverse graph | Deferred |
| `mem_score` | Get importance score | Deferred |
| `mem_search_hybrid` | FTS5 + vector search | Deferred |

### Admin Profile (4 tools)

| Tool | Purpose |
|------|---------|
| `mem_delete` | Soft or hard delete |
| `mem_stats` | Memory statistics |
| `mem_timeline` | Chronological drill-in |
| `mem_archive` | Archive observation |

### Tool Profiles

```bash
cortex mcp                        # All 19 tools (default)
cortex mcp --tools=agent          # 15 agent tools
cortex mcp --tools=admin          # 4 admin tools
cortex mcp --tools=agent,admin    # Combine profiles
cortex mcp --tools=mem_save,mem_search  # Individual tools
```

## Topic Key Workflow

Topic keys enable evolving observations that update instead of creating duplicates:

```
1. Agent saves "JWT auth middleware" with topic_key: architecture/auth-model
2. Later, agent saves update with same topic_key → upsert (updates existing)
3. Different topic? Use different key → new observation
```

Use `mem_suggest_topic_key` to auto-generate stable keys with family heuristic:
- `architecture/*` for design decisions
- `bug/*` for fixes and incidents
- `decision/*` for tradeoff choices
- `pattern/*` for conventions
- `config/*` for setup and infrastructure

## Project Structure

```
cmd/cortex/              CLI entry point
internal/
  app/                   Dependency wiring (config, DB, stores, archival)
  cli/                   Command dispatch
  config/                YAML + env var config (8 sections)
  database/              SQLite connection manager (WAL, pure Go)
  domain/
    models.go            Core types (Observation, Session, Edge, EntityLink, ImportanceScore)
    interfaces.go        Repository interfaces
    memory/              Observation CRUD service
    scoring/             Importance scoring formula
    search/              FTS5 query sanitization
    graph/               Knowledge graph BFS traversal
    session/             Session lifecycle
    lifecycle/           Auto-archival service
    entity/              Entity extraction (regex-based)
  store/
    sqlite/              Observation + vector stores
    session/             Session store
    search/              FTS5 search (BM25 ranking)
    prompt/              Prompt store
    graph/               Knowledge graph store
    scoring/             Importance scoring store
    entity/              Entity link store
  mcp/                   MCP server + 19 tool handlers
  http/                  REST API (net/http stdlib)
  tui/                   Terminal UI
  migration/             Migration framework (up/down)
  setup/                 Agent integration setup
migrations/              SQL migrations (001-006)
plugin/
  claude-code/           Hooks, scripts, skills for Claude Code
  opencode/              TypeScript plugin adapter
testutil/                Test helpers (in-memory DB, fixtures, assertions)
```

## Database Schema

6 migrations:

| Migration | Tables |
|-----------|--------|
| 001_init | sessions, observations |
| 002_fts | observations_fts (FTS5 virtual table + triggers) |
| 003_graph | edges (knowledge graph relationships) |
| 004_scoring | importance_scores (with auto-init trigger) |
| 005_vectors | observation_embeddings (384-dim, optional) |
| 006_entities | entity_links (extracted entities) |

SQLite config: WAL mode, NORMAL sync, 64MB cache, foreign keys ON.

## HTTP API

Port: **7438** (env: `CORTEX_PORT`)

| Method | Endpoint | Purpose |
|--------|----------|---------|
| GET | `/health` | Health check |
| GET | `/api/observations` | List observations |
| POST | `/api/observations` | Create observation |
| GET | `/api/observations/{id}` | Get by ID |
| PUT | `/api/observations/{id}` | Update |
| DELETE | `/api/observations/{id}` | Delete |
| GET | `/api/sessions` | List sessions |
| POST | `/api/sessions` | Create session |
| POST | `/api/sessions/{id}/end` | End session |
| GET | `/api/search?q=...` | Full-text search |
| POST | `/api/graph/edges` | Create edge |
| GET | `/api/graph/{id}/related` | Get related |
| DELETE | `/api/graph/edges/{id}` | Delete edge |
| GET | `/api/scores/{id}` | Get score |
| POST | `/api/scores/{id}/recalculate` | Recalculate score |

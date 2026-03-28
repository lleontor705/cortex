# Comparison

## Cortex vs Engram

Cortex is built on Engram's foundation with full API compatibility. Here's what's different:

| Dimension | Engram | Cortex |
|-----------|--------|--------|
| MCP Tools | 14 | 19 (14 Engram + 5 exclusive) |
| Knowledge Graph | no | yes (typed edges, BFS traversal) |
| Importance Scoring | no | yes (access + recency + edges + type - age) |
| Auto-Archival | no | yes (configurable age + score threshold) |
| Entity Linking | no | yes (files, URLs, packages, symbols) |
| Vector Search | no | yes (optional, 384-dim embeddings + RRF fusion) |
| Git Sync | yes | not yet |
| Search | FTS5 | FTS5 + hybrid vector RRF |
| Storage | SQLite + FTS5 | SQLite + FTS5 + graph + scoring + vectors |
| Migrations | 3 tables | 6 migrations (sessions, FTS, graph, scoring, vectors, entities) |
| HTTP Port | 7437 | 7438 |
| Agent Profiles | agent (11), admin (3) | agent (15), admin (4) |

### When to use Engram
- You want a battle-tested, simpler memory system
- You need Git sync for sharing memories across machines
- You don't need knowledge graph or scoring

### When to use Cortex
- You want to relate observations with typed edges
- You need importance scoring to surface high-value memories
- You want automatic entity extraction from content
- You need hybrid search combining FTS5 with vector similarity
- You want auto-archival of stale, low-importance observations

## Cortex vs claude-mem

| Dimension | claude-mem | Cortex |
|-----------|-----------|--------|
| Language | Python | Go |
| Agent lock-in | Claude only | Any MCP agent |
| Search | keyword | FTS5 + vector + knowledge graph |
| Capture | auto (everything) | agent-driven (intentional) |
| Storage | JSON files | SQLite + FTS5 |
| Dependencies | Python + pip | Zero (single binary) |
| Knowledge graph | no | yes |
| Importance scoring | no | yes |

Cortex lets the agent decide what's worth saving — less noise, better signal.

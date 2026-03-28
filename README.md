<p align="center">
  <img width="1024" height="340" alt="Cortex Logo" src="./assets/cortex-logo.png" />
</p>

<p align="center">
  <strong>Persistent memory for AI coding agents</strong><br>
  <em>Knowledge graph &bull; Importance scoring &bull; Entity linking &bull; Vector search</em>
</p>

<p align="center">
  <a href="https://github.com/lleontor705/cortex/actions/workflows/ci.yml"><img src="https://github.com/lleontor705/cortex/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
  <a href="https://github.com/lleontor705/cortex/releases/latest"><img src="https://img.shields.io/github/v/release/lleontor705/cortex?label=release" alt="Release" /></a>
  <a href="https://github.com/lleontor705/cortex/blob/master/LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License" /></a>
  <a href="https://goreportcard.com/report/github.com/lleontor705/cortex"><img src="https://goreportcard.com/badge/github.com/lleontor705/cortex" alt="Go Report Card" /></a>
</p>

<p align="center">
  <a href="docs/INSTALLATION.md">Installation</a> &bull;
  <a href="docs/AGENT-SETUP.md">Agent Setup</a> &bull;
  <a href="docs/ARCHITECTURE.md">Architecture</a> &bull;
  <a href="docs/PLUGINS.md">Plugins</a> &bull;
  <a href="CONTRIBUTING.md">Contributing</a>
</p>

---

> **cortex** `/ˈkɔːr.tɛks/` — *neuroscience*: the outer layer of the brain responsible for memory, attention, perception, and cognition.

Your AI coding agent forgets everything when the session ends. Cortex gives it a brain — with a knowledge graph.

Built on the foundation of [Engram](https://github.com/Gentleman-Programming/engram), Cortex adds **knowledge graph relationships**, **importance scoring**, **entity linking**, **auto-archival**, and **optional vector search** while maintaining full API compatibility. Drop-in replacement — migrate in one command.

A **Go binary** with SQLite + FTS5 full-text search + Knowledge Graph, exposed via CLI, HTTP API, MCP server, and TUI. Works with **any agent** that supports MCP — Claude Code, OpenCode, Gemini CLI, Codex, VS Code (Copilot), Antigravity, Cursor, Windsurf, or anything else.

```
Agent (Claude Code / OpenCode / Gemini CLI / Codex / VS Code / Antigravity / ...)
    ↓ MCP stdio
Cortex (single Go binary)
    ↓
SQLite + FTS5 + Knowledge Graph + Importance Scoring
```

## Quick Start

### Install

```bash
# macOS / Linux
brew install lleontor705/tap/cortex

# go install (all platforms)
go install github.com/lleontor705/cortex/cmd/cortex@latest
```

Windows, pre-built binaries, Docker → [docs/INSTALLATION.md](docs/INSTALLATION.md)

### Setup Your Agent

| Agent | One-liner |
|-------|-----------|
| Claude Code | `cortex setup claude-code` |
| OpenCode | `cortex setup opencode` |
| Gemini CLI | `cortex setup gemini-cli` |
| Codex | `cortex setup codex` |
| VS Code | `code --add-mcp '{"name":"cortex","command":"cortex","args":["mcp"]}'` |
| Cursor / Windsurf / Any MCP | See [docs/AGENT-SETUP.md](docs/AGENT-SETUP.md) |

Full per-agent config, Memory Protocol, and compaction survival → [docs/AGENT-SETUP.md](docs/AGENT-SETUP.md)

That's it. No Node.js, no Python, no Docker. **One binary, one SQLite file.**

### Migrating from Engram?

```bash
cortex import --from-engram --path ~/.engram/engram.db
```

Full migration script (uninstalls Engram, migrates data, reconfigures agents) → [`scripts/migrate-from-engram.sh`](scripts/migrate-from-engram.sh)

## How It Works

```
1. Agent completes significant work (bugfix, architecture decision, etc.)
2. Agent calls mem_save → title, type, What/Why/Where/Learned
3. Cortex persists to SQLite with FTS5 indexing + importance scoring
4. Entities auto-extracted (files, URLs, packages, symbols)
5. Agent relates observations via knowledge graph (mem_relate)
6. Next session: agent searches memory, gets relevant context + graph connections
```

## Key Features

### Core Memory (Engram-Compatible)
- **Full-Text Search (FTS5)** — Lightning-fast search across all observations
- **Session Tracking** — Automatic session management with context aggregation
- **Topic Keys** — Upsert semantics for evolving observations (architecture decisions, etc.)
- **Passive Capture** — Extract learnings from agent output automatically
- **Deduplication** — SHA-256 content-based deduplication

### New in Cortex
- **Knowledge Graph** — Relate observations with typed edges (`references`, `relates_to`, `follows`, `supersedes`, `contradicts`). Traverse with BFS up to depth 10.
- **Importance Scoring** — Automatic scoring based on access patterns, recency, graph connections, observation type, and age. Formula: `base + accessBonus + recencyBonus + edgeBonus + typeBonus - agePenalty`, clamped to [0.0, 5.0].
- **Entity Linking** — Auto-extract files, URLs, packages, and symbols from observation content via regex.
- **Auto-Archival** — Periodically soft-delete old observations with low importance scores. Configurable thresholds.
- **Vector Search** — Optional hybrid search combining FTS5 with embedding similarity via RRF fusion (requires `cortex_vectors` build tag).
- **HTTP REST API** — 17 JSON endpoints for observations, sessions, search, graph, and scoring.

## MCP Tools (19 total)

### Core Tools (Engram-Compatible)

| Tool | Purpose |
|------|---------|
| `mem_save` | Save observation with What/Why/Where/Learned format |
| `mem_search` | Full-text search with filters |
| `mem_context` | Recent session context aggregation |
| `mem_session_summary` | End-of-session comprehensive save (**mandatory**) |
| `mem_get_observation` | Full content by ID |
| `mem_save_prompt` | Save user prompt for context |
| `mem_update` | Update observation by ID |
| `mem_suggest_topic_key` | Stable key for evolving topics |
| `mem_session_start` | Register session start |
| `mem_session_end` | Mark session complete |
| `mem_capture_passive` | Extract learnings from agent output |
| `mem_delete` | Soft or hard delete |
| `mem_stats` | Memory system statistics |
| `mem_timeline` | Chronological drill-in around observation |

### Cortex-Exclusive Tools

| Tool | Purpose |
|------|---------|
| `mem_relate` | Create typed relationship between observations |
| `mem_graph` | Traverse knowledge graph from an observation |
| `mem_score` | Get/recalculate importance score |
| `mem_archive` | Archive an observation (soft-delete) |
| `mem_search_hybrid` | Hybrid FTS5 + vector search with RRF fusion |

Full tool reference → [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)

## Cortex vs Engram

| Feature | Engram | Cortex |
|---------|:------:|:------:|
| FTS5 Search | :white_check_mark: | :white_check_mark: |
| Session Tracking | :white_check_mark: | :white_check_mark: |
| Topic Keys | :white_check_mark: | :white_check_mark: |
| Passive Capture | :white_check_mark: | :white_check_mark: |
| MCP Compatibility | :white_check_mark: | :white_check_mark: (100%) |
| Knowledge Graph | :x: | :white_check_mark: |
| Importance Scoring | :x: | :white_check_mark: |
| Auto-Archival | :x: | :white_check_mark: |
| Entity Linking | :x: | :white_check_mark: |
| Vector Search | :x: | :white_check_mark: (optional) |
| HTTP REST API | :white_check_mark: | :white_check_mark: |

Full comparison (including vs claude-mem) → [docs/COMPARISON.md](docs/COMPARISON.md)

## CLI Reference

| Command | Description |
|---------|-------------|
| `cortex setup [agent]` | Install agent integration |
| `cortex serve` | Start HTTP REST API (default: 7438) |
| `cortex mcp [--tools=PROFILE]` | Start MCP server (stdio) |
| `cortex tui` | Launch terminal UI |
| `cortex search <query>` | Search memories |
| `cortex save <title> <content>` | Save a memory |
| `cortex timeline <obs_id>` | Chronological context (--before/--after) |
| `cortex context [project]` | Recent session context |
| `cortex stats` | Memory statistics |
| `cortex export [--project P]` | Export to JSON |
| `cortex import --from-engram` | Import from Engram database |
| `cortex import --from-json` | Import from JSON file |
| `cortex migrate <up\|down\|status>` | Manage database migrations |
| `cortex version` | Show version |

## Configuration

Cortex reads from `cortex.yaml` with env var overrides (`CORTEX_<SECTION>_<KEY>`):

```yaml
database:
  path: cortex.db
  pragma:
    journal_mode: WAL

search:
  default_limit: 20
  vector: false       # Enable with cortex_vectors build tag

memory:
  auto_archive_days: 90
  min_archive_score: 0.1

lifecycle:
  enable_auto_archive: true
```

Full config reference → [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)

## Documentation

| Doc | Description |
|-----|-------------|
| [Installation](docs/INSTALLATION.md) | Homebrew, go install, binaries, Docker |
| [Agent Setup](docs/AGENT-SETUP.md) | Per-agent configuration + Memory Protocol |
| [Architecture](docs/ARCHITECTURE.md) | Design, scoring formula, graph, API, schema |
| [Plugins](docs/PLUGINS.md) | Claude Code hooks + OpenCode TypeScript plugin |
| [Comparison](docs/COMPARISON.md) | Why Cortex vs Engram vs claude-mem |
| [Contributing](CONTRIBUTING.md) | Contribution workflow + standards |

## License

MIT

---

**Built on the foundation of [Engram](https://github.com/Gentleman-Programming/engram)** — enhanced with knowledge graph, importance scoring, entity linking, and vector search for the next generation of AI coding assistants.

<p align="center">
  <img width="1024" height="340" alt="Cortex Logo" src="./assets/cortex-logo.png" />
</p>

<p align="center">
  <strong>Persistent memory for AI coding agents</strong><br>
  <em>Knowledge graph. Importance scoring. Agent-agnostic. Single binary. Zero dependencies.</em>
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

Built on the foundation of [Engram](https://github.com/Gentleman-Programming/engram), Cortex adds knowledge graph relationships, importance scoring, entity linking, auto-archival, and optional vector search while maintaining full API compatibility.

A **Go binary** with SQLite + FTS5 full-text search + Knowledge Graph, exposed via CLI, HTTP API, MCP server, and TUI. Works with **any agent** that supports MCP — Claude Code, OpenCode, Gemini CLI, Codex, VS Code (Copilot), Antigravity, Cursor, Windsurf, or anything else.

```
Agent (Claude Code / OpenCode / Gemini CLI / Codex / VS Code / Antigravity / ...)
    ↓ MCP stdio
Cortex (single Go binary)
    ↓
SQLite + FTS5 + Knowledge Graph (~/.cortex/cortex.db)
```

## Quick Start

### Install

```bash
brew install lleontor705/tap/cortex
```

Windows, Linux, and other install methods → [docs/INSTALLATION.md](docs/INSTALLATION.md)

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

## How It Works

```
1. Agent completes significant work (bugfix, architecture decision, etc.)
2. Agent calls mem_save → title, type, What/Why/Where/Learned
3. Cortex persists to SQLite with FTS5 indexing + importance scoring
4. Optional: Agent relates observations via knowledge graph
5. Next session: agent searches memory, gets relevant context + related observations
```

Full details on session lifecycle, topic keys, and knowledge graph → [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)

## MCP Tools

### Core Tools (Engram-Compatible)

| Tool | Purpose |
|------|---------|
| `mem_save` | Save observation with What/Why/Where/Learned format |
| `mem_update` | Update observation by ID |
| `mem_delete` | Soft or hard delete |
| `mem_suggest_topic_key` | Stable key for evolving topics |
| `mem_search` | Full-text search |
| `mem_session_summary` | End-of-session save |
| `mem_context` | Recent session context |
| `mem_timeline` | Chronological drill-in |
| `mem_get_observation` | Full content by ID |
| `mem_save_prompt` | Save user prompt |
| `mem_stats` | Memory statistics |
| `mem_session_start` | Register session start |
| `mem_session_end` | Mark session complete |
| `mem_capture_passive` | Extract learnings from agent output |

### Cortex-Exclusive Tools

| Tool | Purpose |
|------|---------|
| `mem_relate` | Create typed relationship between observations |
| `mem_graph` | Traverse knowledge graph from an observation |
| `mem_score` | Get/recalculate importance score |
| `mem_archive` | Archive an observation |
| `mem_search_hybrid` | Hybrid FTS5 + vector search with RRF fusion |

Full tool reference → [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)

## What Cortex Adds Over Engram

| Feature | Engram | Cortex |
|---------|--------|--------|
| FTS5 Search | yes | yes |
| Session Tracking | yes | yes |
| Topic Keys | yes | yes |
| Passive Capture | yes | yes |
| Knowledge Graph | - | yes |
| Importance Scoring | - | yes |
| Auto-Archival | - | yes |
| Entity Linking | - | yes |
| Vector Search | - | yes (optional) |
| HTTP REST API | yes | yes |
| MCP Compatibility | yes | yes (100%) |

## CLI Reference

| Command | Description |
|---------|-------------|
| `cortex setup [agent]` | Install agent integration |
| `cortex serve` | Start HTTP API (default: 7438) |
| `cortex mcp [--tools=PROFILE]` | Start MCP server (stdio) |
| `cortex tui` | Launch terminal UI |
| `cortex search <query>` | Search memories |
| `cortex save <title> <content>` | Save a memory |
| `cortex timeline <obs_id>` | Chronological context |
| `cortex context [project]` | Recent session context |
| `cortex stats` | Memory statistics |
| `cortex export [--project P]` | Export to JSON |
| `cortex import --from-engram` | Import from Engram database |
| `cortex import --from-json` | Import from JSON file |
| `cortex migrate <up\|down\|status>` | Manage migrations |
| `cortex version` | Show version |

## Documentation

| Doc | Description |
|-----|-------------|
| [Installation](docs/INSTALLATION.md) | All install methods + platform support |
| [Agent Setup](docs/AGENT-SETUP.md) | Per-agent configuration + Memory Protocol |
| [Architecture](docs/ARCHITECTURE.md) | How it works + MCP tools + project structure |
| [Plugins](docs/PLUGINS.md) | OpenCode & Claude Code plugin details |
| [Contributing](CONTRIBUTING.md) | Contribution workflow + standards |

## License

MIT

---

**Built on the foundation of [Engram](https://github.com/Gentleman-Programming/engram)** — enhanced with knowledge graph, importance scoring, entity linking, and vector search.

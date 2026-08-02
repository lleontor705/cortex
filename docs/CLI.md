# CLI Reference

The production entrypoint is `cmd/cortex`. Run `cortex help` for flags and `cortex <command> --help` where supported.

| Command | Purpose |
|---|---|
| `setup` | Install Claude Code, OpenCode, Gemini CLI, or Codex integration |
| `search` | Search local observations |
| `save` | Save a local observation |
| `context` | Show recent session context |
| `stats` | Show local statistics |
| `timeline` | Show local observation timeline |
| `revisions` | Show observation revisions |
| `tui` | Launch Bubble Tea UI |
| `serve` | Start local SQLite HTTP |
| `mcp` | Start local MCP stdio |
| `doctor` | Check database, FTS, graph, vectors, and orphans |
| `reindex` | Rebuild local vector/index data |
| `gc` | Remove archived local observations |
| `export` / `import` | Exchange observation data |
| `sync` | Synchronize configured chunks |
| `merge-projects` | Merge local project names |
| `migrate status` | Inspect migration state |

The local v2 baseline is forward-only. `migrate down` is not a supported normal operation and existing v2 databases must not be downgraded automatically.

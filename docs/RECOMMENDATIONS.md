[← Back to README](../README.md)

# Recommendations

Practical guidance for configuring Cortex based on your environment and needs.

## Embedding Provider Selection

Cortex supports multiple embedding providers. Choose based on your priorities:

### Decision Matrix

| Priority | Recommended Provider | Why |
|----------|---------------------|-----|
| **Privacy / air-gapped** | Ollama | 100% local, zero data leaves your machine |
| **Cost-sensitive** | Ollama | Free, no API charges |
| **Best temporal accuracy** | OpenAI | +42% on temporal reasoning vs. local models |
| **Fastest setup** | None (FTS5 only) | No dependencies, works immediately |
| **Enterprise / regulated** | Ollama | GDPR/CCPA compliant by design |
| **Maximum quality** | OpenAI | Higher-dimensional embeddings (1536d) |

### Provider Details

#### Ollama (Recommended Default)

```yaml
# cortex.yaml
search:
  embedding_provider: ollama
```

| Aspect | Value |
|--------|-------|
| Model | nomic-embed-text |
| Dimensions | 768 |
| Latency | ~90ms per embedding |
| Cost | $0 |
| Privacy | 100% local |
| Setup | `ollama pull nomic-embed-text` |
| Requirement | Ollama running (`ollama serve`) |

**Best for:** Individual developers, privacy-sensitive teams, offline environments.

**Setup:**
```bash
# Install Ollama
# macOS/Linux: curl -fsSL https://ollama.com/install.sh | sh
# Windows: download from https://ollama.com/download

# Pull the embedding model (274 MB, one-time download)
ollama pull nomic-embed-text

# Configure Cortex
cortex setup claude-code  # or your agent
```

**Alternative local models:**
| Model | Dimensions | Size | Quality |
|-------|-----------|------|---------|
| nomic-embed-text (default) | 768 | 274 MB | Best balance |
| mxbai-embed-large | 1024 | 670 MB | Higher quality, slower |
| all-minilm | 384 | 46 MB | Fastest, lower quality |

To use a different model:
```yaml
search:
  embedding_provider: ollama
  embedding_model: mxbai-embed-large
```

#### OpenAI

```yaml
# cortex.yaml
search:
  embedding_provider: openai
```

| Aspect | Value |
|--------|-------|
| Model | text-embedding-3-small |
| Dimensions | 1536 |
| Latency | ~200ms per embedding (network) |
| Cost | ~$0.02 per 1M tokens |
| Privacy | Data sent to OpenAI |
| Setup | Set `OPENAI_API_KEY` env var |

**Best for:** Teams already using OpenAI, maximum retrieval quality needed.

**Setup:**
```bash
export OPENAI_API_KEY=sk-...

# Configure Cortex
cortex setup claude-code
```

#### None (FTS5 Only)

```yaml
# cortex.yaml
search:
  embedding_provider: none   # or omit entirely
```

| Aspect | Value |
|--------|-------|
| Search | Keyword matching (BM25) |
| Latency | <1ms per search |
| Cost | $0 |
| Privacy | 100% local |
| Setup | Nothing extra |

**Best for:** Quick start, minimal dependencies, when keyword search is sufficient.

**Limitation:** Cannot find semantically similar content. "What artist did you mention?" will not find "Taylor Swift" unless those words appear in the same observation.

## Performance Tuning

### Memory Storage

| Setting | Default | Recommendation |
|---------|---------|---------------|
| `database.pragma.journal_mode` | WAL | Keep WAL for concurrent reads |
| `database.pragma.cache_size` | -2000 | Increase to -8000 for large databases |
| `database.pragma.mmap_size` | 0 | Set to 268435456 (256MB) for databases > 100MB |
| `search.default_limit` | 20 | Keep 20 for agent use, increase for batch |
| `memory.dedupe_window` | 5m | Increase to 15m if agents save frequently |

### Auto-Archival

```yaml
memory:
  auto_archive_days: 90      # Archive observations older than 90 days
  min_archive_score: 0.1     # Only if importance score < 0.1

lifecycle:
  enable_auto_archive: true
  archive_check_interval: 1h
```

**Recommendation:** Enable auto-archival for long-running projects. Archived observations are soft-deleted (recoverable via TUI Archive screen) and excluded from search by default.

### Knowledge Graph

| Use Case | Recommendation |
|----------|---------------|
| Small project (<100 observations) | Graph optional — FTS5 is sufficient |
| Medium project (100-1000) | Use `mem_relate` for key decisions and their rationale |
| Large project (1000+) | Essential — graph enables multi-hop retrieval |

**Edge types by priority:**
1. `supersedes` — Track when decisions are overridden (most valuable for temporal reasoning)
2. `references` — Link implementation to the decision that motivated it
3. `relates_to` — Connect observations in the same domain
4. `contradicts` — Flag conflicting information
5. `follows` — Sequential dependency chain

### Importance Scoring

The scoring formula is:
```
score = base(0.5) + typeBonus + accessBonus + recencyBonus + edgeBonus - agePenalty
```

**Observations that score highest:**
- Decisions (`+0.5` type bonus) accessed recently with many graph connections
- Bugfixes (`+0.3`) referenced by other observations

**Observations that get archived:**
- Old observations with no graph connections and no recent access

**Tip:** The agent should call `mem_relate` after saving related observations. Each edge adds `+0.2` to the importance score (up to `+1.0`), protecting connected observations from archival.

## Agent-Specific Setup

### Claude Code

```bash
cortex setup claude-code
```

Creates MCP config at `~/.claude/mcp/cortex.json` and adds tool permissions to `~/.claude/settings.json`.

**Post-setup:** Restart Claude Code. Verify with `claude plugin list`.

### OpenCode

```bash
cortex setup opencode
```

Creates MCP config at `~/.config/opencode/cortex-mcp.json` and copies the plugin to `~/.config/opencode/plugins/`.

**Post-setup:** Restart OpenCode. Plugin auto-loads from plugins directory.

### Gemini CLI

```bash
cortex setup gemini-cli
```

Creates MCP config at `~/.gemini/settings.json` and writes Memory Protocol to `~/.gemini/system.md`.

### Codex

```bash
cortex setup codex
```

Creates config at `~/.codex/config.toml` with MCP server, instructions, and compaction prompt.

### VS Code (Copilot)

```bash
code --add-mcp '{"name":"cortex","command":"cortex","args":["mcp"]}'
```

### Cursor / Windsurf / Any MCP Client

See [AGENT-SETUP.md](AGENT-SETUP.md) for detailed per-agent configuration.

## Migration from Engram

```bash
# One command — imports all sessions, observations, and prompts
cortex import --from-engram --path ~/.engram/engram.db

# Full migration (also reconfigures agents and uninstalls Engram)
bash scripts/migrate-from-engram.sh
```

Cortex is 100% API-compatible with Engram. All MCP tools (`mem_save`, `mem_search`, etc.) work identically. Cortex adds 5 exclusive tools (`mem_relate`, `mem_graph`, `mem_score`, `mem_archive`, `mem_search_hybrid`) without breaking existing workflows.

## Troubleshooting

| Problem | Solution |
|---------|----------|
| Ollama embeddings slow | Ensure Ollama is running (`ollama serve`). First embedding after model load is slower. |
| OpenAI rate limiting | Reduce concurrent saves. Cortex embeds in background goroutines. |
| FTS5 search misses relevant content | Enable vector search — FTS5 is keyword-only. |
| High memory usage | Reduce `cache_size`, disable `mmap_size`, or run `cortex gc` to clean archived observations. |
| Vector search returns poor results | Check that observations are embedded (`cortex stats --vectors`). New observations are embedded on save. |
| Agent not finding Cortex | Verify MCP config with `cortex setup <agent>`. Check that Cortex binary is in PATH. |

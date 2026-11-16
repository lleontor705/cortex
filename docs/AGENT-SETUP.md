# Agent Setup

Cortex works with any AI coding agent that supports MCP. Run `cortex setup <agent>` for automatic configuration.

```bash
# Check detected agents and installed integrations
cortex setup --list

# Install with modular profiles (dev: 11 tools, minimal: 5 tools, agent: 22 tools)
cortex setup claude-code --profile=dev
cortex setup opencode --profile=agent
```

## Claude Code

```bash
cortex setup claude-code [--profile=agent|dev|minimal]
```

This does:
- Registers a **user-scope** MCP server named `cortex` (`cortex mcp --tools=<profile>`)
  with `claude mcp add-json cortex … --scope user`. Without the `claude` CLI it merges
  `mcpServers.cortex` into `~/.claude.json`, preserving everything else (an
  unparseable file is left untouched and setup fails). Tools appear as
  `mcp__cortex__*`.
- Updates `~/.claude/settings.json` with the profile's tool allowlist for both
  `mcp__cortex__*` and the plugin namespace `mcp__plugin_cortex_cortex__*`.
- Removes `~/.claude/mcp/cortex.json` left by older versions (Claude Code never read it).

The server follows the runtime mode: in `remote` mode the same `cortex mcp` entry
proxies to your Cortex server, so no per-agent URL or token is needed (see
`cortex connect`).

### Plugin (Optional)

The plugin ships its own MCP server (`plugin/claude-code/.mcp.json`, tools
`mcp__plugin_cortex_cortex__*`, requires `cortex` on `PATH`). Use **either** the
plugin **or** `cortex setup claude-code` to avoid two copies of the same server;
if you install the plugin, remove the user-scope entry with
`claude mcp remove cortex --scope user`.

For full integration with hooks (session tracking, compaction recovery, save nudges):

```bash
claude plugin marketplace add lleontor705/cortex
claude plugin install cortex
```

The plugin provides:
- **SessionStart hook** — loads memory context automatically
- **Post-compaction hook** — recovers context after compaction
- **UserPromptSubmit hook** — first-message tool loading + 15-min save nudge
- **SubagentStop hook** — passive capture from subagent output
- **Stop hook** — marks session as ended
- **SKILL.md** — Memory Protocol injected into agent context

## OpenCode

```bash
cortex setup opencode [--profile=agent|dev|minimal]
```

This creates:
- `~/.config/opencode/cortex-mcp.json` — MCP server registration
- `~/.config/opencode/plugins/cortex.ts` — managed TypeScript plugin embedded in the Cortex binary

The OpenCode plugin provides:
- Session tracking via event hooks
- User prompt capture
- System prompt injection (Memory Protocol)
- Compaction recovery
- Passive capture from Task tool output
- Sub-agent session suppression

Setup always writes both files, including when Cortex was installed from a release archive or with `go install`. Re-run setup after upgrading Cortex to install the matching plugin version.

## Gemini CLI

```bash
cortex setup gemini-cli
```

This creates or updates:
- `~/.gemini/settings.json` — merges `mcpServers.cortex`; every other setting and
  MCP server is preserved. An unparseable file is left untouched and setup fails.
- `~/.gemini/system.md` — Memory Protocol system prompt (only written when the file
  is absent or was written by Cortex; a custom prompt is never replaced)

## Codex

```bash
cortex setup codex
```

This creates or updates:
- `~/.codex/config.toml` — replaces only the `[mcp_servers.cortex]` table and adds the
  root keys `model_instructions_file` / `experimental_compact_prompt_file` when you
  have not set them; the rest of the file is preserved. Invalid TOML is left untouched
  and setup fails.
- `~/.codex/cortex-instructions.md` — Memory Protocol instructions
- `~/.codex/cortex-compact-prompt.md` — Compaction recovery instructions

## VS Code (Manual)

```bash
code --add-mcp '{"name":"cortex","command":"cortex","args":["mcp"]}'
```

Or add to `.vscode/mcp.json`:
```json
{
  "servers": {
    "cortex": {
      "command": "cortex",
      "args": ["mcp", "--tools=agent"]
    }
  }
}
```

## Cursor / Windsurf / Any MCP Agent

Add to your agent's MCP configuration:

```json
{
  "mcpServers": {
    "cortex": {
      "command": "cortex",
      "args": ["mcp", "--tools=agent"]
    }
  }
}
```

## Tool Profiles

Control which tools are loaded:

```bash
  cortex mcp                          # Default: agent profile (22 canonical agent tools)
  cortex mcp --tools=agent            # Full agent suite (memory, graph, AST, blast radius, handoff)
  cortex mcp --tools=dev              # Developer profile (11 tools: memory + AST/blast radius/tests)
  cortex mcp --tools=minimal          # Minimalist profile (5 essential memory tools for fast models)
  cortex mcp --tools=cortex_save,cortex_search  # Individual tools
```

Server deployments expose an authenticated subset of the Cortex-native namespace
through Streamable HTTP at `/mcp`. The server does not load the local profiles;
see [MCP.md](MCP.md) for its exact catalog. Use a bearer token and follow the active schema.

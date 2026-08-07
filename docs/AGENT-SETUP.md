# Agent Setup

Cortex works with any AI coding agent that supports MCP. Run `cortex setup <agent>` for automatic configuration.

## Claude Code

```bash
cortex setup claude-code
```

This creates:
- `~/.claude/mcp/cortex.json` — MCP server registration (durable, survives plugin updates)
- Updates `~/.claude/settings.json` — adds tool allowlists for auto-approval

### Plugin (Optional)

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
cortex setup opencode
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

This creates:
- `~/.gemini/settings.json` — MCP server registration
- `~/.gemini/system.md` — Memory Protocol system prompt

## Codex

```bash
cortex setup codex
```

This creates:
- `~/.codex/config.toml` — MCP server registration
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
  cortex mcp                          # All local tools (default)
  cortex mcp --tools=agent            # Agent profile for coding sessions
  cortex mcp --tools=admin            # Admin profile for curation
  cortex mcp --tools=temporal         # Temporal and observability profile
  cortex mcp --tools=agent,admin      # Combine profiles
  cortex mcp --tools=cortex_save,cortex_search  # Individual tools
```

Server deployments expose an authenticated subset of the Cortex-native namespace
through Streamable HTTP at `/mcp`. The server does not load the local profiles;
see [MCP.md](MCP.md) for its exact ten-tool catalog. Use a bearer token and do
not send `mem_*` tool names.

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
- `~/.config/opencode/plugins/cortex.ts` — TypeScript plugin (if available)

The OpenCode plugin provides:
- Session tracking via event hooks
- User prompt capture
- System prompt injection (Memory Protocol)
- Compaction recovery
- Passive capture from Task tool output
- Sub-agent session suppression

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
cortex mcp                          # All 19 tools (default)
cortex mcp --tools=agent            # 15 agent tools (for coding sessions)
cortex mcp --tools=admin            # 4 admin tools (delete, stats, timeline, archive)
cortex mcp --tools=agent,admin      # Combine profiles
cortex mcp --tools=mem_save,mem_search  # Individual tools
```

# Plugins

Cortex provides deep agent integration through native plugins for Claude Code and OpenCode.

## Claude Code Plugin

The Claude Code plugin adds lifecycle hooks that automate memory management.

### Bare MCP vs Plugin

| Feature | Bare MCP | Plugin |
|---------|----------|--------|
| Memory tools | yes | yes |
| Auto session tracking | - | yes |
| Compaction recovery | - | yes |
| Save nudge (15 min) | - | yes |
| Passive capture | - | yes |
| Memory Protocol injection | - | yes |
| ToolSearch auto-loading | - | yes |

### Plugin Structure

```
plugin/claude-code/
  .claude-plugin/plugin.json    Plugin descriptor
  .mcp.json                     MCP server config
  hooks/hooks.json              5 lifecycle hooks
  scripts/
    _helpers.sh                 Shared helpers (project detection)
    session-start.sh            Load memory context on startup
    post-compaction.sh          Recover context after compaction
    user-prompt-submit.sh       First-message tool loading + save nudge
    subagent-stop.sh            Passive capture from subagent output
    session-stop.sh             Mark session as ended
  skills/memory/SKILL.md        Memory Protocol for the agent
```

### Lifecycle Hooks

#### SessionStart (startup | clear)
1. Ensures cortex HTTP server is running
2. Creates session via HTTP API
3. Fetches memory context for the project
4. Injects Memory Protocol + context into Claude

#### SessionStart (compact)
1. Ensures session exists
2. Injects Memory Protocol + compaction recovery instructions
3. Instructs agent to: save compacted summary → load context → then continue

#### UserPromptSubmit
- **First message**: Injects ToolSearch to load all cortex MCP tools
- **Subsequent**: If > 15 min since last save and session > 5 min old, nudges agent to save

#### SubagentStop (async)
- Captures subagent output and saves as passive observation

#### Stop (async)
- Marks session as ended via HTTP API

### Memory Protocol

The SKILL.md file defines mandatory behaviors for the agent:

- **Proactive saves**: After decisions, bugfixes, discoveries, patterns, preferences
- **Search triggers**: When user recalls, when starting related work, on first message
- **Session close**: Mandatory `cortex_session_summary` with Goal/Discoveries/Accomplished/Next Steps/Relevant Files
- **Knowledge graph**: Use `cortex_relate` to connect related observations
- **Compaction recovery**: 4-step mandatory protocol

## OpenCode Plugin

The TypeScript plugin (`plugin/opencode/cortex.ts`) connects OpenCode's event system to Cortex.

### Features

- **Auto-start**: Detects if cortex server is running, starts it if not
- **Session tracking**: Creates sessions on `session.created` events
- **Sub-agent suppression**: Detects Task() sub-agents and skips session registration
- **User prompt capture**: Saves prompts via `chat.message` hook
- **Tool tracking**: Counts non-Cortex tool calls per session
- **Passive capture**: Extracts learnings from Task tool output
- **Memory Protocol injection**: Appends to system prompt via `experimental.chat.system.transform`
- **Compaction recovery**: Injects context + instructions via `experimental.session.compacting`

### Binary Path Resolution

The plugin uses a 3-tier fallback for the cortex binary:
1. `CORTEX_BIN` environment variable (explicit override)
2. `Bun.which("cortex")` (runtime PATH lookup)
3. Absolute baked-in path (headless/systemd fallback)

### Local Model Compatibility

The system prompt is appended to the last existing system message (not pushed as a new one) to avoid breaking models that only allow a single system block (Qwen, Mistral via llama.cpp).

## Privacy

Content wrapped in `<private>...</private>` tags is stripped by the OpenCode plugin before its HTTP call. This is plugin-specific behavior; other MCP/HTTP clients do not automatically redact those tags.

```
<private>API_KEY=sk-1234</private>  →  [REDACTED]
```

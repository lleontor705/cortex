// Package setup installs Cortex integrations for AI coding agents.
//
// Supported agents: claude-code, opencode, gemini-cli, codex.
// Each agent gets MCP server registration plus agent-specific configuration
// (Memory Protocol instructions, compaction recovery, tool allowlists).
package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cortexMCPTools lists the tool permission names for Claude Code's settings.json.
var cortexMCPTools = []string{
	"mcp__plugin_cortex_cortex__mem_capture_passive",
	"mcp__plugin_cortex_cortex__mem_context",
	"mcp__plugin_cortex_cortex__mem_get_observation",
	"mcp__plugin_cortex_cortex__mem_save",
	"mcp__plugin_cortex_cortex__mem_save_prompt",
	"mcp__plugin_cortex_cortex__mem_search",
	"mcp__plugin_cortex_cortex__mem_session_end",
	"mcp__plugin_cortex_cortex__mem_session_start",
	"mcp__plugin_cortex_cortex__mem_session_summary",
	"mcp__plugin_cortex_cortex__mem_suggest_topic_key",
	"mcp__plugin_cortex_cortex__mem_update",
}

// memoryProtocol is the Memory Protocol instructions injected into agents.
const memoryProtocol = `## Cortex Persistent Memory — Protocol

You have cortex memory tools. Save decisions, bugs, discoveries PROACTIVELY — do NOT wait.

### WHEN TO SAVE (mandatory after each):
- Architecture/design decision made
- Bug fixed (include root cause)
- Non-obvious discovery or gotcha found
- Pattern or convention established
- User preference or constraint learned
- Feature implemented with non-obvious approach

### SEARCH MEMORY when:
- User asks to recall anything
- Starting work on something that might have been done before
- User's FIRST message references the project

### SESSION CLOSE — before saying "done":
Call mem_session_summary with: Goal, Discoveries, Accomplished, Next Steps, Relevant Files.

### KNOWLEDGE GRAPH:
After saving related observations, use mem_relate to create relationships.
Use mem_graph to explore connections.
`

// compactPrompt is the compaction recovery instruction.
const compactPrompt = `FIRST ACTION REQUIRED — context was compacted. Follow these steps IN ORDER:

1. Call mem_session_summary with the compacted summary above to persist it.
2. Call mem_context to recover recent session history.
3. Call mem_search if you need more detail on a specific topic.
4. Only THEN continue working.

All steps are MANDATORY. Without them, you lose context and start blind.
`

// Result contains the paths of files created/modified during setup.
type Result struct {
	Paths   []string
	Message string
}

// SupportedAgents returns descriptions of supported agents.
func SupportedAgents() map[string]string {
	return map[string]string{
		"claude-code": "Claude Code — Native plugin via marketplace (hooks, skills, MCP, compaction recovery)",
		"opencode":    "OpenCode — MCP registration with Memory Protocol",
		"gemini-cli":  "Gemini CLI — MCP registration plus system prompt compaction recovery",
		"codex":       "Codex — MCP registration plus model/compaction instruction files",
	}
}

// Install sets up Cortex integration for the given agent.
func Install(agent string) (string, error) {
	home, err := resolveHome()
	if err != nil {
		return "", err
	}

	bin := resolveBinaryPath()

	switch agent {
	case "claude-code":
		return installClaudeCode(home, bin)
	case "opencode":
		return installOpenCode(home, bin)
	case "gemini-cli":
		return installGeminiCLI(home, bin)
	case "codex":
		return installCodex(home, bin)
	default:
		return "", fmt.Errorf("unsupported agent: %s\nSupported: claude-code, opencode, gemini-cli, codex", agent)
	}
}

// ─── Claude Code ────────────────────────────────────────────────────────────

func installClaudeCode(home, bin string) (string, error) {
	// Write durable MCP config at user level (survives plugin updates)
	mcpPath := filepath.Join(home, ".claude", "mcp", "cortex.json")
	mcpContent := fmt.Sprintf(`{
  "name": "cortex",
  "type": "stdio",
  "command": %s,
  "args": ["mcp", "--tools=agent"]
}
`, jsonString(bin))

	if err := writeFile(mcpPath, mcpContent); err != nil {
		return "", fmt.Errorf("write MCP config: %w", err)
	}

	// Add tool allowlist to settings.json
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	addClaudeCodeAllowlist(settingsPath)

	return mcpPath, nil
}

// addClaudeCodeAllowlist adds cortex MCP tool permissions to Claude Code settings.
func addClaudeCodeAllowlist(settingsPath string) {
	// Read existing settings
	var settings map[string]interface{}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		settings = make(map[string]interface{})
	} else {
		if json.Unmarshal(data, &settings) != nil {
			settings = make(map[string]interface{})
		}
	}

	// Get or create permissions.allow array
	perms, _ := settings["permissions"].(map[string]interface{})
	if perms == nil {
		perms = make(map[string]interface{})
	}

	allowList, _ := perms["allow"].([]interface{})
	existing := make(map[string]bool)
	for _, v := range allowList {
		if s, ok := v.(string); ok {
			existing[s] = true
		}
	}

	// Add missing tools
	for _, tool := range cortexMCPTools {
		if !existing[tool] {
			allowList = append(allowList, tool)
		}
	}

	perms["allow"] = allowList
	settings["permissions"] = perms

	// Write back
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return
	}
	_ = writeFile(settingsPath, string(out)+"\n")
}

// ─── OpenCode ───────────────────────────────────────────────────────────────

func installOpenCode(home, bin string) (string, error) {
	configDir := filepath.Join(home, ".config", "opencode")

	// Write MCP config
	mcpPath := filepath.Join(configDir, "cortex-mcp.json")
	mcpContent := fmt.Sprintf(`{
  "mcp": {
    "cortex": {
      "type": "local",
      "command": [%s, "mcp", "--tools=agent"],
      "enabled": true
    }
  }
}
`, jsonString(bin))

	if err := writeFile(mcpPath, mcpContent); err != nil {
		return "", err
	}

	// Copy plugin file if available (from plugin/opencode/cortex.ts relative to binary)
	pluginDir := filepath.Join(configDir, "plugins")
	pluginDst := filepath.Join(pluginDir, "cortex.ts")

	// Try to find plugin source relative to the binary
	binDir := filepath.Dir(bin)
	candidates := []string{
		filepath.Join(binDir, "..", "plugin", "opencode", "cortex.ts"),
		filepath.Join(binDir, "plugin", "opencode", "cortex.ts"),
	}

	for _, src := range candidates {
		data, err := os.ReadFile(src)
		if err == nil {
			// Patch CORTEX_BIN with resolved binary path
			patched := strings.Replace(
				string(data),
				`return "cortex"`,
				fmt.Sprintf(`return %s`, jsonString(bin)),
				1,
			)
			_ = writeFile(pluginDst, patched)
			break
		}
	}

	return mcpPath, nil
}

// ─── Gemini CLI ─────────────────────────────────────────────────────────────

func installGeminiCLI(home, bin string) (string, error) {
	configPath := filepath.Join(home, ".gemini", "settings.json")
	content := fmt.Sprintf(`{
  "mcpServers": {
    "cortex": {
      "command": %s,
      "args": ["mcp", "--tools=agent"]
    }
  }
}
`, jsonString(bin))

	if err := writeFile(configPath, content); err != nil {
		return "", err
	}

	// Write system prompt with Memory Protocol
	systemPath := filepath.Join(home, ".gemini", "system.md")
	_ = writeFile(systemPath, memoryProtocol)

	return configPath, nil
}

// ─── Codex ──────────────────────────────────────────────────────────────────

func installCodex(home, bin string) (string, error) {
	configDir := filepath.Join(home, ".codex")
	configPath := filepath.Join(configDir, "config.toml")

	content := fmt.Sprintf(`[mcp_servers.cortex]
command = %s
args = ["mcp", "--tools=agent"]

model_instructions_file = "%s"
experimental_compact_prompt_file = "%s"
`, jsonString(bin),
		filepath.Join(configDir, "cortex-instructions.md"),
		filepath.Join(configDir, "cortex-compact-prompt.md"))

	if err := writeFile(configPath, content); err != nil {
		return "", err
	}

	// Write instruction files
	_ = writeFile(filepath.Join(configDir, "cortex-instructions.md"), memoryProtocol)
	_ = writeFile(filepath.Join(configDir, "cortex-compact-prompt.md"), compactPrompt)

	return configPath, nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// resolveBinaryPath returns the absolute path to the cortex binary.
// Uses os.Executable() with symlink resolution for stability across
// package manager upgrades and headless environments.
func resolveBinaryPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "cortex"
	}

	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe
	}

	return resolved
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func resolveHome() (string, error) {
	if v := os.Getenv("HOME"); v != "" {
		return v, nil
	}
	if v := os.Getenv("USERPROFILE"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return home, nil
}

// jsonString returns a JSON-encoded string value for embedding in config files.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}


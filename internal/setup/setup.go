// Package setup installs Cortex integrations for AI coding agents.
//
// Supported agents: claude-code, opencode, gemini-cli, codex.
// Each agent gets MCP server registration plus agent-specific configuration
// (Memory Protocol instructions, compaction recovery, tool allowlists).
package setup

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	opencodeplugin "github.com/lleontor705/cortex/v2/plugin/opencode"
)

// cortexMCPTools lists the tool permission names for Claude Code's settings.json.
var cortexMCPTools = []string{
	"mcp__plugin_cortex_cortex__cortex_analyze_architecture",
	"mcp__plugin_cortex_cortex__cortex_capture_passive",
	"mcp__plugin_cortex_cortex__cortex_code_analyze",
	"mcp__plugin_cortex_cortex__cortex_code_find",
	"mcp__plugin_cortex_cortex__cortex_code_graph",
	"mcp__plugin_cortex_cortex__cortex_code_impact",
	"mcp__plugin_cortex_cortex__cortex_code_map",
	"mcp__plugin_cortex_cortex__cortex_code_scan",
	"mcp__plugin_cortex_cortex__cortex_code_symbols",
	"mcp__plugin_cortex_cortex__cortex_code_tests",
	"mcp__plugin_cortex_cortex__cortex_consolidate",
	"mcp__plugin_cortex_cortex__cortex_context",
	"mcp__plugin_cortex_cortex__cortex_detect_cycles",
	"mcp__plugin_cortex_cortex__cortex_find_symbols",
	"mcp__plugin_cortex_cortex__cortex_get_agent_context",
	"mcp__plugin_cortex_cortex__cortex_get_blast_radius",
	"mcp__plugin_cortex_cortex__cortex_get_code_graph",
	"mcp__plugin_cortex_cortex__cortex_get_code_map",
	"mcp__plugin_cortex_cortex__cortex_get_code_symbols",
	"mcp__plugin_cortex_cortex__cortex_get_impacted_tests",
	"mcp__plugin_cortex_cortex__cortex_get_observation",
	"mcp__plugin_cortex_cortex__cortex_get_rules",
	"mcp__plugin_cortex_cortex__cortex_get_status",
	"mcp__plugin_cortex_cortex__cortex_graph",
	"mcp__plugin_cortex_cortex__cortex_ingest_code",
	"mcp__plugin_cortex_cortex__cortex_project_dna",
	"mcp__plugin_cortex_cortex__cortex_relate",
	"mcp__plugin_cortex_cortex__cortex_revision_history",
	"mcp__plugin_cortex_cortex__cortex_save",
	"mcp__plugin_cortex_cortex__cortex_save_prompt",
	"mcp__plugin_cortex_cortex__cortex_save_rule",
	"mcp__plugin_cortex_cortex__cortex_score",
	"mcp__plugin_cortex_cortex__cortex_search",
	"mcp__plugin_cortex_cortex__cortex_search_hybrid",
	"mcp__plugin_cortex_cortex__cortex_session_end",
	"mcp__plugin_cortex_cortex__cortex_session_start",
	"mcp__plugin_cortex_cortex__cortex_session_summary",
	"mcp__plugin_cortex_cortex__cortex_suggest_topic_key",
	"mcp__plugin_cortex_cortex__cortex_update",
}

// memoryProtocol is the Memory Protocol instructions injected into agents.
const memoryProtocol = `## Cortex Persistent Memory -- Protocol

You have cortex memory tools. Save decisions, bugs, discoveries PROACTIVELY -- do NOT wait.

TRANSPORT IDS: Follow the active MCP tool schema. Local observation/graph IDs are numeric; Cortex Server IDs are public UUID strings. Never convert or reuse IDs across transports.

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

### SESSION CLOSE -- before saying "done":
Call cortex_session_summary with: Goal, Discoveries, Accomplished, Next Steps, Relevant Files.

### KNOWLEDGE GRAPH:
After saving related observations, use cortex_relate to create relationships.
Use cortex_graph to explore connections.
`

// compactPrompt is the compaction recovery instruction.
const compactPrompt = `FIRST ACTION REQUIRED -- context was compacted. Follow these steps IN ORDER:

1. Call cortex_session_summary with the compacted summary above to persist it.
2. Call cortex_context to recover recent session history.
3. Call cortex_search if you need more detail on a specific topic.
4. Only THEN continue working.

All steps are MANDATORY. Without them, you lose context and start blind.
`

// Result contains information about a completed setup.
type Result struct {
	Agent       string
	Destination string
	Files       int
}

// Agent describes a supported agent for setup.
type Agent struct {
	Name        string
	Description string
	InstallDir  string
}

// AgentStatus describes the live installation and configuration state of an agent.
type AgentStatus struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	InstallDir  string `json:"install_dir"`
	Detected    bool   `json:"detected"`
	Configured  bool   `json:"configured"`
	StatusText  string `json:"status_text"`
}

// DetectAgents inspects the environment and reports installation/configuration status for supported agents.
func DetectAgents() []AgentStatus {
	home, _ := resolveHome()
	statuses := make([]AgentStatus, 0, 5)

	// 1. Claude Code
	claudeDir := filepath.Join(home, ".claude")
	claudeDetected := dirOrFileExists(claudeDir) || commandExists("claude")
	claudeMcp := filepath.Join(claudeDir, "mcp", "cortex.json")
	claudeConfigured := fileExists(claudeMcp)
	statusClaude := "Not detected"
	if claudeConfigured {
		statusClaude = "Configured (Plugin Active)"
	} else if claudeDetected {
		statusClaude = "Detected (Ready to install)"
	}
	statuses = append(statuses, AgentStatus{
		Name:        "claude-code",
		DisplayName: "Claude Code",
		Description: "Native plugin via marketplace (hooks, skills, MCP, compaction recovery)",
		InstallDir:  filepath.Join(claudeDir, "mcp"),
		Detected:    claudeDetected,
		Configured:  claudeConfigured,
		StatusText:  statusClaude,
	})

	// 2. OpenCode
	opencodeDir := filepath.Join(home, ".config", "opencode")
	opencodeAlt := filepath.Join(home, ".opencode")
	opencodeDetected := dirOrFileExists(opencodeDir) || dirOrFileExists(opencodeAlt) || commandExists("opencode")
	opencodePlugin := filepath.Join(opencodeDir, "plugins", "cortex.ts")
	opencodeAltPlugin := filepath.Join(opencodeAlt, "plugins", "cortex.ts")
	opencodeConfigured := fileExists(opencodePlugin) || fileExists(opencodeAltPlugin)
	statusOpenCode := "Not detected"
	if opencodeConfigured {
		statusOpenCode = "Configured (Plugin Active)"
	} else if opencodeDetected {
		statusOpenCode = "Detected (Ready to install)"
	}
	statuses = append(statuses, AgentStatus{
		Name:        "opencode",
		DisplayName: "OpenCode",
		Description: "MCP registration with Memory Protocol",
		InstallDir:  opencodeDir,
		Detected:    opencodeDetected,
		Configured:  opencodeConfigured,
		StatusText:  statusOpenCode,
	})

	// 3. Gemini CLI
	geminiDir := filepath.Join(home, ".gemini")
	geminiDetected := dirOrFileExists(geminiDir) || commandExists("gemini")
	geminiConfig := filepath.Join(geminiDir, "antigravity", "mcp.json")
	geminiConfigured := fileExists(geminiConfig)
	statusGemini := "Not detected"
	if geminiConfigured {
		statusGemini = "Configured (MCP Registered)"
	} else if geminiDetected {
		statusGemini = "Detected (Ready to install)"
	}
	statuses = append(statuses, AgentStatus{
		Name:        "gemini-cli",
		DisplayName: "Gemini CLI",
		Description: "MCP registration plus system prompt compaction recovery",
		InstallDir:  geminiDir,
		Detected:    geminiDetected,
		Configured:  geminiConfigured,
		StatusText:  statusGemini,
	})

	// 4. Codex
	codexDir := filepath.Join(home, ".codex")
	codexDetected := dirOrFileExists(codexDir) || commandExists("codex")
	codexConfig := filepath.Join(codexDir, "config.toml")
	codexConfigured := fileExists(codexConfig)
	statusCodex := "Not detected"
	if codexConfigured {
		statusCodex = "Configured"
	} else if codexDetected {
		statusCodex = "Detected (Ready to install)"
	}
	statuses = append(statuses, AgentStatus{
		Name:        "codex",
		DisplayName: "Codex",
		Description: "MCP registration plus model/compaction instruction files",
		InstallDir:  codexDir,
		Detected:    codexDetected,
		Configured:  codexConfigured,
		StatusText:  statusCodex,
	})

	// 5. Ollama Local Embeddings
	ollamaDetected := commandExists("ollama")
	client := &http.Client{Timeout: 350 * time.Millisecond}
	resp, err := client.Get("http://localhost:11434/api/tags")
	ollamaOnline := err == nil && resp != nil && resp.StatusCode == http.StatusOK
	if resp != nil {
		_ = resp.Body.Close()
	}
	statusOllama := "Not detected"
	if ollamaOnline {
		statusOllama = "Online (localhost:11434 ready)"
	} else if ollamaDetected {
		statusOllama = "Installed (daemon offline)"
	}
	statuses = append(statuses, AgentStatus{
		Name:        "ollama",
		DisplayName: "Ollama Embeddings",
		Description: "Local Zero-Token Vector Embedding Engine (nomic-embed-text)",
		InstallDir:  "http://localhost:11434",
		Detected:    ollamaDetected || ollamaOnline,
		Configured:  ollamaOnline,
		StatusText:  statusOllama,
	})

	return statuses
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirOrFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// SupportedAgents returns the list of agents that can be set up.
func SupportedAgents() []Agent {
	home, _ := resolveHome()
	return []Agent{
		{
			Name:        "claude-code",
			Description: "Claude Code -- Native plugin via marketplace (hooks, skills, MCP, compaction recovery)",
			InstallDir:  filepath.Join(home, ".claude", "mcp"),
		},
		{
			Name:        "opencode",
			Description: "OpenCode -- MCP registration with Memory Protocol",
			InstallDir:  filepath.Join(home, ".config", "opencode"),
		},
		{
			Name:        "gemini-cli",
			Description: "Gemini CLI -- MCP registration plus system prompt compaction recovery",
			InstallDir:  filepath.Join(home, ".gemini"),
		},
		{
			Name:        "codex",
			Description: "Codex -- MCP registration plus model/compaction instruction files",
			InstallDir:  filepath.Join(home, ".codex"),
		},
	}
}

// Install sets up Cortex integration for the given agent.
func Install(agent string) (*Result, error) {
	home, err := resolveHome()
	if err != nil {
		return nil, err
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
	case "ollama":
		res, err := SetupOllama("http://localhost:11434", "nomic-embed-text")
		if err != nil {
			return nil, err
		}
		return &Result{Agent: "ollama", Destination: res.ConfigPath, Files: 1}, nil
	default:
		return nil, fmt.Errorf("unsupported agent: %s\nSupported: claude-code, opencode, gemini-cli, codex, ollama", agent)
	}
}

// AddClaudeCodeAllowlist adds cortex MCP tool permissions to Claude Code settings.
// It resolves the settings path automatically.
func AddClaudeCodeAllowlist() error {
	home, err := resolveHome()
	if err != nil {
		return err
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	addClaudeCodeAllowlist(settingsPath)
	return nil
}

// --- Claude Code ------------------------------------------------------------

func installClaudeCode(home, bin string) (*Result, error) {
	mcpPath := filepath.Join(home, ".claude", "mcp", "cortex.json")
	mcpContent := fmt.Sprintf(`{
  "name": "cortex",
  "type": "stdio",
  "command": %s,
  "args": ["mcp", "--tools=agent"]
}
`, jsonString(bin))

	if err := writeFile(mcpPath, mcpContent); err != nil {
		return nil, fmt.Errorf("write MCP config: %w", err)
	}

	// Add tool allowlist to settings.json
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	addClaudeCodeAllowlist(settingsPath)

	return &Result{Agent: "claude-code", Destination: mcpPath, Files: 1}, nil
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
	if err := writeFile(settingsPath, string(out)+"\n"); err != nil {
		log.Printf("setup: failed to write settings %s: %v", settingsPath, err)
	}
}

// --- OpenCode ---------------------------------------------------------------

func installOpenCode(home, bin string) (*Result, error) {
	configDir := filepath.Join(home, ".config", "opencode")

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
		return nil, err
	}

	pluginDir := filepath.Join(configDir, "plugins")
	pluginDst := filepath.Join(pluginDir, "cortex.ts")
	source := opencodeplugin.Source()
	const binaryFallback = `return "cortex"`
	if strings.Count(source, binaryFallback) != 1 {
		return nil, fmt.Errorf("setup opencode: embedded plugin binary fallback is missing or ambiguous")
	}
	patched := strings.Replace(source, binaryFallback, fmt.Sprintf(`return %s`, jsonString(bin)), 1)
	if err := writeFile(pluginDst, patched); err != nil {
		return nil, err
	}

	return &Result{Agent: "opencode", Destination: mcpPath, Files: 2}, nil
}

// --- Gemini CLI -------------------------------------------------------------

func installGeminiCLI(home, bin string) (*Result, error) {
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
		return nil, err
	}

	files := 1
	systemPath := filepath.Join(home, ".gemini", "system.md")
	if err := writeFile(systemPath, memoryProtocol); err != nil {
		log.Printf("setup: failed to write system prompt %s: %v", systemPath, err)
	} else {
		files++
	}

	return &Result{Agent: "gemini-cli", Destination: configPath, Files: files}, nil
}

// --- Codex ------------------------------------------------------------------

func installCodex(home, bin string) (*Result, error) {
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
		return nil, err
	}

	files := 1
	if err := writeFile(filepath.Join(configDir, "cortex-instructions.md"), memoryProtocol); err != nil {
		log.Printf("setup: failed to write instructions: %v", err)
	} else {
		files++
	}
	if err := writeFile(filepath.Join(configDir, "cortex-compact-prompt.md"), compactPrompt); err != nil {
		log.Printf("setup: failed to write compact prompt: %v", err)
	} else {
		files++
	}

	return &Result{Agent: "codex", Destination: configPath, Files: files}, nil
}

// --- Helpers ----------------------------------------------------------------

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

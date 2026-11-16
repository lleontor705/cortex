// Package setup installs Cortex integrations for AI coding agents.
//
// Supported agents: claude-code, opencode, gemini-cli, codex.
// Each agent gets MCP server registration plus agent-specific configuration
// (Memory Protocol instructions, compaction recovery, tool allowlists).
package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	opencodeplugin "github.com/lleontor705/cortex/v2/plugin/opencode"
)

// cortexMCPTools lists the tool permission names for Claude Code's settings.json.
var cortexMCPTools = []string{
	"mcp__plugin_cortex_cortex__cortex_analyze_architecture",
	"mcp__plugin_cortex_cortex__cortex_code_map",
	"mcp__plugin_cortex_cortex__cortex_code_tests",
	"mcp__plugin_cortex_cortex__cortex_context",
	"mcp__plugin_cortex_cortex__cortex_detect_cycles",
	"mcp__plugin_cortex_cortex__cortex_get_agent_context",
	"mcp__plugin_cortex_cortex__cortex_get_blast_radius",
	"mcp__plugin_cortex_cortex__cortex_get_code_symbols",
	"mcp__plugin_cortex_cortex__cortex_get_observation",
	"mcp__plugin_cortex_cortex__cortex_get_rules",
	"mcp__plugin_cortex_cortex__cortex_get_status",
	"mcp__plugin_cortex_cortex__cortex_graph",
	"mcp__plugin_cortex_cortex__cortex_graph_path",
	"mcp__plugin_cortex_cortex__cortex_handoff",
	"mcp__plugin_cortex_cortex__cortex_ingest_code",
	"mcp__plugin_cortex_cortex__cortex_relate",
	"mcp__plugin_cortex_cortex__cortex_revision_history",
	"mcp__plugin_cortex_cortex__cortex_save",
	"mcp__plugin_cortex_cortex__cortex_save_rule",
	"mcp__plugin_cortex_cortex__cortex_search",
	"mcp__plugin_cortex_cortex__cortex_session_summary",
	"mcp__plugin_cortex_cortex__cortex_update",
}

// cortexDevMCPTools lists the tool permission names for Claude Code's settings.json in the dev profile.
var cortexDevMCPTools = []string{
	"mcp__plugin_cortex_cortex__cortex_save",
	"mcp__plugin_cortex_cortex__cortex_search",
	"mcp__plugin_cortex_cortex__cortex_context",
	"mcp__plugin_cortex_cortex__cortex_session_summary",
	"mcp__plugin_cortex_cortex__cortex_get_observation",
	"mcp__plugin_cortex_cortex__cortex_get_agent_context",
	"mcp__plugin_cortex_cortex__cortex_relate",
	"mcp__plugin_cortex_cortex__cortex_get_rules",
	"mcp__plugin_cortex_cortex__cortex_ingest_code",
	"mcp__plugin_cortex_cortex__cortex_get_blast_radius",
	"mcp__plugin_cortex_cortex__cortex_code_tests",
}

// cortexMinimalMCPTools lists the tool permission names for Claude Code's settings.json in the minimal profile.
var cortexMinimalMCPTools = []string{
	"mcp__plugin_cortex_cortex__cortex_save",
	"mcp__plugin_cortex_cortex__cortex_search",
	"mcp__plugin_cortex_cortex__cortex_context",
	"mcp__plugin_cortex_cortex__cortex_session_summary",
	"mcp__plugin_cortex_cortex__cortex_get_observation",
}

// Options specifies customization options for agent installation.
type Options struct {
	Profile string // Profile name or tool filter (e.g. "dev", "minimal", "agent")
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
	claudeConfigured := isClaudeCodeConfigured(home)
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
		InstallDir:  filepath.Join(home, ".claude.json"),
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
	geminiConfigured := isGeminiConfigured(filepath.Join(geminiDir, "settings.json"))
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
	codexConfigured := isCodexConfigured(filepath.Join(codexDir, "config.toml"))
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
			InstallDir:  filepath.Join(home, ".claude.json"),
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

// Install sets up Cortex integration for the given agent using default options.
func Install(agent string) (*Result, error) {
	return InstallWithOptions(agent, Options{})
}

// InstallWithOptions sets up Cortex integration for the given agent with specific options.
func InstallWithOptions(agent string, opts Options) (*Result, error) {
	home, err := resolveHome()
	if err != nil {
		return nil, err
	}

	bin := resolveBinaryPath()

	switch agent {
	case "claude-code":
		return installClaudeCodeWithOptions(home, bin, opts)
	case "opencode":
		return installOpenCodeWithOptions(home, bin, opts)
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

// claudeMCPCommand runs `claude mcp ...` (a seam so tests never touch a real
// Claude Code installation). It returns errClaudeCLIUnavailable when the
// claude binary is not on PATH.
var claudeMCPCommand = func(args ...string) error {
	if !commandExists("claude") {
		return errClaudeCLIUnavailable
	}
	out, err := exec.Command("claude", append([]string{"mcp"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude mcp %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

var errClaudeCLIUnavailable = errors.New("claude CLI not found on PATH")

// installClaudeCodeWithOptions registers Cortex as a user-scope MCP server
// named "cortex" (tools mcp__cortex__*) — the location Claude Code actually
// reads. It prefers the official `claude mcp add-json --scope user`; without
// the claude CLI it merges the entry into ~/.claude.json, preserving every
// other key. The Claude Code plugin ships the same server itself
// (plugin/claude-code/.mcp.json, tools mcp__plugin_cortex_cortex__*), so the
// allowlist covers both namespaces.
func installClaudeCodeWithOptions(home, bin string, opts Options) (*Result, error) {
	profile := strings.TrimSpace(opts.Profile)
	if profile == "" {
		profile = "agent"
	}
	entry := map[string]interface{}{
		"type":    "stdio",
		"command": bin,
		"args":    []string{"mcp", "--tools=" + profile},
	}
	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}

	claudeJSON := filepath.Join(home, ".claude.json")
	destination := claudeJSON
	// Re-registering replaces a previous entry; a missing one is fine.
	_ = claudeMCPCommand("remove", "cortex", "--scope", "user")
	switch err := claudeMCPCommand("add-json", "cortex", string(entryJSON), "--scope", "user"); {
	case err == nil:
	case errors.Is(err, errClaudeCLIUnavailable):
		if err := mergeClaudeUserServer(claudeJSON, entry); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}

	// The pre-fix installer wrote ~/.claude/mcp/cortex.json, which Claude
	// Code never reads; remove that misleading leftover.
	legacy := filepath.Join(home, ".claude", "mcp", "cortex.json")
	if ownedOrMissing(legacy, `"name": "cortex"`) {
		_ = os.Remove(legacy)
	}

	allowTools := cortexMCPTools
	switch profile {
	case "minimal":
		allowTools = cortexMinimalMCPTools
	case "dev", "coder":
		allowTools = cortexDevMCPTools
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	addClaudeCodeAllowlistWithTools(settingsPath, claudeAllowRules(allowTools))

	return &Result{Agent: "claude-code", Destination: destination, Files: 2}, nil
}

// mergeClaudeUserServer writes mcpServers.cortex at the top level of
// ~/.claude.json (user scope) without disturbing any other key. An
// unparseable file is never rewritten.
func mergeClaudeUserServer(path string, entry map[string]interface{}) error {
	state, err := readJSONObject(path)
	if err != nil {
		return fmt.Errorf("setup claude-code: %s is not valid JSON; fix it or install the claude CLI and retry: %w", path, err)
	}
	servers, _ := state["mcpServers"].(map[string]interface{})
	if servers == nil {
		servers = map[string]interface{}{}
	}
	servers["cortex"] = entry
	state["mcpServers"] = servers
	out, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(out, '\n'), 0o600)
}

// isClaudeCodeConfigured reports whether ~/.claude.json registers the cortex
// user-scope server.
func isClaudeCodeConfigured(home string) bool {
	state, err := readJSONObject(filepath.Join(home, ".claude.json"))
	if err != nil {
		return false
	}
	servers, _ := state["mcpServers"].(map[string]interface{})
	_, ok := servers["cortex"]
	return ok
}

// claudeAllowRules turns plugin-namespaced tool rules into rules for both the
// user-scope server (mcp__cortex__) and the plugin server
// (mcp__plugin_cortex_cortex__).
func claudeAllowRules(pluginRules []string) []string {
	const pluginPrefix = "mcp__plugin_cortex_cortex__"
	rules := make([]string, 0, 2*len(pluginRules))
	for _, rule := range pluginRules {
		tool := strings.TrimPrefix(rule, pluginPrefix)
		rules = append(rules, "mcp__cortex__"+tool, pluginPrefix+tool)
	}
	return rules
}

// writeFileAtomic writes via a temp file + rename so a crash never leaves a
// truncated file (important for ~/.claude.json, Claude Code's state file).
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// addClaudeCodeAllowlist adds cortex MCP tool permissions to Claude Code settings.
func addClaudeCodeAllowlist(settingsPath string) {
	addClaudeCodeAllowlistWithTools(settingsPath, claudeAllowRules(cortexMCPTools))
}

func addClaudeCodeAllowlistWithTools(settingsPath string, tools []string) {
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
	for _, tool := range tools {
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

func installOpenCodeWithOptions(home, bin string, opts Options) (*Result, error) {
	profile := strings.TrimSpace(opts.Profile)
	if profile == "" {
		profile = "agent"
	}
	configDir := filepath.Join(home, ".config", "opencode")

	mcpPath := filepath.Join(configDir, "cortex-mcp.json")
	mcpContent := fmt.Sprintf(`{
  "mcp": {
    "cortex": {
      "type": "local",
      "command": [%s, "mcp", "--tools=%s"],
      "enabled": true
    }
  }
}
`, jsonString(bin), profile)

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

// geminiProtocolMarker identifies a system.md that Cortex owns and may refresh.
const geminiProtocolMarker = "Cortex Persistent Memory"

func installGeminiCLI(home, bin string) (*Result, error) {
	configPath := filepath.Join(home, ".gemini", "settings.json")
	settings, err := readJSONObject(configPath)
	if err != nil {
		return nil, fmt.Errorf("setup gemini-cli: %s is not valid JSON; fix or move it and retry: %w", configPath, err)
	}
	servers, _ := settings["mcpServers"].(map[string]interface{})
	if servers == nil {
		servers = make(map[string]interface{})
	}
	servers["cortex"] = map[string]interface{}{
		"command": bin,
		"args":    []string{"mcp", "--tools=agent"},
	}
	settings["mcpServers"] = servers
	if err := writeJSONObject(configPath, settings); err != nil {
		return nil, err
	}

	files := 1
	systemPath := filepath.Join(home, ".gemini", "system.md")
	if ownedOrMissing(systemPath, geminiProtocolMarker) {
		if err := writeFile(systemPath, memoryProtocol); err != nil {
			log.Printf("setup: failed to write system prompt %s: %v", systemPath, err)
		} else {
			files++
		}
	} else {
		log.Printf("setup: %s already exists and is not managed by Cortex; left unchanged", systemPath)
	}

	return &Result{Agent: "gemini-cli", Destination: configPath, Files: files}, nil
}

// isGeminiConfigured reports whether settings.json registers the cortex server.
func isGeminiConfigured(configPath string) bool {
	settings, err := readJSONObject(configPath)
	if err != nil {
		return false
	}
	servers, _ := settings["mcpServers"].(map[string]interface{})
	_, ok := servers["cortex"]
	return ok
}

// --- Codex ------------------------------------------------------------------

const codexCortexTable = "[mcp_servers.cortex]"

func installCodex(home, bin string) (*Result, error) {
	configDir := filepath.Join(home, ".codex")
	configPath := filepath.Join(configDir, "config.toml")
	instructionsPath := filepath.Join(configDir, "cortex-instructions.md")
	compactPath := filepath.Join(configDir, "cortex-compact-prompt.md")

	existing, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("setup codex: read %s: %w", configPath, err)
	}
	if len(existing) > 0 {
		var probe map[string]interface{}
		if err := toml.Unmarshal(existing, &probe); err != nil {
			return nil, fmt.Errorf("setup codex: %s is not valid TOML; fix or move it and retry: %w", configPath, err)
		}
	}
	merged := mergeCodexConfig(string(existing), bin, instructionsPath, compactPath)
	var probe map[string]interface{}
	if err := toml.Unmarshal([]byte(merged), &probe); err != nil {
		return nil, fmt.Errorf("setup codex: merged config is not valid TOML: %w", err)
	}
	if err := writeFile(configPath, merged); err != nil {
		return nil, err
	}

	files := 1
	if err := writeFile(instructionsPath, memoryProtocol); err != nil {
		log.Printf("setup: failed to write instructions: %v", err)
	} else {
		files++
	}
	if err := writeFile(compactPath, compactPrompt); err != nil {
		log.Printf("setup: failed to write compact prompt: %v", err)
	} else {
		files++
	}

	return &Result{Agent: "codex", Destination: configPath, Files: files}, nil
}

// mergeCodexConfig upserts the Cortex MCP table into an existing Codex
// config.toml without disturbing the user's other settings:
//   - any previous [mcp_servers.cortex] table (and its sub-tables) is replaced;
//   - model_instructions_file / experimental_compact_prompt_file are added as
//     ROOT keys (before the first table) only when the user has not set them.
func mergeCodexConfig(existing, bin, instructionsPath, compactPath string) string {
	lines := strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n")

	kept := make([]string, 0, len(lines))
	inCortex := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inCortex = trimmed == codexCortexTable || strings.HasPrefix(trimmed, "[mcp_servers.cortex.")
		}
		if !inCortex {
			kept = append(kept, line)
		}
	}

	firstTable := len(kept)
	rootKeys := map[string]bool{}
	for i, line := range kept {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			firstTable = i
			break
		}
		if key, _, ok := strings.Cut(trimmed, "="); ok {
			rootKeys[strings.TrimSpace(key)] = true
		}
	}

	var root []string
	if !rootKeys["model_instructions_file"] {
		root = append(root, "model_instructions_file = "+jsonString(instructionsPath))
	}
	if !rootKeys["experimental_compact_prompt_file"] {
		root = append(root, "experimental_compact_prompt_file = "+jsonString(compactPath))
	}

	// No arithmetic capacity hint: the size derives from the user's file, and
	// append grows the slice safely (CodeQL go/allocation-size-overflow).
	out := append([]string(nil), root...)
	if len(root) > 0 && firstTable > 0 {
		out = append(out, "")
	}
	out = append(out, kept...)

	body := strings.TrimRight(strings.Join(out, "\n"), "\n")
	block := codexCortexTable + "\ncommand = " + jsonString(bin) + "\nargs = [\"mcp\", \"--tools=agent\"]\n"
	if body == "" {
		return block
	}
	return body + "\n\n" + block
}

// isCodexConfigured reports whether config.toml registers the cortex server.
func isCodexConfigured(configPath string) bool {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == codexCortexTable {
			return true
		}
	}
	return false
}

// --- JSON merge helpers -----------------------------------------------------

// readJSONObject returns the JSON object stored at path, an empty object when
// the file is missing or blank, and an error when it holds invalid JSON (so
// callers never clobber a file they could not parse).
func readJSONObject(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]interface{}{}, nil
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return map[string]interface{}{}, nil
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		obj = map[string]interface{}{}
	}
	return obj, nil
}

func writeJSONObject(path string, obj map[string]interface{}) error {
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, string(out)+"\n")
}

// ownedOrMissing reports whether path is absent or already carries marker
// (i.e. was written by Cortex and may be refreshed).
func ownedOrMissing(path, marker string) bool {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return true
	}
	return err == nil && strings.Contains(string(data), marker)
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

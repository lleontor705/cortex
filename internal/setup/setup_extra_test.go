package setup

// Behavioral tests for the setup package installer contracts.
//
// Scope (task 1.6 G5 of change coverage-70-and-lint, spec #884, design #885):
//   - supported agents enumeration
//   - four installer exact JSON/TOML content and protocol content
//   - unsupported input error contract
//   - allowlist missing/valid/invalid/idempotent behavior with no duplicates
//   - writeFile directory creation
//   - HOME precedence over USERPROFILE and USERPROFILE fallback
//   - jsonString JSON encoding
//   - deterministic properties of resolveBinaryPath
//
// Isolation: every HOME/USERPROFILE value and every destination is temporary
// (t.TempDir + scoped t.Setenv). No real user configuration is touched, no
// external services are used, and no parallel environment mutation occurs.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// setupHome points HOME and USERPROFILE at a fresh temporary directory and
// returns it so each installer writes only inside that isolated tree.
func setupHome(t *testing.T) string {
	t.Helper()
	stubClaudeCLI(t, nil)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// assertFile reads path and fails the test with context if it is missing.
func assertFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return data
}

// --- Supported agents --------------------------------------------------------

func TestSupportedAgents_ListAndOrder(t *testing.T) {
	home := setupHome(t)

	agents := SupportedAgents()
	if got, want := len(agents), 4; got != want {
		t.Fatalf("len(SupportedAgents()) = %d, want %d", got, want)
	}

	want := []string{"claude-code", "opencode", "gemini-cli", "codex"}
	for i, a := range agents {
		if a.Name != want[i] {
			t.Errorf("agents[%d].Name = %q, want %q", i, a.Name, want[i])
		}
		if a.Description == "" {
			t.Errorf("agents[%d].Description is empty", i)
		}
		if a.InstallDir == "" {
			t.Errorf("agents[%d].InstallDir is empty", i)
		}
		// InstallDir must be rooted at the resolved home directory so the
		// description of where files land is correct for the current user.
		if !strings.HasPrefix(a.InstallDir, home) {
			t.Errorf("agents[%d].InstallDir = %q, want prefix %q", i, a.InstallDir, home)
		}
	}
}

// --- Claude Code installer ---------------------------------------------------

func TestInstallClaudeCode_ExactContentAndResult(t *testing.T) {
	home := setupHome(t)

	res, err := Install("claude-code")
	if err != nil {
		t.Fatalf("Install(claude-code) error = %v", err)
	}
	if res == nil {
		t.Fatal("Install(claude-code) result is nil")
	}
	if res.Agent != "claude-code" {
		t.Errorf("Result.Agent = %q, want claude-code", res.Agent)
	}

	claudeJSON := filepath.Join(home, ".claude.json")
	if res.Destination != claudeJSON {
		t.Errorf("Result.Destination = %q, want %q", res.Destination, claudeJSON)
	}
	if res.Files != 2 {
		t.Errorf("Result.Files = %d, want 2", res.Files)
	}

	// Without the claude CLI the user-scope entry is merged into ~/.claude.json.
	cfg := claudeUserEntry(t, home)
	if got, want := cfg["type"], "stdio"; got != want {
		t.Errorf("cfg.type = %v, want %v", got, want)
	}
	command, ok := cfg["command"].(string)
	if !ok {
		t.Fatalf("cfg.command is not a string: %v", cfg["command"])
	}
	if command == "" {
		t.Error("cfg.command is empty")
	}
	// command must be the JSON-encoded resolved binary path, which is at least
	// non-empty and resolves through filepath.EvalSymlinks.
	if resolved, err := filepath.EvalSymlinks(command); err == nil && resolved == "" {
		t.Errorf("cfg.command resolves to empty path: %q", command)
	}
	args, ok := cfg["args"].([]any)
	if !ok {
		t.Fatalf("cfg.args is not an array: %v", cfg["args"])
	}
	if len(args) != 2 || args[0] != "mcp" || args[1] != "--tools=agent" {
		t.Errorf("cfg.args = %v, want [mcp --tools=agent]", args)
	}

	// The allowlist must be written to settings.json with every cortex tool.
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	settingsRaw := assertFile(t, settingsPath)
	var settings map[string]any
	if err := json.Unmarshal(settingsRaw, &settings); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}
	allow := allowListFromSettings(t, settings)
	for _, rule := range claudeAllowRules(cortexMCPTools) {
		if !allow[rule] {
			t.Errorf("settings.json missing allow entry %q", rule)
		}
	}
	if !allow["mcp__cortex__cortex_save"] || !allow["mcp__plugin_cortex_cortex__cortex_save"] {
		t.Error("allowlist must cover both the user-scope and the plugin namespace")
	}
}

// --- OpenCode installer ------------------------------------------------------

func TestInstallOpenCode_ExactContentAndResult(t *testing.T) {
	home := setupHome(t)

	res, err := Install("opencode")
	if err != nil {
		t.Fatalf("Install(opencode) error = %v", err)
	}
	if res == nil || res.Agent != "opencode" {
		t.Fatalf("Install(opencode) result = %+v", res)
	}

	mcpPath := filepath.Join(home, ".config", "opencode", "cortex-mcp.json")
	if res.Destination != mcpPath {
		t.Errorf("Result.Destination = %q, want %q", res.Destination, mcpPath)
	}
	if res.Files != 2 {
		t.Errorf("Result.Files = %d, want 2", res.Files)
	}

	raw := assertFile(t, mcpPath)
	var cfg struct {
		MCP map[string]struct {
			Type    string   `json:"type"`
			Command []string `json:"command"`
			Enabled bool     `json:"enabled"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("cortex-mcp.json is not valid JSON: %v\nraw=%s", err, raw)
	}
	entry, ok := cfg.MCP["cortex"]
	if !ok {
		t.Fatalf("cortex-mcp.json missing mcp.cortex: %v", cfg.MCP)
	}
	if entry.Type != "local" {
		t.Errorf("mcp.cortex.type = %q, want local", entry.Type)
	}
	if !entry.Enabled {
		t.Error("mcp.cortex.enabled = false, want true")
	}
	if len(entry.Command) != 3 {
		t.Fatalf("mcp.cortex.command length = %d, want 3: %v", len(entry.Command), entry.Command)
	}
	if entry.Command[0] == "" {
		t.Error("mcp.cortex.command[0] (binary) is empty")
	}
	if entry.Command[1] != "mcp" || entry.Command[2] != "--tools=agent" {
		t.Errorf("mcp.cortex.command = %v, want [<bin> mcp --tools=agent]", entry.Command)
	}
	plugin := string(assertFile(t, filepath.Join(home, ".config", "opencode", "plugins", "cortex.ts")))
	if strings.Contains(plugin, `return "cortex"`) {
		t.Fatal("installed plugin kept unresolved binary fallback")
	}
	for _, want := range []string{jsonString(entry.Command[0]), "/api/prompts", "session.deleted"} {
		if !strings.Contains(plugin, want) {
			t.Errorf("installed plugin missing %q", want)
		}
	}
}

// --- Gemini CLI installer ----------------------------------------------------

func TestInstallGeminiCLI_ExactContentAndProtocol(t *testing.T) {
	home := setupHome(t)

	res, err := Install("gemini-cli")
	if err != nil {
		t.Fatalf("Install(gemini-cli) error = %v", err)
	}
	if res == nil || res.Agent != "gemini-cli" {
		t.Fatalf("Install(gemini-cli) result = %+v", res)
	}

	configPath := filepath.Join(home, ".gemini", "settings.json")
	if res.Destination != configPath {
		t.Errorf("Result.Destination = %q, want %q", res.Destination, configPath)
	}
	// Gemini writes the MCP config plus the system.md protocol file.
	if res.Files != 2 {
		t.Errorf("Result.Files = %d, want 2", res.Files)
	}

	raw := assertFile(t, configPath)
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v\nraw=%s", err, raw)
	}
	entry, ok := cfg.MCPServers["cortex"]
	if !ok {
		t.Fatalf("settings.json missing mcpServers.cortex: %v", cfg.MCPServers)
	}
	if entry.Command == "" {
		t.Error("mcpServers.cortex.command is empty")
	}
	if len(entry.Args) != 2 || entry.Args[0] != "mcp" || entry.Args[1] != "--tools=agent" {
		t.Errorf("mcpServers.cortex.args = %v, want [mcp --tools=agent]", entry.Args)
	}

	// system.md must contain the Memory Protocol content, not be empty.
	systemRaw := assertFile(t, filepath.Join(home, ".gemini", "system.md"))
	if !strings.Contains(string(systemRaw), "Cortex Persistent Memory") {
		t.Errorf("system.md missing protocol marker; got %q", systemRaw)
	}
	if !strings.Contains(string(systemRaw), "cortex_session_summary") {
		t.Errorf("system.md missing session-summary instruction; got %q", systemRaw)
	}
}

// --- Codex installer ---------------------------------------------------------

func TestInstallCodex_ExactContentAndProtocol(t *testing.T) {
	home := setupHome(t)

	res, err := Install("codex")
	if err != nil {
		t.Fatalf("Install(codex) error = %v", err)
	}
	if res == nil || res.Agent != "codex" {
		t.Fatalf("Install(codex) result = %+v", res)
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	if res.Destination != configPath {
		t.Errorf("Result.Destination = %q, want %q", res.Destination, configPath)
	}
	// Codex writes config.toml + cortex-instructions.md + cortex-compact-prompt.md.
	if res.Files != 3 {
		t.Errorf("Result.Files = %d, want 3", res.Files)
	}

	tomlRaw := assertFile(t, configPath)
	toml := string(tomlRaw)
	// Exact TOML section and the literal args/keys the installer emits.
	for _, want := range []string{
		"[mcp_servers.cortex]",
		`args = ["mcp", "--tools=agent"]`,
		`model_instructions_file = `,
		`experimental_compact_prompt_file = `,
	} {
		if !strings.Contains(toml, want) {
			t.Errorf("config.toml missing %q\nraw=%s", want, toml)
		}
	}
	// model_instructions_file is embedded as a raw path (not JSON-encoded), so
	// it must appear verbatim in the TOML.
	instrPath := filepath.Join(home, ".codex", "cortex-instructions.md")
	if !strings.Contains(toml, instrPath) {
		t.Errorf("config.toml missing instructions path %q; raw=%s", instrPath, toml)
	}
	compactPathRef := filepath.Join(home, ".codex", "cortex-compact-prompt.md")
	if !strings.Contains(toml, compactPathRef) {
		t.Errorf("config.toml missing compact prompt path %q; raw=%s", compactPathRef, toml)
	}

	// cortex-instructions.md carries the Memory Protocol.
	instrRaw := assertFile(t, filepath.Join(home, ".codex", "cortex-instructions.md"))
	if !strings.Contains(string(instrRaw), "Cortex Persistent Memory") {
		t.Errorf("cortex-instructions.md missing protocol marker; got %q", instrRaw)
	}

	// cortex-compact-prompt.md carries the compaction recovery prompt.
	compactRaw := assertFile(t, filepath.Join(home, ".codex", "cortex-compact-prompt.md"))
	if !strings.Contains(string(compactRaw), "FIRST ACTION REQUIRED") {
		t.Errorf("cortex-compact-prompt.md missing compaction marker; got %q", compactRaw)
	}
	if !strings.Contains(string(compactRaw), "cortex_session_summary") {
		t.Errorf("cortex-compact-prompt.md missing session-summary step; got %q", compactRaw)
	}
}

// --- Unsupported agent -------------------------------------------------------

func TestInstall_UnsupportedAgentErrorContract(t *testing.T) {
	setupHome(t)

	cases := []string{"", "claude", "Cursor", "roo", "windsurf", "unknown-agent"}
	for _, agent := range cases {
		agent := agent
		t.Run(agent, func(t *testing.T) {
			res, err := Install(agent)
			if err == nil {
				t.Fatalf("Install(%q) error = nil, want non-nil", agent)
			}
			if res != nil {
				t.Errorf("Install(%q) result = %+v, want nil", agent, res)
			}
			msg := err.Error()
			if !strings.Contains(msg, "unsupported agent") {
				t.Errorf("error %q missing 'unsupported agent'", msg)
			}
			// The error must enumerate the supported set so the caller can
			// self-correct without reading docs.
			for _, supported := range []string{"claude-code", "opencode", "gemini-cli", "codex"} {
				if !strings.Contains(msg, supported) {
					t.Errorf("error %q missing supported agent %q", msg, supported)
				}
			}
		})
	}
}

// --- Allowlist behavior ------------------------------------------------------

func allowListFromSettings(t *testing.T, settings map[string]any) map[string]bool {
	t.Helper()
	perms, ok := settings["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("settings has no permissions object: %v", settings["permissions"])
	}
	allow, ok := perms["allow"].([]any)
	if !ok {
		t.Fatalf("permissions.allow is not an array: %v", perms["allow"])
	}
	out := make(map[string]bool)
	for _, v := range allow {
		if s, ok := v.(string); ok {
			out[s] = true
		}
	}
	return out
}

// AddClaudeCodeAllowlist must create a fresh settings.json with the full
// cortex tool allowlist when none exists.
func TestAddClaudeCodeAllowlist_CreatesWhenMissing(t *testing.T) {
	home := setupHome(t)
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatalf("precondition: settings.json unexpectedly exists at %q", settingsPath)
	}

	if err := AddClaudeCodeAllowlist(); err != nil {
		t.Fatalf("AddClaudeCodeAllowlist() error = %v", err)
	}

	raw := assertFile(t, settingsPath)
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}
	allow := allowListFromSettings(t, settings)
	for _, tool := range cortexMCPTools {
		if !allow[tool] {
			t.Errorf("missing allow entry %q", tool)
		}
	}
}

// Existing valid allow entries must be preserved alongside the cortex tools.
func TestAddClaudeCodeAllowlist_MergesValidExisting(t *testing.T) {
	home := setupHome(t)
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	existing := map[string]any{
		"permissions": map[string]any{
			"allow": []any{"Bash(custom-1)", "Read(*)"},
		},
		"theme": "dark",
	}
	writeJSON(t, settingsPath, existing)

	if err := AddClaudeCodeAllowlist(); err != nil {
		t.Fatalf("AddClaudeCodeAllowlist() error = %v", err)
	}

	raw := assertFile(t, settingsPath)
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}
	allow := allowListFromSettings(t, settings)
	for _, want := range []string{"Bash(custom-1)", "Read(*)"} {
		if !allow[want] {
			t.Errorf("existing allow entry %q was dropped", want)
		}
	}
	for _, tool := range cortexMCPTools {
		if !allow[tool] {
			t.Errorf("missing cortex allow entry %q", tool)
		}
	}
	// Unrelated keys must survive the merge.
	if theme, _ := settings["theme"].(string); theme != "dark" {
		t.Errorf("settings.theme = %v, want dark", settings["theme"])
	}
}

// A malformed settings.json must be replaced with a fresh allowlist rather
// than crashing or silently discarding the cortex tools.
func TestAddClaudeCodeAllowlist_ReplacesInvalidJSON(t *testing.T) {
	home := setupHome(t)
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := writeFile(settingsPath, "{not valid json"); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}

	if err := AddClaudeCodeAllowlist(); err != nil {
		t.Fatalf("AddClaudeCodeAllowlist() error = %v", err)
	}

	raw := assertFile(t, settingsPath)
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("settings.json is not valid JSON after recovery: %v", err)
	}
	allow := allowListFromSettings(t, settings)
	for _, tool := range cortexMCPTools {
		if !allow[tool] {
			t.Errorf("missing cortex allow entry %q after invalid recovery", tool)
		}
	}
}

// Idempotency: calling twice must not duplicate any tool.
func TestAddClaudeCodeAllowlist_IdempotentNoDuplicates(t *testing.T) {
	home := setupHome(t)

	if err := AddClaudeCodeAllowlist(); err != nil {
		t.Fatalf("first AddClaudeCodeAllowlist() error = %v", err)
	}
	if err := AddClaudeCodeAllowlist(); err != nil {
		t.Fatalf("second AddClaudeCodeAllowlist() error = %v", err)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	raw := assertFile(t, settingsPath)
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}
	perms, _ := settings["permissions"].(map[string]any)
	allow, _ := perms["allow"].([]any)

	seen := make(map[string]int)
	for _, v := range allow {
		if s, ok := v.(string); ok {
			seen[s]++
		}
	}
	for _, tool := range cortexMCPTools {
		if seen[tool] != 1 {
			t.Errorf("tool %q appears %d times, want exactly 1", tool, seen[tool])
		}
	}
}

// --- writeFile helper --------------------------------------------------------

func TestWriteFile_CreatesParentDirsAndContent(t *testing.T) {
	root := t.TempDir()
	// A deeply nested path whose parents do not yet exist.
	dst := filepath.Join(root, "a", "b", "c", "config.txt")
	content := "hello-cortex"

	if err := writeFile(dst, content); err != nil {
		t.Fatalf("writeFile(%q) error = %v", dst, err)
	}

	raw := assertFile(t, dst)
	if string(raw) != content {
		t.Errorf("writeFile content = %q, want %q", string(raw), content)
	}
	// Parent directory must have been created.
	info, err := os.Stat(filepath.Dir(dst))
	if err != nil {
		t.Fatalf("Stat(parent) error = %v", err)
	}
	if !info.IsDir() {
		t.Errorf("parent of %q is not a directory", dst)
	}
}

// --- resolveHome precedence and fallback ------------------------------------

func TestResolveHome_HOMETakesPrecedenceOverUSERPROFILE(t *testing.T) {
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home-a"))
	t.Setenv("USERPROFILE", filepath.Join(t.TempDir(), "home-b"))

	got, err := resolveHome()
	if err != nil {
		t.Fatalf("resolveHome() error = %v", err)
	}
	if !strings.HasSuffix(got, "home-a") {
		t.Errorf("resolveHome() = %q, want suffix home-a (HOME precedence)", got)
	}
}

func TestResolveHome_USERPROFILEFallbackWhenHOMEEmpty(t *testing.T) {
	t.Setenv("HOME", "")
	profile := filepath.Join(t.TempDir(), "profile-b")
	t.Setenv("USERPROFILE", profile)

	got, err := resolveHome()
	if err != nil {
		t.Fatalf("resolveHome() error = %v", err)
	}
	if got != profile {
		t.Errorf("resolveHome() = %q, want %q (USERPROFILE fallback)", got, profile)
	}
}

// --- jsonString helper -------------------------------------------------------

func TestJSONString_EncodesAsJSONValue(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "cortex", `"cortex"`},
		{"empty", "", `""`},
		{"with spaces", "/usr/local/bin/cortex", `"/usr/local/bin/cortex"`},
		// Special characters must round-trip as valid JSON when decoded back.
		{"quotes", `he said "hi"`, `"he said \"hi\""`},
		{"backslash", `C:\cortex\bin`, `"C:\\cortex\\bin"`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := jsonString(tc.in)
			if got != tc.want {
				t.Errorf("jsonString(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// The output must decode back to the original string.
			var back string
			if err := json.Unmarshal([]byte(got), &back); err != nil {
				t.Fatalf("jsonString output %q is not valid JSON: %v", got, err)
			}
			if back != tc.in {
				t.Errorf("jsonString round-trip = %q, want %q", back, tc.in)
			}
		})
	}
}

// --- resolveBinaryPath determinism ------------------------------------------

func TestResolveBinaryPath_DeterministicAndNonEmpty(t *testing.T) {
	first := resolveBinaryPath()
	second := resolveBinaryPath()
	if first == "" {
		t.Fatal("resolveBinaryPath() returned empty string")
	}
	if first != second {
		t.Errorf("resolveBinaryPath is not deterministic: first=%q second=%q", first, second)
	}
	// Every invocation within the same test process must resolve to an absolute
	// path or fall back to the documented "cortex" sentinel.
	if first != "cortex" {
		if !filepath.IsAbs(first) {
			t.Errorf("resolveBinaryPath() = %q, want absolute path or 'cortex'", first)
		}
	}
}

// --- helpers -----------------------------------------------------------------

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := writeFile(path, string(data)+"\n"); err != nil {
		t.Fatalf("writeJSON(%q) error = %v", path, err)
	}
}

// --- InstallWithOptions tests ------------------------------------------------

func TestInstallClaudeCode_WithModularProfiles(t *testing.T) {
	home := setupHome(t)

	// 1. Dev Profile
	resDev, err := InstallWithOptions("claude-code", Options{Profile: "dev"})
	if err != nil {
		t.Fatalf("InstallWithOptions(claude-code, dev) error = %v", err)
	}
	if resDev == nil {
		t.Fatal("resDev is nil")
	}

	cfgDev := claudeUserEntry(t, home)
	argsDev := cfgDev["args"].([]any)
	if len(argsDev) != 2 || argsDev[1] != "--tools=dev" {
		t.Errorf("argsDev = %v, want [mcp --tools=dev]", argsDev)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	settingsRaw := assertFile(t, settingsPath)
	var settings map[string]any
	if err := json.Unmarshal(settingsRaw, &settings); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	allow := allowListFromSettings(t, settings)
	for _, tool := range claudeAllowRules(cortexDevMCPTools) {
		if !allow[tool] {
			t.Errorf("settings missing dev tool %q", tool)
		}
	}
	// Verify that tools outside the dev profile were not added
	if allow["mcp__plugin_cortex_cortex__cortex_analyze_architecture"] {
		t.Error("settings should not contain cortex_analyze_architecture in dev profile")
	}

	// 2. Minimal Profile
	home2 := setupHome(t)
	resMin, err := InstallWithOptions("claude-code", Options{Profile: "minimal"})
	if err != nil {
		t.Fatalf("InstallWithOptions(claude-code, minimal) error = %v", err)
	}
	if resMin == nil {
		t.Fatal("resMin is nil")
	}

	cfgMin := claudeUserEntry(t, home2)
	argsMin := cfgMin["args"].([]any)
	if len(argsMin) != 2 || argsMin[1] != "--tools=minimal" {
		t.Errorf("argsMin = %v, want [mcp --tools=minimal]", argsMin)
	}

	settingsRaw2 := assertFile(t, filepath.Join(home2, ".claude", "settings.json"))
	var settings2 map[string]any
	if err := json.Unmarshal(settingsRaw2, &settings2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	allow2 := allowListFromSettings(t, settings2)
	for _, tool := range claudeAllowRules(cortexMinimalMCPTools) {
		if !allow2[tool] {
			t.Errorf("settings missing minimal tool %q", tool)
		}
	}
	if allow2["mcp__plugin_cortex_cortex__cortex_ingest_code"] {
		t.Error("settings should not contain cortex_ingest_code in minimal profile")
	}
}

func TestInstallOpenCode_WithModularProfiles(t *testing.T) {
	home := setupHome(t)

	// 1. Dev Profile
	resDev, err := InstallWithOptions("opencode", Options{Profile: "dev"})
	if err != nil {
		t.Fatalf("InstallWithOptions(opencode, dev) error = %v", err)
	}
	if resDev == nil {
		t.Fatal("resDev is nil")
	}

	mcpPath := filepath.Join(home, ".config", "opencode", "cortex-mcp.json")
	rawDev := assertFile(t, mcpPath)
	var cfgDev struct {
		MCP map[string]struct {
			Type    string   `json:"type"`
			Command []string `json:"command"`
			Enabled bool     `json:"enabled"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(rawDev, &cfgDev); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	entry := cfgDev.MCP["cortex"]
	if len(entry.Command) != 3 || entry.Command[2] != "--tools=dev" {
		t.Errorf("entry.Command = %v, want [<bin> mcp --tools=dev]", entry.Command)
	}

	// 2. Minimal Profile
	home2 := setupHome(t)
	resMin, err := InstallWithOptions("opencode", Options{Profile: "minimal"})
	if err != nil {
		t.Fatalf("InstallWithOptions(opencode, minimal) error = %v", err)
	}
	if resMin == nil {
		t.Fatal("resMin is nil")
	}

	mcpPath2 := filepath.Join(home2, ".config", "opencode", "cortex-mcp.json")
	rawMin := assertFile(t, mcpPath2)
	var cfgMin struct {
		MCP map[string]struct {
			Type    string   `json:"type"`
			Command []string `json:"command"`
			Enabled bool     `json:"enabled"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(rawMin, &cfgMin); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	entryMin := cfgMin.MCP["cortex"]
	if len(entryMin.Command) != 3 || entryMin.Command[2] != "--tools=minimal" {
		t.Errorf("entryMin.Command = %v, want [<bin> mcp --tools=minimal]", entryMin.Command)
	}
}

// --- Non-destructive merges --------------------------------------------------

func TestInstallGeminiCLI_PreservesExistingSettings(t *testing.T) {
	home := setupHome(t)
	configPath := filepath.Join(home, ".gemini", "settings.json")
	systemPath := filepath.Join(home, ".gemini", "system.md")
	if err := writeFile(configPath, `{"theme":"Dracula","mcpServers":{"other":{"command":"other-mcp"}}}`); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(systemPath, "my own system prompt"); err != nil {
		t.Fatal(err)
	}

	res, err := Install("gemini-cli")
	if err != nil {
		t.Fatalf("Install(gemini-cli) error = %v", err)
	}
	if res.Files != 1 {
		t.Errorf("Result.Files = %d, want 1 (user system.md left alone)", res.Files)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(assertFile(t, configPath), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["theme"] != "Dracula" {
		t.Errorf("theme = %v, want preserved", cfg["theme"])
	}
	servers, _ := cfg["mcpServers"].(map[string]interface{})
	if _, ok := servers["other"]; !ok {
		t.Errorf("mcpServers.other was dropped: %v", servers)
	}
	if _, ok := servers["cortex"]; !ok {
		t.Errorf("mcpServers.cortex missing: %v", servers)
	}
	if got := string(assertFile(t, systemPath)); got != "my own system prompt" {
		t.Errorf("system.md overwritten: %q", got)
	}
}

func TestInstallGeminiCLI_RefusesInvalidJSON(t *testing.T) {
	home := setupHome(t)
	configPath := filepath.Join(home, ".gemini", "settings.json")
	if err := writeFile(configPath, `{not json`); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("gemini-cli"); err == nil {
		t.Fatal("expected an error for invalid settings.json")
	}
	if got := string(assertFile(t, configPath)); got != `{not json` {
		t.Errorf("invalid settings.json was clobbered: %q", got)
	}
}

func TestInstallCodex_PreservesExistingConfigAndRootKeys(t *testing.T) {
	home := setupHome(t)
	configPath := filepath.Join(home, ".codex", "config.toml")
	existing := `model = "o4"
model_instructions_file = "/custom/instructions.md"

[mcp_servers.cortex]
command = "old-cortex"
args = ["mcp"]
model_instructions_file = "/misplaced.md"

[mcp_servers.cortex.env]
FOO = "bar"

[mcp_servers.github]
command = "gh-mcp"
`
	if err := writeFile(configPath, existing); err != nil {
		t.Fatal(err)
	}

	if _, err := Install("codex"); err != nil {
		t.Fatalf("Install(codex) error = %v", err)
	}
	raw := assertFile(t, configPath)
	var cfg struct {
		Model                 string `toml:"model"`
		ModelInstructionsFile string `toml:"model_instructions_file"`
		CompactPromptFile     string `toml:"experimental_compact_prompt_file"`
		MCPServers            map[string]struct {
			Command string            `toml:"command"`
			Args    []string          `toml:"args"`
			Env     map[string]string `toml:"env"`
			Extra   string            `toml:"model_instructions_file"`
		} `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("merged config.toml invalid: %v\n%s", err, raw)
	}
	if cfg.Model != "o4" {
		t.Errorf("model = %q, want preserved", cfg.Model)
	}
	if cfg.ModelInstructionsFile != "/custom/instructions.md" {
		t.Errorf("model_instructions_file = %q, want the user's value kept", cfg.ModelInstructionsFile)
	}
	if want := filepath.Join(home, ".codex", "cortex-compact-prompt.md"); cfg.CompactPromptFile != want {
		t.Errorf("experimental_compact_prompt_file = %q, want root key %q", cfg.CompactPromptFile, want)
	}
	if cfg.MCPServers["github"].Command != "gh-mcp" {
		t.Errorf("mcp_servers.github dropped: %+v", cfg.MCPServers)
	}
	cortex := cfg.MCPServers["cortex"]
	if cortex.Command == "old-cortex" || len(cortex.Args) != 2 || cortex.Extra != "" || cortex.Env != nil {
		t.Errorf("mcp_servers.cortex not replaced cleanly: %+v", cortex)
	}
	if strings.Count(string(raw), "[mcp_servers.cortex]") != 1 {
		t.Errorf("expected exactly one cortex table:\n%s", raw)
	}

	// Idempotent: a second install yields the same file.
	if _, err := Install("codex"); err != nil {
		t.Fatal(err)
	}
	if again := assertFile(t, configPath); string(again) != string(raw) {
		t.Errorf("second install changed config.toml:\nfirst:\n%s\nsecond:\n%s", raw, again)
	}
}

func TestInstallCodex_RefusesInvalidTOML(t *testing.T) {
	home := setupHome(t)
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := writeFile(configPath, "model = \n[broken"); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("codex"); err == nil {
		t.Fatal("expected an error for invalid config.toml")
	}
}

// stubClaudeCLI replaces the claude CLI seam. With record == nil the CLI is
// reported as unavailable (forcing the ~/.claude.json merge path); otherwise
// every call is recorded and succeeds.
func stubClaudeCLI(t *testing.T, record *[][]string) {
	t.Helper()
	prev := claudeMCPCommand
	claudeMCPCommand = func(args ...string) error {
		if record == nil {
			return errClaudeCLIUnavailable
		}
		*record = append(*record, append([]string(nil), args...))
		return nil
	}
	t.Cleanup(func() { claudeMCPCommand = prev })
}

// claudeUserEntry returns mcpServers.cortex from ~/.claude.json.
func claudeUserEntry(t *testing.T, home string) map[string]any {
	t.Helper()
	raw := assertFile(t, filepath.Join(home, ".claude.json"))
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("~/.claude.json is not valid JSON: %v\n%s", err, raw)
	}
	servers, _ := state["mcpServers"].(map[string]any)
	entry, ok := servers["cortex"].(map[string]any)
	if !ok {
		t.Fatalf("~/.claude.json missing mcpServers.cortex: %s", raw)
	}
	return entry
}

func TestInstallClaudeCode_PreservesClaudeStateAndRemovesLegacyFile(t *testing.T) {
	home := setupHome(t)
	claudeJSON := filepath.Join(home, ".claude.json")
	if err := writeFile(claudeJSON, `{"numStartups":42,"projects":{"/x":{"mcpServers":{}}},"mcpServers":{"other":{"command":"other-mcp"}}}`); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, ".claude", "mcp", "cortex.json")
	if err := writeFile(legacy, "{\n  \"name\": \"cortex\",\n  \"type\": \"stdio\"\n}\n"); err != nil {
		t.Fatal(err)
	}

	if _, err := Install("claude-code"); err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(assertFile(t, claudeJSON), &state); err != nil {
		t.Fatal(err)
	}
	if state["numStartups"] != float64(42) || state["projects"] == nil {
		t.Errorf("unrelated Claude Code state lost: %v", state)
	}
	servers := state["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Error("other user-scope server dropped")
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy ~/.claude/mcp/cortex.json not removed: %v", err)
	}
	if status := findStatus(DetectAgents(), "claude-code"); status == nil || !status.Configured {
		t.Errorf("claude-code not detected as configured: %+v", status)
	}
}

func TestInstallClaudeCode_RefusesInvalidClaudeJSON(t *testing.T) {
	home := setupHome(t)
	claudeJSON := filepath.Join(home, ".claude.json")
	if err := writeFile(claudeJSON, "{broken"); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("claude-code"); err == nil {
		t.Fatal("expected an error for an invalid ~/.claude.json")
	}
	if got := string(assertFile(t, claudeJSON)); got != "{broken" {
		t.Errorf("invalid ~/.claude.json rewritten: %q", got)
	}
}

func TestInstallClaudeCode_UsesClaudeCLIWhenAvailable(t *testing.T) {
	home := setupHome(t)
	var calls [][]string
	stubClaudeCLI(t, &calls)

	if _, err := InstallWithOptions("claude-code", Options{Profile: "dev"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0][0] != "remove" || calls[1][0] != "add-json" {
		t.Fatalf("claude CLI calls = %v, want remove then add-json", calls)
	}
	add := calls[1]
	if add[1] != "cortex" || add[len(add)-2] != "--scope" || add[len(add)-1] != "user" {
		t.Fatalf("add-json args = %v", add)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(add[2]), &entry); err != nil {
		t.Fatalf("add-json payload: %v", err)
	}
	if entry["type"] != "stdio" || fmt.Sprint(entry["args"]) != "[mcp --tools=dev]" {
		t.Fatalf("payload = %v", entry)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Error("~/.claude.json must not be edited directly when the claude CLI is available")
	}
}

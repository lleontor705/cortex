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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupHome points HOME and USERPROFILE at a fresh temporary directory and
// returns it so each installer writes only inside that isolated tree.
func setupHome(t *testing.T) string {
	t.Helper()
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

	mcpPath := filepath.Join(home, ".claude", "mcp", "cortex.json")
	if res.Destination != mcpPath {
		t.Errorf("Result.Destination = %q, want %q", res.Destination, mcpPath)
	}
	if res.Files != 1 {
		t.Errorf("Result.Files = %d, want 1", res.Files)
	}

	// The MCP config must be valid JSON with the exact contract fields.
	raw := assertFile(t, mcpPath)
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("cortex.json is not valid JSON: %v\nraw=%s", err, raw)
	}
	if got, want := cfg["name"], "cortex"; got != want {
		t.Errorf("cfg.name = %v, want %v", got, want)
	}
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
	for _, tool := range cortexMCPTools {
		if !allow[tool] {
			t.Errorf("settings.json missing allow entry %q", tool)
		}
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
	// In an isolated tree the plugin source is absent, so exactly one file is
	// produced by the primary writer.
	if res.Files != 1 {
		t.Errorf("Result.Files = %d, want 1", res.Files)
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
	if !strings.Contains(string(systemRaw), "mem_session_summary") {
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
	if !strings.Contains(string(compactRaw), "mem_session_summary") {
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

package setup

// Coverage-boosting tests for internal/setup (Wave 9 push).
//
// Target gaps:
//   - DetectAgents: 0% — all branches for Claude, OpenCode (primary + alt),
//     Gemini, Codex, and Ollama status states
//   - commandExists, fileExists, dirOrFileExists: 0% (called by DetectAgents)
//   - Install("ollama") / InstallWithOptions("ollama") case
//   - resolveHome os.UserHomeDir fallback when both HOME and USERPROFILE empty
//
// Isolation: every test uses t.TempDir + t.Setenv for HOME/USERPROFILE.
// No real user configuration is touched. No external services are required
// (Ollama HTTP probe times out gracefully; no daemon needed).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- DetectAgents -----------------------------------------------------------

// TestDetectAgents_AllNotDetected verifies the default path when no agent
// directories or config files exist under the resolved home. On machines
// where an agent binary (e.g. opencode) is in PATH, commandExists returns
// true and the agent is reported as detected — that is correct behavior.
func TestDetectAgents_AllNotDetected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	statuses := DetectAgents()
	if got, want := len(statuses), 5; got != want {
		t.Fatalf("len(DetectAgents()) = %d, want %d", got, want)
	}

	// Claude: no dir, no config file.
	claude := findStatus(statuses, "claude-code")
	if claude == nil {
		t.Fatal("missing claude-code status")
	}
	if claude.Configured {
		t.Error("claude-code Configured = true, want false (no config file)")
	}

	// OpenCode: may be detected via commandExists if the binary is in PATH.
	oc := findStatus(statuses, "opencode")
	if oc == nil {
		t.Fatal("missing opencode status")
	}
	if oc.Configured {
		t.Error("opencode Configured = true, want false (no config file)")
	}

	// Gemini: no dir, no config file.
	gemini := findStatus(statuses, "gemini-cli")
	if gemini == nil {
		t.Fatal("missing gemini-cli status")
	}
	if gemini.Configured {
		t.Error("gemini-cli Configured = true, want false (no config file)")
	}

	// Codex: no dir, no config file.
	codex := findStatus(statuses, "codex")
	if codex == nil {
		t.Fatal("missing codex status")
	}
	if codex.Configured {
		t.Error("codex Configured = true, want false (no config file)")
	}

	// Ollama: no command, HTTP times out → "Not detected" or "Installed"
	// depending on whether ollama binary is in PATH.
	ollama := findStatus(statuses, "ollama")
	if ollama == nil {
		t.Fatal("missing ollama status")
	}
	if ollama.Configured {
		t.Error("ollama Configured = true, want false (no running daemon)")
	}
}

// TestDetectAgents_DetectedNotConfigured verifies the "Detected (Ready to
// install)" path when agent directories exist but no config files are present.
func TestDetectAgents_DetectedNotConfigured(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Create agent directories (triggers dirOrFileExists → true).
	for _, dir := range []string{
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".config", "opencode"),
		filepath.Join(home, ".gemini"),
		filepath.Join(home, ".codex"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	statuses := DetectAgents()

	claude := findStatus(statuses, "claude-code")
	if claude == nil {
		t.Fatal("missing claude-code status")
	}
	if !claude.Detected {
		t.Error("claude-code Detected = false, want true")
	}
	if claude.Configured {
		t.Error("claude-code Configured = true, want false")
	}
	if got, want := claude.StatusText, "Detected (Ready to install)"; got != want {
		t.Errorf("claude-code StatusText = %q, want %q", got, want)
	}

	oc := findStatus(statuses, "opencode")
	if oc == nil {
		t.Fatal("missing opencode status")
	}
	if !oc.Detected {
		t.Error("opencode Detected = false, want true")
	}
	if oc.Configured {
		t.Error("opencode Configured = true, want false")
	}
	if got, want := oc.StatusText, "Detected (Ready to install)"; got != want {
		t.Errorf("opencode StatusText = %q, want %q", got, want)
	}

	gemini := findStatus(statuses, "gemini-cli")
	if gemini == nil {
		t.Fatal("missing gemini-cli status")
	}
	if !gemini.Detected {
		t.Error("gemini-cli Detected = false, want true")
	}
	if gemini.Configured {
		t.Error("gemini-cli Configured = true, want false")
	}
	if got, want := gemini.StatusText, "Detected (Ready to install)"; got != want {
		t.Errorf("gemini-cli StatusText = %q, want %q", got, want)
	}

	codex := findStatus(statuses, "codex")
	if codex == nil {
		t.Fatal("missing codex status")
	}
	if !codex.Detected {
		t.Error("codex Detected = false, want true")
	}
	if codex.Configured {
		t.Error("codex Configured = true, want false")
	}
	if got, want := codex.StatusText, "Detected (Ready to install)"; got != want {
		t.Errorf("codex StatusText = %q, want %q", got, want)
	}
}

// TestDetectAgents_AllConfigured verifies the "Configured" path when every
// agent config file is present.
func TestDetectAgents_AllConfigured(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Claude: .claude/mcp/cortex.json
	claudeMcp := filepath.Join(home, ".claude", "mcp", "cortex.json")
	if err := writeFile(claudeMcp, "{}"); err != nil {
		t.Fatalf("seed claude mcp: %v", err)
	}

	// OpenCode: .config/opencode/plugins/cortex.ts
	ocPlugin := filepath.Join(home, ".config", "opencode", "plugins", "cortex.ts")
	if err := writeFile(ocPlugin, "// plugin"); err != nil {
		t.Fatalf("seed opencode plugin: %v", err)
	}

	// Gemini: .gemini/antigravity/mcp.json
	geminiMcp := filepath.Join(home, ".gemini", "antigravity", "mcp.json")
	if err := writeFile(geminiMcp, "{}"); err != nil {
		t.Fatalf("seed gemini mcp: %v", err)
	}

	// Codex: .codex/config.toml
	codexCfg := filepath.Join(home, ".codex", "config.toml")
	if err := writeFile(codexCfg, ""); err != nil {
		t.Fatalf("seed codex config: %v", err)
	}

	statuses := DetectAgents()

	claude := findStatus(statuses, "claude-code")
	if claude == nil {
		t.Fatal("missing claude-code status")
	}
	if !claude.Detected || !claude.Configured {
		t.Errorf("claude-code Detected=%v Configured=%v, want both true", claude.Detected, claude.Configured)
	}
	if got, want := claude.StatusText, "Configured (Plugin Active)"; got != want {
		t.Errorf("claude-code StatusText = %q, want %q", got, want)
	}

	oc := findStatus(statuses, "opencode")
	if oc == nil {
		t.Fatal("missing opencode status")
	}
	if !oc.Detected || !oc.Configured {
		t.Errorf("opencode Detected=%v Configured=%v, want both true", oc.Detected, oc.Configured)
	}
	if got, want := oc.StatusText, "Configured (Plugin Active)"; got != want {
		t.Errorf("opencode StatusText = %q, want %q", got, want)
	}

	gemini := findStatus(statuses, "gemini-cli")
	if gemini == nil {
		t.Fatal("missing gemini-cli status")
	}
	if !gemini.Detected || !gemini.Configured {
		t.Errorf("gemini-cli Detected=%v Configured=%v, want both true", gemini.Detected, gemini.Configured)
	}
	if got, want := gemini.StatusText, "Configured (MCP Registered)"; got != want {
		t.Errorf("gemini-cli StatusText = %q, want %q", got, want)
	}

	codex := findStatus(statuses, "codex")
	if codex == nil {
		t.Fatal("missing codex status")
	}
	if !codex.Detected || !codex.Configured {
		t.Errorf("codex Detected=%v Configured=%v, want both true", codex.Detected, codex.Configured)
	}
	if got, want := codex.StatusText, "Configured"; got != want {
		t.Errorf("codex StatusText = %q, want %q", got, want)
	}
}

// TestDetectAgents_OpenCodeAltPluginPath verifies the .opencode alternative
// plugin path (opencodeAltPlugin) is detected when .config/opencode does not
// have the plugin file.
func TestDetectAgents_OpenCodeAltPluginPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Create only the .opencode directory and its plugin file.
	altPlugin := filepath.Join(home, ".opencode", "plugins", "cortex.ts")
	if err := writeFile(altPlugin, "// alt plugin"); err != nil {
		t.Fatalf("seed alt plugin: %v", err)
	}

	statuses := DetectAgents()

	oc := findStatus(statuses, "opencode")
	if oc == nil {
		t.Fatal("missing opencode status")
	}
	if !oc.Detected {
		t.Error("opencode Detected = false, want true (via .opencode alt path)")
	}
	if !oc.Configured {
		t.Error("opencode Configured = false, want true (via alt plugin)")
	}
	if got, want := oc.StatusText, "Configured (Plugin Active)"; got != want {
		t.Errorf("opencode StatusText = %q, want %q", got, want)
	}
}

// TestDetectAgents_OpenCodeAltDirOnly verifies the .opencode directory is
// detected as "Ready to install" when no plugin file exists.
func TestDetectAgents_OpenCodeAltDirOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	altDir := filepath.Join(home, ".opencode")
	if err := os.MkdirAll(altDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	statuses := DetectAgents()

	oc := findStatus(statuses, "opencode")
	if oc == nil {
		t.Fatal("missing opencode status")
	}
	if !oc.Detected {
		t.Error("opencode Detected = false, want true (via .opencode dir)")
	}
	if oc.Configured {
		t.Error("opencode Configured = true, want false (no plugin file)")
	}
	if got, want := oc.StatusText, "Detected (Ready to install)"; got != want {
		t.Errorf("opencode StatusText = %q, want %q", got, want)
	}
}

// --- Install("ollama") via InstallWithOptions ------------------------------

// TestInstallWithOllama_AgentCoversSwitchCase verifies the "ollama" case in
// InstallWithOptions. SetupOllama gracefully handles an offline daemon by
// setting OllamaOnline=false and proceeding to persist config.
func TestInstallWithOllama_AgentCoversSwitchCase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	res, err := InstallWithOptions("ollama", Options{})
	if err != nil {
		t.Fatalf("InstallWithOptions(ollama) error = %v", err)
	}
	if res == nil {
		t.Fatal("InstallWithOptions(ollama) result is nil")
	}
	if got, want := res.Agent, "ollama"; got != want {
		t.Errorf("Result.Agent = %q, want %q", got, want)
	}
	if res.Files != 1 {
		t.Errorf("Result.Files = %d, want 1", res.Files)
	}
	if res.Destination == "" {
		t.Error("Result.Destination is empty")
	}
}

// --- resolveHome os.UserHomeDir fallback -----------------------------------

// TestResolveHome_FallbackToUserHomeDir covers the os.UserHomeDir path when
// both HOME and USERPROFILE are empty strings. On Windows, os.UserHomeDir
// fails when USERPROFILE is absent; on Unix it returns the home directory.
// Both outcomes exercise the final branch.
func TestResolveHome_FallbackToUserHomeDir(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	got, err := resolveHome()
	if err != nil {
		// Windows path: os.UserHomeDir() fails when USERPROFILE is not set.
		if !strings.Contains(err.Error(), "resolve home directory") {
			t.Errorf("error %q missing 'resolve home directory' prefix", err.Error())
		}
		return
	}
	// Unix path: os.UserHomeDir() succeeds.
	if got == "" {
		t.Fatal("resolveHome() returned empty string")
	}
	if !filepath.IsAbs(got) {
		t.Errorf("resolveHome() = %q, want absolute path", got)
	}
}

// --- DetectAgents InstallDir prefix contracts ------------------------------

// TestDetectAgents_InstallDirPrefixes verifies every agent's InstallDir is
// rooted under the resolved home directory, exercising the full DetectAgents
// code path once more for confidence. Ollama is excluded because its
// InstallDir is the HTTP endpoint URL, not a filesystem path.
func TestDetectAgents_InstallDirPrefixes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	statuses := DetectAgents()
	for _, s := range statuses {
		if s.InstallDir == "" {
			t.Errorf("%s: InstallDir is empty", s.Name)
			continue
		}
		if s.Name == "ollama" {
			// Ollama's InstallDir is "http://localhost:11434", not a path.
			if !strings.HasPrefix(s.InstallDir, "http") {
				t.Errorf("ollama: InstallDir = %q, want HTTP URL", s.InstallDir)
			}
			continue
		}
		if !strings.HasPrefix(s.InstallDir, home) {
			t.Errorf("%s: InstallDir = %q, want prefix %q", s.Name, s.InstallDir, home)
		}
	}
}

// --- helpers ---------------------------------------------------------------

// findStatus returns the AgentStatus with the given name, or nil if absent.
func findStatus(statuses []AgentStatus, name string) *AgentStatus {
	for i := range statuses {
		if statuses[i].Name == name {
			return &statuses[i]
		}
	}
	return nil
}

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveModePrecedence(t *testing.T) {
	legacyRemote := func() *Config {
		cfg := DefaultConfig()
		cfg.MCP.Remote.Enabled, cfg.MCP.Remote.URL = true, "https://cortex.example/mcp"
		return cfg
	}
	legacySync := func() *Config {
		cfg := DefaultConfig()
		cfg.Sync.Enabled, cfg.Sync.URL = true, "https://cortex.example/"
		return cfg
	}
	cases := []struct {
		name       string
		env        string
		cfg        func() *Config
		wantMode   RuntimeMode
		wantSource ModeSource
	}{
		{"default local", "", DefaultConfig, RuntimeLocal, ModeSourceDefault},
		{"legacy mcp.remote.enabled is remote", "", legacyRemote, RuntimeRemote, ModeSourceLegacy},
		{"legacy sync is hybrid", "", legacySync, RuntimeHybrid, ModeSourceLegacy},
		{"file mode beats legacy keys", "", func() *Config { c := legacyRemote(); c.Mode = "local"; return c }, RuntimeLocal, ModeSourceFile},
		{"env beats file mode", "HYBRID", func() *Config { c := legacySync(); c.Mode = "remote"; return c }, RuntimeHybrid, ModeSourceEnv},
		{"env server", "server", DefaultConfig, RuntimeServer, ModeSourceEnv},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(ModeEnvVar, tc.env)
			res, err := ResolveMode(tc.cfg())
			if err != nil {
				t.Fatalf("ResolveMode: %v", err)
			}
			if res.Mode != tc.wantMode || res.Source != tc.wantSource {
				t.Fatalf("got %s/%s, want %s/%s", res.Mode, res.Source, tc.wantMode, tc.wantSource)
			}
		})
	}
}

func TestResolveModeRejectsInvalidValues(t *testing.T) {
	t.Setenv(ModeEnvVar, "bogus")
	if _, err := ResolveMode(DefaultConfig()); err == nil || !strings.Contains(err.Error(), ModeEnvVar) {
		t.Fatalf("invalid env mode error = %v", err)
	}
	t.Setenv(ModeEnvVar, "")
	cfg := DefaultConfig()
	cfg.Mode = "proxy"
	if _, err := ResolveMode(cfg); err == nil {
		t.Fatal("invalid file mode accepted")
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("Validate accepted an invalid mode key")
	}
}

func TestResolveModeEndpointsAndWarnings(t *testing.T) {
	t.Setenv(ModeEnvVar, "")

	cfg := DefaultConfig()
	cfg.Mode = "remote"
	cfg.Sync.Enabled, cfg.Sync.URL = true, "https://cortex.example/"
	res, err := ResolveMode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.RemoteURL != "https://cortex.example" || res.MCPURL != "https://cortex.example/mcp" {
		t.Fatalf("remote endpoints from sync.url = %q / %q", res.RemoteURL, res.MCPURL)
	}
	if !containsWarning(res.Warnings, "sync.enabled is ignored") {
		t.Fatalf("missing ignored-sync warning: %v", res.Warnings)
	}

	cfg = DefaultConfig()
	cfg.Mode = "hybrid"
	cfg.Server.Storage.DSN = "postgres://x"
	res, err = ResolveMode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != RuntimeHybrid || !containsWarning(res.Warnings, "without sync.url") || !containsWarning(res.Warnings, "server.storage.dsn") {
		t.Fatalf("hybrid warnings = %v", res.Warnings)
	}

	// A server DSN alone no longer flips a local config to "server".
	cfg = DefaultConfig()
	cfg.Server.Storage.DSN = "postgres://x"
	if mode, _ := cfg.DetectMode(); mode != ModeLocal {
		t.Fatalf("DSN-only config reported %q, want local", mode)
	}
}

func TestSetPropertyMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Database.Path = filepath.Join(t.TempDir(), "cortex.db")
	if err := cfg.SetProperty("mode", "Remote"); err != nil || cfg.Mode != "remote" {
		t.Fatalf("SetProperty(mode, Remote) = %v, mode %q", err, cfg.Mode)
	}
	if got, _ := cfg.GetProperty("mode"); got != "remote" {
		t.Fatalf("GetProperty(mode) = %q", got)
	}
	if err := cfg.SetProperty("mode", "nope"); err == nil {
		t.Fatal("invalid mode accepted")
	}
	if err := cfg.SetProperty("mode", ""); err != nil || cfg.Mode != "" {
		t.Fatalf("clearing mode: %v %q", err, cfg.Mode)
	}
}

func TestFileModeReadsOnlyTheModeKey(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", dir)

	mode, path, err := FileMode()
	if err != nil || mode != "" || path != "" {
		t.Fatalf("no file: %q %q %v", mode, path, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cortex", "cortex.yaml")); err == nil {
		t.Fatal("FileMode wrote a default config file")
	}

	if err := os.WriteFile(filepath.Join(dir, "cortex.yaml"), []byte("mode: server\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mode, path, err = FileMode()
	if err != nil || mode != RuntimeServer || !strings.HasSuffix(path, "cortex.yaml") {
		t.Fatalf("server file: %q %q %v", mode, path, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "cortex.yaml"), []byte("mode: sideways\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := FileMode(); err == nil {
		t.Fatal("invalid file mode accepted")
	}
}

func containsWarning(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// TestSavePersistsValuesTurnedOff pins the YAML merge-save fix: a field set in
// the existing file and turned off in memory must be written as an explicit
// zero, while unknown keys and comments survive.
func TestSavePersistsValuesTurnedOff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cortex.yaml")
	initial := `# my cortex config
custom_tool_setting: keep-me
database:
    path: ` + filepath.Join(dir, "cortex.db") + `
http:
    enabled: false # local listener off
mcp:
    remote:
        enabled: true
        url: https://cortex.example/mcp
sync:
    enabled: true
    url: https://cortex.example
`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ModeEnvVar, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MCP.Remote.Enabled = false
	cfg.Sync.Enabled = false
	if err := Save(cfg, path); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.MCP.Remote.Enabled || reloaded.Sync.Enabled {
		t.Fatalf("turned-off switches came back: remote=%v sync=%v", reloaded.MCP.Remote.Enabled, reloaded.Sync.Enabled)
	}
	if reloaded.HTTP.Enabled {
		t.Fatal("http.enabled: false (non-zero default) reverted to its default")
	}
	if reloaded.MCP.Remote.URL != "https://cortex.example/mcp" || reloaded.Sync.URL != "https://cortex.example" {
		t.Fatalf("URLs lost: %q %q", reloaded.MCP.Remote.URL, reloaded.Sync.URL)
	}
	raw, _ := os.ReadFile(path)
	for _, want := range []string{"custom_tool_setting: keep-me", "# my cortex config", "# local listener off"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("saved file lost %q:\n%s", want, raw)
		}
	}
}

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/credential"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cortex.yaml")
	body = "database:\n    path: " + filepath.Join(dir, "cortex.db") + "\n" + body
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRemoteBlockDrivesLegacyKeysInMemory(t *testing.T) {
	t.Setenv(ModeEnvVar, "")
	path := writeConfig(t, `version: 2
mode: hybrid
remote:
    url: https://cortex.example/mcp/
    token: env:TEAM_TOKEN
    timeout: 12s
sync:
    enabled: true
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Remote.URL != "https://cortex.example" || cfg.Sync.URL != "https://cortex.example" || cfg.MCP.Remote.URL != "https://cortex.example/mcp" {
		t.Fatalf("derived URLs: remote=%q sync=%q mcp=%q", cfg.Remote.URL, cfg.Sync.URL, cfg.MCP.Remote.URL)
	}
	if cfg.Sync.TokenEnv != "TEAM_TOKEN" || cfg.MCP.Remote.TokenEnv != "TEAM_TOKEN" {
		t.Fatalf("token env not derived: %q %q", cfg.Sync.TokenEnv, cfg.MCP.Remote.TokenEnv)
	}
	if cfg.Sync.Timeout != 12*time.Second || cfg.MCP.Remote.Timeout != 12*time.Second {
		t.Fatalf("timeouts not derived: %v %v", cfg.Sync.Timeout, cfg.MCP.Remote.Timeout)
	}
	res, err := ResolveMode(cfg)
	if err != nil || res.Mode != RuntimeHybrid || res.RemoteURL != "https://cortex.example" {
		t.Fatalf("resolution = %+v, %v", res, err)
	}
}

func TestSaveKeepsRemoteBlockAndFoldsLegacyEdits(t *testing.T) {
	t.Setenv(ModeEnvVar, "")
	path := writeConfig(t, `version: 2
remote:
    url: https://old.example
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// An unrelated save must not persist the derived legacy URLs.
	if err := Save(cfg, path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "old.example/mcp") || strings.Count(string(raw), "old.example") != 1 {
		t.Fatalf("derived legacy URLs persisted:\n%s", raw)
	}

	// A legacy edit (TUI sync URL field, config set sync.url) moves the server.
	cfg.Sync.URL = "https://new.example/"
	if err := Save(cfg, path); err != nil {
		t.Fatal(err)
	}
	if cfg.Remote.URL != "https://new.example" || cfg.MCP.Remote.URL != "https://new.example/mcp" {
		t.Fatalf("in-memory config not re-derived: %+v / %q", cfg.Remote, cfg.MCP.Remote.URL)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Remote.URL != "https://new.example" {
		t.Fatalf("edit not folded into remote.url: %q", reloaded.Remote.URL)
	}
}

func TestValidateRemoteRejectsPlaintextAndBadVersions(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Config)
		want string
	}{
		{"literal token", func(c *Config) { c.Remote.URL, c.Remote.Token = "https://x.example", "ctx_secret" }, "never stored"},
		{"empty env name", func(c *Config) { c.Remote.URL, c.Remote.Token = "https://x.example", "env:" }, "variable name"},
		{"token without url", func(c *Config) { c.Remote.Token = "keychain" }, "remote.url is empty"},
		{"negative timeout", func(c *Config) { c.Remote.Timeout = -time.Second }, "negative"},
		{"future version", func(c *Config) { c.Version = 9 }, "unsupported config version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Database.Path = filepath.Join(t.TempDir(), "c.db")
			tc.edit(cfg)
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestResolveRemoteBearerPrecedence(t *testing.T) {
	credential.UseInMemoryStore()
	t.Setenv("TEAM_TOKEN", "from-env")
	t.Setenv("CORTEX_REMOTE_TOKEN", "")

	cfg := DefaultConfig()
	cfg.SetRemote("https://cortex.example", RemoteTokenKeychain)
	if _, _, err := ResolveRemoteBearer(cfg, cfg.Sync.TokenEnv); err == nil || !strings.Contains(err.Error(), "cortex connect") {
		t.Fatalf("missing keychain entry err = %v", err)
	}
	if err := credential.Set("https://cortex.example", "from-keychain"); err != nil {
		t.Fatal(err)
	}
	cfg.HTTP.Token = "plaintext"
	if token, source, err := ResolveRemoteBearer(cfg, "TEAM_TOKEN"); err != nil || token != "from-keychain" || source != "OS keychain" {
		t.Fatalf("keychain: %q %q %v", token, source, err)
	}

	cfg.SetRemote("https://cortex.example", "env:TEAM_TOKEN")
	if token, _, err := ResolveRemoteBearer(cfg, ""); err != nil || token != "from-env" {
		t.Fatalf("env ref: %q %v", token, err)
	}

	legacy := DefaultConfig()
	legacy.HTTP.Token = "plaintext"
	if token, source, _ := ResolveRemoteBearer(legacy, "CORTEX_REMOTE_TOKEN"); token != "plaintext" || !strings.Contains(source, "migrate") {
		t.Fatalf("legacy http.token: %q %q", token, source)
	}
	if token, source, _ := ResolveRemoteBearer(legacy, "ctx_literal"); token != "ctx_literal" || !strings.Contains(source, "migrate") {
		t.Fatalf("legacy literal: %q %q", token, source)
	}
}

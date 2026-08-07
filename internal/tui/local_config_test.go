package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/internal/config"
)

func testLocalConfig(t *testing.T) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cortex.yaml")
	data := "# keep me\ndatabase:\n  path: test.db\ncustom_setting: preserved\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestDashboardOpensLocalSettings(t *testing.T) {
	cfg := testLocalConfig(t)
	m := New(&Deps{Config: cfg})
	m.Cursor = 7
	updated, _ := m.handleDashboardSelection()
	result := updated.(Model)
	if result.Screen != ScreenLocalConfig || result.LocalCfgDatabasePath.Value() != "test.db" {
		t.Fatalf("local settings = %+v", result)
	}
	view := result.View()
	for _, want := range []string{"Configuration Center", "LOCAL ONLY", "Local database", "1  Local"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
	result.LocalCfgSaved = true
	if saved := result.View(); !strings.Contains(saved, "Restart Cortex") {
		t.Fatal("saved view does not explain restart requirement")
	}
}

func TestLocalSettingsSectionNavigationAndReview(t *testing.T) {
	m := New(&Deps{Config: testLocalConfig(t)}).openLocalConfig()
	for _, step := range []struct {
		key   string
		field int
	}{{"l", 1}, {"tab", 4}, {"right", 7}, {"l", 11}} {
		updated, _ := m.handleLocalConfigKeys(step.key)
		m = updated.(Model)
		if m.LocalCfgFocusField != step.field {
			t.Fatalf("after %q field=%d, want %d", step.key, m.LocalCfgFocusField, step.field)
		}
	}
	view := m.View()
	for _, want := range []string{"Review and apply", "Storage", "HTTP", "MCP", "Sync", "Validate & save"} {
		if !strings.Contains(view, want) {
			t.Errorf("review missing %q", want)
		}
	}
}

func TestSaveLocalConfigValidatesBeforeWriting(t *testing.T) {
	cfg := testLocalConfig(t)
	original, err := os.ReadFile(cfg.LoadedFrom)
	if err != nil {
		t.Fatal(err)
	}
	cmd := saveLocalConfig(&Deps{Config: cfg}, localConfigValues{databasePath: "test.db", httpEnabled: true, httpHost: "127.0.0.1", httpPort: "invalid", syncInterval: "30s"})
	msg := cmd().(localConfigSavedMsg)
	if msg.err == nil {
		t.Fatal("invalid port was accepted")
	}
	after, err := os.ReadFile(cfg.LoadedFrom)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatal("invalid settings changed the YAML")
	}
}

func TestSaveLocalConfigPreservesFriendlyYAML(t *testing.T) {
	cfg := testLocalConfig(t)
	values := localConfigValues{databasePath: "next.db", httpEnabled: true, httpHost: "localhost", httpPort: "7438", mcpTokenEnv: "CORTEX_REMOTE_TOKEN", syncTokenEnv: "CORTEX_REMOTE_TOKEN", syncInterval: "45s"}
	msg := saveLocalConfig(&Deps{Config: cfg}, values)().(localConfigSavedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	data, err := os.ReadFile(cfg.LoadedFrom)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# keep me", "custom_setting: preserved", "path: next.db", "interval: 45s"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("saved YAML missing %q:\n%s", want, data)
		}
	}
}

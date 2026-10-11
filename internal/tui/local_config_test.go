package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"

	tea "github.com/charmbracelet/bubbletea"
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

func TestDashboardOpensConfiguration(t *testing.T) {
	cfg := testLocalConfig(t)
	m := New(&Deps{Config: cfg})
	updated, _ := m.navigateDashboard(ScreenLocalConfig)
	result := updated.(Model)
	if result.Screen != ScreenLocalConfig || result.LocalCfgDatabasePath.Value() != "test.db" {
		t.Fatalf("configuration = %+v", result)
	}
	view := result.View()
	for _, want := range []string{"Configuration", "Essential", "Advanced", "AI PROVIDER", "EMBEDDING", "SERVER"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestConfigurationTabsAndFields(t *testing.T) {
	m := New(&Deps{Config: testLocalConfig(t)}).openLocalConfig()
	updated, _ := m.handleUnifiedConfigKeys("tab")
	m = updated.(Model)
	if unifiedConfigTabForField(m.LocalCfgFocusField) != ConfigTabAdvanced {
		t.Fatalf("tab did not open Advanced (field=%d)", m.LocalCfgFocusField)
	}
	updated, _ = m.handleUnifiedConfigKeys("j")
	m = updated.(Model)
	view := m.View()
	for _, want := range []string{"Database path", "Config format", "HTTP API", "MCP", "SYNC", "EMBEDDING ADVANCED", "Vector dim"} {
		if !strings.Contains(view, want) {
			t.Errorf("advanced view missing %q:\n%s", want, view)
		}
	}
}

func TestLocalConfigMCPProfileCycle(t *testing.T) {
	m := New(&Deps{Config: testLocalConfig(t)}).openLocalConfig()
	m.LocalCfgFocusField = 8 // Tool profile cycler

	if m.LocalCfgMCPProfile != "agent" {
		t.Fatalf("expected initial profile agent, got %q", m.LocalCfgMCPProfile)
	}

	// Cycle forward with 'l'
	updated, _ := m.handleLocalConfigKeys("l")
	m = updated.(Model)
	if m.LocalCfgMCPProfile != "dev" {
		t.Fatalf("expected profile dev, got %q", m.LocalCfgMCPProfile)
	}

	// Cycle forward with 'right'
	updated, _ = m.handleLocalConfigKeys("right")
	m = updated.(Model)
	if m.LocalCfgMCPProfile != "minimal" {
		t.Fatalf("expected profile minimal, got %q", m.LocalCfgMCPProfile)
	}

	// Cycle forward with space
	updated, _ = m.handleLocalConfigKeys(" ")
	m = updated.(Model)
	if m.LocalCfgMCPProfile != "agent" {
		t.Fatalf("expected profile agent, got %q", m.LocalCfgMCPProfile)
	}

	// Cycle backward with 'h'
	updated, _ = m.handleLocalConfigKeys("h")
	m = updated.(Model)
	if m.LocalCfgMCPProfile != "minimal" {
		t.Fatalf("expected profile minimal, got %q", m.LocalCfgMCPProfile)
	}

	// Cycle backward with 'left'
	updated, _ = m.handleLocalConfigKeys("left")
	m = updated.(Model)
	if m.LocalCfgMCPProfile != "dev" {
		t.Fatalf("expected profile dev, got %q", m.LocalCfgMCPProfile)
	}
}

func TestNewWithScreen(t *testing.T) {
	cfg := testLocalConfig(t)
	m := NewWithScreen(&Deps{Config: cfg}, ScreenLocalConfig)
	if m.Screen != ScreenLocalConfig {
		t.Fatalf("expected ScreenLocalConfig, got %v", m.Screen)
	}
	if m.ActiveWorkspace() != 3 {
		t.Fatalf("expected workspace 3, got %d", m.ActiveWorkspace())
	}
	if m.ControlDeckTab != 1 {
		t.Fatalf("expected ControlDeckTab 1, got %d", m.ControlDeckTab)
	}
	if m.LocalCfgMCPProfile != "agent" {
		t.Fatalf("expected LocalCfgMCPProfile agent, got %q", m.LocalCfgMCPProfile)
	}
}

func TestSaveLocalConfigPersistsMCPProfile(t *testing.T) {
	cfg := testLocalConfig(t)
	deps := &Deps{Config: cfg}
	values := localConfigValues{
		databasePath: "next.db",
		httpEnabled:  true,
		httpHost:     "localhost",
		httpPort:     "7438",
		mcpProfile:   "minimal",
		syncInterval: "30s",
	}
	msg := saveLocalConfig(deps, values)().(localConfigSavedMsg)
	if msg.err != nil {
		t.Fatalf("saveLocalConfig failed: %v", msg.err)
	}
	if deps.Config.MCP.Profile != "minimal" {
		t.Fatalf("expected deps.Config.MCP.Profile to be minimal, got %q", deps.Config.MCP.Profile)
	}

	// Verify persistence on disk
	reloaded, err := config.Load(cfg.LoadedFrom)
	if err != nil {
		t.Fatalf("failed to reload config from %q: %v", cfg.LoadedFrom, err)
	}
	if reloaded.MCP.Profile != "minimal" {
		t.Fatalf("expected reloaded.MCP.Profile to be minimal, got %q", reloaded.MCP.Profile)
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

func TestThemeSwitching(t *testing.T) {
	ApplyTheme(true)
	if !IsDark() {
		t.Errorf("expected Dark theme, got Light")
	}

	isDark := ToggleTheme()
	if isDark {
		t.Errorf("expected Light theme after toggle, got Dark")
	}
	if IsDark() {
		t.Errorf("IsDark() returned true for Light theme")
	}

	isDark = ToggleTheme()
	if !isDark {
		t.Errorf("expected Dark theme after second toggle, got Light")
	}
	if !IsDark() {
		t.Errorf("IsDark() returned false for Dark theme")
	}
}

func TestAuthAndIdentityModel(t *testing.T) {
	m := New(&Deps{Version: "2.0.0"})
	expectedUser := "local-user"
	if u := os.Getenv("USER"); u != "" {
		expectedUser = u
	} else if u := os.Getenv("USERNAME"); u != "" {
		expectedUser = u
	}
	if m.CurrentUser != expectedUser {
		t.Errorf("expected %s, got %s", expectedUser, m.CurrentUser)
	}
	if m.UserRole != "local" {
		t.Errorf("expected local role for unauthenticated clean install, got %s", m.UserRole)
	}
	if m.UploadToCortex {
		t.Errorf("expected UploadToCortex to be false on clean local install")
	}
	if !m.IsDarkTheme {
		t.Errorf("expected dark theme by default")
	}
}

// TestConnectEntryPointsRouteToGuidedFlow covers REQ-SYNC-001 acceptance 3: the
// dashboard entry, the unified Configuration Connect entry, and the L key all
// open the first-class guided flow, and the Auth modal no longer exists.
func TestConnectEntryPointsRouteToGuidedFlow(t *testing.T) {
	m := New(&Deps{Config: testLocalConfig(t), Version: "2.0.0"})

	entryPoints := []struct {
		name string
		open func() (tea.Model, tea.Cmd)
	}{
		{"L key", func() (tea.Model, tea.Cmd) { return m.handleKeyPress("L") }},
		{"unified Configuration Connect", func() (tea.Model, tea.Cmd) { return m.openUnifiedConnect() }},
		{"guided flow opener", func() (tea.Model, tea.Cmd) { return m.openConnectFlow() }},
	}
	for _, ep := range entryPoints {
		out, _ := ep.open()
		res := out.(Model)
		if res.Screen != ScreenConnect || res.ConnectStep != ConnectStepURL {
			t.Fatalf("%s: screen=%v step=%v, want ScreenConnect/step 1", ep.name, res.Screen, res.ConnectStep)
		}
	}

	opened := false
	for i, item := range flattenDashboardItems() {
		if item.action != dashboardConnect {
			continue
		}
		out, _ := m.activateDashboardItem(i)
		if out.(Model).Screen != ScreenConnect {
			t.Fatalf("dashboard Connect did not open the flow: %v", out.(Model).Screen)
		}
		opened = true
	}
	if !opened {
		t.Fatal("dashboard menu has no Connect entry")
	}
}

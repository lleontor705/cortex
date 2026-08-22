package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/internal/config"

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
	for _, want := range []string{"Configuration Center", "LOCAL ONLY", "Local Storage", "1 Storage"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
	result.LocalCfgSaved = true
	if saved := result.View(); !strings.Contains(saved, "Restart") {
		t.Fatal("saved view does not explain restart requirement")
	}
}

func TestLocalSettingsSectionNavigationAndReview(t *testing.T) {
	m := New(&Deps{Config: testLocalConfig(t)}).openLocalConfig()
	for _, step := range []struct {
		key   string
		field int
	}{{"tab", 2}, {"tab", 5}, {"right", 8}, {"l", 11}, {"l", 15}} {
		updated, _ := m.handleLocalConfigKeys(step.key)
		m = updated.(Model)
		if m.LocalCfgFocusField != step.field {
			t.Fatalf("after %q field=%d, want %d", step.key, m.LocalCfgFocusField, step.field)
		}
	}
	view := m.View()
	for _, want := range []string{"Review & Apply", "Storage", "HTTP API", "MCP Proxy", "Sync", "Validate & Save"} {
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

func TestConnectToCortexServerModalSave(t *testing.T) {
	cfg := testLocalConfig(t)
	m := New(&Deps{Config: cfg, Version: "2.0.0"})
	if m.UploadToCortex {
		t.Fatal("expected sync to be disabled initially")
	}

	// Open Connect to Server modal
	m.openAuthModal()
	if !m.AuthModalOpen {
		t.Fatal("expected AuthModalOpen to be true")
	}

	// Set Server URL and switch focus
	m.AuthServerURLInput.SetValue("http://localhost:7438")
	updated, _ := m.handleAuthModalKeys(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.AuthFocusField != 1 {
		t.Fatalf("expected focus on Bearer Token (field 1), got %d", m.AuthFocusField)
	}

	// Set Bearer Token and submit
	m.AuthTokenInput.SetValue("ctx_secret_test_token_123")
	updated, _ = m.handleAuthModalKeys(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.AuthModalOpen {
		t.Fatal("expected AuthModal to be closed after submit")
	}
	if !m.UploadToCortex {
		t.Fatal("expected UploadToCortex to be true after server connection")
	}
	if m.UserRole != "admin" {
		t.Fatalf("expected role admin, got %s", m.UserRole)
	}

	// Verify YAML content was written
	data, err := os.ReadFile(cfg.LoadedFrom)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "ctx_secret_test_token_123") {
		t.Errorf("expected token in saved YAML, got:\n%s", content)
	}
	if !strings.Contains(content, "http://localhost:7438") {
		t.Errorf("expected sync URL in saved YAML, got:\n%s", content)
	}
}


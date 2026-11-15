package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
)

// freshUnifiedConfig loads a minimal config and clears provider knobs.
func freshUnifiedConfig(t *testing.T) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cortex.yaml")
	if err := os.WriteFile(path, []byte("database:\n  path: fresh.db\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AI = config.AIConfig{}
	cfg.Search.EmbeddingProvider = ""
	cfg.Search.EmbeddingModel = ""
	return cfg
}

// REQ-CFG-001 / REQ-CFG-005: every scattered key is reachable from one tab.
func TestUnifiedConfigCoversScatteredKeys(t *testing.T) {
	fields := unifiedConfigFields()
	if len(fields) < 17 {
		t.Fatalf("registry exposes %d keys, want at least 17", len(fields))
	}
	seen := map[string]ConfigTab{}
	tabs := map[ConfigTab]int{}
	for _, f := range fields {
		if _, dup := seen[f.Key]; dup {
			t.Fatalf("duplicate key %q in registry", f.Key)
		}
		seen[f.Key] = f.Tab
		tabs[f.Tab]++
	}
	for _, key := range []string{
		"ai_provider", "ai_model", "ai_base_url", "embedding_provider", "embedding_model",
		"database_path", "config_format", "http_enabled", "http_host", "http_port", "http_token",
		"mcp_profile", "mcp_remote", "mcp_url", "mcp_token_env",
		"sync_enabled", "sync_url", "sync_token_env", "sync_interval",
		"embedding_autostart", "theme",
	} {
		if _, ok := seen[key]; !ok {
			t.Errorf("key %q is not reachable from either tab", key)
		}
	}
	if tabs[ConfigTabEssential] == 0 || tabs[ConfigTabAdvanced] == 0 {
		t.Fatal("both tabs must expose editable keys")
	}
}

// REQ-CFG-002: one shared eight-preset enum, no cross-surface downgrade.
func TestUnifiedConfigProviderEnumNoDowngrade(t *testing.T) {
	if len(providerPresets) != 8 {
		t.Fatalf("provider enum has %d presets, want 8", len(providerPresets))
	}
	if providerPresetIndex("openai-compatible") != 7 {
		t.Fatal("openai-compatible must resolve to the Custom preset")
	}
	if got := embeddingProviderConfigValue(providerPresetIndex("openai-compatible")); got != "openai-compatible" {
		t.Fatalf("custom embedding provider downgraded to %q", got)
	}
	if got := aiProviderConfigValue(providerPresetIndex("anthropic")); got != "anthropic" {
		t.Fatalf("AI provider round-trip = %q, want anthropic", got)
	}

	cfg := testLocalConfig(t)
	cfg.Search.EmbeddingProvider = "openai-compatible"
	m := New(&Deps{Config: cfg})
	m.EmbCfgProvider = providerPresetIndex(cfg.Search.EmbeddingProvider)
	m.LocalCfgDirty = true
	if result := unifiedConfigSave(m.deps, m.unifiedConfigInput()); result.Err != nil {
		t.Fatalf("save: %v", result.Err)
	}
	if cfg.Search.EmbeddingProvider != "openai-compatible" {
		t.Fatalf("unchanged provider rewritten to %q", cfg.Search.EmbeddingProvider)
	}
}

// REQ-CFG-003: hot-reload applies without restart; db-path shows a banner.
func TestUnifiedConfigSaveHotReloadAndRestart(t *testing.T) {
	cfg := testLocalConfig(t)
	m := New(&Deps{Config: cfg})
	m.EmbCfgModel.SetValue("nomic-embed-text")
	m.LocalCfgDirty = true
	applied := unifiedConfigSave(m.deps, m.unifiedConfigInput())
	if applied.Kind() != ToastSavedApplied {
		t.Fatalf("hot-reload kind = %v, want applied", applied.Kind())
	}
	if applied.ToastMessage() != "Saved — changes applied" {
		t.Fatalf("toast = %q", applied.ToastMessage())
	}
	if applied.Banner().Visible() {
		t.Fatal("an applied save must never show a restart banner")
	}

	cfg2 := testLocalConfig(t)
	m2 := New(&Deps{Config: cfg2})
	m2.LocalCfgDatabasePath.SetValue("moved.db")
	m2.LocalCfgDirty = true
	restart := unifiedConfigSave(m2.deps, m2.unifiedConfigInput())
	if restart.Kind() != ToastSavedRestartRequired {
		t.Fatalf("db-path kind = %v, want restart-required", restart.Kind())
	}
	const wantBanner = "⚠ Saved. Restart required for: database path. All other changes are already applied."
	if got := restart.Banner().Text(); got != wantBanner {
		t.Fatalf("banner = %q, want %q", got, wantBanner)
	}
}

// REQ-CFG-004: an empty token field never erases the loaded http.token.
func TestUnifiedConfigEmptyTokenPreservesHTTPToken(t *testing.T) {
	cfg := testLocalConfig(t)
	cfg.HTTP.Token = "ctx_existing_secret"
	m := New(&Deps{Config: cfg})
	m.LocalCfgDirty = true
	m.AuthTokenInput.SetValue("")
	if result := unifiedConfigSave(m.deps, m.unifiedConfigInput()); result.Err != nil {
		t.Fatalf("save: %v", result.Err)
	}
	if cfg.HTTP.Token != "ctx_existing_secret" {
		t.Fatalf("empty token field erased http.token: %q", cfg.HTTP.Token)
	}

	cfg2 := testLocalConfig(t)
	cfg2.HTTP.Token = "ctx_old"
	m2 := New(&Deps{Config: cfg2})
	m2.LocalCfgDirty = true
	m2.AuthTokenInput.SetValue("ctx_new")
	if result := unifiedConfigSave(m2.deps, m2.unifiedConfigInput()); result.Err != nil {
		t.Fatalf("save: %v", result.Err)
	}
	if cfg2.HTTP.Token != "ctx_new" {
		t.Fatalf("entered token not applied: %q", cfg2.HTTP.Token)
	}
}

// REQ-CFG-006: the format note appears on change, clears on revert, and the
// save retires the old file automatically (verified against config.Save).
func TestUnifiedConfigFormatChangeAndNote(t *testing.T) {
	cfg := testLocalConfig(t)
	m := New(&Deps{Config: cfg})
	m.Width, m.Height = 120, 40
	m.LocalCfgFormat = 1
	if !unifiedConfigFormatChanged(m) {
		t.Fatal("format change not detected")
	}
	m.LocalCfgFocusField = configAdvancedFieldBase + 1 // config_format row
	if view := m.viewUnifiedConfig(); !strings.Contains(view, ".bak") {
		t.Fatalf("format note missing from Advanced view:\n%s", view)
	}
	m.LocalCfgFormat = 0
	if unifiedConfigFormatChanged(m) {
		t.Fatal("reverting the format did not clear the change")
	}
	if view := m.viewUnifiedConfig(); strings.Contains(view, ".bak") {
		t.Fatal("reverted view still shows the format note")
	}

	cfg2 := testLocalConfig(t)
	yamlPath := cfg2.LoadedFrom
	m2 := New(&Deps{Config: cfg2})
	m2.LocalCfgFormat = 1
	m2.LocalCfgDirty = true
	result := unifiedConfigSave(m2.deps, m2.unifiedConfigInput())
	if result.Err != nil {
		t.Fatalf("format save: %v", result.Err)
	}
	if len(result.RestartFields) != 1 || result.RestartFields[0] != "config format" {
		t.Fatalf("restart fields = %v, want [config format]", result.RestartFields)
	}
	if _, err := os.Stat(yamlPath + ".bak"); err != nil {
		t.Fatalf("old config was not retired to .bak: %v", err)
	}
}

// REQ-CFG-005: a first-run install opens on Essential with working defaults.
func TestUnifiedConfigFreshInstallDefaults(t *testing.T) {
	cfg := freshUnifiedConfig(t)
	m := New(&Deps{Config: cfg})
	m.Width, m.Height = 120, 40

	essential := m.viewUnifiedConfig()
	for _, want := range []string{"Essential", "Ollama", "nomic-embed-text", "Sync: not configured"} {
		if !strings.Contains(essential, want) {
			t.Errorf("fresh Essential view missing %q:\n%s", want, essential)
		}
	}

	result := unifiedConfigSave(m.deps, m.unifiedConfigInput())
	if result.Err != nil {
		t.Fatalf("fresh save: %v", result.Err)
	}
	if result.Kind() != ToastSavedApplied {
		t.Fatalf("fresh save kind = %v, want applied", result.Kind())
	}
	if cfg.AI.Provider != "ollama" {
		t.Errorf("AI provider default = %q, want ollama", cfg.AI.Provider)
	}
	if cfg.Search.EmbeddingProvider != "ollama" {
		t.Errorf("embedding provider default = %q, want ollama", cfg.Search.EmbeddingProvider)
	}
	if cfg.Search.EmbeddingModel != "nomic-embed-text" {
		t.Errorf("embedding model default = %q", cfg.Search.EmbeddingModel)
	}
	if cfg.Sync.Enabled {
		t.Error("sync must default to disabled")
	}
}

// REQ-CFG-001: the two tabs are editable and every registered key renders.
func TestUnifiedConfigTabsEditKeys(t *testing.T) {
	m := New(&Deps{Config: testLocalConfig(t)})
	m.Width, m.Height = 120, 40

	m.LocalCfgFocusField = 0
	updated, _ := m.handleUnifiedConfigKeys("right")
	m = updated.(Model)
	if m.LocalCfgLLMProvider != 1 {
		t.Fatalf("AI provider did not cycle: %d", m.LocalCfgLLMProvider)
	}

	updated, _ = m.handleUnifiedConfigKeys("tab")
	m = updated.(Model)
	if unifiedConfigTabForField(m.LocalCfgFocusField) != ConfigTabAdvanced {
		t.Fatal("tab key did not switch to Advanced")
	}
	m.LocalCfgFocusField = configAdvancedFieldBase + 2 // http_enabled
	m.LocalCfgHTTPEnabled = false
	updated, _ = m.handleUnifiedConfigKeys(" ")
	m = updated.(Model)
	if !m.LocalCfgHTTPEnabled {
		t.Fatal("HTTP API toggle did not flip")
	}

	for i, f := range unifiedConfigFields() {
		m.LocalCfgFocusField = unifiedConfigFieldNumber(i)
		if !strings.Contains(m.viewUnifiedConfig(), f.Label+":") {
			t.Errorf("field %q not rendered on %s", f.Key, f.Tab)
		}
	}
}

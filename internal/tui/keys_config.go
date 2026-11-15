package tui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleLocalConfigKeys(key string) (tea.Model, tea.Cmd) {
	if m.LocalCfgSaving {
		return m, nil
	}
	if m.LocalCfgSaved {
		switch key {
		case "esc":
			m.NavPop()
			m.Cursor = 0
			return m, loadStats(m.deps)
		case "q", "enter":
			m.NavReset()
			m.Cursor = 0
			return m, loadStats(m.deps)
		}
		return m, nil
	}
	switch key {
	case "shift+tab":
		m.LocalCfgFocusField = localConfigSectionStart(max(0, localConfigSection(m.LocalCfgFocusField)-1))
	case "tab":
		m.LocalCfgFocusField = localConfigSectionStart(min(5, localConfigSection(m.LocalCfgFocusField)+1))
	case "left", "h":
		switch m.LocalCfgFocusField {
		case 1:
			m.LocalCfgFormat = (m.LocalCfgFormat + 2) % 3
			m.LocalCfgDirty = true
		case 2:
			m.LocalCfgLLMProvider = (m.LocalCfgLLMProvider + 7) % 8
			m.LocalCfgDirty = true
		case 8:
			m.LocalCfgMCPProfile = prevMCPProfile(m.LocalCfgMCPProfile)
			m.LocalCfgDirty = true
		default:
			m.LocalCfgFocusField = localConfigSectionStart(max(0, localConfigSection(m.LocalCfgFocusField)-1))
		}
	case "right", "l":
		switch m.LocalCfgFocusField {
		case 1:
			m.LocalCfgFormat = (m.LocalCfgFormat + 1) % 3
			m.LocalCfgDirty = true
		case 2:
			m.LocalCfgLLMProvider = (m.LocalCfgLLMProvider + 1) % 8
			m.LocalCfgDirty = true
		case 8:
			m.LocalCfgMCPProfile = nextMCPProfile(m.LocalCfgMCPProfile)
			m.LocalCfgDirty = true
		default:
			m.LocalCfgFocusField = localConfigSectionStart(min(5, localConfigSection(m.LocalCfgFocusField)+1))
		}
	case "up", "k":
		if m.LocalCfgFocusField > 0 {
			m.LocalCfgFocusField--
		}
	case "down", "j":
		if m.LocalCfgFocusField < 16 {
			m.LocalCfgFocusField++
		}
	case " ":
		switch m.LocalCfgFocusField {
		case 1:
			m.LocalCfgFormat = (m.LocalCfgFormat + 1) % 3
		case 2:
			m.LocalCfgLLMProvider = (m.LocalCfgLLMProvider + 1) % 8
		case 5:
			m.LocalCfgHTTPEnabled = !m.LocalCfgHTTPEnabled
		case 8:
			m.LocalCfgMCPProfile = nextMCPProfile(m.LocalCfgMCPProfile)
		case 9:
			m.LocalCfgMCPRemote = !m.LocalCfgMCPRemote
		case 12:
			m.LocalCfgSyncEnabled = !m.LocalCfgSyncEnabled
		default:
			return m, nil
		}
		m.LocalCfgDirty = true
	case "enter":
		switch m.LocalCfgFocusField {
		case 0:
			m.LocalCfgDatabasePath.Focus()
		case 1:
			m.LocalCfgFormat = (m.LocalCfgFormat + 1) % 3
			m.LocalCfgDirty = true
		case 2:
			m.LocalCfgLLMProvider = (m.LocalCfgLLMProvider + 1) % 8
			m.LocalCfgDirty = true
		case 3:
			m.LocalCfgLLMModel.Focus()
		case 4:
			m.LocalCfgLLMBaseURL.Focus()
		case 6:
			m.LocalCfgHTTPHost.Focus()
		case 7:
			m.LocalCfgHTTPPort.Focus()
		case 8:
			m.LocalCfgMCPProfile = nextMCPProfile(m.LocalCfgMCPProfile)
			m.LocalCfgDirty = true
		case 9:
			m.LocalCfgMCPRemote = !m.LocalCfgMCPRemote
			m.LocalCfgDirty = true
		case 10:
			m.LocalCfgMCPURL.Focus()
		case 11:
			m.LocalCfgMCPTokenEnv.Focus()
		case 12:
			m.LocalCfgSyncEnabled = !m.LocalCfgSyncEnabled
			m.LocalCfgDirty = true
		case 13:
			m.LocalCfgSyncURL.Focus()
		case 14:
			m.LocalCfgSyncTokenEnv.Focus()
		case 15:
			m.LocalCfgSyncInterval.Focus()
		case 16:
			return m.startLocalConfigSave()
		}
	case "t", "T":
		providers := []string{"None", "Ollama", "OpenAI", "Anthropic", "OpenRouter", "Groq", "DeepSeek", "Custom"}
		provName := "Ollama"
		if m.LocalCfgLLMProvider >= 0 && m.LocalCfgLLMProvider < len(providers) {
			provName = providers[m.LocalCfgLLMProvider]
		}
		m.TestConnTesting = true
		m.TestConnStatus = ""
		return m, testConnectionCmd(provName, m.LocalCfgLLMBaseURL.Value())
	case "i", "I":
		m.NavPush(ScreenSetup)
		m.Cursor = 0
		return m, detectAgentsCmd()
	case "s", "S":
		return m.startLocalConfigSave()
	case "r", "R":
		return m.openLocalConfig(), nil
	case "esc":
		m.NavPop()
		m.Cursor = 0
		return m, loadStats(m.deps)
	case "q":
		m.NavReset()
		m.Cursor = 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

func (m Model) handleEmbeddingConfigKeys(key string) (tea.Model, tea.Cmd) {
	// If async operation running, only allow esc
	if m.EmbCfgPulling || m.EmbCfgStarting || m.EmbCfgSaving || m.EmbCfgReindexing {
		return m, nil
	}

	// Post-save: reindex key available when warning is shown
	if m.EmbCfgSaved && m.EmbCfgReindexWarning && !m.EmbCfgReindexing {
		if key == "x" || key == "X" {
			m.EmbCfgReindexing = true
			m.EmbCfgReindexWarning = false
			m.EmbCfgError = ""
			return m, tea.Batch(m.EmbCfgSpinner.Tick, startReindexCmd(m.deps))
		}
	}

	// Post-save Ollama actions
	if m.EmbCfgSaved && m.EmbCfgProvider == 1 && m.EmbCfgOllamaChecked {
		switch key {
		case "s", "S":
			if !m.EmbCfgOllamaRunning {
				m.EmbCfgStarting = true
				m.EmbCfgError = ""
				baseURL := ""
				if m.deps.Config != nil {
					baseURL = m.deps.Config.Search.EmbeddingBaseURL
				}
				return m, tea.Batch(m.EmbCfgSpinner.Tick, startOllamaCmd(baseURL))
			}
		case "p", "P":
			if m.EmbCfgOllamaRunning && !m.EmbCfgOllamaHasModel {
				m.EmbCfgPulling = true
				m.EmbCfgError = ""
				baseURL := ""
				model := m.EmbCfgModel.Value()
				if m.deps.Config != nil {
					baseURL = m.deps.Config.Search.EmbeddingBaseURL
				}
				return m, tea.Batch(m.EmbCfgSpinner.Tick, pullOllamaModelCmd(baseURL, model))
			}
		case "esc":
			m.NavPop()
			m.Cursor = 0
			return m, loadStats(m.deps)
		case "q", "enter":
			m.NavReset()
			m.Cursor = 0
			return m, loadStats(m.deps)
		}
		return m, nil
	}

	// Post-save (non-ollama or not yet checked)
	if m.EmbCfgSaved {
		switch key {
		case "esc":
			m.NavPop()
			m.Cursor = 0
			return m, loadStats(m.deps)
		case "q", "enter":
			m.NavReset()
			m.Cursor = 0
			return m, loadStats(m.deps)
		}
		return m, nil
	}

	// Reload config from disk (r key works in any non-editing state)
	if key == "r" || key == "R" {
		m.EmbCfgSaving = true
		m.EmbCfgError = ""
		return m, tea.Batch(m.EmbCfgSpinner.Tick, reloadConfigCmd(m.deps))
	}

	maxField := 4 // provider(0), model(1), vector(2), autostart(3), save(4)

	switch key {
	case "up", "k":
		if m.EmbCfgFocusField > 0 {
			m.EmbCfgFocusField--
		}
	case "down", "j":
		if m.EmbCfgFocusField < maxField {
			m.EmbCfgFocusField++
		}
	case "left", "h":
		if m.EmbCfgFocusField == 0 {
			m.EmbCfgProvider = (m.EmbCfgProvider + 2) % 3 // cycle left
			m.EmbCfgDirty = true
		}
	case "right", "l":
		if m.EmbCfgFocusField == 0 {
			m.EmbCfgProvider = (m.EmbCfgProvider + 1) % 3 // cycle right
			m.EmbCfgDirty = true
		}
	case " ":
		switch m.EmbCfgFocusField {
		case 2:
			m.EmbCfgVector = !m.EmbCfgVector
			m.EmbCfgDirty = true
		case 3:
			m.EmbCfgAutoStart = !m.EmbCfgAutoStart
			m.EmbCfgDirty = true
		}
	case "enter":
		switch m.EmbCfgFocusField {
		case 1: // Focus model text input
			m.EmbCfgModel.Focus()
			return m, nil
		case 4: // Save
			m.EmbCfgSaving = true
			m.EmbCfgSaved = false
			m.EmbCfgError = ""
			return m, tea.Batch(
				m.EmbCfgSpinner.Tick,
				saveEmbeddingConfig(m.deps, m.EmbCfgProvider, m.EmbCfgModel.Value(), m.EmbCfgVector, m.EmbCfgAutoStart),
			)
		}
	case "esc":
		m.NavPop()
		m.Cursor = 0
		return m, loadStats(m.deps)
	case "q":
		m.NavReset()
		m.Cursor = 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

// ─── Unified Configuration (REQ-CFG-001 .. REQ-CFG-006) ─────────────────────
//
// One Configuration surface with two progressive-disclosure tabs replaces the
// retired Local Config form and Embedding Settings screen. The zero value of
// ConfigTab is Essential so a fresh install never opens Advanced. Field focus
// reuses the existing LocalCfgFocusField, so the active tab is derived from the
// focus range and no additional model state is required.

// ConfigTab selects the progressive-disclosure tab of the single Configuration
// screen.
type ConfigTab int

const (
	ConfigTabEssential ConfigTab = iota
	ConfigTabAdvanced
)

// String returns the stable tab label used by the tab bar and tests.
func (t ConfigTab) String() string {
	if t == ConfigTabAdvanced {
		return "Advanced"
	}
	return "Essential"
}

const (
	configEssentialFieldBase = 0
	configAdvancedFieldBase  = 100
	configEssentialFields    = 7
)

// Fresh-install defaults (REQ-CFG-005): an empty configuration resolves to a
// working local setup without opening the Advanced tab.
const (
	defaultAIProvider     = "ollama"
	defaultEmbeddingModel = "nomic-embed-text"
)

// providerPresets is the single eight-preset provider enum shared by the AI and
// embedding selectors (REQ-CFG-002). The retired Embedding Settings screen
// offered three presets and rewrote a fourth-valued provider on save; both
// selectors now offer the same eight presets in the same order.
var providerPresets = []string{"none", "ollama", "openai", "anthropic", "openrouter", "groq", "deepseek", "custom"}

func providerPresetName(index int) string {
	if index < 0 || index >= len(providerPresets) {
		return providerPresets[0]
	}
	return providerPresets[index]
}

// providerDisplayName is the human-facing label for a preset; the persisted
// value stays lowercase.
func providerDisplayName(index int) string {
	switch providerPresetName(index) {
	case "none":
		return "None"
	case "ollama":
		return "Ollama"
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	case "openrouter":
		return "OpenRouter"
	case "groq":
		return "Groq"
	case "deepseek":
		return "DeepSeek"
	default:
		return "Custom"
	}
}

// providerPresetIndex resolves a persisted provider value back to its preset.
// The embedding layer's openai-compatible value is the Custom preset, so a
// custom endpoint round-trips instead of collapsing to a smaller enum.
func providerPresetIndex(name string) int {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch normalized {
	case "", "none":
		return 0
	case "openai-compatible":
		return len(providerPresets) - 1
	}
	for i, p := range providerPresets {
		if p == normalized {
			return i
		}
	}
	return 0
}

// aiProviderConfigValue maps an AI preset to the persisted ai.provider value.
func aiProviderConfigValue(index int) string {
	if name := providerPresetName(index); name != "none" {
		return name
	}
	return ""
}

// embeddingProviderConfigValue maps a preset to a value the embedding layer
// accepts. Native presets pass through; the remaining presets are
// OpenAI-compatible endpoints and persist as openai-compatible — the exact
// value the old three-preset cycle used to downgrade.
func embeddingProviderConfigValue(index int) string {
	switch name := providerPresetName(index); name {
	case "none":
		return ""
	case "ollama", "openai":
		return name
	default:
		return "openai-compatible"
	}
}

type configFieldKind int

const (
	configFieldText configFieldKind = iota
	configFieldCycle
	configFieldToggle
	configFieldAction
)

// configField is one row of the unified Configuration screen. Restart marks the
// fields whose change genuinely needs a process restart (REQ-CFG-003).
type configField struct {
	Key     string
	Label   string
	Tab     ConfigTab
	Kind    configFieldKind
	Restart bool
}

// unifiedConfigFields is the ordered registry of every configuration key the
// two tabs expose. View, handlers, and the coverage test share it, so no
// capability depends on the command palette (REQ-CFG-001).
func unifiedConfigFields() []configField {
	return []configField{
		{Key: "ai_provider", Label: "Provider", Tab: ConfigTabEssential, Kind: configFieldCycle},
		{Key: "ai_model", Label: "Model", Tab: ConfigTabEssential, Kind: configFieldText},
		{Key: "ai_base_url", Label: "Base URL", Tab: ConfigTabEssential, Kind: configFieldText},
		{Key: "embedding_provider", Label: "Provider", Tab: ConfigTabEssential, Kind: configFieldCycle},
		{Key: "embedding_model", Label: "Model", Tab: ConfigTabEssential, Kind: configFieldText},
		{Key: "server_status", Label: "Status", Tab: ConfigTabEssential, Kind: configFieldAction},
		{Key: "save_essential", Label: "Save & apply", Tab: ConfigTabEssential, Kind: configFieldAction},
		{Key: "database_path", Label: "Database path", Tab: ConfigTabAdvanced, Kind: configFieldText, Restart: true},
		{Key: "config_format", Label: "Config format", Tab: ConfigTabAdvanced, Kind: configFieldCycle, Restart: true},
		{Key: "http_enabled", Label: "HTTP API", Tab: ConfigTabAdvanced, Kind: configFieldToggle},
		{Key: "http_host", Label: "Host", Tab: ConfigTabAdvanced, Kind: configFieldText},
		{Key: "http_port", Label: "Port", Tab: ConfigTabAdvanced, Kind: configFieldText},
		{Key: "http_token", Label: "Token", Tab: ConfigTabAdvanced, Kind: configFieldText},
		{Key: "mcp_profile", Label: "Profile", Tab: ConfigTabAdvanced, Kind: configFieldCycle},
		{Key: "mcp_remote", Label: "Remote", Tab: ConfigTabAdvanced, Kind: configFieldToggle},
		{Key: "mcp_url", Label: "URL", Tab: ConfigTabAdvanced, Kind: configFieldText},
		{Key: "mcp_token_env", Label: "Token env", Tab: ConfigTabAdvanced, Kind: configFieldText},
		{Key: "sync_enabled", Label: "Sync", Tab: ConfigTabAdvanced, Kind: configFieldToggle},
		{Key: "sync_url", Label: "Server URL", Tab: ConfigTabAdvanced, Kind: configFieldText},
		{Key: "sync_token_env", Label: "Token env", Tab: ConfigTabAdvanced, Kind: configFieldText},
		{Key: "sync_interval", Label: "Interval", Tab: ConfigTabAdvanced, Kind: configFieldText},
		{Key: "embedding_autostart", Label: "Autostart Ollama", Tab: ConfigTabAdvanced, Kind: configFieldToggle},
		{Key: "theme", Label: "Theme", Tab: ConfigTabAdvanced, Kind: configFieldCycle},
		{Key: "embedding_dimensions", Label: "Vector dim", Tab: ConfigTabAdvanced, Kind: configFieldText},
		{Key: "save_advanced", Label: "Save & apply", Tab: ConfigTabAdvanced, Kind: configFieldAction},
	}
}

// unifiedConfigTabForField derives the active tab from the shared focus field.
func unifiedConfigTabForField(field int) ConfigTab {
	if field >= configAdvancedFieldBase {
		return ConfigTabAdvanced
	}
	return ConfigTabEssential
}

func unifiedConfigRegistryIndex(field int) int {
	if field >= configAdvancedFieldBase {
		return configEssentialFields + (field - configAdvancedFieldBase)
	}
	return field
}

func unifiedConfigField(field int) (configField, bool) {
	fields := unifiedConfigFields()
	idx := unifiedConfigRegistryIndex(field)
	if idx < 0 || idx >= len(fields) {
		return configField{}, false
	}
	return fields[idx], true
}

// unifiedConfigFieldRange returns the first and last focus field of a tab.
func unifiedConfigFieldRange(tab ConfigTab) (int, int) {
	if tab == ConfigTabAdvanced {
		return configAdvancedFieldBase, configAdvancedFieldBase + (len(unifiedConfigFields()) - configEssentialFields) - 1
	}
	return configEssentialFieldBase, configEssentialFieldBase + configEssentialFields - 1
}

// unifiedConfigFieldNumber is the inverse of unifiedConfigRegistryIndex.
func unifiedConfigFieldNumber(regIndex int) int {
	if regIndex < configEssentialFields {
		return configEssentialFieldBase + regIndex
	}
	return configAdvancedFieldBase + (regIndex - configEssentialFields)
}

func (m Model) effectiveAIProviderIndex() int {
	if !m.LocalCfgDirty {
		if m.deps != nil && m.deps.Config != nil {
			if p := strings.TrimSpace(m.deps.Config.AI.Provider); p != "" {
				return providerPresetIndex(p)
			}
		}
		if m.LocalCfgLLMProvider != 0 {
			return m.LocalCfgLLMProvider
		}
		return providerPresetIndex(defaultAIProvider)
	}
	return m.LocalCfgLLMProvider
}

func (m Model) effectiveEmbeddingProviderIndex() int {
	if !m.LocalCfgDirty {
		if m.deps != nil && m.deps.Config != nil {
			if p := strings.TrimSpace(m.deps.Config.Search.EmbeddingProvider); p != "" {
				return providerPresetIndex(p)
			}
		}
		if m.EmbCfgProvider != 0 {
			return m.EmbCfgProvider
		}
		return providerPresetIndex(defaultAIProvider)
	}
	return m.EmbCfgProvider
}

// handleUnifiedConfigKeys drives the unified Configuration screen. Connect is
// a stopgap entry point that task tui-redesign-017 replaces with the guided
// flow; leaving the handler here keeps the screen self-contained for now.
func (m Model) handleUnifiedConfigKeys(key string) (tea.Model, tea.Cmd) {
	if m.LocalCfgSaving {
		return m, nil
	}
	tab := unifiedConfigTabForField(m.LocalCfgFocusField)
	switch key {
	case "tab", "shift+tab":
		if tab == ConfigTabEssential {
			m.LocalCfgFocusField = configAdvancedFieldBase
		} else {
			m.LocalCfgFocusField = configEssentialFieldBase
		}
		return m, nil
	case "up", "k":
		if first, _ := unifiedConfigFieldRange(tab); m.LocalCfgFocusField > first {
			m.LocalCfgFocusField--
		}
		return m, nil
	case "down", "j":
		if _, last := unifiedConfigFieldRange(tab); m.LocalCfgFocusField < last {
			m.LocalCfgFocusField++
		}
		return m, nil
	case "left", "h":
		return m.cycleUnifiedConfigField(-1)
	case "right", "l":
		return m.cycleUnifiedConfigField(1)
	case " ", "enter":
		return m.activateUnifiedConfigField()
	case "s", "S":
		m.LocalCfgSaving, m.LocalCfgSaved, m.LocalCfgError = true, false, ""
		return m, tea.Batch(m.LocalCfgSpinner.Tick, saveUnifiedConfig(m.deps, m.unifiedConfigInput()))
	case "r", "R":
		return m.openLocalConfig(), nil
	case "c", "C":
		return m.openUnifiedConnect()
	case "esc":
		m.NavPop()
		m.Cursor = 0
		return m, loadStats(m.deps)
	case "q":
		m.NavReset()
		m.Cursor = 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

func (m Model) cycleUnifiedConfigField(delta int) (tea.Model, tea.Cmd) {
	field, ok := unifiedConfigField(m.LocalCfgFocusField)
	if !ok {
		return m, nil
	}
	switch field.Key {
	case "ai_provider":
		m.LocalCfgLLMProvider = wrapIndex(m.LocalCfgLLMProvider+delta, len(providerPresets))
	case "embedding_provider":
		m.EmbCfgProvider = wrapIndex(m.EmbCfgProvider+delta, len(providerPresets))
	case "config_format":
		m.LocalCfgFormat = wrapIndex(m.LocalCfgFormat+delta, 3)
	case "mcp_profile":
		if delta > 0 {
			m.LocalCfgMCPProfile = nextMCPProfile(m.LocalCfgMCPProfile)
		} else {
			m.LocalCfgMCPProfile = prevMCPProfile(m.LocalCfgMCPProfile)
		}
	case "theme":
		m.IsDarkTheme = ToggleTheme()
	default:
		first, last := unifiedConfigFieldRange(unifiedConfigTabForField(m.LocalCfgFocusField))
		next := m.LocalCfgFocusField + delta
		if next < first {
			next = last
		}
		if next > last {
			next = first
		}
		m.LocalCfgFocusField = next
		return m, nil
	}
	m.LocalCfgDirty = true
	return m, nil
}

func (m Model) activateUnifiedConfigField() (tea.Model, tea.Cmd) {
	field, ok := unifiedConfigField(m.LocalCfgFocusField)
	if !ok {
		return m, nil
	}
	switch field.Key {
	case "http_enabled":
		m.LocalCfgHTTPEnabled = !m.LocalCfgHTTPEnabled
	case "mcp_remote":
		m.LocalCfgMCPRemote = !m.LocalCfgMCPRemote
	case "sync_enabled":
		m.LocalCfgSyncEnabled = !m.LocalCfgSyncEnabled
	case "embedding_autostart":
		m.EmbCfgAutoStart = !m.EmbCfgAutoStart
	case "ai_provider":
		m.LocalCfgLLMProvider = wrapIndex(m.LocalCfgLLMProvider+1, len(providerPresets))
	case "embedding_provider":
		m.EmbCfgProvider = wrapIndex(m.EmbCfgProvider+1, len(providerPresets))
	case "config_format":
		m.LocalCfgFormat = wrapIndex(m.LocalCfgFormat+1, 3)
	case "mcp_profile":
		m.LocalCfgMCPProfile = nextMCPProfile(m.LocalCfgMCPProfile)
	case "theme":
		m.IsDarkTheme = ToggleTheme()
	case "ai_model":
		m.LocalCfgLLMModel.Focus()
		return m, textinput.Blink
	case "ai_base_url":
		m.LocalCfgLLMBaseURL.Focus()
		return m, textinput.Blink
	case "embedding_model":
		m.EmbCfgModel.Focus()
		return m, textinput.Blink
	case "database_path":
		m.LocalCfgDatabasePath.Focus()
		return m, textinput.Blink
	case "http_host":
		m.LocalCfgHTTPHost.Focus()
		return m, textinput.Blink
	case "http_port":
		m.LocalCfgHTTPPort.Focus()
		return m, textinput.Blink
	case "http_token":
		m.AuthTokenInput.Focus()
		return m, textinput.Blink
	case "mcp_url":
		m.LocalCfgMCPURL.Focus()
		return m, textinput.Blink
	case "mcp_token_env":
		m.LocalCfgMCPTokenEnv.Focus()
		return m, textinput.Blink
	case "sync_url":
		m.LocalCfgSyncURL.Focus()
		return m, textinput.Blink
	case "sync_token_env":
		m.LocalCfgSyncTokenEnv.Focus()
		return m, textinput.Blink
	case "sync_interval":
		m.LocalCfgSyncInterval.Focus()
		return m, textinput.Blink
	case "embedding_dimensions":
		m.LocalCfgEmbeddingDimensions.Focus()
		return m, textinput.Blink
	case "server_status":
		return m.openUnifiedConnect()
	case "save_essential", "save_advanced":
		m.LocalCfgSaving, m.LocalCfgSaved, m.LocalCfgError = true, false, ""
		return m, tea.Batch(m.LocalCfgSpinner.Tick, saveUnifiedConfig(m.deps, m.unifiedConfigInput()))
	}
	m.LocalCfgDirty = true
	return m, nil
}

// openUnifiedConnect is the Configuration screen's Connect entry point. It opens
// the first-class guided Connect flow (REQ-SYNC-001).
func (m Model) openUnifiedConnect() (tea.Model, tea.Cmd) {
	return m.openConnectFlow()
}

func wrapIndex(i, n int) int {
	if n <= 0 {
		return 0
	}
	return ((i % n) + n) % n
}

// unifiedConfigInput is the staged snapshot the save path consumes.
type unifiedConfigInput struct {
	aiProvider        int
	aiModel           string
	aiBaseURL         string
	embeddingProvider int
	embeddingModel    string
	databasePath      string
	format            int
	httpEnabled       bool
	httpHost          string
	httpPort          string
	httpToken         string
	mcpProfile        string
	mcpRemote         bool
	mcpURL            string
	mcpTokenEnv       string
	syncEnabled       bool
	syncURL           string
	syncTokenEnv      string
	syncInterval      string
	autoStart         bool
	embeddingDims     string
	// applyDefaults resolves the documented fresh-install defaults when the
	// form was saved without edits (REQ-CFG-005).
	applyDefaults bool
}

func (m Model) unifiedConfigInput() unifiedConfigInput {
	return unifiedConfigInput{
		aiProvider:        m.LocalCfgLLMProvider,
		aiModel:           m.LocalCfgLLMModel.Value(),
		aiBaseURL:         m.LocalCfgLLMBaseURL.Value(),
		embeddingProvider: m.EmbCfgProvider,
		embeddingModel:    m.EmbCfgModel.Value(),
		databasePath:      m.LocalCfgDatabasePath.Value(),
		format:            m.LocalCfgFormat,
		httpEnabled:       m.LocalCfgHTTPEnabled,
		httpHost:          m.LocalCfgHTTPHost.Value(),
		httpPort:          m.LocalCfgHTTPPort.Value(),
		httpToken:         m.AuthTokenInput.Value(),
		mcpProfile:        m.LocalCfgMCPProfile,
		mcpRemote:         m.LocalCfgMCPRemote,
		mcpURL:            m.LocalCfgMCPURL.Value(),
		mcpTokenEnv:       m.LocalCfgMCPTokenEnv.Value(),
		syncEnabled:       m.LocalCfgSyncEnabled,
		syncURL:           m.LocalCfgSyncURL.Value(),
		syncTokenEnv:      m.LocalCfgSyncTokenEnv.Value(),
		syncInterval:      m.LocalCfgSyncInterval.Value(),
		autoStart:         m.EmbCfgAutoStart,
		embeddingDims:     m.LocalCfgEmbeddingDimensions.Value(),
		applyDefaults:     !m.LocalCfgDirty,
	}
}

type unifiedConfigSavedMsg struct{ result SaveResult }

func saveUnifiedConfig(d *Deps, in unifiedConfigInput) tea.Cmd {
	return func() tea.Msg {
		return unifiedConfigSavedMsg{result: unifiedConfigSave(d, in)}
	}
}

// unifiedConfigSave writes the unified form, hot-reloads everything reloadable,
// and reports exactly what happened (REQ-CFG-003). An empty token field carries
// the loaded credential forward instead of erasing it (REQ-CFG-004).
func unifiedConfigSave(d *Deps, in unifiedConfigInput) SaveResult {
	if d == nil || d.Config == nil {
		return SaveResult{Err: fmt.Errorf("config not available")}
	}
	prev := *d.Config
	next := prev // carries http.token and every value the form did not edit

	next.AI.Provider = aiProviderConfigValue(in.aiProvider)
	if in.applyDefaults && next.AI.Provider == "" {
		next.AI.Provider = defaultAIProvider
	}
	if v := strings.TrimSpace(in.aiModel); v != "" {
		next.AI.Model = v
	}
	if v := strings.TrimSpace(in.aiBaseURL); v != "" {
		next.AI.BaseURL = v
	}

	next.Search.EmbeddingProvider = embeddingProviderConfigValue(in.embeddingProvider)
	if in.applyDefaults && next.Search.EmbeddingProvider == "" {
		next.Search.EmbeddingProvider = defaultAIProvider
	}
	if v := strings.TrimSpace(in.embeddingModel); v != "" {
		next.Search.EmbeddingModel = v
	} else if in.applyDefaults && next.Search.EmbeddingModel == "" {
		next.Search.EmbeddingModel = defaultEmbeddingModel
	}

	port, err := strconv.Atoi(strings.TrimSpace(in.httpPort))
	if err != nil {
		return SaveResult{Err: fmt.Errorf("HTTP port must be a number between 1 and 65535")}
	}
	interval, err := time.ParseDuration(strings.TrimSpace(in.syncInterval))
	if err != nil {
		return SaveResult{Err: fmt.Errorf("sync interval must be a duration such as 30s or 5m")}
	}
	// An empty Vector dim field preserves the loaded value; a supplied value is
	// validated against search.embedding_dimensions by config.Validate below.
	dims := next.Search.EmbeddingDimensions
	if raw := strings.TrimSpace(in.embeddingDims); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return SaveResult{Err: fmt.Errorf("embedding dimensions must be a whole number between 8 and 4096, or 0 for the provider default")}
		}
		dims = v
	}

	next.Database.Path = strings.TrimSpace(in.databasePath)
	next.HTTP.Enabled, next.HTTP.Host, next.HTTP.Port = in.httpEnabled, strings.TrimSpace(in.httpHost), port
	if token := strings.TrimSpace(in.httpToken); token != "" {
		next.HTTP.Token = token
	}
	if strings.TrimSpace(in.mcpProfile) != "" {
		next.MCP.Profile = strings.ToLower(strings.TrimSpace(in.mcpProfile))
	}
	next.MCP.Remote.Enabled, next.MCP.Remote.URL, next.MCP.Remote.TokenEnv = in.mcpRemote, strings.TrimSpace(in.mcpURL), strings.TrimSpace(in.mcpTokenEnv)
	next.Sync.Enabled, next.Sync.URL, next.Sync.TokenEnv, next.Sync.Interval = in.syncEnabled, strings.TrimSpace(in.syncURL), strings.TrimSpace(in.syncTokenEnv), interval
	next.Search.OllamaAutoStart = in.autoStart
	next.Search.EmbeddingDimensions = dims

	targetPath, formatChanged := unifiedConfigTargetPath(prev.LoadedFrom, in.format)

	if err := config.Validate(&next); err != nil {
		return SaveResult{Err: err}
	}
	if err := config.Save(&next, targetPath); err != nil {
		return SaveResult{Err: err}
	}
	next.LoadedFrom = targetPath
	*d.Config = next
	d.Config = &next

	if d.App != nil {
		if err := d.App.ReloadConfig(); err != nil {
			return SaveResult{Err: fmt.Errorf("config saved but reload failed: %w", err)}
		}
		d.Config = d.App.Config
	}
	return SaveResult{RestartFields: restartFieldsFor(prev, next, formatChanged)}
}

// unifiedConfigTargetPath resolves the save destination from the selected
// format and reports whether that is a genuine format change for this file.
func unifiedConfigTargetPath(loadedFrom string, format int) (string, bool) {
	exts := []string{"yaml", "json", "toml"}
	if format < 0 || format >= len(exts) {
		return loadedFrom, false
	}
	ext := exts[format]
	if loadedFrom == "" {
		return filepath.Join(config.CortexDir(), "cortex."+ext), format != 0
	}
	target := filepath.Join(filepath.Dir(loadedFrom), "cortex."+ext)
	return target, !strings.EqualFold(filepath.Ext(loadedFrom), "."+ext)
}

// restartFieldsFor names exactly the changed fields that need a process restart
// (REQ-CFG-003): the database path and a config-format change. Every other
// change is hot-reloadable.
func restartFieldsFor(prev, next config.Config, formatChanged bool) []string {
	var fields []string
	if strings.TrimSpace(prev.Database.Path) != strings.TrimSpace(next.Database.Path) {
		fields = append(fields, "database path")
	}
	if formatChanged {
		fields = append(fields, "config format")
	}
	return fields
}

// unifiedConfigFormatChanged reports whether the staged format differs from the
// file currently on disk, gating the informational format note (REQ-CFG-006).
func unifiedConfigFormatChanged(m Model) bool {
	loaded := ""
	if m.deps != nil && m.deps.Config != nil {
		loaded = m.deps.Config.LoadedFrom
	}
	staged := 0
	switch m.LocalCfgFormat {
	case 1:
		staged = 1
	case 2:
		staged = 2
	}
	switch strings.ToLower(filepath.Ext(loaded)) {
	case ".json", ".jsonc":
		return staged != 1
	case ".toml":
		return staged != 2
	default:
		return staged != 0
	}
}

// ─── Unified Configuration input routing & keymap ───────────────────────────

// unifiedConfigTextInput resolves the text input backing a unified Configuration
// field key.
func (m Model) unifiedConfigTextInput(key string) (textinput.Model, bool) {
	switch key {
	case "ai_model":
		return m.LocalCfgLLMModel, true
	case "ai_base_url":
		return m.LocalCfgLLMBaseURL, true
	case "embedding_model":
		return m.EmbCfgModel, true
	case "database_path":
		return m.LocalCfgDatabasePath, true
	case "http_host":
		return m.LocalCfgHTTPHost, true
	case "http_port":
		return m.LocalCfgHTTPPort, true
	case "http_token":
		return m.AuthTokenInput, true
	case "mcp_url":
		return m.LocalCfgMCPURL, true
	case "mcp_token_env":
		return m.LocalCfgMCPTokenEnv, true
	case "sync_url":
		return m.LocalCfgSyncURL, true
	case "sync_token_env":
		return m.LocalCfgSyncTokenEnv, true
	case "sync_interval":
		return m.LocalCfgSyncInterval, true
	case "embedding_dimensions":
		return m.LocalCfgEmbeddingDimensions, true
	}
	return textinput.Model{}, false
}

// unifiedConfigInputFocused reports whether the focused Configuration field is a
// text field actively owning the keyboard.
func (m Model) unifiedConfigInputFocused() bool {
	field, ok := unifiedConfigField(m.LocalCfgFocusField)
	if !ok || field.Kind != configFieldText {
		return false
	}
	input, ok := m.unifiedConfigTextInput(field.Key)
	return ok && input.Focused()
}

// handleUnifiedConfigInput routes keystrokes to the focused Configuration text
// field. Enter/esc/tab leave edit mode; every other key edits the value.
func (m Model) handleUnifiedConfigInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	field, ok := unifiedConfigField(m.LocalCfgFocusField)
	if !ok {
		return m, nil
	}
	switch msg.String() {
	case "enter", "esc", "tab", "shift+tab":
		m.blurUnifiedConfigInput()
		return m, nil
	}
	m.LocalCfgDirty = true
	var cmd tea.Cmd
	switch field.Key {
	case "ai_model":
		m.LocalCfgLLMModel, cmd = m.LocalCfgLLMModel.Update(msg)
	case "ai_base_url":
		m.LocalCfgLLMBaseURL, cmd = m.LocalCfgLLMBaseURL.Update(msg)
	case "embedding_model":
		m.EmbCfgModel, cmd = m.EmbCfgModel.Update(msg)
	case "database_path":
		m.LocalCfgDatabasePath, cmd = m.LocalCfgDatabasePath.Update(msg)
	case "http_host":
		m.LocalCfgHTTPHost, cmd = m.LocalCfgHTTPHost.Update(msg)
	case "http_port":
		m.LocalCfgHTTPPort, cmd = m.LocalCfgHTTPPort.Update(msg)
	case "http_token":
		m.AuthTokenInput, cmd = m.AuthTokenInput.Update(msg)
	case "mcp_url":
		m.LocalCfgMCPURL, cmd = m.LocalCfgMCPURL.Update(msg)
	case "mcp_token_env":
		m.LocalCfgMCPTokenEnv, cmd = m.LocalCfgMCPTokenEnv.Update(msg)
	case "sync_url":
		m.LocalCfgSyncURL, cmd = m.LocalCfgSyncURL.Update(msg)
	case "sync_token_env":
		m.LocalCfgSyncTokenEnv, cmd = m.LocalCfgSyncTokenEnv.Update(msg)
	case "sync_interval":
		m.LocalCfgSyncInterval, cmd = m.LocalCfgSyncInterval.Update(msg)
	case "embedding_dimensions":
		m.LocalCfgEmbeddingDimensions, cmd = m.LocalCfgEmbeddingDimensions.Update(msg)
	}
	return m, cmd
}

// blurUnifiedConfigInput releases every Configuration text field from focus so
// the registry resumes normal key dispatch.
func (m *Model) blurUnifiedConfigInput() {
	m.LocalCfgLLMModel.Blur()
	m.LocalCfgLLMBaseURL.Blur()
	m.EmbCfgModel.Blur()
	m.LocalCfgDatabasePath.Blur()
	m.LocalCfgHTTPHost.Blur()
	m.LocalCfgHTTPPort.Blur()
	m.AuthTokenInput.Blur()
	m.LocalCfgMCPURL.Blur()
	m.LocalCfgMCPTokenEnv.Blur()
	m.LocalCfgSyncURL.Blur()
	m.LocalCfgSyncTokenEnv.Blur()
	m.LocalCfgSyncInterval.Blur()
	m.LocalCfgEmbeddingDimensions.Blur()
}

// unifiedConfigKeymap declares the unified Configuration bindings so Help and
// footers are generated from the same registry that dispatches the keys
// (REQ-NAV-004). It is registered for both the Local and Embedding screen ids so
// neither is a palette-only surface.
func unifiedConfigKeymap(screen Screen) Keymap {
	handler := Model.handleUnifiedConfigKeys
	bind := func(key, description string) Binding {
		return Binding{Key: key, Description: description, Handler: handler}
	}
	return Keymap{Screen: screen, Bindings: []Binding{
		bind("tab", "Next tab"),
		bind("shift+tab", "Previous tab"),
		bind("up", "Move up"),
		bind("k", "Move up"),
		bind("down", "Move down"),
		bind("j", "Move down"),
		bind("left", "Cycle left"),
		bind("h", "Cycle left"),
		bind("right", "Cycle right"),
		bind("l", "Cycle right"),
		bind(" ", "Edit / activate"),
		bind("enter", "Edit / activate"),
		bind("s", "Save & apply"),
		bind("S", "Save & apply"),
		bind("r", "Reload from disk"),
		bind("R", "Reload from disk"),
		bind("c", "Connect to server"),
		bind("C", "Connect to server"),
		bind("esc", "Back"),
		bind("q", "Back"),
	}}
}

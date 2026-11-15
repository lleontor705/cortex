package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// Canonical global keys (REQ-NAV-002). The registry evaluates them only after
// no screen binding matches, so a declared screen binding always wins.
const (
	GlobalPalette   = "ctrl+k"
	GlobalHelp      = "?"
	GlobalBack      = "esc"
	GlobalDashboard = "q"
)

// GlobalKeys is the exhaustive global key set; no other key may be global.
var GlobalKeys = []string{GlobalPalette, GlobalHelp, GlobalBack, GlobalDashboard}

var globalKeyDescriptions = map[string]string{
	GlobalPalette:   "Command palette",
	GlobalHelp:      "Help",
	GlobalBack:      "Back (pop)",
	GlobalDashboard: "Dashboard",
}

// inputModeEscapeKeys are the only keys that leave a focused text input; every
// other key is routed to the input instead of the registry.
var inputModeEscapeKeys = map[string]bool{"esc": true, "tab": true, "enter": true}

// Binding is one declarative key binding for a screen.
type Binding struct {
	Key         string // e.g. "enter", "T", "ctrl+s"
	Description string // human-readable, shown in footer + help
	When        func(m *Model) bool
	Handler     func(m Model, key string) (tea.Model, tea.Cmd)
}

// Keymap is the declarative binding set for one screen. Bindings are ordered;
// the first visible match wins.
type Keymap struct {
	Screen   Screen
	Bindings []Binding
}

// HelpEntry is one generated footer/help row.
type HelpEntry struct {
	Key         string
	Description string
	Screen      Screen
	Global      bool
}

// GlobalHandlers supplies the behaviors behind the canonical global keys; the
// registry owns only their declared keys, order, and descriptions.
type GlobalHandlers struct {
	Palette   func(m Model, key string) (tea.Model, tea.Cmd)
	Help      func(m Model, key string) (tea.Model, tea.Cmd)
	Back      func(m Model, key string) (tea.Model, tea.Cmd)
	Dashboard func(m Model, key string) (tea.Model, tea.Cmd)
}

// CanonicalGlobalBindings returns the REQ-NAV-002 global bindings in evaluation
// order, omitting any key whose handler is nil so help never shows a dead key.
func CanonicalGlobalBindings(h GlobalHandlers) []Binding {
	candidates := []Binding{
		{Key: GlobalPalette, Description: globalKeyDescriptions[GlobalPalette], Handler: h.Palette},
		{Key: GlobalHelp, Description: globalKeyDescriptions[GlobalHelp], Handler: h.Help},
		{Key: GlobalBack, Description: globalKeyDescriptions[GlobalBack], Handler: h.Back},
		{Key: GlobalDashboard, Description: globalKeyDescriptions[GlobalDashboard], Handler: h.Dashboard},
	}
	out := make([]Binding, 0, len(candidates))
	for _, b := range candidates {
		if b.Handler != nil {
			out = append(out, b)
		}
	}
	return out
}

// KeymapRegistry is the single key-dispatch path. Screen keymaps are consulted
// before the global bindings, so global keys cannot shadow screen bindings.
type KeymapRegistry struct {
	keymaps   map[Screen]Keymap
	order     []Screen
	globals   []Binding
	inputMode func(m *Model) bool
}

// NewKeymapRegistry returns an empty registry with TextInputFocused as its
// InputMode predicate.
func NewKeymapRegistry() *KeymapRegistry {
	return &KeymapRegistry{
		keymaps:   make(map[Screen]Keymap),
		inputMode: TextInputFocused,
	}
}

// Register installs or replaces the keymap for its screen.
func (r *KeymapRegistry) Register(km Keymap) *KeymapRegistry {
	if r.keymaps == nil {
		r.keymaps = make(map[Screen]Keymap)
	}
	if _, exists := r.keymaps[km.Screen]; !exists {
		r.order = append(r.order, km.Screen)
	}
	r.keymaps[km.Screen] = km
	return r
}

// RegisterGlobal appends a global binding; globals are checked in registration
// order after every screen binding.
func (r *KeymapRegistry) RegisterGlobal(b Binding) *KeymapRegistry {
	r.globals = append(r.globals, b)
	return r
}

// RegisterGlobals appends several global bindings in order.
func (r *KeymapRegistry) RegisterGlobals(bindings ...Binding) *KeymapRegistry {
	for _, b := range bindings {
		r.RegisterGlobal(b)
	}
	return r
}

// SetInputMode overrides the InputMode predicate; a nil predicate disables gating.
func (r *KeymapRegistry) SetInputMode(pred func(m *Model) bool) *KeymapRegistry {
	r.inputMode = pred
	return r
}

// Dispatch resolves key for the model's current screen. It returns handled=false
// when nothing claims the key, so the caller can forward it to a focused input.
func (r *KeymapRegistry) Dispatch(m Model, key string) (tea.Model, tea.Cmd, bool) {
	if r.isInputMode(&m) && !inputModeEscapeKeys[key] {
		return m, nil, false
	}
	if km, ok := r.keymaps[m.Screen]; ok {
		if b, ok := matchBinding(km.Bindings, &m, key); ok {
			model, cmd := b.Handler(m, key)
			return model, cmd, true
		}
	}
	if b, ok := matchBinding(r.globals, &m, key); ok {
		model, cmd := b.Handler(m, key)
		return model, cmd, true
	}
	return m, nil, false
}

// FooterHints renders the visible "[key] description" hints for a screen,
// omitting bindings whose When predicate is false.
func (r *KeymapRegistry) FooterHints(s Screen, m *Model) []string {
	hints := make([]string, 0, len(r.globals))
	for _, b := range r.globals {
		if visible(b, m) {
			hints = append(hints, hint(b))
		}
	}
	if km, ok := r.keymaps[s]; ok {
		for _, b := range km.Bindings {
			if visible(b, m) {
				hints = append(hints, hint(b))
			}
		}
	}
	return hints
}

// HelpEntries lists declared globals followed by every screen binding in
// registration order. The optional model lets callers omit bindings whose When
// predicate is false; without a model only unconditional bindings are listed.
func (r *KeymapRegistry) HelpEntries(models ...*Model) []HelpEntry {
	var m *Model
	if len(models) > 0 {
		m = models[0]
	}
	entries := make([]HelpEntry, 0, len(r.globals))
	for _, b := range r.globals {
		if visible(b, m) {
			entries = append(entries, HelpEntry{Key: b.Key, Description: b.Description, Global: true})
		}
	}
	for _, s := range r.order {
		km := r.keymaps[s]
		for _, b := range km.Bindings {
			if visible(b, m) {
				entries = append(entries, HelpEntry{Key: b.Key, Description: b.Description, Screen: s})
			}
		}
	}
	return entries
}

// TextInputFocused is the default InputMode predicate: it reports whether a text
// input currently owns raw keystrokes for the active model state.
func TextInputFocused(m *Model) bool {
	if m == nil {
		return false
	}
	if m.CmdPaletteOpen || m.NewObsModalOpen {
		return true
	}
	return m.SearchInput.Focused() ||
		m.EmbCfgModel.Focused() ||
		m.LocalCfgDatabasePath.Focused() ||
		m.LocalCfgLLMModel.Focused() ||
		m.LocalCfgLLMBaseURL.Focused() ||
		m.LocalCfgHTTPHost.Focused() ||
		m.LocalCfgHTTPPort.Focused() ||
		m.LocalCfgMCPURL.Focused() ||
		m.LocalCfgMCPTokenEnv.Focused() ||
		m.LocalCfgSyncURL.Focused() ||
		m.LocalCfgSyncTokenEnv.Focused() ||
		m.LocalCfgSyncInterval.Focused() ||
		m.LocalCfgEmbeddingDimensions.Focused()
}

func (r *KeymapRegistry) isInputMode(m *Model) bool {
	return r.inputMode != nil && r.inputMode(m)
}

func matchBinding(bindings []Binding, m *Model, key string) (Binding, bool) {
	for _, b := range bindings {
		if b.Key != key || b.Handler == nil {
			continue
		}
		if b.When == nil || b.When(m) {
			return b, true
		}
	}
	return Binding{}, false
}

func visible(b Binding, m *Model) bool {
	if b.When == nil {
		return true
	}
	if m == nil {
		return false
	}
	return b.When(m)
}

func hint(b Binding) string {
	return fmt.Sprintf("[%s] %s", b.Key, b.Description)
}

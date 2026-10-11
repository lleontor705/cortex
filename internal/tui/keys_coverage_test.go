package tui

import (
	"testing"

	"github.com/lleontor705/cortex/v2/internal/update"

	tea "github.com/charmbracelet/bubbletea"
)

// dispatchTrace records which binding the registry resolved a key to: a screen
// binding (by index within its keymap) or a canonical global.
type dispatchTrace struct {
	screen int
	global bool
}

// registrySnapshot deep-copies the live registry so a test can install probe
// handlers without mutating the global dispatch path.
func registrySnapshot() *KeymapRegistry {
	r := NewKeymapRegistry()
	r.SetInputMode(nil)
	r.globals = append([]Binding(nil), keymapRegistry.globals...)
	for _, s := range keymapRegistry.order {
		km := keymapRegistry.keymaps[s]
		r.Register(Keymap{Screen: km.Screen, Bindings: append([]Binding(nil), km.Bindings...)})
	}
	return r
}

// probedRegistry replaces every handler with a recorder, so a dispatch reports
// the exact binding that won instead of running it.
func probedRegistry(src *KeymapRegistry, tr *dispatchTrace) *KeymapRegistry {
	r := NewKeymapRegistry()
	r.SetInputMode(nil)
	for _, s := range src.order {
		km := src.keymaps[s]
		binds := make([]Binding, len(km.Bindings))
		for i := range km.Bindings {
			i := i
			binds[i] = km.Bindings[i]
			binds[i].Handler = func(m Model, _ string) (tea.Model, tea.Cmd) {
				tr.screen = i
				return m, nil
			}
		}
		r.Register(Keymap{Screen: s, Bindings: binds})
	}
	for gi := range src.globals {
		b := src.globals[gi]
		b.Handler = func(m Model, _ string) (tea.Model, tea.Cmd) {
			tr.screen, tr.global = -1, true
			return m, nil
		}
		r.RegisterGlobal(b)
	}
	return r
}

// bindingStates are the model states under which a guarded binding can win,
// ordered from the fully-off state used by unguarded bindings to the onboarding
// empty states and the unconfigured-sync prompt.
var bindingStates = []func(*Model){
	func(m *Model) { onboardingSyncPromptDismissed = true },
	func(m *Model) { m.ConfirmDelete = true; onboardingSyncPromptDismissed = true },
	func(m *Model) { m.UpdateResult = &update.Result{Latest: "v1"}; onboardingSyncPromptDismissed = true },
	func(m *Model) { m.Stats = &combinedStats{}; onboardingSyncPromptDismissed = false },
	func(m *Model) { m.Stats = &combinedStats{TotalSessions: 1}; onboardingSyncPromptDismissed = false },
	func(m *Model) { m.Stats = &combinedStats{TotalObservations: 1}; onboardingSyncPromptDismissed = false },
}

// firstVisible returns the index of the binding a dispatch on key would resolve
// to, mirroring matchBinding's key/When rules.
func firstVisible(km Keymap, m *Model, key string) int {
	for i := range km.Bindings {
		b := km.Bindings[i]
		if b.Key != key || b.Handler == nil {
			continue
		}
		if b.When == nil || b.When(m) {
			return i
		}
	}
	return -1
}

// bindingState finds a model state that makes binding target the winner for its
// own key, or nil when no state can (an unreachable binding).
func bindingState(km Keymap, target int) *Model {
	b := km.Bindings[target]
	if b.Handler == nil {
		return nil
	}
	for _, seed := range bindingStates {
		m := newTestModel(120, 40)
		m.Screen = km.Screen
		seed(&m)
		if b.When != nil && !b.When(&m) {
			continue
		}
		if firstVisible(km, &m, b.Key) == target {
			return &m
		}
	}
	return nil
}

// TestRegistryCoverageNoShadowing enumerates every declared (screen, binding)
// pair and proves each one, in a state where it is visible, resolves to its own
// handler rather than to a later binding or a canonical global. It is the
// regression oracle for the original global-first defect (update.go), where a
// global key could preempt a screen binding.
func TestRegistryCoverageNoShadowing(t *testing.T) {
	t.Cleanup(func() { onboardingSyncPromptDismissed = false })
	src := registrySnapshot()
	for _, screen := range src.order {
		km := src.keymaps[screen]
		for i := range km.Bindings {
			b := km.Bindings[i]
			state := bindingState(km, i)
			if state == nil {
				t.Fatalf("screen %v binding %d (%q) is unreachable: no state selects it", screen, i, b.Key)
			}
			tr := &dispatchTrace{}
			_, _, handled := probedRegistry(src, tr).Dispatch(*state, b.Key)
			if !handled || tr.global || tr.screen != i {
				t.Fatalf("screen %v binding %d (%q): handled=%v global=%v resolved=%d, want the screen binding",
					screen, i, b.Key, handled, tr.global, tr.screen)
			}
		}
	}
}

// TestGlobalsNeverPreemptScreenBindings proves the REQ-NAV-002 ordering on every
// screen: a declared screen binding always wins over the canonical global key,
// and a global fires only where the screen declares nothing for that key.
func TestGlobalsNeverPreemptScreenBindings(t *testing.T) {
	t.Cleanup(func() { onboardingSyncPromptDismissed = false })
	src := registrySnapshot()
	for _, screen := range src.order {
		km := src.keymaps[screen]
		for _, gk := range GlobalKeys {
			var state *Model
			want := -1
			for _, seed := range bindingStates {
				m := newTestModel(120, 40)
				m.Screen = screen
				seed(&m)
				if idx := firstVisible(km, &m, gk); idx >= 0 {
					state, want = &m, idx
					break
				}
			}
			probe := newTestModel(120, 40)
			probe.Screen = screen
			if state != nil {
				probe = *state
			}
			tr := &dispatchTrace{}
			_, _, handled := probedRegistry(src, tr).Dispatch(probe, gk)
			if !handled {
				t.Fatalf("screen %v key %q was not handled", screen, gk)
			}
			if want >= 0 && (tr.global || tr.screen != want) {
				t.Fatalf("screen %v key %q: global preempted screen binding %d", screen, gk, want)
			}
			if want < 0 && !tr.global {
				t.Fatalf("screen %v key %q: expected the global fallback", screen, gk)
			}
		}
	}
}

func isModel(v tea.Model) bool {
	_, ok := v.(Model)
	return ok
}

// TestEmbeddingConfigKeyBranches covers the embedding handler's state gates and
// model input, which the unified Configuration registry keymap does not express.
func TestEmbeddingConfigKeyBranches(t *testing.T) {
	states := []func(*Model){
		func(m *Model) {}, func(m *Model) { m.EmbCfgFocusField = 1 }, func(m *Model) { m.EmbCfgFocusField = 4 },
		func(m *Model) { m.EmbCfgPulling = true }, func(m *Model) { m.EmbCfgStarting = true },
		func(m *Model) { m.EmbCfgSaving = true }, func(m *Model) { m.EmbCfgReindexing = true },
		func(m *Model) { m.EmbCfgSaved, m.EmbCfgReindexWarning = true, true },
		func(m *Model) { m.EmbCfgSaved, m.EmbCfgProvider, m.EmbCfgOllamaChecked = true, 1, true },
		func(m *Model) {
			m.EmbCfgSaved, m.EmbCfgProvider, m.EmbCfgOllamaChecked, m.EmbCfgOllamaRunning = true, 1, true, true
		},
		func(m *Model) { m.EmbCfgSaved = true },
	}
	for si, set := range states {
		for _, key := range []string{"s", "p", "x", "r", "up", "down", "left", "right", "h", "l", " ", "enter", "esc", "q", "z"} {
			m := newTestModel(120, 40)
			set(&m)
			if out, _ := m.handleEmbeddingConfigKeys(key); !isModel(out) {
				t.Fatalf("state %d key %q: %T", si, key, out)
			}
		}
	}
	for _, msg := range []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyEsc}, {Type: tea.KeyRunes, Runes: []rune("m")}} {
		m := newTestModel(120, 40)
		if out, _ := m.handleEmbeddingModelInput(msg); !isModel(out) {
			t.Fatalf("embedding model input %q: %T", msg.String(), out)
		}
	}
}

// TestModalKeyFlows covers the modal input handlers. They are reached through
// the TextInputFocused input mode rather than the registry, so the declarative
// coverage oracle cannot express them.
func TestModalKeyFlows(t *testing.T) {
	keyMsgs := []tea.KeyMsg{
		{Type: tea.KeyEsc}, {Type: tea.KeyTab}, {Type: tea.KeyShiftTab}, {Type: tea.KeyEnter}, {Type: tea.KeySpace}, {Type: tea.KeyLeft},
		{Type: tea.KeyRight}, {Type: tea.KeyCtrlS}, {Type: tea.KeyCtrlP}, {Type: tea.KeyCtrlN}, {Type: tea.KeyUp}, {Type: tea.KeyDown},
		{Type: tea.KeyRunes, Runes: []rune("u")},
	}
	for field := 0; field <= 4; field++ {
		for _, msg := range keyMsgs {
			m := newTestModel(120, 40)
			m.NewObsModalOpen, m.NewObsFocusField = true, field
			if out, _ := m.handleNewObsModalKeys(msg); !isModel(out) {
				t.Fatalf("newobs field %d msg %q: %T", field, msg.String(), out)
			}
		}
	}
	for _, msg := range keyMsgs {
		m := newTestModel(120, 40)
		m.CmdPaletteOpen = true
		if out, _ := m.handleCmdPaletteKeys(msg); !isModel(out) {
			t.Fatalf("palette msg %q: %T", msg.String(), out)
		}
	}
}

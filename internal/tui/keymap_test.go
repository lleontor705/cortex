package tui

import (
	"reflect"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func marker(tag string) func(m Model, key string) (tea.Model, tea.Cmd) {
	return func(m Model, key string) (tea.Model, tea.Cmd) {
		m.ErrorMsg = tag + ":" + key
		return m, nil
	}
}

func asModel(t *testing.T, im tea.Model) Model {
	t.Helper()
	m, ok := im.(Model)
	if !ok {
		t.Fatalf("dispatch returned %T, want Model", im)
	}
	return m
}

func alwaysInput(*Model) bool { return true }

func TestKeymapRegistry(t *testing.T) {
	b := func(key, tag string) Binding { return Binding{Key: key, Handler: marker(tag)} }
	hidden := func(key string) Binding {
		return Binding{Key: key, Handler: marker("hidden"), When: func(*Model) bool { return false }}
	}

	t.Run("dispatch", func(t *testing.T) {
		tests := []struct {
			name        string
			keymaps     []Keymap
			globals     []Binding
			inputMode   func(m *Model) bool
			inputSet    bool
			screen      Screen
			key         string
			wantHandled bool
			wantTag     string
		}{
			{"screen binding handled", []Keymap{{ScreenRecent, []Binding{b("enter", "screen")}}}, nil, nil, false, ScreenRecent, "enter", true, "screen:enter"},
			{"screen beats global", []Keymap{{ScreenRecent, []Binding{b("q", "screen")}}}, []Binding{b("q", "global")}, nil, false, ScreenRecent, "q", true, "screen:q"},
			{"global when no screen match", []Keymap{{ScreenRecent, []Binding{b("enter", "screen")}}}, []Binding{b("?", "global")}, nil, false, ScreenRecent, "?", true, "global:?"},
			{"global on unknown screen", nil, []Binding{b("esc", "global")}, nil, false, ScreenTimeline, "esc", true, "global:esc"},
			{"empty bindings fall through", []Keymap{{ScreenRecent, nil}}, []Binding{b("q", "global")}, nil, false, ScreenRecent, "q", true, "global:q"},
			{"false When falls through", []Keymap{{ScreenRecent, []Binding{hidden("x"), b("x", "fallback")}}}, nil, nil, false, ScreenRecent, "x", true, "fallback:x"},
			{"nil handler not dispatched", []Keymap{{ScreenRecent, []Binding{{Key: "x"}}}}, nil, nil, false, ScreenRecent, "x", false, ""},
			{"first match wins", []Keymap{{ScreenRecent, []Binding{b("x", "first"), b("x", "second")}}}, nil, nil, false, ScreenRecent, "x", true, "first:x"},
			{"input mode blocks printable", []Keymap{{ScreenSearch, []Binding{b("f", "screen")}}}, nil, alwaysInput, true, ScreenSearch, "f", false, ""},
			{"input mode allows esc", []Keymap{{ScreenSearch, []Binding{b("esc", "screen")}}}, nil, alwaysInput, true, ScreenSearch, "esc", true, "screen:esc"},
			{"input mode allows tab", []Keymap{{ScreenSearch, []Binding{b("tab", "screen")}}}, nil, alwaysInput, true, ScreenSearch, "tab", true, "screen:tab"},
			{"input mode allows enter", []Keymap{{ScreenSearch, []Binding{b("enter", "screen")}}}, nil, alwaysInput, true, ScreenSearch, "enter", true, "screen:enter"},
			{"input mode blocks global", nil, []Binding{b("ctrl+k", "global")}, alwaysInput, true, ScreenSearch, "ctrl+k", false, ""},
			{"nil predicate disables gating", []Keymap{{ScreenSearch, []Binding{b("f", "screen")}}}, nil, nil, true, ScreenSearch, "f", true, "screen:f"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				reg := NewKeymapRegistry()
				for _, km := range tt.keymaps {
					reg.Register(km)
				}
				reg.RegisterGlobals(tt.globals...)
				if tt.inputSet {
					reg.SetInputMode(tt.inputMode)
				}
				got, _, handled := reg.Dispatch(Model{Screen: tt.screen}, tt.key)
				if handled != tt.wantHandled {
					t.Fatalf("handled = %v, want %v", handled, tt.wantHandled)
				}
				if tag := asModel(t, got).ErrorMsg; tag != tt.wantTag {
					t.Fatalf("marker = %q, want %q", tag, tt.wantTag)
				}
			})
		}
	})

	t.Run("footer and help", func(t *testing.T) {
		reg := NewKeymapRegistry()
		reg.RegisterGlobal(Binding{Key: "?", Description: "Help", Handler: marker("g")})
		reg.Register(Keymap{Screen: ScreenRecent, Bindings: []Binding{
			{Key: "enter", Description: "Open detail", Handler: marker("s1")},
			{Key: "x", Description: "Hidden", Handler: marker("s2"), When: func(*Model) bool { return false }},
		}})
		m := Model{Screen: ScreenRecent}

		wantFooter := []string{"[?] Help", "[enter] Open detail"}
		if got := reg.FooterHints(ScreenRecent, &m); !reflect.DeepEqual(got, wantFooter) {
			t.Fatalf("FooterHints = %v, want %v", got, wantFooter)
		}
		if got := reg.FooterHints(ScreenTimeline, &m); !reflect.DeepEqual(got, []string{"[?] Help"}) {
			t.Fatalf("FooterHints(unknown) = %v, want global only", got)
		}

		wantHelp := []HelpEntry{
			{Key: "?", Description: "Help", Global: true},
			{Key: "enter", Description: "Open detail", Screen: ScreenRecent},
		}
		if got := reg.HelpEntries(&m); !reflect.DeepEqual(got, wantHelp) {
			t.Fatalf("HelpEntries = %#v, want %#v", got, wantHelp)
		}
		if got := reg.FooterHints(ScreenRecent, nil); !reflect.DeepEqual(got, wantFooter) {
			t.Fatalf("FooterHints(nil model) = %v, want %v", got, wantFooter)
		}
	})

	t.Run("canonical globals", func(t *testing.T) {
		want := []string{"ctrl+k", "?", "esc", "q"}
		if !reflect.DeepEqual(GlobalKeys, want) {
			t.Fatalf("GlobalKeys = %v, want %v", GlobalKeys, want)
		}
		h := marker("g")
		all := CanonicalGlobalBindings(GlobalHandlers{Palette: h, Help: h, Back: h, Dashboard: h})
		got := make([]string, len(all))
		for i, binding := range all {
			got[i] = binding.Key
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("CanonicalGlobalBindings keys = %v, want %v", got, want)
		}
		partial := CanonicalGlobalBindings(GlobalHandlers{Palette: h})
		if len(partial) != 1 || partial[0].Key != "ctrl+k" {
			t.Fatalf("CanonicalGlobalBindings(partial) = %v, want only ctrl+k", partial)
		}
	})

	t.Run("text input focused", func(t *testing.T) {
		if TextInputFocused(nil) {
			t.Fatal("nil model must not be input mode")
		}
		m := Model{}
		if TextInputFocused(&m) {
			t.Fatal("idle model must not be input mode")
		}
		m.SearchInput = textinput.New()
		m.SearchInput.Focus()
		if !TextInputFocused(&m) {
			t.Fatal("focused search input must be input mode")
		}
		if !TextInputFocused(&Model{CmdPaletteOpen: true}) {
			t.Fatal("open palette must be input mode")
		}
	})
}

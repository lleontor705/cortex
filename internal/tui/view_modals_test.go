package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// registryHelpKeysFor is the ground truth the Help screen and footers must
// render for a screen: the visible globals followed by the visible screen
// bindings, read straight from the registry that dispatches keys.
func registryHelpKeysFor(m *Model, screen Screen) []string {
	keys := make([]string, 0, len(keymapRegistry.globals))
	for _, b := range keymapRegistry.globals {
		if testBindingVisible(b, m) {
			keys = append(keys, b.Key)
		}
	}
	if km, ok := keymapRegistry.keymaps[screen]; ok {
		for _, b := range km.Bindings {
			if testBindingVisible(b, m) {
				keys = append(keys, b.Key)
			}
		}
	}
	return keys
}

func testBindingVisible(b Binding, m *Model) bool {
	return b.When == nil || b.When(m)
}

func bindingHasHandler(entry HelpEntry) bool {
	if entry.Global {
		for _, b := range keymapRegistry.globals {
			if b.Key == entry.Key {
				return b.Handler != nil
			}
		}
		return false
	}
	km, ok := keymapRegistry.keymaps[entry.Screen]
	if !ok {
		return false
	}
	for _, b := range km.Bindings {
		if b.Key == entry.Key {
			return b.Handler != nil
		}
	}
	return false
}

func helpEntryKeys(entries []HelpEntry) []string {
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		keys = append(keys, e.Key)
	}
	return keys
}

func assertMissingKey(keys []string, key string) error {
	for _, k := range keys {
		if k == key {
			return fmt.Errorf("unexpected key %q in %v", key, keys)
		}
	}
	return nil
}

func assertPresentKey(keys []string, key string) error {
	for _, k := range keys {
		if k == key {
			return nil
		}
	}
	return fmt.Errorf("key %q missing from %v", key, keys)
}

// TestHelpFromRegistry verifies REQ-NAV-004: Help and footers are generated from
// the same registry that dispatches keys, so a listed key can never be dead and
// the ctrl+k palette is documented and reachable everywhere non-input.
func TestHelpFromRegistry(t *testing.T) {
	screens := []Screen{
		ScreenDashboard, ScreenSearch, ScreenSearchResults, ScreenRecent,
		ScreenObservationDetail, ScreenTimeline, ScreenSessions, ScreenSessionDetail,
		ScreenSetup, ScreenGraph, ScreenArchive, ScreenHealth,
		ScreenEmbeddingConfig, ScreenLocalConfig, ScreenHelp,
	}

	t.Run("lists exactly globals plus the origin screen bindings", func(t *testing.T) {
		for _, origin := range screens {
			m := newTestModel(120, 40)
			seedNavFrom(&m, ScreenHelp, origin)
			got := helpEntryKeys(m.helpEntries())
			want := registryHelpKeysFor(&m, m.helpOriginScreen())
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("origin %v: help keys = %v, want %v", origin, got, want)
			}
		}
	})

	t.Run("global section is exactly the canonical global keys", func(t *testing.T) {
		m := newTestModel(120, 40)
		seedNavFrom(&m, ScreenHelp, ScreenRecent)
		globals, screenEntries := splitHelpEntries(m.helpEntries())
		if len(globals) != len(GlobalKeys) {
			t.Fatalf("global entries = %d, want %d", len(globals), len(GlobalKeys))
		}
		for _, entry := range globals {
			if !entry.Global {
				t.Fatalf("global entry %+v is not marked global", entry)
			}
			if err := assertPresentKey(GlobalKeys, entry.Key); err != nil {
				t.Fatal(err)
			}
		}
		for _, entry := range screenEntries {
			if entry.Global {
				t.Fatalf("screen entry %+v leaked into globals", entry)
			}
		}
	})

	t.Run("no listed key lacks a handler", func(t *testing.T) {
		for _, origin := range screens {
			m := newTestModel(120, 40)
			seedNavFrom(&m, ScreenHelp, origin)
			for _, entry := range m.helpEntries() {
				if !bindingHasHandler(entry) {
					t.Fatalf("origin %v: listed key %q has no handler", origin, entry.Key)
				}
			}
		}
	})

	t.Run("footers match registry bindings with When filtering", func(t *testing.T) {
		for _, screen := range screens {
			m := newTestModel(120, 40)
			m.Screen = screen
			got := declaredFooterKeys(t, keymapRegistry.FooterHints(screen, &m))
			want := registryHelpKeysFor(&m, screen)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("screen %v: footer keys = %v, want %v", screen, got, want)
			}
		}
	})

	t.Run("footer omits bindings whose When is false", func(t *testing.T) {
		plain := newTestModel(120, 40)
		plain.Screen = ScreenRecent
		if err := assertMissingKey(declaredFooterKeys(t, keymapRegistry.FooterHints(ScreenRecent, &plain)), "y"); err != nil {
			t.Fatal(err)
		}
		pending := newTestModel(120, 40)
		pending.Screen, pending.ConfirmDelete = ScreenRecent, true
		if err := assertPresentKey(declaredFooterKeys(t, keymapRegistry.FooterHints(ScreenRecent, &pending)), "y"); err != nil {
			t.Fatal(err)
		}
		help := newTestModel(120, 40)
		seedNavFrom(&help, ScreenHelp, ScreenRecent)
		if err := assertMissingKey(helpEntryKeys(help.helpEntries()), "y"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("palette is documented with its entries", func(t *testing.T) {
		m := newTestModel(120, 40)
		seedNavFrom(&m, ScreenHelp, ScreenRecent)
		out := m.viewHelp()
		for _, want := range []string{"Command palette (ctrl+k)", "Help / Keyboard shortcuts"} {
			if !strings.Contains(out, want) {
				t.Fatalf("help missing %q", want)
			}
		}
		for _, command := range allCommands() {
			if !strings.Contains(out, command.name) {
				t.Fatalf("help does not document palette entry %q", command.name)
			}
		}
	})

	t.Run("palette opens from every non-input screen", func(t *testing.T) {
		for _, screen := range screens {
			m := newTestModel(120, 40)
			m.Screen = screen
			if TextInputFocused(&m) {
				t.Fatalf("screen %v is unexpectedly in input mode", screen)
			}
			model, _, handled := keymapRegistry.Dispatch(m, GlobalPalette)
			if !handled {
				t.Fatalf("ctrl+k not handled on screen %v", screen)
			}
			if !model.(Model).CmdPaletteOpen {
				t.Fatalf("ctrl+k did not open the palette on screen %v", screen)
			}
		}
	})

	t.Run("t lowercase alias is documented", func(t *testing.T) {
		m := newTestModel(120, 40)
		seedNavFrom(&m, ScreenHelp, ScreenRecent)
		out := m.viewHelp()
		if !strings.Contains(out, "T/t") {
			t.Fatalf("help does not fold the T/t alias:\n%s", out)
		}
		if !strings.Contains(out, "Lowercase aliases") {
			t.Fatalf("help does not state the lowercase alias note:\n%s", out)
		}
	})
}

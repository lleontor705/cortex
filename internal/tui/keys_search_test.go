package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestSearchKeymap covers the Search/Results/Recent registry migration
// (REQ-NAV-001, REQ-NAV-003): the bindings live in the registry, every declared
// key dispatches, printable characters reach the focused input, T opens the
// timeline, esc pops / q resets, and empty results render guidance.
func TestSearchKeymap(t *testing.T) {
	t.Run("bindings are declared in the registry", func(t *testing.T) {
		for _, km := range searchKeymaps() {
			reg, ok := keymapRegistry.keymaps[km.Screen]
			if !ok {
				t.Fatalf("screen %v is not registered", km.Screen)
			}
			if len(reg.Bindings) != len(km.Bindings) {
				t.Fatalf("screen %v: registry has %d bindings, declared %d", km.Screen, len(reg.Bindings), len(km.Bindings))
			}
			for i := range km.Bindings {
				want, got := km.Bindings[i], reg.Bindings[i]
				if got.Key != want.Key || got.Description != want.Description {
					t.Fatalf("screen %v binding %d: registry %q/%q, declared %q/%q",
						km.Screen, i, got.Key, got.Description, want.Key, want.Description)
				}
			}
		}
	})

	t.Run("every declared binding dispatches through the registry", func(t *testing.T) {
		for _, km := range searchKeymaps() {
			for _, b := range km.Bindings {
				m := newTestModel(120, 40)
				seedSearchItems(&m)
				seedRecentItems(&m)
				m.Screen = km.Screen
				if b.When != nil && !b.When(&m) {
					continue
				}
				if _, _, handled := keymapRegistry.Dispatch(m, b.Key); !handled {
					t.Fatalf("screen %v key %q was not handled by the registry", km.Screen, b.Key)
				}
			}
		}
	})

	t.Run("confirm-delete bindings gate on pending state", func(t *testing.T) {
		for _, screen := range []Screen{ScreenSearchResults, ScreenRecent} {
			plain := newTestModel(120, 40)
			plain.Screen = screen
			if _, _, handled := keymapRegistry.Dispatch(plain, "y"); handled {
				t.Fatalf("screen %v: y dispatched without a pending confirmation", screen)
			}
			pending := newTestModel(120, 40)
			seedSearchItems(&pending)
			seedRecentItems(&pending)
			pending.Screen, pending.ConfirmDelete = screen, true
			if _, _, handled := keymapRegistry.Dispatch(pending, "y"); !handled {
				t.Fatalf("screen %v: y not dispatched while confirmation pending", screen)
			}
		}
	})

	t.Run("legacy handlers resolve through the same declarations", func(t *testing.T) {
		m := newTestModel(120, 40)
		seedNavFrom(&m, ScreenSearchResults, ScreenSearch)
		out, _ := m.handleSearchResultsKeys("esc")
		if got := out.(Model).Screen; got != ScreenSearch {
			t.Fatalf("handleSearchResultsKeys(esc) screen = %v, want %v", got, ScreenSearch)
		}
	})

	t.Run("printable characters reach the focused search input", func(t *testing.T) {
		m := newTestModel(120, 40)
		m.Screen = ScreenSearch
		m.SearchInput.Focus()
		for _, r := range []rune{'f', 'z', '1', '@'} {
			out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			m = out.(Model)
		}
		if got := m.SearchInput.Value(); got != "fz1@" {
			t.Fatalf("search input = %q, want %q", got, "fz1@")
		}
		if m.Screen != ScreenSearch {
			t.Fatalf("typing changed the screen to %v", m.Screen)
		}
	})

	t.Run("T opens timeline from Results and Recent", func(t *testing.T) {
		for _, screen := range []Screen{ScreenSearchResults, ScreenRecent} {
			m := newTestModel(120, 40)
			seedSearchItems(&m)
			seedRecentItems(&m)
			m.Screen = screen
			_, cmd := m.handleKeyPress("T")
			if cmd == nil {
				t.Fatalf("T on %v returned no command", screen)
			}
			if _, ok := cmd().(timelineMsg); !ok {
				t.Fatalf("T on %v produced %T, want timelineMsg", screen, cmd())
			}
		}
	})

	t.Run("esc pops and q resets to Dashboard", func(t *testing.T) {
		results := newTestModel(120, 40)
		seedSearchItems(&results)
		seedNavFrom(&results, ScreenSearchResults, ScreenSearch)
		if out, _ := results.handleKeyPress("esc"); out.(Model).Screen != ScreenSearch {
			t.Fatalf("esc on Results did not pop to Search")
		}
		recent := newTestModel(120, 40)
		seedRecentItems(&recent)
		recent.Screen = ScreenRecent
		if out, _ := recent.handleKeyPress("esc"); out.(Model).Screen != ScreenDashboard {
			t.Fatalf("esc on Recent did not return to Dashboard")
		}
		for _, screen := range []Screen{ScreenSearch, ScreenSearchResults, ScreenRecent} {
			m := newTestModel(120, 40)
			seedSearchItems(&m)
			seedRecentItems(&m)
			m.Screen = screen
			if _, cmd := m.handleKeyPress("q"); cmd == nil {
				t.Fatalf("q on %v returned no command", screen)
			}
			_ = screen
		}
		m := newTestModel(120, 40)
		m.Screen = ScreenSearchResults
		if out, _ := m.handleKeyPress("q"); out.(Model).Screen != ScreenDashboard {
			t.Fatalf("q did not reset to Dashboard")
		}
	})

	t.Run("p cycles the project filter on Results and Recent", func(t *testing.T) {
		for _, screen := range []Screen{ScreenSearchResults, ScreenRecent} {
			m := newTestModel(120, 40)
			seedSearchItems(&m)
			seedRecentItems(&m)
			m.Screen, m.SearchQuery, m.Stats = screen, "q", &combinedStats{Projects: []string{"a", "b"}}
			out, _ := m.handleKeyPress("p")
			if got := out.(Model).FilterProject; got != "a" {
				t.Fatalf("p on %v set filter %q, want %q", screen, got, "a")
			}
		}
	})

	t.Run("empty results render guidance", func(t *testing.T) {
		m := newTestModel(120, 40)
		m.SearchQuery = "nothing"
		out := m.viewSearchResults()
		for _, want := range []string{"No results found", "Try a different query"} {
			if !strings.Contains(out, want) {
				t.Fatalf("empty results missing %q in:\n%s", want, out)
			}
		}
	})
}

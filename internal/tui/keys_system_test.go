package tui

import (
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// declaredFooterKeys parses "[key] description" hints into their keys.
func declaredFooterKeys(t *testing.T, hints []string) []string {
	t.Helper()
	keys := make([]string, 0, len(hints))
	for _, h := range hints {
		end := strings.Index(h, "]")
		if !strings.HasPrefix(h, "[") || end < 0 {
			t.Fatalf("footer hint %q is not in \"[key] description\" form", h)
		}
		keys = append(keys, h[1:end])
	}
	return keys
}

func TestSystemKeymap(t *testing.T) {
	t.Run("health footer keys are all dispatchable", func(t *testing.T) {
		m := newTestModel(120, 40)
		m.Screen = ScreenHealth
		hints := keymapRegistry.FooterHints(ScreenHealth, &m)
		if len(hints) == 0 {
			t.Fatal("health footer is empty")
		}
		for _, key := range declaredFooterKeys(t, hints) {
			fresh := newTestModel(120, 40)
			fresh.Screen = ScreenHealth
			if _, _, handled := keymapRegistry.Dispatch(fresh, key); !handled {
				t.Fatalf("health footer advertises %q but nothing dispatches it", key)
			}
		}
	})

	t.Run("health footer is registry generated", func(t *testing.T) {
		m := newTestModel(120, 40)
		m.Screen = ScreenHealth
		out := m.viewHealth()
		for _, want := range []string{"enter Review stale", "o Review orphaned"} {
			if !strings.Contains(out, want) {
				t.Fatalf("health footer %q missing %q", out, want)
			}
		}
		if strings.Contains(out, "tab section") || strings.Contains(out, "enter expand") {
			t.Fatalf("health footer still advertises removed no-ops: %q", out)
		}
	})

	t.Run("health enter reviews stale", func(t *testing.T) {
		m := newTestModel(120, 40)
		m.Screen = ScreenHealth
		m.HealthStale = []*domain.Observation{newTestObs(7, "decision", "Stale one")}
		out, cmd := m.handleKeyPress("enter")
		if cmd == nil {
			t.Fatal("enter should dispatch a stale review command")
		}
		got := out.(Model)
		if !got.DetailLoading || got.Screen != ScreenHealth {
			t.Fatalf("enter review state = loading:%v screen:%v, want loading:true screen:%v", got.DetailLoading, got.Screen, ScreenHealth)
		}
	})

	t.Run("health o reviews orphaned", func(t *testing.T) {
		m := newTestModel(120, 40)
		m.Screen = ScreenHealth
		m.HealthOrphans = []*domain.Observation{newTestObs(3, "discovery", "Orphan one")}
		out, cmd := m.handleKeyPress("o")
		if cmd == nil {
			t.Fatal("o should dispatch an orphaned review command")
		}
		got := out.(Model)
		if !got.DetailLoading || got.Screen != ScreenHealth {
			t.Fatalf("o review state = loading:%v screen:%v, want loading:true screen:%v", got.DetailLoading, got.Screen, ScreenHealth)
		}
	})

	t.Run("health review targets its own list", func(t *testing.T) {
		stale := newTestModel(120, 40)
		stale.Screen = ScreenHealth
		stale.HealthOrphans = []*domain.Observation{newTestObs(3, "discovery", "Orphan one")}
		if out, cmd := stale.handleKeyPress("enter"); cmd != nil {
			t.Fatal("enter with no stale observations must not dispatch a review")
		} else if got := out.(Model); got.ToastMessage != "No stale observations to review" {
			t.Fatalf("enter empty toast = %q", got.ToastMessage)
		}

		orphan := newTestModel(120, 40)
		orphan.Screen = ScreenHealth
		orphan.HealthStale = []*domain.Observation{newTestObs(7, "decision", "Stale one")}
		if out, cmd := orphan.handleKeyPress("o"); cmd != nil {
			t.Fatal("o with no orphaned observations must not dispatch a review")
		} else if got := out.(Model); got.ToastMessage != "No orphaned observations to review" {
			t.Fatalf("o empty toast = %q", got.ToastMessage)
		}
	})

	t.Run("graph bindings are declared with enter re-rooting", func(t *testing.T) {
		m := newTestModel(120, 40)
		m.Screen = ScreenGraph
		seedGraphItems(&m)
		root := m.GraphObservations[0].ID
		m.GraphRootID = 999

		out, cmd := m.handleKeyPress("enter")
		if cmd == nil {
			t.Fatal("enter should dispatch a re-root command")
		}
		if got := out.(Model); got.GraphRootID != root {
			t.Fatalf("enter root = %d, want %d", got.GraphRootID, root)
		}

		again := newTestModel(120, 40)
		again.Screen = ScreenGraph
		seedGraphItems(&again)
		again.GraphRootID = 999
		out, cmd = again.handleKeyPress("r")
		if cmd == nil {
			t.Fatal("r should dispatch a re-root command")
		}
		if got := out.(Model); got.GraphRootID != root {
			t.Fatalf("r root = %d, want %d", got.GraphRootID, root)
		}
	})

	t.Run("graph footer is registry generated", func(t *testing.T) {
		m := newTestModel(120, 40)
		m.Screen = ScreenGraph
		seedGraphItems(&m)
		out := m.viewGraph()
		for _, want := range []string{"enter Focus as root", "r re-root"} {
			if !strings.Contains(out, want) {
				t.Fatalf("graph footer %q missing %q", out, want)
			}
		}
	})

	t.Run("esc pops and q resets on graph and health", func(t *testing.T) {
		for _, screen := range []Screen{ScreenGraph, ScreenHealth} {
			m := newTestModel(120, 40)
			seedNavFrom(&m, screen, ScreenRecent)
			out, _, handled := keymapRegistry.Dispatch(m, "esc")
			if !handled {
				t.Fatalf("esc not handled on %v", screen)
			}
			if got := out.(Model); got.Screen != ScreenRecent {
				t.Fatalf("esc on %v: screen = %v, want %v", screen, got.Screen, ScreenRecent)
			}

			m = newTestModel(120, 40)
			m.Screen = screen
			out, _, handled = keymapRegistry.Dispatch(m, "q")
			if !handled {
				t.Fatalf("q not handled on %v", screen)
			}
			if got := out.(Model); got.Screen != ScreenDashboard {
				t.Fatalf("q on %v: screen = %v, want %v", screen, got.Screen, ScreenDashboard)
			}
		}
	})

	t.Run("removed health no-ops are inert", func(t *testing.T) {
		m := newTestModel(120, 40)
		m.Screen = ScreenHealth
		for _, key := range []string{"tab", "j", "k", "up", "down"} {
			out, cmd := m.handleKeyPress(key)
			if cmd != nil || out.(Model).ToastMessage != "" {
				t.Fatalf("removed key %q still acts: cmd=%v", key, cmd)
			}
		}
	})
}

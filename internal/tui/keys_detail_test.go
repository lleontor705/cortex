package tui

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"

	"github.com/charmbracelet/bubbles/list"
)

// TestDetailTimelineKeymap is the REQ-NAV-001/REQ-NAV-003 oracle for the Detail,
// Timeline, and Archive migration: every required binding is declared in the
// live registry, T opens Timeline for the focused observation, Archive restore
// is a real persisted restore, and esc/q follow the navigation-stack semantics.
func TestDetailTimelineKeymap(t *testing.T) {
	t.Run("bindings declared in the registry", func(t *testing.T) {
		required := map[Screen][]string{
			ScreenObservationDetail: {"up", "k", "down", "j", "pgup", "pgdown", "t", "g", "s", "d", "esc", "q", "T"},
			ScreenTimeline:          {"up", "k", "down", "j", "enter", "esc", "q"},
			ScreenArchive:           {"enter", "v", "u", "d", "f", "esc", "q"},
		}
		for screen, keys := range required {
			km, ok := keymapRegistry.keymaps[screen]
			if !ok {
				t.Fatalf("no keymap registered for %v", screen)
			}
			declared := make(map[string]bool, len(km.Bindings))
			for _, b := range km.Bindings {
				if b.Handler == nil {
					t.Fatalf("%v binding %q has nil handler", screen, b.Key)
				}
				declared[b.Key] = true
			}
			for _, key := range keys {
				if !declared[key] {
					t.Fatalf("%v missing registry binding %q", screen, key)
				}
			}
		}
	})

	t.Run("T from detail opens timeline for the observation", func(t *testing.T) {
		deps := newTUIStoreDeps(t)
		seedSession(t, deps, "s1", "cortex")
		id := seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "Fix JWT", "body")

		m := New(deps)
		m.Screen = ScreenObservationDetail
		m.SelectedObservation = &domain.Observation{ID: id, SessionID: "s1", Title: "Fix JWT"}

		_, cmd := m.handleKeyPress("T")
		msg := assertMsgType[timelineMsg](t, cmd, "T on detail")
		if msg.err != nil {
			t.Fatalf("timeline load error: %v", msg.err)
		}
		if msg.focus == nil || msg.focus.ID != id {
			t.Fatalf("timeline focus = %+v, want id %d", msg.focus, id)
		}

		out, _ := m.Update(msg)
		got := out.(Model)
		if got.Screen != ScreenTimeline {
			t.Fatalf("screen = %v, want Timeline", got.Screen)
		}
		if got.TimelineFocus == nil || got.TimelineFocus.ID != id {
			t.Fatalf("TimelineFocus = %+v, want id %d", got.TimelineFocus, id)
		}
	})

	t.Run("enter on timeline opens the focus observation", func(t *testing.T) {
		m := newTestModel(120, 40)
		m.Screen = ScreenTimeline
		m.TimelineFocus = &domain.Observation{ID: 1042}

		out, cmd := m.handleKeyPress("enter")
		got := out.(Model)
		if cmd == nil {
			t.Fatal("enter on timeline returned no command")
		}
		if !got.DetailLoading {
			t.Fatal("enter on timeline did not start loading the focus observation")
		}
		if got.Screen != ScreenTimeline {
			t.Fatalf("enter pushed before the load resolved: screen = %v, want Timeline", got.Screen)
		}
	})

	t.Run("archive restore performs a real restore", func(t *testing.T) {
		deps := newTUIStoreDeps(t)
		seedSession(t, deps, "s1", "cortex")
		id := seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "Archived", "c1")
		ctx := context.Background()
		if err := deps.Observations.SoftDelete(ctx, id); err != nil {
			t.Fatalf("soft delete: %v", err)
		}

		m := New(deps)
		m.Screen = ScreenArchive
		m.ArchivedObservations = []*domain.Observation{{ID: id, Title: "Archived"}}
		m.ArchiveList.SetItems([]list.Item{observationItem{obs: m.ArchivedObservations[0]}})

		_, cmd := m.handleKeyPress("u")
		msg := assertMsgType[unarchiveObservationMsg](t, cmd, "u on archive")
		if msg.err != nil {
			t.Fatalf("restore error: %v", msg.err)
		}
		if msg.id != id {
			t.Fatalf("restore id = %d, want %d", msg.id, id)
		}
		if _, err := deps.Observations.GetByID(ctx, id); err != nil {
			t.Fatalf("observation not restored after u: %v", err)
		}
	})

	t.Run("esc pops and q resets to the dashboard floor", func(t *testing.T) {
		for _, screen := range []Screen{ScreenObservationDetail, ScreenTimeline, ScreenArchive} {
			t.Run(screenNameForTest(screen), func(t *testing.T) {
				m := newTestModel(120, 40)
				seedNavFrom(&m, screen, ScreenSearchResults)
				out, _ := m.handleKeyPress("esc")
				if got := out.(Model).Screen; got != ScreenSearchResults {
					t.Fatalf("esc: screen = %v, want Search Results", got)
				}

				floor := newTestModel(120, 40)
				seedNavFrom(&floor, screen, ScreenDashboard)
				out, _ = floor.handleKeyPress("esc")
				if got := out.(Model).Screen; got != ScreenDashboard {
					t.Fatalf("esc at floor: screen = %v, want Dashboard", got)
				}

				reset := newTestModel(120, 40)
				seedNavFrom(&reset, screen, ScreenSearchResults)
				out, _ = reset.handleKeyPress("q")
				if got := out.(Model).Screen; got != ScreenDashboard {
					t.Fatalf("q: screen = %v, want Dashboard", got)
				}
			})
		}
	})
}

func screenNameForTest(s Screen) string {
	switch s {
	case ScreenObservationDetail:
		return "detail"
	case ScreenTimeline:
		return "timeline"
	case ScreenArchive:
		return "archive"
	default:
		return "screen"
	}
}

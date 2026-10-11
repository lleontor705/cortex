package tui

import (
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/store/session"
)

// TestSessionsKeymap is the REQ-NAV-001/REQ-NAV-003 oracle for the Sessions and
// Session Detail migration (design W15): every required binding is declared in
// the live registry, enter opens the session/observation per the wireframe,
// esc pops through the navigation stack, and q resets to the Dashboard floor.
func TestSessionsKeymap(t *testing.T) {
	t.Run("bindings declared in the registry", func(t *testing.T) {
		required := map[Screen][]string{
			ScreenSessions:      {"enter", "p", "esc", "q"},
			ScreenSessionDetail: {"up", "k", "down", "j", "enter", "t", "T", "esc", "q"},
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

	t.Run("every declared binding dispatches", func(t *testing.T) {
		for _, screen := range []Screen{ScreenSessions, ScreenSessionDetail} {
			km := keymapRegistry.keymaps[screen]
			for _, b := range km.Bindings {
				m := newTestModel(120, 40)
				seedSessionItems(&m)
				m.Screen = screen
				if _, _, handled := keymapRegistry.Dispatch(m, b.Key); !handled {
					t.Fatalf("screen %v key %q was not handled by the registry", screen, b.Key)
				}
			}
		}
	})

	t.Run("enter opens the selected session", func(t *testing.T) {
		deps := newTUIStoreDeps(t)
		seedSession(t, deps, "s1", "cortex")
		seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "Keymap registry contract", "body")

		m := New(deps)
		m.Width, m.Height = 120, 40
		m.NavPush(ScreenSessions)
		out, _ := m.Update(assertMsgType[recentSessionsMsg](t, loadRecentSessions(deps), "load sessions"))
		m = out.(Model)

		out, cmd := m.handleKeyPress("enter")
		m = out.(Model)
		obsMsg := assertMsgType[sessionObservationsMsg](t, cmd, "enter on sessions")
		if obsMsg.err != nil {
			t.Fatalf("load session observations: %v", obsMsg.err)
		}

		out, _ = m.Update(obsMsg)
		got := out.(Model)
		if got.Screen != ScreenSessionDetail {
			t.Fatalf("screen = %v, want Session Detail", got.Screen)
		}
		if got.NavParent() != ScreenSessions {
			t.Fatalf("NavParent() = %v, want Sessions", got.NavParent())
		}
		if len(got.SessionObservations) != 1 || got.SessionObservations[0].Title != "Keymap registry contract" {
			t.Fatalf("observations = %+v, want the seeded observation", got.SessionObservations)
		}
	})

	t.Run("enter opens the focused observation", func(t *testing.T) {
		m := newTestModel(120, 40)
		seedNavFrom(&m, ScreenSessionDetail, ScreenSessions)
		m.SessionObservations = []*domain.Observation{newTestObs(42, "decision", "Nav")}

		out, cmd := m.handleKeyPress("enter")
		got := out.(Model)
		if cmd == nil {
			t.Fatal("enter on Session Detail returned no command")
		}
		if !got.DetailLoading {
			t.Fatal("enter did not start loading the focused observation")
		}
		if got.Screen != ScreenSessionDetail {
			t.Fatalf("enter pushed before the load resolved: screen = %v, want Session Detail", got.Screen)
		}
	})

	t.Run("esc pops back to sessions then the dashboard floor", func(t *testing.T) {
		detail := newTestModel(120, 40)
		seedNavFrom(&detail, ScreenSessionDetail, ScreenSessions)
		if out, _ := detail.handleKeyPress("esc"); out.(Model).Screen != ScreenSessions {
			t.Fatalf("esc on Session Detail = %v, want Sessions", out.(Model).Screen)
		}

		sessions := newTestModel(120, 40)
		seedNavFrom(&sessions, ScreenSessions, ScreenDashboard)
		if out, _ := sessions.handleKeyPress("esc"); out.(Model).Screen != ScreenDashboard {
			t.Fatalf("esc on Sessions = %v, want Dashboard", out.(Model).Screen)
		}
	})

	t.Run("q resets to the dashboard floor", func(t *testing.T) {
		for _, screen := range []Screen{ScreenSessions, ScreenSessionDetail} {
			m := newTestModel(120, 40)
			seedNavFrom(&m, screen, ScreenSessions)
			out, cmd := m.handleKeyPress("q")
			if got := out.(Model).Screen; got != ScreenDashboard {
				t.Fatalf("q on %v = %v, want Dashboard", screen, got)
			}
			if cmd == nil {
				t.Fatalf("q on %v returned no command", screen)
			}
		}
	})

	t.Run("p cycles the project filter", func(t *testing.T) {
		m := newTestModel(120, 40)
		m.Screen = ScreenSessions
		m.Stats = &combinedStats{Projects: []string{"a", "b"}}
		out, _ := m.handleKeyPress("p")
		if got := out.(Model).FilterProject; got != "a" {
			t.Fatalf("FilterProject = %q, want %q", got, "a")
		}
	})

	t.Run("header chrome follows the W15 path", func(t *testing.T) {
		m := newTestModel(120, 40)
		seedNavFrom(&m, ScreenSessions, ScreenDashboard)
		if got := m.navBreadcrumb(); got != "Dashboard / Sessions" {
			t.Fatalf("sessions breadcrumb = %q", got)
		}
		m.Sessions = []*session.SessionStats{{Session: &domain.Session{ID: "ses_8f3a"}}}
		m.SelectedSessionIdx = 0
		m.NavPush(ScreenSessionDetail)
		if got := m.navBreadcrumb(); got != "Dashboard / Sessions / Session Detail" {
			t.Fatalf("session detail breadcrumb = %q", got)
		}
	})
}

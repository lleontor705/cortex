package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Sessions and SessionDetail are migrated to declarative registry keymaps
// (REQ-NAV-001, design W15). Every key the handlers below switch on is declared
// in the keymap, so the registry is the single dispatch path and no screen-local
// switch can bypass it. esc/q follow the navigation-stack semantics from nav.go:
// esc pops one level (Dashboard is the floor), q resets to the Dashboard floor.
//
// The keymaps are registered at package init, after update.go has built the
// registry, so they replace the delegate placeholders there.

func init() {
	keymapRegistry.Register(sessionsKeymap())
	keymapRegistry.Register(sessionDetailKeymap())
}

// sessionsKeymap declares the Sessions screen bindings (design W15).
func sessionsKeymap() Keymap {
	bind := func(key, description string) Binding {
		return Binding{Key: key, Description: description, Handler: Model.handleSessionsKeys}
	}
	return Keymap{Screen: ScreenSessions, Bindings: []Binding{
		bind("enter", "Open session"),
		{Key: "p", Description: "Project filter", Handler: cycleFilterKey},
		bind("esc", "Back"),
		bind("q", "Dashboard"),
	}}
}

// sessionDetailKeymap declares the Session Detail screen bindings (design W15).
func sessionDetailKeymap() Keymap {
	bind := func(key, description string) Binding {
		return Binding{Key: key, Description: description, Handler: Model.handleSessionDetailKeys}
	}
	return Keymap{Screen: ScreenSessionDetail, Bindings: []Binding{
		bind("up", "Move up"),
		bind("k", "Move up"),
		bind("down", "Move down"),
		bind("j", "Move down"),
		bind("enter", "Open observation"),
		bind("t", "Timeline"),
		bind("T", "Timeline"),
		bind("esc", "Back to sessions"),
		bind("q", "Dashboard"),
	}}
}

// ─── Sessions ───────────────────────────────────────────────────────────────

func (m Model) handleSessionsKeys(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		if item, ok := m.SessionListModel.SelectedItem().(sessionItem); ok {
			m.SelectedSessionIdx = m.SessionListModel.Index()
			return m, loadSessionObservations(m.deps, item.session.Session.ID)
		}
	case "esc":
		prev := m.NavPop()
		m.Cursor = m.PrevCursor
		return m, m.refreshScreen(prev)
	case "q":
		m.NavReset()
		m.Cursor = 0
		m.Scroll = 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

// ─── Session Detail ─────────────────────────────────────────────────────────

func (m Model) handleSessionDetailKeys(key string) (tea.Model, tea.Cmd) {
	visibleItems := (m.Height - 12) / linesPerItem
	if visibleItems < minVisibleItems {
		visibleItems = minVisibleItems
	}

	switch key {
	case "up", "k":
		if m.Cursor > 0 {
			m.Cursor--
			if m.Cursor < m.SessionDetailScroll {
				m.SessionDetailScroll = m.Cursor
			}
		}
	case "down", "j":
		if m.Cursor < len(m.SessionObservations)-1 {
			m.Cursor++
			if m.Cursor >= m.SessionDetailScroll+visibleItems {
				m.SessionDetailScroll = m.Cursor - visibleItems + 1
			}
		}
	case "enter":
		if len(m.SessionObservations) > 0 && m.Cursor < len(m.SessionObservations) {
			obsID := m.SessionObservations[m.Cursor].ID
			m.DetailLoading = true
			return m, tea.Batch(m.SetupSpinner.Tick, loadObservationDetail(m.deps, obsID))
		}
	case "t", "T":
		if len(m.SessionObservations) > 0 && m.Cursor < len(m.SessionObservations) {
			obsID := m.SessionObservations[m.Cursor].ID
			return m, loadTimeline(m.deps, obsID)
		}
	case "esc":
		prev := m.NavPop()
		m.Cursor = m.SelectedSessionIdx
		m.SessionDetailScroll = 0
		return m, m.refreshScreen(prev)
	case "q":
		m.NavReset()
		m.Cursor = 0
		m.Scroll = 0
		m.SessionDetailScroll = 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

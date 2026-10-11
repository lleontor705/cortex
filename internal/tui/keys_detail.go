package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// ─── Observation Detail ─────────────────────────────────────────────────────

func (m Model) handleObservationDetailKeys(key string) (tea.Model, tea.Cmd) {
	if m.ConfirmDelete {
		switch key {
		case "y", "Y":
			m.ConfirmDelete = false
			return m, deleteObservationCmd(m.deps, m.ConfirmDeleteID)
		case "n", "N", "esc":
			m.ConfirmDelete = false
		}
		return m, nil
	}

	switch key {
	case "up", "k":
		m.DetailViewport.ScrollUp(1)
	case "down", "j":
		m.DetailViewport.ScrollDown(1)
	case "pgup":
		m.DetailViewport.HalfPageUp()
	case "pgdown":
		m.DetailViewport.HalfPageDown()
	case "T", "t":
		if m.SelectedObservation != nil {
			return m, loadTimeline(m.deps, m.SelectedObservation.ID)
		}
	case "g":
		if m.SelectedObservation != nil {
			m.GraphRootID = m.SelectedObservation.ID
			return m, loadGraphRelated(m.deps, m.SelectedObservation.ID)
		}
	case "s", "S":
		if m.SelectedObservation != nil && m.SelectedObservation.SessionID != "" {
			m.NavPush(ScreenSessionDetail)
			m.Cursor = 0
			m.SessionDetailScroll = 0
			return m, loadSessionObservations(m.deps, m.SelectedObservation.SessionID)
		}
	case "d":
		if m.SelectedObservation != nil {
			m.ConfirmDelete = true
			m.ConfirmDeleteID = m.SelectedObservation.ID
			m.DeleteTargetTitle = m.SelectedObservation.Title
		}
		return m, nil
	case "esc":
		prev := m.NavPop()
		m.Cursor = m.PrevCursor
		m.DetailViewport.GotoTop()
		return m, m.refreshScreen(prev)
	case "q":
		m.NavReset()
		m.Cursor = 0
		m.Scroll = 0
		m.DetailViewport.GotoTop()
		return m, loadStats(m.deps)
	}
	return m, nil
}

// ─── Timeline ───────────────────────────────────────────────────────────────

func (m Model) handleTimelineKeys(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.Scroll > 0 {
			m.Scroll--
		}
	case "down", "j":
		m.Scroll++
		if m.Scroll > maxScrollOffset {
			m.Scroll = maxScrollOffset
		}
	case "enter":
		if m.TimelineFocus != nil {
			m.DetailLoading = true
			return m, tea.Batch(m.SetupSpinner.Tick, loadObservationDetail(m.deps, m.TimelineFocus.ID))
		}
	case "esc":
		prev := m.NavPop()
		m.Cursor = 0
		m.Scroll = 0
		return m, m.refreshScreen(prev)
	case "q":
		m.NavReset()
		m.Cursor = 0
		m.Scroll = 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

// timelineKeymap declares the Timeline screen bindings (design W11). Every key
// routes through handleTimelineKeys so the declaration and the handler cannot
// drift apart.
func timelineKeymap() Keymap {
	bind := func(key, description string) Binding {
		return Binding{Key: key, Description: description, Handler: Model.handleTimelineKeys}
	}
	return Keymap{Screen: ScreenTimeline, Bindings: []Binding{
		bind("up", "Move up"),
		bind("k", "Move up"),
		bind("down", "Move down"),
		bind("j", "Move down"),
		bind("enter", "Open"),
		bind("esc", "Back to detail"),
		bind("q", "Dashboard"),
	}}
}

func init() {
	keymapRegistry.Register(timelineKeymap())
}

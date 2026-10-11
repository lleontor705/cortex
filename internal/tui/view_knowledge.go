package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ─── Archive (Cortex-exclusive) ─────────────────────────────────────────────

func (m Model) viewArchive() string {
	var b strings.Builder

	count := len(m.ArchivedObservations)
	header := fmt.Sprintf("  Archived Observations — %d total", count)
	if m.FilterProject != "" {
		header += fmt.Sprintf(" (project: %s)", m.FilterProject)
	}
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	if count == 0 {
		b.WriteString(noResultsStyle.Render("No archived observations."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  esc back"))
		return b.String()
	}

	b.WriteString(m.ArchiveList.View())
	b.WriteString(helpStyle.Render("\n  [enter] View • [v] Preview • [u] Restore • [esc] Back"))

	return b.String()
}

// archiveBack implements the W14 esc binding: pop to the previous screen,
// honoring the Dashboard floor (REQ-NAV-003).
func archiveBack(m Model, _ string) (tea.Model, tea.Cmd) {
	prev := m.NavPop()
	m.Cursor = 0
	m.Scroll = 0
	return m, m.refreshScreen(prev)
}

// archiveReset implements the q binding: reset navigation to the Dashboard.
func archiveReset(m Model, _ string) (tea.Model, tea.Cmd) {
	m.NavReset()
	m.Cursor = 0
	m.Scroll = 0
	return m, loadStats(m.deps)
}

// archiveKeymap declares the Archive screen bindings (design W14). List actions
// delegate to handleArchiveKeys; esc/q are declared here so the screen follows
// the REQ-NAV-003 stack semantics instead of the handler's legacy hardcoded
// Dashboard return.
func archiveKeymap() Keymap {
	delegate := func(key, description string) Binding {
		return Binding{Key: key, Description: description, Handler: Model.handleArchiveKeys}
	}
	return Keymap{Screen: ScreenArchive, Bindings: []Binding{
		delegate("enter", "View"),
		{Key: "v", Description: "Preview", Handler: togglePreviewKey},
		{Key: "V", Description: "Preview", Handler: togglePreviewKey},
		delegate("u", "Restore"),
		delegate("d", "Delete"),
		delegate("f", "Filter"),
		{Key: "p", Description: "Project filter", Handler: cycleFilterKey},
		delegate("y", "Confirm"),
		delegate("Y", "Confirm"),
		{Key: "esc", Description: "Back", Handler: archiveBack},
		{Key: "q", Description: "Dashboard", Handler: archiveReset},
	}}
}

func init() {
	keymapRegistry.Register(archiveKeymap())
}

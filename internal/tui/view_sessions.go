package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// ─── Sessions ───────────────────────────────────────────────────────────────

func (m Model) viewSessions() string {
	var b strings.Builder

	count := len(m.Sessions)
	project := m.FilterProject
	if project == "" {
		project = "all"
	}
	header := fmt.Sprintf("  Sessions — %d total", count)
	b.WriteString(headerStyle.Render(header))
	b.WriteString(helpStyle.Render(fmt.Sprintf("  [p] proj: %s", project)))
	b.WriteString("\n")

	if count == 0 {
		b.WriteString(noResultsStyle.Render("No sessions yet."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  [esc] Back"))
		return b.String()
	}

	b.WriteString(m.SessionListModel.View())

	b.WriteString(helpStyle.Render("\n  [enter] Open session • [esc] Back"))

	return b.String()
}

// ─── Session Detail ─────────────────────────────────────────────────────────

func (m Model) viewSessionDetail() string {
	var b strings.Builder

	if m.SelectedSessionIdx >= len(m.Sessions) {
		b.WriteString(headerStyle.Render("  Session Detail"))
		b.WriteString("\n")
		b.WriteString(noResultsStyle.Render("Session not found."))
		return b.String()
	}

	sess := m.Sessions[m.SelectedSessionIdx]
	header := fmt.Sprintf("  Session: %s — %s", sess.Session.Project, formatTime(sess.Session.StartedAt))
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	// Session info table
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(colorOverlay)).
		Headers("Field", "Value").
		Row("Project", sess.Session.Project).
		Row("Started", formatTime(sess.Session.StartedAt)).
		Row("Observations", fmt.Sprintf("%d", sess.ObservationCount)).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Padding(0, 1)
			}
			if col == 0 {
				return lipgloss.NewStyle().Foreground(colorPurple).Padding(0, 1)
			}
			return lipgloss.NewStyle().Foreground(colorText).Padding(0, 1)
		})

	if sess.Session.Summary != "" {
		t = t.Row("Summary", truncateStr(sess.Session.Summary, 60))
	}

	b.WriteString(t.Render())
	b.WriteString("\n\n")

	count := len(m.SessionObservations)
	b.WriteString(sectionHeadingStyle.Render(fmt.Sprintf("  Observations (%d)", count)))
	b.WriteString("\n")

	if count == 0 {
		b.WriteString(noResultsStyle.Render("No observations in this session."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  [esc] Back to sessions"))
		return b.String()
	}

	visibleItems := (m.Height - 12) / 2
	if visibleItems < 3 {
		visibleItems = 3
	}

	end := m.SessionDetailScroll + visibleItems
	if end > count {
		end = count
	}

	for i := m.SessionDetailScroll; i < end; i++ {
		o := m.SessionObservations[i]
		b.WriteString(m.renderObservationListItem(i, o.ID, o.Type, o.Title, o.Content, o.CreatedAt, o.Project, nil))
	}

	if count > visibleItems {
		fmt.Fprintf(&b, "\n  %s",
			timestampStyle.Render(fmt.Sprintf("showing %d-%d of %d", m.SessionDetailScroll+1, end, count)))
	}

	b.WriteString(helpStyle.Render("\n  [enter] Open observation • [esc] Back to sessions"))

	return b.String()
}

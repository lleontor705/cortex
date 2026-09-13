package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// ─── Search ─────────────────────────────────────────────────────────────────

func (m Model) viewSearch() string {
	var b strings.Builder

	b.WriteString(headerStyle.Render("  Search Knowledge"))
	b.WriteString("\n\n")

	source := "Memories"
	if m.SearchMode == "code" {
		source = "Code AST"
	}
	project := "all projects"
	if m.FilterProject != "" {
		project = m.FilterProject
	}
	b.WriteString(helpStyle.Render(fmt.Sprintf("  Source: %s  •  Project: %s", source, project)))
	b.WriteString("\n\n")

	b.WriteString(searchInputStyle.Render(m.SearchInput.View()))
	b.WriteString("\n\n")

	b.WriteString(helpStyle.Render("  enter search • tab switch source • f project • esc back"))

	return b.String()
}

// ─── Search Results ─────────────────────────────────────────────────────────

func (m Model) viewSearchResults() string {
	var b strings.Builder

	resultCount := len(m.SearchResults)
	source := "memories"
	if m.SearchMode == "code" {
		resultCount = len(m.CodeSearchResults)
		source = "code symbols"
	}
	header := fmt.Sprintf("  Search %s: %q — %d result", source, m.SearchQuery, resultCount)
	if resultCount != 1 {
		header += "s"
	}
	if m.FilterProject != "" {
		header += fmt.Sprintf(" (project: %s)", m.FilterProject)
	}
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	if resultCount == 0 {
		b.WriteString(noResultsStyle.Render("No results found. Try another query, source, or project."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  / new search • esc back"))
		return b.String()
	}

	if m.Width >= 70 {
		listWidth := m.Width * 38 / 100
		if listWidth < 28 {
			listWidth = 28
		}
		previewWidth := m.Width - listWidth - 4
		if previewWidth < 30 {
			previewWidth = 30
		}
		paneHeight := m.Height - 8
		if paneHeight < 8 {
			paneHeight = 8
		}

		m.SearchListModel.SetSize(listWidth-4, paneHeight-2)
		m.PreviewViewport.Width = previewWidth - 4
		m.PreviewViewport.Height = paneHeight - 2

		listPane := paneFocusedStyle.
			Width(listWidth).
			Height(paneHeight).
			Render(m.SearchListModel.View())

		previewContent := m.PreviewViewport.View()
		if previewContent == "" && (len(m.SearchResults) > 0 || len(m.CodeSearchResults) > 0) {
			m.updatePreviewContent()
			previewContent = m.PreviewViewport.View()
		}
		if previewContent == "" {
			previewContent = lipgloss.NewStyle().Foreground(colorSubtext).Italic(true).Render("Select a result to inspect live details...")
		}
		scrollPct := fmt.Sprintf(" %3.f%% ", m.PreviewViewport.ScrollPercent()*100)
		previewPane := paneUnfocusedStyle.
			Width(previewWidth).
			Height(paneHeight).
			Render(previewContent + "\n" + timestampStyle.Render(scrollPct))

		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, listPane, previewPane))
	} else {
		b.WriteString(m.SearchListModel.View())
	}

	helpText := "  j/k navigate • enter detail • t timeline • d delete • f filter • / search • esc back"
	b.WriteString(helpStyle.Render("\n" + helpText))

	return b.String()
}

// ─── Recent Observations ────────────────────────────────────────────────────

func (m Model) viewRecent() string {
	var b strings.Builder

	count := len(m.RecentObservations)
	header := fmt.Sprintf("  Recent Observations — %d total", count)
	if m.FilterProject != "" {
		header += fmt.Sprintf(" (project: %s)", m.FilterProject)
	}
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	if count == 0 {
		b.WriteString(noResultsStyle.Render("No observations yet."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  esc back"))
		return b.String()
	}

	if m.Width >= 70 {
		listWidth := m.Width * 38 / 100
		if listWidth < 28 {
			listWidth = 28
		}
		previewWidth := m.Width - listWidth - 4
		if previewWidth < 30 {
			previewWidth = 30
		}
		paneHeight := m.Height - 8
		if paneHeight < 8 {
			paneHeight = 8
		}

		m.RecentList.SetSize(listWidth-4, paneHeight-2)
		m.PreviewViewport.Width = previewWidth - 4
		m.PreviewViewport.Height = paneHeight - 2

		listPane := paneFocusedStyle.
			Width(listWidth).
			Height(paneHeight).
			Render(m.RecentList.View())

		previewContent := m.PreviewViewport.View()
		if previewContent == "" && len(m.RecentObservations) > 0 {
			m.updatePreviewContent()
			previewContent = m.PreviewViewport.View()
		}
		if previewContent == "" {
			previewContent = lipgloss.NewStyle().Foreground(colorSubtext).Italic(true).Render("Select an observation to inspect live details...")
		}
		scrollPct := fmt.Sprintf(" %3.f%% ", m.PreviewViewport.ScrollPercent()*100)
		previewPane := paneUnfocusedStyle.
			Width(previewWidth).
			Height(paneHeight).
			Render(previewContent + "\n" + timestampStyle.Render(scrollPct))

		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, listPane, previewPane))
	} else {
		b.WriteString(m.RecentList.View())
	}

	helpText := "  j/k navigate • enter detail • t timeline • g graph • d delete • f filter • esc back"
	b.WriteString(helpStyle.Render("\n" + helpText))

	return b.String()
}

// ─── Observation Detail ─────────────────────────────────────────────────────

func (m Model) viewObservationDetail() string {
	var b strings.Builder

	if m.SelectedObservation == nil {
		b.WriteString(headerStyle.Render("  Observation Detail"))
		b.WriteString("\n")
		if m.DetailLoading {
			b.WriteString(noResultsStyle.Render(m.SetupSpinner.View() + " Loading observation..."))
		} else {
			b.WriteString(noResultsStyle.Render("Loading..."))
		}
		return b.String()
	}

	obs := m.SelectedObservation

	header := fmt.Sprintf("  Observation #%d", obs.ID)
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	// Viewport handles all content rendering and scrolling
	b.WriteString(m.DetailViewport.View())
	b.WriteString("\n")

	// Scroll percentage indicator
	scrollPct := fmt.Sprintf(" %3.f%% ", m.DetailViewport.ScrollPercent()*100)
	b.WriteString(timestampStyle.Render(scrollPct))

	b.WriteString(helpStyle.Render("  j/k scroll • t timeline • g graph • s session • d delete • esc back"))

	return b.String()
}

// ─── Timeline ───────────────────────────────────────────────────────────────

func (m Model) viewTimeline() string {
	var b strings.Builder

	if m.TimelineFocus == nil {
		b.WriteString(headerStyle.Render("  Timeline"))
		b.WriteString("\n")
		b.WriteString(noResultsStyle.Render("Loading..."))
		return b.String()
	}

	focus := m.TimelineFocus
	total := len(m.TimelineBefore) + 1 + len(m.TimelineAfter)
	header := fmt.Sprintf("  Timeline — Observation #%d (%d in session)", focus.ID, total)
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	// Session info
	fmt.Fprintf(&b, "  %s %s  %s %s\n\n",
		detailLabelStyle.Render("Session:"),
		idStyle.Render(focus.SessionID),
		detailLabelStyle.Render("Project:"),
		projectStyle.Render(focus.Project))

	// Before entries
	if len(m.TimelineBefore) > 0 {
		b.WriteString(sectionHeadingStyle.Render("  Before"))
		b.WriteString("\n")
		for _, e := range m.TimelineBefore {
			fmt.Fprintf(&b, "  %s %s %s  %s\n",
				timelineConnectorStyle.Render("|"),
				idStyle.Render(fmt.Sprintf("#%-4d", e.ID)),
				typeBadgeStyle.Render(fmt.Sprintf("[%-12s]", e.Type)),
				timelineItemStyle.Render(truncateStr(e.Title, 60)))
		}
		fmt.Fprintf(&b, "  %s\n", timelineConnectorStyle.Render("|"))
	}

	// Focus (highlighted)
	focusContent := fmt.Sprintf("  %s %s  %s\n  %s",
		idStyle.Render(fmt.Sprintf("#%d", focus.ID)),
		typeBadgeStyle.Render("["+focus.Type+"]"),
		lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render(focus.Title),
		detailContentStyle.Render(truncateStr(focus.Content, 120)))
	b.WriteString(timelineFocusStyle.Render(focusContent))
	b.WriteString("\n")

	// After entries
	if len(m.TimelineAfter) > 0 {
		fmt.Fprintf(&b, "  %s\n", timelineConnectorStyle.Render("|"))
		b.WriteString(sectionHeadingStyle.Render("  After"))
		b.WriteString("\n")
		for _, e := range m.TimelineAfter {
			fmt.Fprintf(&b, "  %s %s %s  %s\n",
				timelineConnectorStyle.Render("|"),
				idStyle.Render(fmt.Sprintf("#%-4d", e.ID)),
				typeBadgeStyle.Render(fmt.Sprintf("[%-12s]", e.Type)),
				timelineItemStyle.Render(truncateStr(e.Title, 60)))
		}
	}

	b.WriteString(helpStyle.Render("\n  j/k scroll • esc back"))

	return b.String()
}

// ─── Sessions ───────────────────────────────────────────────────────────────

func (m Model) viewSessions() string {
	var b strings.Builder

	count := len(m.Sessions)
	header := fmt.Sprintf("  Sessions — %d total", count)
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	if count == 0 {
		b.WriteString(noResultsStyle.Render("No sessions yet."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  esc back"))
		return b.String()
	}

	b.WriteString(m.SessionListModel.View())

	b.WriteString(helpStyle.Render("\n  j/k navigate • enter view session • esc back"))

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
		b.WriteString(helpStyle.Render("  esc back"))
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

	b.WriteString(helpStyle.Render("\n  j/k navigate • enter detail • t timeline • esc back"))

	return b.String()
}

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

	b.WriteString(helpStyle.Render("\n  j/k navigate • enter detail • u unarchive • d delete • f filter • esc back"))

	return b.String()
}

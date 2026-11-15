package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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

	b.WriteString(helpStyle.Render("  j/k scroll • [T] timeline • g graph • [d] delete (confirm) • [esc] back"))

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

	b.WriteString(helpStyle.Render("\n  [enter] Open • [esc] Back to detail"))

	return b.String()
}

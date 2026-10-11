package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
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

	b.WriteString(helpStyle.Render("  enter search • tab switch source • p project • esc back"))

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
		b.WriteString(noResultsStyle.Render("  No results found."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  Try a different query, switch source with tab, or clear the project filter with p."))
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

	helpText := "  j/k navigate • enter open • T timeline • v preview • p project • d delete • / search • esc back"
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

	helpText := "  j/k navigate • enter open • T timeline • g graph • v preview • p project • d delete • esc back"
	b.WriteString(helpStyle.Render("\n" + helpText))

	return b.String()
}

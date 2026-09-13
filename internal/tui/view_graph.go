package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// ─── Graph (Cortex-exclusive) ───────────────────────────────────────────────

func (m Model) viewGraph() string {
	var b strings.Builder

	header := fmt.Sprintf("  Knowledge Graph — Root #%d", m.GraphRootID)
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	// Show edges as a table (up to 20) or summary counts if more
	if len(m.GraphEdges) > 0 && len(m.GraphEdges) <= 20 {
		et := table.New().
			Border(lipgloss.NormalBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(colorOverlay)).
			Headers("From", "Relation", "To", "Weight").
			StyleFunc(func(row, col int) lipgloss.Style {
				if row == table.HeaderRow {
					return lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Padding(0, 1)
				}
				if col == 1 {
					return lipgloss.NewStyle().Foreground(colorMauve).Bold(true).Padding(0, 1)
				}
				return lipgloss.NewStyle().Foreground(colorText).Padding(0, 1)
			})

		for _, e := range m.GraphEdges {
			et = et.Row(
				fmt.Sprintf("#%d", e.FromObsID),
				e.RelationType,
				fmt.Sprintf("#%d", e.ToObsID),
				fmt.Sprintf("%.1f", e.Weight),
			)
		}

		b.WriteString(et.Render())
		b.WriteString("\n\n")
	} else if len(m.GraphEdges) > 20 {
		edgeCounts := make(map[string]int)
		for _, e := range m.GraphEdges {
			edgeCounts[e.RelationType]++
		}
		var parts []string
		for rel, count := range edgeCounts {
			parts = append(parts, fmt.Sprintf("%s: %d", rel, count))
		}
		fmt.Fprintf(&b, "  %s\n\n", timestampStyle.Render(strings.Join(parts, " | ")))
	}

	count := len(m.GraphObservations)
	if count == 0 {
		b.WriteString(noResultsStyle.Render("No related observations found."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  esc back"))
		return b.String()
	}

	b.WriteString(m.GraphListModel.View())

	b.WriteString(helpStyle.Render("\n  j/k navigate • enter detail • r re-root • esc back"))

	return b.String()
}

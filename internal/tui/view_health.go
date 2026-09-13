package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

// ─── Health (Cortex-exclusive) ──────────────────────────────────────────

func (m Model) viewHealth() string {
	var b strings.Builder

	b.WriteString(headerStyle.Render("  Memory Health Dashboard"))
	b.WriteString("\n")

	// Density
	density := 0.0
	if m.HealthObsCount > 0 {
		density = float64(m.HealthEdgeCount) / float64(m.HealthObsCount)
	}
	densityLabel := "low"
	if density >= 1.0 {
		densityLabel = "healthy"
	} else if density >= 0.5 {
		densityLabel = "moderate"
	}

	statsContent := fmt.Sprintf(
		"%s %s\n%s %s\n%s %s",
		statNumberStyle.Render(fmt.Sprintf("%d", m.HealthObsCount)),
		statLabelStyle.Render("observations"),
		statNumberStyle.Render(fmt.Sprintf("%d", m.HealthEdgeCount)),
		statLabelStyle.Render("knowledge links"),
		statNumberStyle.Render(fmt.Sprintf("%.1f", density)),
		statLabelStyle.Render("links/observation ("+densityLabel+")"),
	)
	b.WriteString(statCardStyle.Render(statsContent))
	b.WriteString("\n")

	// Stale observations — tab-style header
	tabStyle := lipgloss.NewStyle().Background(colorAmber).Foreground(lipgloss.Color("#16161e")).Bold(true).Padding(0, 1)
	tabActiveStyle := lipgloss.NewStyle().Background(colorCyan).Foreground(lipgloss.Color("#16161e")).Bold(true).Padding(0, 1)
	tabRedStyle := lipgloss.NewStyle().Background(colorRed).Foreground(lipgloss.Color("#16161e")).Bold(true).Padding(0, 1)

	_ = tabActiveStyle // used below
	b.WriteString("  " + tabStyle.Render(fmt.Sprintf("Stale (%d)", len(m.HealthStale))))
	b.WriteString("\n")
	if len(m.HealthStale) == 0 {
		b.WriteString(listItemStyle.Render("  None — all high-score observations accessed recently"))
		b.WriteString("\n")
	} else {
		b.WriteString(renderHealthTable(m.HealthStale))
	}
	b.WriteString("\n")

	// Orphan observations — tab-style header
	b.WriteString("  " + tabRedStyle.Render(fmt.Sprintf("Orphans (%d)", len(m.HealthOrphans))))
	b.WriteString("\n")
	if len(m.HealthOrphans) == 0 {
		b.WriteString(listItemStyle.Render("  None — all observations are connected via graph"))
		b.WriteString("\n")
	} else {
		b.WriteString(renderHealthTable(m.HealthOrphans))
	}
	b.WriteString("\n")

	// Consolidation candidates — tab-style header
	b.WriteString("  " + tabActiveStyle.Render(fmt.Sprintf("Consolidation (%d)", len(m.HealthCandidates))))
	b.WriteString("\n")
	if len(m.HealthCandidates) == 0 {
		b.WriteString(listItemStyle.Render("  None — no duplicate topic keys found"))
		b.WriteString("\n")
	} else {
		for i, c := range m.HealthCandidates {
			if i >= 10 {
				fmt.Fprintf(&b, "    %s\n", timestampStyle.Render(fmt.Sprintf("...and %d more", len(m.HealthCandidates)-10)))
				break
			}
			fmt.Fprintf(&b, "  %-40s  %s\n",
				projectStyle.Render(c.topicKey),
				statNumberStyle.Render(fmt.Sprintf("%d obs", c.count)))
		}
	}

	b.WriteString(helpStyle.Render("\n  j/k scroll • tab section • enter expand/collapse • esc back"))

	return b.String()
}

// renderHealthTable renders a table of observations for the health screen.
func renderHealthTable(observations []*domain.Observation) string {
	if len(observations) == 0 {
		return "  No items\n"
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(colorOverlay)).
		Headers("ID", "Type", "Title", "Project", "Created").
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Padding(0, 1)
			}
			return lipgloss.NewStyle().Foreground(colorText).Padding(0, 1)
		})

	limit := 10
	for i, o := range observations {
		if i >= limit {
			break
		}
		t = t.Row(
			fmt.Sprintf("#%d", o.ID),
			o.Type,
			truncateStr(o.Title, 35),
			o.Project,
			formatTime(o.CreatedAt),
		)
	}

	result := t.Render()
	if len(observations) > limit {
		result += fmt.Sprintf("\n  %s", timestampStyle.Render(fmt.Sprintf("...and %d more", len(observations)-limit)))
	}
	return result + "\n"
}

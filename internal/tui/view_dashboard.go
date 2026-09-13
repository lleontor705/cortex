package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ─── Home / Configuration & Control Hub (based on cortex-ia TUI) ───────────

var homeDescriptions = []string{
	"Modular settings: Storage, AI/LLM, HTTP API, MCP Proxy & Sync",
	"Inspect recently captured decisions, bugfixes and learnings",
	"Chronological agent session history and interaction logs",
	"HippoRAG Personalized PageRank & AST dependency call-graph",
	"Assess graph density, orphan observations & SQLite integrity",
	"Browse soft-deleted observations, revision ledger & restore",
	"Vector dimensions, Ollama/OpenAI models and auto-start toggles",
	"Zero-CGO SQLite v2 baseline path, WAL mode and auto-vacuum",
	"Authenticate with remote Cortex Server via bearer token",
	"Install & configure Claude Code, OpenCode, Gemini CLI, Cursor",
	"Exit cortex session",
}

func (m Model) viewDashboard() string {
	width := m.Width
	if width <= 0 {
		width = 80
	}
	if width > 100 {
		width = 100
	}
	contentWidth := width - 4
	if contentWidth < 18 {
		contentWidth = 18
	}

	var lines []string

	// 1. Shimmer Logo with Braille Wings
	if m.Height <= 0 || m.Height >= 18 {
		lines = append(lines, ShimmerLogo(m.AnimFrame))
		sub := lipgloss.NewStyle().Foreground(colorSubtext).Render("  cortex " + m.Version + " · Configuration & Runtime Control Hub · " + m.CurrentUser)
		lines = append(lines, sub, "")
	} else {
		title := lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("cortex " + m.Version)
		sub := lipgloss.NewStyle().Foreground(colorSubtext).Render(" · Configuration Hub · " + m.CurrentUser)
		lines = append(lines, title+sub, "")
	}

	// 2. Identity, Runtime Mode & Telemetry Status Line
	var metaParts []string
	role := strings.ToLower(m.UserRole)
	if role == "" {
		role = "admin"
	}
	metaParts = append(metaParts, fmt.Sprintf("%s (%s)", m.CurrentUser, role))

	syncStatus := "local"
	if m.UploadToCortex {
		syncStatus = "cloud sync"
	}
	metaParts = append(metaParts, syncStatus)

	if m.Stats != nil {
		metaParts = append(metaParts, fmt.Sprintf("%d observations", m.Stats.TotalObservations))
		metaParts = append(metaParts, fmt.Sprintf("%d knowledge links", m.Stats.TotalEdges))
		if len(m.Stats.Projects) > 0 {
			metaParts = append(metaParts, "projects: "+strings.Join(m.Stats.Projects, ", "))
		}
	} else {
		metaParts = append(metaParts, "telemetry ready")
	}

	themeLabel := "dark"
	if !m.IsDarkTheme {
		themeLabel = "light"
	}
	metaParts = append(metaParts, themeLabel)

	lines = append(lines, lipgloss.NewStyle().Foreground(colorSubtext).Render("  "+strings.Join(metaParts, " · ")), "")

	// 3. Update notification banner if present
	if m.UpdateResult != nil {
		msg := fmt.Sprintf("  Update available: %s (current: %s) · Press [U] to install · %s", m.UpdateResult.Latest, m.Version, m.UpdateResult.UpdateURL)
		lines = append(lines, updateBannerStyle.Render(msg), "")
	}

	// 4. Memory Types Breakdown if available
	if m.Stats != nil && len(m.Stats.ByType) > 0 {
		var typeParts []string
		types := []string{"decision", "bugfix", "discovery", "pattern", "feature", "learning"}
		for _, t := range types {
			if c, ok := m.Stats.ByType[t]; ok && c > 0 {
				badge := renderTypeBadge(t)
				typeParts = append(typeParts, fmt.Sprintf("%s %d", badge, c))
			}
		}
		for t, c := range m.Stats.ByType {
			found := false
			for _, dt := range types {
				if dt == t {
					found = true
					break
				}
			}
			if !found && c > 0 {
				badge := renderTypeBadge(t)
				typeParts = append(typeParts, fmt.Sprintf("%s %d", badge, c))
			}
		}
		if len(typeParts) > 0 {
			lines = append(lines, lipgloss.NewStyle().Foreground(colorSubtext).Render("  Types: ")+strings.Join(typeParts, "  "), "")
		}
	}

	// 5. Menu Entries (Configuration-Centered)
	for i, entry := range dashboardMenuItems {
		prefix := fmt.Sprintf("  [%d] ", i+1)
		text := entry
		desc := ""
		if i < len(homeDescriptions) {
			desc = " · " + lipgloss.NewStyle().Foreground(colorSubtext).Render(homeDescriptions[i])
		}
		if i == m.Cursor {
			prefix = fmt.Sprintf("▸ [%d] ", i+1)
			text = lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render(entry)
		} else {
			text = lipgloss.NewStyle().Foreground(colorText).Render(entry)
		}
		lines = append(lines, truncateStr(prefix+text+desc, contentWidth))
	}

	// 6. Navigation Footer
	lines = append(lines, "", lipgloss.NewStyle().Foreground(colorSubtext).Render("  ↑/↓ move · 1-9/enter select · c config · s search · t theme · q quit"))

	body := strings.Join(lines, "\n")
	frame := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPurple).
		Padding(0, 1).
		Render(body)

	return frame
}

// renderSparkline renders a 7-day activity sparkline with block characters.
func renderSparkline(data []int) string {
	blocks := []rune{'\u2581', '\u2582', '\u2583', '\u2584', '\u2585', '\u2586', '\u2587', '\u2588'}

	maxVal := 0
	for _, v := range data {
		if v > maxVal {
			maxVal = v
		}
	}
	if maxVal == 0 {
		maxVal = 1
	}

	var spark strings.Builder
	for _, v := range data {
		idx := (v * (len(blocks) - 1)) / maxVal
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		spark.WriteRune(blocks[idx])
	}

	labelStyle := lipgloss.NewStyle().Foreground(colorSubtext)
	sparkStyle := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	countStyle := lipgloss.NewStyle().Foreground(colorSubtext)

	total := 0
	for _, v := range data {
		total += v
	}

	return fmt.Sprintf("%s %s %s",
		labelStyle.Render("7-day activity:"),
		sparkStyle.Render(spark.String()),
		countStyle.Render(fmt.Sprintf("(%d total)", total)))
}

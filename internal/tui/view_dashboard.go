package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/lleontor705/cortex/v2/internal/app"
)

// ─── Home / Dashboard ──────────────────────────────────────────────────────

// dashboardAction distinguishes entries that push a screen from the two
// lifecycle entries that open the connect modal or end the session.
type dashboardAction int

const (
	dashboardNavigate dashboardAction = iota
	dashboardConnect
	dashboardQuit
)

// Destinations for the two entries that are not plain screens. They are unique
// sentinels so the duplicate-destination invariant covers every menu entry.
const (
	dashboardConnectDest Screen = -1
	dashboardQuitDest    Screen = -2
)

type dashboardItem struct {
	label  string
	hint   string
	dest   Screen
	action dashboardAction
}

type dashboardSection struct {
	title string
	items []dashboardItem
}

// dashboardMenu is the single source of truth for the Dashboard information
// architecture (REQ-NAV-005, wireframe W1): content screens are grouped under
// MAIN, operations and lifecycle entries under SYSTEM. Every entry has a unique
// destination and a plain-language label, so the menu can never route two
// entries to the same screen or expose internal jargon.
func dashboardMenu() []dashboardSection {
	return []dashboardSection{
		{
			title: "MAIN",
			items: []dashboardItem{
				{label: "Recent observations", hint: "latest captured memories", dest: ScreenRecent},
				{label: "Search memories", hint: "full-text and semantic search", dest: ScreenSearch},
				{label: "Knowledge graph", hint: "related observations and links", dest: ScreenGraph},
				{label: "Memory health", hint: "stale, orphaned and broken links", dest: ScreenHealth},
				{label: "Sessions", hint: "agent session history", dest: ScreenSessions},
				{label: "Archived observations", hint: "restore soft-deleted memories", dest: ScreenArchive},
				{label: "Timeline", hint: "before and after a focused memory", dest: ScreenTimeline},
			},
		},
		{
			title: "SYSTEM",
			items: []dashboardItem{
				{label: "Connect to server...", hint: "hybrid sync or remote proxy", dest: dashboardConnectDest, action: dashboardConnect},
				{label: "Configuration...", hint: "provider, embedding and server settings", dest: ScreenLocalConfig},
				{label: "Setup agent plugin...", hint: "install the Cortex plugin", dest: ScreenSetup},
				{label: "Help", hint: "keys for the current screen", dest: ScreenHelp},
				{label: "Quit", hint: "exit Cortex", dest: dashboardQuitDest, action: dashboardQuit},
			},
		},
	}
}

// flattenDashboardItems returns the menu items in render order, which is also
// the cursor order the key handlers walk.
func flattenDashboardItems() []dashboardItem {
	var items []dashboardItem
	for _, section := range dashboardMenu() {
		items = append(items, section.items...)
	}
	return items
}

// dashboardSyncChipFor derives the honest header chip from a sync snapshot. It
// is the pure core of the dashboard chip so every state can be exercised
// without a live App; the interval is only attached while the loop is running.
func dashboardSyncChipFor(status app.SyncStatus, hasEndpoint bool, interval time.Duration) SyncChip {
	chip := SyncChipFromStatus(status, hasEndpoint)
	if chip.State == SyncRunning {
		chip.Interval = interval
	}
	return chip
}

// dashboardSyncChip reports the real sync state for the Dashboard header
// (REQ-SYNC-003). It never reports running unless the replication loop is
// actually active.
func (m Model) dashboardSyncChip() SyncChip {
	hasEndpoint := false
	var interval time.Duration
	if m.deps != nil && m.deps.Config != nil {
		hasEndpoint = strings.TrimSpace(m.deps.Config.Sync.URL) != ""
		interval = m.deps.Config.Sync.Interval
	}
	return dashboardSyncChipFor(m.authSyncStatus(), hasEndpoint, interval)
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
	lines = append(lines, m.renderLogo())

	// 2. Identity, counts and honest sync chip
	var metaParts []string
	role := strings.ToLower(m.UserRole)
	if role == "" {
		role = "admin"
	}
	metaParts = append(metaParts, fmt.Sprintf("%s (%s)", m.CurrentUser, role))

	if m.Stats != nil {
		metaParts = append(metaParts, fmt.Sprintf("%d observations", m.Stats.TotalObservations))
		metaParts = append(metaParts, fmt.Sprintf("%d sessions", m.Stats.TotalSessions))
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

	lines = append(lines, lipgloss.NewStyle().Foreground(colorSubtext).Render("  "+strings.Join(metaParts, " · ")))
	lines = append(lines, m.renderSyncChip())

	// Onboarding: prompt for a guided Connect while sync is unconfigured
	// (REQ-SYNC-005). It is suppressed while a full empty state already offers
	// Connect and after the user skips it for this launch.
	if prompt := m.viewUnconfiguredSyncPrompt(); prompt != "" {
		lines = append(lines, prompt)
	}

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

	// 5. Menu entries grouped MAIN / SYSTEM, or the onboarding empty state that
	// replaces a raw blank menu when the store holds no observations.
	lines = append(lines, "")
	if kind := m.onboardingEmptyState(); kind != EmptyStateNone {
		lines = append(lines, m.viewOnboardingEmptyState(kind))
	} else {
		cursor := 0
		for _, section := range dashboardMenu() {
			lines = append(lines, lipgloss.NewStyle().Foreground(colorSubtext).Bold(true).Render("  "+section.title))
			for _, item := range section.items {
				marker := "    "
				labelStyle := lipgloss.NewStyle().Foreground(colorText)
				if cursor == m.Cursor {
					marker = "  ▸ "
					labelStyle = lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
				}
				line := marker + labelStyle.Render(item.label)
				if item.hint != "" {
					line += "  " + lipgloss.NewStyle().Foreground(colorSubtext).Render(item.hint)
				}
				lines = append(lines, truncateStr(line, contentWidth))
				cursor++
			}
		}
	}

	// 6. Navigation Footer
	lines = append(lines, "", lipgloss.NewStyle().Foreground(colorSubtext).Render("  ↑/↓ move · enter select · c config · s search · t theme · q quit"))

	body := strings.Join(lines, "\n")
	frame := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPurple).
		Padding(0, 1).
		Render(body)

	return frame
}

// renderSyncChip renders the header chip from the honest status vocabulary.
// Only a running loop is styled as success; every other state stays neutral.
func (m Model) renderSyncChip() string {
	chip := m.dashboardSyncChip()
	style := lipgloss.NewStyle().Foreground(colorSubtext)
	if chip.IsSuccess() {
		style = lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	}
	return style.Render("  [" + chip.Text() + "]")
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

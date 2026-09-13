package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ─── Layout Helpers ─────────────────────────────────────────────────────────

// isCompact returns true when the terminal is too small for full layout.
func (m Model) isCompact() bool {
	return m.Width < 60 || m.Height < 20
}

// ─── View (Main Router & Presentation Coordinator) ──────────────────────────

func (m Model) View() string {
	var content string

	switch m.Screen {
	case ScreenDashboard:
		content = m.viewDashboard()
	case ScreenSearch:
		content = m.viewSearch()
	case ScreenSearchResults:
		content = m.viewSearchResults()
	case ScreenRecent:
		content = m.viewRecent()
	case ScreenObservationDetail:
		content = m.viewObservationDetail()
	case ScreenTimeline:
		content = m.viewTimeline()
	case ScreenSessions:
		content = m.viewSessions()
	case ScreenSessionDetail:
		content = m.viewSessionDetail()
	case ScreenSetup:
		content = m.viewSetup()
	case ScreenGraph:
		content = m.viewGraph()
	case ScreenArchive:
		content = m.viewArchive()
	case ScreenHealth:
		content = m.viewHealth()
	case ScreenEmbeddingConfig:
		content = m.viewEmbeddingConfig()
	case ScreenLocalConfig:
		content = m.viewLocalConfig()
	case ScreenHelp:
		content = m.viewHelp()
	default:
		content = "Unknown screen"
	}

	if m.Screen != ScreenDashboard && !m.isCompact() {
		deckHeader := m.renderDeckHeader()
		if deckHeader != "" {
			content = deckHeader + content
		}
	}

	if m.ErrorMsg != "" {
		content += "\n" + errorStyle.Render("Error: "+m.ErrorMsg)
	}

	// Toast message
	if m.ToastMessage != "" {
		var prefix string
		switch m.ToastType {
		case "warning":
			prefix = lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render("  ! ")
		case "error":
			prefix = lipgloss.NewStyle().Foreground(colorRed).Bold(true).Render("  x ")
		default:
			prefix = lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Render("  v ")
		}
		content += "\n" + prefix + lipgloss.NewStyle().Foreground(colorText).Render(m.ToastMessage)
	}

	// Delete confirmation modal (centered overlay)
	if m.ConfirmDelete {
		title := m.DeleteTargetTitle
		if len(title) > 35 {
			title = title[:35] + "..."
		}
		modalContent := fmt.Sprintf("Delete %q?\n\n  [y] Yes    [n] No", title)
		modal := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorAmber).
			Foreground(colorText).
			Padding(1, 3).
			Width(50).
			Render(modalContent)

		// Overlay on top of content using Place
		content = lipgloss.Place(m.Width-4, m.Height-2,
			lipgloss.Center, lipgloss.Center,
			modal)
	}

	// Command palette overlay
	if m.CmdPaletteOpen {
		content = m.viewCmdPalette()
	}

	// Quick memory creation modal overlay
	if m.NewObsModalOpen {
		content = m.renderNewObsModal()
	}

	// Auth token login modal overlay
	if m.AuthModalOpen {
		content = m.renderAuthModal()
	}

	rendered := appStyle.Render(content)

	// Status bar at the bottom
	rendered += "\n" + m.renderStatusBar()

	return rendered
}

// ─── Status Bar ─────────────────────────────────────────────────────────────

func (m Model) screenName() string {
	switch m.Screen {
	case ScreenDashboard:
		return "Dashboard"
	case ScreenSearch:
		return "Search"
	case ScreenSearchResults:
		return "Search Results"
	case ScreenRecent:
		return "Recent"
	case ScreenObservationDetail:
		return "Detail"
	case ScreenTimeline:
		return "Timeline"
	case ScreenSessions:
		return "Sessions"
	case ScreenSessionDetail:
		return "Session Detail"
	case ScreenSetup:
		return "Setup"
	case ScreenGraph:
		return "Graph"
	case ScreenArchive:
		return "Archive"
	case ScreenHealth:
		return "Health"
	case ScreenEmbeddingConfig:
		return "Embedding Settings"
	case ScreenLocalConfig:
		return "Local Settings"
	case ScreenHelp:
		return "Help"
	default:
		return "Cortex"
	}
}

func (m Model) renderStatusBar() string {
	compact := m.isCompact()

	var parts []string

	// Screen name
	nameStyle := lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Background(activePalette.PanelBg)
	parts = append(parts, nameStyle.Render(m.screenName()))

	// User Identity Badge
	roleLabel := strings.ToUpper(m.UserRole)
	if roleLabel == "" {
		roleLabel = "MEMBER"
	}
	userBadge := fmt.Sprintf("👤 %s [%s]", m.CurrentUser, roleLabel)
	parts = append(parts, lipgloss.NewStyle().Foreground(colorCyan).Render(userBadge))

	// Theme Badge
	themeBadge := "🌙 Dark"
	if !m.IsDarkTheme {
		themeBadge = "☀️ Light"
	}
	parts = append(parts, lipgloss.NewStyle().Foreground(colorAmber).Render(themeBadge))

	// List position (from bubbles/list components)
	switch m.Screen {
	case ScreenRecent:
		if len(m.RecentList.Items()) > 0 {
			parts = append(parts, fmt.Sprintf("[%d/%d]", m.RecentList.Index()+1, len(m.RecentList.Items())))
		}
	case ScreenSearchResults:
		if len(m.SearchListModel.Items()) > 0 {
			parts = append(parts, fmt.Sprintf("[%d/%d]", m.SearchListModel.Index()+1, len(m.SearchListModel.Items())))
		}
	case ScreenSessions:
		if len(m.SessionListModel.Items()) > 0 {
			parts = append(parts, fmt.Sprintf("[%d/%d]", m.SessionListModel.Index()+1, len(m.SessionListModel.Items())))
		}
	case ScreenGraph:
		if len(m.GraphListModel.Items()) > 0 {
			parts = append(parts, fmt.Sprintf("[%d/%d]", m.GraphListModel.Index()+1, len(m.GraphListModel.Items())))
		}
	case ScreenArchive:
		if len(m.ArchiveList.Items()) > 0 {
			parts = append(parts, fmt.Sprintf("[%d/%d]", m.ArchiveList.Index()+1, len(m.ArchiveList.Items())))
		}
	}

	if compact {
		// Compact mode: just screen name + position
		barContent := strings.Join(parts, "  |  ")
		width := m.Width
		if width < 20 {
			width = 80
		}
		return statusBarStyle.Width(width).Render(barContent)
	}

	// Full mode: add observation count, version, help hint
	if m.Stats != nil {
		parts = append(parts, fmt.Sprintf("%d obs", m.Stats.TotalObservations))
	}

	if m.FilterProject != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorPurple).Bold(true).Render("prj: "+m.FilterProject))
	}

	if m.PreviewVisible {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorTeal).Render("preview: on"))
	}

	if m.UploadToCortex {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorGreen).Render("sync: on"))
	} else {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorSubtext).Render("local only"))
	}

	if m.Version != "" {
		parts = append(parts, m.Version)
	}

	if m.Screen == ScreenDashboard {
		parts = append(parts, "j/k nav • enter select • / search • r recent • g graph • c config • ? help • q quit")
	} else {
		parts = append(parts, "n new • t theme • L login • ? help • Esc back")
	}

	barContent := strings.Join(parts, "  |  ")
	width := m.Width
	if width < 20 {
		width = 80
	}
	return statusBarStyle.Width(width).Render(barContent)
}

// ─── Shared Renderers ───────────────────────────────────────────────────────

func (m Model) renderObservationListItem(index int, id int64, obsType, title, content string, createdAt time.Time, project string, overrideStyle *lipgloss.Style) string {
	cursor := "  "
	style := listItemStyle
	if index == m.Cursor {
		cursor = "▸ "
		style = listSelectedStyle
	}
	if overrideStyle != nil {
		style = *overrideStyle
	}

	proj := ""
	if project != "" {
		proj = "  " + projectStyle.Render(project)
	}

	// Use type-specific color for the badge
	tColor := typeColor(obsType)
	typeBadge := lipgloss.NewStyle().Foreground(tColor).Bold(true).Render(fmt.Sprintf("[%-12s]", obsType))

	line := fmt.Sprintf("%s%s %s %s%s  %s\n",
		cursor,
		idStyle.Render(fmt.Sprintf("#%-5d", id)),
		typeBadge,
		style.Render(truncateStr(title, 50)),
		proj,
		timestampStyle.Render(formatTime(createdAt)))

	preview := truncateStr(content, 80)
	if preview != "" {
		line += contentPreviewStyle.Render(preview) + "\n"
	}

	return line
}

// ─── Formatting Helpers ─────────────────────────────────────────────────────

// formatTime formats a time.Time for display.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func truncateStr(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

func entityIcon(entityType string) string {
	switch entityType {
	case "file":
		return "F"
	case "url":
		return "U"
	case "package":
		return "P"
	case "symbol":
		return "S"
	default:
		return "*"
	}
}

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderDeckHeader renders the persistent Command Deck navigation bar across non-dashboard screens.
func (m Model) renderDeckHeader() string {
	if m.isCompact() {
		return ""
	}

	var b strings.Builder

	// Top row: Brand + Version + Identity
	brand := lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("CORTEX")
	ver := lipgloss.NewStyle().Foreground(colorSubtext).Render(" " + m.Version)

	role := strings.ToLower(m.UserRole)
	if role == "" {
		role = "member"
	}
	user := fmt.Sprintf("%s (%s)", m.CurrentUser, role)
	syncLabel := "local"
	if m.UploadToCortex {
		syncLabel = "cloud sync"
	}
	themeLabel := "dark"
	if !m.IsDarkTheme {
		themeLabel = "light"
	}
	meta := lipgloss.NewStyle().Foreground(colorSubtext).Render(fmt.Sprintf("  •  %s  •  %s  •  %s", user, syncLabel, themeLabel))

	b.WriteString("  " + brand + ver + meta + "\n\n")

	// Workspace Tabs row
	wsTabs := []struct {
		id    int
		label string
	}{
		{0, "[1] Vault"},
		{1, "[2] Graph"},
		{2, "[3] Health"},
		{3, "[4] Settings"},
	}

	currentWs := m.ActiveWorkspace()
	var tabsRow []string
	for _, tab := range wsTabs {
		if tab.id == currentWs {
			tabsRow = append(tabsRow, deckTabActiveStyle.Render(tab.label))
		} else {
			tabsRow = append(tabsRow, deckTabInactiveStyle.Render(tab.label))
		}
	}

	b.WriteString("  " + strings.Join(tabsRow, " ") + "\n")
	sepWidth := 80
	if m.Width > 84 {
		sepWidth = m.Width - 6
	}
	sep := strings.Repeat("─", sepWidth)
	b.WriteString(lipgloss.NewStyle().Foreground(colorOverlay).Render("  "+sep) + "\n\n")

	return b.String()
}

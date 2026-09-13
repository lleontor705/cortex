package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// ─── Command Palette ──────────────────────────────────────────────────────

func (m Model) viewCmdPalette() string {
	var b strings.Builder

	filtered := m.filteredCommands()

	b.WriteString("  " + m.CmdPaletteInput.View())
	b.WriteString("\n\n")

	for i, cmd := range filtered {
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(colorText)
		if i == m.CmdPaletteCursor {
			cursor = "\u25b8 "
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("#16161e")).Background(colorCyan).Bold(true)
		}

		shortcut := ""
		if cmd.shortcut != "" {
			shortcut = lipgloss.NewStyle().Foreground(colorSubtext).Render("  " + cmd.shortcut)
		}

		b.WriteString(cursor + style.Render(cmd.name) + shortcut + "\n")
	}

	modalContent := b.String()
	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorCyan).
		Padding(1, 2).
		Width(50).
		Render(modalContent)

	return lipgloss.Place(m.Width-4, m.Height-2,
		lipgloss.Center, lipgloss.Center,
		modal)
}

// ─── Quick Create Observation Modal ────────────────────────────────────────

func (m Model) renderNewObsModal() string {
	var panel strings.Builder
	titleStyle := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(colorPurple).Width(12)
	dim := lipgloss.NewStyle().Foreground(colorSubtext)

	panel.WriteString(titleStyle.Render("✦ Quick Create Memory") + "\n")
	panel.WriteString(dim.Render("Create and persist a new observation directly into Cortex memory.") + "\n\n")

	f := func(field int, name string, input textinput.Model) {
		cursor := "  "
		if m.NewObsFocusField == field {
			cursor = listSelectedStyle.Render("▸ ")
		}
		panel.WriteString(cursor + labelStyle.Render(name) + input.View() + "\n")
	}

	f(0, "Title:", m.NewObsTitleInput)
	f(1, "Content:", m.NewObsContentInput)
	f(2, "Type:", m.NewObsTypeInput)
	f(3, "Project:", m.NewObsProjectInput)

	panel.WriteString("\n  ")
	btnStyle := lipgloss.NewStyle().Padding(0, 2)
	if m.NewObsFocusField == 4 {
		btnStyle = btnStyle.Background(colorCyan).Foreground(lipgloss.Color("#16161e")).Bold(true)
	} else {
		btnStyle = btnStyle.Border(lipgloss.NormalBorder()).BorderForeground(colorOverlay)
	}
	panel.WriteString(btnStyle.Render("Save Observation [Enter]"))
	panel.WriteString("   " + dim.Render("[Tab] Next Field  •  [Esc] Cancel  •  [Ctrl+S] Save"))

	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorCyan).
		Padding(1, 2).
		Width(68).
		Render(panel.String())

	width := m.Width - 4
	if width < 20 {
		width = 72
	}
	height := m.Height - 2
	if height < 10 {
		height = 20
	}

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

// ─── Auth Token Login Modal ────────────────────────────────────────────────

func (m Model) renderAuthModal() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("🌐 Connect to Cortex Server & Authenticate"))
	b.WriteString("\n\n")
	b.WriteString(detailContentStyle.Render("Configure your remote Cortex Server endpoint and bearer token to synchronize memories and authenticate."))
	b.WriteString("\n\n")

	marker0 := "  "
	marker1 := "  "
	marker2 := "  "
	switch m.AuthFocusField {
	case 0:
		marker0 = listSelectedStyle.Render("▸ ")
	case 1:
		marker1 = listSelectedStyle.Render("▸ ")
	default:
		marker2 = listSelectedStyle.Render("▸ ")
	}

	b.WriteString(marker0 + detailLabelStyle.Render("Server URL:   ") + m.AuthServerURLInput.View())
	b.WriteString("\n")
	b.WriteString(marker1 + detailLabelStyle.Render("Bearer Token: ") + m.AuthTokenInput.View())
	b.WriteString("\n\n")

	modeStr := "[●] Hybrid: Local-First + Sync [Recommended]   [ ] Remote MCP Proxy"
	if !m.AuthModeHybrid {
		modeStr = "[ ] Hybrid: Local-First + Sync                 [●] Remote MCP Proxy"
	}
	b.WriteString(marker2 + detailLabelStyle.Render("Mode:         ") + lipgloss.NewStyle().Foreground(colorCyan).Render(modeStr))
	b.WriteString("\n")
	if m.AuthModeHybrid {
		b.WriteString("              " + helpStyle.Render("Fast local SQLite + Zero-CGO AST + background cloud sync."))
	} else {
		b.WriteString("              " + helpStyle.Render("Direct proxy to server (AST tools run in cloud container)."))
	}
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("Tab/↑↓: Navigate • Space: Toggle Mode • Enter: Save to YAML • Esc: Cancel"))

	modal := lipgloss.NewStyle().
		BorderStyle(lipgloss.DoubleBorder()).
		BorderForeground(colorCyan).
		Background(activePalette.PanelBg).
		Foreground(colorText).
		Padding(1, 3).
		Width(78).
		Render(b.String())

	return lipgloss.Place(m.Width-4, m.Height-2, lipgloss.Center, lipgloss.Center, modal)
}

// ─── Keyboard Reference Help View ──────────────────────────────────────────

func (m Model) viewHelp() string {
	var b strings.Builder

	b.WriteString(headerStyle.Render("  Cortex TUI — Keyboard Reference"))
	b.WriteString("\n\n")

	section := func(title string, bindings [][2]string) {
		b.WriteString(sectionHeadingStyle.Render("  " + title))
		b.WriteString("\n")
		keyStyle := lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Width(16)
		descStyle := lipgloss.NewStyle().Foreground(colorText)
		for _, bind := range bindings {
			fmt.Fprintf(&b, "  %s %s\n", keyStyle.Render(bind[0]), descStyle.Render(bind[1]))
		}
		b.WriteString("\n")
	}

	section("Global", [][2]string{
		{"?", "Toggle this help screen"},
		{"q / esc", "Go back / quit"},
		{"ctrl+c", "Force quit"},
	})

	section("Navigation", [][2]string{
		{"j / down", "Move cursor down"},
		{"k / up", "Move cursor up"},
		{"G", "Jump to last item"},
		{"gg", "Jump to first item"},
		{"enter", "Select / open"},
		{"s / /", "Open search"},
		{"f", "Cycle project filter"},
	})

	section("List Screens (Recent, Search Results)", [][2]string{
		{"enter", "View observation detail"},
		{"t", "View timeline"},
		{"d", "Delete observation (with confirm)"},
		{"f", "Cycle project filter"},
	})

	section("Detail View", [][2]string{
		{"j / k", "Scroll content"},
		{"t", "View timeline"},
		{"g", "View graph connections"},
		{"s", "Jump to session"},
		{"d", "Delete observation (with confirm)"},
	})

	section("Graph", [][2]string{
		{"enter", "View observation detail"},
		{"r", "Re-root graph on selection"},
	})

	section("Health", [][2]string{
		{"j / k", "Scroll dashboard"},
		{"tab", "Cycle section"},
		{"enter", "Expand/collapse section"},
	})

	section("Archive", [][2]string{
		{"enter", "View observation detail"},
		{"u", "Unarchive (restore) observation"},
		{"d", "Delete observation (with confirm)"},
		{"f", "Cycle project filter"},
	})

	section("Embedding Settings", [][2]string{
		{"h / l", "Cycle provider"},
		{"space", "Toggle checkbox"},
		{"enter", "Edit model name / save"},
		{"r", "Reload config from disk"},
		{"x", "Reindex all embeddings (after save)"},
	})

	b.WriteString(helpStyle.Render("  Press esc / q / ? to close"))

	return b.String()
}

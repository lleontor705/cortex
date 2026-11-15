package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// viewConnect renders the guided Connect wizard (design.md W5/W6/W7). It is the
// single writable surface for the server URL once the entry points route here;
// the Auth modal is retained only until that rewiring lands in 023.
func (m Model) viewConnect() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("  Connect to server"))
	b.WriteString("   " + lipgloss.NewStyle().Foreground(colorSubtext).Render(m.ConnectStep.Label()) + "\n")
	b.WriteString("  " + lipgloss.NewStyle().Foreground(colorSubtext).Render(strings.Repeat("─", 62)) + "\n\n")

	switch m.ConnectStep {
	case ConnectStepURL:
		b.WriteString(m.viewConnectURLStep())
	case ConnectStepToken:
		b.WriteString(m.viewConnectTokenStep())
	case ConnectStepTest:
		b.WriteString(m.viewConnectTestStep())
	}

	if m.ConnectError != "" {
		b.WriteString("\n  " + errorStyle.Render("Error: "+m.ConnectError))
	}
	if m.ConnectSummary != "" {
		b.WriteString("\n\n  " + m.connectSummaryStyle().Render(m.ConnectSummary))
	}

	b.WriteString(helpStyle.Render("\n\n  " + m.connectFooter()))
	b.WriteString("\n  " + lipgloss.NewStyle().Foreground(colorSubtext).Render("Dashboard / Connect · "+m.ConnectStep.Label()))
	return b.String()
}

func (m Model) viewConnectURLStep() string {
	label := lipgloss.NewStyle().Foreground(colorText)
	value := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colorSubtext)

	var b strings.Builder
	b.WriteString("  " + label.Render("Server URL") + "\n")
	b.WriteString("  " + inputValue(m.ConnectURL, value, dim, "https://your-cortex.up.railway.app") + "\n\n")
	b.WriteString("  " + label.Render("Mode after connect:") + "\n")
	b.WriteString("    " + connectModeMarker(!m.ConnectProxyMode, m.ConnectFocus == 1) + " " +
		value.Render("Hybrid sync") + " " + dim.Render("— local DB + background replication") + " " +
		lipgloss.NewStyle().Foreground(colorGreen).Render("[recommended]") + "\n")
	b.WriteString("    " + connectModeMarker(m.ConnectProxyMode, m.ConnectFocus == 1) + " " +
		value.Render("Remote MCP proxy") + " " + dim.Render("— no local replication") + "\n")

	note := dim.Render("        note: proxy mode disables background sync (stated, not silent)")
	if m.ConnectProxyMode {
		note = lipgloss.NewStyle().Foreground(colorAmber).Bold(true).
			Render("        proxy mode disables background sync — local replication will be turned off")
	}
	b.WriteString("    " + note + "\n")
	return b.String()
}

func (m Model) viewConnectTokenStep() string {
	label := lipgloss.NewStyle().Foreground(colorText)
	value := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colorSubtext)

	var b strings.Builder
	b.WriteString("  " + label.Render("Bearer token") + "\n")
	b.WriteString("  " + inputValue(m.ConnectToken, value, dim, m.connectTokenFallback()) + "\n\n")
	b.WriteString("  " + dim.Render("Stored in config as http.token.") + "\n")
	b.WriteString("  " + dim.Render("A token set via `cortex auth login` is preserved if you leave this empty.") + "\n")
	return b.String()
}

// connectTokenFallback labels an untouched token field so the user can see an
// existing credential will be preserved rather than erased (REQ-CFG-004).
func (m Model) connectTokenFallback() string {
	if m.deps != nil && m.deps.Config != nil && strings.TrimSpace(m.deps.Config.HTTP.Token) != "" {
		return "•••••••• (existing token preserved — leave empty to keep)"
	}
	return "ctx_..."
}

func (m Model) viewConnectTestStep() string {
	dim := lipgloss.NewStyle().Foreground(colorSubtext)
	var b strings.Builder
	b.WriteString("  " + lipgloss.NewStyle().Foreground(colorText).
		Render("Testing "+strings.TrimSpace(m.ConnectURL.Value())+" ...") + "\n\n")
	if m.ConnectResult == nil {
		return b.String()
	}
	for _, check := range m.ConnectResult.Checks {
		mark, style := "✓", lipgloss.NewStyle().Foreground(colorGreen)
		if !check.OK {
			mark, style = "✗", lipgloss.NewStyle().Foreground(colorRed)
		}
		line := "  " + style.Render(mark+" "+check.Name)
		if check.Detail != "" {
			line += " " + dim.Render(check.Detail)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (m Model) connectSummaryStyle() lipgloss.Style {
	if m.ConnectCommitted {
		return lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	}
	return lipgloss.NewStyle().Foreground(colorAmber).Bold(true)
}

func (m Model) connectFooter() string {
	switch m.ConnectStep {
	case ConnectStepURL:
		return "[enter] Continue    [esc] Cancel"
	case ConnectStepToken:
		return "[enter] Test connection    [esc] Back"
	default:
		if m.ConnectCommitted {
			return "[enter] Done    [esc] Back"
		}
		return "[enter] Save & connect    [esc] Back"
	}
}

func connectModeMarker(selected, focused bool) string {
	mark := "( )"
	if selected {
		mark = "(•)"
	}
	if focused {
		return lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render("▸" + mark)
	}
	return " " + mark
}

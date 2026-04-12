package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/lleontor705/cortex/internal/domain"
)

// ─── Logo ───────────────────────────────────────────────────────────────────

func (m Model) renderLogo() string {
	// Compact logo for small terminals (width < 60 or height < 25)
	if m.Width > 0 && (m.Width < 60 || m.Height < 25) {
		compactStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorOverlay).
			Padding(0, 1).
			MarginBottom(1)
		title := lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render("CORTEX")
		tagline := lipgloss.NewStyle().Foreground(colorSubtext).Italic(true).Render(" " + m.Version + " — your brain never forgets")
		return compactStyle.Render(title + tagline) + "\n"
	}

	logoText := []string{
		` ██████  ██████  ██████  ████████ ███████ ██   ██ `,
		`██      ██    ██ ██   ██    ██    ██       ██ ██  `,
		`██      ██    ██ ██████     ██    █████     ███   `,
		`██      ██    ██ ██   ██    ██    ██       ██ ██  `,
		` ██████  ██████  ██   ██    ██    ███████ ██   ██ `,
	}

	frameStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorOverlay).
		Padding(0, 1).
		MarginBottom(1)

	colors := []lipgloss.Color{
		colorPurple, // Top
		colorBlue,   // Middle-top
		colorCyan,   // Middle
		colorTeal,   // Middle-bottom
		colorGreen,  // Bottom
	}

	accentStyle := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	taglineStyle := lipgloss.NewStyle().Foreground(colorSubtext).Italic(true)

	var b strings.Builder

	b.WriteString(accentStyle.Render(" NEURAL LINK ACTIVE ") + strings.Repeat(" ", 28) + accentStyle.Render(" MEM: OK ") + "\n\n")

	for i, line := range logoText {
		b.WriteString(" " + lipgloss.NewStyle().Foreground(colors[i]).Bold(true).Render(line) + "\n")
	}
	b.WriteString("\n")

	b.WriteString(taglineStyle.Render(" > cortex " + m.Version + " — your brain never forgets"))

	return frameStyle.Render(b.String()) + "\n"
}

// ─── View (main router) ────────────────────────────────────────────────────

// isCompact returns true when the terminal is too small for full layout.
func (m Model) isCompact() bool {
	return m.Width < 60 || m.Height < 20
}

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
	case ScreenHelp:
		content = m.viewHelp()
	default:
		content = "Unknown screen"
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

	rendered := appStyle.Render(content)

	// Status bar at the bottom
	rendered += "\n" + m.renderStatusBar()

	return rendered
}

// ─── Dashboard ──────────────────────────────────────────────────────────────

func (m Model) viewDashboard() string {
	var b strings.Builder

	b.WriteString(m.renderLogo())
	b.WriteString("\n")

	// Update notification
	if m.UpdateResult != nil {
		msg := fmt.Sprintf("Update available: %s — %s", m.UpdateResult.Latest, m.UpdateResult.UpdateURL)
		b.WriteString(updateBannerStyle.Render(msg))
		b.WriteString("\n\n")
	}

	// Stats card
	if m.Stats != nil {
		statsContent := fmt.Sprintf(
			"%s %s\n%s %s\n%s %s\n%s %s",
			statNumberStyle.Render(fmt.Sprintf("%d", m.Stats.TotalSessions)),
			statLabelStyle.Render("sessions"),
			statNumberStyle.Render(fmt.Sprintf("%d", m.Stats.TotalObservations)),
			statLabelStyle.Render("observations"),
			statNumberStyle.Render(fmt.Sprintf("%d", m.Stats.TotalEdges)),
			statLabelStyle.Render("knowledge links"),
			statNumberStyle.Render(fmt.Sprintf("%d", len(m.Stats.Projects))),
			statLabelStyle.Render("projects"),
		)
		b.WriteString(statCardStyle.Render(statsContent))
		b.WriteString("\n")

		if len(m.Stats.Projects) > 0 {
			b.WriteString(titleStyle.Render("  Projects"))
			b.WriteString("\n")

			limit := 5
			for i, p := range m.Stats.Projects {
				if i >= limit {
					break
				}
				b.WriteString(listItemStyle.Render("• " + p))
				b.WriteString("\n")
			}

			if len(m.Stats.Projects) > limit {
				remaining := len(m.Stats.Projects) - limit
				fmt.Fprintf(&b, "    %s\n", timestampStyle.Render(fmt.Sprintf("...and %d more projects", remaining)))
			}
			b.WriteString("\n")
		}
	} else {
		b.WriteString(statCardStyle.Render("Loading stats..."))
		b.WriteString("\n")
	}

	// 7-day activity sparkline
	if len(m.ActivityData) > 0 {
		sparkline := renderSparkline(m.ActivityData)
		b.WriteString(sparkline)
		b.WriteString("\n")
	}

	// Menu
	b.WriteString(titleStyle.Render("  Actions"))
	b.WriteString("\n")

	for i, item := range dashboardMenuItems {
		if i == m.Cursor {
			b.WriteString(menuSelectedStyle.Render("▸ " + item))
		} else {
			b.WriteString(menuItemStyle.Render("  " + item))
		}
		b.WriteString("\n")
	}

	if m.isCompact() {
		b.WriteString(helpStyle.Render("\n  j/k • enter • s search • q quit"))
	} else {
		b.WriteString(helpStyle.Render("\n  j/k navigate • enter select • s search • ? help • q quit"))
	}

	return b.String()
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
	sparkStyle := lipgloss.NewStyle().Foreground(colorCyan)
	countStyle := lipgloss.NewStyle().Foreground(colorSubtext)

	// Sum total
	total := 0
	for _, v := range data {
		total += v
	}

	return fmt.Sprintf("  %s %s %s",
		labelStyle.Render("7-day activity:"),
		sparkStyle.Render(spark.String()),
		countStyle.Render(fmt.Sprintf("(%d total)", total)))
}

// ─── Search ─────────────────────────────────────────────────────────────────

func (m Model) viewSearch() string {
	var b strings.Builder

	b.WriteString(headerStyle.Render("  Search Memories"))
	b.WriteString("\n\n")

	b.WriteString(searchInputStyle.Render(m.SearchInput.View()))
	b.WriteString("\n\n")

	b.WriteString(helpStyle.Render("  Type a query and press enter • esc go back"))

	return b.String()
}

// ─── Search Results ─────────────────────────────────────────────────────────

func (m Model) viewSearchResults() string {
	var b strings.Builder

	resultCount := len(m.SearchResults)
	header := fmt.Sprintf("  Search: %q — %d result", m.SearchQuery, resultCount)
	if resultCount != 1 {
		header += "s"
	}
	if m.FilterProject != "" {
		header += fmt.Sprintf(" (project: %s)", m.FilterProject)
	}
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	if resultCount == 0 {
		b.WriteString(noResultsStyle.Render("No memories found. Try a different query."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  / new search • esc back"))
		return b.String()
	}

	if m.PreviewVisible && m.Width >= 100 {
		listWidth := m.Width * 2 / 5
		previewWidth := m.Width - listWidth - 5

		listContent := m.SearchListModel.View()
		listPane := lipgloss.NewStyle().
			Width(listWidth).
			Height(m.Height - 8).
			Render(listContent)

		previewContent := m.PreviewViewport.View()
		scrollPct := fmt.Sprintf(" %3.f%% ", m.PreviewViewport.ScrollPercent()*100)
		previewPane := lipgloss.NewStyle().
			Width(previewWidth).
			Height(m.Height - 8).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(colorOverlay).
			PaddingLeft(1).
			Render(previewContent + "\n" + timestampStyle.Render(scrollPct))

		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, listPane, previewPane))
	} else {
		b.WriteString(m.SearchListModel.View())
	}

	helpText := "  j/k navigate • enter detail • t timeline • d delete • f filter"
	if m.Width >= 100 {
		helpText += " • p preview"
	}
	helpText += " • / search • esc back"
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

	if m.PreviewVisible && m.Width >= 100 {
		listWidth := m.Width * 2 / 5
		previewWidth := m.Width - listWidth - 5

		listContent := m.RecentList.View()
		listPane := lipgloss.NewStyle().
			Width(listWidth).
			Height(m.Height - 8).
			Render(listContent)

		previewContent := m.PreviewViewport.View()
		scrollPct := fmt.Sprintf(" %3.f%% ", m.PreviewViewport.ScrollPercent()*100)
		previewPane := lipgloss.NewStyle().
			Width(previewWidth).
			Height(m.Height - 8).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(colorOverlay).
			PaddingLeft(1).
			Render(previewContent + "\n" + timestampStyle.Render(scrollPct))

		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, listPane, previewPane))
	} else {
		b.WriteString(m.RecentList.View())
	}

	helpText := "  j/k navigate • enter detail • t timeline • d delete • f filter"
	if m.Width >= 100 {
		helpText += " • p preview"
	}
	helpText += " • esc back"
	b.WriteString(helpStyle.Render("\n" + helpText))

	return b.String()
}

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

	b.WriteString(helpStyle.Render("  j/k scroll • t timeline • g graph • s session • d delete • esc back"))

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

	b.WriteString(helpStyle.Render("\n  j/k scroll • esc back"))

	return b.String()
}

// ─── Sessions ───────────────────────────────────────────────────────────────

func (m Model) viewSessions() string {
	var b strings.Builder

	count := len(m.Sessions)
	header := fmt.Sprintf("  Sessions — %d total", count)
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	if count == 0 {
		b.WriteString(noResultsStyle.Render("No sessions yet."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  esc back"))
		return b.String()
	}

	b.WriteString(m.SessionListModel.View())

	b.WriteString(helpStyle.Render("\n  j/k navigate • enter view session • esc back"))

	return b.String()
}

// ─── Session Detail ─────────────────────────────────────────────────────────

func (m Model) viewSessionDetail() string {
	var b strings.Builder

	if m.SelectedSessionIdx >= len(m.Sessions) {
		b.WriteString(headerStyle.Render("  Session Detail"))
		b.WriteString("\n")
		b.WriteString(noResultsStyle.Render("Session not found."))
		return b.String()
	}

	sess := m.Sessions[m.SelectedSessionIdx]
	header := fmt.Sprintf("  Session: %s — %s", sess.Session.Project, formatTime(sess.Session.StartedAt))
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	// Session info table
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(colorOverlay)).
		Headers("Field", "Value").
		Row("Project", sess.Session.Project).
		Row("Started", formatTime(sess.Session.StartedAt)).
		Row("Observations", fmt.Sprintf("%d", sess.ObservationCount)).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Padding(0, 1)
			}
			if col == 0 {
				return lipgloss.NewStyle().Foreground(colorPurple).Padding(0, 1)
			}
			return lipgloss.NewStyle().Foreground(colorText).Padding(0, 1)
		})

	if sess.Session.Summary != "" {
		t = t.Row("Summary", truncateStr(sess.Session.Summary, 60))
	}

	b.WriteString(t.Render())
	b.WriteString("\n\n")

	count := len(m.SessionObservations)
	b.WriteString(sectionHeadingStyle.Render(fmt.Sprintf("  Observations (%d)", count)))
	b.WriteString("\n")

	if count == 0 {
		b.WriteString(noResultsStyle.Render("No observations in this session."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  esc back"))
		return b.String()
	}

	visibleItems := (m.Height - 12) / 2
	if visibleItems < 3 {
		visibleItems = 3
	}

	end := m.SessionDetailScroll + visibleItems
	if end > count {
		end = count
	}

	for i := m.SessionDetailScroll; i < end; i++ {
		o := m.SessionObservations[i]
		b.WriteString(m.renderObservationListItem(i, o.ID, o.Type, o.Title, o.Content, o.CreatedAt, o.Project, nil))
	}

	if count > visibleItems {
		fmt.Fprintf(&b, "\n  %s",
			timestampStyle.Render(fmt.Sprintf("showing %d-%d of %d", m.SessionDetailScroll+1, end, count)))
	}

	b.WriteString(helpStyle.Render("\n  j/k navigate • enter detail • t timeline • esc back"))

	return b.String()
}

// ─── Setup ──────────────────────────────────────────────────────────────────

func (m Model) viewSetup() string {
	var b strings.Builder

	b.WriteString(headerStyle.Render("  Setup — Install Agent Plugin"))
	b.WriteString("\n")

	// Show spinner while installing
	if m.SetupInstalling {
		b.WriteString("\n")
		fmt.Fprintf(&b, "  %s Installing %s plugin...\n",
			m.SetupSpinner.View(),
			lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render(m.SetupInstallingName))
		b.WriteString("\n")
		return b.String()
	}

	// Allowlist prompt (after successful claude-code install)
	if m.SetupAllowlistPrompt && m.SetupResult != nil {
		successMsg := fmt.Sprintf("Installed %s plugin", m.SetupResult.Agent)
		fmt.Fprintf(&b, "\n  %s %s\n\n",
			lipgloss.NewStyle().Bold(true).Foreground(colorGreen).Render("✓"),
			lipgloss.NewStyle().Bold(true).Foreground(colorGreen).Render(successMsg))

		b.WriteString(sectionHeadingStyle.Render("  Permissions Allowlist"))
		b.WriteString("\n\n")
		b.WriteString(detailContentStyle.Render("  Add cortex tools to ~/.claude/settings.json allowlist?"))
		b.WriteString("\n")
		b.WriteString(timestampStyle.Render("  This prevents Claude Code from asking permission on every tool call."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  [y] Yes  [n] No"))
		return b.String()
	}

	// Post-install result
	if m.SetupDone {
		if m.SetupError != "" {
			b.WriteString(errorStyle.Render("  ✗ Installation failed: " + m.SetupError))
			b.WriteString("\n\n")
		} else if m.SetupResult != nil {
			successMsg := fmt.Sprintf("Installed %s plugin", m.SetupResult.Agent)
			if m.SetupResult.Files > 0 {
				successMsg += fmt.Sprintf(" (%d files)", m.SetupResult.Files)
			}
			fmt.Fprintf(&b, "  %s %s\n",
				lipgloss.NewStyle().Bold(true).Foreground(colorGreen).Render("✓"),
				lipgloss.NewStyle().Bold(true).Foreground(colorGreen).Render(successMsg))
			fmt.Fprintf(&b, "  %s %s\n\n",
				detailLabelStyle.Render("Location:"),
				projectStyle.Render(m.SetupResult.Destination))

			// Agent-specific post-install instructions
			b.WriteString(sectionHeadingStyle.Render("  Next Steps"))
			b.WriteString("\n")

			switch m.SetupResult.Agent {
			case "claude-code":
				if m.SetupAllowlistApplied {
					fmt.Fprintf(&b, "  %s %s\n",
						lipgloss.NewStyle().Bold(true).Foreground(colorGreen).Render("✓"),
						detailContentStyle.Render("Cortex tools added to allowlist"))
				} else if m.SetupAllowlistError != "" {
					fmt.Fprintf(&b, "  %s %s\n",
						lipgloss.NewStyle().Bold(true).Foreground(colorRed).Render("✗"),
						detailContentStyle.Render("Allowlist update failed: "+m.SetupAllowlistError))
					b.WriteString(detailContentStyle.Render("  Add manually to permissions.allow in ~/.claude/settings.json"))
					b.WriteString("\n")
				}
				b.WriteString(detailContentStyle.Render("1. Restart Claude Code — the plugin is active immediately"))
				b.WriteString("\n")
				b.WriteString(detailContentStyle.Render("2. Verify with: claude plugin list"))
				b.WriteString("\n")
			case "opencode":
				b.WriteString(detailContentStyle.Render("1. Restart OpenCode"))
				b.WriteString("\n")
				b.WriteString(detailContentStyle.Render("2. Plugin is auto-loaded from ~/.config/opencode/plugins/"))
				b.WriteString("\n")
				b.WriteString(detailContentStyle.Render("3. Make sure 'cortex' is in your MCP config"))
				b.WriteString("\n")
			default:
				b.WriteString(detailContentStyle.Render("1. Restart your agent to activate the plugin"))
				b.WriteString("\n")
				b.WriteString(detailContentStyle.Render("2. Verify with: cortex mcp --tools=agent"))
				b.WriteString("\n")
			}
		}

		b.WriteString(helpStyle.Render("\n  enter/esc back to dashboard"))
		return b.String()
	}

	// Agent selection
	b.WriteString("\n")
	b.WriteString(titleStyle.Render("  Select an agent to set up"))
	b.WriteString("\n\n")

	for i, agent := range m.SetupAgents {
		if i == m.Cursor {
			b.WriteString(menuSelectedStyle.Render("▸ " + agent.Description))
		} else {
			b.WriteString(menuItemStyle.Render("  " + agent.Description))
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "      %s %s\n\n",
			detailLabelStyle.Render("Install to:"),
			timestampStyle.Render(agent.InstallDir))
	}

	b.WriteString(helpStyle.Render("\n  j/k navigate • enter install • esc back"))

	return b.String()
}

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

// ─── Archive (Cortex-exclusive) ─────────────────────────────────────────────

func (m Model) viewArchive() string {
	var b strings.Builder

	count := len(m.ArchivedObservations)
	header := fmt.Sprintf("  Archived Observations — %d total", count)
	if m.FilterProject != "" {
		header += fmt.Sprintf(" (project: %s)", m.FilterProject)
	}
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	if count == 0 {
		b.WriteString(noResultsStyle.Render("No archived observations."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  esc back"))
		return b.String()
	}

	b.WriteString(m.ArchiveList.View())

	b.WriteString(helpStyle.Render("\n  j/k navigate • enter detail • u unarchive • d delete • f filter • esc back"))

	return b.String()
}

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
			if i >= 8 {
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
	nameStyle := lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Background(lipgloss.Color("#1a1b2e"))
	parts = append(parts, nameStyle.Render(m.screenName()))

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

	if m.Version != "" {
		parts = append(parts, m.Version)
	}

	parts = append(parts, "? help")

	barContent := strings.Join(parts, "  |  ")
	width := m.Width
	if width < 20 {
		width = 80
	}
	return statusBarStyle.Width(width).Render(barContent)
}

// ─── Help Screen ────────────────────────────────────────────────────────────

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

func (m Model) viewHelp() string {
	var b strings.Builder

	b.WriteString(headerStyle.Render("  Cortex TUI — Keyboard Reference"))
	b.WriteString("\n\n")

	section := func(title string, bindings [][2]string) {
		b.WriteString(sectionHeadingStyle.Render("  "+title))
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

// ─── Helpers ────────────────────────────────────────────────────────────────

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

// ─── Embedding Config ──────────────────────────────────────────────────────

func (m Model) viewEmbeddingConfig() string {
	var b strings.Builder

	b.WriteString(headerStyle.Render("  Embedding Settings"))
	b.WriteString("\n")

	if m.EmbCfgDirty {
		b.WriteString("  " + lipgloss.NewStyle().Foreground(colorAmber).Render("* Unsaved changes"))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	providers := []string{"none", "ollama", "openai"}
	focusMarker := func(field int) string {
		if m.EmbCfgFocusField == field {
			return listSelectedStyle.Render("▸ ")
		}
		return "  "
	}

	labelStyle := lipgloss.NewStyle().Width(14).Foreground(colorText)
	valueStyle := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorSubtext)

	// Provider selector (field 0)
	providerDisplay := fmt.Sprintf("◂ %s ▸", providers[m.EmbCfgProvider])
	b.WriteString(focusMarker(0) + labelStyle.Render("Provider:") + " " + valueStyle.Render(providerDisplay))
	b.WriteString("\n")

	// Model input (field 1)
	if m.EmbCfgModel.Focused() {
		b.WriteString(focusMarker(1) + labelStyle.Render("Model:") + " " + m.EmbCfgModel.View())
	} else {
		modelVal := m.EmbCfgModel.Value()
		if modelVal == "" {
			modelVal = dimStyle.Render("(default)")
		} else {
			modelVal = valueStyle.Render(modelVal)
		}
		b.WriteString(focusMarker(1) + labelStyle.Render("Model:") + " " + modelVal)
	}
	b.WriteString("\n")

	// Vector toggle (field 2)
	vectorCheck := dimStyle.Render("[ ] Disabled")
	if m.EmbCfgVector {
		vectorCheck = lipgloss.NewStyle().Foreground(colorGreen).Render("[x] Enabled")
	}
	b.WriteString(focusMarker(2) + labelStyle.Render("Vector:") + " " + vectorCheck)
	b.WriteString("\n")

	// Auto-start toggle (field 3)
	autoCheck := dimStyle.Render("[ ] Disabled")
	if m.EmbCfgAutoStart {
		autoCheck = lipgloss.NewStyle().Foreground(colorGreen).Render("[x] Enabled")
	}
	b.WriteString(focusMarker(3) + labelStyle.Render("Auto-start:") + " " + autoCheck)
	b.WriteString("\n\n")

	// Save button (field 4)
	if m.EmbCfgFocusField == 4 {
		b.WriteString("  " + lipgloss.NewStyle().Background(colorCyan).Foreground(lipgloss.Color("#16161e")).Bold(true).Padding(0, 2).Render("Save"))
	} else {
		b.WriteString("  " + lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(colorOverlay).Padding(0, 2).Render("Save"))
	}
	b.WriteString("\n")

	// Status messages
	if m.EmbCfgSaving {
		b.WriteString("\n  " + m.EmbCfgSpinner.View() + " Saving configuration...")
	}

	if m.EmbCfgError != "" {
		b.WriteString("\n  " + errorStyle.Render("Error: "+m.EmbCfgError))
	}

	if m.EmbCfgSaved {
		b.WriteString("\n  " + lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Render("Configuration saved."))

		// Reindex warning (amber)
		if m.EmbCfgReindexWarning {
			b.WriteString("\n\n  " + lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render("! Provider/model changed — existing embeddings may be stale."))
			b.WriteString("\n  " + lipgloss.NewStyle().Foreground(colorSubtext).Render("Press [x] to reindex all observations with the new model."))
		}

		// Reindexing spinner + progress bar
		if m.EmbCfgReindexing {
			b.WriteString("\n\n  " + m.EmbCfgSpinner.View() + " Reindexing all observations...")
			pct := 0.0
			if m.ReindexTotal > 0 {
				pct = float64(m.ReindexDone) / float64(m.ReindexTotal)
			}
			b.WriteString("\n  " + m.ReindexProgressBar.ViewAs(pct))
		}

		// Reindex complete (green)
		if m.EmbCfgReindexProgress != "" {
			b.WriteString("\n\n  " + lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Render(m.EmbCfgReindexProgress))
		}

		// Ollama status section
		if m.EmbCfgProvider == 1 {
			b.WriteString("\n")
			b.WriteString("\n  " + lipgloss.NewStyle().Foreground(colorPurple).Bold(true).Render("── Ollama Status ──"))
			b.WriteString("\n")

			if !m.EmbCfgOllamaChecked {
				b.WriteString("  " + m.EmbCfgSpinner.View() + " Checking Ollama status...")
			} else {
				// Running status
				if m.EmbCfgOllamaRunning {
					b.WriteString("  " + lipgloss.NewStyle().Foreground(colorGreen).Render("● Running"))
				} else {
					b.WriteString("  " + lipgloss.NewStyle().Foreground(colorRed).Render("● Stopped"))
					b.WriteString("  " + dimStyle.Render("Press [s] to start Ollama"))
				}

				// Model status
				if m.EmbCfgOllamaRunning {
					if m.EmbCfgOllamaHasModel {
						b.WriteString("    " + lipgloss.NewStyle().Foreground(colorGreen).Render("Model: found"))
					} else {
						modelName := m.EmbCfgModel.Value()
						if modelName == "" {
							modelName = "default model"
						}
						b.WriteString("    " + lipgloss.NewStyle().Foreground(colorAmber).Render("Model: not found"))
						b.WriteString("\n  " + dimStyle.Render(fmt.Sprintf("Press [p] to pull %s", modelName)))
					}
				}
			}

			if m.EmbCfgStarting {
				b.WriteString("\n  " + m.EmbCfgSpinner.View() + " Starting Ollama...")
			}
			if m.EmbCfgPulling {
				b.WriteString("\n  " + m.EmbCfgSpinner.View() + " Pulling model...")
			}
		}
	}

	// Help
	if m.EmbCfgSaved {
		b.WriteString(helpStyle.Render("\n\n  esc back to dashboard"))
	} else if m.EmbCfgModel.Focused() {
		b.WriteString(helpStyle.Render("\n\n  Type model name • enter confirm • esc cancel"))
	} else {
		b.WriteString(helpStyle.Render("\n\n  j/k navigate • h/l cycle provider • space toggle • enter edit/save • r reload config • esc back"))
	}

	return b.String()
}

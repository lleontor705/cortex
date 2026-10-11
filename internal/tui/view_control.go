package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// ─── Setup ──────────────────────────────────────────────────────────────────

func (m Model) viewSetup() string {
	var b strings.Builder

	b.WriteString(headerStyle.Render("  Setup — Install Agent Plugin"))
	b.WriteString("\n")

	// Control & Setup Sub-Tabs
	st1 := deckSubTabActiveStyle.Render("1 Agent Plugins (MCP)")
	st2 := deckSubTabInactiveStyle.Render("2 Local & AI Config")
	fmt.Fprintf(&b, "  Tabs: %s %s  %s\n\n", st1, st2, lipgloss.NewStyle().Foreground(colorSubtext).Render("(press [c] for local config)"))

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

	profile := m.SetupProfile
	if profile == "" {
		profile = "agent"
	}
	profileBadge := "agent (22 tools)"
	switch profile {
	case "dev":
		profileBadge = "dev (11 tools)"
	case "minimal":
		profileBadge = "minimal (5 tools)"
	}
	fmt.Fprintf(&b, "  Profile: %s %s\n\n",
		lipgloss.NewStyle().Background(colorCyan).Foreground(activePalette.BaseBg).Bold(true).Padding(0, 1).Render(profileBadge),
		lipgloss.NewStyle().Foreground(colorSubtext).Render("(press [p] to cycle profile)"))

	for i, agent := range m.SetupAgents {
		badge := ""
		for _, detected := range m.SetupDetectedAgents {
			if detected.Name == agent.Name {
				if detected.Configured {
					badge = " " + lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Render("[✓ CONFIGURED]")
				} else if detected.Detected {
					badge = " " + lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render("[● READY]")
				} else {
					badge = " " + lipgloss.NewStyle().Foreground(colorSubtext).Render("[○ NOT DETECTED]")
				}
				break
			}
		}

		if i == m.Cursor {
			b.WriteString(menuSelectedStyle.Render("▸ "+agent.Description) + badge)
		} else {
			b.WriteString(menuItemStyle.Render("  "+agent.Description) + badge)
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "      %s %s\n\n",
			detailLabelStyle.Render("Install to:"),
			timestampStyle.Render(agent.InstallDir))
	}

	b.WriteString(helpStyle.Render("\n  j/k navigate • enter install • p profile • c local config • esc back"))

	return b.String()
}

// ─── Local Config ───────────────────────────────────────────────────────────

func (m Model) viewLocalConfig() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("  ⬡ Configuration Center"))
	b.WriteString("\n  " + lipgloss.NewStyle().Foreground(colorSubtext).Render("Modular runtime configuration for local storage, intelligence and synchronization."))

	// Control & Setup Sub-Tabs
	st1 := deckSubTabInactiveStyle.Render("1 Agent Plugins (MCP)")
	st2 := deckSubTabActiveStyle.Render("2 Local & AI Config")
	fmt.Fprintf(&b, "\n  Tabs: %s %s  %s\n", st1, st2, lipgloss.NewStyle().Foreground(colorSubtext).Render("(press [i] for agent plugins)"))

	mode, modeDetail, modeColor := "LOCAL ONLY", "SQLite stays local on this device — Zero-CGO, Zero-Bloat.", colorTeal
	if m.LocalCfgMCPRemote {
		mode, modeDetail, modeColor = "REMOTE MCP", "MCP calls bypass SQLite and run on Cortex Server.", colorPurple
	} else if m.LocalCfgSyncEnabled {
		mode, modeDetail, modeColor = "LOCAL-FIRST + SYNC", "SQLite stays local; changes flow both ways in the background.", colorGreen
	}
	b.WriteString("\n  " + lipgloss.NewStyle().Background(modeColor).Foreground(activePalette.BaseBg).Bold(true).Padding(0, 1).Render(mode))
	b.WriteString("  " + lipgloss.NewStyle().Foreground(colorText).Render(modeDetail))
	if m.LocalCfgDirty {
		b.WriteString("  " + lipgloss.NewStyle().Background(colorAmber).Foreground(activePalette.BaseBg).Bold(true).Padding(0, 1).Render("● UNSAVED"))
	}
	b.WriteString("\n\n")

	section := localConfigSection(m.LocalCfgFocusField)
	tabs := []string{"1 Storage", "2 AI & LLM", "3 HTTP API", "4 MCP Proxy", "5 Sync", "6 Review"}
	for i, tab := range tabs {
		if i == section {
			b.WriteString(lipgloss.NewStyle().Background(colorCyan).Foreground(activePalette.BaseBg).Bold(true).Padding(0, 2).Render("▸ " + tab))
		} else {
			b.WriteString(lipgloss.NewStyle().Background(activePalette.Overlay).Foreground(colorSubtext).Padding(0, 2).Render(tab))
		}
		if i < len(tabs)-1 {
			b.WriteString(" ")
		}
	}
	b.WriteString("\n")

	marker := func(field int) string {
		if m.LocalCfgFocusField == field {
			return lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render("▸ ")
		}
		return "  "
	}
	label := lipgloss.NewStyle().Width(19).Foreground(colorText)
	value := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colorSubtext)
	var panel strings.Builder

	var sectionTitleStyle lipgloss.Style
	var cardBorderColor lipgloss.Color
	switch section {
	case 0:
		sectionTitleStyle = lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
		cardBorderColor = colorCyan
	case 1:
		sectionTitleStyle = lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
		cardBorderColor = colorPurple
	case 2:
		sectionTitleStyle = lipgloss.NewStyle().Foreground(colorBlue).Bold(true)
		cardBorderColor = colorBlue
	case 3:
		sectionTitleStyle = lipgloss.NewStyle().Foreground(colorMauve).Bold(true)
		cardBorderColor = colorMauve
	case 4:
		sectionTitleStyle = lipgloss.NewStyle().Foreground(colorTeal).Bold(true)
		cardBorderColor = colorTeal
	default:
		sectionTitleStyle = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
		cardBorderColor = colorGreen
	}

	formats := []string{"YAML", "JSON", "TOML"}
	providers := []string{"None", "Ollama", "OpenAI", "Anthropic", "OpenRouter", "Groq", "DeepSeek", "Custom"}

	switch section {
	case 0:
		panel.WriteString(sectionTitleStyle.Render("💾 Local Storage & Format") + "\n")
		panel.WriteString(dim.Render("Where Cortex stores data on this device and config file format.") + "\n\n")
		textLineTo(&panel, marker, label, value, dim, 0, "Database path:", m.LocalCfgDatabasePath)

		// Format cycler line (field 1)
		var fmtOpts strings.Builder
		for i, f := range formats {
			if i == m.LocalCfgFormat {
				fmtOpts.WriteString(lipgloss.NewStyle().Background(colorCyan).Foreground(activePalette.BaseBg).Bold(true).Padding(0, 1).Render(f) + " ")
			} else {
				fmtOpts.WriteString(dim.Render("["+f+"]") + " ")
			}
		}
		panel.WriteString(marker(1) + label.Render("Config format:") + " " + fmtOpts.String() + "\n")
		panel.WriteString("\n" + dim.Render("Press [Space] or [Enter] on Config format to cycle YAML, JSON, TOML."))

	case 1:
		panel.WriteString(sectionTitleStyle.Render("🤖 AI & Embeddings Provider") + "\n")
		panel.WriteString(dim.Render("Configure model provider for vector embeddings and memory retrieval.") + "\n\n")

		// Provider cycler line (field 2)
		var provOpts strings.Builder
		for i, p := range providers {
			if i == m.LocalCfgLLMProvider {
				provOpts.WriteString(lipgloss.NewStyle().Background(colorPurple).Foreground(activePalette.BaseBg).Bold(true).Padding(0, 1).Render(p) + " ")
			} else {
				provOpts.WriteString(dim.Render("["+p+"]") + " ")
			}
		}
		panel.WriteString(marker(2) + label.Render("LLM Provider:") + " " + provOpts.String() + "\n")
		textLineTo(&panel, marker, label, value, dim, 3, "Model name:", m.LocalCfgLLMModel)
		textLineTo(&panel, marker, label, value, dim, 4, "Base URL:", m.LocalCfgLLMBaseURL)
		panel.WriteString("\n" + dim.Render("Press [Space], [h], [l] on LLM Provider to cycle presets. [t] Test connection."))
		if m.TestConnTesting {
			panel.WriteString("\n" + lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render("  ⟳ Testing connection to "+m.LocalCfgLLMBaseURL.Value()+"..."))
		} else if m.TestConnStatus != "" {
			var style lipgloss.Style
			if strings.HasPrefix(m.TestConnStatus, "✓") {
				style = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
			} else {
				style = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
			}
			panel.WriteString("\n  " + style.Render(m.TestConnStatus))
		}

	case 2:
		panel.WriteString(sectionTitleStyle.Render("🌐 Local HTTP REST API") + "\n")
		panel.WriteString(dim.Render("Used by IDE extensions, plugins, and local HTTP clients.") + "\n\n")
		toggleLineTo(&panel, marker, label, dim, 5, "HTTP API:", m.LocalCfgHTTPEnabled)
		textLineTo(&panel, marker, label, value, dim, 6, "Bind host:", m.LocalCfgHTTPHost)
		textLineTo(&panel, marker, label, value, dim, 7, "Port:", m.LocalCfgHTTPPort)
		panel.WriteString("\n" + dim.Render("Endpoint preview: http://"+m.LocalCfgHTTPHost.Value()+":"+m.LocalCfgHTTPPort.Value()))

	case 3:
		panel.WriteString(sectionTitleStyle.Render("🔌 MCP Transport (Model Context Protocol)") + "\n")
		panel.WriteString(dim.Render("Choose where agent tool calls execute (local SQLite vs remote server).") + "\n\n")

		// Tool profile cycler line (field 8)
		profiles := []struct {
			name  string
			badge string
		}{
			{"agent", "agent (22 tools)"},
			{"dev", "dev (11 tools)"},
			{"minimal", "minimal (5 tools)"},
		}
		var profOpts strings.Builder
		curProfile := strings.ToLower(strings.TrimSpace(m.LocalCfgMCPProfile))
		if curProfile == "" {
			curProfile = "agent"
		}
		for _, p := range profiles {
			if p.name == curProfile {
				profOpts.WriteString(lipgloss.NewStyle().Background(colorMauve).Foreground(activePalette.BaseBg).Bold(true).Padding(0, 1).Render(p.badge) + " ")
			} else {
				profOpts.WriteString(dim.Render("["+p.badge+"]") + " ")
			}
		}
		panel.WriteString(marker(8) + label.Render("Tool profile:") + " " + profOpts.String() + "\n")
		toggleLineTo(&panel, marker, label, dim, 9, "Remote proxy:", m.LocalCfgMCPRemote)
		textLineTo(&panel, marker, label, value, dim, 10, "MCP endpoint:", m.LocalCfgMCPURL)
		textLineTo(&panel, marker, label, value, dim, 11, "Token env name:", m.LocalCfgMCPTokenEnv)
		panel.WriteString("\n" + dim.Render("Press [Space], [h], [l] on Tool profile to cycle agent/dev/minimal. Remote proxy bypasses local SQLite."))

	case 4:
		panel.WriteString(sectionTitleStyle.Render("🔄 Bidirectional Synchronization") + "\n")
		panel.WriteString(dim.Render("Work locally while sharing memories in real-time with Cortex Server.") + "\n\n")
		toggleLineTo(&panel, marker, label, dim, 12, "Background sync:", m.LocalCfgSyncEnabled)
		textLineTo(&panel, marker, label, value, dim, 13, "Server URL:", m.LocalCfgSyncURL)
		textLineTo(&panel, marker, label, value, dim, 14, "Token env name:", m.LocalCfgSyncTokenEnv)
		textLineTo(&panel, marker, label, value, dim, 15, "Sync interval:", m.LocalCfgSyncInterval)
		panel.WriteString("\n" + dim.Render("Only the environment variable name is saved, never the secret token."))

	case 5:
		panel.WriteString(sectionTitleStyle.Render("📋 Review & Apply Settings") + "\n")
		panel.WriteString(dim.Render("Review configured parameters before writing to disk.") + "\n\n")

		curFmt := "YAML"
		if m.LocalCfgFormat >= 0 && m.LocalCfgFormat < len(formats) {
			curFmt = formats[m.LocalCfgFormat]
		}
		curProv := "None"
		if m.LocalCfgLLMProvider >= 0 && m.LocalCfgLLMProvider < len(providers) {
			curProv = providers[m.LocalCfgLLMProvider]
		}
		curProf := strings.ToLower(strings.TrimSpace(m.LocalCfgMCPProfile))
		if curProf == "" {
			curProf = "agent"
		}
		profBadge := curProf + " (" + profileToolCount(curProf) + ")"

		panel.WriteString(configSummaryLine("Storage", m.LocalCfgDatabasePath.Value()+" ("+curFmt+")"))
		panel.WriteString(configSummaryLine("AI / LLM", curProv+" · "+m.LocalCfgLLMModel.Value()))
		panel.WriteString(configSummaryLine("HTTP API", enabledSummary(m.LocalCfgHTTPEnabled, m.LocalCfgHTTPHost.Value()+":"+m.LocalCfgHTTPPort.Value())))
		panel.WriteString(configSummaryLine("MCP Profile", profBadge))
		panel.WriteString(configSummaryLine("MCP Proxy", enabledSummary(m.LocalCfgMCPRemote, m.LocalCfgMCPURL.Value())))
		panel.WriteString(configSummaryLine("Sync", enabledSummary(m.LocalCfgSyncEnabled, m.LocalCfgSyncURL.Value()+" ("+m.LocalCfgSyncInterval.Value()+")")))
		panel.WriteString("\n")
		if m.LocalCfgFocusField == 16 {
			panel.WriteString(lipgloss.NewStyle().Background(colorCyan).Foreground(activePalette.BaseBg).Bold(true).Padding(0, 3).Render("✔ Validate & Save Configuration"))
		} else {
			panel.WriteString(lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorOverlay).Padding(0, 3).Render("Validate & Save Configuration"))
		}
	}
	panelWidth := 76
	if m.Width > 0 && m.Width < 82 {
		panelWidth = max(44, m.Width-8)
	}
	b.WriteString("\n" + lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cardBorderColor).Padding(1, 2).Width(panelWidth).Render(panel.String()))
	if m.LocalCfgSaving {
		b.WriteString("\n\n  " + m.LocalCfgSpinner.View() + " Validating and saving configuration...")
	}
	if m.LocalCfgError != "" {
		b.WriteString("\n\n  " + errorStyle.Render("Error: "+m.LocalCfgError))
	}
	if m.LocalCfgSaved {
		b.WriteString("\n\n  " + lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Render("✔ Configuration saved successfully."))
		b.WriteString("\n  " + lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render("Tip: Restart cortex or agents to immediately apply runtime changes."))
	}
	if m.localConfigInputFocused() {
		b.WriteString(helpStyle.Render("\n\n  Type value • [Enter] Confirm • [Esc] Stop editing"))
	} else {
		b.WriteString(helpStyle.Render("\n\n  [Tab]/[h/l] Sections • [j/k] Navigate • [Enter] Edit • [Space] Toggle/Cycle • [t] test LLM • [i] Setup agents • [s] Save • [Esc] Back"))
	}
	return b.String()
}

func textLineTo(b *strings.Builder, marker func(int) string, label, value, dim lipgloss.Style, field int, name string, input textinput.Model) {
	shown := input.Value()
	if input.Focused() {
		shown = input.View()
	} else if shown == "" {
		shown = dim.Render("(not set)")
	} else {
		shown = value.Render(shown)
	}
	b.WriteString(marker(field) + label.Render(name) + " " + shown + "\n")
}

func toggleLineTo(b *strings.Builder, marker func(int) string, label, dim lipgloss.Style, field int, name string, enabled bool) {
	shown := dim.Render("[ ] Off")
	if enabled {
		shown = lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Render("[x] On")
	}
	b.WriteString(marker(field) + label.Render(name) + " " + shown + "\n")
}

func configSummaryLine(label, value string) string {
	return lipgloss.NewStyle().Foreground(colorSubtext).Width(11).Render(label) + lipgloss.NewStyle().Foreground(colorText).Render(value) + "\n"
}

func enabledSummary(enabled bool, detail string) string {
	if !enabled {
		return "Off"
	}
	if strings.TrimSpace(detail) == "" {
		return "On"
	}
	return "On · " + detail
}

func profileToolCount(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "dev":
		return "11 tools"
	case "minimal":
		return "5 tools"
	default:
		return "22 tools"
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
		b.WriteString("  " + lipgloss.NewStyle().Background(colorCyan).Foreground(activePalette.BaseBg).Bold(true).Padding(0, 2).Render("Save"))
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

// ─── Unified Configuration view (REQ-CFG-001 .. REQ-CFG-006) ────────────────

// unifiedConfigFormatNote documents the shipped config-layer behavior: a format
// change writes the new file and moves the previous one to a .bak sibling. The
// historic "delete the old file manually" warning is therefore obsolete
// (verified against internal/config.Save -> retireConfigFile).
const unifiedConfigFormatNote = "Format change: the previous config file is moved to a .bak backup automatically — no manual deletion needed."

// viewUnifiedConfig renders the single Configuration screen. The active tab is
// derived from the shared focus field, so a fresh install opens on Essential.
func (m Model) viewUnifiedConfig() string {
	tab := unifiedConfigTabForField(m.LocalCfgFocusField)
	var b strings.Builder
	b.WriteString(headerStyle.Render("  Configuration"))
	b.WriteString("   " + unifiedConfigTabBar(tab) + "\n")
	b.WriteString("  " + lipgloss.NewStyle().Foreground(colorSubtext).Render("Essential covers the defaults; Advanced is opt-in.") + "\n\n")

	label := lipgloss.NewStyle().Width(18).Foreground(colorText)
	value := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colorSubtext)

	fields := unifiedConfigFields()
	section := ""
	var panel strings.Builder
	for i, f := range fields {
		if f.Tab != tab {
			continue
		}
		group := unifiedConfigGroup(f.Tab, f.Key)
		if group != section {
			if section != "" {
				panel.WriteString("\n")
			}
			panel.WriteString(lipgloss.NewStyle().Foreground(colorPurple).Bold(true).Render(group) + "\n")
			section = group
		}
		panel.WriteString(m.unifiedConfigFieldLine(unifiedConfigFieldNumber(i), f, label, value, dim))
	}

	panelWidth := 76
	if m.Width > 0 && m.Width < 82 {
		panelWidth = max(44, m.Width-8)
	}
	b.WriteString(lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorCyan).Padding(1, 2).Width(panelWidth).Render(panel.String()))

	if tab == ConfigTabAdvanced && unifiedConfigFormatChanged(m) {
		b.WriteString("\n  " + lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render("⚠ "+unifiedConfigFormatNote))
	}
	if m.LocalCfgSaving {
		b.WriteString("\n\n  " + m.LocalCfgSpinner.View() + " Saving configuration...")
	}
	if m.LocalCfgError != "" {
		b.WriteString("\n\n  " + errorStyle.Render("Error: "+m.LocalCfgError))
	}
	if m.LocalCfgDirty {
		b.WriteString("\n\n  " + lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render("● unsaved changes"))
	}

	b.WriteString(helpStyle.Render("\n\n  [tab] tab • [j/k] navigate • [enter] edit/activate • [s] Save & apply • [r] Reload from disk • [c] Connect • [esc] Back"))
	return b.String()
}

// viewConfiguration is the stable name the View router and the pinned
// view_test.go call. All rendering now lives in viewUnifiedConfig; this is a
// pure alias so the two entry points can never drift apart again.
func (m Model) viewConfiguration() string {
	return m.viewUnifiedConfig()
}

func unifiedConfigTabBar(active ConfigTab) string {
	render := func(tab ConfigTab) string {
		if tab == active {
			return lipgloss.NewStyle().Background(colorCyan).Foreground(activePalette.BaseBg).Bold(true).Padding(0, 1).Render(tab.String())
		}
		return lipgloss.NewStyle().Foreground(colorSubtext).Render(tab.String())
	}
	return render(ConfigTabEssential) + "   " + render(ConfigTabAdvanced)
}

func unifiedConfigGroup(tab ConfigTab, key string) string {
	if tab == ConfigTabEssential {
		switch key {
		case "ai_provider", "ai_model", "ai_base_url":
			return "AI PROVIDER"
		case "embedding_provider", "embedding_model":
			return "EMBEDDING"
		case "save_essential":
			return "APPLY"
		default:
			return "SERVER"
		}
	}
	switch key {
	case "database_path", "config_format":
		return "STORAGE"
	case "http_enabled", "http_host", "http_port", "http_token":
		return "HTTP API"
	case "mcp_profile", "mcp_remote", "mcp_url", "mcp_token_env":
		return "MCP"
	case "sync_enabled", "sync_url", "sync_token_env", "sync_interval":
		return "SYNC"
	case "save_advanced":
		return "APPLY"
	default:
		return "EMBEDDING ADVANCED"
	}
}

func (m Model) unifiedConfigFieldLine(num int, f configField, label, value, dim lipgloss.Style) string {
	marker := "  "
	if m.LocalCfgFocusField == num {
		marker = lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render("▸ ")
	}
	return marker + label.Render(f.Label+":") + " " + m.unifiedConfigFieldValue(f, value, dim) + "\n"
}

func (m Model) unifiedConfigFieldValue(f configField, value, dim lipgloss.Style) string {
	switch f.Key {
	case "ai_provider":
		return value.Render("◂ " + providerDisplayName(m.effectiveAIProviderIndex()) + " ▸")
	case "ai_model":
		return inputValue(m.LocalCfgLLMModel, value, dim, "nomic-embed-text")
	case "ai_base_url":
		return inputValue(m.LocalCfgLLMBaseURL, value, dim, "http://localhost:11434")
	case "embedding_provider":
		return value.Render("◂ " + providerDisplayName(m.effectiveEmbeddingProviderIndex()) + " ▸")
	case "embedding_model":
		return inputValue(m.EmbCfgModel, value, dim, defaultEmbeddingModel)
	case "embedding_dimensions":
		return inputValue(m.LocalCfgEmbeddingDimensions, value, dim, "0 (provider default)")
	case "server_status":
		chip := m.configServerChip()
		return chipTextStyle(chip).Render(chip.Text()) + "  " + dim.Render("[ Connect... -> ]")
	case "save_essential", "save_advanced":
		return value.Render("[ Save & apply ]")
	case "database_path":
		return inputValue(m.LocalCfgDatabasePath, value, dim, "~/.cortex/cortex.db")
	case "config_format":
		return value.Render("◂ " + configFormatLabel(m.LocalCfgFormat) + " ▸")
	case "http_enabled":
		return toggleText(m.LocalCfgHTTPEnabled, dim)
	case "http_host":
		return inputValue(m.LocalCfgHTTPHost, value, dim, "127.0.0.1")
	case "http_port":
		return inputValue(m.LocalCfgHTTPPort, value, dim, "7438")
	case "http_token":
		if strings.TrimSpace(m.AuthTokenInput.Value()) == "" {
			return dim.Render("(unchanged)")
		}
		return value.Render("••••••••")
	case "mcp_profile":
		return value.Render("◂ " + mcpProfileLabel(m.LocalCfgMCPProfile) + " ▸")
	case "mcp_remote":
		return toggleText(m.LocalCfgMCPRemote, dim)
	case "mcp_url":
		return inputValue(m.LocalCfgMCPURL, value, dim, "(not set)")
	case "mcp_token_env":
		return inputValue(m.LocalCfgMCPTokenEnv, value, dim, "CORTEX_REMOTE_TOKEN")
	case "sync_enabled":
		return toggleText(m.LocalCfgSyncEnabled, dim)
	case "sync_url":
		return inputValue(m.LocalCfgSyncURL, value, dim, "(not set)")
	case "sync_token_env":
		return inputValue(m.LocalCfgSyncTokenEnv, value, dim, "CORTEX_REMOTE_TOKEN")
	case "sync_interval":
		return inputValue(m.LocalCfgSyncInterval, value, dim, "30s")
	case "embedding_autostart":
		return toggleText(m.EmbCfgAutoStart, dim)
	case "theme":
		return value.Render("◂ " + themeLabel(m.IsDarkTheme) + " ▸")
	}
	return dim.Render("(not set)")
}

func inputValue(input textinput.Model, value, dim lipgloss.Style, fallback string) string {
	if input.Focused() {
		return input.View()
	}
	if v := input.Value(); v != "" {
		return value.Render(v)
	}
	return dim.Render(fallback)
}

func toggleText(on bool, dim lipgloss.Style) string {
	if on {
		return lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Render("[x] On")
	}
	return dim.Render("[ ] Off")
}

func configFormatLabel(format int) string {
	switch format {
	case 1:
		return "json"
	case 2:
		return "toml"
	default:
		return "yaml"
	}
}

func mcpProfileLabel(profile string) string {
	if strings.TrimSpace(profile) == "" {
		return "agent"
	}
	return strings.ToLower(strings.TrimSpace(profile))
}

func themeLabel(dark bool) string {
	if dark {
		return "dark"
	}
	return "light"
}

// configServerChip derives the honest server status shown on the Essential tab
// (REQ-CFG-005/REQ-SYNC-003).
func (m Model) configServerChip() SyncChip {
	hasEndpoint := false
	var interval time.Duration
	if m.deps != nil && m.deps.Config != nil {
		cfg := m.deps.Config
		hasEndpoint = strings.TrimSpace(cfg.Sync.URL) != "" || strings.TrimSpace(cfg.MCP.Remote.URL) != ""
		interval = cfg.Sync.Interval
	}
	return dashboardSyncChipFor(m.authSyncStatus(), hasEndpoint, interval)
}

func chipTextStyle(chip SyncChip) lipgloss.Style {
	if chip.IsSuccess() {
		return lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	}
	return lipgloss.NewStyle().Foreground(colorAmber).Bold(true)
}

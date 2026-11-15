package tui

import (
	"fmt"

	"github.com/lleontor705/cortex/v2/internal/setup"

	tea "github.com/charmbracelet/bubbletea"
)

// ─── Dashboard ──────────────────────────────────────────────────────────────

func (m Model) handleDashboardKeys(key string) (tea.Model, tea.Cmd) {
	items := flattenDashboardItems()
	maxItems := len(items)

	switch key {
	case "up", "k":
		if m.Cursor > 0 {
			m.Cursor--
		}
	case "down", "j":
		if m.Cursor < maxItems-1 {
			m.Cursor++
		}
	case "enter", " ":
		return m.activateDashboardItem(m.Cursor)
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		idx := int(key[0] - '1')
		if idx >= 0 && idx < maxItems {
			m.Cursor = idx
			return m.activateDashboardItem(idx)
		}
	case "t", "T":
		ToggleTheme()
		return m, nil
	case "s", "/":
		return m.navigateDashboard(ScreenSearch)
	case "r", "R":
		return m.navigateDashboard(ScreenRecent)
	case "g", "G":
		return m.navigateDashboard(ScreenGraph)
	case "c", "C":
		return m.navigateDashboard(ScreenLocalConfig)
	case "i", "I":
		return m.navigateDashboard(ScreenSetup)
	case "u", "U":
		if m.UpdateResult != nil {
			m.ToastMessage = fmt.Sprintf("Downloading & installing %s...", m.UpdateResult.Latest)
			m.ToastType = "warning"
			return m, m.performSelfUpdateCmd()
		}
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

// activateDashboardItem opens the menu entry at idx. Connect launches the
// first-class guided flow; Quit ends the session.
func (m Model) activateDashboardItem(idx int) (tea.Model, tea.Cmd) {
	items := flattenDashboardItems()
	if idx < 0 || idx >= len(items) {
		return m, nil
	}
	switch items[idx].action {
	case dashboardConnect:
		return m.openConnectFlow()
	case dashboardQuit:
		return m, tea.Quit
	}
	return m.navigateDashboard(items[idx].dest)
}

// navigateDashboard pushes the destination screen with its data load, mirroring
// the per-destination behavior of the legacy dashboard selection.
func (m Model) navigateDashboard(dest Screen) (tea.Model, tea.Cmd) {
	m.Cursor = 0
	m.Scroll = 0
	switch dest {
	case ScreenRecent:
		m.NavPush(ScreenRecent)
		return m, loadRecentObservations(m.deps, m.FilterProject)
	case ScreenSearch:
		m.NavPush(ScreenSearch)
		m.SearchInput.SetValue("")
		m.SearchInput.Focus()
		return m, nil
	case ScreenGraph:
		m.NavPush(ScreenGraph)
		if len(m.RecentObservations) > 0 {
			m.GraphRootID = m.RecentObservations[0].ID
			return m, loadGraphRelated(m.deps, m.GraphRootID)
		}
		return m, loadRecentObservations(m.deps, m.FilterProject)
	case ScreenHealth:
		m.NavPush(ScreenHealth)
		project := ""
		if m.Stats != nil && len(m.Stats.Projects) > 0 {
			project = m.Stats.Projects[0]
		}
		return m, loadHealthData(m.deps, project)
	case ScreenSessions:
		m.NavPush(ScreenSessions)
		return m, loadRecentSessions(m.deps)
	case ScreenArchive:
		m.NavPush(ScreenArchive)
		return m, loadArchivedObservations(m.deps, m.FilterProject)
	case ScreenTimeline:
		if len(m.RecentObservations) == 0 {
			m.ToastMessage = "Load recent observations first to open a timeline"
			m.ToastType = "warning"
			return m, nil
		}
		m.NavPush(ScreenTimeline)
		return m, loadTimeline(m.deps, m.RecentObservations[0].ID)
	case ScreenLocalConfig:
		return m.openLocalConfig(), nil
	case ScreenSetup:
		m.NavPush(ScreenSetup)
		m.SetupAgents = setup.SupportedAgents()
		m.SetupResult = nil
		m.SetupDone = false
		m.SetupInstalling = false
		m.SetupInstallingName = ""
		m.SetupError = ""
		m.SetupAllowlistPrompt = false
		m.SetupAllowlistApplied = false
		m.SetupAllowlistError = ""
		return m, detectAgentsCmd()
	case ScreenHelp:
		m.NavPush(ScreenHelp)
		return m, nil
	}
	return m, nil
}

// ─── Setup ──────────────────────────────────────────────────────────────────

func (m Model) handleSetupKeys(key string) (tea.Model, tea.Cmd) {
	if m.SetupInstalling {
		return m, nil
	}

	// Allowlist prompt: y/n
	if m.SetupAllowlistPrompt {
		switch key {
		case "y", "Y":
			m.SetupAllowlistPrompt = false
			m.SetupDone = true
			if err := addClaudeCodeAllowlistFn(); err != nil {
				m.SetupAllowlistError = err.Error()
			} else {
				m.SetupAllowlistApplied = true
			}
			return m, nil
		case "n", "N", "esc":
			m.SetupAllowlistPrompt = false
			m.SetupDone = true
			return m, nil
		}
		return m, nil
	}

	if m.SetupDone {
		switch key {
		case "esc":
			m.NavPop()
			m.Cursor = 0
			m.SetupDone = false
			m.SetupResult = nil
			m.SetupError = ""
			m.SetupAllowlistApplied = false
			m.SetupAllowlistError = ""
			return m, loadStats(m.deps)
		case "q", "enter":
			m.NavReset()
			m.Cursor = 0
			m.SetupDone = false
			m.SetupResult = nil
			m.SetupError = ""
			m.SetupAllowlistApplied = false
			m.SetupAllowlistError = ""
			return m, loadStats(m.deps)
		}
		return m, nil
	}

	switch key {
	case "p", "P":
		switch m.SetupProfile {
		case "agent":
			m.SetupProfile = "dev"
		case "dev":
			m.SetupProfile = "minimal"
		default:
			m.SetupProfile = "agent"
		}
		return m, nil
	case "up", "k":
		if m.Cursor > 0 {
			m.Cursor--
		}
	case "down", "j":
		if m.Cursor < len(m.SetupAgents)-1 {
			m.Cursor++
		}
	case "enter":
		if len(m.SetupAgents) > 0 && m.Cursor < len(m.SetupAgents) {
			agent := m.SetupAgents[m.Cursor]
			m.SetupInstalling = true
			m.SetupInstallingName = agent.Name
			profile := m.SetupProfile
			if profile == "" {
				profile = "agent"
			}
			return m, tea.Batch(m.SetupSpinner.Tick, installAgentWithProfile(agent.Name, profile))
		}
	case "c", "C":
		return m.openLocalConfig(), nil
	case "esc":
		m.NavPop()
		m.Cursor = 0
		return m, loadStats(m.deps)
	case "q":
		m.NavReset()
		m.Cursor = 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

// ─── Help ──────────────────────────────────────────────────────────────────

func (m Model) handleHelpKeys(_ string) (tea.Model, tea.Cmd) {
	m.NavPop()
	return m, nil
}

// updatePending gates the self-update binding: the key is only advertised (and
// dispatchable) while an update is actually staged, so Help and footers never
// show a control that would do nothing (REQ-NAV-004).
func updatePending(m *Model) bool { return m != nil && m.UpdateResult != nil }

// dashboardKeymap declares the Dashboard bindings so the screen the user starts
// from is truthful in Help. The generic delegation described the drifted keys
// (i as "Search", r as "Reload", q as "Back") which no longer matched their
// handlers. Every handler here is the one the delegation already used, so only
// the descriptions change.
func dashboardKeymap() Keymap {
	handler := Model.handleDashboardKeys
	bind := func(key, description string) Binding {
		return Binding{Key: key, Description: description, Handler: handler}
	}
	return Keymap{Screen: ScreenDashboard, Bindings: combineBindings(
		workspaceBindings(),
		newObservationBindings(),
		authBindings(),
		filterBindings(),
		themeBindings(),
		[]Binding{
			bind("up", "Move up"),
			bind("k", "Move up"),
			bind("down", "Move down"),
			bind("j", "Move down"),
			bind("enter", "Open"),
			bind(" ", "Select"),
			bind("5", "Select"),
			bind("6", "Select"),
			bind("7", "Select"),
			bind("8", "Select"),
			bind("9", "Select"),
			bind("s", "Search"),
			bind("/", "Search"),
			bind("r", "Recent"),
			bind("R", "Recent"),
			bind("g", "Graph"),
			bind("G", "Graph"),
			bind("c", "Configuration"),
			bind("C", "Configuration"),
			bind("i", "Setup"),
			bind("I", "Setup"),
			{Key: "u", Description: "Install update", When: updatePending, Handler: handler},
			{Key: "U", Description: "Install update", When: updatePending, Handler: handler},
			bind("q", "Quit"),
		},
	)}
}

// helpKeymap declares the Help screen's own bindings so its footer and the
// registry agree on how to leave the screen. The generic delegation left `?`
// with an empty description; it is declared here so Help never lists a blank
// key.
func helpKeymap() Keymap {
	bind := func(key, description string) Binding {
		return Binding{Key: key, Description: description, Handler: Model.handleHelpKeys}
	}
	return Keymap{Screen: ScreenHelp, Bindings: []Binding{
		bind("esc", "Back"),
		bind("q", "Back"),
		bind("?", "Close help"),
	}}
}

func init() {
	keymapRegistry.Register(dashboardKeymap())
	keymapRegistry.Register(helpKeymap())
}

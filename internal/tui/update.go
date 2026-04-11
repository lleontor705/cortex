package tui

import (
	"github.com/lleontor705/cortex/internal/setup"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	minVisibleItems = 3  // Minimum items shown in any list
	linesPerItem    = 2  // Lines per observation item (title + preview)
	maxScrollOffset = 500 // Upper bound for scroll values
)

// ─── Update ─────────────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.Screen == ScreenSearch && m.SearchInput.Focused() {
			return m.handleSearchInputKeys(msg)
		}
		return m.handleKeyPress(msg.String())

	// ─── Data loaded messages ────────────────────────────────────────
	case updateCheckMsg:
		m.UpdateResult = msg.result
		return m, nil

	case statsLoadedMsg:
		if msg.err != nil {
			m.ErrorMsg = msg.err.Error()
			return m, nil
		}
		m.Stats = msg.stats
		return m, nil

	case searchResultsMsg:
		if msg.err != nil {
			m.ErrorMsg = msg.err.Error()
			return m, nil
		}
		m.SearchResults = msg.results
		m.SearchQuery = msg.query
		m.Screen = ScreenSearchResults
		m.Cursor = 0
		m.Scroll = 0
		return m, nil

	case recentObservationsMsg:
		if msg.err != nil {
			m.ErrorMsg = msg.err.Error()
			return m, nil
		}
		m.RecentObservations = msg.observations
		return m, nil

	case observationDetailMsg:
		if msg.err != nil {
			m.ErrorMsg = msg.err.Error()
			return m, nil
		}
		m.SelectedObservation = msg.observation
		m.DetailScore = msg.score
		m.DetailEntities = msg.entities
		m.DetailEdges = msg.edges
		m.Screen = ScreenObservationDetail
		m.DetailScroll = 0
		return m, nil

	case timelineMsg:
		if msg.err != nil {
			m.ErrorMsg = msg.err.Error()
			return m, nil
		}
		m.TimelineFocus = msg.focus
		m.TimelineBefore = msg.before
		m.TimelineAfter = msg.after
		m.Screen = ScreenTimeline
		m.Scroll = 0
		return m, nil

	case recentSessionsMsg:
		if msg.err != nil {
			m.ErrorMsg = msg.err.Error()
			return m, nil
		}
		m.Sessions = msg.sessions
		return m, nil

	case sessionObservationsMsg:
		if msg.err != nil {
			m.ErrorMsg = msg.err.Error()
			return m, nil
		}
		m.SessionObservations = msg.observations
		m.Screen = ScreenSessionDetail
		m.Cursor = 0
		m.SessionDetailScroll = 0
		return m, nil

	case graphLoadedMsg:
		if msg.err != nil {
			m.ErrorMsg = msg.err.Error()
			return m, nil
		}
		m.GraphObservations = msg.related
		m.GraphEdges = msg.edges
		m.Screen = ScreenGraph
		m.Cursor = 0
		m.Scroll = 0
		return m, nil

	case healthLoadedMsg:
		if msg.err != nil {
			m.ErrorMsg = msg.err.Error()
			return m, nil
		}
		m.HealthStale = msg.stale
		m.HealthOrphans = msg.orphans
		m.HealthEdgeCount = msg.edgeCount
		m.HealthObsCount = msg.obsCount
		m.HealthCandidates = msg.candidates
		return m, nil

	case archiveLoadedMsg:
		if msg.err != nil {
			m.ErrorMsg = msg.err.Error()
			return m, nil
		}
		m.ArchivedObservations = msg.observations
		return m, nil

	case setupInstallMsg:
		m.SetupInstalling = false
		if msg.err != nil {
			m.SetupDone = true
			m.SetupError = msg.err.Error()
			return m, nil
		}
		m.SetupResult = msg.result
		m.SetupError = ""
		// For claude-code, show allowlist prompt before marking done
		if msg.result != nil && msg.result.Agent == "claude-code" {
			m.SetupAllowlistPrompt = true
			return m, nil
		}
		m.SetupDone = true
		return m, nil

	case spinner.TickMsg:
		if m.SetupInstalling {
			var cmd tea.Cmd
			m.SetupSpinner, cmd = m.SetupSpinner.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	return m, nil
}

// ─── Key Press Router ───────────────────────────────────────────────────────

func (m Model) handleKeyPress(key string) (tea.Model, tea.Cmd) {
	m.ErrorMsg = ""

	switch m.Screen {
	case ScreenDashboard:
		return m.handleDashboardKeys(key)
	case ScreenSearch:
		return m.handleSearchKeys(key)
	case ScreenSearchResults:
		return m.handleSearchResultsKeys(key)
	case ScreenRecent:
		return m.handleRecentKeys(key)
	case ScreenObservationDetail:
		return m.handleObservationDetailKeys(key)
	case ScreenTimeline:
		return m.handleTimelineKeys(key)
	case ScreenSessions:
		return m.handleSessionsKeys(key)
	case ScreenSessionDetail:
		return m.handleSessionDetailKeys(key)
	case ScreenSetup:
		return m.handleSetupKeys(key)
	case ScreenGraph:
		return m.handleGraphKeys(key)
	case ScreenArchive:
		return m.handleArchiveKeys(key)
	case ScreenHealth:
		return m.handleHealthKeys(key)
	}
	return m, nil
}

// ─── Dashboard ──────────────────────────────────────────────────────────────

var dashboardMenuItems = []string{
	"Search memories",
	"Recent observations",
	"Browse sessions",
	"Knowledge graph",
	"Memory health",
	"Archived observations",
	"Setup agent plugin",
	"Quit",
}

func (m Model) handleDashboardKeys(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.Cursor > 0 {
			m.Cursor--
		}
	case "down", "j":
		if m.Cursor < len(dashboardMenuItems)-1 {
			m.Cursor++
		}
	case "enter", " ":
		return m.handleDashboardSelection()
	case "s", "/":
		m.PrevScreen = ScreenDashboard
		m.Screen = ScreenSearch
		m.Cursor = 0
		m.SearchInput.SetValue("")
		m.SearchInput.Focus()
		return m, nil
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) handleDashboardSelection() (tea.Model, tea.Cmd) {
	switch m.Cursor {
	case 0: // Search
		m.PrevScreen = ScreenDashboard
		m.Screen = ScreenSearch
		m.Cursor = 0
		m.SearchInput.SetValue("")
		m.SearchInput.Focus()
		return m, nil
	case 1: // Recent observations
		m.PrevScreen = ScreenDashboard
		m.Screen = ScreenRecent
		m.Cursor = 0
		m.Scroll = 0
		return m, loadRecentObservations(m.deps)
	case 2: // Sessions
		m.PrevScreen = ScreenDashboard
		m.Screen = ScreenSessions
		m.Cursor = 0
		m.Scroll = 0
		return m, loadRecentSessions(m.deps)
	case 3: // Knowledge graph
		m.PrevScreen = ScreenDashboard
		m.Screen = ScreenGraph
		m.Cursor = 0
		m.Scroll = 0
		// Load graph from most recent observation if available
		if len(m.RecentObservations) > 0 {
			m.GraphRootID = m.RecentObservations[0].ID
			return m, loadGraphRelated(m.deps, m.GraphRootID)
		}
		return m, loadRecentObservations(m.deps)
	case 4: // Memory health
		m.PrevScreen = ScreenDashboard
		m.Screen = ScreenHealth
		m.Cursor = 0
		m.Scroll = 0
		project := ""
		if m.Stats != nil && len(m.Stats.Projects) > 0 {
			project = m.Stats.Projects[0]
		}
		return m, loadHealthData(m.deps, project)
	case 5: // Archived
		m.PrevScreen = ScreenDashboard
		m.Screen = ScreenArchive
		m.Cursor = 0
		m.Scroll = 0
		return m, loadArchivedObservations(m.deps)
	case 6: // Setup
		m.PrevScreen = ScreenDashboard
		m.Screen = ScreenSetup
		m.Cursor = 0
		m.SetupAgents = setup.SupportedAgents()
		m.SetupResult = nil
		m.SetupDone = false
		m.SetupInstalling = false
		m.SetupInstallingName = ""
		m.SetupError = ""
		m.SetupAllowlistPrompt = false
		m.SetupAllowlistApplied = false
		m.SetupAllowlistError = ""
		return m, nil
	case 7: // Quit
		return m, tea.Quit
	}
	return m, nil
}

// ─── Search Input ───────────────────────────────────────────────────────────

func (m Model) handleSearchInputKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		query := m.SearchInput.Value()
		if query != "" {
			m.SearchInput.Blur()
			return m, searchMemories(m.deps, query)
		}
		return m, nil
	case "esc":
		m.SearchInput.Blur()
		m.Screen = ScreenDashboard
		m.Cursor = 0
		return m, nil
	}

	var cmd tea.Cmd
	m.SearchInput, cmd = m.SearchInput.Update(msg)
	return m, cmd
}

func (m Model) handleSearchKeys(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q":
		m.Screen = ScreenDashboard
		m.Cursor = 0
		return m, nil
	case "i", "/":
		m.SearchInput.Focus()
		return m, nil
	}
	return m, nil
}

// ─── Search Results ─────────────────────────────────────────────────────────

func (m Model) handleSearchResultsKeys(key string) (tea.Model, tea.Cmd) {
	visibleItems := (m.Height - 10) / linesPerItem
	if visibleItems < minVisibleItems {
		visibleItems = minVisibleItems
	}

	switch key {
	case "up", "k":
		if m.Cursor > 0 {
			m.Cursor--
			if m.Cursor < m.Scroll {
				m.Scroll = m.Cursor
			}
		}
	case "down", "j":
		if m.Cursor < len(m.SearchResults)-1 {
			m.Cursor++
			if m.Cursor >= m.Scroll+visibleItems {
				m.Scroll = m.Cursor - visibleItems + 1
			}
		}
	case "enter":
		if len(m.SearchResults) > 0 && m.Cursor < len(m.SearchResults) {
			obsID := m.SearchResults[m.Cursor].ID
			m.PrevScreen = ScreenSearchResults
			m.PrevCursor = m.Cursor
			return m, loadObservationDetail(m.deps, obsID)
		}
	case "t":
		if len(m.SearchResults) > 0 && m.Cursor < len(m.SearchResults) {
			obsID := m.SearchResults[m.Cursor].ID
			m.PrevScreen = ScreenSearchResults
			m.PrevCursor = m.Cursor
			return m, loadTimeline(m.deps, obsID)
		}
	case "/", "s":
		m.PrevScreen = ScreenSearchResults
		m.Screen = ScreenSearch
		m.SearchInput.Focus()
		return m, nil
	case "esc", "q":
		m.PrevScreen = ScreenDashboard
		m.Screen = ScreenSearch
		m.Cursor = 0
		m.Scroll = 0
		m.SearchInput.Focus()
		return m, nil
	}
	return m, nil
}

// ─── Recent Observations ────────────────────────────────────────────────────

func (m Model) handleRecentKeys(key string) (tea.Model, tea.Cmd) {
	visibleItems := (m.Height - 8) / linesPerItem
	if visibleItems < minVisibleItems {
		visibleItems = minVisibleItems
	}

	switch key {
	case "up", "k":
		if m.Cursor > 0 {
			m.Cursor--
			if m.Cursor < m.Scroll {
				m.Scroll = m.Cursor
			}
		}
	case "down", "j":
		if m.Cursor < len(m.RecentObservations)-1 {
			m.Cursor++
			if m.Cursor >= m.Scroll+visibleItems {
				m.Scroll = m.Cursor - visibleItems + 1
			}
		}
	case "enter":
		if len(m.RecentObservations) > 0 && m.Cursor < len(m.RecentObservations) {
			obsID := m.RecentObservations[m.Cursor].ID
			m.PrevScreen = ScreenRecent
			m.PrevCursor = m.Cursor
			return m, loadObservationDetail(m.deps, obsID)
		}
	case "t":
		if len(m.RecentObservations) > 0 && m.Cursor < len(m.RecentObservations) {
			obsID := m.RecentObservations[m.Cursor].ID
			m.PrevScreen = ScreenRecent
			m.PrevCursor = m.Cursor
			return m, loadTimeline(m.deps, obsID)
		}
	case "esc", "q":
		m.Screen = ScreenDashboard
		m.Cursor = 0
		m.Scroll = 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

// ─── Observation Detail ─────────────────────────────────────────────────────

func (m Model) handleObservationDetailKeys(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.DetailScroll > 0 {
			m.DetailScroll--
		}
	case "down", "j":
		// Bounded scroll — view.go clamps to maxScroll during rendering,
		// but we cap here to avoid ever-growing scroll values.
		m.DetailScroll++
		if m.DetailScroll > maxScrollOffset {
			m.DetailScroll = maxScrollOffset
		}
	case "t":
		if m.SelectedObservation != nil {
			m.PrevScreen = ScreenObservationDetail
			return m, loadTimeline(m.deps, m.SelectedObservation.ID)
		}
	case "g":
		// Jump to graph view for this observation
		if m.SelectedObservation != nil {
			m.GraphRootID = m.SelectedObservation.ID
			m.PrevScreen = ScreenObservationDetail
			return m, loadGraphRelated(m.deps, m.SelectedObservation.ID)
		}
	case "esc", "q":
		m.Screen = m.PrevScreen
		m.Cursor = m.PrevCursor
		m.DetailScroll = 0
		return m, m.refreshScreen(m.PrevScreen)
	}
	return m, nil
}

// ─── Timeline ───────────────────────────────────────────────────────────────

func (m Model) handleTimelineKeys(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.Scroll > 0 {
			m.Scroll--
		}
	case "down", "j":
		m.Scroll++
		if m.Scroll > maxScrollOffset {
			m.Scroll = maxScrollOffset
		}
	case "esc", "q":
		m.Screen = m.PrevScreen
		m.Cursor = 0
		m.Scroll = 0
		return m, m.refreshScreen(m.PrevScreen)
	}
	return m, nil
}

// ─── Sessions ───────────────────────────────────────────────────────────────

func (m Model) handleSessionsKeys(key string) (tea.Model, tea.Cmd) {
	visibleItems := m.Height - 8
	if visibleItems < minVisibleItems+2 {
		visibleItems = minVisibleItems + 2
	}

	switch key {
	case "up", "k":
		if m.Cursor > 0 {
			m.Cursor--
			if m.Cursor < m.Scroll {
				m.Scroll = m.Cursor
			}
		}
	case "down", "j":
		if m.Cursor < len(m.Sessions)-1 {
			m.Cursor++
			if m.Cursor >= m.Scroll+visibleItems {
				m.Scroll = m.Cursor - visibleItems + 1
			}
		}
	case "enter":
		if len(m.Sessions) > 0 && m.Cursor < len(m.Sessions) {
			m.SelectedSessionIdx = m.Cursor
			m.PrevScreen = ScreenSessions
			sessionID := m.Sessions[m.Cursor].Session.ID
			return m, loadSessionObservations(m.deps, sessionID)
		}
	case "esc", "q":
		m.Screen = ScreenDashboard
		m.Cursor = 0
		m.Scroll = 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

// ─── Session Detail ─────────────────────────────────────────────────────────

func (m Model) handleSessionDetailKeys(key string) (tea.Model, tea.Cmd) {
	visibleItems := (m.Height - 12) / linesPerItem
	if visibleItems < minVisibleItems {
		visibleItems = minVisibleItems
	}

	switch key {
	case "up", "k":
		if m.Cursor > 0 {
			m.Cursor--
			if m.Cursor < m.SessionDetailScroll {
				m.SessionDetailScroll = m.Cursor
			}
		}
	case "down", "j":
		if m.Cursor < len(m.SessionObservations)-1 {
			m.Cursor++
			if m.Cursor >= m.SessionDetailScroll+visibleItems {
				m.SessionDetailScroll = m.Cursor - visibleItems + 1
			}
		}
	case "enter":
		if len(m.SessionObservations) > 0 && m.Cursor < len(m.SessionObservations) {
			obsID := m.SessionObservations[m.Cursor].ID
			m.PrevScreen = ScreenSessionDetail
			return m, loadObservationDetail(m.deps, obsID)
		}
	case "t":
		if len(m.SessionObservations) > 0 && m.Cursor < len(m.SessionObservations) {
			obsID := m.SessionObservations[m.Cursor].ID
			m.PrevScreen = ScreenSessionDetail
			return m, loadTimeline(m.deps, obsID)
		}
	case "esc", "q":
		m.Screen = ScreenSessions
		m.Cursor = m.SelectedSessionIdx
		m.SessionDetailScroll = 0
		return m, loadRecentSessions(m.deps)
	}
	return m, nil
}

// ─── Graph (Cortex-exclusive) ───────────────────────────────────────────────

func (m Model) handleGraphKeys(key string) (tea.Model, tea.Cmd) {
	visibleItems := (m.Height - 8) / linesPerItem
	if visibleItems < minVisibleItems {
		visibleItems = minVisibleItems
	}

	switch key {
	case "up", "k":
		if m.Cursor > 0 {
			m.Cursor--
			if m.Cursor < m.Scroll {
				m.Scroll = m.Cursor
			}
		}
	case "down", "j":
		if m.Cursor < len(m.GraphObservations)-1 {
			m.Cursor++
			if m.Cursor >= m.Scroll+visibleItems {
				m.Scroll = m.Cursor - visibleItems + 1
			}
		}
	case "enter":
		if len(m.GraphObservations) > 0 && m.Cursor < len(m.GraphObservations) {
			obsID := m.GraphObservations[m.Cursor].ID
			m.PrevScreen = ScreenGraph
			return m, loadObservationDetail(m.deps, obsID)
		}
	case "r":
		// Re-root graph on selected observation
		if len(m.GraphObservations) > 0 && m.Cursor < len(m.GraphObservations) {
			obsID := m.GraphObservations[m.Cursor].ID
			m.GraphRootID = obsID
			m.Cursor = 0
			m.Scroll = 0
			return m, loadGraphRelated(m.deps, obsID)
		}
	case "esc", "q":
		m.Screen = m.PrevScreen
		m.Cursor = 0
		m.Scroll = 0
		return m, m.refreshScreen(m.PrevScreen)
	}
	return m, nil
}

// ─── Archive (Cortex-exclusive) ─────────────────────────────────────────────

func (m Model) handleArchiveKeys(key string) (tea.Model, tea.Cmd) {
	visibleItems := (m.Height - 8) / linesPerItem
	if visibleItems < minVisibleItems {
		visibleItems = minVisibleItems
	}

	switch key {
	case "up", "k":
		if m.Cursor > 0 {
			m.Cursor--
			if m.Cursor < m.Scroll {
				m.Scroll = m.Cursor
			}
		}
	case "down", "j":
		if m.Cursor < len(m.ArchivedObservations)-1 {
			m.Cursor++
			if m.Cursor >= m.Scroll+visibleItems {
				m.Scroll = m.Cursor - visibleItems + 1
			}
		}
	case "enter":
		if len(m.ArchivedObservations) > 0 && m.Cursor < len(m.ArchivedObservations) {
			obsID := m.ArchivedObservations[m.Cursor].ID
			m.PrevScreen = ScreenArchive
			return m, loadObservationDetail(m.deps, obsID)
		}
	case "esc", "q":
		m.Screen = ScreenDashboard
		m.Cursor = 0
		m.Scroll = 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

// ─── Health (Cortex-exclusive) ───────────────────────────────────────────

func (m Model) handleHealthKeys(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.Scroll > 0 {
			m.Scroll--
		}
	case "down", "j":
		m.Scroll++
		if m.Scroll > maxScrollOffset {
			m.Scroll = maxScrollOffset
		}
	case "esc", "q":
		m.Screen = ScreenDashboard
		m.Cursor = 0
		m.Scroll = 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

// ─── Setup ──────────────────────────────────────────────────────────────────

var installAgentFn = setup.Install
var addClaudeCodeAllowlistFn = setup.AddClaudeCodeAllowlist

func installAgent(agentName string) tea.Cmd {
	return func() tea.Msg {
		result, err := installAgentFn(agentName)
		return setupInstallMsg{result: result, err: err}
	}
}

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
		case "esc", "q", "enter":
			m.Screen = ScreenDashboard
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
			return m, tea.Batch(m.SetupSpinner.Tick, installAgent(agent.Name))
		}
	case "esc", "q":
		m.Screen = ScreenDashboard
		m.Cursor = 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func (m Model) refreshScreen(screen Screen) tea.Cmd {
	switch screen {
	case ScreenDashboard:
		return loadStats(m.deps)
	case ScreenRecent:
		return loadRecentObservations(m.deps)
	case ScreenSessions:
		return loadRecentSessions(m.deps)
	case ScreenArchive:
		return loadArchivedObservations(m.deps)
	default:
		return nil
	}
}

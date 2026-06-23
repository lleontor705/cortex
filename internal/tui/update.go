package tui

import (
	"fmt"
	"strings"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/setup"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
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
		if m.Screen == ScreenObservationDetail {
			w := m.Width - 4
			if w < 20 {
				w = 20
			}
			h := m.Height - 8
			if h < 5 {
				h = 5
			}
			m.DetailViewport.Width = w
			m.DetailViewport.Height = h
		}
		// Preview pane: disable if too narrow, resize if visible
		if m.Width < 100 {
			m.PreviewVisible = false
		}
		if m.PreviewVisible {
			m.PreviewViewport.Width = m.Width*3/5 - 6
			m.PreviewViewport.Height = m.Height - 10
		}

		// Resize all list components
		w := msg.Width - 4
		if w < 20 {
			w = 20
		}
		if m.PreviewVisible {
			listWidth := m.Width * 2 / 5
			m.SearchListModel.SetSize(listWidth-4, msg.Height-10)
			m.RecentList.SetSize(listWidth-4, msg.Height-8)
		} else {
			m.SearchListModel.SetSize(w, msg.Height-10)
			m.RecentList.SetSize(w, msg.Height-8)
		}
		m.SessionListModel.SetSize(w, msg.Height-8)
		m.GraphListModel.SetSize(w, msg.Height-12)
		m.ArchiveList.SetSize(w, msg.Height-8)
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		// Command palette intercept
		if m.CmdPaletteOpen {
			return m.handleCmdPaletteKeys(msg)
		}
		if m.Screen == ScreenSearch && m.SearchInput.Focused() {
			return m.handleSearchInputKeys(msg)
		}
		if m.Screen == ScreenEmbeddingConfig && m.EmbCfgModel.Focused() {
			return m.handleEmbeddingModelInput(msg)
		}

		// For list screens, pass key messages to list component for navigation
		var listCmd tea.Cmd
		switch m.Screen {
		case ScreenSearchResults:
			if !m.ConfirmDelete {
				m.SearchListModel, listCmd = m.SearchListModel.Update(msg)
			}
		case ScreenRecent:
			if !m.ConfirmDelete {
				m.RecentList, listCmd = m.RecentList.Update(msg)
			}
		case ScreenSessions:
			m.SessionListModel, listCmd = m.SessionListModel.Update(msg)
		case ScreenGraph:
			m.GraphListModel, listCmd = m.GraphListModel.Update(msg)
		case ScreenArchive:
			if !m.ConfirmDelete {
				m.ArchiveList, listCmd = m.ArchiveList.Update(msg)
			}
		}

		// Update preview content if visible (cursor may have changed)
		if m.PreviewVisible {
			m.updatePreviewContent()
		}

		// Then handle action keys
		actionModel, actionCmd := m.handleKeyPress(msg.String())
		if listCmd != nil {
			return actionModel, tea.Batch(listCmd, actionCmd)
		}
		return actionModel, actionCmd

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

	case activityDataMsg:
		if msg.err == nil {
			m.ActivityData = msg.daily
		}
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
		// Populate bubbles/list
		items := make([]list.Item, len(msg.results))
		for i, r := range msg.results {
			items[i] = searchResultItem{result: r}
		}
		m.SearchListModel.SetItems(items)
		w := m.Width - 4
		if w < 20 {
			w = 20
		}
		h := m.Height - 10
		if h < 5 {
			h = 5
		}
		m.SearchListModel.SetSize(w, h)
		return m, nil

	case recentObservationsMsg:
		if msg.err != nil {
			m.ErrorMsg = msg.err.Error()
			return m, nil
		}
		m.RecentObservations = msg.observations
		// Populate bubbles/list
		items := make([]list.Item, len(msg.observations))
		for i, o := range msg.observations {
			items[i] = observationItem{obs: o}
		}
		m.RecentList.SetItems(items)
		w := m.Width - 4
		if w < 20 {
			w = 20
		}
		h := m.Height - 8
		if h < 5 {
			h = 5
		}
		m.RecentList.SetSize(w, h)
		return m, nil

	case observationDetailMsg:
		m.DetailLoading = false
		if msg.err != nil {
			m.ErrorMsg = msg.err.Error()
			return m, nil
		}
		m.SelectedObservation = msg.observation
		m.DetailScore = msg.score
		m.DetailEntities = msg.entities
		m.DetailEdges = msg.edges
		m.Screen = ScreenObservationDetail

		// Build viewport content
		contentStr := buildDetailContent(msg.observation, msg.score, msg.entities, msg.edges)
		w := m.Width - 4
		if w < 20 {
			w = 20
		}
		h := m.Height - 8
		if h < 5 {
			h = 5
		}
		m.DetailViewport = viewport.New(w, h)
		m.DetailViewport.SetContent(contentStr)
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
		// Populate bubbles/list
		items := make([]list.Item, len(msg.sessions))
		for i, s := range msg.sessions {
			items[i] = sessionItem{session: s}
		}
		m.SessionListModel.SetItems(items)
		w := m.Width - 4
		if w < 20 {
			w = 20
		}
		h := m.Height - 8
		if h < 5 {
			h = 5
		}
		m.SessionListModel.SetSize(w, h)
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
		// Populate bubbles/list
		items := make([]list.Item, len(msg.related))
		for i, o := range msg.related {
			edgeLabel := ""
			for _, e := range msg.edges {
				if e.FromObsID == o.ID || e.ToObsID == o.ID {
					edgeLabel = e.RelationType
					break
				}
			}
			items[i] = graphItem{obs: o, edgeLabel: edgeLabel}
		}
		m.GraphListModel.SetItems(items)
		w := m.Width - 4
		if w < 20 {
			w = 20
		}
		h := m.Height - 12
		if h < 5 {
			h = 5
		}
		m.GraphListModel.SetSize(w, h)
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
		// Populate bubbles/list
		items := make([]list.Item, len(msg.observations))
		for i, o := range msg.observations {
			items[i] = observationItem{obs: o}
		}
		m.ArchiveList.SetItems(items)
		w := m.Width - 4
		if w < 20 {
			w = 20
		}
		h := m.Height - 8
		if h < 5 {
			h = 5
		}
		m.ArchiveList.SetSize(w, h)
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

	case configSavedMsg:
		m.EmbCfgSaving = false
		if msg.err != nil {
			m.EmbCfgError = msg.err.Error()
			return m, nil
		}
		m.EmbCfgSaved = true
		m.EmbCfgError = ""
		m.EmbCfgDirty = false
		// Detect provider/model changes for reindex warning
		providerChanged := m.EmbCfgProvider != m.EmbCfgOriginalProvider
		modelChanged := m.EmbCfgModel.Value() != m.EmbCfgOriginalModel
		if providerChanged || modelChanged {
			m.EmbCfgReindexWarning = true
		}
		m.EmbCfgOriginalProvider = m.EmbCfgProvider
		m.EmbCfgOriginalModel = m.EmbCfgModel.Value()
		// If ollama is configured, check its status
		if m.EmbCfgProvider == 1 {
			m.EmbCfgOllamaChecked = false
			return m, checkOllamaStatus(m.deps)
		}
		return m, nil

	case reindexProgressMsg:
		m.EmbCfgReindexing = false
		m.ReindexTotal = msg.total
		m.ReindexDone = msg.indexed
		if msg.err != nil {
			m.EmbCfgError = msg.err.Error()
			return m, nil
		}
		if msg.done {
			m.EmbCfgReindexProgress = msg.progress
			m.EmbCfgReindexWarning = false
		}
		return m, nil

	case deleteObservationMsg:
		if msg.err != nil {
			m.ErrorMsg = msg.err.Error()
			return m, nil
		}
		m.ConfirmDelete = false
		m.ConfirmDeleteID = 0
		m.ToastMessage = fmt.Sprintf("Observation #%d deleted", msg.id)
		m.ToastType = "success"
		return m, m.refreshScreen(m.Screen)

	case unarchiveObservationMsg:
		if msg.err != nil {
			m.ErrorMsg = msg.err.Error()
			return m, nil
		}
		m.ToastMessage = fmt.Sprintf("Observation #%d restored", msg.id)
		m.ToastType = "success"
		return m, loadArchivedObservations(m.deps, m.FilterProject)

	case ollamaStatusMsg:
		m.EmbCfgOllamaChecked = true
		m.EmbCfgOllamaRunning = msg.running
		m.EmbCfgOllamaHasModel = msg.hasModel
		if msg.err != nil {
			m.EmbCfgError = msg.err.Error()
		}
		return m, nil

	case ollamaStartMsg:
		m.EmbCfgStarting = false
		if msg.err != nil {
			m.EmbCfgError = msg.err.Error()
			return m, nil
		}
		m.EmbCfgOllamaRunning = true
		// After starting, check if model exists
		return m, checkOllamaStatus(m.deps)

	case ollamaPullMsg:
		m.EmbCfgPulling = false
		if msg.err != nil {
			m.EmbCfgError = msg.err.Error()
			return m, nil
		}
		m.EmbCfgOllamaHasModel = true
		return m, nil

	case configReloadedMsg:
		m.EmbCfgSaving = false
		m.EmbCfgDirty = false
		if msg.err != nil {
			m.EmbCfgError = msg.err.Error()
			return m, nil
		}
		// Sync TUI state with reloaded config
		if msg.cfg != nil {
			switch msg.cfg.Search.EmbeddingProvider {
			case "ollama":
				m.EmbCfgProvider = 1
			case "openai":
				m.EmbCfgProvider = 2
			default:
				m.EmbCfgProvider = 0
			}
			m.EmbCfgModel.SetValue(msg.cfg.Search.EmbeddingModel)
			m.EmbCfgVector = msg.cfg.Search.Vector
			m.EmbCfgAutoStart = msg.cfg.Search.OllamaAutoStart
		}
		m.EmbCfgSaved = false
		m.EmbCfgError = ""
		m.EmbCfgOllamaChecked = false
		m.ErrorMsg = ""
		// Check Ollama status if ollama is configured
		if m.EmbCfgProvider == 1 {
			return m, checkOllamaStatus(m.deps)
		}
		return m, nil

	case spinner.TickMsg:
		if m.DetailLoading {
			var cmd tea.Cmd
			m.SetupSpinner, cmd = m.SetupSpinner.Update(msg)
			return m, cmd
		}
		if m.SetupInstalling {
			var cmd tea.Cmd
			m.SetupSpinner, cmd = m.SetupSpinner.Update(msg)
			return m, cmd
		}
		if m.EmbCfgPulling || m.EmbCfgStarting || m.EmbCfgSaving || m.EmbCfgReindexing {
			var cmd tea.Cmd
			m.EmbCfgSpinner, cmd = m.EmbCfgSpinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			// Scroll delegated to active component
			switch m.Screen {
			case ScreenObservationDetail:
				var cmd tea.Cmd
				m.DetailViewport, cmd = m.DetailViewport.Update(msg)
				return m, cmd
			}
		}
		return m, nil
	}

	return m, nil
}

// ─── Key Press Router ───────────────────────────────────────────────────────

func (m Model) handleKeyPress(key string) (tea.Model, tea.Cmd) {
	m.ErrorMsg = ""

	// Clear toast on any key press
	if m.ToastMessage != "" {
		m.ToastMessage = ""
		m.ToastType = ""
	}

	// Cancel delete confirmation on any key except y/Y
	if m.ConfirmDelete && key != "y" && key != "Y" {
		m.ConfirmDelete = false
		m.ConfirmDeleteID = 0
		return m, nil
	}

	// Global help key — skip for screens with text input
	if key == "?" && m.Screen != ScreenSearch && m.Screen != ScreenEmbeddingConfig && m.Screen != ScreenHelp {
		m.PrevScreen = m.Screen
		m.Screen = ScreenHelp
		return m, nil
	}

	// Command palette
	if key == "ctrl+k" {
		m.CmdPaletteOpen = true
		m.CmdPaletteCursor = 0
		m.CmdPaletteInput.SetValue("")
		m.CmdPaletteInput.Focus()
		return m, nil
	}


	switch m.Screen {
	case ScreenHelp:
		return m.handleHelpKeys(key)
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
	case ScreenEmbeddingConfig:
		return m.handleEmbeddingConfigKeys(key)
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
	"Embedding settings",
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
		return m, loadRecentObservations(m.deps, m.FilterProject)
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
		return m, loadRecentObservations(m.deps, m.FilterProject)
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
		return m, loadArchivedObservations(m.deps, m.FilterProject)
	case 6: // Embedding settings
		m.PrevScreen = ScreenDashboard
		m.Screen = ScreenEmbeddingConfig
		m.Cursor = 0
		m.EmbCfgFocusField = 0
		m.EmbCfgSaved = false
		m.EmbCfgSaving = false
		m.EmbCfgError = ""
		m.EmbCfgDirty = false
		m.EmbCfgOllamaChecked = false
		m.EmbCfgPulling = false
		m.EmbCfgStarting = false
		m.EmbCfgReindexWarning = false
		m.EmbCfgReindexing = false
		m.EmbCfgReindexProgress = ""
		// Reload config from disk to get latest values
		if m.deps.App != nil {
			_ = m.deps.App.ReloadConfig()
			m.deps.Config = m.deps.App.Config
		}
		// Load current config values into TUI fields
		if m.deps.Config != nil {
			switch m.deps.Config.Search.EmbeddingProvider {
			case "ollama":
				m.EmbCfgProvider = 1
			case "openai":
				m.EmbCfgProvider = 2
			default:
				m.EmbCfgProvider = 0
			}
			m.EmbCfgModel.SetValue(m.deps.Config.Search.EmbeddingModel)
			m.EmbCfgVector = m.deps.Config.Search.Vector
			m.EmbCfgAutoStart = m.deps.Config.Search.OllamaAutoStart
		}
		// Save original values for change detection
		m.EmbCfgOriginalProvider = m.EmbCfgProvider
		m.EmbCfgOriginalModel = m.EmbCfgModel.Value()
		return m, nil
	case 7: // Setup
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
	case 8: // Quit
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
			// Append to search history (max 20 entries)
			m.SearchHistory = append(m.SearchHistory, query)
			if len(m.SearchHistory) > 20 {
				m.SearchHistory = m.SearchHistory[len(m.SearchHistory)-20:]
			}
			m.SearchHistoryIdx = len(m.SearchHistory)
			m.SearchInput.Blur()
			return m, searchMemories(m.deps, query, m.FilterProject)
		}
		return m, nil
	case "up":
		if len(m.SearchHistory) > 0 {
			if m.SearchHistoryIdx > 0 {
				m.SearchHistoryIdx--
			}
			m.SearchInput.SetValue(m.SearchHistory[m.SearchHistoryIdx])
			return m, nil
		}
	case "down":
		if len(m.SearchHistory) > 0 {
			if m.SearchHistoryIdx < len(m.SearchHistory)-1 {
				m.SearchHistoryIdx++
				m.SearchInput.SetValue(m.SearchHistory[m.SearchHistoryIdx])
			} else {
				m.SearchHistoryIdx = len(m.SearchHistory)
				m.SearchInput.SetValue("")
			}
			return m, nil
		}
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
	// Confirm delete
	if m.ConfirmDelete {
		switch key {
		case "y", "Y":
			m.ConfirmDelete = false
			return m, deleteObservationCmd(m.deps, m.ConfirmDeleteID)
		case "n", "N", "esc":
			m.ConfirmDelete = false
		}
		return m, nil
	}

	if key != "g" {
		m.PendingKey = ""
	}

	// Confirm delete
	if m.ConfirmDelete {
		if key == "y" || key == "Y" {
			id := m.ConfirmDeleteID
			m.ConfirmDelete = false
			m.ConfirmDeleteID = 0
			return m, deleteObservationCmd(m.deps, id)
		}
		return m, nil
	}

	switch key {
	case "enter":
		if item, ok := m.SearchListModel.SelectedItem().(searchResultItem); ok {
			m.PrevScreen = ScreenSearchResults
			m.PrevCursor = m.SearchListModel.Index()
			m.DetailLoading = true
			return m, tea.Batch(m.SetupSpinner.Tick, loadObservationDetail(m.deps, item.result.ID))
		}
	case "t":
		if item, ok := m.SearchListModel.SelectedItem().(searchResultItem); ok {
			m.PrevScreen = ScreenSearchResults
			m.PrevCursor = m.SearchListModel.Index()
			return m, loadTimeline(m.deps, item.result.ID)
		}
	case "f":
		if m.Stats != nil && len(m.Stats.Projects) > 0 {
			projects := append([]string{""}, m.Stats.Projects...)
			currentIdx := 0
			for i, p := range projects {
				if p == m.FilterProject {
					currentIdx = i
					break
				}
			}
			m.FilterProject = projects[(currentIdx+1)%len(projects)]
			return m, searchMemories(m.deps, m.SearchQuery, m.FilterProject)
		}
	case "d":
		if item, ok := m.SearchListModel.SelectedItem().(searchResultItem); ok {
			m.ConfirmDelete = true
			m.ConfirmDeleteID = item.result.ID
			m.DeleteTargetTitle = item.result.Title
		}
		return m, nil
	case "/", "s":
		m.PrevScreen = ScreenSearchResults
		m.Screen = ScreenSearch
		m.SearchInput.Focus()
		return m, nil
	case "p":
		if m.Width >= 100 {
			m.PreviewVisible = !m.PreviewVisible
			if m.PreviewVisible {
				m.PreviewViewport = viewport.New(m.Width*3/5-6, m.Height-10)
				m.updatePreviewContent()
				// Shrink list to left pane
				listWidth := m.Width * 2 / 5
				m.SearchListModel.SetSize(listWidth-4, m.Height-10)
			} else {
				// Restore full-width list
				w := m.Width - 4
				if w < 20 {
					w = 20
				}
				m.SearchListModel.SetSize(w, m.Height-10)
			}
		}
		return m, nil
	case "esc", "q":
		m.PreviewVisible = false
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
	// Confirm delete
	if m.ConfirmDelete {
		switch key {
		case "y", "Y":
			m.ConfirmDelete = false
			return m, deleteObservationCmd(m.deps, m.ConfirmDeleteID)
		case "n", "N", "esc":
			m.ConfirmDelete = false
		}
		return m, nil
	}

	if key != "g" {
		m.PendingKey = ""
	}

	switch key {
	}

	// Confirm delete
	if m.ConfirmDelete {
		if key == "y" || key == "Y" {
			id := m.ConfirmDeleteID
			m.ConfirmDelete = false
			m.ConfirmDeleteID = 0
			return m, deleteObservationCmd(m.deps, id)
		}
		return m, nil
	}

	switch key {
	case "enter":
		if item, ok := m.RecentList.SelectedItem().(observationItem); ok {
			m.PrevScreen = ScreenRecent
			m.PrevCursor = m.RecentList.Index()
			m.DetailLoading = true
			return m, tea.Batch(m.SetupSpinner.Tick, loadObservationDetail(m.deps, item.obs.ID))
		}
	case "t":
		if item, ok := m.RecentList.SelectedItem().(observationItem); ok {
			m.PrevScreen = ScreenRecent
			m.PrevCursor = m.RecentList.Index()
			return m, loadTimeline(m.deps, item.obs.ID)
		}
	case "d":
		if item, ok := m.RecentList.SelectedItem().(observationItem); ok {
			m.ConfirmDelete = true
			m.ConfirmDeleteID = item.obs.ID
			m.DeleteTargetTitle = item.obs.Title
		}
		return m, nil
	case "f":
		if m.Stats != nil && len(m.Stats.Projects) > 0 {
			projects := append([]string{""}, m.Stats.Projects...)
			currentIdx := 0
			for i, p := range projects {
				if p == m.FilterProject {
					currentIdx = i
					break
				}
			}
			m.FilterProject = projects[(currentIdx+1)%len(projects)]
			return m, loadRecentObservations(m.deps, m.FilterProject)
		}
	case "p":
		if m.Width >= 100 {
			m.PreviewVisible = !m.PreviewVisible
			if m.PreviewVisible {
				m.PreviewViewport = viewport.New(m.Width*3/5-6, m.Height-10)
				m.updatePreviewContent()
				// Shrink list to left pane
				listWidth := m.Width * 2 / 5
				m.RecentList.SetSize(listWidth-4, m.Height-8)
			} else {
				// Restore full-width list
				w := m.Width - 4
				if w < 20 {
					w = 20
				}
				m.RecentList.SetSize(w, m.Height-8)
			}
		}
		return m, nil
	case "esc", "q":
		m.PreviewVisible = false
		m.Screen = ScreenDashboard
		m.Cursor = 0
		m.Scroll = 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

// ─── Observation Detail ─────────────────────────────────────────────────────

func (m Model) handleObservationDetailKeys(key string) (tea.Model, tea.Cmd) {
	if m.ConfirmDelete {
		switch key {
		case "y", "Y":
			m.ConfirmDelete = false
			return m, deleteObservationCmd(m.deps, m.ConfirmDeleteID)
		case "n", "N", "esc":
			m.ConfirmDelete = false
		}
		return m, nil
	}

	switch key {
	case "up", "k":
		m.DetailViewport.ScrollUp(1)
	case "down", "j":
		m.DetailViewport.ScrollDown(1)
	case "pgup":
		m.DetailViewport.HalfPageUp()
	case "pgdown":
		m.DetailViewport.HalfPageDown()
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
	case "s", "S":
		if m.SelectedObservation != nil && m.SelectedObservation.SessionID != "" {
			m.PrevScreen = ScreenObservationDetail
			m.Screen = ScreenSessionDetail
			m.Cursor = 0
			m.SessionDetailScroll = 0
			return m, loadSessionObservations(m.deps, m.SelectedObservation.SessionID)
		}
	case "d":
		if m.SelectedObservation != nil {
			m.ConfirmDelete = true
			m.ConfirmDeleteID = m.SelectedObservation.ID
			m.DeleteTargetTitle = m.SelectedObservation.Title
		}
		return m, nil
	case "esc", "q":
		m.Screen = m.PrevScreen
		m.Cursor = m.PrevCursor
		m.DetailViewport.GotoTop()
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
	switch key {
	case "enter":
		if item, ok := m.SessionListModel.SelectedItem().(sessionItem); ok {
			m.SelectedSessionIdx = m.SessionListModel.Index()
			m.PrevScreen = ScreenSessions
			return m, loadSessionObservations(m.deps, item.session.Session.ID)
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
			m.DetailLoading = true
			return m, tea.Batch(m.SetupSpinner.Tick, loadObservationDetail(m.deps, obsID))
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
	switch key {
	case "enter":
		if item, ok := m.GraphListModel.SelectedItem().(graphItem); ok {
			m.PrevScreen = ScreenGraph
			m.DetailLoading = true
			return m, tea.Batch(m.SetupSpinner.Tick, loadObservationDetail(m.deps, item.obs.ID))
		}
	case "r":
		// Re-root graph on selected observation
		if item, ok := m.GraphListModel.SelectedItem().(graphItem); ok {
			m.GraphRootID = item.obs.ID
			m.Cursor = 0
			m.Scroll = 0
			return m, loadGraphRelated(m.deps, item.obs.ID)
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
	if m.ConfirmDelete {
		switch key {
		case "y", "Y":
			m.ConfirmDelete = false
			return m, deleteObservationCmd(m.deps, m.ConfirmDeleteID)
		case "n", "N", "esc":
			m.ConfirmDelete = false
		}
		return m, nil
	}

	if key != "g" {
		m.PendingKey = ""
	}

	switch key {
	case "enter":
		if item, ok := m.ArchiveList.SelectedItem().(observationItem); ok {
			m.PrevScreen = ScreenArchive
			m.DetailLoading = true
			return m, tea.Batch(m.SetupSpinner.Tick, loadObservationDetail(m.deps, item.obs.ID))
		}
	case "u":
		if item, ok := m.ArchiveList.SelectedItem().(observationItem); ok {
			return m, unarchiveObservationCmd(m.deps, item.obs.ID)
		}
	case "d":
		if item, ok := m.ArchiveList.SelectedItem().(observationItem); ok {
			m.ConfirmDelete = true
			m.ConfirmDeleteID = item.obs.ID
			m.DeleteTargetTitle = item.obs.Title
		}
		return m, nil
	case "f":
		if m.Stats != nil && len(m.Stats.Projects) > 0 {
			projects := append([]string{""}, m.Stats.Projects...)
			currentIdx := 0
			for i, p := range projects {
				if p == m.FilterProject {
					currentIdx = i
					break
				}
			}
			m.FilterProject = projects[(currentIdx+1)%len(projects)]
			return m, loadArchivedObservations(m.deps, m.FilterProject)
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
	case "tab":
		m.HealthSection = (m.HealthSection + 1) % 3
		m.HealthExpanded = false
		m.Scroll = 0
	case "enter":
		m.HealthExpanded = !m.HealthExpanded
		m.Scroll = 0
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

// ──�� Embedding Config ───────────────────────────────────────────────────────

func (m Model) handleEmbeddingModelInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "esc":
		m.EmbCfgModel.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.EmbCfgModel, cmd = m.EmbCfgModel.Update(msg)
	return m, cmd
}

func (m Model) handleEmbeddingConfigKeys(key string) (tea.Model, tea.Cmd) {
	// If async operation running, only allow esc
	if m.EmbCfgPulling || m.EmbCfgStarting || m.EmbCfgSaving || m.EmbCfgReindexing {
		return m, nil
	}

	// Post-save: reindex key available when warning is shown
	if m.EmbCfgSaved && m.EmbCfgReindexWarning && !m.EmbCfgReindexing {
		if key == "x" || key == "X" {
			m.EmbCfgReindexing = true
			m.EmbCfgReindexWarning = false
			m.EmbCfgError = ""
			return m, tea.Batch(m.EmbCfgSpinner.Tick, startReindexCmd(m.deps))
		}
	}

	// Post-save Ollama actions
	if m.EmbCfgSaved && m.EmbCfgProvider == 1 && m.EmbCfgOllamaChecked {
		switch key {
		case "s", "S":
			if !m.EmbCfgOllamaRunning {
				m.EmbCfgStarting = true
				m.EmbCfgError = ""
				baseURL := ""
				if m.deps.Config != nil {
					baseURL = m.deps.Config.Search.EmbeddingBaseURL
				}
				return m, tea.Batch(m.EmbCfgSpinner.Tick, startOllamaCmd(baseURL))
			}
		case "p", "P":
			if m.EmbCfgOllamaRunning && !m.EmbCfgOllamaHasModel {
				m.EmbCfgPulling = true
				m.EmbCfgError = ""
				baseURL := ""
				model := m.EmbCfgModel.Value()
				if m.deps.Config != nil {
					baseURL = m.deps.Config.Search.EmbeddingBaseURL
				}
				return m, tea.Batch(m.EmbCfgSpinner.Tick, pullOllamaModelCmd(baseURL, model))
			}
		case "esc", "q", "enter":
			m.Screen = ScreenDashboard
			m.Cursor = 0
			return m, loadStats(m.deps)
		}
		return m, nil
	}

	// Post-save (non-ollama or not yet checked)
	if m.EmbCfgSaved {
		switch key {
		case "esc", "q", "enter":
			m.Screen = ScreenDashboard
			m.Cursor = 0
			return m, loadStats(m.deps)
		}
		return m, nil
	}

	// Reload config from disk (r key works in any non-editing state)
	if key == "r" || key == "R" {
		m.EmbCfgSaving = true
		m.EmbCfgError = ""
		return m, tea.Batch(m.EmbCfgSpinner.Tick, reloadConfigCmd(m.deps))
	}

	maxField := 4 // provider(0), model(1), vector(2), autostart(3), save(4)

	switch key {
	case "up", "k":
		if m.EmbCfgFocusField > 0 {
			m.EmbCfgFocusField--
		}
	case "down", "j":
		if m.EmbCfgFocusField < maxField {
			m.EmbCfgFocusField++
		}
	case "left", "h":
		if m.EmbCfgFocusField == 0 {
			m.EmbCfgProvider = (m.EmbCfgProvider + 2) % 3 // cycle left
			m.EmbCfgDirty = true
		}
	case "right", "l":
		if m.EmbCfgFocusField == 0 {
			m.EmbCfgProvider = (m.EmbCfgProvider + 1) % 3 // cycle right
			m.EmbCfgDirty = true
		}
	case " ":
		switch m.EmbCfgFocusField {
		case 2:
			m.EmbCfgVector = !m.EmbCfgVector
			m.EmbCfgDirty = true
		case 3:
			m.EmbCfgAutoStart = !m.EmbCfgAutoStart
			m.EmbCfgDirty = true
		}
	case "enter":
		switch m.EmbCfgFocusField {
		case 1: // Focus model text input
			m.EmbCfgModel.Focus()
			return m, nil
		case 4: // Save
			m.EmbCfgSaving = true
			m.EmbCfgSaved = false
			m.EmbCfgError = ""
			return m, tea.Batch(
				m.EmbCfgSpinner.Tick,
				saveEmbeddingConfig(m.deps, m.EmbCfgProvider, m.EmbCfgModel.Value(), m.EmbCfgVector, m.EmbCfgAutoStart),
			)
		}
	case "esc", "q":
		m.Screen = ScreenDashboard
		m.Cursor = 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

// ─── Help ──────────────────────────────────────────────────────────────────

func (m Model) handleHelpKeys(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q", "?":
		m.Screen = m.PrevScreen
		return m, nil
	}
	return m, nil
}

// ─── Command Palette ──────────────────────────────────────────────────────

type paletteCommand struct {
	name     string
	shortcut string
	execute  func(m Model) (Model, tea.Cmd)
}

func allCommands() []paletteCommand {
	return []paletteCommand{
		{"Search memories", "/", func(m Model) (Model, tea.Cmd) {
			m.Screen = ScreenSearch
			m.SearchInput.Focus()
			return m, nil
		}},
		{"Recent observations", "", func(m Model) (Model, tea.Cmd) {
			m.Screen = ScreenRecent
			return m, loadRecentObservations(m.deps, m.FilterProject)
		}},
		{"Browse sessions", "", func(m Model) (Model, tea.Cmd) {
			m.Screen = ScreenSessions
			return m, loadRecentSessions(m.deps)
		}},
		{"Knowledge graph", "", func(m Model) (Model, tea.Cmd) {
			m.Screen = ScreenGraph
			return m, nil
		}},
		{"Memory health", "", func(m Model) (Model, tea.Cmd) {
			m.Screen = ScreenHealth
			return m, loadHealthData(m.deps, "")
		}},
		{"Archived observations", "", func(m Model) (Model, tea.Cmd) {
			m.Screen = ScreenArchive
			return m, loadArchivedObservations(m.deps, m.FilterProject)
		}},
		{"Embedding settings", "", func(m Model) (Model, tea.Cmd) {
			m.Screen = ScreenEmbeddingConfig
			return m, nil
		}},
		{"Setup agent plugin", "", func(m Model) (Model, tea.Cmd) {
			m.Screen = ScreenSetup
			return m, nil
		}},
		{"Help / Keyboard shortcuts", "?", func(m Model) (Model, tea.Cmd) {
			m.Screen = ScreenHelp
			return m, nil
		}},
		{"Go to Dashboard", "", func(m Model) (Model, tea.Cmd) {
			m.Screen = ScreenDashboard
			return m, loadStats(m.deps)
		}},
	}
}

func (m Model) filteredCommands() []paletteCommand {
	query := strings.ToLower(m.CmdPaletteInput.Value())
	if query == "" {
		return allCommands()
	}
	var filtered []paletteCommand
	for _, cmd := range allCommands() {
		if strings.Contains(strings.ToLower(cmd.name), query) {
			filtered = append(filtered, cmd)
		}
	}
	return filtered
}

func (m Model) handleCmdPaletteKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.CmdPaletteOpen = false
		m.CmdPaletteInput.Blur()
		return m, nil
	case "up", "ctrl+p":
		if m.CmdPaletteCursor > 0 {
			m.CmdPaletteCursor--
		}
		return m, nil
	case "down", "ctrl+n":
		filtered := m.filteredCommands()
		if m.CmdPaletteCursor < len(filtered)-1 {
			m.CmdPaletteCursor++
		}
		return m, nil
	case "enter":
		filtered := m.filteredCommands()
		if len(filtered) > 0 && m.CmdPaletteCursor < len(filtered) {
			cmd := filtered[m.CmdPaletteCursor]
			m.CmdPaletteOpen = false
			m.CmdPaletteInput.Blur()
			return cmd.execute(m)
		}
		return m, nil
	}
	// Pass to text input for filtering
	var cmd tea.Cmd
	m.CmdPaletteInput, cmd = m.CmdPaletteInput.Update(msg)
	m.CmdPaletteCursor = 0 // reset cursor on filter change
	return m, cmd
}


// ─── Detail Content Builder ────────────────────────────────────────────────

// buildDetailContent constructs the full text content for the observation
// detail viewport. This is called once when the observation loads, and
// the viewport handles scrolling from there.
func buildDetailContent(obs *domain.Observation, score *domain.ImportanceScore, entities []*domain.EntityLink, edges []*domain.Edge) string {
	var content strings.Builder

	fmt.Fprintf(&content, "Type:       %s\n", obs.Type)
	fmt.Fprintf(&content, "Title:      %s\n", obs.Title)
	fmt.Fprintf(&content, "Session:    %s\n", obs.SessionID)
	fmt.Fprintf(&content, "Created:    %s\n", obs.CreatedAt.Format("2006-01-02 15:04"))
	if obs.Project != "" {
		fmt.Fprintf(&content, "Project:    %s\n", obs.Project)
	}

	if score != nil {
		fmt.Fprintf(&content, "Score:      %.1f/5.0  (accessed %d times)\n", score.Score, score.AccessCount)
	}

	if len(entities) > 0 {
		content.WriteString("\n── Entities ──\n")
		for _, e := range entities {
			fmt.Fprintf(&content, "  [%s] %s\n", e.EntityType, e.EntityValue)
		}
	}

	if len(edges) > 0 {
		fmt.Fprintf(&content, "\n── Related (%d links) ──\n", len(edges))
		for _, e := range edges {
			targetID := e.ToObsID
			if targetID == obs.ID {
				targetID = e.FromObsID
			}
			fmt.Fprintf(&content, "  [%s] #%d  weight: %.1f\n", e.RelationType, targetID, e.Weight)
		}
	}

	content.WriteString("\n── Content ──\n")
	content.WriteString(obs.Content)

	return content.String()
}


// ─── Helpers ────────────────────────────────────────────────────────────────

// updatePreviewContent sets the preview viewport content based on the
// currently selected item in the active list screen.
func (m *Model) updatePreviewContent() {
	var content string
	switch m.Screen {
	case ScreenSearchResults:
		if item, ok := m.SearchListModel.SelectedItem().(searchResultItem); ok {
			r := item.result
			content = fmt.Sprintf("Title: %s\nType: %s\nProject: %s\nCreated: %s\nScore: %.0f%%\n\n%s",
				r.Title, r.Type, r.Project, formatTime(r.CreatedAt), r.Rank*100, r.Content)
		}
	case ScreenRecent:
		if item, ok := m.RecentList.SelectedItem().(observationItem); ok {
			o := item.obs
			content = fmt.Sprintf("Title: %s\nType: %s\nProject: %s\nCreated: %s\n\n%s",
				o.Title, o.Type, o.Project, formatTime(o.CreatedAt), o.Content)
		}
	}
	m.PreviewViewport.SetContent(content)
	m.PreviewViewport.GotoTop()
}

func (m Model) refreshScreen(screen Screen) tea.Cmd {
	switch screen {
	case ScreenDashboard:
		return loadStats(m.deps)
	case ScreenRecent:
		return loadRecentObservations(m.deps, m.FilterProject)
	case ScreenSearchResults:
		if m.SearchQuery != "" {
			return searchMemories(m.deps, m.SearchQuery, m.FilterProject)
		}
		return nil
	case ScreenSessions:
		return loadRecentSessions(m.deps)
	case ScreenArchive:
		return loadArchivedObservations(m.deps, m.FilterProject)
	default:
		return nil
	}
}

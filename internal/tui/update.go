package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/setup"
	"github.com/lleontor705/cortex/v2/internal/update"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	minVisibleItems = 3   // Minimum items shown in any list
	linesPerItem    = 2   // Lines per observation item (title + preview)
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
		// Quick memory modal intercept
		if m.NewObsModalOpen {
			return m.handleNewObsModalKeys(msg)
		}
		// Auth token modal intercept
		if m.AuthModalOpen {
			return m.handleAuthModalKeys(msg)
		}
		if m.Screen == ScreenSearch && m.SearchInput.Focused() {
			return m.handleSearchInputKeys(msg)
		}
		if m.Screen == ScreenEmbeddingConfig && m.EmbCfgModel.Focused() {
			return m.handleEmbeddingModelInput(msg)
		}
		if m.Screen == ScreenLocalConfig && m.localConfigInputFocused() {
			return m.handleLocalConfigInput(msg)
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
	case updateFinishedMsg:
		if msg.err != nil {
			m.ToastMessage = fmt.Sprintf("Update failed: %v", msg.err)
			m.ToastType = "error"
		} else if msg.result != nil {
			m.ToastMessage = fmt.Sprintf("✔ Updated Cortex to %s! Please restart.", msg.result.Latest)
			m.ToastType = "success"
			m.UpdateResult = nil
		}
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

	case localConfigSavedMsg:
		m.LocalCfgSaving = false
		if msg.err != nil {
			m.LocalCfgError = msg.err.Error()
			return m, nil
		}
		m.LocalCfgSaved, m.LocalCfgDirty, m.LocalCfgError = true, false, ""
		m.LocalCfgFocusField = 11
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

	case observationCreatedMsg:
		if msg.err != nil {
			m.ToastMessage = "Failed to create memory: " + msg.err.Error()
			m.ToastType = "error"
			return m, nil
		}
		m.ToastMessage = "Memory created: " + msg.observation.Title
		m.ToastType = "success"
		return m, tea.Batch(loadStats(m.deps), loadRecentObservations(m.deps, m.FilterProject))

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
		if m.LocalCfgSaving {
			var cmd tea.Cmd
			m.LocalCfgSpinner, cmd = m.LocalCfgSpinner.Update(msg)
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
	if key == "?" && m.Screen != ScreenSearch && m.Screen != ScreenEmbeddingConfig && m.Screen != ScreenLocalConfig && m.Screen != ScreenHelp {
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

	// Quick memory creation modal — available from list and dashboard screens
	if (key == "n" || key == "N") && m.Screen != ScreenSearch && m.Screen != ScreenEmbeddingConfig && m.Screen != ScreenLocalConfig && m.Screen != ScreenHelp {
		m.NewObsModalOpen = true
		m.NewObsFocusField = 0
		m.NewObsTitleInput.SetValue("")
		m.NewObsContentInput.SetValue("")
		m.NewObsTypeInput.SetValue("decision")
		m.NewObsProjectInput.SetValue(m.FilterProject)
		m.NewObsTitleInput.Focus()
		return m, textinput.Blink
	}

	// Theme toggle (Light / Dark mode)
	if (key == "t" || key == "T") && m.Screen != ScreenSearch && m.Screen != ScreenEmbeddingConfig && m.Screen != ScreenLocalConfig && m.Screen != ScreenHelp {
		dark := ToggleTheme()
		m.IsDarkTheme = dark
		if dark {
			m.ToastMessage = "Theme: Dark Mode activated"
		} else {
			m.ToastMessage = "Theme: Light Mode activated"
		}
		m.ToastType = "success"
		return m, nil
	}

	// Auth token / connect server modal
	if key == "L" && m.Screen != ScreenSearch && m.Screen != ScreenEmbeddingConfig && m.Screen != ScreenLocalConfig && m.Screen != ScreenHelp {
		m.openAuthModal()
		return m, textinput.Blink
	}

	// Stats view toggle (Personal vs Admin Global)
	if (key == "u" || key == "U") && m.Screen == ScreenDashboard {
		if m.StatsMode == 0 {
			m.StatsMode = 1
			m.ToastMessage = "Dashboard: Admin Global View"
		} else {
			m.StatsMode = 0
			m.ToastMessage = "Dashboard: User Personal View"
		}
		m.ToastType = "info"
		return m, nil
	}

	// Project upload / sync toggle
	if key == "P" && (m.Screen == ScreenDashboard || m.Screen == ScreenLocalConfig) {
		m.UploadToCortex = !m.UploadToCortex
		if m.UploadToCortex {
			m.ToastMessage = "Project sync: Upload to Cortex enabled"
		} else {
			m.ToastMessage = "Project sync: Local only (upload disabled)"
		}
		m.ToastType = "info"
		return m, nil
	}

	// Project filter cycler
	if key == "p" && (m.Screen == ScreenDashboard || m.Screen == ScreenRecent || m.Screen == ScreenSearchResults || m.Screen == ScreenGraph || m.Screen == ScreenSessions) {
		return m.cycleProjectFilter()
	}

	// Split preview toggle
	if (key == "v" || key == "V") && (m.Screen == ScreenRecent || m.Screen == ScreenSearchResults || m.Screen == ScreenArchive) {
		m.PreviewVisible = !m.PreviewVisible
		if m.PreviewVisible {
			m.updatePreviewContent()
		}
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
	case ScreenLocalConfig:
		return m.handleLocalConfigKeys(key)
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
	"Local settings",
	"Connect to server",
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

type updateFinishedMsg struct {
	result *update.Result
	err    error
}

func (m Model) performSelfUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		res, err := update.SelfUpdate(m.Version, nil)
		return updateFinishedMsg{result: res, err: err}
	}
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
	case 7: // Local settings
		return m.openLocalConfig(), nil
	case 8: // Connect to server
		m.openAuthModal()
		return m, textinput.Blink
	case 9: // Setup
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
	case 10: // Quit
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

func (m Model) openLocalConfig() Model {
	m.PrevScreen, m.Screen, m.LocalCfgFocusField = ScreenDashboard, ScreenLocalConfig, 0
	m.LocalCfgDirty, m.LocalCfgSaving, m.LocalCfgSaved, m.LocalCfgError = false, false, false, ""
	if m.deps == nil || m.deps.Config == nil {
		m.LocalCfgError = "config not available"
		return m
	}
	cfg := m.deps.Config
	m.LocalCfgDatabasePath.SetValue(cfg.Database.Path)
	m.LocalCfgHTTPEnabled = cfg.HTTP.Enabled
	m.LocalCfgHTTPHost.SetValue(cfg.HTTP.Host)
	m.LocalCfgHTTPPort.SetValue(fmt.Sprintf("%d", cfg.HTTP.Port))
	m.LocalCfgMCPRemote = cfg.MCP.Remote.Enabled
	m.LocalCfgMCPURL.SetValue(cfg.MCP.Remote.URL)
	m.LocalCfgMCPTokenEnv.SetValue(cfg.MCP.Remote.TokenEnv)
	m.LocalCfgSyncEnabled = cfg.Sync.Enabled
	m.LocalCfgSyncURL.SetValue(cfg.Sync.URL)
	m.LocalCfgSyncTokenEnv.SetValue(cfg.Sync.TokenEnv)
	m.LocalCfgSyncInterval.SetValue(cfg.Sync.Interval.String())
	return m
}

func (m Model) localConfigInputFocused() bool {
	switch m.LocalCfgFocusField {
	case 0:
		return m.LocalCfgDatabasePath.Focused()
	case 3:
		return m.LocalCfgLLMModel.Focused()
	case 4:
		return m.LocalCfgLLMBaseURL.Focused()
	case 6:
		return m.LocalCfgHTTPHost.Focused()
	case 7:
		return m.LocalCfgHTTPPort.Focused()
	case 9:
		return m.LocalCfgMCPURL.Focused()
	case 10:
		return m.LocalCfgMCPTokenEnv.Focused()
	case 12:
		return m.LocalCfgSyncURL.Focused()
	case 13:
		return m.LocalCfgSyncTokenEnv.Focused()
	case 14:
		return m.LocalCfgSyncInterval.Focused()
	default:
		return false
	}
}

func (m Model) handleLocalConfigInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "enter" || msg.String() == "esc" {
		switch m.LocalCfgFocusField {
		case 0:
			m.LocalCfgDatabasePath.Blur()
		case 3:
			m.LocalCfgLLMModel.Blur()
		case 4:
			m.LocalCfgLLMBaseURL.Blur()
		case 6:
			m.LocalCfgHTTPHost.Blur()
		case 7:
			m.LocalCfgHTTPPort.Blur()
		case 9:
			m.LocalCfgMCPURL.Blur()
		case 10:
			m.LocalCfgMCPTokenEnv.Blur()
		case 12:
			m.LocalCfgSyncURL.Blur()
		case 13:
			m.LocalCfgSyncTokenEnv.Blur()
		case 14:
			m.LocalCfgSyncInterval.Blur()
		}
		return m, nil
	}
	m.LocalCfgDirty = true
	var cmd tea.Cmd
	switch m.LocalCfgFocusField {
	case 0:
		m.LocalCfgDatabasePath, cmd = m.LocalCfgDatabasePath.Update(msg)
	case 3:
		m.LocalCfgLLMModel, cmd = m.LocalCfgLLMModel.Update(msg)
	case 4:
		m.LocalCfgLLMBaseURL, cmd = m.LocalCfgLLMBaseURL.Update(msg)
	case 6:
		m.LocalCfgHTTPHost, cmd = m.LocalCfgHTTPHost.Update(msg)
	case 7:
		m.LocalCfgHTTPPort, cmd = m.LocalCfgHTTPPort.Update(msg)
	case 9:
		m.LocalCfgMCPURL, cmd = m.LocalCfgMCPURL.Update(msg)
	case 10:
		m.LocalCfgMCPTokenEnv, cmd = m.LocalCfgMCPTokenEnv.Update(msg)
	case 12:
		m.LocalCfgSyncURL, cmd = m.LocalCfgSyncURL.Update(msg)
	case 13:
		m.LocalCfgSyncTokenEnv, cmd = m.LocalCfgSyncTokenEnv.Update(msg)
	case 14:
		m.LocalCfgSyncInterval, cmd = m.LocalCfgSyncInterval.Update(msg)
	}
	return m, cmd
}

func (m Model) handleLocalConfigKeys(key string) (tea.Model, tea.Cmd) {
	if m.LocalCfgSaving {
		return m, nil
	}
	if m.LocalCfgSaved {
		if key == "esc" || key == "q" || key == "enter" {
			m.Screen, m.Cursor = ScreenDashboard, 0
			return m, loadStats(m.deps)
		}
		return m, nil
	}
	switch key {
	case "shift+tab":
		m.LocalCfgFocusField = localConfigSectionStart(max(0, localConfigSection(m.LocalCfgFocusField)-1))
	case "tab":
		m.LocalCfgFocusField = localConfigSectionStart(min(5, localConfigSection(m.LocalCfgFocusField)+1))
	case "left", "h":
		switch m.LocalCfgFocusField {
		case 1:
			m.LocalCfgFormat = (m.LocalCfgFormat + 2) % 3
			m.LocalCfgDirty = true
		case 2:
			m.LocalCfgLLMProvider = (m.LocalCfgLLMProvider + 7) % 8
			m.LocalCfgDirty = true
		default:
			m.LocalCfgFocusField = localConfigSectionStart(max(0, localConfigSection(m.LocalCfgFocusField)-1))
		}
	case "right", "l":
		switch m.LocalCfgFocusField {
		case 1:
			m.LocalCfgFormat = (m.LocalCfgFormat + 1) % 3
			m.LocalCfgDirty = true
		case 2:
			m.LocalCfgLLMProvider = (m.LocalCfgLLMProvider + 1) % 8
			m.LocalCfgDirty = true
		default:
			m.LocalCfgFocusField = localConfigSectionStart(min(5, localConfigSection(m.LocalCfgFocusField)+1))
		}
	case "up", "k":
		if m.LocalCfgFocusField > 0 {
			m.LocalCfgFocusField--
		}
	case "down", "j":
		if m.LocalCfgFocusField < 15 {
			m.LocalCfgFocusField++
		}
	case " ":
		switch m.LocalCfgFocusField {
		case 1:
			m.LocalCfgFormat = (m.LocalCfgFormat + 1) % 3
		case 2:
			m.LocalCfgLLMProvider = (m.LocalCfgLLMProvider + 1) % 8
		case 5:
			m.LocalCfgHTTPEnabled = !m.LocalCfgHTTPEnabled
		case 8:
			m.LocalCfgMCPRemote = !m.LocalCfgMCPRemote
		case 11:
			m.LocalCfgSyncEnabled = !m.LocalCfgSyncEnabled
		default:
			return m, nil
		}
		m.LocalCfgDirty = true
	case "enter":
		switch m.LocalCfgFocusField {
		case 0:
			m.LocalCfgDatabasePath.Focus()
		case 1:
			m.LocalCfgFormat = (m.LocalCfgFormat + 1) % 3
			m.LocalCfgDirty = true
		case 2:
			m.LocalCfgLLMProvider = (m.LocalCfgLLMProvider + 1) % 8
			m.LocalCfgDirty = true
		case 3:
			m.LocalCfgLLMModel.Focus()
		case 4:
			m.LocalCfgLLMBaseURL.Focus()
		case 6:
			m.LocalCfgHTTPHost.Focus()
		case 7:
			m.LocalCfgHTTPPort.Focus()
		case 9:
			m.LocalCfgMCPURL.Focus()
		case 10:
			m.LocalCfgMCPTokenEnv.Focus()
		case 12:
			m.LocalCfgSyncURL.Focus()
		case 13:
			m.LocalCfgSyncTokenEnv.Focus()
		case 14:
			m.LocalCfgSyncInterval.Focus()
		case 15:
			return m.startLocalConfigSave()
		}
		return m, nil
	case "s", "S":
		return m.startLocalConfigSave()
	case "r", "R":
		return m.openLocalConfig(), nil
	case "esc", "q":
		m.Screen, m.Cursor = ScreenDashboard, 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

func localConfigSection(field int) int {
	switch {
	case field <= 1:
		return 0 // Storage (path, format)
	case field <= 4:
		return 1 // AI & LLM (provider, model, base_url)
	case field <= 7:
		return 2 // HTTP API (enabled, host, port)
	case field <= 10:
		return 3 // MCP (remote, url, token_env)
	case field <= 14:
		return 4 // Sync (enabled, url, token_env, interval)
	default:
		return 5 // Review & Save (save button)
	}
}

func localConfigSectionStart(section int) int {
	return []int{0, 2, 5, 8, 11, 15}[section]
}

func (m Model) startLocalConfigSave() (tea.Model, tea.Cmd) {
	m.LocalCfgSaving, m.LocalCfgSaved, m.LocalCfgError = true, false, ""
	values := localConfigValues{
		databasePath: m.LocalCfgDatabasePath.Value(),
		format:       m.LocalCfgFormat,
		llmProvider:  m.LocalCfgLLMProvider,
		llmModel:     m.LocalCfgLLMModel.Value(),
		llmBaseURL:   m.LocalCfgLLMBaseURL.Value(),
		httpEnabled:  m.LocalCfgHTTPEnabled,
		httpHost:     m.LocalCfgHTTPHost.Value(),
		httpPort:     m.LocalCfgHTTPPort.Value(),
		mcpRemote:    m.LocalCfgMCPRemote,
		mcpURL:       m.LocalCfgMCPURL.Value(),
		mcpTokenEnv:  m.LocalCfgMCPTokenEnv.Value(),
		syncEnabled:  m.LocalCfgSyncEnabled,
		syncURL:      m.LocalCfgSyncURL.Value(),
		syncTokenEnv: m.LocalCfgSyncTokenEnv.Value(),
		syncInterval: m.LocalCfgSyncInterval.Value(),
	}
	return m, tea.Batch(m.LocalCfgSpinner.Tick, saveLocalConfig(m.deps, values))
}

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
		{"Local settings", "", func(m Model) (Model, tea.Cmd) {
			return m.openLocalConfig(), nil
		}},
		{"Connect to server", "L", func(m Model) (Model, tea.Cmd) {
			m.openAuthModal()
			return m, textinput.Blink
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

func (m Model) cycleProjectFilter() (tea.Model, tea.Cmd) {
	if m.Stats == nil || len(m.Stats.Projects) == 0 {
		m.ToastMessage = "No projects available to filter"
		m.ToastType = "warning"
		return m, nil
	}
	projects := m.Stats.Projects
	if m.FilterProject == "" {
		m.FilterProject = projects[0]
	} else {
		idx := -1
		for i, p := range projects {
			if p == m.FilterProject {
				idx = i
				break
			}
		}
		if idx == -1 || idx == len(projects)-1 {
			m.FilterProject = ""
		} else {
			m.FilterProject = projects[idx+1]
		}
	}
	if m.FilterProject == "" {
		m.ToastMessage = "Filter: All projects"
	} else {
		m.ToastMessage = "Filter project: " + m.FilterProject
	}
	m.ToastType = "success"

	switch m.Screen {
	case ScreenRecent:
		return m, loadRecentObservations(m.deps, m.FilterProject)
	case ScreenSearchResults:
		if m.SearchQuery != "" {
			return m, searchMemories(m.deps, m.SearchQuery, m.FilterProject)
		}
		return m, nil
	case ScreenArchive:
		return m, loadArchivedObservations(m.deps, m.FilterProject)
	default:
		return m, nil
	}
}

func (m Model) handleNewObsModalKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.NewObsModalOpen = false
		return m, nil
	case "tab", "down":
		m.NewObsFocusField = (m.NewObsFocusField + 1) % 5
		m.focusNewObsField()
		return m, textinput.Blink
	case "shift+tab", "up":
		m.NewObsFocusField = (m.NewObsFocusField + 4) % 5
		m.focusNewObsField()
		return m, textinput.Blink
	case "enter":
		if m.NewObsFocusField == 4 {
			return m.submitNewObservation()
		}
		m.NewObsFocusField = (m.NewObsFocusField + 1) % 5
		m.focusNewObsField()
		return m, textinput.Blink
	case "ctrl+s":
		return m.submitNewObservation()
	}

	var cmd tea.Cmd
	switch m.NewObsFocusField {
	case 0:
		m.NewObsTitleInput, cmd = m.NewObsTitleInput.Update(msg)
	case 1:
		m.NewObsContentInput, cmd = m.NewObsContentInput.Update(msg)
	case 2:
		m.NewObsTypeInput, cmd = m.NewObsTypeInput.Update(msg)
	case 3:
		m.NewObsProjectInput, cmd = m.NewObsProjectInput.Update(msg)
	}
	return m, cmd
}

func (m *Model) focusNewObsField() {
	m.NewObsTitleInput.Blur()
	m.NewObsContentInput.Blur()
	m.NewObsTypeInput.Blur()
	m.NewObsProjectInput.Blur()

	switch m.NewObsFocusField {
	case 0:
		m.NewObsTitleInput.Focus()
	case 1:
		m.NewObsContentInput.Focus()
	case 2:
		m.NewObsTypeInput.Focus()
	case 3:
		m.NewObsProjectInput.Focus()
	}
}

func (m Model) submitNewObservation() (tea.Model, tea.Cmd) {
	title := strings.TrimSpace(m.NewObsTitleInput.Value())
	if title == "" {
		m.ToastMessage = "Title cannot be empty"
		m.ToastType = "error"
		return m, nil
	}
	content := strings.TrimSpace(m.NewObsContentInput.Value())
	if content == "" {
		content = title
	}
	typ := strings.TrimSpace(m.NewObsTypeInput.Value())
	if typ == "" {
		typ = "decision"
	}
	project := strings.TrimSpace(m.NewObsProjectInput.Value())
	if project == "" {
		project = "default"
	}

	obs := &domain.Observation{
		Title:      title,
		Content:    content,
		Type:       typ,
		Project:    project,
		Scope:      "project",
		Confidence: 1.0,
		Source:     "manual",
	}

	m.NewObsModalOpen = false
	return m, createObservationCmd(m.deps, obs)
}

func (m *Model) openAuthModal() {
	m.AuthModalOpen = true
	m.AuthFocusField = 0
	m.AuthModeHybrid = true
	if m.deps != nil && m.deps.Config != nil {
		if m.deps.Config.Sync.URL != "" {
			m.AuthServerURLInput.SetValue(m.deps.Config.Sync.URL)
		} else if m.deps.Config.MCP.Remote.URL != "" {
			m.AuthServerURLInput.SetValue(m.deps.Config.MCP.Remote.URL)
		}
		if m.deps.Config.HTTP.Token != "" {
			m.AuthTokenInput.SetValue(m.deps.Config.HTTP.Token)
		}
		if m.deps.Config.MCP.Remote.Enabled {
			m.AuthModeHybrid = false
		}
	}
	m.focusAuthField()
}

func (m *Model) focusAuthField() {
	m.AuthServerURLInput.Blur()
	m.AuthTokenInput.Blur()
	switch m.AuthFocusField {
	case 0:
		m.AuthServerURLInput.Focus()
	case 1:
		m.AuthTokenInput.Focus()
	}
}

func (m Model) submitAuthModal() (tea.Model, tea.Cmd) {
	serverURL := strings.TrimSpace(m.AuthServerURLInput.Value())
	token := strings.TrimSpace(m.AuthTokenInput.Value())

	m.AuthToken = token
	m.AuthModalOpen = false

	if m.deps != nil && m.deps.Config != nil {
		m.deps.Config.HTTP.Token = token
		if serverURL != "" {
			if m.AuthModeHybrid {
				m.deps.Config.Sync.URL = serverURL
				m.deps.Config.Sync.Enabled = true
				if m.deps.Config.Sync.TokenEnv == "" {
					m.deps.Config.Sync.TokenEnv = "CORTEX_HTTP_TOKEN"
				}
				if m.deps.Config.MCP.Remote.URL == "" {
					m.deps.Config.MCP.Remote.URL = strings.TrimRight(serverURL, "/") + "/mcp"
				}
				m.deps.Config.MCP.Remote.Enabled = false
				m.UploadToCortex = true
			} else {
				m.deps.Config.MCP.Remote.URL = strings.TrimRight(serverURL, "/") + "/mcp"
				m.deps.Config.MCP.Remote.Enabled = true
				if m.deps.Config.MCP.Remote.TokenEnv == "" {
					m.deps.Config.MCP.Remote.TokenEnv = "CORTEX_HTTP_TOKEN"
				}
				m.deps.Config.Sync.Enabled = false
				m.UploadToCortex = false
			}
			if token != "" {
				_ = os.Setenv("CORTEX_HTTP_TOKEN", token)
			}
		} else if token == "" {
			m.deps.Config.Sync.Enabled = false
			m.deps.Config.MCP.Remote.Enabled = false
			m.UploadToCortex = false
		}
		targetPath := m.deps.Config.LoadedFrom
		if targetPath == "" {
			targetPath = filepath.Join(config.CortexDir(), "cortex.yaml")
		}
		if err := config.Save(m.deps.Config, targetPath); err != nil {
			m.ToastMessage = fmt.Sprintf("Error saving config: %v", err)
			m.ToastType = "error"
			return m, nil
		}
		m.deps.Config.LoadedFrom = targetPath
	}

	if token != "" || serverURL != "" {
		if m.deps != nil && m.deps.Config != nil && m.deps.Config.Server.PrincipalSubject != "" {
			m.CurrentUser = m.deps.Config.Server.PrincipalSubject
		} else {
			u := os.Getenv("USER")
			if u == "" {
				u = os.Getenv("USERNAME")
			}
			if u != "" {
				m.CurrentUser = u
			}
		}
		if m.deps != nil && m.deps.Config != nil && len(m.deps.Config.Server.Roles) > 0 {
			m.UserRole = m.deps.Config.Server.Roles[0]
		} else {
			m.UserRole = "admin"
		}
		if m.AuthModeHybrid {
			m.ToastMessage = "✔ Connected in Hybrid Mode (Local-First + Sync)"
		} else {
			m.ToastMessage = "✔ Configured Remote MCP Proxy Mode"
		}
		m.ToastType = "success"
	} else {
		u := os.Getenv("USER")
		if u == "" {
			u = os.Getenv("USERNAME")
		}
		if u == "" {
			u = "local-user"
		}
		m.CurrentUser = u
		m.UserRole = "local"
		m.ToastMessage = "Session cleared: unauthenticated local mode"
		m.ToastType = "warning"
	}

	return m, nil
}

func (m Model) handleAuthModalKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.AuthModalOpen = false
		return m, nil
	case "tab", "down":
		m.AuthFocusField = (m.AuthFocusField + 1) % 3
		m.focusAuthField()
		return m, textinput.Blink
	case "shift+tab", "up":
		m.AuthFocusField = (m.AuthFocusField + 2) % 3
		m.focusAuthField()
		return m, textinput.Blink
	case " ", "left", "right":
		if m.AuthFocusField == 2 {
			m.AuthModeHybrid = !m.AuthModeHybrid
			return m, nil
		}
	case "enter":
		if m.AuthFocusField == 0 {
			m.AuthFocusField = 1
			m.focusAuthField()
			return m, textinput.Blink
		}
		return m.submitAuthModal()
	case "ctrl+s":
		return m.submitAuthModal()
	}

	var cmd tea.Cmd
	switch m.AuthFocusField {
	case 0:
		m.AuthServerURLInput, cmd = m.AuthServerURLInput.Update(msg)
	case 1:
		m.AuthTokenInput, cmd = m.AuthTokenInput.Update(msg)
	}
	return m, cmd
}

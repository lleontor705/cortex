package tui

import (
	"github.com/lleontor705/cortex/internal/config"
	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/setup"
	"github.com/lleontor705/cortex/internal/store/session"
	"github.com/lleontor705/cortex/internal/update"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─── Screens ─────────────────────────────────────────────────────────────────

// Screen represents the current TUI view.
type Screen int

const (
	ScreenDashboard Screen = iota
	ScreenSearch
	ScreenSearchResults
	ScreenRecent
	ScreenObservationDetail
	ScreenTimeline
	ScreenSessions
	ScreenSessionDetail
	ScreenSetup
	ScreenGraph
	ScreenArchive
	ScreenHealth
	ScreenEmbeddingConfig
	ScreenHelp
)

// ─── Aggregate types ────────────────────────────────────────────────────────

type combinedStats struct {
	TotalObservations int
	TotalSessions     int
	TotalEdges        int
	Projects          []string
	ByType            map[string]int
}

// ─── Custom Messages ────────────────────────────────────────────────────────

type updateCheckMsg struct {
	result *update.Result
}

type statsLoadedMsg struct {
	stats *combinedStats
	err   error
}

type searchResultsMsg struct {
	results []*domain.SearchResult
	query   string
	err     error
}

type recentObservationsMsg struct {
	observations []*domain.Observation
	err          error
}

type observationDetailMsg struct {
	observation *domain.Observation
	score       *domain.ImportanceScore
	entities    []*domain.EntityLink
	edges       []*domain.Edge
	err         error
}

type timelineMsg struct {
	focus  *domain.Observation
	before []*domain.Observation
	after  []*domain.Observation
	err    error
}

type recentSessionsMsg struct {
	sessions []*session.SessionStats
	err      error
}

type sessionObservationsMsg struct {
	observations []*domain.Observation
	err          error
}

type graphLoadedMsg struct {
	related []*domain.Observation
	edges   []*domain.Edge
	err     error
}

type archiveLoadedMsg struct {
	observations []*domain.Observation
	err          error
}

type healthLoadedMsg struct {
	stale      []*domain.Observation
	orphans    []*domain.Observation
	edgeCount  int
	obsCount   int
	candidates []healthCandidate
	err        error
}

type healthCandidate struct {
	topicKey string
	count    int
}

type setupInstallMsg struct {
	result *setup.Result
	err    error
}

// Embedding config messages
type ollamaStatusMsg struct {
	running  bool
	hasModel bool
	err      error
}

type ollamaStartMsg struct {
	err error
}

type ollamaPullMsg struct {
	done bool
	err  error
}

type configSavedMsg struct {
	err error
}

type configReloadedMsg struct {
	cfg *config.Config
	err error
}

type reindexProgressMsg struct {
	progress string
	done     bool
	err      error
}

type deleteObservationMsg struct {
	id  int64
	err error
}

type unarchiveObservationMsg struct {
	id  int64
	err error
}

// ─── Model ──────────────────────────────────────────────────────────────────

// Model holds the TUI state.
type Model struct {
	deps       *Deps
	Version    string
	Screen     Screen
	PrevScreen Screen
	PrevCursor int
	Width      int
	Height     int
	Cursor     int
	Scroll     int

	// Update notification
	UpdateResult *update.Result

	// Error display
	ErrorMsg string

	// Dashboard
	Stats *combinedStats

	// Search
	SearchInput   textinput.Model
	SearchQuery   string
	SearchResults []*domain.SearchResult

	// Recent observations
	RecentObservations []*domain.Observation

	// Observation detail (enriched with Cortex data)
	SelectedObservation *domain.Observation
	DetailScore         *domain.ImportanceScore
	DetailEntities      []*domain.EntityLink
	DetailEdges         []*domain.Edge
	DetailScroll        int

	// Timeline
	TimelineFocus  *domain.Observation
	TimelineBefore []*domain.Observation
	TimelineAfter  []*domain.Observation

	// Sessions
	Sessions            []*session.SessionStats
	SelectedSessionIdx  int
	SessionObservations []*domain.Observation
	SessionDetailScroll int

	// Graph (Cortex-exclusive)
	GraphObservations []*domain.Observation
	GraphEdges        []*domain.Edge
	GraphRootID       int64

	// Archive (Cortex-exclusive)
	ArchivedObservations []*domain.Observation

	// Health (Cortex-exclusive)
	HealthStale      []*domain.Observation
	HealthOrphans    []*domain.Observation
	HealthEdgeCount  int
	HealthObsCount   int
	HealthCandidates []healthCandidate

	// Setup
	SetupAgents           []setup.Agent
	SetupResult           *setup.Result
	SetupInstalling       bool
	SetupInstallingName   string
	SetupDone             bool
	SetupError            string
	SetupAllowlistPrompt  bool
	SetupAllowlistApplied bool
	SetupAllowlistError   string
	SetupSpinner          spinner.Model

	// Embedding config
	EmbCfgProvider       int             // 0=none, 1=ollama, 2=openai
	EmbCfgModel          textinput.Model // model name input
	EmbCfgVector         bool            // vector search toggle
	EmbCfgAutoStart      bool            // auto-start Ollama toggle
	EmbCfgFocusField     int             // focused field (0=provider, 1=model, 2=vector, 3=autostart, 4=save)
	EmbCfgSaving         bool
	EmbCfgSaved          bool
	EmbCfgError          string
	EmbCfgOllamaRunning  bool
	EmbCfgOllamaHasModel bool
	EmbCfgOllamaChecked  bool // true after status check completes
	EmbCfgPulling        bool
	EmbCfgStarting       bool
	EmbCfgSpinner        spinner.Model

	// Embedding config — provider/model change detection
	EmbCfgOriginalProvider int
	EmbCfgOriginalModel    string
	EmbCfgReindexWarning   bool
	EmbCfgReindexing       bool
	EmbCfgReindexProgress  string

	// Toast messages
	ToastMessage string
	ToastType    string // "success", "warning", "error"

	// Delete confirmation
	ConfirmDelete   bool
	ConfirmDeleteID int64
}

// New creates a new TUI model connected to the given stores.
func New(deps *Deps) Model {
	ti := textinput.New()
	ti.Placeholder = "Search memories..."
	ti.CharLimit = 256
	ti.Width = 60

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorCyan)

	embSp := spinner.New()
	embSp.Spinner = spinner.Dot
	embSp.Style = lipgloss.NewStyle().Foreground(colorCyan)

	embModel := textinput.New()
	embModel.Placeholder = "e.g. qwen3-embedding:8b"
	embModel.CharLimit = 128
	embModel.Width = 40

	// Initialize embedding config from current config if available
	embProvider := 0
	embVector := false
	embAutoStart := false
	if deps.Config != nil {
		switch deps.Config.Search.EmbeddingProvider {
		case "ollama":
			embProvider = 1
		case "openai":
			embProvider = 2
		}
		embModel.SetValue(deps.Config.Search.EmbeddingModel)
		embVector = deps.Config.Search.Vector
		embAutoStart = deps.Config.Search.OllamaAutoStart
	}

	return Model{
		deps:            deps,
		Version:         deps.Version,
		Screen:          ScreenDashboard,
		SearchInput:     ti,
		SetupSpinner:    sp,
		EmbCfgSpinner:   embSp,
		EmbCfgModel:     embModel,
		EmbCfgProvider:   embProvider,
		EmbCfgVector:     embVector,
		EmbCfgAutoStart:  embAutoStart,
	}
}

// Init loads initial data (stats for the dashboard).
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		loadStats(m.deps),
		checkForUpdate(m.Version),
		tea.EnterAltScreen,
	)
}

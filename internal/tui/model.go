package tui

import (
	"fmt"
	"github.com/lleontor705/cortex/internal/config"
	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/setup"
	"github.com/lleontor705/cortex/internal/store/session"
	"github.com/lleontor705/cortex/internal/update"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
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
	ScreenLocalConfig
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

type localConfigSavedMsg struct{ err error }

type reindexProgressMsg struct {
	progress string
	done     bool
	total    int
	indexed  int
	err      error
}

type activityDataMsg struct {
	daily []int // observation counts per day, last 7 days (index 0 = 6 days ago, index 6 = today)
	err   error
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
	SearchInput      textinput.Model
	SearchQuery      string
	SearchResults    []*domain.SearchResult
	SearchHistory    []string
	SearchHistoryIdx int

	// Recent observations
	RecentObservations []*domain.Observation

	// Observation detail (enriched with Cortex data)
	SelectedObservation *domain.Observation
	DetailScore         *domain.ImportanceScore
	DetailEntities      []*domain.EntityLink
	DetailEdges         []*domain.Edge
	DetailViewport      viewport.Model
	DetailLoading       bool

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

	// List filtering
	FilterProject string // active project filter ("" = all)
	FilterActive  bool   // whether filter is shown

	// Vim multi-key sequences
	PendingKey string // for multi-key sequences like "gg"

	// Archive (Cortex-exclusive)
	ArchivedObservations []*domain.Observation

	// Health (Cortex-exclusive)
	HealthStale      []*domain.Observation
	HealthOrphans    []*domain.Observation
	HealthEdgeCount  int
	HealthObsCount   int
	HealthCandidates []healthCandidate
	HealthSection    int // 0=stale, 1=orphans, 2=candidates
	HealthExpanded   bool

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
	EmbCfgDirty          bool
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
	ReindexProgressBar     progress.Model
	ReindexTotal           int
	ReindexDone            int

	// Local-first configuration. Values are staged until Save is selected.
	LocalCfgDatabasePath textinput.Model
	LocalCfgHTTPEnabled  bool
	LocalCfgHTTPHost     textinput.Model
	LocalCfgHTTPPort     textinput.Model
	LocalCfgMCPRemote    bool
	LocalCfgMCPURL       textinput.Model
	LocalCfgMCPTokenEnv  textinput.Model
	LocalCfgSyncEnabled  bool
	LocalCfgSyncURL      textinput.Model
	LocalCfgSyncTokenEnv textinput.Model
	LocalCfgSyncInterval textinput.Model
	LocalCfgFocusField   int
	LocalCfgDirty        bool
	LocalCfgSaving       bool
	LocalCfgSaved        bool
	LocalCfgError        string
	LocalCfgSpinner      spinner.Model

	// Activity sparkline (7-day observation counts)
	ActivityData []int

	// Split pane preview
	PreviewVisible  bool
	PreviewViewport viewport.Model
	FocusedPane     int // 0=main, 1=preview

	// Toast messages
	ToastMessage string
	ToastType    string // "success", "warning", "error"

	// Delete confirmation
	ConfirmDelete     bool
	ConfirmDeleteID   int64
	DeleteTargetTitle string

	// Command palette
	CmdPaletteOpen   bool
	CmdPaletteInput  textinput.Model
	CmdPaletteCursor int

	// List components (bubbles/list)
	SearchListModel  list.Model
	RecentList       list.Model
	SessionListModel list.Model
	GraphListModel   list.Model
	ArchiveList      list.Model
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
	localInput := func(placeholder string, limit int) textinput.Model {
		input := textinput.New()
		input.Placeholder = placeholder
		input.CharLimit = limit
		input.Width = 54
		return input
	}
	localSp := spinner.New()
	localSp.Spinner = spinner.Dot
	localSp.Style = lipgloss.NewStyle().Foreground(colorCyan)
	databasePath := localInput("~/.cortex/cortex.db", 512)
	httpHost := localInput("127.0.0.1", 255)
	httpPort := localInput("7438", 5)
	mcpURL := localInput("https://server.example/mcp", 512)
	mcpTokenEnv := localInput("CORTEX_REMOTE_TOKEN", 128)
	syncURL := localInput("https://server.example", 512)
	syncTokenEnv := localInput("CORTEX_REMOTE_TOKEN", 128)
	syncInterval := localInput("30s", 32)

	cmdInput := textinput.New()
	cmdInput.Placeholder = "Type a command..."
	cmdInput.CharLimit = 64
	cmdInput.Width = 40

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
		databasePath.SetValue(deps.Config.Database.Path)
		httpHost.SetValue(deps.Config.HTTP.Host)
		httpPort.SetValue(fmt.Sprintf("%d", deps.Config.HTTP.Port))
		mcpURL.SetValue(deps.Config.MCP.Remote.URL)
		mcpTokenEnv.SetValue(deps.Config.MCP.Remote.TokenEnv)
		syncURL.SetValue(deps.Config.Sync.URL)
		syncTokenEnv.SetValue(deps.Config.Sync.TokenEnv)
		syncInterval.SetValue(deps.Config.Sync.Interval.String())
	}

	// Create empty list models with default delegate
	newEmptyList := func() list.Model {
		l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
		l.SetShowHelp(false)
		l.SetShowStatusBar(false)
		l.SetShowTitle(false)
		l.DisableQuitKeybindings()
		l.SetFilteringEnabled(true)
		return l
	}

	return Model{
		deps:                 deps,
		Version:              deps.Version,
		Screen:               ScreenDashboard,
		SearchInput:          ti,
		SetupSpinner:         sp,
		EmbCfgSpinner:        embSp,
		EmbCfgModel:          embModel,
		LocalCfgDatabasePath: databasePath,
		LocalCfgHTTPEnabled:  deps.Config != nil && deps.Config.HTTP.Enabled,
		LocalCfgHTTPHost:     httpHost,
		LocalCfgHTTPPort:     httpPort,
		LocalCfgMCPRemote:    deps.Config != nil && deps.Config.MCP.Remote.Enabled,
		LocalCfgMCPURL:       mcpURL,
		LocalCfgMCPTokenEnv:  mcpTokenEnv,
		LocalCfgSyncEnabled:  deps.Config != nil && deps.Config.Sync.Enabled,
		LocalCfgSyncURL:      syncURL,
		LocalCfgSyncTokenEnv: syncTokenEnv,
		LocalCfgSyncInterval: syncInterval,
		LocalCfgSpinner:      localSp,
		EmbCfgProvider:       embProvider,
		EmbCfgVector:         embVector,
		EmbCfgAutoStart:      embAutoStart,
		ReindexProgressBar:   progress.New(progress.WithDefaultGradient()),
		CmdPaletteInput:      cmdInput,
		SearchListModel:      newEmptyList(),
		RecentList:           newEmptyList(),
		SessionListModel:     newEmptyList(),
		GraphListModel:       newEmptyList(),
		ArchiveList:          newEmptyList(),
	}
}

// Init loads initial data (stats for the dashboard).
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		loadStats(m.deps),
		loadActivityData(m.deps),
		checkForUpdate(m.Version),
		tea.EnterAltScreen,
	)
}

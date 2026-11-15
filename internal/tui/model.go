package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
	"github.com/lleontor705/cortex/v2/internal/setup"
	"github.com/lleontor705/cortex/v2/internal/store/session"
	"github.com/lleontor705/cortex/v2/internal/update"

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
	ScreenConnect
)

// ─── Guided Connect flow (REQ-SYNC-001/002) ─────────────────────────────────

// ConnectStep indexes the three steps of the guided Connect flow (design.md
// W5/W6/W7): URL + mode, bearer token, connectivity test. The zero value is
// step 1 so entering the flow needs no extra state.
type ConnectStep int

const (
	// ConnectStepURL collects the server URL and the post-connect mode.
	ConnectStepURL ConnectStep = iota
	// ConnectStepToken collects the bearer token (masked).
	ConnectStepToken
	// ConnectStepTest runs the real connectivity test and commits on success.
	ConnectStepTest
)

// String returns the stable step identifier used in diagnostics and tests.
func (s ConnectStep) String() string {
	switch s {
	case ConnectStepURL:
		return "url"
	case ConnectStepToken:
		return "token"
	case ConnectStepTest:
		return "test"
	default:
		return "invalid"
	}
}

// Label renders the wireframe step counter, e.g. "step 1 of 3".
func (s ConnectStep) Label() string {
	n := 3
	switch s {
	case ConnectStepURL:
		n = 1
	case ConnectStepToken:
		n = 2
	case ConnectStepTest:
		n = 3
	}
	return fmt.Sprintf("step %d of 3", n)
}

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

type codeSearchResultsMsg struct {
	results []code.Symbol
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

type animTickMsg time.Time

func animTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return animTickMsg(t)
	})
}

type setupAgentsDetectedMsg struct {
	statuses []setup.AgentStatus
}

func detectAgentsCmd() tea.Cmd {
	return func() tea.Msg {
		return setupAgentsDetectedMsg{statuses: setup.DetectAgents()}
	}
}

type testConnResultMsg struct {
	provider string
	success  bool
	message  string
}

func testConnectionCmd(provider, baseURL string) tea.Cmd {
	return func() tea.Msg {
		if provider == "ollama" || provider == "Ollama" {
			res, err := setup.SetupOllama(baseURL, "")
			if err != nil {
				return testConnResultMsg{provider: provider, success: false, message: err.Error()}
			}
			if res.OllamaOnline {
				msg := fmt.Sprintf("Online (%d models available)", len(res.AvailableModels))
				return testConnResultMsg{provider: provider, success: true, message: msg}
			}
			return testConnResultMsg{provider: provider, success: false, message: "Ollama not reachable at " + baseURL}
		}
		return testConnResultMsg{provider: provider, success: true, message: "Configuration valid"}
	}
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

type observationCreatedMsg struct {
	observation *domain.Observation
	err         error
}

// ─── Model ──────────────────────────────────────────────────────────────────

// Model holds the TUI state.
type Model struct {
	deps       *Deps
	Version    string
	Screen     Screen
	PrevCursor int
	Width      int
	Height     int
	Cursor     int
	Scroll     int

	// Nav is the live multi-level navigation stack backing the header
	// breadcrumb and canonical esc/q history. Every screen transition drives it
	// through NavPush (forward), NavPop (esc), and NavReset (q), so the stack is
	// the single source of navigation truth (REQ-NAV-003).
	Nav NavStack

	// Command Deck Workspaces
	ActiveDeckWorkspace int // 0: Knowledge Hub, 1: Neural Graph, 2: System Health, 3: Control & Setup
	ControlDeckTab      int // 0: Agents, 1: Local & AI Config, 2: Embeddings
	KnowledgeDeckTab    int // 0: Recent, 1: Search, 2: Sessions, 3: Archived

	// Update notification
	UpdateResult *update.Result

	// Error display
	ErrorMsg string

	// Dashboard
	Stats *combinedStats

	// Search
	SearchInput       textinput.Model
	SearchQuery       string
	SearchResults     []*domain.SearchResult
	CodeSearchResults []code.Symbol
	SearchMode        string
	SearchHistory     []string
	SearchHistoryIdx  int

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
	SetupDetectedAgents   []setup.AgentStatus
	SetupResult           *setup.Result
	SetupInstalling       bool
	SetupInstallingName   string
	SetupDone             bool
	SetupError            string
	SetupAllowlistPrompt  bool
	SetupAllowlistApplied bool
	SetupAllowlistError   string
	SetupProfile          string // "agent", "dev", "minimal"
	SetupSpinner          spinner.Model

	// Animation & Testing State
	AnimFrame       int
	TestConnStatus  string
	TestConnTesting bool

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
	LocalCfgFormat       int // 0=YAML, 1=JSON, 2=TOML
	LocalCfgLLMProvider  int // 0=none, 1=ollama, 2=openai, 3=anthropic, 4=openrouter, 5=groq, 6=deepseek, 7=custom
	LocalCfgLLMModel     textinput.Model
	LocalCfgLLMBaseURL   textinput.Model
	LocalCfgHTTPEnabled  bool
	LocalCfgHTTPHost     textinput.Model
	LocalCfgHTTPPort     textinput.Model
	LocalCfgMCPProfile   string // "agent", "dev", "minimal"
	LocalCfgMCPRemote    bool
	LocalCfgMCPURL       textinput.Model
	LocalCfgMCPTokenEnv  textinput.Model
	LocalCfgSyncEnabled  bool
	LocalCfgSyncURL      textinput.Model
	LocalCfgSyncTokenEnv textinput.Model
	LocalCfgSyncInterval textinput.Model
	// LocalCfgEmbeddingDimensions edits search.embedding_dimensions (Provider
	// MRL dimension, 0 = provider default) on the Configuration Advanced tab.
	LocalCfgEmbeddingDimensions textinput.Model
	LocalCfgFocusField          int
	LocalCfgDirty               bool
	LocalCfgSaving              bool
	LocalCfgSaved               bool
	LocalCfgError               string
	LocalCfgSpinner             spinner.Model

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

	// Quick memory modal
	NewObsModalOpen    bool
	NewObsTitleInput   textinput.Model
	NewObsContentInput textinput.Model
	NewObsTypeInput    textinput.Model
	NewObsProjectInput textinput.Model
	NewObsFocusField   int

	// Theme
	IsDarkTheme bool

	// Auth & Identity. The Auth modal was dissolved by tui-redesign-023b: the
	// guided Connect flow owns the server URL and the unified Configuration owns
	// http.token through AuthTokenInput.
	CurrentUser    string
	UserRole       string // "admin", "owner", "member", "viewer", "local"
	AuthTokenInput textinput.Model

	// Guided Connect flow (REQ-SYNC-001/002). The server URL has exactly one
	// writable surface here; the Auth modal is retained only until 023 rewires
	// the entry points to this flow.
	ConnectStep      ConnectStep
	ConnectURL       textinput.Model
	ConnectToken     textinput.Model
	ConnectProxyMode bool // false = Hybrid sync (recommended), true = Remote MCP proxy
	ConnectFocus     int  // step 1: 0 = URL, 1 = mode selector
	ConnectResult    *connectTestResult
	ConnectCommitted bool
	ConnectSummary   string
	ConnectError     string

	// Project & Upload Policy
	UploadToCortex bool // whether current project uploads to Cortex server

	// List components (bubbles/list)
	SearchListModel  list.Model
	RecentList       list.Model
	SessionListModel list.Model
	GraphListModel   list.Model
	ArchiveList      list.Model
}

// New creates a new TUI model connected to the given stores.
func New(deps *Deps) Model {
	return NewWithScreen(deps, ScreenDashboard)
}

// NewWithScreen creates a new TUI model starting on initialScreen.
func NewWithScreen(deps *Deps, initialScreen Screen) Model {
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
	embeddingDimensions := localInput("0", 5)

	cmdInput := textinput.New()
	cmdInput.Placeholder = "Type a command..."
	cmdInput.CharLimit = 64
	cmdInput.Width = 40

	// Initialize embedding config from current config if available
	cfgFormat := 0
	llmProvider := 0
	embProvider := 0
	embVector := false
	embAutoStart := false
	llmModel := localInput("e.g. qwen3-embedding:8b or text-embedding-3-small", 128)
	llmBaseURL := localInput("http://localhost:11434", 255)

	if deps.Config != nil {
		switch deps.Config.Search.EmbeddingProvider {
		case "ollama":
			embProvider = 1
			llmProvider = 1
		case "openai":
			embProvider = 2
			llmProvider = 2
		}
		if strings.HasSuffix(strings.ToLower(deps.Config.LoadedFrom), ".json") || strings.HasSuffix(strings.ToLower(deps.Config.LoadedFrom), ".jsonc") {
			cfgFormat = 1
		} else if strings.HasSuffix(strings.ToLower(deps.Config.LoadedFrom), ".toml") {
			cfgFormat = 2
		}
		embModel.SetValue(deps.Config.Search.EmbeddingModel)
		llmModel.SetValue(deps.Config.Search.EmbeddingModel)
		llmBaseURL.SetValue(deps.Config.Search.EmbeddingBaseURL)
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
		if deps.Config.Search.EmbeddingDimensions > 0 {
			embeddingDimensions.SetValue(fmt.Sprintf("%d", deps.Config.Search.EmbeddingDimensions))
		}
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

	obsTitle := textinput.New()
	obsTitle.Placeholder = "Observation title..."
	obsTitle.CharLimit = 128
	obsTitle.Width = 50

	obsContent := textinput.New()
	obsContent.Placeholder = "What, Why, Where, Learned..."
	obsContent.CharLimit = 1024
	obsContent.Width = 50

	obsType := textinput.New()
	obsType.Placeholder = "decision, bugfix, architecture, pattern, config, discovery, learning"
	obsType.SetValue("decision")
	obsType.CharLimit = 32
	obsType.Width = 50

	obsProject := textinput.New()
	obsProject.Placeholder = "Project name (e.g. cortex)"
	obsProject.CharLimit = 64
	obsProject.Width = 50

	authInput := textinput.New()
	authInput.Placeholder = "Enter bearer token (e.g. ctx_...)"
	authInput.CharLimit = 256
	authInput.Width = 50
	authInput.EchoMode = textinput.EchoPassword

	connectURLInput := textinput.New()
	connectURLInput.Placeholder = "https://your-cortex.up.railway.app"
	connectURLInput.CharLimit = 512
	connectURLInput.Width = 54

	connectTokenInput := textinput.New()
	connectTokenInput.Placeholder = "ctx_..."
	connectTokenInput.CharLimit = 256
	connectTokenInput.Width = 54
	connectTokenInput.EchoMode = textinput.EchoPassword
	connectTokenInput.EchoCharacter = '•'

	currentUser := "local-user"
	if u := os.Getenv("USER"); u != "" {
		currentUser = u
	} else if u := os.Getenv("USERNAME"); u != "" {
		currentUser = u
	}
	userRole := "local"
	authToken := ""
	uploadToCortex := false
	if deps != nil && deps.Config != nil {
		if deps.Config.HTTP.Token != "" {
			authToken = deps.Config.HTTP.Token
			authInput.SetValue(authToken)
			userRole = "admin"
		}
		if deps.Config.Sync.URL != "" {
			connectURLInput.SetValue(deps.Config.Sync.URL)
		} else if deps.Config.MCP.Remote.URL != "" {
			connectURLInput.SetValue(strings.TrimSuffix(deps.Config.MCP.Remote.URL, "/mcp"))
		}
		if deps.Config.Server.PrincipalSubject != "" {
			currentUser = deps.Config.Server.PrincipalSubject
		}
		if len(deps.Config.Server.Roles) > 0 {
			userRole = deps.Config.Server.Roles[0]
		}
		uploadToCortex = deps.Config.Sync.Enabled
	}

	activeWorkspace := 0
	controlDeckTab := 0
	switch initialScreen {
	case ScreenGraph:
		activeWorkspace = 1
	case ScreenHealth, ScreenArchive:
		activeWorkspace = 2
	case ScreenSetup:
		activeWorkspace = 3
		controlDeckTab = 0
	case ScreenLocalConfig:
		activeWorkspace = 3
		controlDeckTab = 1
	case ScreenEmbeddingConfig:
		activeWorkspace = 3
		controlDeckTab = 2
	}

	localCfgMCPProfile := "agent"
	if deps != nil && deps.Config != nil {
		if p := strings.ToLower(strings.TrimSpace(deps.Config.MCP.Profile)); p != "" {
			if p == "agent" || p == "dev" || p == "minimal" {
				localCfgMCPProfile = p
			}
		}
	}

	// Seed the live stack so a non-Dashboard launch reports the correct
	// breadcrumb depth while the Dashboard floor stays implicit.
	nav := NavStack{}
	if initialScreen != navFloor {
		nav.ResetTo(initialScreen)
	}

	return Model{
		deps:                        deps,
		Version:                     deps.Version,
		Screen:                      initialScreen,
		Nav:                         nav,
		ActiveDeckWorkspace:         activeWorkspace,
		ControlDeckTab:              controlDeckTab,
		IsDarkTheme:                 true,
		CurrentUser:                 currentUser,
		UserRole:                    userRole,
		AuthTokenInput:              authInput,
		ConnectStep:                 ConnectStepURL,
		ConnectURL:                  connectURLInput,
		ConnectToken:                connectTokenInput,
		ConnectProxyMode:            deps.Config != nil && deps.Config.MCP.Remote.Enabled,
		UploadToCortex:              uploadToCortex,
		SearchInput:                 ti,
		SearchMode:                  "memory",
		SetupSpinner:                sp,
		EmbCfgSpinner:               embSp,
		EmbCfgModel:                 embModel,
		LocalCfgDatabasePath:        databasePath,
		LocalCfgFormat:              cfgFormat,
		LocalCfgLLMProvider:         llmProvider,
		LocalCfgLLMModel:            llmModel,
		LocalCfgLLMBaseURL:          llmBaseURL,
		LocalCfgHTTPEnabled:         deps.Config != nil && deps.Config.HTTP.Enabled,
		LocalCfgHTTPHost:            httpHost,
		LocalCfgHTTPPort:            httpPort,
		LocalCfgMCPProfile:          localCfgMCPProfile,
		LocalCfgMCPRemote:           deps.Config != nil && deps.Config.MCP.Remote.Enabled,
		LocalCfgMCPURL:              mcpURL,
		LocalCfgMCPTokenEnv:         mcpTokenEnv,
		LocalCfgSyncEnabled:         deps.Config != nil && deps.Config.Sync.Enabled,
		LocalCfgSyncURL:             syncURL,
		LocalCfgSyncTokenEnv:        syncTokenEnv,
		LocalCfgSyncInterval:        syncInterval,
		LocalCfgSpinner:             localSp,
		LocalCfgEmbeddingDimensions: embeddingDimensions,
		EmbCfgProvider:              embProvider,
		EmbCfgVector:                embVector,
		EmbCfgAutoStart:             embAutoStart,
		ReindexProgressBar:          progress.New(progress.WithDefaultGradient()),
		CmdPaletteInput:             cmdInput,
		NewObsTitleInput:            obsTitle,
		NewObsContentInput:          obsContent,
		NewObsTypeInput:             obsType,
		NewObsProjectInput:          obsProject,
		SearchListModel:             newEmptyList(),
		RecentList:                  newEmptyList(),
		SessionListModel:            newEmptyList(),
		GraphListModel:              newEmptyList(),
		ArchiveList:                 newEmptyList(),
	}
}

// Init loads initial data (stats for the dashboard and memory stream).
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		loadStats(m.deps),
		loadRecentObservations(m.deps, ""),
		checkForUpdate(m.Version),
		animTickCmd(),
		detectAgentsCmd(),
		tea.EnterAltScreen,
	)
}

// ─── Live navigation stack ──────────────────────────────────────────────────
//
// The Nav field and the accessors below are the navigation seam introduced by
// tui-redesign-022 and adopted across every screen handler and the global
// bindings (REQ-NAV-003). NavPush is the forward transition, NavPop the esc
// transition, and NavReset the q transition; together they make the live stack
// the single source of navigation truth.

// NavPush records forward navigation to s as a new stack level.
func (m *Model) NavPush(s Screen) {
	if s == m.Screen {
		return
	}
	// Detach from any sibling Model copy sharing the stack backing array before
	// appending (bubbletea passes Model by value).
	m.Nav = m.Nav.clone()
	m.Nav.Push(s)
	m.Screen = s
}

// NavPop applies esc semantics: move back one level, floored at Dashboard, and
// return the new current screen.
func (m *Model) NavPop() Screen {
	m.Screen = m.Nav.Pop()
	return m.Screen
}

// NavReset applies q semantics: clear navigation history and land on the
// Dashboard floor.
func (m *Model) NavReset() Screen {
	m.Nav.ResetTo(navFloor)
	m.Screen = navFloor
	return m.Screen
}

// NavParent returns the screen one level below the current top of the live
// stack, or the Dashboard floor. Views that need the referring screen (the Help
// origin, the header chrome fallback) read it here instead of carrying a shadow
// history field.
func (m Model) NavParent() Screen {
	if len(m.Nav.stack) < 2 {
		return navFloor
	}
	return m.Nav.stack[len(m.Nav.stack)-2]
}

// NavBreadcrumb renders the live stack path with the shared screen vocabulary.
func (m Model) NavBreadcrumb() string {
	return m.Nav.Breadcrumb(screenLabel)
}

// NavDepth returns the live breadcrumb segment count (Dashboard floor = 1).
func (m Model) NavDepth() int {
	return m.Nav.Depth()
}

// clone returns a detached copy of the stack so an append on one Model value
// cannot overwrite the history of a sibling value sharing the same backing
// array. It leaves the nav.go contract untouched.
func (n NavStack) clone() NavStack {
	return NavStack{stack: append([]Screen(nil), n.stack...)}
}

// ActiveWorkspace returns the 0-indexed active deck workspace (0..3).
func (m Model) ActiveWorkspace() int {
	switch m.Screen {
	case ScreenGraph:
		return 1
	case ScreenHealth, ScreenArchive:
		return 2
	case ScreenSetup, ScreenLocalConfig, ScreenEmbeddingConfig:
		return 3
	default:
		return 0
	}
}

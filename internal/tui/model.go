package tui

import (
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

	return Model{
		deps:         deps,
		Version:      deps.Version,
		Screen:       ScreenDashboard,
		SearchInput:  ti,
		SetupSpinner: sp,
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

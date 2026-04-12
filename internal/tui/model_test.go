package tui

import (
	"errors"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/setup"
	"github.com/lleontor705/cortex/internal/store/session"
	"github.com/lleontor705/cortex/internal/update"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewInitializesModelDefaults(t *testing.T) {
	m := New(&Deps{Version: "test"})

	if m.Screen != ScreenDashboard {
		t.Fatalf("screen = %v, want %v", m.Screen, ScreenDashboard)
	}
	if m.SearchInput.Placeholder != "Search memories..." {
		t.Fatalf("placeholder = %q", m.SearchInput.Placeholder)
	}
	if m.SearchInput.CharLimit != 256 {
		t.Fatalf("char limit = %d", m.SearchInput.CharLimit)
	}
	if m.SearchInput.Width != 60 {
		t.Fatalf("width = %d", m.SearchInput.Width)
	}
	if m.SetupSpinner.Spinner.Frames == nil {
		t.Fatal("spinner was not initialized")
	}
	if m.Version != "test" {
		t.Fatalf("version = %q, want %q", m.Version, "test")
	}
}

func TestInitReturnsCommand(t *testing.T) {
	m := New(&Deps{Version: "dev"})
	if cmd := m.Init(); cmd == nil {
		t.Fatal("init should return a startup command")
	}
}

func TestUpdateHandlesWindowSize(t *testing.T) {
	m := New(&Deps{})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	result := updated.(Model)
	if result.Width != 120 || result.Height != 40 {
		t.Fatalf("size = %dx%d, want 120x40", result.Width, result.Height)
	}
}

func TestUpdateHandlesCtrlC(t *testing.T) {
	m := New(&Deps{})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c should return quit command")
	}
}

func TestUpdateHandlesStatsLoaded(t *testing.T) {
	m := New(&Deps{})
	stats := &combinedStats{
		TotalObservations: 42,
		TotalSessions:     5,
		TotalEdges:        10,
		Projects:          []string{"cortex", "test"},
	}

	updated, _ := m.Update(statsLoadedMsg{stats: stats})
	result := updated.(Model)
	if result.Stats == nil || result.Stats.TotalObservations != 42 {
		t.Fatalf("stats = %+v", result.Stats)
	}
}

func TestUpdateHandlesStatsError(t *testing.T) {
	m := New(&Deps{})
	updated, _ := m.Update(statsLoadedMsg{err: errors.New("db error")})
	result := updated.(Model)
	if result.ErrorMsg != "db error" {
		t.Fatalf("error = %q", result.ErrorMsg)
	}
}

func TestUpdateHandlesSearchResults(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenSearch

	results := []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1, Title: "Test"}},
	}

	updated, _ := m.Update(searchResultsMsg{results: results, query: "test"})
	result := updated.(Model)
	if result.Screen != ScreenSearchResults {
		t.Fatalf("screen = %v, want %v", result.Screen, ScreenSearchResults)
	}
	if len(result.SearchResults) != 1 {
		t.Fatalf("results = %d", len(result.SearchResults))
	}
	if result.SearchQuery != "test" {
		t.Fatalf("query = %q", result.SearchQuery)
	}
}

func TestUpdateHandlesObservationDetail(t *testing.T) {
	m := New(&Deps{})
	obs := &domain.Observation{ID: 42, Title: "Test", CreatedAt: time.Now()}
	score := &domain.ImportanceScore{ObservationID: 42, Score: 3.5, AccessCount: 10}
	entities := []*domain.EntityLink{{EntityType: "file", EntityValue: "test.go"}}
	edges := []*domain.Edge{{FromObsID: 42, ToObsID: 43, RelationType: "references"}}

	updated, _ := m.Update(observationDetailMsg{
		observation: obs,
		score:       score,
		entities:    entities,
		edges:       edges,
	})
	result := updated.(Model)
	if result.Screen != ScreenObservationDetail {
		t.Fatalf("screen = %v", result.Screen)
	}
	if result.DetailScore == nil || result.DetailScore.Score != 3.5 {
		t.Fatalf("score = %+v", result.DetailScore)
	}
	if len(result.DetailEntities) != 1 {
		t.Fatalf("entities = %d", len(result.DetailEntities))
	}
	if len(result.DetailEdges) != 1 {
		t.Fatalf("edges = %d", len(result.DetailEdges))
	}
}

func TestUpdateHandlesTimeline(t *testing.T) {
	m := New(&Deps{})
	focus := &domain.Observation{ID: 10, Title: "Focus"}
	before := []*domain.Observation{{ID: 9, Title: "Before"}}
	after := []*domain.Observation{{ID: 11, Title: "After"}}

	updated, _ := m.Update(timelineMsg{focus: focus, before: before, after: after})
	result := updated.(Model)
	if result.Screen != ScreenTimeline {
		t.Fatalf("screen = %v", result.Screen)
	}
	if result.TimelineFocus.ID != 10 {
		t.Fatalf("focus ID = %d", result.TimelineFocus.ID)
	}
	if len(result.TimelineBefore) != 1 || len(result.TimelineAfter) != 1 {
		t.Fatalf("before=%d, after=%d", len(result.TimelineBefore), len(result.TimelineAfter))
	}
}

func TestUpdateHandlesGraphLoaded(t *testing.T) {
	m := New(&Deps{})
	related := []*domain.Observation{{ID: 5, Title: "Related"}}
	edges := []*domain.Edge{{FromObsID: 1, ToObsID: 5, RelationType: "references"}}

	updated, _ := m.Update(graphLoadedMsg{related: related, edges: edges})
	result := updated.(Model)
	if result.Screen != ScreenGraph {
		t.Fatalf("screen = %v", result.Screen)
	}
	if len(result.GraphObservations) != 1 {
		t.Fatalf("graph obs = %d", len(result.GraphObservations))
	}
}

func TestUpdateHandlesArchiveLoaded(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenArchive
	obs := []*domain.Observation{{ID: 1, Title: "Archived"}}

	updated, _ := m.Update(archiveLoadedMsg{observations: obs})
	result := updated.(Model)
	if len(result.ArchivedObservations) != 1 {
		t.Fatalf("archived = %d", len(result.ArchivedObservations))
	}
}

func TestUpdateHandlesRecentSessions(t *testing.T) {
	m := New(&Deps{})
	sessions := []*session.SessionStats{
		{
			Session:          &domain.Session{ID: "s1", Project: "test"},
			ObservationCount: 5,
		},
	}

	updated, _ := m.Update(recentSessionsMsg{sessions: sessions})
	result := updated.(Model)
	if len(result.Sessions) != 1 {
		t.Fatalf("sessions = %d", len(result.Sessions))
	}
}

func TestUpdateHandlesUpdateCheck(t *testing.T) {
	m := New(&Deps{})
	res := &update.Result{Latest: "v1.0.0", UpdateURL: "https://example.com"}

	updated, _ := m.Update(updateCheckMsg{result: res})
	result := updated.(Model)
	if result.UpdateResult == nil || result.UpdateResult.Latest != "v1.0.0" {
		t.Fatalf("update = %+v", result.UpdateResult)
	}
}

func TestUpdateHandlesSetupInstallOpencode(t *testing.T) {
	m := New(&Deps{})
	m.SetupInstalling = true

	updated, _ := m.Update(setupInstallMsg{
		result: &setup.Result{Agent: "opencode", Destination: "/tmp/config", Files: 1},
	})
	result := updated.(Model)
	if result.SetupInstalling {
		t.Fatal("should not be installing")
	}
	if !result.SetupDone {
		t.Fatal("should be done for non-claude-code agent")
	}
	if result.SetupResult.Destination != "/tmp/config" {
		t.Fatalf("dest = %q", result.SetupResult.Destination)
	}
}

func TestUpdateHandlesSetupInstallClaudeCode(t *testing.T) {
	m := New(&Deps{})
	m.SetupInstalling = true

	updated, _ := m.Update(setupInstallMsg{
		result: &setup.Result{Agent: "claude-code", Destination: "/tmp/mcp", Files: 1},
	})
	result := updated.(Model)
	if result.SetupInstalling {
		t.Fatal("should not be installing")
	}
	if result.SetupDone {
		t.Fatal("should NOT be done yet — allowlist prompt expected")
	}
	if !result.SetupAllowlistPrompt {
		t.Fatal("should show allowlist prompt for claude-code")
	}
}

func TestInstallAgentCommand(t *testing.T) {
	original := installAgentFn
	t.Cleanup(func() { installAgentFn = original })

	t.Run("success", func(t *testing.T) {
		installAgentFn = func(agentName string) (*setup.Result, error) {
			return &setup.Result{Agent: agentName, Destination: "/tmp/" + agentName, Files: 1}, nil
		}

		msg := installAgent("opencode")()
		res, ok := msg.(setupInstallMsg)
		if !ok {
			t.Fatalf("message type = %T", msg)
		}
		if res.err != nil {
			t.Fatalf("unexpected error: %v", res.err)
		}
		if res.result.Destination != "/tmp/opencode" {
			t.Fatalf("dest = %q", res.result.Destination)
		}
	})

	t.Run("error", func(t *testing.T) {
		installAgentFn = func(string) (*setup.Result, error) {
			return nil, errors.New("install failed")
		}

		msg := installAgent("claude-code")()
		res, ok := msg.(setupInstallMsg)
		if !ok {
			t.Fatalf("message type = %T", msg)
		}
		if res.err == nil || res.err.Error() != "install failed" {
			t.Fatalf("expected install error, got %v", res.err)
		}
	})
}

// ─── Error Recovery ─────────────────────────────────────────────────────────

func TestUpdateHandlesObservationDetailError(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenRecent // Should NOT switch to detail on error

	updated, _ := m.Update(observationDetailMsg{err: errors.New("not found")})
	result := updated.(Model)
	if result.ErrorMsg != "not found" {
		t.Fatalf("error = %q, want %q", result.ErrorMsg, "not found")
	}
	if result.SelectedObservation != nil {
		t.Fatal("observation should be nil on error")
	}
}

func TestUpdateHandlesTimelineError(t *testing.T) {
	m := New(&Deps{})
	updated, _ := m.Update(timelineMsg{err: errors.New("timeline fail")})
	result := updated.(Model)
	if result.ErrorMsg != "timeline fail" {
		t.Fatalf("error = %q", result.ErrorMsg)
	}
	if result.TimelineFocus != nil {
		t.Fatal("focus should be nil on error")
	}
}

func TestUpdateHandlesGraphError(t *testing.T) {
	m := New(&Deps{})
	updated, _ := m.Update(graphLoadedMsg{err: errors.New("graph fail")})
	result := updated.(Model)
	if result.ErrorMsg != "graph fail" {
		t.Fatalf("error = %q", result.ErrorMsg)
	}
}

func TestUpdateHandlesSessionsError(t *testing.T) {
	m := New(&Deps{})
	updated, _ := m.Update(recentSessionsMsg{err: errors.New("session fail")})
	result := updated.(Model)
	if result.ErrorMsg != "session fail" {
		t.Fatalf("error = %q", result.ErrorMsg)
	}
}

// ─── Nil Deps Safety ────────────────────────────────────────────────────────

func TestLoadStatsWithNilDeps(t *testing.T) {
	cmd := loadStats(nil)
	msg := cmd()
	loaded, ok := msg.(statsLoadedMsg)
	if !ok {
		t.Fatalf("message type = %T", msg)
	}
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
}

func TestLoadStatsWithNilObservations(t *testing.T) {
	cmd := loadStats(&Deps{})
	msg := cmd()
	loaded, ok := msg.(statsLoadedMsg)
	if !ok {
		t.Fatalf("message type = %T", msg)
	}
	if loaded.err == nil {
		t.Fatal("expected error with nil observations store")
	}
}

func TestLoadSearchWithNilDeps(t *testing.T) {
	cmd := searchMemories(nil, "test", "")
	msg := cmd()
	loaded, ok := msg.(searchResultsMsg)
	if !ok {
		t.Fatalf("message type = %T", msg)
	}
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
}

func TestLoadGraphWithNilDeps(t *testing.T) {
	cmd := loadGraphRelated(nil, 1)
	msg := cmd()
	loaded, ok := msg.(graphLoadedMsg)
	if !ok {
		t.Fatalf("message type = %T", msg)
	}
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
}

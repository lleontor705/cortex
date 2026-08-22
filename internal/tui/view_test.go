package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/setup"
	"github.com/lleontor705/cortex/internal/store/session"
	"github.com/lleontor705/cortex/internal/update"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
)

// Type alias for cleaner test literals.
type updateResult = update.Result

func TestViewDashboard(t *testing.T) {
	m := New(&Deps{Version: "v0.1.0"})
	m.Width, m.Height = 120, 40
	m.Stats = &combinedStats{
		TotalObservations: 42,
		TotalSessions:     5,
		TotalEdges:        10,
		Projects:          []string{"cortex", "test-project"},
	}

	output := m.viewDashboard()

	if !strings.Contains(output, "cortex") {
		t.Error("dashboard should contain cortex in logo/tagline")
	}
	if !strings.Contains(output, "42") {
		t.Error("dashboard should show observation count")
	}
	if !strings.Contains(output, "knowledge links") {
		t.Error("dashboard should show knowledge links")
	}
	if !strings.Contains(output, "cortex") {
		t.Error("dashboard should show project names")
	}
	if !strings.Contains(output, "Knowledge graph") {
		t.Error("dashboard should have Knowledge graph menu item")
	}
	if !strings.Contains(output, "Archived observations") {
		t.Error("dashboard should have Archived observations menu item")
	}
}

func TestViewDashboardWithUpdate(t *testing.T) {
	m := New(&Deps{Version: "v0.1.0"})
	m.Width, m.Height = 120, 40
	m.Stats = &combinedStats{}
	m.UpdateResult = &updateResult{Latest: "v1.0.0", UpdateURL: "https://example.com"}

	output := m.viewDashboard()
	if !strings.Contains(output, "v1.0.0") {
		t.Error("dashboard should show update notification")
	}
}

func TestViewSearch(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40

	output := m.viewSearch()
	if !strings.Contains(output, "Search Memories") {
		t.Error("should show search header")
	}
}

func TestViewSearchResults(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	m.SearchQuery = "test"
	results := []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1, Type: "bugfix", Title: "Fix bug", Content: "content", CreatedAt: time.Now()}},
	}
	m.SearchResults = results
	items := []list.Item{searchResultItem{result: results[0]}}
	m.SearchListModel.SetItems(items)
	m.SearchListModel.SetSize(116, 30)

	output := m.viewSearchResults()
	if !strings.Contains(output, "test") {
		t.Error("should show search query")
	}
	if !strings.Contains(output, "1 result") {
		t.Error("should show result count")
	}
	if !strings.Contains(output, "Fix bug") {
		t.Error("should show result title")
	}
}

func TestViewSearchResultsEmpty(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	m.SearchQuery = "nothing"
	m.SearchResults = nil

	output := m.viewSearchResults()
	if !strings.Contains(output, "No memories found") {
		t.Error("should show no results message")
	}
}

func TestViewRecent(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	obs := []*domain.Observation{
		{ID: 1, Type: "decision", Title: "Use BubbleTea", Content: "TUI framework", CreatedAt: time.Now(), Project: "cortex"},
	}
	m.RecentObservations = obs
	items := []list.Item{observationItem{obs: obs[0]}}
	m.RecentList.SetItems(items)
	m.RecentList.SetSize(116, 32)

	output := m.viewRecent()
	if !strings.Contains(output, "Recent Observations") {
		t.Error("should show header")
	}
	if !strings.Contains(output, "Use BubbleTea") {
		t.Error("should show observation title")
	}
}

func TestViewObservationDetailWithEnrichment(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	obs := &domain.Observation{
		ID:        42,
		Type:      "bugfix",
		Title:     "Fix N+1 query",
		Content:   "Added eager loading",
		SessionID: "session-1",
		Project:   "cortex",
		CreatedAt: time.Now(),
	}
	score := &domain.ImportanceScore{
		ObservationID: 42,
		Score:         3.7,
		AccessCount:   15,
	}
	entities := []*domain.EntityLink{
		{EntityType: "file", EntityValue: "store.go"},
		{EntityType: "package", EntityValue: "sqlitestore"},
	}
	edges := []*domain.Edge{
		{FromObsID: 42, ToObsID: 43, RelationType: "references", Weight: 1.0},
	}
	m.SelectedObservation = obs
	m.DetailScore = score
	m.DetailEntities = entities
	m.DetailEdges = edges

	// Build viewport content as the update handler would
	contentStr := buildDetailContent(obs, score, entities, edges)
	m.DetailViewport = viewport.New(m.Width-4, m.Height-8)
	m.DetailViewport.SetContent(contentStr)

	output := m.viewObservationDetail()
	if !strings.Contains(output, "3.7/5.0") {
		t.Error("should show importance score")
	}
	if !strings.Contains(output, "store.go") {
		t.Error("should show entity link")
	}
	if !strings.Contains(output, "references") {
		t.Error("should show edge relationship")
	}
	if !strings.Contains(output, "g graph") {
		t.Error("should show graph shortcut in help")
	}
}

func TestViewTimeline(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	m.TimelineFocus = &domain.Observation{
		ID: 10, Type: "decision", Title: "Focus obs",
		Content: "Focus content", SessionID: "s1", Project: "cortex",
	}
	m.TimelineBefore = []*domain.Observation{
		{ID: 9, Type: "bugfix", Title: "Before obs"},
	}
	m.TimelineAfter = []*domain.Observation{
		{ID: 11, Type: "discovery", Title: "After obs"},
	}

	output := m.viewTimeline()
	if !strings.Contains(output, "Focus obs") {
		t.Error("should show focus observation")
	}
	if !strings.Contains(output, "Before") {
		t.Error("should show before section")
	}
	if !strings.Contains(output, "After") {
		t.Error("should show after section")
	}
}

func TestViewSessions(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	sessions := []*session.SessionStats{
		{
			Session:          &domain.Session{ID: "s1", Project: "cortex", StartedAt: time.Now(), Summary: "Test session"},
			ObservationCount: 5,
		},
	}
	m.Sessions = sessions
	items := []list.Item{sessionItem{session: sessions[0]}}
	m.SessionListModel.SetItems(items)
	m.SessionListModel.SetSize(116, 32)

	output := m.viewSessions()
	if !strings.Contains(output, "Sessions") {
		t.Error("should show sessions header")
	}
	if !strings.Contains(output, "cortex") {
		t.Error("should show project name")
	}
}

func TestViewGraph(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	m.GraphRootID = 1
	obs := []*domain.Observation{
		{ID: 2, Type: "decision", Title: "Related obs", Content: "related"},
	}
	edges := []*domain.Edge{
		{FromObsID: 1, ToObsID: 2, RelationType: "references", Weight: 1.0},
	}
	m.GraphObservations = obs
	m.GraphEdges = edges
	items := []list.Item{graphItem{obs: obs[0], edgeLabel: "references"}}
	m.GraphListModel.SetItems(items)
	m.GraphListModel.SetSize(116, 28)

	output := m.viewGraph()
	if !strings.Contains(output, "Knowledge Graph") {
		t.Error("should show graph header")
	}
	if !strings.Contains(output, "references") {
		t.Error("should show edge types")
	}
	if !strings.Contains(output, "Related obs") {
		t.Error("should show related observation")
	}
	if !strings.Contains(output, "r re-root") {
		t.Error("should show re-root shortcut")
	}
}

func TestViewArchive(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	obs := []*domain.Observation{
		{ID: 1, Type: "old", Title: "Archived item", Content: "old content", CreatedAt: time.Now()},
	}
	m.ArchivedObservations = obs
	items := []list.Item{observationItem{obs: obs[0]}}
	m.ArchiveList.SetItems(items)
	m.ArchiveList.SetSize(116, 32)

	output := m.viewArchive()
	if !strings.Contains(output, "Archived Observations") {
		t.Error("should show archive header")
	}
	if !strings.Contains(output, "Archived item") {
		t.Error("should show archived observation")
	}
}

func TestViewArchiveEmpty(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	m.ArchivedObservations = nil

	output := m.viewArchive()
	if !strings.Contains(output, "No archived observations") {
		t.Error("should show empty message")
	}
}

func TestViewSetup(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	m.SetupAgents = []setup.Agent{
		{Name: "claude-code", Description: "Claude Code plugin", InstallDir: "/tmp/.claude/mcp"},
	}

	output := m.viewSetup()
	if !strings.Contains(output, "Setup") {
		t.Error("should show setup header")
	}
	if !strings.Contains(output, "Claude Code plugin") {
		t.Error("should show agent description")
	}
	if !strings.Contains(output, "Install to:") {
		t.Error("should show install directory")
	}
}

func TestViewSetupAllowlistPrompt(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	m.SetupAllowlistPrompt = true
	m.SetupResult = &setup.Result{Agent: "claude-code", Destination: "/tmp/mcp", Files: 1}

	output := m.viewSetup()
	if !strings.Contains(output, "Permissions Allowlist") {
		t.Error("should show allowlist prompt")
	}
	if !strings.Contains(output, "[y] Yes") {
		t.Error("should show y/n options")
	}
}

func TestViewSetupDoneClaudeCode(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	m.SetupDone = true
	m.SetupResult = &setup.Result{Agent: "claude-code", Destination: "/tmp/mcp", Files: 1}
	m.SetupAllowlistApplied = true

	output := m.viewSetup()
	if !strings.Contains(output, "Cortex tools added to allowlist") {
		t.Error("should show allowlist success")
	}
	if !strings.Contains(output, "claude plugin list") {
		t.Error("should show claude-code specific instructions")
	}
}

func TestViewSetupDoneOpencode(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	m.SetupDone = true
	m.SetupResult = &setup.Result{Agent: "opencode", Destination: "/tmp/config", Files: 2}

	output := m.viewSetup()
	if !strings.Contains(output, "Restart OpenCode") {
		t.Error("should show opencode-specific instructions")
	}
	if !strings.Contains(output, "(2 files)") {
		t.Error("should show file count")
	}
}

func TestViewError(t *testing.T) {
	m := New(&Deps{Version: "dev"})
	m.Width, m.Height = 120, 40
	m.ErrorMsg = "something went wrong"

	output := m.View()
	if !strings.Contains(output, "something went wrong") {
		t.Error("should show error message")
	}
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"short", 10, "short"},
		{"a longer string here", 10, "a longer s..."},
		{"", 5, ""},
		{"with\nnewlines", 20, "with newlines"},
	}

	for _, tt := range tests {
		got := truncateStr(tt.input, tt.max)
		if got != tt.want {
			t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
		}
	}
}

func TestFormatTime(t *testing.T) {
	zero := time.Time{}
	if got := formatTime(zero); got != "—" {
		t.Errorf("formatTime(zero) = %q, want %q", got, "—")
	}

	now := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	got := formatTime(now)
	if got == "" || got == "—" {
		t.Errorf("formatTime(now) = %q", got)
	}
}

func TestEntityIcon(t *testing.T) {
	tests := map[string]string{
		"file":    "F",
		"url":     "U",
		"package": "P",
		"symbol":  "S",
		"other":   "*",
	}
	for input, want := range tests {
		if got := entityIcon(input); got != want {
			t.Errorf("entityIcon(%q) = %q, want %q", input, got, want)
		}
	}
}

// ─── Small Terminal ─────────────────────────────────────────────────────────

func TestViewDashboardSmallTerminal(t *testing.T) {
	m := New(&Deps{Version: "v0.1.0"})
	m.Width, m.Height = 20, 10
	m.Stats = &combinedStats{TotalObservations: 1}

	// Should not panic
	output := m.viewDashboard()
	if output == "" {
		t.Error("should produce output even on small terminal")
	}
}

func TestViewRecentSmallTerminal(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 20, 10
	obs := []*domain.Observation{
		{ID: 1, Type: "test", Title: "T", Content: "C", CreatedAt: time.Now()},
	}
	m.RecentObservations = obs
	items := []list.Item{observationItem{obs: obs[0]}}
	m.RecentList.SetItems(items)
	m.RecentList.SetSize(16, 2)

	output := m.viewRecent()
	if output == "" {
		t.Error("should produce output even on small terminal")
	}
}

func TestViewObservationDetailSmallTerminal(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 20, 10
	obs := &domain.Observation{
		ID: 1, Type: "test", Title: "Title", Content: "Content content content",
		CreatedAt: time.Now(),
	}
	m.SelectedObservation = obs

	// Build viewport content as the update handler would
	contentStr := buildDetailContent(obs, nil, nil, nil)
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

	output := m.viewObservationDetail()
	if output == "" {
		t.Error("should produce output even on small terminal")
	}
}

func TestViewGraphEmpty(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	m.GraphRootID = 1
	m.GraphObservations = nil

	output := m.viewGraph()
	if !strings.Contains(output, "No related observations") {
		t.Error("should show empty message for graph")
	}
}

func TestRenderNewObsModal(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	m.NewObsModalOpen = true
	m.NewObsTitleInput.SetValue("My Test Memory")
	m.NewObsContentInput.SetValue("Content details")

	output := m.renderNewObsModal()
	if !strings.Contains(output, "Quick Create Memory") {
		t.Errorf("expected modal header in output, got %q", output)
	}
	if !strings.Contains(output, "My Test Memory") {
		t.Errorf("expected input value in output, got %q", output)
	}
	if !strings.Contains(output, "Save Observation") {
		t.Errorf("expected save button in output, got %q", output)
	}
}



package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/store/session"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
)

func newTestModel(width, height int) Model {
	m := New(&Deps{Version: "v1.0.0"})
	m.Width, m.Height = width, height
	return m
}

func newTestObs(id int64, obsType, title string) *domain.Observation {
	return &domain.Observation{ID: id, Type: obsType, Title: title, Content: "Content for " + title,
		CreatedAt: time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC), Project: "cortex"}
}

func observationItems(obs ...*domain.Observation) []list.Item {
	items := make([]list.Item, len(obs))
	for i, o := range obs {
		items[i] = observationItem{obs: o}
	}
	return items
}

func seedSearchItems(m *Model) {
	m.SearchResults = []*domain.SearchResult{{Observation: domain.Observation{ID: 1, Title: "R", Content: "c"}}}
	m.SearchListModel.SetItems([]list.Item{searchResultItem{result: m.SearchResults[0]}})
	m.SearchListModel.SetSize(116, 30)
}

func seedRecentItems(m *Model) {
	obs := newTestObs(1, "decision", "Obs")
	m.RecentObservations = []*domain.Observation{obs}
	m.RecentList.SetItems(observationItems(obs))
	m.RecentList.SetSize(116, 30)
}

func seedSessionItems(m *Model) {
	s := []*session.SessionStats{{Session: &domain.Session{ID: "s1", Project: "p"}, ObservationCount: 2}}
	m.Sessions = s
	m.SessionListModel.SetItems([]list.Item{sessionItem{session: s[0]}})
	m.SessionListModel.SetSize(116, 30)
}

func seedGraphItems(m *Model) {
	m.GraphRootID = 5
	m.GraphObservations = []*domain.Observation{newTestObs(5, "decision", "G")}
	m.GraphEdges = []*domain.Edge{{FromObsID: 5, ToObsID: 5, RelationType: "ref"}}
	m.GraphListModel.SetItems([]list.Item{graphItem{obs: m.GraphObservations[0], edgeLabel: "ref"}})
	m.GraphListModel.SetSize(116, 28)
}

func seedArchiveItems(m *Model) {
	obs := newTestObs(2, "old", "A")
	m.ArchivedObservations = []*domain.Observation{obs}
	m.ArchiveList.SetItems(observationItems(obs))
	m.ArchiveList.SetSize(116, 30)
}

func TestViewRoutesAllScreens(t *testing.T) {
	obs := newTestObs(1, "decision", "Seed")
	sessions := []*session.SessionStats{{Session: &domain.Session{ID: "s1", Project: "cortex", StartedAt: time.Now()}, ObservationCount: 2}}
	cases := []struct {
		name   string
		screen Screen
		setup  func(*Model)
	}{
		{"search", ScreenSearch, nil},
		{"searchResults", ScreenSearchResults, seedSearchItems},
		{"timeline", ScreenTimeline, func(m *Model) {
			m.TimelineFocus = obs
			m.TimelineBefore = []*domain.Observation{newTestObs(9, "bugfix", "B")}
			m.TimelineAfter = []*domain.Observation{newTestObs(11, "discovery", "A")}
		}},
		{"sessions", ScreenSessions, seedSessionItems},
		{"sessionDetail", ScreenSessionDetail, func(m *Model) {
			m.Sessions, m.SelectedSessionIdx = sessions, 0
			m.SessionObservations = []*domain.Observation{obs}
		}},
		{"setup", ScreenSetup, nil},
		{"graph", ScreenGraph, seedGraphItems},
		{"archive", ScreenArchive, seedArchiveItems},
		{"health", ScreenHealth, func(m *Model) {
			m.HealthObsCount, m.HealthEdgeCount, m.HealthCandidates = 10, 15, []healthCandidate{{topicKey: "arch/x", count: 3}}
		}},
		{"embedding", ScreenEmbeddingConfig, func(m *Model) {
			m.EmbCfgSaved, m.EmbCfgProvider, m.EmbCfgOllamaChecked, m.EmbCfgOllamaRunning, m.EmbCfgOllamaHasModel = true, 1, true, true, true
		}},
		{"localConfig", ScreenLocalConfig, nil},
		{"help", ScreenHelp, func(m *Model) { m.PrevScreen = ScreenDashboard }},
		{"dashboard", ScreenDashboard, func(m *Model) { m.Stats = &combinedStats{TotalObservations: 5} }},
		{"recent", ScreenRecent, seedRecentItems},
		{"observationDetail", ScreenObservationDetail, func(m *Model) {
			m.SelectedObservation = newTestObs(1, "bugfix", "D")
			m.DetailViewport = viewport.New(80, 20)
			m.DetailViewport.SetContent("detail content")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(120, 40)
			m.Screen = tc.screen
			if tc.setup != nil {
				tc.setup(&m)
			}
			if m.View() == "" {
				t.Errorf("View() on screen %s returned empty", tc.name)
			}
		})
	}
}

func TestViewHealthBranches(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen, m.HealthObsCount, m.HealthEdgeCount = ScreenHealth, 10, 5
	m.HealthStale = []*domain.Observation{newTestObs(1, "bugfix", "S1"), newTestObs(2, "decision", "S2")}
	m.HealthOrphans = []*domain.Observation{newTestObs(3, "discovery", "O")}
	m.HealthCandidates = []healthCandidate{{topicKey: "arch/auth", count: 5}}
	for _, want := range []string{"Memory Health Dashboard", "Stale (2)", "Orphans (1)", "Consolidation (1)", "moderate"} {
		if !strings.Contains(m.viewHealth(), want) {
			t.Errorf("viewHealth missing %q", want)
		}
	}
	empty := newTestModel(120, 40)
	empty.Screen, empty.HealthObsCount, empty.HealthEdgeCount = ScreenHealth, 20, 25
	for _, want := range []string{"Stale (0)", "Orphans (0)", "Consolidation (0)", "all high-score", "healthy"} {
		if !strings.Contains(empty.viewHealth(), want) {
			t.Errorf("empty viewHealth missing %q", want)
		}
	}
	if out := renderHealthTable(nil); !strings.Contains(out, "No items") {
		t.Errorf("expected empty table message, got %q", out)
	}
	outOfRange := newTestModel(120, 40)
	outOfRange.Sessions = []*session.SessionStats{{Session: &domain.Session{ID: "s1"}}}
	outOfRange.SelectedSessionIdx = 99
	if !strings.Contains(outOfRange.viewSessionDetail(), "Session not found") {
		t.Error("expected 'Session not found'")
	}
	noObs := newTestModel(120, 40)
	noObs.Sessions = []*session.SessionStats{{Session: &domain.Session{ID: "s1", Project: "p"}}}
	if !strings.Contains(noObs.viewSessionDetail(), "No observations in this session") {
		t.Error("expected empty observations message")
	}
}

func TestViewEmbeddingConfigBranches(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*Model)
		want  []string
	}{
		{"default", nil, []string{"Embedding Settings"}},
		{"dirty", func(m *Model) { m.EmbCfgDirty = true }, []string{"Unsaved changes"}},
		{"saving", func(m *Model) { m.EmbCfgSaving = true }, []string{"Saving"}},
		{"error", func(m *Model) { m.EmbCfgError = "save failed" }, []string{"save failed"}},
		{"reindex-warning", func(m *Model) { m.EmbCfgSaved, m.EmbCfgReindexWarning = true, true }, []string{"Configuration saved", "Provider/model changed", "reindex all observations"}},
		{"reindexing", func(m *Model) { m.EmbCfgSaved, m.EmbCfgReindexing, m.ReindexTotal, m.ReindexDone = true, true, 10, 5 }, []string{"Reindexing"}},
		{"reindex-progress", func(m *Model) { m.EmbCfgSaved, m.EmbCfgReindexProgress = true, "All done!" }, []string{"All done!"}},
		{"ollama-running-model", func(m *Model) {
			m.EmbCfgSaved, m.EmbCfgProvider, m.EmbCfgOllamaChecked, m.EmbCfgOllamaRunning, m.EmbCfgOllamaHasModel = true, 1, true, true, true
		}, []string{"Ollama Status", "Running", "Model: found"}},
		{"ollama-running-nomodel", func(m *Model) {
			m.EmbCfgSaved, m.EmbCfgProvider, m.EmbCfgOllamaChecked, m.EmbCfgOllamaRunning = true, 1, true, true
		}, []string{"Model: not found"}},
		{"ollama-stopped", func(m *Model) { m.EmbCfgSaved, m.EmbCfgProvider, m.EmbCfgOllamaChecked = true, 1, true }, []string{"Stopped"}},
		{"ollama-checking", func(m *Model) { m.EmbCfgSaved, m.EmbCfgProvider = true, 1 }, []string{"Checking"}},
		{"ollama-starting", func(m *Model) { m.EmbCfgSaved, m.EmbCfgProvider, m.EmbCfgStarting = true, 1, true }, []string{"Starting Ollama"}},
		{"ollama-pulling", func(m *Model) { m.EmbCfgSaved, m.EmbCfgProvider, m.EmbCfgPulling = true, 1, true }, []string{"Pulling model"}},
		{"vector-enabled", func(m *Model) { m.EmbCfgVector, m.EmbCfgAutoStart = true, true }, []string{"Enabled", "[x]"}},
		{"model-focused", func(m *Model) { m.EmbCfgModel.SetValue("my-model"); m.EmbCfgModel.Focus() }, []string{"my-model", "Type model name"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(120, 40)
			if tc.setup != nil {
				tc.setup(&m)
			}
			out := m.viewEmbeddingConfig()
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("viewEmbeddingConfig %s missing %q", tc.name, want)
				}
			}
		})
	}
}

func TestStaticViewsAndOverlays(t *testing.T) {
	auth := newTestModel(120, 40)
	auth.AuthModalOpen = true
	for _, want := range []string{"Connect to Cortex Server", "Server URL", "Bearer Token", "Hybrid", "Fast local SQLite"} {
		if !strings.Contains(auth.renderAuthModal(), want) {
			t.Errorf("renderAuthModal missing %q", want)
		}
	}
	for _, field := range []int{0, 1, 2} {
		m := newTestModel(120, 40)
		m.AuthFocusField = field
		if m.renderAuthModal() == "" {
			t.Errorf("auth modal focus %d empty", field)
		}
	}
	palette := newTestModel(120, 40)
	palette.CmdPaletteOpen = true
	if !strings.Contains(palette.viewCmdPalette(), "Type a command") {
		t.Error("missing palette input")
	}
	help := newTestModel(120, 40)
	for _, want := range []string{"Keyboard Reference", "Global", "Embedding Settings", "Detail View"} {
		if !strings.Contains(help.viewHelp(), want) {
			t.Errorf("viewHelp missing %q", want)
		}
	}
	compact := newTestModel(30, 10)
	compact.Screen = ScreenRecent
	if compact.renderDeckHeader() != "" {
		t.Error("compact terminal should return empty header")
	}
	deck := newTestModel(120, 40)
	deck.Screen = ScreenRecent
	deck.UploadToCortex, deck.IsDarkTheme, deck.UserRole, deck.CurrentUser = true, false, "admin", "testuser"
	for _, want := range []string{"CORTEX", "cloud sync", "light", "admin"} {
		if !strings.Contains(deck.renderDeckHeader(), want) {
			t.Errorf("renderDeckHeader missing %q", want)
		}
	}
	if out := deck.renderObservationListItem(0, 1, "bugfix", "Fix bug", "Content here", time.Now(), "myproject", nil); !strings.Contains(out, "Fix bug") || !strings.Contains(out, "myproject") {
		t.Error("renderObservationListItem missing content")
	}
	overlays := []struct {
		name  string
		setup func(*Model)
		want  string
	}{
		{"toast-warning", func(m *Model) { m.ToastMessage, m.ToastType = "warning msg", "warning" }, "warning msg"},
		{"toast-error", func(m *Model) { m.ToastMessage, m.ToastType = "error msg", "error" }, "error msg"},
		{"toast-success", func(m *Model) { m.ToastMessage, m.ToastType = "ok msg", "success" }, "ok msg"},
		{"confirm-delete", func(m *Model) { m.ConfirmDelete, m.DeleteTargetTitle = true, "Some Observation" }, "Some Observation"},
		{"confirm-delete-truncation", func(m *Model) { m.ConfirmDelete, m.DeleteTargetTitle = true, strings.Repeat("A", 50) }, "..."},
		{"cmd-palette", func(m *Model) { m.CmdPaletteOpen = true }, "Type a command"},
		{"auth-modal", func(m *Model) { m.AuthModalOpen = true }, "Connect to Cortex Server"},
		{"new-obs-modal", func(m *Model) { m.NewObsModalOpen = true }, "Quick Create Memory"},
	}
	for _, tc := range overlays {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(120, 40)
			tc.setup(&m)
			if !strings.Contains(m.View(), tc.want) {
				t.Errorf("View overlay %s missing %q", tc.name, tc.want)
			}
		})
	}
}

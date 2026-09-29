package tui

import (
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/store/session"

	"github.com/charmbracelet/bubbles/viewport"
)

type benchScreen struct {
	name  string
	model Model
}

// benchmarkScreens builds one representative, populated model per TUI screen so
// BenchmarkView renders a real view path instead of an empty fallback. The set
// mirrors the Screen iota range exactly.
func benchmarkScreens() []benchScreen {
	obs := newTestObs(1, "decision", "Seed")
	sessions := []*session.SessionStats{{
		Session:          &domain.Session{ID: "s1", Project: "cortex", StartedAt: time.Now()},
		ObservationCount: 2,
	}}

	cases := []struct {
		name   string
		screen Screen
		setup  func(*Model)
	}{
		{"dashboard", ScreenDashboard, func(m *Model) {
			m.Stats = &combinedStats{TotalObservations: 5}
		}},
		{"search", ScreenSearch, nil},
		{"searchResults", ScreenSearchResults, seedSearchItems},
		{"recent", ScreenRecent, seedRecentItems},
		{"observationDetail", ScreenObservationDetail, func(m *Model) {
			m.SelectedObservation = newTestObs(1, "bugfix", "D")
			m.DetailViewport = viewport.New(80, 20)
			m.DetailViewport.SetContent("detail content")
		}},
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
			m.HealthObsCount, m.HealthEdgeCount = 10, 15
			m.HealthCandidates = []healthCandidate{{topicKey: "arch/x", count: 3}}
		}},
		{"embedding", ScreenEmbeddingConfig, func(m *Model) {
			m.EmbCfgSaved, m.EmbCfgProvider = true, 1
			m.EmbCfgOllamaChecked, m.EmbCfgOllamaRunning, m.EmbCfgOllamaHasModel = true, true, true
		}},
		{"localConfig", ScreenLocalConfig, nil},
		{"help", ScreenHelp, func(m *Model) { m.PrevScreen = ScreenDashboard }},
	}

	models := make([]benchScreen, 0, len(cases))
	for _, tc := range cases {
		m := newTestModel(120, 40)
		m.Screen = tc.screen
		if tc.setup != nil {
			tc.setup(&m)
		}
		models = append(models, benchScreen{name: tc.name, model: m})
	}
	return models
}

// BenchmarkView captures the W5 TUI-render baseline: the full View() string
// rebuild across every screen at a 120x40 terminal. Models are built once and
// View() is a pure value-receiver render, so each iteration isolates
// presentation cost.
func BenchmarkView(b *testing.B) {
	models := benchmarkScreens()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, entry := range models {
			if out := entry.model.View(); out == "" {
				b.Fatalf("View() on screen %s returned empty", entry.name)
			}
		}
	}
}

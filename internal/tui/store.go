// Package tui implements the BubbleTea terminal UI for Cortex.
//
// Following BubbleTea patterns:
// - Screen constants as iota
// - Single Model struct holds ALL state
// - Update() with type switch
// - Per-screen key handlers returning (tea.Model, tea.Cmd)
// - Vim keys (j/k) for navigation
// - PrevScreen for back navigation
package tui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lleontor705/cortex/internal/app"
	"github.com/lleontor705/cortex/internal/config"
	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/ollama"
	entitystore "github.com/lleontor705/cortex/internal/store/entity"
	graphstore "github.com/lleontor705/cortex/internal/store/graph"
	scoringstore "github.com/lleontor705/cortex/internal/store/scoring"
	"github.com/lleontor705/cortex/internal/store/search"
	"github.com/lleontor705/cortex/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
	"github.com/lleontor705/cortex/internal/update"

	tea "github.com/charmbracelet/bubbletea"
)

// Deps bundles all store dependencies for the TUI.
type Deps struct {
	Observations *sqlitestore.Store
	Sessions     *session.Store
	Search       *search.Store
	Graph        *graphstore.Store
	Scoring      *scoringstore.Store
	Entities     *entitystore.Store
	App          *app.App
	Config       *config.Config
	Version      string
}

// ─── Commands (async data loading) ──────────────────────────────────────────

func checkForUpdate(version string) tea.Cmd {
	return func() tea.Msg {
		result := update.Check(version)
		return updateCheckMsg{result: result}
	}
}

func loadStats(d *Deps) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.Observations == nil {
			return statsLoadedMsg{err: fmt.Errorf("observations store not available")}
		}
		ctx := context.Background()
		stats, err := d.Observations.Stats(ctx)
		if err != nil {
			return statsLoadedMsg{err: err}
		}

		// Enrich with session count and edge count (non-critical — default to 0 on error)
		var sessionCount, edgeCount int
		var wg sync.WaitGroup
		if d.Sessions != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sessions, _ := d.Sessions.RecentWithStats(ctx, "", 1000)
				sessionCount = len(sessions)
			}()
		}
		if d.Graph != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				edgeCount, _ = d.Graph.CountAllEdges(ctx)
			}()
		}
		wg.Wait()

		return statsLoadedMsg{
			stats: &combinedStats{
				TotalObservations: stats.TotalObservations,
				TotalSessions:     sessionCount,
				TotalEdges:        edgeCount,
				Projects:          stats.Projects,
				ByType:            stats.ByType,
			},
		}
	}
}

func loadActivityData(d *Deps) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.Observations == nil {
			return activityDataMsg{daily: make([]int, 7)}
		}
		ctx := context.Background()
		daily := make([]int, 7)
		now := time.Now()
		for i := 0; i < 7; i++ {
			dayStart := time.Date(now.Year(), now.Month(), now.Day()-6+i, 0, 0, 0, 0, now.Location())
			dayEnd := dayStart.Add(24 * time.Hour)
			obs, _ := d.Observations.List(ctx, domain.ObservationFilter{
				CreatedAfter:  &dayStart,
				CreatedBefore: &dayEnd,
				Limit:         1000,
			})
			daily[i] = len(obs)
		}
		return activityDataMsg{daily: daily}
	}
}

func searchMemories(d *Deps, query string, project string) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.Search == nil {
			return searchResultsMsg{err: fmt.Errorf("search store not available")}
		}
		ctx := context.Background()
		results, err := d.Search.Search(ctx, query, domain.SearchOptions{Limit: 50, Project: project})
		return searchResultsMsg{results: results, query: query, err: err}
	}
}

func loadRecentObservations(d *Deps, project string) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.Observations == nil {
			return recentObservationsMsg{err: fmt.Errorf("observations store not available")}
		}
		ctx := context.Background()
		obs, err := d.Observations.List(ctx, domain.ObservationFilter{Limit: 50, Project: project})
		return recentObservationsMsg{observations: obs, err: err}
	}
}

func loadObservationDetail(d *Deps, id int64) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.Observations == nil {
			return observationDetailMsg{err: fmt.Errorf("observations store not available")}
		}
		ctx := context.Background()
		obs, err := d.Observations.GetByID(ctx, id)
		if err != nil {
			return observationDetailMsg{err: err}
		}

		// Concurrent enrichment with Cortex-exclusive data (non-critical — nil on error)
		var score *domain.ImportanceScore
		var entities []*domain.EntityLink
		var edges []*domain.Edge
		var wg sync.WaitGroup
		if d.Scoring != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				score, _ = d.Scoring.GetScore(ctx, id)
			}()
		}
		if d.Entities != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				entities, _ = d.Entities.GetByObservation(ctx, id)
			}()
		}
		if d.Graph != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				edges, _ = d.Graph.GetEdgesForObservation(ctx, id)
			}()
		}
		wg.Wait()

		return observationDetailMsg{
			observation: obs,
			score:       score,
			entities:    entities,
			edges:       edges,
		}
	}
}

func loadTimeline(d *Deps, obsID int64) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.Observations == nil {
			return timelineMsg{err: fmt.Errorf("observations store not available")}
		}
		ctx := context.Background()

		obs, err := d.Observations.GetByID(ctx, obsID)
		if err != nil {
			return timelineMsg{err: err}
		}

		createdAt := obs.CreatedAt

		var before, after []*domain.Observation
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			before, _ = d.Observations.List(ctx, domain.ObservationFilter{
				SessionID:     obs.SessionID,
				CreatedBefore: &createdAt,
				Limit:         10,
			})
		}()
		go func() {
			defer wg.Done()
			after, _ = d.Observations.List(ctx, domain.ObservationFilter{
				SessionID:    obs.SessionID,
				CreatedAfter: &createdAt,
				Limit:        10,
				OrderAsc:     true,
			})
		}()
		wg.Wait()

		return timelineMsg{
			focus:  obs,
			before: before,
			after:  after,
		}
	}
}

func loadRecentSessions(d *Deps) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.Sessions == nil {
			return recentSessionsMsg{err: fmt.Errorf("sessions store not available")}
		}
		ctx := context.Background()
		sessions, err := d.Sessions.RecentWithStats(ctx, "", 50)
		return recentSessionsMsg{sessions: sessions, err: err}
	}
}

func loadSessionObservations(d *Deps, sessionID string) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.Observations == nil {
			return sessionObservationsMsg{err: fmt.Errorf("observations store not available")}
		}
		ctx := context.Background()
		obs, err := d.Observations.List(ctx, domain.ObservationFilter{
			SessionID: sessionID,
			Limit:     200,
			OrderAsc:  true,
		})
		return sessionObservationsMsg{observations: obs, err: err}
	}
}

func loadGraphRelated(d *Deps, obsID int64) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.Graph == nil {
			return graphLoadedMsg{err: fmt.Errorf("graph store not available")}
		}
		ctx := context.Background()
		related, err := d.Graph.GetRelated(ctx, obsID, 2)
		if err != nil {
			return graphLoadedMsg{err: err}
		}
		edges, _ := d.Graph.GetEdgesForObservation(ctx, obsID)
		return graphLoadedMsg{related: related, edges: edges}
	}
}

func loadArchivedObservations(d *Deps, project string) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.Observations == nil {
			return archiveLoadedMsg{err: fmt.Errorf("observations store not available")}
		}
		ctx := context.Background()
		obs, err := d.Observations.List(ctx, domain.ObservationFilter{
			Limit:           50,
			IncludeArchived: true,
			Project:         project,
		})
		return archiveLoadedMsg{observations: obs, err: err}
	}
}

func loadHealthData(d *Deps, project string) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.Observations == nil {
			return healthLoadedMsg{err: fmt.Errorf("observations store not available")}
		}
		ctx := context.Background()

		var stale, orphans []*domain.Observation
		var edgeCount, obsCount int
		var candidates []healthCandidate
		var wg sync.WaitGroup

		// Stale observations (high score but not accessed in 30 days)
		wg.Add(1)
		go func() {
			defer wg.Done()
			stale, _ = d.Observations.StaleObservations(ctx, project, 1.5, 30)
		}()

		// Orphan observations (no graph edges)
		wg.Add(1)
		go func() {
			defer wg.Done()
			orphans, _ = d.Observations.OrphanObservations(ctx, project, 20)
		}()

		// Graph density
		if d.Graph != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				edgeCount, _ = d.Graph.CountAllEdges(ctx)
			}()
		}

		// Observation count
		wg.Add(1)
		go func() {
			defer wg.Done()
			obsCount, _ = d.Observations.CountAll(ctx)
		}()

		// Consolidation candidates
		wg.Add(1)
		go func() {
			defer wg.Done()
			groups, _ := d.Observations.FindConsolidationCandidates(ctx, project, 2)
			for _, g := range groups {
				candidates = append(candidates, healthCandidate{topicKey: g.TopicKey, count: g.Count})
			}
		}()

		wg.Wait()

		return healthLoadedMsg{
			stale:      stale,
			orphans:    orphans,
			edgeCount:  edgeCount,
			obsCount:   obsCount,
			candidates: candidates,
		}
	}
}

// ─── Embedding Config Commands ─────────────────────────────────────────────

var embeddingProviders = []string{"none", "ollama", "openai"}

func saveEmbeddingConfig(d *Deps, provider int, model string, vector, autoStart bool) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.Config == nil {
			return configSavedMsg{err: fmt.Errorf("config not available")}
		}
		d.Config.Search.EmbeddingProvider = embeddingProviders[provider]
		d.Config.Search.EmbeddingModel = model
		d.Config.Search.Vector = vector
		d.Config.Search.OllamaAutoStart = autoStart
		if err := config.Save(d.Config, ""); err != nil {
			return configSavedMsg{err: err}
		}
		// Reload app to reinitialize embedding service with new config
		if d.App != nil {
			if err := d.App.ReloadConfig(); err != nil {
				return configSavedMsg{err: fmt.Errorf("config saved but reload failed: %w", err)}
			}
			d.Config = d.App.Config
		}
		return configSavedMsg{}
	}
}

func checkOllamaStatus(d *Deps) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		baseURL := ""
		model := ""
		if d != nil && d.Config != nil {
			baseURL = d.Config.Search.EmbeddingBaseURL
			model = d.Config.Search.EmbeddingModel
		}
		mgr := ollama.NewManager(baseURL)
		running := mgr.IsRunning(ctx)
		hasModel := false
		if running && model != "" {
			hasModel, _ = mgr.HasModel(ctx, model)
		}
		return ollamaStatusMsg{running: running, hasModel: hasModel}
	}
}

func startOllamaCmd(baseURL string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		mgr := ollama.NewManager(baseURL)
		err := mgr.EnsureRunning(ctx)
		return ollamaStartMsg{err: err}
	}
}

func pullOllamaModelCmd(baseURL, model string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		mgr := ollama.NewManager(baseURL)
		err := mgr.PullModel(ctx, model, nil)
		return ollamaPullMsg{done: true, err: err}
	}
}

func deleteObservationCmd(d *Deps, id int64) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.Observations == nil {
			return deleteObservationMsg{id: id, err: fmt.Errorf("observations store not available")}
		}
		ctx := context.Background()
		err := d.Observations.SoftDelete(ctx, id)
		return deleteObservationMsg{id: id, err: err}
	}
}

func unarchiveObservationCmd(d *Deps, id int64) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.Observations == nil {
			return unarchiveObservationMsg{id: id, err: fmt.Errorf("observations store not available")}
		}
		ctx := context.Background()
		err := d.Observations.Unarchive(ctx, id)
		return unarchiveObservationMsg{id: id, err: err}
	}
}

func startReindexCmd(d *Deps) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.App == nil {
			return reindexProgressMsg{done: true, err: fmt.Errorf("app not available")}
		}

		// Reload config to pick up new provider/model
		if err := d.App.ReloadConfig(); err != nil {
			return reindexProgressMsg{done: true, err: fmt.Errorf("reload config: %w", err)}
		}
		d.Config = d.App.Config

		if d.App.Stores.Embeddings == nil {
			return reindexProgressMsg{done: true, err: fmt.Errorf("no embedding provider configured")}
		}
		if d.App.Stores.Vectors == nil || !domain.IsVectorIndexHealthy(context.Background(), d.App.Stores.Vectors) {
			return reindexProgressMsg{done: true, err: fmt.Errorf("vector store not available (build with -tags cortex_vectors)")}
		}

		ctx := context.Background()
		obs, err := d.Observations.List(ctx, domain.ObservationFilter{Limit: 5000})
		if err != nil {
			return reindexProgressMsg{done: true, err: fmt.Errorf("list observations: %w", err)}
		}

		indexed, errCount := 0, 0
		for _, o := range obs {
			text := o.Title + "\n" + o.Content
			vec, embErr := d.App.Stores.Embeddings.Embed(ctx, text)
			if embErr != nil {
				errCount++
				continue
			}
			if storeErr := d.App.Stores.Vectors.Upsert(ctx, []domain.VectorPoint{{
			ID:     o.ID,
			Vector: vec,
			ModelInfo: domain.ModelInfo{
				Name:      d.App.Stores.Embeddings.Model(),
				Dimension: d.App.Stores.Embeddings.Dimensions(),
			},
		}}); storeErr != nil {
				errCount++
				continue
			}
			indexed++
		}

		progress := fmt.Sprintf("Reindexed %d/%d observations (%d errors)", indexed, len(obs), errCount)
		return reindexProgressMsg{progress: progress, done: true, total: len(obs), indexed: indexed}
	}
}

func reloadConfigCmd(d *Deps) tea.Cmd {
	return func() tea.Msg {
		if d == nil || d.App == nil {
			return configReloadedMsg{err: fmt.Errorf("app not available")}
		}
		if err := d.App.ReloadConfig(); err != nil {
			return configReloadedMsg{err: err}
		}
		// Update the Deps config pointer to the reloaded config
		d.Config = d.App.Config
		return configReloadedMsg{cfg: d.App.Config}
	}
}

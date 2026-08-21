package dna

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/database"
	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
	graphstore "github.com/lleontor705/cortex/internal/store/graph"
	scoringstore "github.com/lleontor705/cortex/internal/store/scoring"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
)

type mockLister struct {
	obs []*domain.Observation
}

func (m *mockLister) List(_ context.Context, _ domain.ObservationFilter) ([]*domain.Observation, error) {
	return m.obs, nil
}

// mockScoreProvider serves per-ID scores like the legacy scoring store.
type mockScoreProvider struct {
	scores map[int64]float64
	calls  int
}

func (m *mockScoreProvider) GetScore(_ context.Context, obsID int64) (*domain.ImportanceScore, error) {
	m.calls++
	if v, ok := m.scores[obsID]; ok {
		return &domain.ImportanceScore{ObservationID: obsID, Score: v}, nil
	}
	return nil, &domain.NotFoundError{Type: "importance_score", ID: obsID}
}

// batchScoreProvider adds the optional batch capability on top of the
// per-ID provider. Each batch call models exactly one SQL statement.
type batchScoreProvider struct {
	mockScoreProvider
	batchCalls int
	batchErr   error
	omit       map[int64]bool
}

func (m *batchScoreProvider) GetScoresByObservationIDs(_ context.Context, obsIDs []int64) (map[int64]*domain.ImportanceScore, error) {
	m.batchCalls++
	if m.batchErr != nil {
		return nil, m.batchErr
	}
	out := make(map[int64]*domain.ImportanceScore, len(obsIDs))
	for _, id := range obsIDs {
		if m.omit[id] {
			continue
		}
		if v, ok := m.scores[id]; ok {
			out[id] = &domain.ImportanceScore{ObservationID: id, Score: v}
		}
	}
	return out, nil
}

// mockEdgeCounter serves per-ID edge counts like the legacy graph store.
type mockEdgeCounter struct {
	counts map[int64]int
	calls  int
}

func (m *mockEdgeCounter) CountEdgesByObservation(_ context.Context, obsID int64) (int, error) {
	m.calls++
	return m.counts[obsID], nil
}

// batchEdgeCounter adds the optional batch capability on top of the per-ID
// counter. Each batch call models exactly one SQL statement.
type batchEdgeCounter struct {
	mockEdgeCounter
	batchCalls int
	batchErr   error
	omit       map[int64]bool
}

func (m *batchEdgeCounter) CountEdgesByObservationIDs(_ context.Context, obsIDs []int64) (map[int64]int, error) {
	m.batchCalls++
	if m.batchErr != nil {
		return nil, m.batchErr
	}
	out := make(map[int64]int, len(obsIDs))
	for _, id := range obsIDs {
		if m.omit[id] {
			continue
		}
		out[id] = m.counts[id]
	}
	return out, nil
}

func TestGenerateEmpty(t *testing.T) {
	svc := NewService(&mockLister{}, nil, nil)
	result, err := svc.Generate(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No observations found") {
		t.Fatalf("expected empty message, got: %s", result)
	}
}

func TestGenerateWithObservations(t *testing.T) {
	now := time.Now()
	obs := []*domain.Observation{
		{ID: 1, Type: domain.TypeDecision, Title: "Use SQLite", Content: "Chose SQLite for simplicity", CreatedAt: now},
		{ID: 2, Type: domain.TypePattern, Title: "Table-driven tests", Content: "All tests use table pattern", CreatedAt: now},
		{ID: 3, Type: domain.TypeBugfix, Title: "Fix N+1 query", Content: "Added eager loading in UserList", CreatedAt: now},
		{ID: 4, Type: domain.TypeConfig, Title: "WAL mode enabled", Content: "journal_mode=WAL", CreatedAt: now},
		{ID: 5, Type: domain.TypeDiscovery, Title: "FTS5 triggers", Content: "Must update on schema change", CreatedAt: now},
	}

	svc := NewService(&mockLister{obs: obs}, nil, nil)
	result, err := svc.Generate(context.Background(), "cortex")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []string{
		"Project DNA: cortex",
		"Key Decisions",
		"Use SQLite",
		"Patterns",
		"Table-driven tests",
		"Known Gotchas",
		"Fix N+1 query",
		"Configuration",
		"WAL mode",
		"Discoveries",
		"FTS5 triggers",
		"5 observations",
	}

	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("missing %q in DNA output", check)
		}
	}
}

// reverseObservations returns a reversed copy so two listers never share
// ordering, mirroring equivalent rows queried in different orders.
func reverseObservations(in []*domain.Observation) []*domain.Observation {
	out := make([]*domain.Observation, len(in))
	for i, o := range in {
		out[len(in)-1-i] = o
	}
	return out
}

// assertTitlesInOrder verifies that every title appears in the markdown and
// that their relative positions follow wantOrder.
func assertTitlesInOrder(t *testing.T, markdown string, wantOrder []string) {
	t.Helper()
	last := -1
	for _, title := range wantOrder {
		idx := strings.Index(markdown, title)
		if idx < 0 {
			t.Fatalf("missing title %q in DNA output:\n%s", title, markdown)
		}
		if idx < last {
			t.Fatalf("title %q at offset %d breaks expected order %v", title, idx, wantOrder)
		}
		last = idx
	}
}

func TestGenerateDeterministicTotalOrder(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// All observations tie on the default score 0.5 (no scoring provider).
	// Expected order: created_at descending, then observation ID descending.
	// IDs 7 and 4 share created_at, so the tie resolves by descending ID.
	obs := []*domain.Observation{
		{ID: 1, Type: domain.TypeDecision, Title: "D-one", Content: "c1", CreatedAt: base.Add(3 * time.Hour)},
		{ID: 2, Type: domain.TypeDecision, Title: "D-two", Content: "c2", CreatedAt: base.Add(1 * time.Hour)},
		{ID: 3, Type: domain.TypeDecision, Title: "D-three", Content: "c3", CreatedAt: base.Add(2 * time.Hour)},
		{ID: 4, Type: domain.TypeDecision, Title: "D-four", Content: "c4", CreatedAt: base.Add(4 * time.Hour)},
		{ID: 7, Type: domain.TypeDecision, Title: "D-seven", Content: "c7", CreatedAt: base.Add(4 * time.Hour)},
	}
	wantOrder := []string{"D-seven", "D-four", "D-one", "D-three", "D-two"}

	forward, err := NewService(&mockLister{obs: obs}, nil, nil).Generate(context.Background(), "cortex")
	if err != nil {
		t.Fatalf("forward generate: %v", err)
	}
	reversed, err := NewService(&mockLister{obs: reverseObservations(obs)}, nil, nil).Generate(context.Background(), "cortex")
	if err != nil {
		t.Fatalf("reversed generate: %v", err)
	}

	if forward != reversed {
		t.Fatalf("equivalent rows must produce byte-identical markdown regardless of query order")
	}
	assertTitlesInOrder(t, forward, wantOrder)
}

func TestGenerateDeterministicScorePrecedesTime(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Higher score wins even when created_at is older.
	obs := []*domain.Observation{
		{ID: 10, Type: domain.TypeDecision, Title: "S-high-old", Content: "c", CreatedAt: base},
		{ID: 11, Type: domain.TypeDecision, Title: "S-low-new", Content: "c", CreatedAt: base.Add(24 * time.Hour)},
	}
	scores := &mockScoreProvider{scores: map[int64]float64{10: 4.0, 11: 1.0}}

	out, err := NewService(&mockLister{obs: obs}, scores, nil).Generate(context.Background(), "cortex")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertTitlesInOrder(t, out, []string{"S-high-old", "S-low-new"})
}

// buildDNAFixture builds n observations with alternating types, tied scores
// for a deterministic prefix, and mixed created_at values.
func buildDNAFixture(n int) []*domain.Observation {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	types := []string{domain.TypeDecision, domain.TypePattern, domain.TypeConfig, domain.TypeBugfix, domain.TypeDiscovery}
	obs := make([]*domain.Observation, n)
	for i := range obs {
		obs[i] = &domain.Observation{
			ID:        int64(i + 1),
			Type:      types[i%len(types)],
			Title:     fmt.Sprintf("Title-%03d", i+1),
			Content:   "content",
			CreatedAt: base.Add(time.Duration(i%7) * time.Hour),
		}
	}
	return obs
}

func TestGenerateBatchHydrationQueryCount(t *testing.T) {
	const n = 500
	obs := buildDNAFixture(n)

	scores := make(map[int64]float64, n)
	for i := 1; i <= n; i++ {
		scores[int64(i)] = float64(i%5) + 0.5
	}
	counts := make(map[int64]int, n)
	for i := 1; i <= n; i++ {
		counts[int64(i)] = i % 3
	}
	scoring := &batchScoreProvider{mockScoreProvider: mockScoreProvider{scores: scores}}
	edges := &batchEdgeCounter{mockEdgeCounter: mockEdgeCounter{counts: counts}}

	out, err := NewService(&mockLister{obs: obs}, scoring, edges).Generate(context.Background(), "cortex")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if scoring.batchCalls != 1 {
		t.Errorf("expected exactly 1 batch score call, got %d", scoring.batchCalls)
	}
	if edges.batchCalls != 1 {
		t.Errorf("expected exactly 1 batch edge call, got %d", edges.batchCalls)
	}
	if scoring.calls != 0 {
		t.Errorf("legacy GetScore must not run on the batch path, got %d calls", scoring.calls)
	}
	if edges.calls != 0 {
		t.Errorf("legacy CountEdgesByObservation must not run on the batch path, got %d calls", edges.calls)
	}

	// One list plus one score statement plus one edge-count statement.
	statements := 1 + scoring.batchCalls + edges.batchCalls
	if statements > 3 {
		t.Fatalf("N=500 successful path must stay <=3 SQL statements, got %d", statements)
	}
	if !strings.Contains(out, "500 observations") {
		t.Errorf("expected the full fixture to be summarized, output:\n%s", out)
	}
}

func TestGenerateBatchErrorFallsBackPerProvider(t *testing.T) {
	obs := buildDNAFixture(12)
	scores := map[int64]float64{1: 4.0, 2: 3.0, 3: 2.0}
	counts := map[int64]int{1: 2, 2: 1}

	legacy := NewService(&mockLister{obs: obs},
		&mockScoreProvider{scores: scores},
		&mockEdgeCounter{counts: counts})
	want, err := legacy.Generate(context.Background(), "cortex")
	if err != nil {
		t.Fatalf("legacy generate: %v", err)
	}

	t.Run("score batch error", func(t *testing.T) {
		scoring := &batchScoreProvider{
			mockScoreProvider: mockScoreProvider{scores: scores},
			batchErr:          errors.New("batch unavailable"),
		}
		edges := &batchEdgeCounter{mockEdgeCounter: mockEdgeCounter{counts: counts}}

		got, err := NewService(&mockLister{obs: obs}, scoring, edges).Generate(context.Background(), "cortex")
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if got != want {
			t.Fatalf("batch score error must fall back to identical legacy output")
		}
		if scoring.calls != len(obs) {
			t.Errorf("expected per-item fallback for all %d observations, got %d calls", len(obs), scoring.calls)
		}
		if edges.batchCalls != 1 || edges.calls != 0 {
			t.Errorf("edge provider must stay batched when score provider fails: batch=%d legacy=%d", edges.batchCalls, edges.calls)
		}
	})

	t.Run("edge batch error", func(t *testing.T) {
		scoring := &batchScoreProvider{mockScoreProvider: mockScoreProvider{scores: scores}}
		edges := &batchEdgeCounter{
			mockEdgeCounter: mockEdgeCounter{counts: counts},
			batchErr:        errors.New("batch unavailable"),
		}

		got, err := NewService(&mockLister{obs: obs}, scoring, edges).Generate(context.Background(), "cortex")
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if got != want {
			t.Fatalf("batch edge error must fall back to identical legacy output")
		}
		if edges.calls != len(obs) {
			t.Errorf("expected per-item fallback for all %d observations, got %d calls", len(obs), edges.calls)
		}
		if scoring.batchCalls != 1 || scoring.calls != 0 {
			t.Errorf("score provider must stay batched when edge provider fails: batch=%d legacy=%d", scoring.batchCalls, scoring.calls)
		}
	})
}

func TestGenerateBatchMissingValuesKeepDefaults(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	obs := []*domain.Observation{
		{ID: 1, Type: domain.TypeDecision, Title: "Scored", Content: "c", CreatedAt: base},
		{ID: 2, Type: domain.TypeDecision, Title: "Unscored", Content: "c", CreatedAt: base.Add(time.Hour)},
	}
	scoring := &batchScoreProvider{
		mockScoreProvider: mockScoreProvider{scores: map[int64]float64{1: 4.0}},
		omit:              map[int64]bool{2: true},
	}
	edges := &batchEdgeCounter{
		mockEdgeCounter: mockEdgeCounter{counts: map[int64]int{1: 3}},
		omit:            map[int64]bool{2: true},
	}

	out, err := NewService(&mockLister{obs: obs}, scoring, edges).Generate(context.Background(), "cortex")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if !strings.Contains(out, "**Scored** (score: 4.0)") {
		t.Errorf("scored observation must render its batched score, output:\n%s", out)
	}
	if !strings.Contains(out, "**Unscored** (score: 0.5)") {
		t.Errorf("missing batch score must default to 0.5, output:\n%s", out)
	}
	if scoring.calls != 0 || edges.calls != 0 {
		t.Errorf("missing batch values must not trigger per-item fallback: score=%d edge=%d", scoring.calls, edges.calls)
	}
}

// dnaTimeFormat matches SQLite's datetime() text format used by the stores.
const dnaTimeFormat = "2006-01-02 15:04:05"

// dnaTestRegistry returns the migrations that build the sessions,
// observations, edges, and importance_scores tables plus the score trigger,
// mirroring the local composition the DNA service runs against.
func dnaTestRegistry() *migration.Registry {
	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1,
		Name:    "init",
		UpSQL: `
			CREATE TABLE IF NOT EXISTS sessions (
				id         TEXT PRIMARY KEY,
				project    TEXT NOT NULL,
				directory  TEXT NOT NULL,
				started_at TEXT NOT NULL DEFAULT (datetime('now')),
				ended_at   TEXT,
				summary    TEXT
			);

			CREATE TABLE IF NOT EXISTS observations (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				sync_id    TEXT,
				session_id TEXT    NOT NULL,
				type       TEXT    NOT NULL,
				title      TEXT    NOT NULL,
				content    TEXT    NOT NULL,
				tool_name  TEXT,
				project    TEXT,
				scope      TEXT    NOT NULL DEFAULT 'project',
				topic_key  TEXT,
				normalized_hash TEXT,
				revision_count INTEGER NOT NULL DEFAULT 1,
				duplicate_count INTEGER NOT NULL DEFAULT 1,
				last_seen_at TEXT,
				confidence REAL    NOT NULL DEFAULT 1.0,
				source     TEXT    NOT NULL DEFAULT 'manual',
				tags       TEXT,
				created_at TEXT    NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT    NOT NULL DEFAULT (datetime('now')),
				deleted_at TEXT,
				FOREIGN KEY (session_id) REFERENCES sessions(id)
			);
		`,
		DownSQL: `
			DROP TABLE IF EXISTS observations;
			DROP TABLE IF EXISTS sessions;
		`,
	})
	registry.Register(migration.Migration{
		Version: 3,
		Name:    "graph",
		UpSQL: `
			CREATE TABLE edges (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				from_obs_id INTEGER NOT NULL,
				to_obs_id INTEGER NOT NULL,
				relation_type TEXT NOT NULL,
				weight REAL NOT NULL DEFAULT 1.0,
				created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (from_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
				FOREIGN KEY (to_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
				UNIQUE(from_obs_id, to_obs_id, relation_type)
			);
		`,
		DownSQL: `DROP TABLE IF EXISTS edges;`,
	})
	registry.Register(migration.Migration{
		Version: 4,
		Name:    "scoring",
		UpSQL: `
			CREATE TABLE importance_scores (
				observation_id INTEGER PRIMARY KEY,
				score REAL NOT NULL DEFAULT 0.0,
				access_count INTEGER NOT NULL DEFAULT 0,
				last_accessed DATETIME,
				updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (observation_id) REFERENCES observations(id) ON DELETE CASCADE
			);
			CREATE INDEX idx_importance_score ON importance_scores(score DESC);

			CREATE TRIGGER importance_init AFTER INSERT ON observations
			BEGIN
				INSERT INTO importance_scores (observation_id, score, updated_at)
				VALUES (new.id, 0.0, CURRENT_TIMESTAMP);
			END;
		`,
		DownSQL: `
			DROP TRIGGER IF EXISTS importance_init;
			DROP TABLE IF EXISTS importance_scores;
		`,
	})
	return registry
}

// applyDNAMigrations applies the DNA test schema to an already-open database.
func applyDNAMigrations(tb testing.TB, db *sql.DB) {
	tb.Helper()

	migrator, err := migration.NewMigrator(db, "")
	if err != nil {
		tb.Fatalf("dna: create migrator: %v", err)
	}
	for _, m := range dnaTestRegistry().GetAll() {
		migrator.Register(m)
	}
	if err := migrator.Up(context.Background()); err != nil {
		tb.Fatalf("dna: apply migrations: %v", err)
	}
}

// newDNATestDB builds an in-memory SQLite database carrying the DNA test
// schema.
func newDNATestDB(tb testing.TB) *sql.DB {
	tb.Helper()

	mgr, err := database.NewManager(database.InMemoryConfig())
	if err != nil {
		tb.Fatalf("dna: create database manager: %v", err)
	}
	tb.Cleanup(func() { _ = mgr.Close() })
	db := mgr.DB()

	applyDNAMigrations(tb, db)
	return db
}

// seedDNAFixture inserts observations with explicit IDs (so equivalent rows
// keep identical IDs across databases) and a linear edge fan-out. The score
// trigger auto-creates 0.0 score rows; missingScoreIDs rows are deleted
// afterwards to model absent scores.
func seedDNAFixture(tb testing.TB, db *sql.DB, project string, obs []*domain.Observation, missingScoreIDs []int64) {
	tb.Helper()

	if _, err := db.Exec(`INSERT INTO sessions (id, project, directory) VALUES ('s1', ?, '/tmp/dna')`, project); err != nil {
		tb.Fatalf("dna: seed session: %v", err)
	}
	for _, o := range obs {
		stamp := o.CreatedAt.UTC().Format(dnaTimeFormat)
		if _, err := db.Exec(
			`INSERT INTO observations (id, session_id, type, title, content, project, scope, created_at, updated_at)
			 VALUES (?, 's1', ?, ?, ?, ?, 'project', ?, ?)`,
			o.ID, o.Type, o.Title, o.Content, project, stamp, stamp,
		); err != nil {
			tb.Fatalf("dna: seed observation %d: %v", o.ID, err)
		}
	}
	for i := 0; i+1 < len(obs); i += 2 {
		if _, err := db.Exec(
			`INSERT INTO edges (from_obs_id, to_obs_id, relation_type, weight) VALUES (?, ?, 'references', 1.0)`,
			obs[i].ID, obs[i+1].ID,
		); err != nil {
			tb.Fatalf("dna: seed edge %d->%d: %v", obs[i].ID, obs[i+1].ID, err)
		}
	}
	for _, id := range missingScoreIDs {
		if _, err := db.Exec(`DELETE FROM importance_scores WHERE observation_id = ?`, id); err != nil {
			tb.Fatalf("dna: drop score %d: %v", id, err)
		}
	}
}

// The local composition must expose the optional batch capabilities the DNA
// service detects; these pins fail to compile until the stores implement them.
var (
	_ BatchScoreProvider = (*scoringstore.Store)(nil)
	_ BatchEdgeCounter   = (*graphstore.Store)(nil)
)

func TestGenerateSQLiteDeterministicBatchedOutput(t *testing.T) {
	ctx := context.Background()
	obs := buildDNAFixture(500)

	build := func(seedOrder []*domain.Observation) string {
		db := newDNATestDB(t)
		// IDs 6 and 11 are decision-type rows whose score rows are removed,
		// so they must render with the 0.5 default at the top of the section.
		seedDNAFixture(t, db, "dna", seedOrder, []int64{6, 11})
		svc := NewService(
			sqlitestore.NewStore(db),
			scoringstore.NewStore(db),
			graphstore.NewStore(db),
		)
		out, err := svc.Generate(ctx, "dna")
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		return out
	}

	forward := build(obs)
	reversed := build(reverseObservations(obs))

	if forward == "" || !strings.Contains(forward, "500 observations") {
		t.Fatalf("expected the full fixture summary, got:\n%s", forward)
	}
	if forward != reversed {
		t.Fatalf("reversed insertion must yield byte-identical markdown under the total order")
	}
	// Missing score rows surface first (0.5 beats the 0.0 trigger default).
	if !strings.Contains(forward, "(score: 0.5)") {
		t.Errorf("observations without score rows must default to 0.5, output:\n%s", forward)
	}
	if !strings.Contains(forward, "**Title-011** (score: 0.5)") {
		t.Errorf("decision 11 must render the 0.5 default, output:\n%s", forward)
	}
}

// legacyOnlyScoring hides the batch capability so the service exercises the
// historical per-ID path against the real store.
type legacyOnlyScoring struct{ inner *scoringstore.Store }

func (l legacyOnlyScoring) GetScore(ctx context.Context, obsID int64) (*domain.ImportanceScore, error) {
	return l.inner.GetScore(ctx, obsID)
}

// legacyOnlyEdges hides the batch capability so the service exercises the
// historical per-ID path against the real store.
type legacyOnlyEdges struct{ inner *graphstore.Store }

func (l legacyOnlyEdges) CountEdgesByObservation(ctx context.Context, obsID int64) (int, error) {
	return l.inner.CountEdgesByObservation(ctx, obsID)
}

func BenchmarkProjectDNABatch(b *testing.B) {
	db := newDNATestDB(b)
	seedDNAFixture(b, db, "dna", buildDNAFixture(500), nil)
	svc := NewService(
		sqlitestore.NewStore(db),
		scoringstore.NewStore(db),
		graphstore.NewStore(db),
	)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.Generate(ctx, "dna"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkProjectDNALegacy(b *testing.B) {
	db := newDNATestDB(b)
	seedDNAFixture(b, db, "dna", buildDNAFixture(500), nil)
	svc := NewService(
		sqlitestore.NewStore(db),
		legacyOnlyScoring{inner: scoringstore.NewStore(db)},
		legacyOnlyEdges{inner: graphstore.NewStore(db)},
	)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.Generate(ctx, "dna"); err != nil {
			b.Fatal(err)
		}
	}
}

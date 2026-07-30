package tui

// store_extra_test.go — behavioral coverage for the async data-loading commands and
// the surviving delete/unarchive command paths declared in store.go.
//
// Each command is exercised through two contracts:
//   1. Nil-safety: a nil Deps or nil store yields a typed error message (no panic).
//   2. Real behavior: a migrated in-memory SQLite store yields the expected typed
//      message with observable content (counts, returned IDs, persisted rows).
//
// Tests use fresh migrated stores per test (REQ-TEST-004 isolation) and assert
// observable contracts rather than execution-only coverage (REQ-TEST-001).

import (
	"context"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
	graphstore "github.com/lleontor705/cortex/internal/store/graph"
	"github.com/lleontor705/cortex/internal/store/search"
	"github.com/lleontor705/cortex/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
	"github.com/lleontor705/cortex/testutil"

	tea "github.com/charmbracelet/bubbletea"
)

// tuiInitSQL builds the full schema required by the wired stores: sessions,
// observations, edges, importance_scores, plus the FTS5 external-content tables
// and their synchronizing triggers (mirrors the production migration surface so
// the search store can run real FTS5 queries).
func tuiInitSQL() string {
	return `
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

CREATE INDEX IF NOT EXISTS idx_obs_session ON observations(session_id);
CREATE INDEX IF NOT EXISTS idx_obs_type    ON observations(type);
CREATE INDEX IF NOT EXISTS idx_obs_project ON observations(project);
CREATE INDEX IF NOT EXISTS idx_obs_created ON observations(created_at DESC);

CREATE TABLE IF NOT EXISTS importance_scores (
	observation_id INTEGER PRIMARY KEY,
	score REAL NOT NULL DEFAULT 0.0,
	access_count INTEGER NOT NULL DEFAULT 0,
	last_accessed DATETIME,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (observation_id) REFERENCES observations(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS edges (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	from_obs_id INTEGER NOT NULL,
	to_obs_id INTEGER NOT NULL,
	relation_type TEXT NOT NULL,
	weight REAL NOT NULL DEFAULT 1.0,
	confidence REAL NOT NULL DEFAULT 1.0,
	source TEXT, reasoning TEXT, valid_from TEXT, invalid_at TEXT,
	evolution_id INTEGER, evolution_type TEXT NOT NULL DEFAULT 'original',
	fact_state TEXT NOT NULL DEFAULT 'current', change_reason TEXT,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (from_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
	FOREIGN KEY (to_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
	UNIQUE(from_obs_id, to_obs_id, relation_type)
);

CREATE VIRTUAL TABLE IF NOT EXISTS observations_fts USING fts5(
	title, content, tool_name, type, project, scope, topic_key,
	content='observations', content_rowid='id', tokenize='porter unicode61'
);

CREATE TRIGGER IF NOT EXISTS obs_fts_insert AFTER INSERT ON observations BEGIN
	INSERT INTO observations_fts(rowid, title, content, tool_name, type, project, scope, topic_key)
	VALUES (new.id, new.title, new.content, new.tool_name, new.type, new.project, new.scope, new.topic_key);
END;

CREATE TRIGGER IF NOT EXISTS obs_fts_delete AFTER DELETE ON observations BEGIN
	INSERT INTO observations_fts(observations_fts, rowid, title, content, tool_name, type, project, scope, topic_key)
	VALUES ('delete', old.id, old.title, old.content, old.tool_name, old.type, old.project, old.scope, old.topic_key);
END;

CREATE TRIGGER IF NOT EXISTS obs_fts_update AFTER UPDATE ON observations BEGIN
	INSERT INTO observations_fts(observations_fts, rowid, title, content, tool_name, type, project, scope, topic_key)
	VALUES ('delete', old.id, old.title, old.content, old.tool_name, old.type, old.project, old.scope, old.topic_key);
	INSERT INTO observations_fts(rowid, title, content, tool_name, type, project, scope, topic_key)
	VALUES (new.id, new.title, new.content, new.tool_name, new.type, new.project, new.scope, new.topic_key);
END;
`
}

// newTUIStoreDeps builds an isolated migrated in-memory SQLite database and wires
// the observation, session, search, and graph stores into a Deps value. Scoring,
// entity, app, and config dependencies are intentionally left nil so that the
// nil-enrichment branches of the detail/health commands are also exercised.
func newTUIStoreDeps(t *testing.T) *Deps {
	t.Helper()

	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1,
		Name:    "tui_test_init",
		UpSQL:   tuiInitSQL(),
		DownSQL: `
DROP TRIGGER IF EXISTS obs_fts_update;
DROP TRIGGER IF EXISTS obs_fts_delete;
DROP TRIGGER IF EXISTS obs_fts_insert;
DROP TABLE IF EXISTS observations_fts;
DROP TABLE IF EXISTS edges;
DROP TABLE IF EXISTS importance_scores;
DROP INDEX IF EXISTS idx_obs_created;
DROP INDEX IF EXISTS idx_obs_project;
DROP INDEX IF EXISTS idx_obs_type;
DROP INDEX IF EXISTS idx_obs_session;
DROP TABLE IF EXISTS observations;
DROP TABLE IF EXISTS sessions;
`,
	})

	testDB := testutil.NewTestDBWithMigrations(t, registry)
	db := testDB.DB()

	return &Deps{
		Observations: sqlitestore.NewStore(db),
		Sessions:     session.NewStore(db),
		Search:       search.NewStore(db),
		Graph:        graphstore.NewStore(db),
		// Scoring, Entities, App, Config intentionally nil.
	}
}

// seedSession inserts a session and returns its id. It fails the test on error.
func seedSession(t *testing.T, deps *Deps, id, project string) {
	t.Helper()
	ctx := context.Background()
	if err := deps.Sessions.Create(ctx, &domain.Session{
		ID:        id,
		Project:   project,
		Directory: "/test/dir",
		StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seedSession: %v", err)
	}
}

// seedObservation saves an observation and returns its assigned id with a
// behaviorally observable reload so callers can assert persisted fields.
func seedObservation(t *testing.T, deps *Deps, sessionID, project, obsType, title, content string) int64 {
	t.Helper()
	ctx := context.Background()
	obs := &domain.Observation{
		SessionID: sessionID,
		Project:   project,
		Type:      obsType,
		Title:     title,
		Content:   content,
	}
	if err := deps.Observations.Save(ctx, obs); err != nil {
		t.Fatalf("seedObservation: %v", err)
	}
	if obs.ID == 0 {
		t.Fatal("seedObservation: expected non-zero id after save")
	}
	return obs.ID
}

// assertMsgType runs cmd and type-asserts the returned message, failing otherwise.
func assertMsgType[T any](t *testing.T, cmd tea.Cmd, label string) T {
	t.Helper()
	if cmd == nil {
		t.Fatalf("%s: expected non-nil command", label)
	}
	msg := cmd()
	result, ok := msg.(T)
	if !ok {
		t.Fatalf("%s: message type = %T, want %T", label, msg, result)
	}
	return result
}

// ─── loadStats ───────────────────────────────────────────────────────────────

func TestLoadStatsNilDepsReturnsError(t *testing.T) {
	loaded := assertMsgType[statsLoadedMsg](t, loadStats(nil), "loadStats(nil)")
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
	if loaded.stats != nil {
		t.Fatal("stats must be nil on error")
	}
}

func TestLoadStatsNilObservationsReturnsError(t *testing.T) {
	loaded := assertMsgType[statsLoadedMsg](t, loadStats(&Deps{}), "loadStats(empty deps)")
	if loaded.err == nil {
		t.Fatal("expected error with nil observations store")
	}
}

func TestLoadStatsRealStoreReportsCounts(t *testing.T) {
	deps := newTUIStoreDeps(t)
	seedSession(t, deps, "s1", "cortex")
	seedObservation(t, deps, "s1", "cortex", domain.TypeDecision, "Decide A", "content a")
	seedObservation(t, deps, "s1", "cortex", domain.TypeBugfix, "Fix B", "content b")

	loaded := assertMsgType[statsLoadedMsg](t, loadStats(deps), "loadStats(real)")
	if loaded.err != nil {
		t.Fatalf("unexpected error: %v", loaded.err)
	}
	if loaded.stats == nil {
		t.Fatal("expected non-nil stats")
	}
	if loaded.stats.TotalObservations != 2 {
		t.Fatalf("TotalObservations = %d, want 2", loaded.stats.TotalObservations)
	}
	if loaded.stats.TotalSessions != 1 {
		t.Fatalf("TotalSessions = %d, want 1", loaded.stats.TotalSessions)
	}
	// Graph store present but empty → zero edges.
	if loaded.stats.TotalEdges != 0 {
		t.Fatalf("TotalEdges = %d, want 0", loaded.stats.TotalEdges)
	}
	if len(loaded.stats.Projects) != 1 || loaded.stats.Projects[0] != "cortex" {
		t.Fatalf("Projects = %v, want [cortex]", loaded.stats.Projects)
	}
}

// ─── loadActivityData ────────────────────────────────────────────────────────

func TestLoadActivityDataNilDepsReturnsEmptySlice(t *testing.T) {
	msg := assertMsgType[activityDataMsg](t, loadActivityData(nil), "loadActivityData(nil)")
	if len(msg.daily) != 7 {
		t.Fatalf("daily length = %d, want 7", len(msg.daily))
	}
	for i, v := range msg.daily {
		if v != 0 {
			t.Fatalf("daily[%d] = %d, want 0 for nil deps", i, v)
		}
	}
}

func TestLoadActivityDataRealStoreReturnsSevenBuckets(t *testing.T) {
	deps := newTUIStoreDeps(t)
	seedSession(t, deps, "s1", "cortex")
	seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "Today", "created now")

	msg := assertMsgType[activityDataMsg](t, loadActivityData(deps), "loadActivityData(real)")
	if len(msg.daily) != 7 {
		t.Fatalf("daily length = %d, want 7", len(msg.daily))
	}
	total := 0
	for _, v := range msg.daily {
		total += v
	}
	if total < 1 {
		t.Fatalf("expected at least 1 observation across the 7-day window, got total=%d", total)
	}
}

// ─── searchMemories ──────────────────────────────────────────────────────────

func TestSearchMemoriesNilDepsReturnsError(t *testing.T) {
	loaded := assertMsgType[searchResultsMsg](t, searchMemories(nil, "x", ""), "searchMemories(nil)")
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
}

func TestSearchMemoriesNilSearchReturnsError(t *testing.T) {
	loaded := assertMsgType[searchResultsMsg](t, searchMemories(&Deps{}, "x", ""), "searchMemories(empty deps)")
	if loaded.err == nil {
		t.Fatal("expected error with nil search store")
	}
}

func TestSearchMemoriesRealStoreReturnsMatches(t *testing.T) {
	deps := newTUIStoreDeps(t)
	seedSession(t, deps, "s1", "cortex")
	seedObservation(t, deps, "s1", "cortex", domain.TypePattern, "Golang concurrency", "channels and goroutines")
	seedObservation(t, deps, "s1", "cortex", domain.TypeDiscovery, "Unrelated", "nothing about the query term")

	loaded := assertMsgType[searchResultsMsg](t, searchMemories(deps, "golang", ""), "searchMemories(real)")
	if loaded.err != nil {
		t.Fatalf("unexpected error: %v", loaded.err)
	}
	if loaded.query != "golang" {
		t.Fatalf("query = %q, want %q", loaded.query, "golang")
	}
	if len(loaded.results) == 0 {
		t.Fatal("expected at least one search result for 'golang'")
	}
	if loaded.results[0].Title != "Golang concurrency" {
		t.Fatalf("first result title = %q, want %q", loaded.results[0].Title, "Golang concurrency")
	}
}

// ─── loadRecentObservations ──────────────────────────────────────────────────

func TestLoadRecentObservationsNilDepsReturnsError(t *testing.T) {
	loaded := assertMsgType[recentObservationsMsg](t, loadRecentObservations(nil, ""), "loadRecentObservations(nil)")
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
}

func TestLoadRecentObservationsRealStoreReturnsRows(t *testing.T) {
	deps := newTUIStoreDeps(t)
	seedSession(t, deps, "s1", "cortex")
	seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "First", "c1")
	seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "Second", "c2")

	loaded := assertMsgType[recentObservationsMsg](t, loadRecentObservations(deps, ""), "loadRecentObservations(real)")
	if loaded.err != nil {
		t.Fatalf("unexpected error: %v", loaded.err)
	}
	if len(loaded.observations) != 2 {
		t.Fatalf("observations = %d, want 2", len(loaded.observations))
	}
}

func TestLoadRecentObservationsProjectFilterExcludesOthers(t *testing.T) {
	deps := newTUIStoreDeps(t)
	seedSession(t, deps, "s1", "cortex")
	seedSession(t, deps, "s2", "other")
	seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "In", "c1")
	seedObservation(t, deps, "s2", "other", domain.TypeManual, "Out", "c2")

	loaded := assertMsgType[recentObservationsMsg](t, loadRecentObservations(deps, "cortex"), "loadRecentObservations(cortex)")
	if loaded.err != nil {
		t.Fatalf("unexpected error: %v", loaded.err)
	}
	if len(loaded.observations) != 1 {
		t.Fatalf("observations = %d, want 1 for project filter", len(loaded.observations))
	}
	if loaded.observations[0].Project != "cortex" {
		t.Fatalf("project = %q, want cortex", loaded.observations[0].Project)
	}
}

// ─── loadObservationDetail ───────────────────────────────────────────────────

func TestLoadObservationDetailNilDepsReturnsError(t *testing.T) {
	loaded := assertMsgType[observationDetailMsg](t, loadObservationDetail(nil, 1), "loadObservationDetail(nil)")
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
}

func TestLoadObservationDetailMissingReturnsError(t *testing.T) {
	deps := newTUIStoreDeps(t)
	loaded := assertMsgType[observationDetailMsg](t, loadObservationDetail(deps, 9999), "loadObservationDetail(missing)")
	if loaded.err == nil {
		t.Fatal("expected error for missing observation")
	}
	if loaded.observation != nil {
		t.Fatal("observation must be nil on error")
	}
}

func TestLoadObservationDetailRealStoreReturnsObservation(t *testing.T) {
	deps := newTUIStoreDeps(t)
	seedSession(t, deps, "s1", "cortex")
	id := seedObservation(t, deps, "s1", "cortex", domain.TypeDiscovery, "Arc Title", "arc body")

	loaded := assertMsgType[observationDetailMsg](t, loadObservationDetail(deps, id), "loadObservationDetail(real)")
	if loaded.err != nil {
		t.Fatalf("unexpected error: %v", loaded.err)
	}
	if loaded.observation == nil || loaded.observation.ID != id {
		t.Fatalf("observation = %+v, want id %d", loaded.observation, id)
	}
	if loaded.observation.Title != "Arc Title" {
		t.Fatalf("title = %q", loaded.observation.Title)
	}
	// Scoring/Entities/Graph are nil → enrichment fields stay nil.
	if loaded.score != nil || loaded.entities != nil || loaded.edges != nil {
		t.Fatal("enrichment fields must be nil when stores are absent")
	}
}

// ─── loadTimeline ────────────────────────────────────────────────────────────

func TestLoadTimelineNilDepsReturnsError(t *testing.T) {
	loaded := assertMsgType[timelineMsg](t, loadTimeline(nil, 1), "loadTimeline(nil)")
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
}

func TestLoadTimelineRealStoreReturnsFocus(t *testing.T) {
	deps := newTUIStoreDeps(t)
	seedSession(t, deps, "s1", "cortex")
	id := seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "Focus", "focus body")

	loaded := assertMsgType[timelineMsg](t, loadTimeline(deps, id), "loadTimeline(real)")
	if loaded.err != nil {
		t.Fatalf("unexpected error: %v", loaded.err)
	}
	if loaded.focus == nil || loaded.focus.ID != id {
		t.Fatalf("focus = %+v, want id %d", loaded.focus, id)
	}
}

// ─── loadRecentSessions ──────────────────────────────────────────────────────

func TestLoadRecentSessionsNilDepsReturnsError(t *testing.T) {
	loaded := assertMsgType[recentSessionsMsg](t, loadRecentSessions(nil), "loadRecentSessions(nil)")
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
}

func TestLoadRecentSessionsRealStoreReturnsSessions(t *testing.T) {
	deps := newTUIStoreDeps(t)
	seedSession(t, deps, "s1", "cortex")
	seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "Obs", "c1")

	loaded := assertMsgType[recentSessionsMsg](t, loadRecentSessions(deps), "loadRecentSessions(real)")
	if loaded.err != nil {
		t.Fatalf("unexpected error: %v", loaded.err)
	}
	if len(loaded.sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(loaded.sessions))
	}
	if loaded.sessions[0].ObservationCount != 1 {
		t.Fatalf("observation count = %d, want 1", loaded.sessions[0].ObservationCount)
	}
}

// ─── loadSessionObservations ─────────────────────────────────────────────────

func TestLoadSessionObservationsNilDepsReturnsError(t *testing.T) {
	loaded := assertMsgType[sessionObservationsMsg](t, loadSessionObservations(nil, "s1"), "loadSessionObservations(nil)")
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
}

func TestLoadSessionObservationsRealStoreFiltersBySession(t *testing.T) {
	deps := newTUIStoreDeps(t)
	seedSession(t, deps, "s1", "cortex")
	seedSession(t, deps, "s2", "cortex")
	seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "In session", "c1")
	seedObservation(t, deps, "s2", "cortex", domain.TypeManual, "Other session", "c2")

	loaded := assertMsgType[sessionObservationsMsg](t, loadSessionObservations(deps, "s1"), "loadSessionObservations(real)")
	if loaded.err != nil {
		t.Fatalf("unexpected error: %v", loaded.err)
	}
	if len(loaded.observations) != 1 {
		t.Fatalf("observations = %d, want 1", len(loaded.observations))
	}
	if loaded.observations[0].SessionID != "s1" {
		t.Fatalf("session id = %q, want s1", loaded.observations[0].SessionID)
	}
}

// ─── loadGraphRelated ────────────────────────────────────────────────────────

func TestLoadGraphRelatedNilDepsReturnsError(t *testing.T) {
	loaded := assertMsgType[graphLoadedMsg](t, loadGraphRelated(nil, 1), "loadGraphRelated(nil)")
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
}

func TestLoadGraphRelatedRealStoreReturnsEmptyForIsolatedObservation(t *testing.T) {
	deps := newTUIStoreDeps(t)
	seedSession(t, deps, "s1", "cortex")
	id := seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "Solo", "no edges")

	loaded := assertMsgType[graphLoadedMsg](t, loadGraphRelated(deps, id), "loadGraphRelated(real)")
	if loaded.err != nil {
		t.Fatalf("unexpected error: %v", loaded.err)
	}
	if len(loaded.related) != 0 {
		t.Fatalf("related = %d, want 0 for isolated observation", len(loaded.related))
	}
}

// ─── loadArchivedObservations ────────────────────────────────────────────────

func TestLoadArchivedObservationsNilDepsReturnsError(t *testing.T) {
	loaded := assertMsgType[archiveLoadedMsg](t, loadArchivedObservations(nil, ""), "loadArchivedObservations(nil)")
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
}

func TestLoadArchivedObservationsIncludesSoftDeleted(t *testing.T) {
	deps := newTUIStoreDeps(t)
	seedSession(t, deps, "s1", "cortex")
	id := seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "To archive", "c1")

	// Soft-delete via the store directly, then verify the archive loader surfaces it
	// (IncludeArchived includes soft-deleted rows).
	ctx := context.Background()
	if err := deps.Observations.SoftDelete(ctx, id); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	loaded := assertMsgType[archiveLoadedMsg](t, loadArchivedObservations(deps, ""), "loadArchivedObservations(real)")
	if loaded.err != nil {
		t.Fatalf("unexpected error: %v", loaded.err)
	}
	found := false
	for _, o := range loaded.observations {
		if o.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("soft-deleted observation id %d not present in archive load", id)
	}
}

// ─── loadHealthData ──────────────────────────────────────────────────────────

func TestLoadHealthDataNilDepsReturnsError(t *testing.T) {
	loaded := assertMsgType[healthLoadedMsg](t, loadHealthData(nil, ""), "loadHealthData(nil)")
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
}

func TestLoadHealthDataRealStoreReportsCountsAndOrphans(t *testing.T) {
	deps := newTUIStoreDeps(t)
	seedSession(t, deps, "s1", "cortex")
	seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "Orphan obs", "no edges")

	loaded := assertMsgType[healthLoadedMsg](t, loadHealthData(deps, "cortex"), "loadHealthData(real)")
	if loaded.err != nil {
		t.Fatalf("unexpected error: %v", loaded.err)
	}
	if loaded.obsCount != 1 {
		t.Fatalf("obsCount = %d, want 1", loaded.obsCount)
	}
	// The single observation has no edges → it is an orphan.
	if len(loaded.orphans) != 1 {
		t.Fatalf("orphans = %d, want 1", len(loaded.orphans))
	}
	if loaded.orphans[0].Title != "Orphan obs" {
		t.Fatalf("orphan title = %q", loaded.orphans[0].Title)
	}
}

// ─── deleteObservationCmd (surviving command) ────────────────────────────────

func TestDeleteObservationCmdNilDepsReturnsError(t *testing.T) {
	loaded := assertMsgType[deleteObservationMsg](t, deleteObservationCmd(nil, 5), "deleteObservationCmd(nil)")
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
	if loaded.id != 5 {
		t.Fatalf("id = %d, want 5 (echoed even on error)", loaded.id)
	}
}

func TestDeleteObservationCmdSoftDeletesAndPersists(t *testing.T) {
	deps := newTUIStoreDeps(t)
	seedSession(t, deps, "s1", "cortex")
	id := seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "Delete me", "c1")

	loaded := assertMsgType[deleteObservationMsg](t, deleteObservationCmd(deps, id), "deleteObservationCmd(real)")
	if loaded.err != nil {
		t.Fatalf("unexpected error: %v", loaded.err)
	}
	if loaded.id != id {
		t.Fatalf("id = %d, want %d", loaded.id, id)
	}

	// Persisted contract: GetByID must no longer find it (soft-deleted).
	ctx := context.Background()
	if _, err := deps.Observations.GetByID(ctx, id); err == nil {
		t.Fatal("GetByID should fail after soft delete")
	}
}

func TestDeleteObservationCmdMissingReturnsError(t *testing.T) {
	deps := newTUIStoreDeps(t)
	loaded := assertMsgType[deleteObservationMsg](t, deleteObservationCmd(deps, 4242), "deleteObservationCmd(missing)")
	if loaded.err == nil {
		t.Fatal("expected NotFound error for missing observation")
	}
}

// ─── unarchiveObservationCmd (surviving command) ─────────────────────────────

func TestUnarchiveObservationCmdNilDepsReturnsError(t *testing.T) {
	loaded := assertMsgType[unarchiveObservationMsg](t, unarchiveObservationCmd(nil, 7), "unarchiveObservationCmd(nil)")
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
	if loaded.id != 7 {
		t.Fatalf("id = %d, want 7 (echoed even on error)", loaded.id)
	}
}

func TestUnarchiveObservationCmdRestoresPersistedState(t *testing.T) {
	deps := newTUIStoreDeps(t)
	seedSession(t, deps, "s1", "cortex")
	id := seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "Archive then restore", "c1")

	ctx := context.Background()
	if err := deps.Observations.SoftDelete(ctx, id); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	// Precondition: the soft-delete must be visible before we unarchive. This
	// guards against transient in-memory connection state by failing with a
	// clear, attributable message rather than a confusing downstream mismatch.
	if _, err := deps.Observations.GetByID(ctx, id); err == nil {
		t.Fatal("precondition failed: observation still reachable after soft delete")
	}

	loaded := assertMsgType[unarchiveObservationMsg](t, unarchiveObservationCmd(deps, id), "unarchiveObservationCmd(real)")
	if loaded.err != nil {
		t.Fatalf("unexpected error: %v", loaded.err)
	}
	if loaded.id != id {
		t.Fatalf("id = %d, want %d", loaded.id, id)
	}

	// Persisted contract: GetByID finds it again after unarchive.
	got, err := deps.Observations.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID after unarchive: %v", err)
	}
	if got.ID != id {
		t.Fatalf("restored id = %d, want %d", got.ID, id)
	}
}

func TestUnarchiveObservationCmdNotArchivedReturnsError(t *testing.T) {
	deps := newTUIStoreDeps(t)
	seedSession(t, deps, "s1", "cortex")
	id := seedObservation(t, deps, "s1", "cortex", domain.TypeManual, "Active", "c1")

	ctx := context.Background()
	// Precondition: confirm the seeded observation exists and is active (not
	// archived) before asserting the unarchive contract. This isolates any
	// transient seeding/state issue from the behavioral oracle under test.
	if _, err := deps.Observations.GetByID(ctx, id); err != nil {
		t.Fatalf("precondition failed: active observation not reachable after seed: %v", err)
	}

	// Not archived → unarchive must report NotFound, leaving the row active.
	loaded := assertMsgType[unarchiveObservationMsg](t, unarchiveObservationCmd(deps, id), "unarchiveObservationCmd(active)")
	if loaded.err == nil {
		t.Fatal("expected NotFound error when observation is not archived")
	}

	if _, err := deps.Observations.GetByID(ctx, id); err != nil {
		t.Fatalf("active observation must still be reachable after failed unarchive: %v", err)
	}
}

// ─── reloadConfigCmd / startReindexCmd (nil-app safety) ─────────────────────

func TestReloadConfigCmdNilAppReturnsError(t *testing.T) {
	loaded := assertMsgType[configReloadedMsg](t, reloadConfigCmd(nil), "reloadConfigCmd(nil)")
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
}

func TestReloadConfigCmdNilAppOnEmptyDepsReturnsError(t *testing.T) {
	loaded := assertMsgType[configReloadedMsg](t, reloadConfigCmd(&Deps{}), "reloadConfigCmd(empty deps)")
	if loaded.err == nil {
		t.Fatal("expected error with nil app")
	}
}

func TestStartReindexCmdNilAppReturnsError(t *testing.T) {
	loaded := assertMsgType[reindexProgressMsg](t, startReindexCmd(nil), "startReindexCmd(nil)")
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
	if !loaded.done {
		t.Fatal("reindex message should be marked done on error")
	}
}

// ─── embedding config commands (nil-config safety) ──────────────────────────

func TestSaveEmbeddingConfigNilConfigReturnsError(t *testing.T) {
	loaded := assertMsgType[configSavedMsg](t, saveEmbeddingConfig(nil, 0, "m", false, false), "saveEmbeddingConfig(nil)")
	if loaded.err == nil {
		t.Fatal("expected error with nil deps")
	}
}

func TestSaveEmbeddingConfigNilConfigOnEmptyDepsReturnsError(t *testing.T) {
	loaded := assertMsgType[configSavedMsg](t, saveEmbeddingConfig(&Deps{}, 1, "m", true, false), "saveEmbeddingConfig(empty deps)")
	if loaded.err == nil {
		t.Fatal("expected error with nil config")
	}
}

// ─── ollama status command (offline / nil-config path) ───────────────────────

func TestCheckOllamaStatusReturnsTypedMessageAndInvariant(t *testing.T) {
	// checkOllamaStatus tolerates nil deps/config and probes the manager; the
	// observable contract is a typed ollamaStatusMsg (no panic) plus the
	// invariant that hasModel can only be true when running is true (you cannot
	// have a model on a server that is not running). This is environment-
	// independent: it holds whether or not Ollama is present in CI.
	msg := assertMsgType[ollamaStatusMsg](t, checkOllamaStatus(nil), "checkOllamaStatus(nil)")
	if !msg.running && msg.hasModel {
		t.Fatal("invariant violated: hasModel=true while running=false")
	}
}

// ─── checkForUpdate ──────────────────────────────────────────────────────────

func TestCheckForUpdateDevBuildSkipsCheck(t *testing.T) {
	// "dev" builds deterministically short-circuit update.Check (no network),
	// so the command must return a typed message with a nil result.
	msg := assertMsgType[updateCheckMsg](t, checkForUpdate("dev"), "checkForUpdate(dev)")
	if msg.result != nil {
		t.Fatalf("dev build should skip update check, got result = %+v", msg.result)
	}
}

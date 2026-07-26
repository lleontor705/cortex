package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
	"github.com/lleontor705/cortex/testutil"
)

// fullSchemaUp is the SQL for a complete schema covering every Store method
// exercised here: sessions, observations, temporal_snapshots, edges,
// importance_scores, sync_chunks, search_feedback, and user_prompts.
const fullSchemaUp = `
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

	CREATE INDEX IF NOT EXISTS idx_obs_project ON observations(project);

	CREATE TABLE IF NOT EXISTS temporal_snapshots (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		snapshot_key TEXT NOT NULL,
		timestamp DATETIME NOT NULL,
		description TEXT,
		observation_count INTEGER NOT NULL DEFAULT 0,
		edge_count INTEGER NOT NULL DEFAULT 0,
		root_observation_id INTEGER,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(snapshot_key, timestamp)
	);

	CREATE TABLE IF NOT EXISTS edges (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		from_obs_id INTEGER NOT NULL,
		to_obs_id INTEGER NOT NULL,
		relation_type TEXT NOT NULL,
		weight REAL NOT NULL DEFAULT 1.0,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(from_obs_id, to_obs_id, relation_type)
	);

	CREATE TABLE IF NOT EXISTS importance_scores (
		observation_id INTEGER PRIMARY KEY,
		score REAL NOT NULL DEFAULT 0.0,
		access_count INTEGER NOT NULL DEFAULT 0,
		last_accessed DATETIME,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS sync_chunks (
		chunk_id    TEXT PRIMARY KEY,
		imported_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS search_feedback (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		query TEXT NOT NULL,
		observation_id INTEGER NOT NULL,
		rank_position INTEGER NOT NULL DEFAULT 0,
		clicked_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS user_prompts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT,
		content TEXT,
		project TEXT,
		created_at TEXT
	);
`

const fullSchemaDown = `
	DROP TABLE IF EXISTS user_prompts;
	DROP TABLE IF EXISTS search_feedback;
	DROP TABLE IF EXISTS sync_chunks;
	DROP TABLE IF EXISTS importance_scores;
	DROP TABLE IF EXISTS edges;
	DROP TABLE IF EXISTS temporal_snapshots;
	DROP INDEX IF EXISTS idx_obs_project;
	DROP TABLE IF EXISTS observations;
	DROP TABLE IF EXISTS sessions;
`

// setupFullTestStore creates a Store backed by a fresh migrated in-memory DB
// with the full schema (sessions, observations, edges, importance_scores,
// sync_chunks, search_feedback, user_prompts, temporal_snapshots).
func setupFullTestStore(t *testing.T) (*Store, *sql.DB, func()) {
	t.Helper()
	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1,
		Name:    "full_schema",
		UpSQL:   fullSchemaUp,
		DownSQL: fullSchemaDown,
	})
	testDB := testutil.NewTestDBWithMigrations(t, registry)
	store := NewStore(testDB.DB())
	return store, testDB.DB(), func() { testDB.Cleanup() }
}

// setObsCreatedAt forces a fixed created_at on an observation (for deterministic
// archivable/stale filtering) without altering production behavior.
func setObsCreatedAt(t *testing.T, db *sql.DB, id int64, created string) {
	t.Helper()
	if _, err := db.Exec("UPDATE observations SET created_at = ? WHERE id = ?", created, id); err != nil {
		t.Fatalf("set obs created_at: %v", err)
	}
}

// seedImportance inserts a controlled importance_scores row for deterministic
// archivable/stale behavior.
func seedImportance(t *testing.T, db *sql.DB, obsID int64, score float64, lastAccessed string) {
	t.Helper()
	if _, err := db.Exec(
		"INSERT INTO importance_scores(observation_id, score, access_count, last_accessed, updated_at) VALUES(?,?,1,?,datetime('now'))",
		obsID, score, lastAccessed,
	); err != nil {
		t.Fatalf("seed importance: %v", err)
	}
}

func insertEdge(t *testing.T, db *sql.DB, fromID, toID int64, relation string) {
	t.Helper()
	if _, err := db.Exec(
		"INSERT INTO edges(from_obs_id, to_obs_id, relation_type) VALUES(?,?,?)",
		fromID, toID, relation,
	); err != nil {
		t.Fatalf("insert edge: %v", err)
	}
}

// directInsertObs inserts an observation directly (bypassing Save's topic_key
// upsert) so multiple rows can share a topic_key. topicKey "" stores NULL.
func directInsertObs(t *testing.T, db *sql.DB, sessionID, title, content, project, topicKey string) int64 {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO observations (session_id, type, title, content, project, scope, topic_key,
			normalized_hash, revision_count, duplicate_count, confidence, source, tags, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'project', ?, ?, 1, 1, 1.0, 'manual', NULL, datetime('now'), datetime('now'))
	`, sessionID, domain.TypeManual, title, content, project, nullableString(topicKey), hashNormalized(content))
	if err != nil {
		t.Fatalf("direct insert obs: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// --- Restore / Unarchive ------------------------------------------------------

func TestStore_Restore_Unarchive_RoundTrip(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	obs := &domain.Observation{SessionID: "s1", Title: "T", Content: "C", Project: "proj"}
	if err := store.Save(ctx, obs); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Soft-delete then restore via Restore (alias for Unarchive).
	if err := store.SoftDelete(ctx, obs.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := store.GetByID(ctx, obs.ID); err == nil {
		t.Fatal("expected NotFound after soft delete")
	}
	if err := store.Restore(ctx, obs.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, err := store.GetByID(ctx, obs.ID)
	if err != nil {
		t.Fatalf("GetByID after restore: %v", err)
	}
	if got.Title != "T" {
		t.Errorf("restored Title = %q, want %q", got.Title, "T")
	}

	// deleted_at must be cleared.
	var deletedAt sql.NullString
	if err := db.QueryRow("SELECT deleted_at FROM observations WHERE id = ?", obs.ID).Scan(&deletedAt); err != nil {
		t.Fatalf("query deleted_at: %v", err)
	}
	if deletedAt.Valid {
		t.Errorf("deleted_at should be NULL after restore, got %q", deletedAt.String)
	}
}

func TestStore_Unarchive_NotArchivedIsNotFound(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	obs := &domain.Observation{SessionID: "s1", Title: "T", Content: "C"}
	if err := store.Save(ctx, obs); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Unarchiving a non-deleted observation is a no-op -> NotFound.
	if err := store.Unarchive(ctx, obs.ID); err == nil {
		t.Fatal("Unarchive on non-archived expected error, got nil")
	}
	if !domain.IsNotFoundError(store.Unarchive(ctx, obs.ID)) {
		t.Error("Unarchive on non-archived expected NotFoundError")
	}
	// Also for a truly missing id.
	if !domain.IsNotFoundError(store.Unarchive(ctx, 99999)) {
		t.Error("Unarchive on missing id expected NotFoundError")
	}
}

// --- ListArchivable -----------------------------------------------------------

func TestStore_ListArchivable_FiltersByScoreAndCutoff(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	// Insert directly so both share a non-empty topic_key (ListArchivable scans
	// topic_key into a non-nullable string; NULL would fail the scan).
	lowID := directInsertObs(t, db, "s1", "low", "c1", "proj", "arch/low")
	highID := directInsertObs(t, db, "s1", "high", "c2", "proj", "arch/high")

	// Make both old enough to be eligible by cutoff.
	setObsCreatedAt(t, db, lowID, "2020-01-01 00:00:00")
	setObsCreatedAt(t, db, highID, "2020-01-01 00:00:00")
	seedImportance(t, db, lowID, 0.1, "2020-01-01 00:00:00")  // below minScore -> archivable
	seedImportance(t, db, highID, 0.9, "2020-01-01 00:00:00") // at/above minScore -> excluded

	cutoff := mustParseTime(t, "2024-01-01T00:00:00Z")
	got, err := store.ListArchivable(ctx, cutoff, 0.5, 10)
	if err != nil {
		t.Fatalf("ListArchivable: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 archivable, got %d", len(got))
	}
	if got[0].ID != lowID {
		t.Errorf("archivable ID = %d, want %d", got[0].ID, lowID)
	}
}

func TestStore_ListArchivable_DefaultLimit(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	// Create more than the default limit (500) to confirm the default applies.
	// Direct inserts avoid Save's topic_key upsert and keep topic_key non-NULL.
	for i := 0; i < 510; i++ {
		id := directInsertObs(t, db, "s1", "t", "c", "proj", "arch/bulk")
		setObsCreatedAt(t, db, id, "2020-01-01 00:00:00")
	}
	// No importance score -> s.score IS NULL -> archivable.
	cutoff := mustParseTime(t, "2024-01-01T00:00:00Z")
	got, err := store.ListArchivable(ctx, cutoff, 0.5, 0) // limit<=0 -> default 500
	if err != nil {
		t.Fatalf("ListArchivable default limit: %v", err)
	}
	if len(got) != 500 {
		t.Errorf("expected default limit 500, got %d", len(got))
	}
}

// --- ListByTopicKey -----------------------------------------------------------

func TestStore_ListByTopicKey(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	// Direct inserts: Save would upsert duplicate (project, topic_key) into one row.
	directInsertObs(t, db, "s1", "a", "ca", "proj", "arch/auth")
	directInsertObs(t, db, "s1", "b", "cb", "proj", "arch/auth")
	directInsertObs(t, db, "s1", "o", "co", "proj", "other/key")

	got, err := store.ListByTopicKey(ctx, "proj", "arch/auth")
	if err != nil {
		t.Fatalf("ListByTopicKey: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	for _, o := range got {
		if o.TopicKey != "arch/auth" {
			t.Errorf("topic_key = %q, want %q", o.TopicKey, "arch/auth")
		}
	}
}

// --- FindConsolidationCandidates ---------------------------------------------

func TestStore_FindConsolidationCandidates(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	// Three observations share a topic key, one has a different key, two have none.
	// Direct inserts avoid the topic_key upsert that Save performs.
	for _, title := range []string{"d1", "d2", "d3"} {
		directInsertObs(t, db, "s1", title, "c", "proj", "shared/x")
	}
	directInsertObs(t, db, "s1", "s", "c", "proj", "lonely/y")
	directInsertObs(t, db, "s1", "n1", "c", "proj", "")
	directInsertObs(t, db, "s1", "n2", "c", "proj", "")

	groups, err := store.FindConsolidationCandidates(ctx, "proj", 2)
	if err != nil {
		t.Fatalf("FindConsolidationCandidates: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].TopicKey != "shared/x" {
		t.Errorf("group topic = %q, want %q", groups[0].TopicKey, "shared/x")
	}
	if groups[0].Count != 3 {
		t.Errorf("group count = %d, want 3", groups[0].Count)
	}

	// Default minCount (<=0 -> 2) keeps the same result.
	groups, err = store.FindConsolidationCandidates(ctx, "proj", 0)
	if err != nil {
		t.Fatalf("FindConsolidationCandidates default: %v", err)
	}
	if len(groups) != 1 {
		t.Errorf("default minCount expected 1 group, got %d", len(groups))
	}
}

// --- StaleObservations --------------------------------------------------------

func TestStore_StaleObservations(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	stale := &domain.Observation{SessionID: "s1", Title: "stale", Content: "c", Project: "proj"}
	fresh := &domain.Observation{SessionID: "s1", Title: "fresh", Content: "c", Project: "proj"}
	if err := store.Save(ctx, stale); err != nil {
		t.Fatalf("Save stale: %v", err)
	}
	if err := store.Save(ctx, fresh); err != nil {
		t.Fatalf("Save fresh: %v", err)
	}

	seedImportance(t, db, stale.ID, 0.9, "2020-01-01 00:00:00") // old access -> stale
	seedImportance(t, db, fresh.ID, 0.9, time.Now().UTC().Format("2006-01-02 15:04:05"))

	got, err := store.StaleObservations(ctx, "proj", 0.5, 5)
	if err != nil {
		t.Fatalf("StaleObservations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 stale, got %d", len(got))
	}
	if got[0].ID != stale.ID {
		t.Errorf("stale ID = %d, want %d", got[0].ID, stale.ID)
	}
}

// --- OrphanObservations -------------------------------------------------------

func TestStore_OrphanObservations(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	connected1 := &domain.Observation{SessionID: "s1", Title: "c1", Content: "c", Project: "proj"}
	connected2 := &domain.Observation{SessionID: "s1", Title: "c2", Content: "c", Project: "proj"}
	orphan := &domain.Observation{SessionID: "s1", Title: "orphan", Content: "c", Project: "proj"}
	for _, o := range []*domain.Observation{connected1, connected2, orphan} {
		if err := store.Save(ctx, o); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	// Link the two connected observations; leave orphan with no edges.
	insertEdge(t, db, connected1.ID, connected2.ID, "references")

	got, err := store.OrphanObservations(ctx, "proj", 10)
	if err != nil {
		t.Fatalf("OrphanObservations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(got))
	}
	if got[0].ID != orphan.ID {
		t.Errorf("orphan ID = %d, want %d", got[0].ID, orphan.ID)
	}
}

// --- CountAll / CountByRoot ---------------------------------------------------

func TestStore_CountAll(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	for i := 0; i < 4; i++ {
		o := &domain.Observation{SessionID: "s1", Title: "t", Content: "c", Project: "proj"}
		if err := store.Save(ctx, o); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	// Soft-delete one -> excluded from count.
	toDelete := &domain.Observation{SessionID: "s1", Title: "del", Content: "c", Project: "proj"}
	if err := store.Save(ctx, toDelete); err != nil {
		t.Fatalf("Save del: %v", err)
	}
	if err := store.SoftDelete(ctx, toDelete.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	count, err := store.CountAll(ctx)
	if err != nil {
		t.Fatalf("CountAll: %v", err)
	}
	if count != 4 {
		t.Errorf("CountAll = %d, want 4 (excludes soft-deleted)", count)
	}
}

func TestStore_CountByRoot(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	o1 := &domain.Observation{SessionID: "s1", Title: "1", Content: "c", Project: "proj"}
	o2 := &domain.Observation{SessionID: "s1", Title: "2", Content: "c", Project: "proj"}
	o3 := &domain.Observation{SessionID: "s1", Title: "3", Content: "c", Project: "proj"}
	for _, o := range []*domain.Observation{o1, o2, o3} {
		if err := store.Save(ctx, o); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	// Chain: 1 -> 2 -> 3 ; all reachable from root 1.
	insertEdge(t, db, o1.ID, o2.ID, "references")
	insertEdge(t, db, o2.ID, o3.ID, "references")

	count, err := store.CountByRoot(ctx, o1.ID)
	if err != nil {
		t.Fatalf("CountByRoot: %v", err)
	}
	if count != 3 {
		t.Errorf("CountByRoot = %d, want 3", count)
	}
}

// --- GetBySource / GetByType --------------------------------------------------

func TestStore_GetBySource(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	for _, src := range []string{domain.SourceAI, domain.SourceManual} {
		o := &domain.Observation{SessionID: "s1", Title: "t", Content: "c", Project: "proj", Source: src}
		if err := store.Save(ctx, o); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	got, err := store.GetBySource(ctx, "AI", 10) // normalization lowercases
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].Source != domain.SourceAI {
		t.Errorf("Source = %q, want %q", got[0].Source, domain.SourceAI)
	}
}

func TestStore_GetByType(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	o1 := &domain.Observation{SessionID: "s1", Title: "t", Content: "c", Project: "proj", Type: domain.TypeDecision}
	o2 := &domain.Observation{SessionID: "s1", Title: "t", Content: "c", Project: "proj", Type: domain.TypeBugfix}
	for _, o := range []*domain.Observation{o1, o2} {
		if err := store.Save(ctx, o); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	got, err := store.GetByType(ctx, "DECISION", 10) // normalization lowercases
	if err != nil {
		t.Fatalf("GetByType: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].Type != domain.TypeDecision {
		t.Errorf("Type = %q, want %q", got[0].Type, domain.TypeDecision)
	}
}

// --- Search feedback ----------------------------------------------------------

func TestStore_SearchFeedback_Stats(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")
	obs := &domain.Observation{SessionID: "s1", Title: "t", Content: "c", Project: "proj"}
	if err := store.Save(ctx, obs); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := store.RecordSearchFeedback(ctx, "go", obs.ID, 1); err != nil {
		t.Fatalf("RecordSearchFeedback: %v", err)
	}
	if err := store.RecordSearchFeedback(ctx, "go", obs.ID, 2); err != nil {
		t.Fatalf("RecordSearchFeedback: %v", err)
	}
	if err := store.RecordSearchFeedback(ctx, "rust", obs.ID, 1); err != nil {
		t.Fatalf("RecordSearchFeedback: %v", err)
	}

	total, unique, err := store.GetSearchFeedbackStats(ctx)
	if err != nil {
		t.Fatalf("GetSearchFeedbackStats: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if unique != 2 {
		t.Errorf("unique = %d, want 2", unique)
	}
}

// --- Sync chunks --------------------------------------------------------------

func TestStore_SyncedChunks_Idempotent(t *testing.T) {
	store, _, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()

	for _, id := range []string{"chunk-1", "chunk-1", "chunk-2"} {
		if err := store.RecordSyncedChunk(ctx, id); err != nil {
			t.Fatalf("RecordSyncedChunk(%q): %v", id, err)
		}
	}

	chunks, err := store.GetSyncedChunks(ctx)
	if err != nil {
		t.Fatalf("GetSyncedChunks: %v", err)
	}
	if len(chunks) != 2 {
		t.Errorf("expected 2 unique chunks, got %d", len(chunks))
	}
	if !chunks["chunk-1"] || !chunks["chunk-2"] {
		t.Errorf("expected chunk-1 and chunk-2, got %v", chunks)
	}
}

// --- Export / Import ----------------------------------------------------------

func TestStore_ExportImport_RoundTrip(t *testing.T) {
	src, srcDB, srcCleanup := setupFullTestStore(t)
	ctx := context.Background()
	createTestSession(t, srcDB, "sess-export", "proj")
	obs := &domain.Observation{SessionID: "sess-export", Title: "exported", Content: "body", Project: "proj"}
	if err := src.Save(ctx, obs); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := srcDB.Exec(
		"INSERT INTO user_prompts(session_id, content, project, created_at) VALUES(?,?,?,?)",
		"sess-export", "prompt body", "proj", mustParseTime(t, "2024-01-01T00:00:00Z").Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert prompt: %v", err)
	}

	data, err := src.ExportAll(ctx)
	srcCleanup()
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	if len(data.Observations) != 1 {
		t.Fatalf("exported observations = %d, want 1", len(data.Observations))
	}
	if data.Observations[0].Title != "exported" {
		t.Errorf("exported obs title = %q, want %q", data.Observations[0].Title, "exported")
	}
	if len(data.Sessions) != 1 || data.Sessions[0].ID != "sess-export" {
		t.Errorf("exported sessions = %+v", data.Sessions)
	}
	if len(data.Prompts) != 1 || data.Prompts[0].Content != "prompt body" {
		t.Errorf("exported prompts = %+v", data.Prompts)
	}

	// Import into a fresh DB.
	dst, dstDB, dstCleanup := setupFullTestStore(t)
	defer dstCleanup()

	res, err := dst.ImportData(ctx, data)
	if err != nil {
		t.Fatalf("ImportData: %v", err)
	}
	if res.SessionsImported != 1 || res.ObservationsImported != 1 || res.PromptsImported != 1 {
		t.Errorf("import result = %+v, want 1/1/1", res)
	}
	// Verify the imported observation is queryable.
	listed, err := dst.List(ctx, domain.ObservationFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List after import: %v", err)
	}
	if len(listed) != 1 || listed[0].Title != "exported" {
		t.Errorf("imported listed = %+v", listed)
	}

	// Re-importing skips the duplicate session (INSERT OR IGNORE) but re-inserts
	// observations/prompts with new IDs.
	res2, err := dst.ImportData(ctx, data)
	if err != nil {
		t.Fatalf("ImportData dup: %v", err)
	}
	if res2.SessionsImported != 0 {
		t.Errorf("re-import sessions = %d, want 0 (skipped)", res2.SessionsImported)
	}
	// Verify only one session row exists in dst.
	var sessionCount int
	if err := dstDB.QueryRow("SELECT COUNT(*) FROM sessions WHERE id = ?", "sess-export").Scan(&sessionCount); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessionCount != 1 {
		t.Errorf("session rows = %d, want 1", sessionCount)
	}
}

// --- MergeProjects ------------------------------------------------------------

func TestStore_MergeProjects(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "myapp2")
	createTestSession(t, db, "s2", "myapp")

	// Observations in source projects.
	for _, proj := range []string{"myapp2", "myapp2", "myapp"} {
		o := &domain.Observation{SessionID: "s1", Title: "t", Content: "c", Project: proj}
		if err := store.Save(ctx, o); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	res, err := store.MergeProjects(ctx, []string{"MyApp2", "myapp-x"}, "myapp")
	if err != nil {
		t.Fatalf("MergeProjects: %v", err)
	}
	if res.Canonical != "myapp" {
		t.Errorf("Canonical = %q, want %q", res.Canonical, "myapp")
	}
	if res.ObservationsUpdated != 2 {
		t.Errorf("ObservationsUpdated = %d, want 2", res.ObservationsUpdated)
	}
	// Both non-canonical sources are recorded as merged even when one matched
	// zero rows; only sources equal to canonical are skipped.
	if len(res.SourcesMerged) != 2 {
		t.Fatalf("SourcesMerged = %+v, want 2 entries", res.SourcesMerged)
	}
	merged := map[string]bool{res.SourcesMerged[0]: true, res.SourcesMerged[1]: true}
	if !merged["myapp2"] || !merged["myapp-x"] {
		t.Errorf("SourcesMerged = %+v, want myapp2 and myapp-x", res.SourcesMerged)
	}

	// All observations now belong to canonical project.
	got, err := store.GetBySource(ctx, "manual", 50)
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	for _, o := range got {
		if o.Project != "myapp" {
			t.Errorf("project = %q, want %q", o.Project, "myapp")
		}
	}

	// Empty canonical is a validation error.
	if _, err := store.MergeProjects(ctx, []string{"x"}, ""); err == nil {
		t.Fatal("MergeProjects with empty canonical expected error")
	} else if !domain.IsValidationError(err) {
		t.Errorf("MergeProjects empty canonical error = %v, want ValidationError", err)
	}
}

func TestStore_MergeProjects_SkipsCanonicalSelf(t *testing.T) {
	store, _, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// A source equal to canonical is silently skipped.
	res, err := store.MergeProjects(ctx, []string{"myapp", "  "}, "myapp")
	if err != nil {
		t.Fatalf("MergeProjects: %v", err)
	}
	if res.ObservationsUpdated != 0 {
		t.Errorf("ObservationsUpdated = %d, want 0 (self skipped)", res.ObservationsUpdated)
	}
	if len(res.SourcesMerged) != 0 {
		t.Errorf("SourcesMerged = %+v, want empty", res.SourcesMerged)
	}
}

// --- Missing-table fallback (sync_chunks / search_feedback / user_prompts) ----

func TestStore_MissingTableFallback(t *testing.T) {
	// setupTestStore creates observations/sessions/temporal_snapshots only.
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// search_feedback absent -> graceful no-op.
	if err := store.RecordSearchFeedback(ctx, "q", 1, 1); err != nil {
		t.Errorf("RecordSearchFeedback without table: %v", err)
	}
	total, unique, err := store.GetSearchFeedbackStats(ctx)
	if err != nil {
		t.Errorf("GetSearchFeedbackStats without table: %v", err)
	}
	if total != 0 || unique != 0 {
		t.Errorf("stats = %d/%d, want 0/0", total, unique)
	}

	// sync_chunks absent -> graceful no-op and empty result.
	if err := store.RecordSyncedChunk(ctx, "c1"); err != nil {
		t.Errorf("RecordSyncedChunk without table: %v", err)
	}
	chunks, err := store.GetSyncedChunks(ctx)
	if err != nil {
		t.Errorf("GetSyncedChunks without table: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("chunks = %d, want 0", len(chunks))
	}
}

// --- List additional filters --------------------------------------------------

func TestStore_List_FilterByTags(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	tagged := &domain.Observation{SessionID: "s1", Title: "tagged", Content: "c", Project: "proj", Tags: []string{"go", "auth"}}
	other := &domain.Observation{SessionID: "s1", Title: "other", Content: "c", Project: "proj", Tags: []string{"rust"}}
	for _, o := range []*domain.Observation{tagged, other} {
		if err := store.Save(ctx, o); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	got, err := store.List(ctx, domain.ObservationFilter{Tags: []string{"go"}, Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 tagged, got %d", len(got))
	}
	if got[0].Title != "tagged" {
		t.Errorf("title = %q, want %q", got[0].Title, "tagged")
	}
}

func TestStore_List_FilterByMinConfidenceAndSource(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	high := &domain.Observation{SessionID: "s1", Title: "high", Content: "c", Project: "proj", Confidence: 0.9, Source: domain.SourceAI}
	low := &domain.Observation{SessionID: "s1", Title: "low", Content: "c", Project: "proj", Confidence: 0.5, Source: domain.SourceManual}
	for _, o := range []*domain.Observation{high, low} {
		if err := store.Save(ctx, o); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	got, err := store.List(ctx, domain.ObservationFilter{MinConfidence: 0.8, Limit: 10})
	if err != nil {
		t.Fatalf("List min confidence: %v", err)
	}
	if len(got) != 1 || got[0].Title != "high" {
		t.Errorf("min confidence filter = %+v", got)
	}

	got, err = store.List(ctx, domain.ObservationFilter{Source: domain.SourceAI, Limit: 10})
	if err != nil {
		t.Fatalf("List source: %v", err)
	}
	if len(got) != 1 || got[0].Source != domain.SourceAI {
		t.Errorf("source filter = %+v", got)
	}
}

func TestStore_List_IncludeArchivedAndOrder(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	a := &domain.Observation{SessionID: "s1", Title: "a", Content: "c", Project: "proj"}
	b := &domain.Observation{SessionID: "s1", Title: "b", Content: "c", Project: "proj"}
	for _, o := range []*domain.Observation{a, b} {
		if err := store.Save(ctx, o); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	// datetime('now') has second precision, so force distinct timestamps.
	setObsCreatedAt(t, db, a.ID, "2024-01-01 00:00:00")
	setObsCreatedAt(t, db, b.ID, "2024-01-02 00:00:00")
	// Archive 'b'.
	if err := store.SoftDelete(ctx, b.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	// Default excludes archived.
	got, err := store.List(ctx, domain.ObservationFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != a.ID {
		t.Errorf("default list = %+v, want only non-archived 'a'", got)
	}

	// IncludeArchived returns both.
	got, err = store.List(ctx, domain.ObservationFilter{IncludeArchived: true, Limit: 10})
	if err != nil {
		t.Fatalf("List include archived: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("include archived = %d, want 2", len(got))
	}

	// OrderAsc returns ascending created_at (default DESC would be b first).
	got, err = store.List(ctx, domain.ObservationFilter{IncludeArchived: true, OrderAsc: true, Limit: 10})
	if err != nil {
		t.Fatalf("List order asc: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].ID != a.ID || got[1].ID != b.ID {
		t.Errorf("OrderAsc ids = [%d, %d], want [%d, %d]", got[0].ID, got[1].ID, a.ID, b.ID)
	}
}

func TestStore_List_FilterByCreatedRange(t *testing.T) {
	store, db, cleanup := setupFullTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "s1", "proj")

	o := &domain.Observation{SessionID: "s1", Title: "old", Content: "c", Project: "proj"}
	if err := store.Save(ctx, o); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Force a fixed old creation date.
	setObsCreatedAt(t, db, o.ID, "2020-01-01 00:00:00")

	before2022 := mustParseTime(t, "2022-01-01T00:00:00Z")
	after2019 := mustParseTime(t, "2019-01-01T00:00:00Z")

	got, err := store.List(ctx, domain.ObservationFilter{CreatedAfter: &after2019, CreatedBefore: &before2022, Limit: 10})
	if err != nil {
		t.Fatalf("List created range: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 in range, got %d", len(got))
	}
	if got[0].Title != "old" {
		t.Errorf("title = %q, want %q", got[0].Title, "old")
	}

	// A range that excludes the old observation returns none.
	future := mustParseTime(t, "2025-01-01T00:00:00Z")
	got, err = store.List(ctx, domain.ObservationFilter{CreatedAfter: &future, Limit: 10})
	if err != nil {
		t.Fatalf("List created after future: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 after future date, got %d", len(got))
	}
}

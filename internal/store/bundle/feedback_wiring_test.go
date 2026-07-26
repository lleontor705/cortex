package bundle_test

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite" // Pure Go SQLite driver (zero-CGO)

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/store/bundle"
	searchstore "github.com/lleontor705/cortex/internal/store/search"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
)

// feedback_wiring_test.go verifies bundle.WireSearchFeedback connects the
// search store's request-scoped feedback attribution (REQ-RET-001) to the
// observation store's persistence layer, and that the removed Stores shared
// search-query field is gone.

// setupFeedbackDB creates an in-memory SQLite DB with the minimal schema needed
// for search + feedback: observations (with FTS5), sessions, and search_feedback.
func setupFeedbackDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE sessions (
			id TEXT PRIMARY KEY, project TEXT NOT NULL, directory TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')), ended_at TEXT, summary TEXT
		)`,
		`CREATE TABLE observations (
			id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
			type TEXT NOT NULL, title TEXT NOT NULL, content TEXT NOT NULL,
			tool_name TEXT, project TEXT, scope TEXT NOT NULL DEFAULT 'project',
			topic_key TEXT, normalized_hash TEXT, confidence REAL DEFAULT 1.0,
			source TEXT DEFAULT 'manual', tags TEXT,
			revision_count INTEGER DEFAULT 1, duplicate_count INTEGER DEFAULT 1,
			last_seen_at TEXT, created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')), deleted_at TEXT
		)`,
		`CREATE VIRTUAL TABLE observations_fts USING fts5(
			title, content, tool_name, type, project, scope, topic_key,
			content='observations', content_rowid='id', tokenize='porter unicode61'
		)`,
		`CREATE TRIGGER obs_fts_insert AFTER INSERT ON observations BEGIN
			INSERT INTO observations_fts(rowid, title, content, tool_name, type, project, scope, topic_key)
			VALUES (new.id, new.title, new.content, new.tool_name, new.type, new.project, new.scope, new.topic_key);
		END`,
		`CREATE TRIGGER obs_fts_delete AFTER DELETE ON observations BEGIN
			INSERT INTO observations_fts(observations_fts, rowid, title, content, tool_name, type, project, scope, topic_key)
			VALUES ('delete', old.id, old.title, old.content, old.tool_name, old.type, old.project, old.scope, old.topic_key);
		END`,
		`CREATE TABLE search_feedback (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			query TEXT NOT NULL, observation_id INTEGER NOT NULL,
			rank_position INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec schema: %v\nsql: %s", err, s)
		}
	}
	return db
}

func insertFeedbackObservation(t *testing.T, db *sql.DB, id int64, title, content, project string) {
	t.Helper()
	now := "2026-01-01T00:00:00Z"
	if _, err := db.Exec(
		`INSERT INTO observations (id, session_id, type, title, content, project, scope, created_at, updated_at)
		 VALUES (?, 's', 'decision', ?, ?, ?, 'project', ?, ?)`,
		id, title, content, project, now, now,
	); err != nil {
		t.Fatalf("insert observation: %v", err)
	}
}

// TestWireSearchFeedback_NilSafe verifies the wiring helper does not panic when
// stores or sub-stores are nil (feedback stays safely disabled).
func TestWireSearchFeedback_NilSafe(t *testing.T) {
	// All nil combinations must be safe no-ops.
	bundle.WireSearchFeedback(nil)
	bundle.WireSearchFeedback(&bundle.Stores{})
	bundle.WireSearchFeedback(&bundle.Stores{Search: searchstore.NewStore(nil)})
	bundle.WireSearchFeedback(&bundle.Stores{Observations: sqlitestore.NewStore(nil)})
}

// TestWireSearchFeedback_AttributesToCorrectQuery verifies the bundle wiring
// routes RecordFeedback through to the observation store's search_feedback
// table, attributed to the originating search's query (REQ-RET-001).
func TestWireSearchFeedback_AttributesToCorrectQuery(t *testing.T) {
	db := setupFeedbackDB(t)
	insertFeedbackObservation(t, db, 1, "Alpha Gamma", "alpha gamma content", "p")
	insertFeedbackObservation(t, db, 2, "Beta Gamma", "beta gamma content", "p")

	stores := &bundle.Stores{
		Observations: sqlitestore.NewStore(db),
		Search:       searchstore.NewStore(db),
	}
	bundle.WireSearchFeedback(stores)

	ctx := context.Background()

	// Search A returns the Alpha result carrying a SearchID.
	resultsA, err := stores.Search.Search(ctx, "alpha", domain.SearchOptions{Project: "p"})
	if err != nil || len(resultsA) == 0 {
		t.Fatalf("search A: err=%v len=%d", err, len(resultsA))
	}
	searchIDA := resultsA[0].SearchID
	if searchIDA == "" {
		t.Fatal("no SearchID on results")
	}

	// A second search runs; it must NOT clobber attribution for Search A.
	if _, err := stores.Search.Search(ctx, "beta", domain.SearchOptions{Project: "p"}); err != nil {
		t.Fatalf("search B: %v", err)
	}

	// Feedback for Search A's result persists against query "alpha".
	if err := stores.Search.RecordFeedback(ctx, searchIDA, resultsA[0].ID, 1); err != nil {
		t.Fatalf("RecordFeedback: %v", err)
	}

	// search_feedback table holds exactly one row attributed to query "alpha".
	var query string
	var obsID int64
	err = db.QueryRow(`SELECT query, observation_id FROM search_feedback WHERE query = ?`, "alpha").Scan(&query, &obsID)
	if err != nil {
		t.Fatalf("no feedback row for 'alpha': %v", err)
	}
	if obsID != resultsA[0].ID {
		t.Errorf("feedback observation_id = %d, want %d", obsID, resultsA[0].ID)
	}

	// Confirm NO feedback was recorded for "beta" (no cross-attribution).
	var betaCount int
	_ = db.QueryRow(`SELECT count(*) FROM search_feedback WHERE query = ?`, "beta").Scan(&betaCount)
	if betaCount != 0 {
		t.Errorf("cross-attribution: %d feedback rows for 'beta' (should be 0)", betaCount)
	}
}

// TestWireSearchFeedback_UnknownSearchID_NoPersistence verifies that feedback
// for an unknown SearchID records NOTHING — never falls back to a shared global.
func TestWireSearchFeedback_UnknownSearchID_NoPersistence(t *testing.T) {
	db := setupFeedbackDB(t)
	insertFeedbackObservation(t, db, 1, "Some Topic", "some content here", "p")

	stores := &bundle.Stores{
		Observations: sqlitestore.NewStore(db),
		Search:       searchstore.NewStore(db),
	}
	bundle.WireSearchFeedback(stores)

	ctx := context.Background()
	// Unknown SearchID: safe no-op, no persistence.
	if err := stores.Search.RecordFeedback(ctx, domain.SearchID("sch_unknown"), 1, 0); err != nil {
		t.Errorf("unknown SearchID returned error: %v", err)
	}
	var count int
	_ = db.QueryRow(`SELECT count(*) FROM search_feedback`).Scan(&count)
	if count != 0 {
		t.Errorf("unknown SearchID persisted %d rows (global fallback)", count)
	}
}

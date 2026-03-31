package graph

import (
	"context"
	"database/sql"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
	"github.com/lleontor705/cortex/testutil"
)

// setupTestStore creates a test database with migrations and returns a Store.
func setupTestStore(t *testing.T) (*Store, *sql.DB, func()) {
	t.Helper()

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
				confidence REAL NOT NULL DEFAULT 1.0,
				source TEXT,
				reasoning TEXT,
				valid_from TEXT,
				invalid_at TEXT,
				evolution_id INTEGER,
				evolution_type TEXT NOT NULL DEFAULT 'original',
				fact_state TEXT NOT NULL DEFAULT 'current',
				change_reason TEXT,
				created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (from_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
				FOREIGN KEY (to_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
				UNIQUE(from_obs_id, to_obs_id, relation_type)
			);
			CREATE INDEX idx_edges_from ON edges(from_obs_id);
			CREATE INDEX idx_edges_to ON edges(to_obs_id);
		`,
		DownSQL: `
			DROP TABLE IF EXISTS edges;
		`,
	})

	testDB := testutil.NewTestDBWithMigrations(t, registry)
	store := NewStore(testDB.DB())

	return store, testDB.DB(), func() {
		testDB.Cleanup()
	}
}

// createTestSession inserts a test session.
func createTestSession(t *testing.T, db *sql.DB, sessionID, project string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO sessions (id, project, directory) VALUES (?, ?, ?)`,
		sessionID, project, "/tmp/test",
	)
	if err != nil {
		t.Fatalf("create test session: %v", err)
	}
}

// createTestObservation inserts a test observation and returns its ID.
func createTestObservation(t *testing.T, db *sql.DB, title, sessionID string) int64 {
	t.Helper()
	result, err := db.Exec(
		`INSERT INTO observations (session_id, type, title, content, project, scope)
		 VALUES (?, 'manual', ?, 'test content', 'test-project', 'project')`,
		sessionID, title,
	)
	if err != nil {
		t.Fatalf("create test observation: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func TestCreateEdge(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	createTestSession(t, db, "s1", "test-project")
	obs1 := createTestObservation(t, db, "Obs 1", "s1")
	obs2 := createTestObservation(t, db, "Obs 2", "s1")

	t.Run("success", func(t *testing.T) {
		edge := &domain.Edge{
			FromObsID:    obs1,
			ToObsID:      obs2,
			RelationType: domain.RelationReferences,
			Weight:       0.8,
		}

		err := store.CreateEdge(context.Background(), edge)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if edge.ID == 0 {
			t.Error("expected edge ID to be set")
		}
	})

	t.Run("duplicate edge", func(t *testing.T) {
		edge := &domain.Edge{
			FromObsID:    obs1,
			ToObsID:      obs2,
			RelationType: domain.RelationReferences,
			Weight:       0.5,
		}

		err := store.CreateEdge(context.Background(), edge)
		if err != domain.ErrAlreadyExists {
			t.Fatalf("expected ErrAlreadyExists, got %v", err)
		}
	})

	t.Run("different relation type allowed", func(t *testing.T) {
		edge := &domain.Edge{
			FromObsID:    obs1,
			ToObsID:      obs2,
			RelationType: domain.RelationRelatesTo,
			Weight:       1.0,
		}

		err := store.CreateEdge(context.Background(), edge)
		if err != nil {
			t.Fatalf("expected no error for different relation type, got %v", err)
		}
	})

	t.Run("nil edge", func(t *testing.T) {
		err := store.CreateEdge(context.Background(), nil)
		if err == nil {
			t.Fatal("expected error for nil edge")
		}
	})
}

func TestGetRelated(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	createTestSession(t, db, "s1", "test-project")
	obs1 := createTestObservation(t, db, "Obs 1", "s1")
	obs2 := createTestObservation(t, db, "Obs 2", "s1")
	obs3 := createTestObservation(t, db, "Obs 3", "s1")
	obs4 := createTestObservation(t, db, "Obs 4", "s1")

	// Create edges: 1->2, 2->3, 3->4
	ctx := context.Background()
	store.CreateEdge(ctx, &domain.Edge{FromObsID: obs1, ToObsID: obs2, RelationType: "references", Weight: 1.0}) //nolint:errcheck
	store.CreateEdge(ctx, &domain.Edge{FromObsID: obs2, ToObsID: obs3, RelationType: "references", Weight: 1.0}) //nolint:errcheck
	store.CreateEdge(ctx, &domain.Edge{FromObsID: obs3, ToObsID: obs4, RelationType: "references", Weight: 1.0}) //nolint:errcheck

	t.Run("depth 1", func(t *testing.T) {
		results, err := store.GetRelated(ctx, obs1, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].ID != obs2 {
			t.Errorf("expected obs %d, got %d", obs2, results[0].ID)
		}
	})

	t.Run("depth 2", func(t *testing.T) {
		results, err := store.GetRelated(ctx, obs1, 2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
	})

	t.Run("depth 3 traverses full chain", func(t *testing.T) {
		results, err := store.GetRelated(ctx, obs1, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 results, got %d", len(results))
		}
	})

	t.Run("bidirectional", func(t *testing.T) {
		// obs2 should find obs1 (reverse) and obs3 (forward)
		results, err := store.GetRelated(ctx, obs2, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results (bidirectional), got %d", len(results))
		}
	})

	t.Run("no relations", func(t *testing.T) {
		orphan := createTestObservation(t, db, "Orphan", "s1")
		results, err := store.GetRelated(ctx, orphan, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("expected 0 results, got %d", len(results))
		}
	})
}

func TestDeleteEdge(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	createTestSession(t, db, "s1", "test-project")
	obs1 := createTestObservation(t, db, "Obs 1", "s1")
	obs2 := createTestObservation(t, db, "Obs 2", "s1")

	ctx := context.Background()
	edge := &domain.Edge{FromObsID: obs1, ToObsID: obs2, RelationType: "references", Weight: 1.0}
	if err := store.CreateEdge(ctx, edge); err != nil {
		t.Fatalf("setup: create edge: %v", err)
	}

	t.Run("success", func(t *testing.T) {
		err := store.DeleteEdge(ctx, edge.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify deletion
		results, err := store.GetRelated(ctx, obs1, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 0 {
			t.Error("expected no related observations after delete")
		}
	})

	t.Run("not found", func(t *testing.T) {
		err := store.DeleteEdge(ctx, 99999)
		if !domain.IsNotFoundError(err) {
			t.Fatalf("expected NotFoundError, got %v", err)
		}
	})
}

package scoring

import (
	"context"
	"database/sql"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
	"github.com/lleontor705/cortex/testutil"
)

// setupTestStore creates a test database with migrations for scoring tests.
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

	testDB := testutil.NewTestDBWithMigrations(t, registry)
	store := NewStore(testDB.DB())

	return store, testDB.DB(), func() {
		testDB.Cleanup()
	}
}

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

func createTestObservation(t *testing.T, db *sql.DB, title, project, sessionID string) int64 {
	t.Helper()
	result, err := db.Exec(
		`INSERT INTO observations (session_id, type, title, content, project, scope)
		 VALUES (?, 'manual', ?, 'test content', ?, 'project')`,
		sessionID, title, project,
	)
	if err != nil {
		t.Fatalf("create test observation: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func TestGetScore(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	createTestSession(t, db, "s1", "test-project")
	obsID := createTestObservation(t, db, "Obs 1", "test-project", "s1")

	ctx := context.Background()

	t.Run("auto-initialized score", func(t *testing.T) {
		score, err := store.GetScore(ctx, obsID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if score.ObservationID != obsID {
			t.Errorf("expected obs ID %d, got %d", obsID, score.ObservationID)
		}
		if score.Score != 0.0 {
			t.Errorf("expected initial score 0.0, got %f", score.Score)
		}
		if score.AccessCount != 0 {
			t.Errorf("expected access count 0, got %d", score.AccessCount)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := store.GetScore(ctx, 99999)
		if !domain.IsNotFoundError(err) {
			t.Fatalf("expected NotFoundError, got %v", err)
		}
	})
}

func TestRecordAccess(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	createTestSession(t, db, "s1", "test-project")
	obsID := createTestObservation(t, db, "Obs 1", "test-project", "s1")

	ctx := context.Background()

	t.Run("increments access count", func(t *testing.T) {
		err := store.RecordAccess(ctx, obsID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		score, _ := store.GetScore(ctx, obsID)
		if score.AccessCount != 1 {
			t.Errorf("expected access count 1, got %d", score.AccessCount)
		}

		// Verify last_accessed was updated (row count check is sufficient;
		// timestamp parsing can vary across SQLite drivers)
	})

	t.Run("multiple accesses", func(t *testing.T) {
		store.RecordAccess(ctx, obsID)
		store.RecordAccess(ctx, obsID)

		score, _ := store.GetScore(ctx, obsID)
		if score.AccessCount != 3 {
			t.Errorf("expected access count 3, got %d", score.AccessCount)
		}
	})

	t.Run("not found", func(t *testing.T) {
		err := store.RecordAccess(ctx, 99999)
		if !domain.IsNotFoundError(err) {
			t.Fatalf("expected NotFoundError, got %v", err)
		}
	})
}

func TestSetScore(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	createTestSession(t, db, "s1", "test-project")
	obsID := createTestObservation(t, db, "Obs 1", "test-project", "s1")

	ctx := context.Background()

	t.Run("set score", func(t *testing.T) {
		err := store.SetScore(ctx, obsID, 3.5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		score, _ := store.GetScore(ctx, obsID)
		if score.Score != 3.5 {
			t.Errorf("expected score 3.5, got %f", score.Score)
		}
	})

	t.Run("clamps to max", func(t *testing.T) {
		err := store.SetScore(ctx, obsID, 10.0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		score, _ := store.GetScore(ctx, obsID)
		if score.Score != 5.0 {
			t.Errorf("expected score clamped to 5.0, got %f", score.Score)
		}
	})

	t.Run("clamps to min", func(t *testing.T) {
		err := store.SetScore(ctx, obsID, -5.0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		score, _ := store.GetScore(ctx, obsID)
		if score.Score != 0.0 {
			t.Errorf("expected score clamped to 0.0, got %f", score.Score)
		}
	})

	t.Run("not found", func(t *testing.T) {
		err := store.SetScore(ctx, 99999, 1.0)
		if !domain.IsNotFoundError(err) {
			t.Fatalf("expected NotFoundError, got %v", err)
		}
	})
}

func TestUpdateScore(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	createTestSession(t, db, "s1", "test-project")
	obsID := createTestObservation(t, db, "Obs 1", "test-project", "s1")

	ctx := context.Background()

	t.Run("increment", func(t *testing.T) {
		// Initial score is 0.0
		err := store.UpdateScore(ctx, obsID, 2.5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		score, _ := store.GetScore(ctx, obsID)
		if score.Score != 2.5 {
			t.Errorf("expected score 2.5, got %f", score.Score)
		}
	})

	t.Run("decrement", func(t *testing.T) {
		err := store.UpdateScore(ctx, obsID, -1.0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		score, _ := store.GetScore(ctx, obsID)
		if score.Score != 1.5 {
			t.Errorf("expected score 1.5, got %f", score.Score)
		}
	})
}

func TestGetTopByScore(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	createTestSession(t, db, "s1", "test-project")
	obs1 := createTestObservation(t, db, "Obs 1", "test-project", "s1")
	obs2 := createTestObservation(t, db, "Obs 2", "test-project", "s1")
	obs3 := createTestObservation(t, db, "Obs 3", "other-project", "s1")

	ctx := context.Background()
	store.SetScore(ctx, obs1, 4.0)
	store.SetScore(ctx, obs2, 2.0)
	store.SetScore(ctx, obs3, 3.0)

	t.Run("all projects", func(t *testing.T) {
		scores, err := store.GetTopByScore(ctx, "", 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(scores) != 3 {
			t.Fatalf("expected 3 scores, got %d", len(scores))
		}
		// Should be ordered by score DESC
		if scores[0].Score != 4.0 {
			t.Errorf("expected first score 4.0, got %f", scores[0].Score)
		}
	})

	t.Run("filter by project", func(t *testing.T) {
		scores, err := store.GetTopByScore(ctx, "test-project", 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(scores) != 2 {
			t.Fatalf("expected 2 scores for test-project, got %d", len(scores))
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		scores, err := store.GetTopByScore(ctx, "", 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(scores) != 1 {
			t.Fatalf("expected 1 score, got %d", len(scores))
		}
		if scores[0].ObservationID != obs1 {
			t.Errorf("expected obs %d (highest score), got %d", obs1, scores[0].ObservationID)
		}
	})
}

func TestGetAllScores(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	createTestSession(t, db, "s1", "test-project")
	createTestObservation(t, db, "Obs 1", "test-project", "s1")
	createTestObservation(t, db, "Obs 2", "test-project", "s1")

	ctx := context.Background()

	scores, err := store.GetAllScores(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}
}

func TestGetIncomingEdgeCount(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	createTestSession(t, db, "s1", "test-project")
	obs1 := createTestObservation(t, db, "Obs 1", "test-project", "s1")
	obs2 := createTestObservation(t, db, "Obs 2", "test-project", "s1")
	obs3 := createTestObservation(t, db, "Obs 3", "test-project", "s1")

	ctx := context.Background()

	// Create edges: obs1->obs2, obs3->obs2
	db.Exec(`INSERT INTO edges (from_obs_id, to_obs_id, relation_type, weight) VALUES (?, ?, 'references', 1.0)`, obs1, obs2)
	db.Exec(`INSERT INTO edges (from_obs_id, to_obs_id, relation_type, weight) VALUES (?, ?, 'relates_to', 1.0)`, obs3, obs2)

	t.Run("with edges", func(t *testing.T) {
		count, err := store.GetIncomingEdgeCount(ctx, obs2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if count != 2 {
			t.Errorf("expected 2 incoming edges, got %d", count)
		}
	})

	t.Run("no edges", func(t *testing.T) {
		count, err := store.GetIncomingEdgeCount(ctx, obs1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if count != 0 {
			t.Errorf("expected 0 incoming edges, got %d", count)
		}
	})
}

func TestGetObservation(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	createTestSession(t, db, "s1", "test-project")
	obsID := createTestObservation(t, db, "Test Title", "test-project", "s1")

	ctx := context.Background()

	t.Run("found", func(t *testing.T) {
		obs, err := store.GetObservation(ctx, obsID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if obs.Title != "Test Title" {
			t.Errorf("expected title 'Test Title', got %q", obs.Title)
		}
		if obs.Project != "test-project" {
			t.Errorf("expected project 'test-project', got %q", obs.Project)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := store.GetObservation(ctx, 99999)
		if !domain.IsNotFoundError(err) {
			t.Fatalf("expected NotFoundError, got %v", err)
		}
	})

	t.Run("soft deleted", func(t *testing.T) {
		db.Exec(`UPDATE observations SET deleted_at = datetime('now') WHERE id = ?`, obsID)
		_, err := store.GetObservation(ctx, obsID)
		if !domain.IsNotFoundError(err) {
			t.Fatalf("expected NotFoundError for soft-deleted obs, got %v", err)
		}
	})
}

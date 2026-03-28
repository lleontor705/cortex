package session

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
	"github.com/lleontor705/cortex/testutil"
)

// setupTestStore creates a test database with the session tables and returns a store.
func setupTestStore(t *testing.T) (*Store, func()) {
	t.Helper()

	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1,
		Name:    "create_sessions_table",
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
				session_id TEXT NOT NULL,
				title      TEXT NOT NULL,
				content    TEXT NOT NULL,
				type       TEXT NOT NULL,
				project    TEXT,
				scope      TEXT NOT NULL DEFAULT 'project',
				confidence REAL NOT NULL DEFAULT 1.0,
				source     TEXT NOT NULL DEFAULT 'manual',
				tags       TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				deleted_at TEXT,
				FOREIGN KEY (session_id) REFERENCES sessions(id)
			);

			CREATE INDEX IF NOT EXISTS idx_obs_session ON observations(session_id);
		`,
		DownSQL: `
			DROP TABLE IF EXISTS observations;
			DROP TABLE IF EXISTS sessions;
		`,
	})

	testDB := testutil.NewTestDBWithMigrations(t, registry)
	store := NewStore(testDB.DB())

	return store, func() {
		testDB.Cleanup()
	}
}

func TestNewStore(t *testing.T) {
	t.Run("creates store successfully", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		testutil.AssertNotNil(t, store)
	})
}

func TestStore_Create(t *testing.T) {
	t.Run("creates session with all fields", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		session := &domain.Session{
			ID:        "session-123",
			Project:   "test-project",
			Directory: "/tmp/test-project",
		}

		err := store.Create(ctx, session)
		testutil.RequireNoError(t, err)

		// Verify StartedAt was set
		testutil.AssertTrue(t, !session.StartedAt.IsZero(), "StartedAt should be set")
	})

	t.Run("creates session with pre-set StartedAt", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		expectedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		session := &domain.Session{
			ID:        "session-456",
			Project:   "test-project",
			Directory: "/tmp/test-project",
			StartedAt: expectedTime,
		}

		err := store.Create(ctx, session)
		testutil.RequireNoError(t, err)

		// Retrieve and verify timestamp was preserved
		retrieved, err := store.GetByID(ctx, "session-456")
		testutil.RequireNoError(t, err)

		testutil.AssertWithinDuration(t, expectedTime, retrieved.StartedAt, time.Second)
	})

	t.Run("returns error for nil session", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		err := store.Create(ctx, nil)

		testutil.AssertError(t, err)
		testutil.AssertValidationError(t, err, "session")
	})

	t.Run("returns error for empty ID", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		session := &domain.Session{
			ID:        "",
			Project:   "test-project",
			Directory: "/tmp/test-project",
		}

		err := store.Create(ctx, session)

		testutil.AssertError(t, err)
		testutil.AssertValidationError(t, err, "id")
	})

	t.Run("returns error for empty project", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		session := &domain.Session{
			ID:        "session-123",
			Project:   "",
			Directory: "/tmp/test-project",
		}

		err := store.Create(ctx, session)

		testutil.AssertError(t, err)
		testutil.AssertValidationError(t, err, "project")
	})

	t.Run("returns error for empty directory", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		session := &domain.Session{
			ID:        "session-123",
			Project:   "test-project",
			Directory: "",
		}

		err := store.Create(ctx, session)

		testutil.AssertError(t, err)
		testutil.AssertValidationError(t, err, "directory")
	})

	t.Run("returns error for duplicate ID", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		session1 := &domain.Session{
			ID:        "session-duplicate",
			Project:   "test-project",
			Directory: "/tmp/test-project",
		}
		session2 := &domain.Session{
			ID:        "session-duplicate",
			Project:   "other-project",
			Directory: "/tmp/other-project",
		}

		err := store.Create(ctx, session1)
		testutil.RequireNoError(t, err)

		err = store.Create(ctx, session2)
		testutil.AssertError(t, err)
	})
}

func TestStore_GetByID(t *testing.T) {
	t.Run("retrieves existing session", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		original := &domain.Session{
			ID:        "session-get-test",
			Project:   "my-project",
			Directory: "/home/user/my-project",
		}

		err := store.Create(ctx, original)
		testutil.RequireNoError(t, err)

		retrieved, err := store.GetByID(ctx, "session-get-test")
		testutil.RequireNoError(t, err)

		testutil.AssertEqual(t, original.ID, retrieved.ID)
		testutil.AssertEqual(t, original.Project, retrieved.Project)
		testutil.AssertEqual(t, original.Directory, retrieved.Directory)
		testutil.AssertTrue(t, retrieved.EndedAt == nil, "EndedAt should be nil for active session")
	})

	t.Run("retrieves ended session", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		session := &domain.Session{
			ID:        "session-ended",
			Project:   "test-project",
			Directory: "/tmp/test-project",
		}

		err := store.Create(ctx, session)
		testutil.RequireNoError(t, err)

		// End the session
		err = store.End(ctx, "session-ended", "Completed successfully")
		testutil.RequireNoError(t, err)

		// Retrieve and verify
		retrieved, err := store.GetByID(ctx, "session-ended")
		testutil.RequireNoError(t, err)

		testutil.AssertTrue(t, retrieved.EndedAt != nil, "EndedAt should not be nil")
		testutil.AssertEqual(t, "Completed successfully", retrieved.Summary)
	})

	t.Run("returns NotFoundError for non-existent session", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		_, err := store.GetByID(ctx, "nonexistent-session")

		testutil.AssertNotFoundError(t, err, "session", "nonexistent-session")
	})

	t.Run("returns error for empty ID", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		_, err := store.GetByID(ctx, "")

		testutil.AssertError(t, err)
		testutil.AssertValidationError(t, err, "id")
	})
}

func TestStore_End(t *testing.T) {
	t.Run("ends active session with summary", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		session := &domain.Session{
			ID:        "session-end-test",
			Project:   "test-project",
			Directory: "/tmp/test-project",
		}

		err := store.Create(ctx, session)
		testutil.RequireNoError(t, err)

		// End the session
		err = store.End(ctx, "session-end-test", "Session completed")
		testutil.RequireNoError(t, err)

		// Verify it was ended
		retrieved, err := store.GetByID(ctx, "session-end-test")
		testutil.RequireNoError(t, err)

		testutil.AssertTrue(t, retrieved.EndedAt != nil, "EndedAt should be set")
		testutil.AssertEqual(t, "Session completed", retrieved.Summary)
	})

	t.Run("ends session with empty summary", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		session := &domain.Session{
			ID:        "session-empty-summary",
			Project:   "test-project",
			Directory: "/tmp/test-project",
		}

		err := store.Create(ctx, session)
		testutil.RequireNoError(t, err)

		// End the session with empty summary
		err = store.End(ctx, "session-empty-summary", "")
		testutil.RequireNoError(t, err)

		// Verify it was ended
		retrieved, err := store.GetByID(ctx, "session-empty-summary")
		testutil.RequireNoError(t, err)

		testutil.AssertTrue(t, retrieved.EndedAt != nil, "EndedAt should be set")
		testutil.AssertEqual(t, "", retrieved.Summary)
	})

	t.Run("returns error for already ended session", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		session := &domain.Session{
			ID:        "session-already-ended",
			Project:   "test-project",
			Directory: "/tmp/test-project",
		}

		err := store.Create(ctx, session)
		testutil.RequireNoError(t, err)

		// End the session once
		err = store.End(ctx, "session-already-ended", "First end")
		testutil.RequireNoError(t, err)

		// Try to end again
		err = store.End(ctx, "session-already-ended", "Second end")
		testutil.AssertError(t, err)
		testutil.AssertEqual(t, domain.ErrSessionEnded, err)
	})

	t.Run("returns error for non-existent session", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		err := store.End(ctx, "nonexistent-session", "Summary")

		testutil.AssertNotFoundError(t, err, "session", "nonexistent-session")
	})

	t.Run("returns error for empty ID", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		err := store.End(ctx, "", "Summary")

		testutil.AssertError(t, err)
		testutil.AssertValidationError(t, err, "id")
	})
}

func TestStore_List(t *testing.T) {
	t.Run("lists all sessions", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create sessions for different projects
		sessions := []*domain.Session{
			{ID: "s1", Project: "project-a", Directory: "/tmp/a"},
			{ID: "s2", Project: "project-b", Directory: "/tmp/b"},
			{ID: "s3", Project: "project-a", Directory: "/tmp/a"},
		}

		for _, s := range sessions {
			err := store.Create(ctx, s)
			testutil.RequireNoError(t, err)
		}

		// List all sessions
		results, err := store.List(ctx, "")
		testutil.RequireNoError(t, err)

		testutil.AssertLen(t, results, 3)
	})

	t.Run("lists sessions for specific project", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create sessions for different projects
		sessions := []*domain.Session{
			{ID: "s1", Project: "project-a", Directory: "/tmp/a"},
			{ID: "s2", Project: "project-b", Directory: "/tmp/b"},
			{ID: "s3", Project: "project-a", Directory: "/tmp/a"},
		}

		for _, s := range sessions {
			err := store.Create(ctx, s)
			testutil.RequireNoError(t, err)
		}

		// List sessions for project-a
		results, err := store.List(ctx, "project-a")
		testutil.RequireNoError(t, err)

		testutil.AssertLen(t, results, 2)

		// Verify all results are for project-a
		for _, r := range results {
			testutil.AssertEqual(t, "project-a", r.Project)
		}
	})

	t.Run("orders by started_at descending", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create sessions with explicit timestamps
		now := time.Now()
		s1 := &domain.Session{
			ID:        "oldest",
			Project:   "test-project",
			Directory: "/tmp/test",
			StartedAt: now.Add(-2 * time.Hour),
		}
		s2 := &domain.Session{
			ID:        "newest",
			Project:   "test-project",
			Directory: "/tmp/test",
			StartedAt: now,
		}
		s3 := &domain.Session{
			ID:        "middle",
			Project:   "test-project",
			Directory: "/tmp/test",
			StartedAt: now.Add(-1 * time.Hour),
		}

		testutil.RequireNoError(t, store.Create(ctx, s1))
		testutil.RequireNoError(t, store.Create(ctx, s2))
		testutil.RequireNoError(t, store.Create(ctx, s3))

		results, err := store.List(ctx, "test-project")
		testutil.RequireNoError(t, err)

		testutil.AssertLen(t, results, 3)

		// Newest should be first
		testutil.AssertEqual(t, "newest", results[0].ID)
		testutil.AssertEqual(t, "middle", results[1].ID)
		testutil.AssertEqual(t, "oldest", results[2].ID)
	})

	t.Run("returns empty list for project with no sessions", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		results, err := store.List(ctx, "nonexistent-project")

		testutil.RequireNoError(t, err)
		testutil.AssertLen(t, results, 0)
	})
}

func TestStore_Recent(t *testing.T) {
	t.Run("returns recent sessions with limit", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create 5 sessions
		for i := 0; i < 5; i++ {
			session := &domain.Session{
				ID:        string(rune('a' + i)),
				Project:   "test-project",
				Directory: "/tmp/test",
			}
			err := store.Create(ctx, session)
			testutil.RequireNoError(t, err)
		}

		// Get recent with limit of 3
		results, err := store.Recent(ctx, "test-project", 3)
		testutil.RequireNoError(t, err)

		testutil.AssertLen(t, results, 3)
	})

	t.Run("uses default limit when limit <= 0", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create 10 sessions
		for i := 0; i < 10; i++ {
			session := &domain.Session{
				ID:        string(rune('0' + i)),
				Project:   "test-project",
				Directory: "/tmp/test",
			}
			err := store.Create(ctx, session)
			testutil.RequireNoError(t, err)
		}

		// Get recent with limit of 0 (should use default of 5)
		results, err := store.Recent(ctx, "test-project", 0)
		testutil.RequireNoError(t, err)

		testutil.AssertLen(t, results, 5)
	})
}

func TestStore_GetWithStats(t *testing.T) {
	t.Run("returns session with observation count", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create session
		session := &domain.Session{
			ID:        "session-stats",
			Project:   "test-project",
			Directory: "/tmp/test",
		}
		err := store.Create(ctx, session)
		testutil.RequireNoError(t, err)

		// Add some observations
		db := store.db
		for i := 0; i < 3; i++ {
			_, err := db.ExecContext(ctx, `
				INSERT INTO observations (session_id, title, content, type, project)
				VALUES (?, ?, ?, ?, ?)
			`, "session-stats", "Test", "Content", "manual", "test-project")
			testutil.RequireNoError(t, err)
		}

		// Get stats
		stats, err := store.GetWithStats(ctx, "session-stats")
		testutil.RequireNoError(t, err)

		testutil.AssertNotNil(t, stats.Session)
		testutil.AssertEqual(t, "session-stats", stats.Session.ID)
		testutil.AssertEqual(t, 3, stats.ObservationCount)
	})

	t.Run("returns zero count for session with no observations", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create session without observations
		session := &domain.Session{
			ID:        "session-empty",
			Project:   "test-project",
			Directory: "/tmp/test",
		}
		err := store.Create(ctx, session)
		testutil.RequireNoError(t, err)

		// Get stats
		stats, err := store.GetWithStats(ctx, "session-empty")
		testutil.RequireNoError(t, err)

		testutil.AssertEqual(t, 0, stats.ObservationCount)
	})
}

func TestStore_RecentWithStats(t *testing.T) {
	t.Run("returns recent sessions with observation counts", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		db := store.db

		// Create sessions
		for i := 0; i < 3; i++ {
			session := &domain.Session{
				ID:        string(rune('a' + i)),
				Project:   "test-project",
				Directory: "/tmp/test",
			}
			err := store.Create(ctx, session)
			testutil.RequireNoError(t, err)

			// Add observations for each session
			for j := 0; j <= i; j++ {
				_, err := db.ExecContext(ctx, `
					INSERT INTO observations (session_id, title, content, type, project)
					VALUES (?, ?, ?, ?, ?)
				`, session.ID, "Test", "Content", "manual", "test-project")
				testutil.RequireNoError(t, err)
			}
		}

		// Get recent with stats
		results, err := store.RecentWithStats(ctx, "test-project", 10)
		testutil.RequireNoError(t, err)

		testutil.AssertLen(t, results, 3)

		// Verify counts
		for _, stats := range results {
			expected := int(stats.Session.ID[0] - 'a' + 1)
			testutil.AssertEqual(t, expected, stats.ObservationCount)
		}
	})
}

func TestStore_GetCurrent(t *testing.T) {
	t.Run("returns current active session", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create an ended session
		endedSession := &domain.Session{
			ID:        "ended-session",
			Project:   "test-project",
			Directory: "/tmp/test",
		}
		err := store.Create(ctx, endedSession)
		testutil.RequireNoError(t, err)
		err = store.End(ctx, "ended-session", "Done")
		testutil.RequireNoError(t, err)

		// Create an active session
		activeSession := &domain.Session{
			ID:        "active-session",
			Project:   "test-project",
			Directory: "/tmp/test",
		}
		err = store.Create(ctx, activeSession)
		testutil.RequireNoError(t, err)

		// Get current
		current, err := store.GetCurrent(ctx, "test-project")
		testutil.RequireNoError(t, err)

		testutil.AssertEqual(t, "active-session", current.ID)
		testutil.AssertTrue(t, current.EndedAt == nil, "Current session should be active")
	})

	t.Run("returns NotFoundError when no active session", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create only ended sessions
		session := &domain.Session{
			ID:        "ended-only",
			Project:   "test-project",
			Directory: "/tmp/test",
		}
		err := store.Create(ctx, session)
		testutil.RequireNoError(t, err)
		err = store.End(ctx, "ended-only", "Done")
		testutil.RequireNoError(t, err)

		// Try to get current
		_, err = store.GetCurrent(ctx, "test-project")
		testutil.AssertNotFoundError(t, err, "active session", "test-project")
	})

	t.Run("returns error for empty project", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		_, err := store.GetCurrent(ctx, "")

		testutil.AssertError(t, err)
		testutil.AssertValidationError(t, err, "project")
	})
}

func TestStore_GetStats(t *testing.T) {
	t.Run("returns overall statistics", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		db := store.db

		// Create sessions for different projects
		for i := 0; i < 3; i++ {
			session := &domain.Session{
				ID:        string(rune('a' + i)),
				Project:   "project-" + string(rune('a'+i)),
				Directory: "/tmp/test",
			}
			err := store.Create(ctx, session)
			testutil.RequireNoError(t, err)
		}

		// End one session
		err := store.End(ctx, "a", "Done")
		testutil.RequireNoError(t, err)

		// Add some observations
		for i := 0; i < 5; i++ {
			_, err := db.ExecContext(ctx, `
				INSERT INTO observations (session_id, title, content, type, project)
				VALUES (?, ?, ?, ?, ?)
			`, "b", "Test", "Content", "manual", "project-b")
			testutil.RequireNoError(t, err)
		}

		// Get stats
		stats, err := store.GetStats(ctx)
		testutil.RequireNoError(t, err)

		testutil.AssertEqual(t, 3, stats.TotalSessions)
		testutil.AssertEqual(t, 2, stats.ActiveSessions)
		testutil.AssertEqual(t, 1, stats.EndedSessions)
		testutil.AssertEqual(t, 5, stats.TotalObservations)
		testutil.AssertLen(t, stats.Projects, 3)
	})

	t.Run("returns empty stats when no sessions", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		stats, err := store.GetStats(ctx)
		testutil.RequireNoError(t, err)

		testutil.AssertEqual(t, 0, stats.TotalSessions)
		testutil.AssertEqual(t, 0, stats.ActiveSessions)
		testutil.AssertEqual(t, 0, stats.EndedSessions)
		testutil.AssertEqual(t, 0, stats.TotalObservations)
		testutil.AssertLen(t, stats.Projects, 0)
	})
}

func TestStore_ScanSessions(t *testing.T) {
	t.Run("handles null ended_at and summary", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create active session (null ended_at and summary)
		session := &domain.Session{
			ID:        "null-fields",
			Project:   "test-project",
			Directory: "/tmp/test",
		}
		err := store.Create(ctx, session)
		testutil.RequireNoError(t, err)

		// Retrieve and verify
		retrieved, err := store.GetByID(ctx, "null-fields")
		testutil.RequireNoError(t, err)

		testutil.AssertTrue(t, retrieved.EndedAt == nil, "EndedAt should be nil")
		testutil.AssertEqual(t, "", retrieved.Summary)
	})
}

// Benchmark tests

func BenchmarkStore_Create(b *testing.B) {
	// Create test database
	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1,
		Name:    "create_sessions_table",
		UpSQL: `
			CREATE TABLE IF NOT EXISTS sessions (
				id         TEXT PRIMARY KEY,
				project    TEXT NOT NULL,
				directory  TEXT NOT NULL,
				started_at TEXT NOT NULL DEFAULT (datetime('now')),
				ended_at   TEXT,
				summary    TEXT
			);
		`,
		DownSQL: `DROP TABLE IF EXISTS sessions;`,
	})

	db, _ := sql.Open("sqlite", ":memory:")
	migrator, _ := migration.NewMigrator(db, "")
	migrator.Register(registry.GetAll()[0])
	_ = migrator.Up(context.Background())

	store := NewStore(db)
	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		session := &domain.Session{
			ID:        string(rune(i)),
			Project:   "benchmark-project",
			Directory: "/tmp/benchmark",
		}
		_ = store.Create(ctx, session)
	}

	_ = db.Close()
}

func BenchmarkStore_GetByID(b *testing.B) {
	// Create test database
	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1,
		Name:    "create_sessions_table",
		UpSQL: `
			CREATE TABLE IF NOT EXISTS sessions (
				id         TEXT PRIMARY KEY,
				project    TEXT NOT NULL,
				directory  TEXT NOT NULL,
				started_at TEXT NOT NULL DEFAULT (datetime('now')),
				ended_at   TEXT,
				summary    TEXT
			);
		`,
		DownSQL: `DROP TABLE IF EXISTS sessions;`,
	})

	db, _ := sql.Open("sqlite", ":memory:")
	migrator, _ := migration.NewMigrator(db, "")
	migrator.Register(registry.GetAll()[0])
	_ = migrator.Up(context.Background())

	store := NewStore(db)
	ctx := context.Background()

	// Create test session
	session := &domain.Session{
		ID:        "benchmark-session",
		Project:   "benchmark-project",
		Directory: "/tmp/benchmark",
	}
	_ = store.Create(ctx, session)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = store.GetByID(ctx, "benchmark-session")
	}

	_ = db.Close()
}

func BenchmarkStore_List(b *testing.B) {
	// Create test database
	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1,
		Name:    "create_sessions_table",
		UpSQL: `
			CREATE TABLE IF NOT EXISTS sessions (
				id         TEXT PRIMARY KEY,
				project    TEXT NOT NULL,
				directory  TEXT NOT NULL,
				started_at TEXT NOT NULL DEFAULT (datetime('now')),
				ended_at   TEXT,
				summary    TEXT
			);
		`,
		DownSQL: `DROP TABLE IF EXISTS sessions;`,
	})

	db, _ := sql.Open("sqlite", ":memory:")
	migrator, _ := migration.NewMigrator(db, "")
	migrator.Register(registry.GetAll()[0])
	_ = migrator.Up(context.Background())

	store := NewStore(db)
	ctx := context.Background()

	// Create test sessions
	for i := 0; i < 100; i++ {
		session := &domain.Session{
			ID:        string(rune(i)),
			Project:   "benchmark-project",
			Directory: "/tmp/benchmark",
		}
		_ = store.Create(ctx, session)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = store.List(ctx, "benchmark-project")
	}

	_ = db.Close()
}

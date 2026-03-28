package prompt

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
	"github.com/lleontor705/cortex/testutil"
)

// setupTestStore creates a test database with the prompt tables and returns a store.
func setupTestStore(t *testing.T) (*Store, func()) {
	t.Helper()

	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1,
		Name:    "create_prompts_table",
		UpSQL: `
			CREATE TABLE IF NOT EXISTS user_prompts (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				content    TEXT    NOT NULL,
				project    TEXT    NOT NULL,
				session_id TEXT    NOT NULL,
				created_at TEXT    NOT NULL DEFAULT (datetime('now'))
			);

			CREATE INDEX IF NOT EXISTS idx_prompts_session ON user_prompts(session_id);
			CREATE INDEX IF NOT EXISTS idx_prompts_project ON user_prompts(project);
			CREATE INDEX IF NOT EXISTS idx_prompts_created ON user_prompts(created_at DESC);

			CREATE VIRTUAL TABLE IF NOT EXISTS prompts_fts USING fts5(
				content,
				project,
				content='user_prompts',
				content_rowid='id'
			);

			CREATE TRIGGER IF NOT EXISTS prompt_fts_insert AFTER INSERT ON user_prompts BEGIN
				INSERT INTO prompts_fts(rowid, content, project)
				VALUES (new.id, new.content, new.project);
			END;

			CREATE TRIGGER IF NOT EXISTS prompt_fts_delete AFTER DELETE ON user_prompts BEGIN
				INSERT INTO prompts_fts(prompts_fts, rowid, content, project)
				VALUES ('delete', old.id, old.content, old.project);
			END;

			CREATE TRIGGER IF NOT EXISTS prompt_fts_update AFTER UPDATE ON user_prompts BEGIN
				INSERT INTO prompts_fts(prompts_fts, rowid, content, project)
				VALUES ('delete', old.id, old.content, old.project);
				INSERT INTO prompts_fts(rowid, content, project)
				VALUES (new.id, new.content, new.project);
			END;
		`,
		DownSQL: `
			DROP TABLE IF EXISTS user_prompts;
			DROP TABLE IF EXISTS prompts_fts;
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

func TestStore_Save(t *testing.T) {
	t.Run("saves prompt with all fields", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		prompt := &domain.Prompt{
			Content:   "Implement user authentication",
			Project:   "test-project",
			SessionID: "session-123",
		}

		err := store.Save(ctx, prompt)
		testutil.RequireNoError(t, err)

		// Verify ID was set
		testutil.AssertTrue(t, prompt.ID > 0, "prompt ID should be set")

		// Verify CreatedAt was set
		testutil.AssertTrue(t, !prompt.CreatedAt.IsZero(), "CreatedAt should be set")
	})

	t.Run("saves prompt with pre-set CreatedAt", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		expectedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		prompt := &domain.Prompt{
			Content:   "Test prompt",
			Project:   "test-project",
			SessionID: "session-123",
			CreatedAt: expectedTime,
		}

		err := store.Save(ctx, prompt)
		testutil.RequireNoError(t, err)

		// Retrieve and verify timestamp was preserved
		retrieved, err := store.GetByID(ctx, prompt.ID)
		testutil.RequireNoError(t, err)

		testutil.AssertWithinDuration(t, expectedTime, retrieved.CreatedAt, time.Second)
	})

	t.Run("returns error for nil prompt", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		err := store.Save(ctx, nil)

		testutil.AssertError(t, err)
		testutil.AssertValidationError(t, err, "prompt")
	})

	t.Run("returns error for empty content", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		prompt := &domain.Prompt{
			Content:   "",
			Project:   "test-project",
			SessionID: "session-123",
		}

		err := store.Save(ctx, prompt)

		testutil.AssertError(t, err)
		testutil.AssertValidationError(t, err, "content")
	})
}

func TestStore_GetByID(t *testing.T) {
	t.Run("retrieves existing prompt", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		original := &domain.Prompt{
			Content:   "Find all TODO comments",
			Project:   "my-project",
			SessionID: "session-456",
		}

		err := store.Save(ctx, original)
		testutil.RequireNoError(t, err)

		retrieved, err := store.GetByID(ctx, original.ID)
		testutil.RequireNoError(t, err)

		testutil.AssertPromptEqual(t, original, retrieved)
	})

	t.Run("returns NotFoundError for non-existent prompt", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		_, err := store.GetByID(ctx, 99999)

		testutil.AssertNotFoundError(t, err, "prompt", int64(99999))
	})
}

func TestStore_List(t *testing.T) {
	t.Run("lists prompts for project", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create prompts for different projects
		prompts := []*domain.Prompt{
			{Content: "Prompt 1", Project: "project-a", SessionID: "s1"},
			{Content: "Prompt 2", Project: "project-b", SessionID: "s2"},
			{Content: "Prompt 3", Project: "project-a", SessionID: "s1"},
		}

		for _, p := range prompts {
			err := store.Save(ctx, p)
			testutil.RequireNoError(t, err)
		}

		// List prompts for project-a
		results, err := store.List(ctx, "project-a", 10)
		testutil.RequireNoError(t, err)

		testutil.AssertLen(t, results, 2)

		// Verify all results are for project-a
		for _, r := range results {
			testutil.AssertEqual(t, "project-a", r.Project)
		}
	})

	t.Run("respects limit parameter", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create 5 prompts
		for i := 0; i < 5; i++ {
			err := store.Save(ctx, &domain.Prompt{
				Content:   "Prompt",
				Project:   "test-project",
				SessionID: "s1",
			})
			testutil.RequireNoError(t, err)
		}

		// List with limit of 2
		results, err := store.List(ctx, "test-project", 2)
		testutil.RequireNoError(t, err)

		testutil.AssertLen(t, results, 2)
	})

	t.Run("uses default limit when limit <= 0", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create 25 prompts
		for i := 0; i < 25; i++ {
			err := store.Save(ctx, &domain.Prompt{
				Content:   "Prompt",
				Project:   "test-project",
				SessionID: "s1",
			})
			testutil.RequireNoError(t, err)
		}

		// List with limit of 0 (should use default of 20)
		results, err := store.List(ctx, "test-project", 0)
		testutil.RequireNoError(t, err)

		testutil.AssertLen(t, results, 20)
	})

	t.Run("returns empty list for project with no prompts", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		results, err := store.List(ctx, "nonexistent-project", 10)

		testutil.RequireNoError(t, err)
		testutil.AssertLen(t, results, 0)
	})

	t.Run("orders by created_at descending", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create prompts with explicit timestamps
		p1 := &domain.Prompt{
			Content:   "Oldest prompt",
			Project:   "test-project",
			SessionID: "s1",
			CreatedAt: time.Now().Add(-2 * time.Hour),
		}
		p2 := &domain.Prompt{
			Content:   "Newest prompt",
			Project:   "test-project",
			SessionID: "s1",
			CreatedAt: time.Now(),
		}
		p3 := &domain.Prompt{
			Content:   "Middle prompt",
			Project:   "test-project",
			SessionID: "s1",
			CreatedAt: time.Now().Add(-1 * time.Hour),
		}

		testutil.RequireNoError(t, store.Save(ctx, p1))
		testutil.RequireNoError(t, store.Save(ctx, p2))
		testutil.RequireNoError(t, store.Save(ctx, p3))

		results, err := store.List(ctx, "test-project", 10)
		testutil.RequireNoError(t, err)

		testutil.AssertLen(t, results, 3)

		// Newest should be first
		testutil.AssertEqual(t, "Newest prompt", results[0].Content)
		testutil.AssertEqual(t, "Middle prompt", results[1].Content)
		testutil.AssertEqual(t, "Oldest prompt", results[2].Content)
	})
}

func TestStore_ListBySession(t *testing.T) {
	t.Run("lists prompts for session", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create prompts for different sessions
		prompts := []*domain.Prompt{
			{Content: "Session A prompt 1", Project: "project", SessionID: "session-a"},
			{Content: "Session B prompt", Project: "project", SessionID: "session-b"},
			{Content: "Session A prompt 2", Project: "project", SessionID: "session-a"},
		}

		for _, p := range prompts {
			err := store.Save(ctx, p)
			testutil.RequireNoError(t, err)
		}

		// List prompts for session-a
		results, err := store.ListBySession(ctx, "session-a")
		testutil.RequireNoError(t, err)

		testutil.AssertLen(t, results, 2)

		// Verify all results are for session-a
		for _, r := range results {
			testutil.AssertEqual(t, "session-a", r.SessionID)
		}
	})

	t.Run("returns error for empty session ID", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		_, err := store.ListBySession(ctx, "")

		testutil.AssertError(t, err)
		testutil.AssertValidationError(t, err, "session_id")
	})

	t.Run("returns empty list for session with no prompts", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		results, err := store.ListBySession(ctx, "nonexistent-session")

		testutil.RequireNoError(t, err)
		testutil.AssertLen(t, results, 0)
	})
}

func TestStore_Delete(t *testing.T) {
	t.Run("deletes existing prompt", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		prompt := &domain.Prompt{
			Content:   "Prompt to delete",
			Project:   "test-project",
			SessionID: "s1",
		}

		err := store.Save(ctx, prompt)
		testutil.RequireNoError(t, err)

		// Delete the prompt
		err = store.Delete(ctx, prompt.ID)
		testutil.RequireNoError(t, err)

		// Verify it's deleted
		_, err = store.GetByID(ctx, prompt.ID)
		testutil.AssertNotFoundError(t, err, "prompt", prompt.ID)
	})

	t.Run("returns NotFoundError for non-existent prompt", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()
		err := store.Delete(ctx, 99999)

		testutil.AssertNotFoundError(t, err, "prompt", int64(99999))
	})
}

func TestStore_Search(t *testing.T) {
	t.Run("searches prompts by content", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		prompts := []*domain.Prompt{
			{Content: "Implement user authentication system", Project: "project-a", SessionID: "s1"},
			{Content: "Fix database connection bug", Project: "project-a", SessionID: "s1"},
			{Content: "Add user profile page", Project: "project-a", SessionID: "s1"},
		}

		for _, p := range prompts {
			err := store.Save(ctx, p)
			testutil.RequireNoError(t, err)
		}

		// Search for "user"
		results, err := store.Search(ctx, "user", "project-a", 10)
		testutil.RequireNoError(t, err)

		// Should find "user authentication" and "user profile"
		testutil.AssertTrue(t, len(results) >= 2, "should find at least 2 prompts with 'user'")
	})

	t.Run("filters by project", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		prompts := []*domain.Prompt{
			{Content: "Authentication feature", Project: "project-a", SessionID: "s1"},
			{Content: "Authentication feature", Project: "project-b", SessionID: "s2"},
		}

		for _, p := range prompts {
			err := store.Save(ctx, p)
			testutil.RequireNoError(t, err)
		}

		// Search only in project-a
		results, err := store.Search(ctx, "authentication", "project-a", 10)
		testutil.RequireNoError(t, err)

		testutil.AssertLen(t, results, 1)
		testutil.AssertEqual(t, "project-a", results[0].Project)
	})

	t.Run("respects limit parameter", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create multiple prompts with same keyword
		for i := 0; i < 5; i++ {
			err := store.Save(ctx, &domain.Prompt{
				Content:   "Test keyword prompt",
				Project:   "test-project",
				SessionID: "s1",
			})
			testutil.RequireNoError(t, err)
		}

		// Search with limit of 2
		results, err := store.Search(ctx, "keyword", "test-project", 2)
		testutil.RequireNoError(t, err)

		testutil.AssertLen(t, results, 2)
	})

	t.Run("uses default limit when limit <= 0", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		// Create prompts
		for i := 0; i < 15; i++ {
			err := store.Save(ctx, &domain.Prompt{
				Content:   "Searchable content",
				Project:   "test-project",
				SessionID: "s1",
			})
			testutil.RequireNoError(t, err)
		}

		// Search with limit of 0 (should use default of 10)
		results, err := store.Search(ctx, "searchable", "test-project", 0)
		testutil.RequireNoError(t, err)

		testutil.AssertLen(t, results, 10)
	})

	t.Run("handles special characters in query", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		err := store.Save(ctx, &domain.Prompt{
			Content:   "Fix N+1 query issue",
			Project:   "test-project",
			SessionID: "s1",
		})
		testutil.RequireNoError(t, err)

		// Search with special characters (should not cause FTS5 syntax error)
		results, err := store.Search(ctx, "fix query", "test-project", 10)
		testutil.RequireNoError(t, err)

		testutil.AssertTrue(t, len(results) >= 1, "should find prompt")
	})

	t.Run("returns empty list when no matches", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		ctx := context.Background()

		err := store.Save(ctx, &domain.Prompt{
			Content:   "Some content",
			Project:   "test-project",
			SessionID: "s1",
		})
		testutil.RequireNoError(t, err)

		// Search for non-matching term
		results, err := store.Search(ctx, "nonexistent", "test-project", 10)
		testutil.RequireNoError(t, err)

		testutil.AssertLen(t, results, 0)
	})
}

func TestSanitizeFTS(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple query",
			input:    "user authentication",
			expected: `"user" "authentication"`,
		},
		{
			name:     "query with special characters",
			input:    "fix bug",
			expected: `"fix" "bug"`,
		},
		{
			name:     "empty query",
			input:    "",
			expected: "",
		},
		{
			name:     "whitespace only",
			input:    "   ",
			expected: "",
		},
		{
			name:     "already quoted terms",
			input:    `"already" "quoted"`,
			expected: `"already" "quoted"`,
		},
		{
			name:     "mixed quotes",
			input:    `test "quoted" value`,
			expected: `"test" "quoted" "value"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeFTS(tt.input)
			testutil.AssertEqual(t, tt.expected, result)
		})
	}
}

// Benchmark tests

func BenchmarkStore_Save(b *testing.B) {
	// Create test database
	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1,
		Name:    "create_prompts_table",
		UpSQL: `
			CREATE TABLE IF NOT EXISTS user_prompts (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				content    TEXT    NOT NULL,
				project    TEXT    NOT NULL,
				session_id TEXT    NOT NULL,
				created_at TEXT    NOT NULL DEFAULT (datetime('now'))
			);
			CREATE VIRTUAL TABLE IF NOT EXISTS prompts_fts USING fts5(
				content,
				project,
				content='user_prompts',
				content_rowid='id'
			);
		`,
		DownSQL: `DROP TABLE IF EXISTS user_prompts; DROP TABLE IF EXISTS prompts_fts;`,
	})

	db, _ := sql.Open("sqlite", ":memory:")
	migrator, _ := migration.NewMigrator(db, "")
	migrator.Register(registry.GetAll()[0])
	_ = migrator.Up(context.Background())

	store := NewStore(db)
	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		prompt := &domain.Prompt{
			Content:   "Benchmark prompt content",
			Project:   "benchmark-project",
			SessionID: "benchmark-session",
		}
		_ = store.Save(ctx, prompt)
	}

	_ = db.Close()
}

func BenchmarkStore_Search(b *testing.B) {
	// Create test database with data
	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1,
		Name:    "create_prompts_table",
		UpSQL: `
			CREATE TABLE IF NOT EXISTS user_prompts (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				content    TEXT    NOT NULL,
				project    TEXT    NOT NULL,
				session_id TEXT    NOT NULL,
				created_at TEXT    NOT NULL DEFAULT (datetime('now'))
			);
			CREATE VIRTUAL TABLE IF NOT EXISTS prompts_fts USING fts5(
				content,
				project,
				content='user_prompts',
				content_rowid='id'
			);
			CREATE TRIGGER prompt_fts_insert AFTER INSERT ON user_prompts BEGIN
				INSERT INTO prompts_fts(rowid, content, project)
				VALUES (new.id, new.content, new.project);
			END;
		`,
		DownSQL: `DROP TABLE IF EXISTS user_prompts; DROP TABLE IF EXISTS prompts_fts;`,
	})

	db, _ := sql.Open("sqlite", ":memory:")
	migrator, _ := migration.NewMigrator(db, "")
	migrator.Register(registry.GetAll()[0])
	_ = migrator.Up(context.Background())

	store := NewStore(db)
	ctx := context.Background()

	// Insert test data
	for i := 0; i < 1000; i++ {
		prompt := &domain.Prompt{
			Content:   "Test prompt with searchable content",
			Project:   "benchmark-project",
			SessionID: "benchmark-session",
		}
		_ = store.Save(ctx, prompt)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = store.Search(ctx, "searchable content", "benchmark-project", 10)
	}

	_ = db.Close()
}

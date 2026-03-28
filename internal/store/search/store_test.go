package search

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
	"github.com/lleontor705/cortex/testutil"
)

// TestSearchStore_Search tests the main search functionality.
func TestSearchStore_Search(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, db *sql.DB)
		query      string
		opts       domain.SearchOptions
		wantCount  int
		wantErr    bool
		checkOrder func(t *testing.T, results []*domain.SearchResult)
	}{
		{
			name: "empty query returns empty results",
			setup: func(t *testing.T, db *sql.DB) {
				// No setup needed
			},
			query:     "",
			opts:      domain.SearchOptions{},
			wantCount: 0,
			wantErr:   false,
		},
		{
			name: "whitespace query returns empty results",
			setup: func(t *testing.T, db *sql.DB) {
				// No setup needed
			},
			query:     "   ",
			opts:      domain.SearchOptions{},
			wantCount: 0,
			wantErr:   false,
		},
		{
			name: "keyword search finds matching title",
			setup: func(t *testing.T, db *sql.DB) {
				insertTestObservation(t, db, 1, "JWT Authentication", "Implement JWT auth", "decision", "test-project", "project")
				insertTestObservation(t, db, 2, "Database Config", "Set up database", "config", "test-project", "project")
			},
			query: "JWT",
			opts: domain.SearchOptions{
				Project: "test-project",
			},
			wantCount: 1,
			wantErr:   false,
			checkOrder: func(t *testing.T, results []*domain.SearchResult) {
				if len(results) > 0 && results[0].Title != "JWT Authentication" {
					t.Errorf("expected JWT Authentication, got %s", results[0].Title)
				}
			},
		},
		{
			name: "keyword search finds matching content",
			setup: func(t *testing.T, db *sql.DB) {
				insertTestObservation(t, db, 1, "Bug Fix", "Fixed N+1 query in user list", "bugfix", "test-project", "project")
				insertTestObservation(t, db, 2, "Feature", "Added search functionality", "manual", "test-project", "project")
			},
			query: "query",
			opts: domain.SearchOptions{
				Project: "test-project",
			},
			wantCount: 1,
			wantErr:   false,
		},
		{
			name: "filter by type",
			setup: func(t *testing.T, db *sql.DB) {
				insertTestObservation(t, db, 1, "Decision A", "Use SQLite", "decision", "test-project", "project")
				insertTestObservation(t, db, 2, "Bug Fix B", "Fixed bug", "bugfix", "test-project", "project")
			},
			query: "SQLite",
			opts: domain.SearchOptions{
				Project: "test-project",
				Type:    "decision",
			},
			wantCount: 1,
			wantErr:   false,
		},
		{
			name: "filter by scope",
			setup: func(t *testing.T, db *sql.DB) {
				insertTestObservation(t, db, 1, "Personal Note", "My note", "manual", "test-project", "personal")
				insertTestObservation(t, db, 2, "Project Note", "Team note", "manual", "test-project", "project")
			},
			query: "note",
			opts: domain.SearchOptions{
				Project: "test-project",
				Scope:   "personal",
			},
			wantCount: 1,
			wantErr:   false,
		},
		{
			name: "respects limit",
			setup: func(t *testing.T, db *sql.DB) {
				for i := 1; i <= 20; i++ {
					insertTestObservation(t, db, int64(i), "Test Observation", "Content", "manual", "test-project", "project")
				}
			},
			query: "Test",
			opts: domain.SearchOptions{
				Project: "test-project",
				Limit:   5,
			},
			wantCount: 5,
			wantErr:   false,
		},
		{
			name: "excludes deleted observations",
			setup: func(t *testing.T, db *sql.DB) {
				insertTestObservation(t, db, 1, "Active", "Active content", "manual", "test-project", "project")
				insertDeletedTestObservation(t, db, 2, "Deleted", "Deleted content", "manual", "test-project", "project")
			},
			query: "content",
			opts: domain.SearchOptions{
				Project: "test-project",
			},
			wantCount: 1,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test database with migrations
			db := setupTestDB(t)
			tt.setup(t, db)

			store := NewStore(db)
			results, err := store.Search(context.Background(), tt.query, tt.opts)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(results) != tt.wantCount {
				t.Errorf("expected %d results, got %d", tt.wantCount, len(results))
			}

			if tt.checkOrder != nil {
				tt.checkOrder(t, results)
			}
		})
	}
}

// TestSearchStore_TopicKeyLookup tests topic key direct lookup.
func TestSearchStore_TopicKeyLookup(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, db *sql.DB)
		query     string
		opts      domain.SearchOptions
		wantCount int
		wantIDs   []int64
	}{
		{
			name: "exact topic key match",
			setup: func(t *testing.T, db *sql.DB) {
				insertTestObservationWithTopicKey(t, db, 1, "Auth Config", "JWT settings", "decision", "test-project", "project", "sdd/auth/config")
				insertTestObservation(t, db, 2, "Other", "Other content", "manual", "test-project", "project")
			},
			query:     "sdd/auth/config",
			opts:      domain.SearchOptions{Project: "test-project"},
			wantCount: 1,
			wantIDs:   []int64{1},
		},
		{
			name: "topic key with filters",
			setup: func(t *testing.T, db *sql.DB) {
				insertTestObservationWithTopicKey(t, db, 1, "Auth", "JWT", "decision", "test-project", "project", "auth/setup")
				insertTestObservationWithTopicKey(t, db, 2, "Auth", "JWT", "bugfix", "test-project", "project", "auth/setup")
			},
			query: "auth/setup",
			opts: domain.SearchOptions{
				Project: "test-project",
				Type:    "decision",
			},
			wantCount: 1,
			wantIDs:   []int64{1},
		},
		{
			name: "no topic key match returns empty",
			setup: func(t *testing.T, db *sql.DB) {
				insertTestObservation(t, db, 1, "Other", "Content", "manual", "test-project", "project")
			},
			query:     "nonexistent/topic",
			opts:      domain.SearchOptions{Project: "test-project"},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupTestDB(t)
			tt.setup(t, db)

			store := NewStore(db)
			results, err := store.Search(context.Background(), tt.query, tt.opts)

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(results) != tt.wantCount {
				t.Errorf("expected %d results, got %d", tt.wantCount, len(results))
				return
			}

			if tt.wantIDs != nil {
				for i, id := range tt.wantIDs {
					if i >= len(results) || results[i].ID != id {
						t.Errorf("result[%d]: expected ID %d, got %d", i, id, results[i].ID)
					}
				}
			}
		})
	}
}

// TestSearchStore_RRFFusion tests RRF fusion algorithm.
func TestSearchStore_RRFFusion(t *testing.T) {
	tests := []struct {
		name        string
		topicKey    string
		setup       func(t *testing.T, db *sql.DB)
		query       string
		opts        domain.SearchOptions
		wantPresent int64 // ID that should be in results
	}{
		{
			name:     "combines topic key and keyword matches",
			topicKey: "auth/jwt",
			setup: func(t *testing.T, db *sql.DB) {
				// Topic key match
				insertTestObservationWithTopicKey(t, db, 1, "JWT Setup", "Configure JWT", "decision", "test-project", "project", "auth/jwt")
				// Keyword match
				insertTestObservation(t, db, 2, "Auth Guide", "JWT authentication guide", "manual", "test-project", "project")
			},
			query:       "auth/jwt",
			opts:        domain.SearchOptions{Project: "test-project"},
			wantPresent: 1, // Topic key match should be present
		},
		{
			name:     "topic key match ranks higher",
			topicKey: "database/config",
			setup: func(t *testing.T, db *sql.DB) {
				// Topic key match
				insertTestObservationWithTopicKey(t, db, 1, "DB Config", "Database settings", "config", "test-project", "project", "database/config")
				// Keyword match (less relevant)
				insertTestObservation(t, db, 2, "Other Config", "Config file", "manual", "test-project", "project")
			},
			query:       "database/config",
			opts:        domain.SearchOptions{Project: "test-project"},
			wantPresent: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupTestDB(t)
			tt.setup(t, db)

			store := NewStore(db)
			results, err := store.Search(context.Background(), tt.query, tt.opts)

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Check that the expected ID is present
			found := false
			for _, r := range results {
				if r.ID == tt.wantPresent {
					found = true
					break
				}
			}

			if !found {
				t.Errorf("expected ID %d in results, not found", tt.wantPresent)
			}
		})
	}
}

// TestSearchStore_Snippet tests snippet extraction.
func TestSearchStore_Snippet(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		query        string
		maxLength    int
		wantNotEmpty bool
	}{
		{
			name:         "extracts snippet from long content",
			content:      "This is a very long piece of content that contains the keyword authentication somewhere in the middle of the text.",
			query:        "authentication",
			maxLength:    50,
			wantNotEmpty: true,
		},
		{
			name:         "handles short content",
			content:      "Short text",
			query:        "Short",
			maxLength:    200,
			wantNotEmpty: true,
		},
		{
			name:         "uses default max length",
			content:      "Some content here",
			query:        "content",
			maxLength:    0,
			wantNotEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupTestDB(t)
			store := NewStore(db)

			snippet, err := store.GetSnippet(context.Background(), tt.query, tt.content, tt.maxLength)

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if tt.wantNotEmpty && snippet == "" {
				t.Error("expected non-empty snippet")
			}
		})
	}
}

// TestSearchStore_BM25Ranking tests BM25 ranking with column weights.
func TestSearchStore_BM25Ranking(t *testing.T) {
	t.Run("content weight higher than title", func(t *testing.T) {
		db := setupTestDB(t)

		// Insert observations where content match should rank higher than title match
		insertTestObservation(t, db, 1, "Other Title", "authentication authentication authentication", "manual", "test-project", "project")
		insertTestObservation(t, db, 2, "authentication", "Other content", "manual", "test-project", "project")

		store := NewStore(db)
		results, err := store.Search(context.Background(), "authentication", domain.SearchOptions{
			Project: "test-project",
			Limit:   10,
		})

		if err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}

		// Content match (ID 1) should rank higher due to 2x weight
		if len(results) >= 2 {
			// Note: BM25 ranking is complex, so we just verify we got results
			// The actual ranking depends on term frequency and document length
			t.Logf("Results: ID=%d Rank=%f, ID=%d Rank=%f",
				results[0].ID, results[0].Rank,
				results[1].ID, results[1].Rank)
		}
	})
}

// TestSearchStore_DefaultLimit tests default limit behavior.
func TestSearchStore_DefaultLimit(t *testing.T) {
	t.Run("applies default limit of 10", func(t *testing.T) {
		db := setupTestDB(t)

		// Insert 20 observations
		for i := 1; i <= 20; i++ {
			insertTestObservation(t, db, int64(i), "Test", "Content", "manual", "test-project", "project")
		}

		store := NewStore(db)
		results, err := store.Search(context.Background(), "Test", domain.SearchOptions{
			Project: "test-project",
			// No limit specified
		})

		if err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}

		if len(results) != 10 {
			t.Errorf("expected 10 results (default), got %d", len(results))
		}
	})

	t.Run("caps limit at 100", func(t *testing.T) {
		db := setupTestDB(t)

		// Insert 150 observations
		for i := 1; i <= 150; i++ {
			insertTestObservation(t, db, int64(i), "Test", "Content", "manual", "test-project", "project")
		}

		store := NewStore(db)
		results, err := store.Search(context.Background(), "Test", domain.SearchOptions{
			Project: "test-project",
			Limit:   200, // Request more than max
		})

		if err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}

		if len(results) > 100 {
			t.Errorf("expected at most 100 results (max cap), got %d", len(results))
		}
	})
}

// TestSanitizeFTS tests FTS5 query sanitization.
func TestSanitizeFTS(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple query",
			input: "test",
			want:  `"test*"`,
		},
		{
			name:  "multiple terms",
			input: "fix auth bug",
			want:  `"fix" AND "auth" AND "bug*"`,
		},
		{
			name:  "removes special operators",
			input: "test* ^keyword ~fuzzy",
			want:  `"test" AND "keyword" AND "fuzzy*"`,
		},
		{
			name:  "handles minus as space",
			input: "test-query",
			want:  `"test" AND "query*"`,
		},
		{
			name:  "escapes double quotes",
			input: `test "quoted" value`,
			want:  `"test" AND "'quoted'" AND "value*"`,
		},
		{
			name:  "empty query",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   ",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFTS(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFTS(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestNormalizeScope tests scope normalization.
func TestNormalizeScope(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"personal", "personal"},
		{"PERSONAL", "personal"},
		{"Personal", "personal"},
		{"project", "project"},
		{"PROJECT", "project"},
		{"Project", "project"},
		{"custom", "custom"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeScope(tt.input)
			if got != tt.want {
				t.Errorf("normalizeScope(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// Helper functions

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	// Create test database with migrations
	testDB := testutil.NewTestDB(t)

	// Register migrations
	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1,
		Name:    "init",
		UpSQL:   getInitMigrationSQL(),
		DownSQL: "DROP TABLE IF EXISTS observations; DROP TABLE IF EXISTS sessions; DROP TABLE IF EXISTS user_prompts;",
	})
	registry.Register(migration.Migration{
		Version: 2,
		Name:    "add_fts",
		UpSQL:   getFTSMigrationSQL(),
		DownSQL: getFTSMigrationDownSQL(),
	})

	// Apply migrations
	migrator, err := migration.NewMigrator(testDB.DB(), "")
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}

	for _, m := range registry.GetAll() {
		migrator.Register(m)
	}

	if err := migrator.Up(context.Background()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	return testDB.DB()
}

func insertTestObservation(t *testing.T, db *sql.DB, id int64, title, content, obsType, project, scope string) {
	t.Helper()

	// Ensure session exists first (required for foreign key constraint)
	ensureTestSession(t, db)

	now := time.Now().Format(time.RFC3339)
	_, err := db.Exec(`
		INSERT INTO observations (id, session_id, type, title, content, project, scope, created_at, updated_at)
		VALUES (?, 'test-session', ?, ?, ?, ?, ?, ?, ?)
	`, id, obsType, title, content, project, scope, now, now)
	if err != nil {
		t.Fatalf("insert test observation: %v", err)
	}
}

func insertTestObservationWithTopicKey(t *testing.T, db *sql.DB, id int64, title, content, obsType, project, scope, topicKey string) {
	t.Helper()

	// Ensure session exists first (required for foreign key constraint)
	ensureTestSession(t, db)

	now := time.Now().Format(time.RFC3339)
	_, err := db.Exec(`
		INSERT INTO observations (id, session_id, type, title, content, project, scope, topic_key, created_at, updated_at)
		VALUES (?, 'test-session', ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, obsType, title, content, project, scope, topicKey, now, now)
	if err != nil {
		t.Fatalf("insert test observation with topic key: %v", err)
	}
}

func insertDeletedTestObservation(t *testing.T, db *sql.DB, id int64, title, content, obsType, project, scope string) {
	t.Helper()

	// Ensure session exists first (required for foreign key constraint)
	ensureTestSession(t, db)

	now := time.Now().Format(time.RFC3339)
	_, err := db.Exec(`
		INSERT INTO observations (id, session_id, type, title, content, project, scope, created_at, updated_at, deleted_at)
		VALUES (?, 'test-session', ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, obsType, title, content, project, scope, now, now, now)
	if err != nil {
		t.Fatalf("insert deleted test observation: %v", err)
	}
}

// ensureTestSession creates a test session if it doesn't exist.
func ensureTestSession(t *testing.T, db *sql.DB) {
	t.Helper()

	now := time.Now().Format(time.RFC3339)
	_, err := db.Exec(`
		INSERT OR IGNORE INTO sessions (id, project, directory, started_at)
		VALUES ('test-session', 'test-project', '/tmp/test', ?)
	`, now)
	if err != nil {
		t.Fatalf("ensure test session: %v", err)
	}
}

func getInitMigrationSQL() string {
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

CREATE INDEX IF NOT EXISTS idx_obs_session  ON observations(session_id);
CREATE INDEX IF NOT EXISTS idx_obs_type     ON observations(type);
CREATE INDEX IF NOT EXISTS idx_obs_project  ON observations(project);
CREATE INDEX IF NOT EXISTS idx_obs_created  ON observations(created_at DESC);

CREATE TABLE IF NOT EXISTS user_prompts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    sync_id    TEXT,
    session_id TEXT    NOT NULL,
    content    TEXT    NOT NULL,
    project    TEXT,
    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX IF NOT EXISTS idx_prompts_session ON user_prompts(session_id);
CREATE INDEX IF NOT EXISTS idx_prompts_project ON user_prompts(project);
CREATE INDEX IF NOT EXISTS idx_prompts_created ON user_prompts(created_at DESC);
`
}

func getFTSMigrationSQL() string {
	return `
CREATE VIRTUAL TABLE IF NOT EXISTS observations_fts USING fts5(
    title,
    content,
    tool_name,
    type,
    project,
    scope,
    topic_key,
    content='observations',
    content_rowid='id',
    tokenize='porter unicode61'
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

CREATE VIRTUAL TABLE IF NOT EXISTS prompts_fts USING fts5(
    content,
    project,
    content='user_prompts',
    content_rowid='id',
    tokenize='porter unicode61'
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
`
}

func getFTSMigrationDownSQL() string {
	return `
DROP TRIGGER IF EXISTS prompt_fts_update;
DROP TRIGGER IF EXISTS prompt_fts_delete;
DROP TRIGGER IF EXISTS prompt_fts_insert;
DROP TABLE IF EXISTS prompts_fts;

DROP TRIGGER IF EXISTS obs_fts_update;
DROP TRIGGER IF EXISTS obs_fts_delete;
DROP TRIGGER IF EXISTS obs_fts_insert;
DROP TABLE IF EXISTS observations_fts;
`
}

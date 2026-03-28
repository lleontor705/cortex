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
				created_at TEXT    NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT    NOT NULL DEFAULT (datetime('now')),
				deleted_at TEXT,
				FOREIGN KEY (session_id) REFERENCES sessions(id)
			);

			CREATE INDEX IF NOT EXISTS idx_obs_session  ON observations(session_id);
			CREATE INDEX IF NOT EXISTS idx_obs_type     ON observations(type);
			CREATE INDEX IF NOT EXISTS idx_obs_project  ON observations(project);
			CREATE INDEX IF NOT EXISTS idx_obs_created  ON observations(created_at DESC);
		`,
		DownSQL: `
			DROP INDEX IF EXISTS idx_obs_created;
			DROP INDEX IF EXISTS idx_obs_project;
			DROP INDEX IF EXISTS idx_obs_type;
			DROP INDEX IF EXISTS idx_obs_session;
			DROP TABLE IF EXISTS observations;
			DROP TABLE IF EXISTS sessions;
		`,
	})

	testDB := testutil.NewTestDBWithMigrations(t, registry)
	store := NewStore(testDB.DB())

	return store, testDB.DB(), func() {
		testDB.Cleanup()
	}
}

// createTestSession creates a test session and returns its ID.
func createTestSession(t *testing.T, db *sql.DB, sessionID, project string) {
	t.Helper()

	_, err := db.Exec(`
		INSERT INTO sessions (id, project, directory, started_at)
		VALUES (?, ?, ?, datetime('now'))
	`, sessionID, project, "/test/dir")
	if err != nil {
		t.Fatalf("failed to create test session: %v", err)
	}
}

// ─── Save Tests ───────────────────────────────────────────────────────────────

func TestStore_Save_Basic(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	fixtures := testutil.NewFixtures()

	// Create session first
	createTestSession(t, db, "session-1", "test-project")

	obs := fixtures.Observation(func(o *domain.Observation) {
		o.SessionID = "session-1"
		o.Title = "First Observation"
		o.Content = "This is the content"
	})

	err := store.Save(ctx, obs)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if obs.ID == 0 {
		t.Error("Save() should set observation ID")
	}

	if obs.CreatedAt.IsZero() {
		t.Error("Save() should set CreatedAt")
	}

	if obs.UpdatedAt.IsZero() {
		t.Error("Save() should set UpdatedAt")
	}
}

func TestStore_Save_ValidationErrors(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	tests := []struct {
		name    string
		obs     *domain.Observation
		wantErr string
	}{
		{
			name:    "nil observation",
			obs:     nil,
			wantErr: "observation cannot be nil",
		},
		{
			name: "empty title",
			obs: &domain.Observation{
				Content: "content",
			},
			wantErr: "title is required",
		},
		{
			name: "empty content",
			obs: &domain.Observation{
				Title: "title",
			},
			wantErr: "content is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.Save(ctx, tt.obs)
			if err == nil {
				t.Fatalf("Save() expected error containing %q, got nil", tt.wantErr)
			}

			var validationErr *domain.ValidationError
			if !domain.IsValidationError(err) {
				t.Errorf("Save() error = %v, want ValidationError", err)
			}

			if validationErr != nil && validationErr.Message != tt.wantErr {
				t.Errorf("Save() error message = %q, want %q", validationErr.Message, tt.wantErr)
			}
		})
	}
}

func TestStore_Save_Defaults(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	obs := &domain.Observation{
		SessionID: "session-1",
		Title:     "Test",
		Content:   "Content",
		// Type and Scope left empty to test defaults
	}

	err := store.Save(ctx, obs)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if obs.Type != domain.TypeManual {
		t.Errorf("Save() Type = %q, want %q", obs.Type, domain.TypeManual)
	}

	if obs.Scope != domain.ScopeProject {
		t.Errorf("Save() Scope = %q, want %q", obs.Scope, domain.ScopeProject)
	}
}

func TestStore_Save_ScopeNormalization(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	tests := []struct {
		input    string
		expected string
	}{
		{"", "project"},
		{"project", "project"},
		{"PROJECT", "project"},
		{"personal", "personal"},
		{"PERSONAL", "personal"},
		{"  personal  ", "personal"},
		{"unknown", "project"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			obs := &domain.Observation{
				SessionID: "session-1",
				Title:     "Test",
				Content:   "Content",
				Scope:     tt.input,
			}

			err := store.Save(ctx, obs)
			if err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			if obs.Scope != tt.expected {
				t.Errorf("Save() Scope = %q, want %q", obs.Scope, tt.expected)
			}
		})
	}
}

// ─── Topic Key Upsert Tests ───────────────────────────────────────────────────

func TestStore_Save_TopicKeyUpsert(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	// Create first observation with topic key
	obs1 := &domain.Observation{
		SessionID: "session-1",
		Title:     "Original Title",
		Content:   "Original Content",
		Project:   "test-project",
		TopicKey:  "architecture/auth",
	}

	err := store.Save(ctx, obs1)
	if err != nil {
		t.Fatalf("Save() first error = %v", err)
	}
	originalID := obs1.ID
	originalCreatedAt := obs1.CreatedAt

	// Wait a bit to ensure different timestamp
	time.Sleep(10 * time.Millisecond)

	// Save with same topic key - should update, not create new
	obs2 := &domain.Observation{
		SessionID: "session-1",
		Title:     "Updated Title",
		Content:   "Updated Content",
		Project:   "test-project",
		TopicKey:  "architecture/auth", // Same topic key
	}

	err = store.Save(ctx, obs2)
	if err != nil {
		t.Fatalf("Save() second error = %v", err)
	}

	// Should have same ID (updated, not new)
	if obs2.ID != originalID {
		t.Errorf("Save() ID = %d, want %d (should update same record)", obs2.ID, originalID)
	}

	// Title should be updated
	if obs2.Title != "Updated Title" {
		t.Errorf("Save() Title = %q, want %q", obs2.Title, "Updated Title")
	}

	// CreatedAt should be preserved
	if !obs2.CreatedAt.Equal(originalCreatedAt) {
		t.Errorf("Save() CreatedAt changed, should be preserved")
	}

	// Verify only one record exists
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM observations WHERE topic_key = ?", "architecture/auth").Scan(&count)
	if err != nil {
		t.Fatalf("count query error = %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 observation with topic_key, got %d", count)
	}
}

func TestStore_Save_TopicKeyNormalization(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	tests := []struct {
		input    string
		expected string
	}{
		{"Architecture/Auth", "architecture/auth"},
		{"  architecture/auth  ", "architecture/auth"},
		{"architecture  auth", "architecture-auth"},
		{"ARCHITECTURE/AUTH", "architecture/auth"},
	}

	for i, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			obs := &domain.Observation{
				SessionID: "session-1",
				Title:     "Test",
				Content:   "Content",
				Project:   "test-project",
				TopicKey:  tt.input,
			}

			err := store.Save(ctx, obs)
			if err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			if obs.TopicKey != tt.expected {
				t.Errorf("Save() TopicKey = %q, want %q", obs.TopicKey, tt.expected)
			}

			// Verify in database
			var dbTopicKey string
			err = db.QueryRow("SELECT topic_key FROM observations WHERE id = ?", obs.ID).Scan(&dbTopicKey)
			if err != nil {
				t.Fatalf("query error = %v", err)
			}
			if dbTopicKey != tt.expected {
				t.Errorf("DB topic_key = %q, want %q", dbTopicKey, tt.expected)
			}

			// Clean up for next test
			db.Exec("DELETE FROM observations WHERE id = ?", obs.ID)
			_ = i // avoid unused variable warning
		})
	}
}

// ─── Deduplication Tests ──────────────────────────────────────────────────────

func TestStore_Save_Deduplication(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	// Create first observation
	obs1 := &domain.Observation{
		SessionID: "session-1",
		Title:     "Same Title",
		Content:   "Same Content",
		Project:   "test-project",
		Type:      domain.TypeManual,
	}

	err := store.Save(ctx, obs1)
	if err != nil {
		t.Fatalf("Save() first error = %v", err)
	}
	originalID := obs1.ID

	// Create duplicate observation (same content, title, type, project)
	obs2 := &domain.Observation{
		SessionID: "session-1",
		Title:     "Same Title",
		Content:   "Same Content",
		Project:   "test-project",
		Type:      domain.TypeManual,
	}

	err = store.Save(ctx, obs2)
	if err != nil {
		t.Fatalf("Save() second error = %v", err)
	}

	// Should have same ID (duplicate detected)
	if obs2.ID != originalID {
		t.Errorf("Save() ID = %d, want %d (should detect duplicate)", obs2.ID, originalID)
	}

	// Verify duplicate_count was incremented
	var dupCount int
	err = db.QueryRow("SELECT duplicate_count FROM observations WHERE id = ?", originalID).Scan(&dupCount)
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	if dupCount != 2 {
		t.Errorf("duplicate_count = %d, want 2", dupCount)
	}
}

func TestStore_Save_DeduplicationNormalizesContent(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	// Create first observation
	obs1 := &domain.Observation{
		SessionID: "session-1",
		Title:     "Test",
		Content:   "Same Content",
		Project:   "test-project",
		Type:      domain.TypeManual,
	}

	err := store.Save(ctx, obs1)
	if err != nil {
		t.Fatalf("Save() first error = %v", err)
	}
	originalID := obs1.ID

	// Create with whitespace variations - should be treated as duplicate
	obs2 := &domain.Observation{
		SessionID: "session-1",
		Title:     "Test",
		Content:   "  Same   Content  ", // Extra whitespace
		Project:   "test-project",
		Type:      domain.TypeManual,
	}

	err = store.Save(ctx, obs2)
	if err != nil {
		t.Fatalf("Save() second error = %v", err)
	}

	// Should have same ID (duplicate detected after normalization)
	if obs2.ID != originalID {
		t.Errorf("Save() ID = %d, want %d (should detect duplicate after normalization)", obs2.ID, originalID)
	}
}

// ─── GetByID Tests ────────────────────────────────────────────────────────────

func TestStore_GetByID_Success(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	// Create observation
	obs := &domain.Observation{
		SessionID: "session-1",
		Title:     "Test Title",
		Content:   "Test Content",
		Project:   "test-project",
		Type:      domain.TypeDecision,
		Scope:     domain.ScopePersonal,
		TopicKey:  "test/topic",
	}

	err := store.Save(ctx, obs)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Retrieve
	got, err := store.GetByID(ctx, obs.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}

	if got.ID != obs.ID {
		t.Errorf("GetByID() ID = %d, want %d", got.ID, obs.ID)
	}
	if got.Title != obs.Title {
		t.Errorf("GetByID() Title = %q, want %q", got.Title, obs.Title)
	}
	if got.Content != obs.Content {
		t.Errorf("GetByID() Content = %q, want %q", got.Content, obs.Content)
	}
	if got.Project != obs.Project {
		t.Errorf("GetByID() Project = %q, want %q", got.Project, obs.Project)
	}
	if got.Type != obs.Type {
		t.Errorf("GetByID() Type = %q, want %q", got.Type, obs.Type)
	}
	if got.Scope != obs.Scope {
		t.Errorf("GetByID() Scope = %q, want %q", got.Scope, obs.Scope)
	}
	if got.TopicKey != obs.TopicKey {
		t.Errorf("GetByID() TopicKey = %q, want %q", got.TopicKey, obs.TopicKey)
	}
}

func TestStore_GetByID_NotFound(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	_, err := store.GetByID(ctx, 99999)
	if err == nil {
		t.Fatal("GetByID() expected error, got nil")
	}

	if !domain.IsNotFoundError(err) {
		t.Errorf("GetByID() error = %v, want NotFoundError", err)
	}
}

func TestStore_GetByID_SoftDeleted(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	// Create and soft delete observation
	obs := &domain.Observation{
		SessionID: "session-1",
		Title:     "Test",
		Content:   "Content",
	}

	err := store.Save(ctx, obs)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	err = store.SoftDelete(ctx, obs.ID)
	if err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}

	// Should not be retrievable
	_, err = store.GetByID(ctx, obs.ID)
	if err == nil {
		t.Fatal("GetByID() expected error for soft-deleted, got nil")
	}

	if !domain.IsNotFoundError(err) {
		t.Errorf("GetByID() error = %v, want NotFoundError", err)
	}
}

// ─── GetByTopicKey Tests ──────────────────────────────────────────────────────

func TestStore_GetByTopicKey_Success(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	obs := &domain.Observation{
		SessionID: "session-1",
		Title:     "Test",
		Content:   "Content",
		Project:   "test-project",
		TopicKey:  "architecture/auth",
	}

	err := store.Save(ctx, obs)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := store.GetByTopicKey(ctx, "test-project", "architecture/auth")
	if err != nil {
		t.Fatalf("GetByTopicKey() error = %v", err)
	}

	if got.ID != obs.ID {
		t.Errorf("GetByTopicKey() ID = %d, want %d", got.ID, obs.ID)
	}
}

func TestStore_GetByTopicKey_NotFound(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	_, err := store.GetByTopicKey(ctx, "test-project", "nonexistent/topic")
	if err == nil {
		t.Fatal("GetByTopicKey() expected error, got nil")
	}

	if !domain.IsNotFoundError(err) {
		t.Errorf("GetByTopicKey() error = %v, want NotFoundError", err)
	}
}

func TestStore_GetByTopicKey_EmptyTopicKey(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	_, err := store.GetByTopicKey(ctx, "test-project", "")
	if err == nil {
		t.Fatal("GetByTopicKey() expected error for empty topic_key, got nil")
	}

	if !domain.IsValidationError(err) {
		t.Errorf("GetByTopicKey() error = %v, want ValidationError", err)
	}
}

// ─── Update Tests ─────────────────────────────────────────────────────────────

func TestStore_Update_Success(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	// Create observation
	obs := &domain.Observation{
		SessionID: "session-1",
		Title:     "Original Title",
		Content:   "Original Content",
		Project:   "test-project",
	}

	err := store.Save(ctx, obs)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	originalCreatedAt := obs.CreatedAt

	// Wait to ensure different timestamp
	time.Sleep(10 * time.Millisecond)

	// Update
	obs.Title = "Updated Title"
	obs.Content = "Updated Content"
	obs.Project = "updated-project"
	obs.Type = domain.TypeBugfix
	obs.Scope = domain.ScopePersonal
	obs.TopicKey = "updated/topic"

	err = store.Update(ctx, obs)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Verify update
	got, err := store.GetByID(ctx, obs.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}

	if got.Title != "Updated Title" {
		t.Errorf("Update() Title = %q, want %q", got.Title, "Updated Title")
	}
	if got.Content != "Updated Content" {
		t.Errorf("Update() Content = %q, want %q", got.Content, "Updated Content")
	}
	if got.Project != "updated-project" {
		t.Errorf("Update() Project = %q, want %q", got.Project, "updated-project")
	}
	if got.Type != domain.TypeBugfix {
		t.Errorf("Update() Type = %q, want %q", got.Type, domain.TypeBugfix)
	}
	if got.Scope != domain.ScopePersonal {
		t.Errorf("Update() Scope = %q, want %q", got.Scope, domain.ScopePersonal)
	}
	if got.TopicKey != "updated/topic" {
		t.Errorf("Update() TopicKey = %q, want %q", got.TopicKey, "updated/topic")
	}

	// CreatedAt should be preserved
	if !got.CreatedAt.Equal(originalCreatedAt) {
		t.Error("Update() should preserve CreatedAt")
	}

	// UpdatedAt should be newer
	if !got.UpdatedAt.After(originalCreatedAt) {
		t.Error("Update() should set UpdatedAt to newer time")
	}

	// Revision count should be incremented
	var revisionCount int
	err = db.QueryRow("SELECT revision_count FROM observations WHERE id = ?", obs.ID).Scan(&revisionCount)
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	if revisionCount != 2 {
		t.Errorf("revision_count = %d, want 2", revisionCount)
	}
}

func TestStore_Update_NotFound(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	obs := &domain.Observation{
		ID:      99999,
		Title:   "Test",
		Content: "Content",
	}

	err := store.Update(ctx, obs)
	if err == nil {
		t.Fatal("Update() expected error, got nil")
	}

	if !domain.IsNotFoundError(err) {
		t.Errorf("Update() error = %v, want NotFoundError", err)
	}
}

func TestStore_Update_ValidationErrors(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	// Create valid observation first
	obs := &domain.Observation{
		SessionID: "session-1",
		Title:     "Test",
		Content:   "Content",
	}
	store.Save(ctx, obs)

	tests := []struct {
		name    string
		obs     *domain.Observation
		wantErr bool
	}{
		{
			name:    "nil observation",
			obs:     nil,
			wantErr: true,
		},
		{
			name: "empty title",
			obs: &domain.Observation{
				ID:      obs.ID,
				Content: "content",
			},
			wantErr: true,
		},
		{
			name: "empty content",
			obs: &domain.Observation{
				ID:    obs.ID,
				Title: "title",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.Update(ctx, tt.obs)
			if (err != nil) != tt.wantErr {
				t.Errorf("Update() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// ─── Delete Tests ─────────────────────────────────────────────────────────────

func TestStore_SoftDelete_Success(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	obs := &domain.Observation{
		SessionID: "session-1",
		Title:     "Test",
		Content:   "Content",
	}

	err := store.Save(ctx, obs)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	err = store.SoftDelete(ctx, obs.ID)
	if err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}

	// Verify deleted_at is set
	var deletedAt sql.NullString
	err = db.QueryRow("SELECT deleted_at FROM observations WHERE id = ?", obs.ID).Scan(&deletedAt)
	if err != nil {
		t.Fatalf("query error = %v", err)
	}

	if !deletedAt.Valid {
		t.Error("SoftDelete() should set deleted_at")
	}

	// Record should still exist
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM observations WHERE id = ?", obs.ID).Scan(&count)
	if err != nil {
		t.Fatalf("count query error = %v", err)
	}
	if count != 1 {
		t.Error("SoftDelete() should not remove record from database")
	}
}

func TestStore_SoftDelete_NotFound(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	err := store.SoftDelete(ctx, 99999)
	if err == nil {
		t.Fatal("SoftDelete() expected error, got nil")
	}

	if !domain.IsNotFoundError(err) {
		t.Errorf("SoftDelete() error = %v, want NotFoundError", err)
	}
}

func TestStore_HardDelete_Success(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	obs := &domain.Observation{
		SessionID: "session-1",
		Title:     "Test",
		Content:   "Content",
	}

	err := store.Save(ctx, obs)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	err = store.HardDelete(ctx, obs.ID)
	if err != nil {
		t.Fatalf("HardDelete() error = %v", err)
	}

	// Verify record is gone
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM observations WHERE id = ?", obs.ID).Scan(&count)
	if err != nil {
		t.Fatalf("count query error = %v", err)
	}
	if count != 0 {
		t.Error("HardDelete() should remove record from database")
	}
}

func TestStore_HardDelete_NotFound(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	err := store.HardDelete(ctx, 99999)
	if err == nil {
		t.Fatal("HardDelete() expected error, got nil")
	}

	if !domain.IsNotFoundError(err) {
		t.Errorf("HardDelete() error = %v, want NotFoundError", err)
	}
}

func TestStore_Delete_DefaultSoftDelete(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	obs := &domain.Observation{
		SessionID: "session-1",
		Title:     "Test",
		Content:   "Content",
	}

	err := store.Save(ctx, obs)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Delete should perform soft delete by default
	err = store.Delete(ctx, obs.ID)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify deleted_at is set (soft delete)
	var deletedAt sql.NullString
	err = db.QueryRow("SELECT deleted_at FROM observations WHERE id = ?", obs.ID).Scan(&deletedAt)
	if err != nil {
		t.Fatalf("query error = %v", err)
	}

	if !deletedAt.Valid {
		t.Error("Delete() should perform soft delete by default")
	}
}

// ─── List Tests ───────────────────────────────────────────────────────────────

func TestStore_List_All(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	// Create multiple observations
	for i := 0; i < 5; i++ {
		obs := &domain.Observation{
			SessionID: "session-1",
			Title:     "Test",
			Content:   "Content",
			Project:   "test-project",
		}
		if err := store.Save(ctx, obs); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		time.Sleep(1 * time.Millisecond) // Ensure different timestamps
	}

	observations, err := store.List(ctx, domain.ObservationFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(observations) != 5 {
		t.Errorf("List() returned %d observations, want 5", len(observations))
	}
}

func TestStore_List_FilterByProject(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "project-a")
	createTestSession(t, db, "session-2", "project-b")

	// Create observations in different projects
	for _, project := range []string{"project-a", "project-b"} {
		obs := &domain.Observation{
			SessionID: "session-1",
			Title:     "Test",
			Content:   "Content",
			Project:   project,
		}
		if project == "project-b" {
			obs.SessionID = "session-2"
		}
		if err := store.Save(ctx, obs); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	observations, err := store.List(ctx, domain.ObservationFilter{
		Project: "project-a",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(observations) != 1 {
		t.Errorf("List() returned %d observations, want 1", len(observations))
	}
	if observations[0].Project != "project-a" {
		t.Errorf("List() Project = %q, want %q", observations[0].Project, "project-a")
	}
}

func TestStore_List_FilterByScope(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	// Create observations with different scopes
	for _, scope := range []string{"project", "personal"} {
		obs := &domain.Observation{
			SessionID: "session-1",
			Title:     "Test",
			Content:   "Content",
			Project:   "test-project",
			Scope:     scope,
		}
		if err := store.Save(ctx, obs); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	observations, err := store.List(ctx, domain.ObservationFilter{
		Scope: "personal",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(observations) != 1 {
		t.Errorf("List() returned %d observations, want 1", len(observations))
	}
	if observations[0].Scope != "personal" {
		t.Errorf("List() Scope = %q, want %q", observations[0].Scope, "personal")
	}
}

func TestStore_List_FilterByType(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	// Create observations with different types
	types := []string{domain.TypeManual, domain.TypeBugfix, domain.TypeDecision}
	for _, typ := range types {
		obs := &domain.Observation{
			SessionID: "session-1",
			Title:     "Test",
			Content:   "Content",
			Project:   "test-project",
			Type:      typ,
		}
		if err := store.Save(ctx, obs); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	observations, err := store.List(ctx, domain.ObservationFilter{
		Type:  domain.TypeBugfix,
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(observations) != 1 {
		t.Errorf("List() returned %d observations, want 1", len(observations))
	}
	if observations[0].Type != domain.TypeBugfix {
		t.Errorf("List() Type = %q, want %q", observations[0].Type, domain.TypeBugfix)
	}
}

func TestStore_List_Pagination(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	// Create 10 observations
	for i := 0; i < 10; i++ {
		obs := &domain.Observation{
			SessionID: "session-1",
			Title:     "Test",
			Content:   "Content",
			Project:   "test-project",
		}
		if err := store.Save(ctx, obs); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		time.Sleep(1 * time.Millisecond)
	}

	// Test limit
	observations, err := store.List(ctx, domain.ObservationFilter{Limit: 5})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(observations) != 5 {
		t.Errorf("List() with limit returned %d observations, want 5", len(observations))
	}

	// Test offset
	observations, err = store.List(ctx, domain.ObservationFilter{Limit: 5, Offset: 5})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(observations) != 5 {
		t.Errorf("List() with offset returned %d observations, want 5", len(observations))
	}
}

func TestStore_List_DefaultLimit(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	// Create 30 observations
	for i := 0; i < 30; i++ {
		obs := &domain.Observation{
			SessionID: "session-1",
			Title:     "Test",
			Content:   "Content",
			Project:   "test-project",
		}
		if err := store.Save(ctx, obs); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		time.Sleep(1 * time.Millisecond)
	}

	// List without limit should use default of 20
	observations, err := store.List(ctx, domain.ObservationFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(observations) != 20 {
		t.Errorf("List() without limit returned %d observations, want 20 (default)", len(observations))
	}
}

func TestStore_List_ExcludesSoftDeleted(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	// Create observation
	obs := &domain.Observation{
		SessionID: "session-1",
		Title:     "Test",
		Content:   "Content",
		Project:   "test-project",
	}
	if err := store.Save(ctx, obs); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Soft delete it
	if err := store.SoftDelete(ctx, obs.ID); err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}

	// List should not include soft-deleted
	observations, err := store.List(ctx, domain.ObservationFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(observations) != 0 {
		t.Errorf("List() returned %d observations, want 0 (soft-deleted excluded)", len(observations))
	}
}

// ─── Stats Tests ──────────────────────────────────────────────────────────────

func TestStore_Stats_Basic(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "project-a")
	createTestSession(t, db, "session-2", "project-b")

	// Create observations in different projects with different types
	testCases := []struct {
		project string
		typ     string
	}{
		{"project-a", domain.TypeManual},
		{"project-a", domain.TypeBugfix},
		{"project-b", domain.TypeManual},
		{"project-b", domain.TypeDecision},
		{"project-b", domain.TypeDecision},
	}

	for i, tc := range testCases {
		obs := &domain.Observation{
			SessionID: "session-1",
			Title:     "Test",
			Content:   "Content",
			Project:   tc.project,
			Type:      tc.typ,
		}
		if tc.project == "project-b" {
			obs.SessionID = "session-2"
		}
		if err := store.Save(ctx, obs); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		_ = i // avoid unused variable
	}

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}

	if stats.TotalObservations != 5 {
		t.Errorf("Stats() TotalObservations = %d, want 5", stats.TotalObservations)
	}

	if len(stats.Projects) != 2 {
		t.Errorf("Stats() Projects count = %d, want 2", len(stats.Projects))
	}

	if stats.ByType[domain.TypeManual] != 2 {
		t.Errorf("Stats() ByType[manual] = %d, want 2", stats.ByType[domain.TypeManual])
	}

	if stats.ByType[domain.TypeDecision] != 2 {
		t.Errorf("Stats() ByType[decision] = %d, want 2", stats.ByType[domain.TypeDecision])
	}

	if stats.ByType[domain.TypeBugfix] != 1 {
		t.Errorf("Stats() ByType[bugfix] = %d, want 1", stats.ByType[domain.TypeBugfix])
	}
}

func TestStore_Stats_ExcludesSoftDeleted(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	createTestSession(t, db, "session-1", "test-project")

	// Create and soft delete observation
	obs := &domain.Observation{
		SessionID: "session-1",
		Title:     "Test",
		Content:   "Content",
		Project:   "test-project",
	}
	if err := store.Save(ctx, obs); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.SoftDelete(ctx, obs.ID); err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}

	if stats.TotalObservations != 0 {
		t.Errorf("Stats() TotalObservations = %d, want 0 (soft-deleted excluded)", stats.TotalObservations)
	}
}

func TestStore_Stats_Empty(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}

	if stats.TotalObservations != 0 {
		t.Errorf("Stats() TotalObservations = %d, want 0", stats.TotalObservations)
	}

	if len(stats.Projects) != 0 {
		t.Errorf("Stats() Projects count = %d, want 0", len(stats.Projects))
	}

	if len(stats.ByType) != 0 {
		t.Errorf("Stats() ByType count = %d, want 0", len(stats.ByType))
	}
}

// ─── Helper Function Tests ────────────────────────────────────────────────────

func TestNormalizeScope(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "project"},
		{"project", "project"},
		{"PROJECT", "project"},
		{"personal", "personal"},
		{"PERSONAL", "personal"},
		{"  personal  ", "personal"},
		{"unknown", "project"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeScope(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeScope(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestNormalizeTopicKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"Architecture/Auth", "architecture/auth"},
		{"  architecture/auth  ", "architecture/auth"},
		{"architecture  auth", "architecture-auth"},
		{"ARCHITECTURE/AUTH", "architecture/auth"},
		{"a b c d e", "a-b-c-d-e"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeTopicKey(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeTopicKey(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestHashNormalized(t *testing.T) {
	// Same content should produce same hash
	hash1 := hashNormalized("Same Content")
	hash2 := hashNormalized("Same Content")
	if hash1 != hash2 {
		t.Error("hashNormalized() should produce same hash for same content")
	}

	// Normalized content should produce same hash
	hash1 = hashNormalized("Same Content")
	hash2 = hashNormalized("  same   content  ")
	if hash1 != hash2 {
		t.Error("hashNormalized() should produce same hash for normalized content")
	}

	// Different content should produce different hash
	hash1 = hashNormalized("Content A")
	hash2 = hashNormalized("Content B")
	if hash1 == hash2 {
		t.Error("hashNormalized() should produce different hashes for different content")
	}

	// Hash should be hex string
	if len(hash1) != 64 { // SHA-256 produces 64 hex characters
		t.Errorf("hashNormalized() hash length = %d, want 64", len(hash1))
	}
}

func TestNullableString(t *testing.T) {
	tests := []struct {
		input string
		isNil bool
	}{
		{"", true},
		{"value", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := nullableString(tt.input)
			isNil := result == nil
			if isNil != tt.isNil {
				t.Errorf("nullableString(%q) isNil = %v, want %v", tt.input, isNil, tt.isNil)
			}
		})
	}
}

// Package migration provides a database migration framework for SQLite.
package migration

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// testDB creates an in-memory SQLite database for testing.
func testDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

// TestNewMigrator tests migrator creation.
func TestNewMigrator(t *testing.T) {
	db := testDB(t)

	m, err := NewMigrator(db, "")
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}

	if m == nil {
		t.Fatal("NewMigrator() returned nil")
	}

	// Verify _migrations table was created
	var name string
	err = db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='_migrations'",
	).Scan(&name)
	if err != nil {
		t.Errorf("_migrations table not created: %v", err)
	}
}

// TestNewMigrator_NilDB tests migrator creation with nil database.
func TestNewMigrator_NilDB(t *testing.T) {
	_, err := NewMigrator(nil, "")
	if err == nil {
		t.Fatal("NewMigrator() should fail with nil database")
	}
}

// TestRegister tests programmatic migration registration.
func TestRegister(t *testing.T) {
	db := testDB(t)
	m, err := NewMigrator(db, "")
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}

	migration := Migration{
		Version:     1,
		Name:        "test",
		Description: "Test migration",
		UpSQL:       "CREATE TABLE test (id INTEGER PRIMARY KEY);",
		DownSQL:     "DROP TABLE IF EXISTS test;",
	}

	m.Register(migration)

	// Verify migration was registered
	migrations, err := m.getAllMigrations()
	if err != nil {
		t.Fatalf("getAllMigrations() error = %v", err)
	}

	if len(migrations) != 1 {
		t.Errorf("expected 1 migration, got %d", len(migrations))
	}

	if migrations[0].Version != 1 {
		t.Errorf("expected version 1, got %d", migrations[0].Version)
	}
}

// TestUp tests applying migrations.
func TestUp(t *testing.T) {
	db := testDB(t)
	m, err := NewMigrator(db, "")
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}

	// Register migrations
	m.Register(Migration{
		Version: 1,
		Name:    "create_users",
		UpSQL:   "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);",
		DownSQL: "DROP TABLE IF EXISTS users;",
	})

	m.Register(Migration{
		Version: 2,
		Name:    "create_posts",
		UpSQL:   "CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER, title TEXT);",
		DownSQL: "DROP TABLE IF EXISTS posts;",
	})

	// Apply migrations
	ctx := context.Background()
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	// Verify migrations were applied
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM _migrations").Scan(&count)
	if err != nil {
		t.Fatalf("count migrations: %v", err)
	}

	if count != 2 {
		t.Errorf("expected 2 applied migrations, got %d", count)
	}

	// Verify tables were created
	var name string
	err = db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='users'",
	).Scan(&name)
	if err != nil {
		t.Error("users table not created")
	}

	err = db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='posts'",
	).Scan(&name)
	if err != nil {
		t.Error("posts table not created")
	}

	// Verify version tracking
	if m.Version() != 2 {
		t.Errorf("expected version 2, got %d", m.Version())
	}
}

// TestUp_NoPending tests Up with no pending migrations.
func TestUp_NoPending(t *testing.T) {
	db := testDB(t)
	m, err := NewMigrator(db, "")
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}

	ctx := context.Background()
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
}

// TestDown tests rolling back migrations.
func TestDown(t *testing.T) {
	db := testDB(t)
	m, err := NewMigrator(db, "")
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}

	// Register and apply migrations
	m.Register(Migration{
		Version: 1,
		Name:    "create_users",
		UpSQL:   "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);",
		DownSQL: "DROP TABLE IF EXISTS users;",
	})

	m.Register(Migration{
		Version: 2,
		Name:    "create_posts",
		UpSQL:   "CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER, title TEXT);",
		DownSQL: "DROP TABLE IF EXISTS posts;",
	})

	ctx := context.Background()
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	// Rollback to version 1
	if err := m.Down(ctx, 1); err != nil {
		t.Fatalf("Down(1) error = %v", err)
	}

	// Verify posts table was dropped
	var name string
	err = db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='posts'",
	).Scan(&name)
	if err == nil {
		t.Error("posts table should have been dropped")
	}

	// Verify users table still exists
	err = db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='users'",
	).Scan(&name)
	if err != nil {
		t.Error("users table should still exist")
	}

	// Verify version tracking
	if m.Version() != 1 {
		t.Errorf("expected version 1, got %d", m.Version())
	}
}

// TestDown_All tests rolling back all migrations.
func TestDown_All(t *testing.T) {
	db := testDB(t)
	m, err := NewMigrator(db, "")
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}

	// Register and apply migrations
	m.Register(Migration{
		Version: 1,
		Name:    "create_users",
		UpSQL:   "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);",
		DownSQL: "DROP TABLE IF EXISTS users;",
	})

	ctx := context.Background()
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	// Rollback all (version 0)
	if err := m.Down(ctx, 0); err != nil {
		t.Fatalf("Down(0) error = %v", err)
	}

	// Verify users table was dropped
	var name string
	err = db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='users'",
	).Scan(&name)
	if err == nil {
		t.Error("users table should have been dropped")
	}

	// Verify version tracking
	if m.Version() != 0 {
		t.Errorf("expected version 0, got %d", m.Version())
	}
}

// TestStatus tests migration status reporting.
func TestStatus(t *testing.T) {
	db := testDB(t)
	m, err := NewMigrator(db, "")
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}

	// Register migrations
	m.Register(Migration{
		Version: 1,
		Name:    "create_users",
		UpSQL:   "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);",
		DownSQL: "DROP TABLE IF EXISTS users;",
	})

	m.Register(Migration{
		Version: 2,
		Name:    "create_posts",
		UpSQL:   "CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER, title TEXT);",
		DownSQL: "DROP TABLE IF EXISTS posts;",
	})

	// Apply first migration only
	ctx := context.Background()
	migrations, err := m.getPendingMigrations()
	if err != nil {
		t.Fatalf("getPendingMigrations() error = %v", err)
	}

	if len(migrations) > 0 {
		if err := m.applyMigration(ctx, migrations[0]); err != nil {
			t.Fatalf("applyMigration() error = %v", err)
		}
	}

	// Get status
	statuses, err := m.Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	if len(statuses) != 2 {
		t.Errorf("expected 2 statuses, got %d", len(statuses))
	}

	// Verify first migration is applied
	if !statuses[0].Applied {
		t.Error("first migration should be applied")
	}

	// Verify second migration is pending
	if statuses[1].Applied {
		t.Error("second migration should be pending")
	}

	// Verify applied_at timestamp for first migration
	if statuses[0].AppliedAt == "" {
		t.Error("first migration should have applied_at timestamp")
	}

	// Verify no applied_at timestamp for second migration
	if statuses[1].AppliedAt != "" {
		t.Error("second migration should not have applied_at timestamp")
	}
}

// TestLoadMigrationsFromDisk tests loading migrations from filesystem.
func TestLoadMigrationsFromDisk(t *testing.T) {
	// Create temporary directory
	tmpDir := t.TempDir()

	// Create test migration file
	migrationContent := `-- +migrate Up
CREATE TABLE test_table (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL
);

-- +migrate Down
DROP TABLE IF EXISTS test_table;
`
	migrationPath := filepath.Join(tmpDir, "001_create_test_table.sql")
	if err := os.WriteFile(migrationPath, []byte(migrationContent), 0644); err != nil {
		t.Fatalf("write migration file: %v", err)
	}

	// Create migrator
	db := testDB(t)
	m, err := NewMigrator(db, tmpDir)
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}

	// Get migrations
	migrations, err := m.getAllMigrations()
	if err != nil {
		t.Fatalf("getAllMigrations() error = %v", err)
	}

	if len(migrations) != 1 {
		t.Fatalf("expected 1 migration, got %d", len(migrations))
	}

	// Verify migration properties
	if migrations[0].Version != 1 {
		t.Errorf("expected version 1, got %d", migrations[0].Version)
	}

	if migrations[0].Name != "create_test_table" {
		t.Errorf("expected name 'create_test_table', got %s", migrations[0].Name)
	}

	if migrations[0].UpSQL == "" {
		t.Error("UpSQL should not be empty")
	}

	if migrations[0].DownSQL == "" {
		t.Error("DownSQL should not be empty")
	}

	// Apply migration
	ctx := context.Background()
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	// Verify table was created
	var name string
	err = db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='test_table'",
	).Scan(&name)
	if err != nil {
		t.Error("test_table not created")
	}
}

// TestParseMigrationFilename tests filename parsing.
func TestParseMigrationFilename(t *testing.T) {
	tests := []struct {
		filename    string
		wantVersion int
		wantName    string
		wantErr     bool
	}{
		{"001_init.sql", 1, "init", false},
		{"002_create_users.sql", 2, "create_users", false},
		{"010_add_index.sql", 10, "add_index", false},
		{"invalid.sql", 0, "", true},
		{"no_underscore.sql", 0, "", true},
		{"abc_invalid.sql", 0, "", true},
	}

	for _, tt := range tests {
		version, name, err := parseMigrationFilename(tt.filename)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseMigrationFilename(%q) error = %v, wantErr %v", tt.filename, err, tt.wantErr)
			continue
		}

		if !tt.wantErr {
			if version != tt.wantVersion {
				t.Errorf("parseMigrationFilename(%q) version = %d, want %d", tt.filename, version, tt.wantVersion)
			}

			if name != tt.wantName {
				t.Errorf("parseMigrationFilename(%q) name = %q, want %q", tt.filename, name, tt.wantName)
			}
		}
	}
}

// TestParseMigrationContent tests content parsing.
func TestParseMigrationContent(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantUp   string
		wantDown string
		wantErr  bool
	}{
		{
			name: "valid with both sections",
			content: `-- +migrate Up
CREATE TABLE users (id INTEGER);

-- +migrate Down
DROP TABLE users;`,
			wantUp:   "CREATE TABLE users (id INTEGER);",
			wantDown: "DROP TABLE users;",
			wantErr:  false,
		},
		{
			name: "valid with only up section",
			content: `-- +migrate Up
CREATE TABLE users (id INTEGER);`,
			wantUp:   "CREATE TABLE users (id INTEGER);",
			wantDown: "",
			wantErr:  false,
		},
		{
			name: "missing up marker",
			content: `CREATE TABLE users (id INTEGER);
-- +migrate Down
DROP TABLE users;`,
			wantUp:   "",
			wantDown: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upSQL, downSQL, err := parseMigrationContent(tt.content)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseMigrationContent() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if upSQL != tt.wantUp {
					t.Errorf("parseMigrationContent() upSQL = %q, want %q", upSQL, tt.wantUp)
				}

				if downSQL != tt.wantDown {
					t.Errorf("parseMigrationContent() downSQL = %q, want %q", downSQL, tt.wantDown)
				}
			}
		})
	}
}

// TestTransactionSafety tests that migrations are applied in transactions.
func TestTransactionSafety(t *testing.T) {
	db := testDB(t)
	m, err := NewMigrator(db, "")
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}

	// Register migration with error
	m.Register(Migration{
		Version: 1,
		Name:    "failing_migration",
		UpSQL: `CREATE TABLE test (id INTEGER PRIMARY KEY);
INSERT INTO nonexistent_table VALUES (1);`, // This will fail
		DownSQL: "DROP TABLE IF EXISTS test;",
	})

	// Try to apply migration
	ctx := context.Background()
	err = m.Up(ctx)
	if err == nil {
		t.Fatal("Up() should fail with invalid SQL")
	}

	// Verify test table was NOT created (transaction rolled back)
	var name string
	err = db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='test'",
	).Scan(&name)
	if err == nil {
		t.Error("test table should not exist after failed migration")
	}

	// Verify migration was not recorded
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM _migrations").Scan(&count)
	if err != nil {
		t.Fatalf("count migrations: %v", err)
	}

	if count != 0 {
		t.Errorf("expected 0 applied migrations, got %d", count)
	}
}

// TestRegistry tests the registry functionality.
func TestRegistry(t *testing.T) {
	r := NewRegistry()

	// Test empty registry
	if r.Count() != 0 {
		t.Error("new registry should be empty")
	}

	// Test register
	migration := Migration{
		Version: 1,
		Name:    "test",
		UpSQL:   "SELECT 1;",
	}
	r.Register(migration)

	if r.Count() != 1 {
		t.Error("registry should have 1 migration")
	}

	// Test get
	m, exists := r.Get(1)
	if !exists {
		t.Fatal("migration should exist")
	}
	if m.Version != 1 {
		t.Errorf("expected version 1, got %d", m.Version)
	}

	// Test get non-existent
	_, exists = r.Get(999)
	if exists {
		t.Error("non-existent migration should not exist")
	}

	// Test GetAll
	migrations := r.GetAll()
	if len(migrations) != 1 {
		t.Errorf("expected 1 migration, got %d", len(migrations))
	}

	// Test clear
	r.Clear()
	if r.Count() != 0 {
		t.Error("registry should be empty after clear")
	}
}

// TestConcurrentAccess tests concurrent migration access.
func TestConcurrentAccess(t *testing.T) {
	db := testDB(t)
	m, err := NewMigrator(db, "")
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}

	// Register migrations
	for i := 1; i <= 10; i++ {
		m.Register(Migration{
			Version: i,
			Name:    "migration",
			UpSQL:   "CREATE TABLE IF NOT EXISTS test (id INTEGER);",
			DownSQL: "DROP TABLE IF EXISTS test;",
		})
	}

	// Apply migrations
	ctx := context.Background()
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	// Concurrent status checks
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			_, err := m.Status(ctx)
			if err != nil {
				t.Errorf("Status() error: %v", err)
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestMigrationOrder tests that migrations are applied in correct order.
func TestMigrationOrder(t *testing.T) {
	db := testDB(t)
	m, err := NewMigrator(db, "")
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}

	// Register migrations out of order
	m.Register(Migration{
		Version: 3,
		Name:    "third",
		UpSQL:   "CREATE TABLE third (id INTEGER);",
		DownSQL: "DROP TABLE IF EXISTS third;",
	})

	m.Register(Migration{
		Version: 1,
		Name:    "first",
		UpSQL:   "CREATE TABLE first (id INTEGER);",
		DownSQL: "DROP TABLE IF EXISTS first;",
	})

	m.Register(Migration{
		Version: 2,
		Name:    "second",
		UpSQL:   "CREATE TABLE second (id INTEGER);",
		DownSQL: "DROP TABLE IF EXISTS second;",
	})

	// Apply migrations
	ctx := context.Background()
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	// Verify migrations were applied in order
	rows, err := db.Query("SELECT version FROM _migrations ORDER BY applied_at")
	if err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var versions []int
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan version: %v", err)
		}
		versions = append(versions, version)
	}

	expected := []int{1, 2, 3}
	if len(versions) != len(expected) {
		t.Fatalf("expected %d versions, got %d", len(expected), len(versions))
	}

	for i, v := range expected {
		if versions[i] != v {
			t.Errorf("expected version %d at position %d, got %d", v, i, versions[i])
		}
	}
}

// TestIdempotency tests that running Up multiple times is safe.
func TestIdempotency(t *testing.T) {
	db := testDB(t)
	m, err := NewMigrator(db, "")
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}

	m.Register(Migration{
		Version: 1,
		Name:    "test",
		UpSQL:   "CREATE TABLE test (id INTEGER);",
		DownSQL: "DROP TABLE IF EXISTS test;",
	})

	ctx := context.Background()

	// Apply once
	if err := m.Up(ctx); err != nil {
		t.Fatalf("first Up() error = %v", err)
	}

	// Apply again
	if err := m.Up(ctx); err != nil {
		t.Fatalf("second Up() error = %v", err)
	}

	// Verify only one migration was recorded
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM _migrations").Scan(&count)
	if err != nil {
		t.Fatalf("count migrations: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 migration, got %d", count)
	}
}

// TestSanitizeMigrationName tests migration name sanitization.
func TestSanitizeMigrationName(t *testing.T) {
	tests := []struct {
		input  string
		output string
	}{
		{"create users table", "create_users_table"},
		{"add-index", "add-index"},
		{"test@migration#1", "test_migration_1"},
		{"UPPER CASE", "UPPER_CASE"},
	}

	for _, tt := range tests {
		result := sanitizeMigrationName(tt.input)
		if result != tt.output {
			t.Errorf("sanitizeMigrationName(%q) = %q, want %q", tt.input, result, tt.output)
		}
	}
}

// BenchmarkApplyMigration benchmarks migration application.
func BenchmarkApplyMigration(b *testing.B) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	m, err := NewMigrator(db, "")
	if err != nil {
		b.Fatalf("NewMigrator() error = %v", err)
	}

	m.Register(Migration{
		Version: 1,
		Name:    "benchmark",
		UpSQL:   "CREATE TABLE bench (id INTEGER PRIMARY KEY, value TEXT);",
		DownSQL: "DROP TABLE IF EXISTS bench;",
	})

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Rollback first if applied
		_ = m.Down(ctx, 0)
		// Apply
		_ = m.Up(ctx)
	}
}

// BenchmarkStatus benchmarks status reporting.
func BenchmarkStatus(b *testing.B) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	m, err := NewMigrator(db, "")
	if err != nil {
		b.Fatalf("NewMigrator() error = %v", err)
	}

	// Register 100 migrations
	for i := 1; i <= 100; i++ {
		m.Register(Migration{
			Version: i,
			Name:    "test",
			UpSQL:   "SELECT 1;",
			DownSQL: "SELECT 1;",
		})
	}

	// Apply 50 migrations
	ctx := context.Background()
	_ = m.Up(ctx)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = m.Status(ctx)
	}
}

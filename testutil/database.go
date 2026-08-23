// Package testutil provides testing utilities for Cortex tests.
//
// This package contains helpers for creating test databases, fixtures,
// and custom assertions to simplify writing tests.
package testutil

import (
	"context"
	"database/sql"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/database"
	"github.com/lleontor705/cortex/v2/internal/migration"
)

// TestDB wraps a test database with automatic cleanup and migration support.
// It provides an in-memory SQLite database that is automatically cleaned up
// when the test completes.
type TestDB struct {
	*database.Manager
	t        *testing.T
	closed   bool
	migrator *migration.Migrator
}

// NewTestDB creates an in-memory test database.
// The database is automatically cleaned up via t.Cleanup when the test completes.
//
// Example:
//
//	func TestSomething(t *testing.T) {
//	    db := testutil.NewTestDB(t)
//	    // Use db.DB() for queries
//	}
func NewTestDB(t *testing.T) *TestDB {
	t.Helper()

	cfg := database.InMemoryConfig()
	mgr, err := database.NewManager(cfg)
	if err != nil {
		t.Fatalf("testutil: create database manager: %v", err)
	}

	testDB := &TestDB{
		Manager: mgr,
		t:       t,
		closed:  false,
	}

	// Register cleanup
	t.Cleanup(testDB.Cleanup)

	return testDB
}

// NewTestDBWithMigrations creates an in-memory test database with migrations applied.
// It registers all migrations from the provided registry and applies them.
//
// Example:
//
//	func TestWithMigrations(t *testing.T) {
//	    registry := migration.NewRegistry()
//	    registry.Register(migration.Migration{
//	        Version: 1,
//	        Name:    "init",
//	        UpSQL:   "CREATE TABLE test (id INTEGER PRIMARY KEY);",
//	        DownSQL: "DROP TABLE test;",
//	    })
//	    db := testutil.NewTestDBWithMigrations(t, registry)
//	}
func NewTestDBWithMigrations(t *testing.T, registry *migration.Registry) *TestDB {
	t.Helper()

	testDB := NewTestDB(t)

	// Create migrator with empty dir (we'll use registry only)
	migrator, err := migration.NewMigrator(testDB.DB(), "")
	if err != nil {
		t.Fatalf("testutil: create migrator: %v", err)
	}

	// Register all migrations from the registry
	for _, m := range registry.GetAll() {
		migrator.Register(m)
	}

	// Apply migrations
	ctx := context.Background()
	if err := migrator.Up(ctx); err != nil {
		t.Fatalf("testutil: apply migrations: %v", err)
	}

	testDB.migrator = migrator

	return testDB
}

// Cleanup closes the database connection and removes test resources.
// It is automatically called via t.Cleanup when using NewTestDB.
// It is safe to call multiple times.
func (db *TestDB) Cleanup() {
	if db.closed {
		return
	}

	db.closed = true

	if err := db.Close(); err != nil {
		db.t.Errorf("testutil: close database: %v", err)
	}
}

// WithTransaction runs fn in a transaction that is automatically rolled back.
// This is useful for tests that need to verify database operations without
// persisting changes.
//
// Example:
//
//	err := db.WithTransaction(ctx, func(tx *sql.Tx) error {
//	    _, err := tx.Exec("INSERT INTO observations (title) VALUES (?)", "test")
//	    return err
//	})
//	// Transaction is rolled back, changes are not persisted
func (db *TestDB) WithTransaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	db.t.Helper()

	tx, err := db.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	// Always rollback
	defer func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			db.t.Errorf("testutil: rollback transaction: %v", err)
		}
	}()

	if err := fn(tx); err != nil {
		return err
	}

	// Commit to complete the transaction, then we'll rollback in defer
	// Actually, for test isolation, we want to rollback, so we don't commit
	return nil
}

// WithTransactionCommit runs fn in a transaction that is committed if fn succeeds.
// This is useful for tests that need to verify the committed state.
func (db *TestDB) WithTransactionCommit(ctx context.Context, fn func(tx *sql.Tx) error) error {
	db.t.Helper()

	tx, err := db.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}

// Migrator returns the migrator for this test database.
// Returns nil if NewTestDBWithMigrations was not used.
func (db *TestDB) Migrator() *migration.Migrator {
	return db.migrator
}

// Exec executes a SQL statement with the given arguments.
// It fails the test if the execution fails.
func (db *TestDB) Exec(query string, args ...any) sql.Result {
	db.t.Helper()

	result, err := db.DB().Exec(query, args...)
	if err != nil {
		db.t.Fatalf("testutil: exec query: %v", err)
	}
	return result
}

// QueryRow executes a query that returns at most one row.
// It fails the test if the query fails.
func (db *TestDB) QueryRow(query string, args ...any) *sql.Row {
	db.t.Helper()

	return db.DB().QueryRow(query, args...)
}

// Query executes a query that returns multiple rows.
// It fails the test if the query fails.
func (db *TestDB) Query(query string, args ...any) *sql.Rows {
	db.t.Helper()

	rows, err := db.DB().Query(query, args...)
	if err != nil {
		db.t.Fatalf("testutil: query: %v", err)
	}
	return rows
}

// MustExec executes a SQL statement and panics if it fails.
// Use this for setup code where you want to panic on failure.
func (db *TestDB) MustExec(query string, args ...any) sql.Result {
	db.t.Helper()

	result, err := db.DB().Exec(query, args...)
	if err != nil {
		db.t.Fatalf("testutil: must exec: %v", err)
	}
	return result
}

// TableExists returns true if the specified table exists in the database.
func (db *TestDB) TableExists(name string) bool {
	db.t.Helper()

	var exists bool
	err := db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)",
		name,
	).Scan(&exists)
	if err != nil {
		db.t.Fatalf("testutil: check table exists: %v", err)
	}
	return exists
}

// CountRows returns the number of rows in the specified table.
func (db *TestDB) CountRows(table string) int {
	db.t.Helper()

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
	if err != nil {
		db.t.Fatalf("testutil: count rows: %v", err)
	}
	return count
}

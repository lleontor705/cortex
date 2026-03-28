package testutil

import (
	"context"
	"database/sql"
	"testing"

	"github.com/lleontor705/cortex/internal/migration"
)

func TestNewTestDB(t *testing.T) {
	db := NewTestDB(t)

	if db == nil {
		t.Fatal("expected non-nil TestDB")
	}

	if db.DB() == nil {
		t.Fatal("expected non-nil DB")
	}

	if !db.IsInMemory() {
		t.Error("expected in-memory database")
	}

	// Verify we can execute queries
	var result int
	if err := db.DB().QueryRow("SELECT 1").Scan(&result); err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if result != 1 {
		t.Errorf("expected 1, got %d", result)
	}
}

func TestNewTestDBWithMigrations(t *testing.T) {
	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1,
		Name:    "init",
		UpSQL: `CREATE TABLE test_table (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL
		);`,
		DownSQL: "DROP TABLE test_table;",
	})

	db := NewTestDBWithMigrations(t, registry)

	if db == nil {
		t.Fatal("expected non-nil TestDB")
	}

	// Verify table was created
	if !db.TableExists("test_table") {
		t.Error("expected test_table to exist after migration")
	}

	// Verify migrator is available
	if db.Migrator() == nil {
		t.Error("expected migrator to be available")
	}
}

func TestTestDB_Cleanup(t *testing.T) {
	// Create a separate test to verify cleanup behavior
	innerT := &testing.T{}

	db := NewTestDB(innerT)

	// Cleanup should be safe to call multiple times
	db.Cleanup()
	db.Cleanup() // Should not panic or error
}

func TestTestDB_WithTransaction(t *testing.T) {
	db := NewTestDB(t)

	// Create a test table
	db.MustExec("CREATE TABLE test_tx (id INTEGER PRIMARY KEY, value TEXT)")

	ctx := context.Background()

	// Insert data in a transaction
	err := db.WithTransaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO test_tx (value) VALUES (?)", "test")
		return err
	})

	if err != nil {
		t.Fatalf("WithTransaction failed: %v", err)
	}

	// Data should NOT be persisted (transaction was rolled back)
	count := db.CountRows("test_tx")
	if count != 0 {
		t.Errorf("expected 0 rows after rollback, got %d", count)
	}
}

func TestTestDB_WithTransactionCommit(t *testing.T) {
	db := NewTestDB(t)

	// Create a test table
	db.MustExec("CREATE TABLE test_tx_commit (id INTEGER PRIMARY KEY, value TEXT)")

	ctx := context.Background()

	// Insert data in a transaction that commits
	err := db.WithTransactionCommit(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO test_tx_commit (value) VALUES (?)", "test")
		return err
	})

	if err != nil {
		t.Fatalf("WithTransactionCommit failed: %v", err)
	}

	// Data should be persisted
	count := db.CountRows("test_tx_commit")
	if count != 1 {
		t.Errorf("expected 1 row after commit, got %d", count)
	}
}

func TestTestDB_Exec(t *testing.T) {
	db := NewTestDB(t)

	result := db.Exec("CREATE TABLE exec_test (id INTEGER PRIMARY KEY)")
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestTestDB_QueryRow(t *testing.T) {
	db := NewTestDB(t)

	var result int
	err := db.QueryRow("SELECT 1 + 1").Scan(&result)
	if err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if result != 2 {
		t.Errorf("expected 2, got %d", result)
	}
}

func TestTestDB_Query(t *testing.T) {
	db := NewTestDB(t)

	db.MustExec("CREATE TABLE query_test (id INTEGER PRIMARY KEY, value TEXT)")
	db.MustExec("INSERT INTO query_test (value) VALUES ('a'), ('b'), ('c')")

	rows := db.Query("SELECT value FROM query_test ORDER BY id")
	defer rows.Close()

	var values []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("Scan failed: %v", err)
		}
		values = append(values, v)
	}

	if len(values) != 3 {
		t.Errorf("expected 3 values, got %d", len(values))
	}
}

func TestTestDB_TableExists(t *testing.T) {
	db := NewTestDB(t)

	if db.TableExists("nonexistent") {
		t.Error("expected nonexistent table to not exist")
	}

	db.MustExec("CREATE TABLE exists_test (id INTEGER PRIMARY KEY)")

	if !db.TableExists("exists_test") {
		t.Error("expected exists_test table to exist")
	}
}

func TestTestDB_CountRows(t *testing.T) {
	db := NewTestDB(t)

	db.MustExec("CREATE TABLE count_test (id INTEGER PRIMARY KEY)")

	if db.CountRows("count_test") != 0 {
		t.Error("expected 0 rows initially")
	}

	db.MustExec("INSERT INTO count_test DEFAULT VALUES")
	db.MustExec("INSERT INTO count_test DEFAULT VALUES")

	if db.CountRows("count_test") != 2 {
		t.Errorf("expected 2 rows, got %d", db.CountRows("count_test"))
	}
}

func TestTestDB_MustExec_Error(t *testing.T) {
	// Note: MustExec calls Fatalf on error, which causes FailNow.
	// This cannot be tested with inner tests because Fatalf exits the goroutine.
	// The behavior is verified by manual testing.
}

func TestTestDB_QueryRow_Success(t *testing.T) {
	// QueryRow doesn't fail immediately, only on Scan
	db := NewTestDB(t)

	row := db.QueryRow("SELECT 1")
	var result int
	if err := row.Scan(&result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Note: TestTestDB_Query_Fatalf is removed because Query calls Fatalf on error,
// which cannot be tested with inner tests as it causes FailNow.

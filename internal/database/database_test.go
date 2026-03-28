package database

import (
	"context"
	"testing"
	"time"
)

func TestNewManager_DefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Path = t.TempDir() + "/test.db"

	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	if m.db == nil {
		t.Fatal("expected db to be initialized")
	}

	if m.Path() != cfg.Path {
		t.Errorf("expected path %q, got %q", cfg.Path, m.Path())
	}

	if m.IsInMemory() {
		t.Error("expected IsInMemory to be false for file database")
	}
}

func TestNewManager_InMemory(t *testing.T) {
	cfg := InMemoryConfig()

	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	if !m.IsInMemory() {
		t.Error("expected IsInMemory to be true")
	}

	if m.Path() != ":memory:" {
		t.Errorf("expected path ':memory:', got %q", m.Path())
	}
}

func TestNewManager_EmptyPath(t *testing.T) {
	cfg := DatabaseConfig{} // Empty path

	_, err := NewManager(cfg)
	if err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
}

func TestDB(t *testing.T) {
	m, err := NewManager(InMemoryConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = m.Close() }()

	db := m.DB()
	if db == nil {
		t.Fatal("expected non-nil DB")
	}

	// Test that we can execute a simple query
	var result int
	if err := db.QueryRow("SELECT 1").Scan(&result); err != nil {
		t.Fatalf("QueryRow: %v", err)
	}
	if result != 1 {
		t.Errorf("expected 1, got %d", result)
	}
}

func TestClose(t *testing.T) {
	m, err := NewManager(InMemoryConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// First close should succeed
	if err := m.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}

	// Second close should be no-op
	if err := m.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}

	// DB should be nil after close
	if m.DB() != nil {
		t.Error("expected DB to be nil after Close")
	}
}

func TestBeginTx(t *testing.T) {
	m, err := NewManager(InMemoryConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = m.Close() }()

	ctx := context.Background()
	tx, err := m.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}

	// Test that we can execute queries in the transaction
	if _, err := tx.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("Exec in tx: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func TestBeginTx_AfterClose(t *testing.T) {
	m, err := NewManager(InMemoryConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ctx := context.Background()
	_, err = m.BeginTx(ctx)
	if err == nil {
		t.Fatal("expected error when BeginTx on closed manager")
	}
}

func TestApplyPragmas(t *testing.T) {
	m, err := NewManager(InMemoryConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = m.Close() }()

	ctx := context.Background()
	if err := m.ApplyPragmas(ctx); err != nil {
		t.Fatalf("ApplyPragmas: %v", err)
	}

	// Verify pragmas were applied
	db := m.DB()

	// Check foreign_keys
	var fkEnabled bool
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if !fkEnabled {
		t.Error("expected foreign_keys to be enabled")
	}

	// Check synchronous
	var syncMode int
	if err := db.QueryRow("PRAGMA synchronous").Scan(&syncMode); err != nil {
		t.Fatalf("query synchronous: %v", err)
	}
	if syncMode != 1 { // NORMAL = 1
		t.Errorf("expected synchronous=NORMAL (1), got %d", syncMode)
	}

	// Check cache_size
	var cacheSize int
	if err := db.QueryRow("PRAGMA cache_size").Scan(&cacheSize); err != nil {
		t.Fatalf("query cache_size: %v", err)
	}
	if cacheSize != -64000 {
		t.Errorf("expected cache_size=-64000, got %d", cacheSize)
	}
}

func TestApplyPragmas_WALMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Path = t.TempDir() + "/test_wal.db"
	cfg.EnableWAL = true

	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = m.Close() }()

	// Check journal_mode
	db := m.DB()
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("expected journal_mode=wal, got %q", journalMode)
	}
}

func TestApplyPragmas_AfterClose(t *testing.T) {
	m, err := NewManager(InMemoryConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ctx := context.Background()
	if err := m.ApplyPragmas(ctx); err == nil {
		t.Fatal("expected error when ApplyPragmas on closed manager")
	}
}

func TestApplyPragmas_ContextCancellation(t *testing.T) {
	m, err := NewManager(InMemoryConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = m.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	if err := m.ApplyPragmas(ctx); err == nil {
		t.Fatal("expected error when context is cancelled")
	}
}

func TestPing(t *testing.T) {
	m, err := NewManager(InMemoryConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = m.Close() }()

	ctx := context.Background()
	if err := m.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPing_AfterClose(t *testing.T) {
	m, err := NewManager(InMemoryConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ctx := context.Background()
	if err := m.Ping(ctx); err == nil {
		t.Fatal("expected error when Ping on closed manager")
	}
}

func TestStats(t *testing.T) {
	m, err := NewManager(InMemoryConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = m.Close() }()

	stats := m.Stats()
	if stats.MaxOpenConnections != 1 { // In-memory uses 1 connection
		t.Errorf("expected MaxOpenConnections=1, got %d", stats.MaxOpenConnections)
	}
}

func TestStats_AfterClose(t *testing.T) {
	m, err := NewManager(InMemoryConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	stats := m.Stats()
	if stats.MaxOpenConnections != 0 {
		t.Errorf("expected empty stats after close, got %+v", stats)
	}
}

func TestConnectionPooling(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Path = t.TempDir() + "/test_pool.db"
	cfg.MaxOpenConns = 5
	cfg.MaxIdleConns = 3
	cfg.BusyTimeout = 10000 // Increase busy timeout for concurrent access

	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = m.Close() }()

	db := m.DB()

	// Run multiple concurrent queries to test pooling
	errChan := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() {
			var result int
			if err := db.QueryRow("SELECT 1").Scan(&result); err != nil {
				errChan <- err
			} else {
				errChan <- nil
			}
		}()
	}

	// Wait for all queries to complete and check for errors
	for i := 0; i < 10; i++ {
		select {
		case err := <-errChan:
			if err != nil {
				t.Errorf("concurrent query: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for concurrent queries")
		}
	}

	stats := m.Stats()
	if stats.MaxOpenConnections != 5 {
		t.Errorf("expected MaxOpenConnections=5, got %d", stats.MaxOpenConnections)
	}
}

func TestBuildPragmas(t *testing.T) {
	tests := []struct {
		name     string
		config   DatabaseConfig
		contains []string
		excludes []string
	}{
		{
			name:   "in-memory disables WAL and mmap",
			config: InMemoryConfig(),
			contains: []string{
				"PRAGMA synchronous = NORMAL",
				"PRAGMA foreign_keys = ON",
			},
			excludes: []string{
				"PRAGMA journal_mode = WAL",
				"PRAGMA mmap_size",
			},
		},
		{
			name: "persistent enables WAL and mmap",
			config: DatabaseConfig{
				Path:        "test.db",
				EnableWAL:   true,
				CacheSize:   -64000,
				MMapSize:    268435456,
				BusyTimeout: 5000,
			},
			contains: []string{
				"PRAGMA journal_mode = WAL",
				"PRAGMA mmap_size = 268435456",
				"PRAGMA cache_size = -64000",
			},
			excludes: []string{},
		},
		{
			name: "WAL disabled",
			config: DatabaseConfig{
				Path:      "test.db",
				EnableWAL: false,
			},
			contains: []string{
				"PRAGMA synchronous = NORMAL",
			},
			excludes: []string{
				"PRAGMA journal_mode = WAL",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create manager with minimal config for testing buildPragmas
			m := &Manager{config: tt.config}
			pragmas := m.buildPragmas()

			for _, want := range tt.contains {
				found := false
				for _, p := range pragmas {
					if p == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected pragmas to contain %q, got %v", want, pragmas)
				}
			}

			for _, notWant := range tt.excludes {
				for _, p := range pragmas {
					if p == notWant {
						t.Errorf("expected pragmas to NOT contain %q", notWant)
					}
				}
			}
		})
	}
}

func TestPragmaValues(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Path = t.TempDir() + "/test_pragma.db"
	cfg.CacheSize = -128000  // 128MB
	cfg.MMapSize = 536870912 // 512MB
	cfg.BusyTimeout = 10000  // 10 seconds

	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = m.Close() }()

	db := m.DB()

	// Verify cache_size
	var cacheSize int
	if err := db.QueryRow("PRAGMA cache_size").Scan(&cacheSize); err != nil {
		t.Fatalf("query cache_size: %v", err)
	}
	if cacheSize != -128000 {
		t.Errorf("expected cache_size=-128000, got %d", cacheSize)
	}

	// Verify mmap_size
	var mmapSize int64
	if err := db.QueryRow("PRAGMA mmap_size").Scan(&mmapSize); err != nil {
		t.Fatalf("query mmap_size: %v", err)
	}
	if mmapSize != 536870912 {
		t.Errorf("expected mmap_size=536870912, got %d", mmapSize)
	}

	// Verify busy_timeout
	var busyTimeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != 10000 {
		t.Errorf("expected busy_timeout=10000, got %d", busyTimeout)
	}
}

func TestMMapSizeZeroForInMemory(t *testing.T) {
	// Explicitly set MMapSize to a value, but it should be ignored for in-memory
	cfg := DatabaseConfig{
		Path:     ":memory:",
		MMapSize: 268435456,
	}

	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = m.Close() }()

	// Verify that mmap_size pragma is NOT in the pragmas list for in-memory
	pragmas := m.buildPragmas()
	for _, p := range pragmas {
		if p == "PRAGMA mmap_size = 268435456" {
			t.Error("expected mmap_size pragma to be excluded for in-memory database")
		}
	}
}

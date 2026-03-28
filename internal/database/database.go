// Package database provides SQLite connection management with optimized pragmas.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sync"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver (no CGO)
)

// DatabaseConfig holds configuration for database connection.
type DatabaseConfig struct {
	// Path is the filesystem path to the SQLite database file.
	// Use ":memory:" for an in-memory database (useful for testing).
	Path string

	// MaxOpenConns sets the maximum number of open connections.
	// Default: 25
	MaxOpenConns int

	// MaxIdleConns sets the maximum number of idle connections.
	// Default: 10
	MaxIdleConns int

	// ConnMaxLifetime sets the maximum lifetime of a connection.
	// Default: 30 minutes
	ConnMaxLifetime time.Duration

	// EnableWAL enables Write-Ahead Logging mode.
	// Default: true
	EnableWAL bool

	// CacheSize sets the SQLite cache size in kilobytes.
	// Negative values set the cache size in pages.
	// Default: -64000 (64MB)
	CacheSize int

	// MMapSize sets the memory-mapped I/O size in bytes.
	// Default: 268435456 (256MB)
	MMapSize int64

	// BusyTimeout sets the timeout for busy waiting in milliseconds.
	// Default: 5000 (5 seconds)
	BusyTimeout int
}

// DefaultConfig returns a DatabaseConfig with sensible defaults.
func DefaultConfig() DatabaseConfig {
	return DatabaseConfig{
		Path:            "cortex.db",
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 30 * time.Minute,
		EnableWAL:       true,
		CacheSize:       -64000,    // 64MB in pages
		MMapSize:        268435456, // 256MB
		BusyTimeout:     5000,      // 5 seconds
	}
}

// InMemoryConfig returns a DatabaseConfig for an in-memory database.
func InMemoryConfig() DatabaseConfig {
	return DatabaseConfig{
		Path:            ":memory:",
		MaxOpenConns:    1, // In-memory requires single connection
		MaxIdleConns:    1,
		ConnMaxLifetime: 0,
		EnableWAL:       false, // WAL not useful for in-memory
		CacheSize:       -64000,
		MMapSize:        0, // MMap not useful for in-memory
		BusyTimeout:     5000,
	}
}

// Manager manages SQLite database connections with optimized settings.
type Manager struct {
	db     *sql.DB
	config DatabaseConfig
	mu     sync.RWMutex
}

// NewManager creates a new database manager with the given configuration.
func NewManager(cfg DatabaseConfig) (*Manager, error) {
	if cfg.Path == "" {
		cfg.Path = "cortex.db"
	}
	if cfg.MaxOpenConns <= 0 {
		cfg.MaxOpenConns = 25
	}
	if cfg.MaxIdleConns <= 0 {
		cfg.MaxIdleConns = 10
	}
	if cfg.ConnMaxLifetime == 0 {
		cfg.ConnMaxLifetime = 30 * time.Minute
	}
	if cfg.CacheSize == 0 {
		cfg.CacheSize = -64000
	}
	if cfg.MMapSize == 0 && cfg.Path != ":memory:" {
		cfg.MMapSize = 268435456
	}
	if cfg.BusyTimeout == 0 {
		cfg.BusyTimeout = 5000
	}

	// Build DSN with _pragma params so every pooled connection gets them
	dsn := cfg.buildDSN()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("database: open connection: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	m := &Manager{
		db:     db,
		config: cfg,
	}

	// Apply WAL and mmap pragmas that only need to run once (database-level, not per-connection)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if cfg.EnableWAL && cfg.Path != ":memory:" {
		if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("database: pragma journal_mode: %w", err)
		}
	}
	if cfg.MMapSize > 0 && cfg.Path != ":memory:" {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA mmap_size = %d", cfg.MMapSize)); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("database: pragma mmap_size: %w", err)
		}
	}

	return m, nil
}

// DB returns the underlying database connection.
// The returned *sql.DB is safe for concurrent use by multiple goroutines.
func (m *Manager) DB() *sql.DB {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.db
}

// Close closes the database connection gracefully.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db == nil {
		return nil
	}

	err := m.db.Close()
	m.db = nil
	return err
}

// BeginTx starts a new transaction with the given context.
// The transaction must be committed or rolled back by the caller.
func (m *Manager) BeginTx(ctx context.Context) (*sql.Tx, error) {
	m.mu.RLock()
	db := m.db
	m.mu.RUnlock()

	if db == nil {
		return nil, fmt.Errorf("database: connection closed")
	}

	return db.BeginTx(ctx, nil)
}

// ApplyPragmas applies SQLite pragma settings for optimal performance.
// These pragmas are applied on each connection in the pool.
func (m *Manager) ApplyPragmas(ctx context.Context) error {
	m.mu.RLock()
	db := m.db
	m.mu.RUnlock()

	if db == nil {
		return fmt.Errorf("database: connection closed")
	}

	pragmas := m.buildPragmas()

	for _, pragma := range pragmas {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("database: pragma %q: %w", pragma, err)
		}
	}

	return nil
}

// buildDSN constructs a DSN with _pragma query parameters so that
// per-connection pragmas are applied automatically by the driver on every
// new connection in the pool.
func (cfg DatabaseConfig) buildDSN() string {
	v := url.Values{}
	v.Add("_pragma", fmt.Sprintf("busy_timeout=%d", cfg.BusyTimeout))
	v.Add("_pragma", "synchronous=NORMAL")
	v.Add("_pragma", "foreign_keys=ON")
	v.Add("_pragma", "temp_store=MEMORY")
	v.Add("_pragma", fmt.Sprintf("cache_size=%d", cfg.CacheSize))
	return cfg.Path + "?" + v.Encode()
}

// buildPragmas builds the list of pragma statements based on configuration.
// Used by ApplyPragmas for one-shot pragma application.
func (m *Manager) buildPragmas() []string {
	pragmas := []string{
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA temp_store = MEMORY",
		fmt.Sprintf("PRAGMA cache_size = %d", m.config.CacheSize),
		fmt.Sprintf("PRAGMA busy_timeout = %d", m.config.BusyTimeout),
	}

	// WAL mode for persistent databases
	if m.config.EnableWAL && m.config.Path != ":memory:" {
		pragmas = append(pragmas, "PRAGMA journal_mode = WAL")
	}

	// Memory-mapped I/O for persistent databases
	if m.config.MMapSize > 0 && m.config.Path != ":memory:" {
		pragmas = append(pragmas, fmt.Sprintf("PRAGMA mmap_size = %d", m.config.MMapSize))
	}

	return pragmas
}

// Ping verifies the database connection is still alive.
func (m *Manager) Ping(ctx context.Context) error {
	m.mu.RLock()
	db := m.db
	m.mu.RUnlock()

	if db == nil {
		return fmt.Errorf("database: connection closed")
	}

	return db.PingContext(ctx)
}

// Stats returns database statistics for monitoring.
func (m *Manager) Stats() sql.DBStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.db == nil {
		return sql.DBStats{}
	}

	return m.db.Stats()
}

// IsInMemory returns true if the database is in-memory.
func (m *Manager) IsInMemory() bool {
	return m.config.Path == ":memory:"
}

// Path returns the database file path.
func (m *Manager) Path() string {
	return m.config.Path
}

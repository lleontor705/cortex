// Package migration provides a database migration framework for SQLite.
//
// It supports version-tracked migrations with Up/Down capabilities,
// transaction safety, and status reporting.
package migration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Migration represents a single database migration.
type Migration struct {
	Version     int    // Migration version number
	Name        string // Migration name (from filename)
	Description string // Human-readable description
	UpSQL       string // SQL to apply migration
	DownSQL     string // SQL to rollback migration
}

// MigrationStatus represents the status of a migration.
type MigrationStatus struct {
	Version   int    // Migration version
	Name      string // Migration name
	Applied   bool   // Whether the migration has been applied
	AppliedAt string // When the migration was applied (empty if not applied)
}

// Migrator manages database migrations.
type Migrator struct {
	db       *sql.DB
	dir      string       // migrations directory
	applied  map[int]bool // cache of applied migrations
	registry *Registry    // in-memory registry for programmatically registered migrations
}

// NewMigrator creates a new migrator instance.
// The dir parameter specifies the filesystem path to the migrations directory.
func NewMigrator(db *sql.DB, dir string) (*Migrator, error) {
	if db == nil {
		return nil, fmt.Errorf("migration: database connection is nil")
	}

	m := &Migrator{
		db:       db,
		dir:      dir,
		applied:  make(map[int]bool),
		registry: NewRegistry(),
	}

	// Ensure migrations tracking table exists
	if err := m.ensureMigrationsTable(); err != nil {
		return nil, fmt.Errorf("migration: create migrations table: %w", err)
	}

	// Load applied migrations into cache
	if err := m.loadAppliedMigrations(); err != nil {
		return nil, fmt.Errorf("migration: load applied migrations: %w", err)
	}

	return m, nil
}

// ensureMigrationsTable creates the _migrations table if it doesn't exist.
func (m *Migrator) ensureMigrationsTable() error {
	schema := `
		CREATE TABLE IF NOT EXISTS _migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`
	_, err := m.db.Exec(schema)
	return err
}

// loadAppliedMigrations loads the list of applied migrations into the cache.
func (m *Migrator) loadAppliedMigrations() error {
	rows, err := m.db.Query("SELECT version FROM _migrations")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return err
		}
		m.applied[version] = true
	}

	return rows.Err()
}

// Register adds a migration to the in-memory registry.
// This is useful for programmatically defining migrations instead of loading from disk.
func (m *Migrator) Register(migration Migration) {
	m.registry.Register(migration)
}

// Up applies all pending migrations.
// Migrations are applied in version order (lowest to highest).
//
// v2-aware (W3, REQ-DB-001): on a cortex-v2 database the v1 migrations 001-014
// are RETIRED — they are consolidated into the v2 baseline applied by the app
// bootstrap. Running them here would conflict ("table already exists"), so on a
// v2 database Up is an idempotent no-op. On a non-v2 database Up behaves as
// before (legacy v1 line).
func (m *Migrator) Up(ctx context.Context) error {
	if m.isV2Database() {
		// v1 001-014 are retired on the v2 line; the v2 baseline is authoritative.
		return nil
	}
	migrations, err := m.getPendingMigrations()
	if err != nil {
		return fmt.Errorf("migration: get pending migrations: %w", err)
	}

	if len(migrations) == 0 {
		return nil // No pending migrations
	}

	// Sort by version
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	// Apply each migration
	for _, migration := range migrations {
		if err := m.applyMigration(ctx, migration); err != nil {
			return fmt.Errorf("migration: apply version %d: %w", migration.Version, err)
		}
	}

	return nil
}

// Down rolls back migrations to the specified version.
// If version is 0, all migrations are rolled back.
// Migrations are rolled back in reverse order (highest to lowest).
func (m *Migrator) Down(ctx context.Context, version int) error {
	// Get applied migrations that need to be rolled back
	applied, err := m.getAppliedMigrations()
	if err != nil {
		return fmt.Errorf("migration: get applied migrations: %w", err)
	}

	// Filter to only those >= target version
	var toRollback []Migration
	for _, migration := range applied {
		if version == 0 || migration.Version > version {
			toRollback = append(toRollback, migration)
		}
	}

	if len(toRollback) == 0 {
		return nil // Nothing to rollback
	}

	// Sort by version descending
	sort.Slice(toRollback, func(i, j int) bool {
		return toRollback[i].Version > toRollback[j].Version
	})

	// Rollback each migration
	for _, migration := range toRollback {
		if err := m.rollbackMigration(ctx, migration); err != nil {
			return fmt.Errorf("migration: rollback version %d: %w", migration.Version, err)
		}
	}

	return nil
}

// Status returns the status of all migrations (both applied and pending).
//
// v2-aware (W3, REQ-DB-001): on a cortex-v2 database the v1 migrations 001-014
// are consolidated into the v2 baseline, so they are reported as applied (the
// schema they define is present). On a non-v2 database, status reflects the
// _migrations tracking table as before.
func (m *Migrator) Status(ctx context.Context) ([]MigrationStatus, error) {
	// Get all migrations (from registry and disk)
	allMigrations, err := m.getAllMigrations()
	if err != nil {
		return nil, fmt.Errorf("migration: get all migrations: %w", err)
	}

	// On a v2 database, v1 001-014 are consolidated into the baseline.
	consolidated := m.isV2Database()

	// Build status list
	var statuses []MigrationStatus
	for _, migration := range allMigrations {
		applied := m.applied[migration.Version] || consolidated
		status := MigrationStatus{
			Version: migration.Version,
			Name:    migration.Name,
			Applied: applied,
		}

		// Get applied_at timestamp if migration was applied via the tracker.
		if m.applied[migration.Version] {
			var appliedAt string
			err := m.db.QueryRow(
				"SELECT applied_at FROM _migrations WHERE version = ?",
				migration.Version,
			).Scan(&appliedAt)
			if err == nil {
				status.AppliedAt = appliedAt
			}
		}

		statuses = append(statuses, status)
	}

	// Sort by version
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Version < statuses[j].Version
	})

	return statuses, nil
}

// getPendingMigrations returns migrations that haven't been applied yet.
func (m *Migrator) getPendingMigrations() ([]Migration, error) {
	allMigrations, err := m.getAllMigrations()
	if err != nil {
		return nil, err
	}

	var pending []Migration
	for _, migration := range allMigrations {
		if !m.applied[migration.Version] {
			pending = append(pending, migration)
		}
	}

	return pending, nil
}

// getAppliedMigrations returns migrations that have been applied.
func (m *Migrator) getAppliedMigrations() ([]Migration, error) {
	allMigrations, err := m.getAllMigrations()
	if err != nil {
		return nil, err
	}

	var applied []Migration
	for _, migration := range allMigrations {
		if m.applied[migration.Version] {
			applied = append(applied, migration)
		}
	}

	return applied, nil
}

// getAllMigrations returns all migrations from registry and disk.
func (m *Migrator) getAllMigrations() ([]Migration, error) {
	migrations := m.registry.GetAll()

	// Load migrations from disk if directory exists
	if m.dir != "" {
		diskMigrations, err := m.loadMigrationsFromDisk()
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, diskMigrations...)
	}

	// Deduplicate by version (registry takes precedence)
	seen := make(map[int]bool)
	var result []Migration
	for _, migration := range migrations {
		if !seen[migration.Version] {
			seen[migration.Version] = true
			result = append(result, migration)
		}
	}

	return result, nil
}

// applyMigration applies a single migration within a transaction.
func (m *Migrator) applyMigration(ctx context.Context, migration Migration) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Execute Up SQL
	if _, err := tx.Exec(migration.UpSQL); err != nil {
		return fmt.Errorf("execute up sql: %w", err)
	}

	// Record migration
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	if _, err := tx.Exec(
		"INSERT INTO _migrations (version, name, applied_at) VALUES (?, ?, ?)",
		migration.Version, migration.Name, now,
	); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	// Update cache
	m.applied[migration.Version] = true

	return nil
}

// rollbackMigration rolls back a single migration within a transaction.
func (m *Migrator) rollbackMigration(ctx context.Context, migration Migration) error {
	if migration.DownSQL == "" {
		return fmt.Errorf("no down sql defined for migration %d", migration.Version)
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Execute Down SQL
	if _, err := tx.Exec(migration.DownSQL); err != nil {
		return fmt.Errorf("execute down sql: %w", err)
	}

	// Remove migration record
	if _, err := tx.Exec(
		"DELETE FROM _migrations WHERE version = ?",
		migration.Version,
	); err != nil {
		return fmt.Errorf("remove migration record: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	// Update cache
	delete(m.applied, migration.Version)

	return nil
}

// loadMigrationsFromDisk loads migration files from the migrations directory.
func (m *Migrator) loadMigrationsFromDisk() ([]Migration, error) {
	if m.dir == "" {
		return nil, nil
	}

	// Check if directory exists
	if _, err := os.Stat(m.dir); os.IsNotExist(err) {
		return nil, nil // Directory doesn't exist, return empty
	}

	// Read directory
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}

	var migrations []Migration
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Check for .sql extension
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		// Parse version and name from filename
		version, name, err := parseMigrationFilename(entry.Name())
		if err != nil {
			continue // Skip invalid filenames
		}

		// Read file contents
		filePath := filepath.Join(m.dir, entry.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read migration file %s: %w", entry.Name(), err)
		}

		// Parse Up and Down SQL
		upSQL, downSQL, err := parseMigrationContent(string(content))
		if err != nil {
			return nil, fmt.Errorf("parse migration file %s: %w", entry.Name(), err)
		}

		migrations = append(migrations, Migration{
			Version:     version,
			Name:        name,
			Description: name,
			UpSQL:       upSQL,
			DownSQL:     downSQL,
		})
	}

	return migrations, nil
}

// parseMigrationFilename extracts version and name from a migration filename.
// Expected format: NNN_description.sql (e.g., 001_init.sql)
func parseMigrationFilename(filename string) (int, string, error) {
	// Remove .sql extension
	name := strings.TrimSuffix(filename, ".sql")

	// Split on first underscore
	parts := strings.SplitN(name, "_", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("invalid filename format: %s", filename)
	}

	// Parse version number
	version, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", fmt.Errorf("invalid version number: %s", parts[0])
	}

	return version, parts[1], nil
}

// parseMigrationContent parses migration file content into Up and Down SQL sections.
// Format:
//
//	-- +migrate Up
//	<up sql>
//	-- +migrate Down
//	<down sql>
func parseMigrationContent(content string) (upSQL, downSQL string, err error) {
	// Find Up section
	upMarker := "-- +migrate Up"
	downMarker := "-- +migrate Down"

	upIndex := strings.Index(content, upMarker)
	if upIndex == -1 {
		return "", "", fmt.Errorf("missing -- +migrate Up marker")
	}

	downIndex := strings.Index(content, downMarker)

	// Extract Up SQL
	upStart := upIndex + len(upMarker)
	if downIndex > upStart {
		upSQL = strings.TrimSpace(content[upStart:downIndex])
	} else {
		upSQL = strings.TrimSpace(content[upStart:])
	}

	// Extract Down SQL (if present)
	if downIndex > -1 {
		downStart := downIndex + len(downMarker)
		downSQL = strings.TrimSpace(content[downStart:])
	}

	return upSQL, downSQL, nil
}

// Version returns the current migration version (highest applied version).
func (m *Migrator) Version() int {
	maxVersion := 0
	for version := range m.applied {
		if version > maxVersion {
			maxVersion = version
		}
	}
	return maxVersion
}

// isV2Database reports whether the managed database carries a cortex-v2 schema
// identity (cortex_meta.schema_family == "cortex-v2"). On a v2 database the v1
// migrations 001-014 are retired (consolidated into the v2 baseline), so the
// migrator treats them as already applied and refuses to re-run them. Returns
// false on any error (table missing, no row) or when the database is not a
// cortex-v2 database — i.e. it is conservative for pre-v2/foreign databases,
// preserving the legacy v1 behavior there.
func (m *Migrator) isV2Database() bool {
	var family string
	err := m.db.QueryRow(`SELECT value FROM cortex_meta WHERE key = 'schema_family'`).Scan(&family)
	if err != nil {
		return false
	}
	return family == SchemaFamilyCortexV2
}

// sanitizeMigrationName removes special characters from migration names.
func sanitizeMigrationName(name string) string {
	// Replace spaces and special characters with underscores
	re := regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	return re.ReplaceAllString(name, "_")
}

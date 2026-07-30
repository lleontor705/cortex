// Package migration — v2 clean baseline runner (W3.1, REQ-DB-001).
//
// This file implements the forward-only v2 schema baseline: it applies the
// embedded SQL bundle inside a single transaction, records the schema identity
// (family + version + SHA-256 checksum) in cortex_meta within that SAME
// transaction, and only commits after PRAGMA integrity_check passes.
//
// Design grounding: ADR-03 (clean v2 DB + read-only old-DB refusal), spec
// REQ-DB-001 (clean v2 database at a new path with version identity).
//
// Key semantics (see v2_test.go for the full scenario matrix):
//   - UP:           fresh DB → full schema + identity + integrity pass.
//   - DOWN:         forward-only guard; returns ErrForwardOnly, never mutates.
//   - FRESH:        absent path → clean baseline; re-init is idempotent no-op.
//   - INTEGRITY:    only "ready" after integrity_check + checksum match pass.
//   - PATH:         unwritable path → fail before mutation, no partial file.
//   - V1-RETIRED:   v1 migrations 001-014 are absent from the v2 line.
package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	v2migrations "github.com/lleontor705/cortex/migrations/v2"
)

// Schema identity constants recorded in cortex_meta.
const (
	// SchemaFamilyCortexV2 is the schema family identifier for Cortex v2.
	SchemaFamilyCortexV2 = "cortex-v2"

	// V2BaselineVersion is the version label of the initial v2 baseline
	// (corresponds to migrations/v2/001_init.sql).
	V2BaselineVersion = "001"
)

// DefaultV2DBPath returns the default filesystem path for the Cortex v2
// database: ~/.cortex/v2/cortex.db. It is deliberately DISTINCT from any
// v1/Engram path (~/.cortex/cortex.db) to ensure a clean major-version
// cutover without touching legacy data (ADR-03, REQ-DB-001).
//
// When this path is absent, InitV2Database creates it cleanly (parent
// directory and baseline in one operation), preserving local fresh-install
// behavior.
func DefaultV2DBPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		// Fallback: relative path (same convention as config.CortexDir).
		return filepath.Join(".cortex", "v2", "cortex.db")
	}
	return filepath.Join(home, ".cortex", "v2", "cortex.db")
}

// Sentinel errors for the v2 baseline.
var (
	// ErrForwardOnly is returned by Down(). The v2 baseline is forward-only:
	// rolling it back would destroy user data. Down never mutates the database.
	ErrForwardOnly = errors.New("migration: v2 baseline is forward-only; Down is not supported")

	// ErrSchemaTampered is returned when the recorded schema checksum does not
	// match the expected baseline checksum (tampering or incompatible schema).
	ErrSchemaTampered = errors.New("migration: schema identity tampered or checksum mismatch")

	// ErrIncompatibleDatabase is returned when the DB has a schema identity
	// from a different family (e.g., a v1 or foreign database).
	ErrIncompatibleDatabase = errors.New("migration: incompatible database schema family")
)

// SchemaIdentity is the version+checksum tuple recorded in cortex_meta.
type SchemaIdentity struct {
	Family   string // e.g. "cortex-v2"
	Version  string // e.g. "001"
	Checksum string // hex SHA-256 of the baseline SQL
}

// V2Baseline is the forward-only v2 schema baseline.
type V2Baseline struct {
	sql      string
	identity SchemaIdentity
}

// NewV2Baseline constructs the v2 baseline from the embedded SQL bundle.
// The checksum is the SHA-256 of the baseline SQL, computed once at construction.
func NewV2Baseline() (*V2Baseline, error) {
	raw := v2migrations.BaselineSQL
	if raw == "" {
		return nil, fmt.Errorf("migration: embedded v2 baseline SQL is empty")
	}
	sum := sha256.Sum256([]byte(raw))
	return &V2Baseline{
		sql: raw,
		identity: SchemaIdentity{
			Family:   SchemaFamilyCortexV2,
			Version:  V2BaselineVersion,
			Checksum: hex.EncodeToString(sum[:]),
		},
	}, nil
}

// Identity returns the schema identity of this baseline.
func (b *V2Baseline) Identity() SchemaIdentity {
	return b.identity
}

// Apply creates the v2 baseline schema inside a single transaction, records
// the schema identity, runs PRAGMA integrity_check, and commits. It is
// IDEMPOTENT: a second Apply on a DB whose cortex_meta carries a matching
// identity is a silent no-op. A mismatched checksum returns ErrSchemaTampered.
func (b *V2Baseline) Apply(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("migration: database connection is nil")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Idempotency / compatibility check: read existing identity if present.
	existing, err := readIdentity(tx)
	if err != nil && !errors.Is(err, errNoIdentity) {
		return fmt.Errorf("migration: read existing identity: %w", err)
	}
	if err == nil {
		// cortex_meta already carries an identity.
		_ = tx.Rollback() // nothing to do; safe to discard the empty tx.
		committed = true
		return b.checkExistingIdentity(existing)
	}

	// Fresh path: execute the full baseline DDL inside this transaction.
	if _, err := tx.Exec(b.sql); err != nil {
		return fmt.Errorf("migration: execute v2 baseline SQL: %w", err)
	}

	// Integrity gate: PRAGMA integrity_check must report "ok" BEFORE we record
	// the identity or commit. This ensures the DB is only "ready" if the schema
	// is structurally sound.
	if err := integrityCheckTx(tx); err != nil {
		return fmt.Errorf("migration: integrity check: %w", err)
	}

	// Record schema identity in cortex_meta (same transaction).
	if err := writeIdentity(tx, b.identity); err != nil {
		return fmt.Errorf("migration: record schema identity: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration: commit v2 baseline: %w", err)
	}
	committed = true
	return nil
}

// Down is the forward-only guard for the v2 baseline.
// It returns ErrForwardOnly and does NOT mutate the database under any
// circumstances. The v2 baseline creates the sole location for v2 data;
// rolling it back would destroy user data, which is explicitly a non-goal
// of the v2 major release (issue #49).
func (b *V2Baseline) Down(ctx context.Context, db *sql.DB) error {
	// Deliberately does nothing. No transaction, no query, no mutation.
	return ErrForwardOnly
}

// VerifyIntegrity runs PRAGMA integrity_check and verifies the recorded
// schema checksum matches the expected baseline checksum. Returns
// ErrSchemaTampered if either check fails.
func (b *V2Baseline) VerifyIntegrity(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("migration: database connection is nil")
	}

	// Structural integrity.
	if err := integrityCheckDB(db); err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaTampered, err)
	}

	// Identity / checksum verification.
	existing, err := readIdentity(db)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaTampered, err)
	}
	if existing.Family != b.identity.Family ||
		existing.Version != b.identity.Version ||
		existing.Checksum != b.identity.Checksum {
		return fmt.Errorf("%w: expected %s/%s, got %s/%s", ErrSchemaTampered,
			b.identity.Family, b.identity.Version, existing.Family, existing.Version)
	}
	return nil
}

// IsV2 reports whether db has a valid cortex-v2 schema identity with a
// matching checksum and passing integrity check. Returns (false, err) if the
// DB is not a valid v2 database (missing identity, tampered, or corrupt).
func (b *V2Baseline) IsV2(ctx context.Context, db *sql.DB) (bool, error) {
	if err := b.VerifyIntegrity(ctx, db); err != nil {
		return false, err
	}
	return true, nil
}

// checkExistingIdentity decides what to do when cortex_meta already has an
// identity: matching checksum → idempotent no-op; mismatch → error.
func (b *V2Baseline) checkExistingIdentity(existing SchemaIdentity) error {
	if existing.Family != b.identity.Family {
		return fmt.Errorf("%w: family %q", ErrIncompatibleDatabase, existing.Family)
	}
	if existing.Checksum != b.identity.Checksum {
		return fmt.Errorf("%w: recorded %s, expected %s",
			ErrSchemaTampered, existing.Checksum, b.identity.Checksum)
	}
	// Matching identity → idempotent success.
	return nil
}

// --- low-level helpers -----------------------------------------------------

// errNoIdentity signals that cortex_meta does not carry a schema identity
// (table missing or no schema_family row).
var errNoIdentity = errors.New("no schema identity")

// querier is satisfied by both *sql.DB and *sql.Tx.
type querier interface {
	Exec(string, ...any) (sql.Result, error)
	QueryRow(string, ...any) *sql.Row
}

// readIdentity reads the schema identity from cortex_meta. Returns
// errNoIdentity if the table or the identity rows are absent.
func readIdentity(q querier) (SchemaIdentity, error) {
	var family string
	err := q.QueryRow(`SELECT value FROM cortex_meta WHERE key = 'schema_family'`).Scan(&family)
	if err != nil {
		// Table missing or no rows → no identity.
		return SchemaIdentity{}, errNoIdentity
	}
	if family == "" {
		return SchemaIdentity{}, errNoIdentity
	}
	var version, checksum string
	_ = q.QueryRow(`SELECT value FROM cortex_meta WHERE key = 'schema_version'`).Scan(&version)
	_ = q.QueryRow(`SELECT value FROM cortex_meta WHERE key = 'schema_checksum'`).Scan(&checksum)
	return SchemaIdentity{Family: family, Version: version, Checksum: checksum}, nil
}

// writeIdentity inserts the schema identity rows into cortex_meta.
func writeIdentity(q querier, id SchemaIdentity) error {
	rows := [][2]string{
		{"schema_family", id.Family},
		{"schema_version", id.Version},
		{"schema_checksum", id.Checksum},
	}
	for _, r := range rows {
		if _, err := q.Exec(
			`INSERT INTO cortex_meta (key, value) VALUES (?, ?)`,
			r[0], r[1],
		); err != nil {
			return err
		}
	}
	return nil
}

// integrityCheckTx runs PRAGMA integrity_check inside a transaction.
func integrityCheckTx(tx *sql.Tx) error {
	var result string
	if err := tx.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity_check returned: %s", result)
	}
	return nil
}

// integrityCheckDB runs PRAGMA integrity_check on a *sql.DB.
func integrityCheckDB(db *sql.DB) error {
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity_check returned: %s", result)
	}
	return nil
}

// --- InitV2Database: path-level initializer --------------------------------

// InitV2Database creates a v2 database at the given filesystem path. If the
// path does not exist, the parent directory is created and the full baseline
// is applied. If the path already holds a valid v2 DB, it is opened without
// re-running the baseline (idempotent).
//
// If the path is not writable (parent cannot be created), it fails BEFORE any
// mutation and creates no partial database file.
func InitV2Database(ctx context.Context, path string) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("migration: database path is required")
	}

	baseline, err := NewV2Baseline()
	if err != nil {
		return nil, err
	}

	// In-memory: just open and apply.
	if path == ":memory:" {
		db, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			return nil, fmt.Errorf("migration: open in-memory db: %w", err)
		}
		if err := baseline.Apply(ctx, db); err != nil {
			_ = db.Close()
			return nil, err
		}
		return db, nil
	}

	// Ensure parent directory exists (fails before any DB mutation).
	parent := filepath.Dir(path)
	if parent != "." && parent != "" {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return nil, fmt.Errorf("migration: create data directory %s: %w", parent, err)
		}
	}

	// Record whether the DB file existed before we touch it.
	existedBefore := fileExistsOnDisk(path)

	// Open the DB (read-write). modernc.org/sqlite creates the file lazily on
	// first write; if the directory was just created above, this succeeds.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("migration: open database %s: %w", path, err)
	}

	if err := baseline.Apply(ctx, db); err != nil {
		_ = db.Close()
		// Cleanup: remove any partial file if it did not exist before.
		if !existedBefore {
			removeDBSidecars(path)
		}
		return nil, err
	}

	return db, nil
}

// removeDBSidecars removes a SQLite database file and its journal/WAL/SHM
// sidecars. Used for cleanup on failed initialization.
func removeDBSidecars(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	_ = os.Remove(path + "-journal")
}

// fileExistsOnDisk reports whether a file exists (not a directory).
func fileExistsOnDisk(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// --- V2Registry: v2 migration line + v1 retirement -------------------------

// V2BaselineMigrationVersion is the numeric version of the v2 baseline in the
// migration registry. v2 migrations use versions >= 2000 so they never collide
// with the retired v1 set (1-14). 2001 = "v2 line, migration 001".
const V2BaselineMigrationVersion = 2001

// v1MigrationCount is the number of v1 migrations retired from the v2 line.
const v1MigrationCount = 14

// V2Registry defines the v2 migration line and tracks which v1 migrations are
// retired from it. v1 migrations 001-014 MUST NOT run on a v2 database.
type V2Registry struct {
	baseline  *V2Baseline
	retiredV1 []int
}

// NewV2Registry creates the v2 registry: the embedded baseline plus the set of
// retired v1 versions (1 through 14).
func NewV2Registry() (*V2Registry, error) {
	baseline, err := NewV2Baseline()
	if err != nil {
		return nil, err
	}
	retired := make([]int, v1MigrationCount)
	for i := 0; i < v1MigrationCount; i++ {
		retired[i] = i + 1
	}
	return &V2Registry{
		baseline:  baseline,
		retiredV1: retired,
	}, nil
}

// RetiredV1Versions returns the v1 migration versions retired from the v2
// line (1 through 14). These MUST NOT run on a v2 database.
func (r *V2Registry) RetiredV1Versions() []int {
	return r.retiredV1
}

// IsV1Retired reports whether a given v1 migration version is retired in the
// v2 line. All versions 1-14 are retired.
func (r *V2Registry) IsV1Retired(version int) bool {
	return version >= 1 && version <= v1MigrationCount
}

// V2Migrations returns the migrations in the v2 line (currently just the
// baseline). None of these have versions in the retired v1 range.
func (r *V2Registry) V2Migrations() []Migration {
	return []Migration{
		{
			Version:     V2BaselineMigrationVersion,
			Name:        "v2_001_init",
			Description: "Cortex v2 clean baseline (consolidated v1 001-014 + v2 corrections)",
			UpSQL:       r.baseline.sql,
			// Forward-only: DownSQL is intentionally empty. The runner's Down()
			// returns ErrForwardOnly. This field is kept empty to signal that
			// rollback is unsupported, consistent with the v2 major-release policy.
			DownSQL: "",
		},
	}
}

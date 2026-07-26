// Package app — W3.2 wiring tests (REQ-DB-002).
//
// These tests prove the read-only compatibility probe is wired into app.Open
// BEFORE any write-capable open or pragma: an incompatible database prevents
// startup with INCOMPATIBLE_DATABASE, a fresh path creates a clean cortex-v2
// database, and a cortex-v2 database reopens as Compatible. They also prove the
// probe runs before the WAL pragma: a refused old database is left byte-identical
// with no WAL/journal sidecar.
package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"

	_ "modernc.org/sqlite"
)

// isolateEnv points CORTEX_DATABASE_PATH at dbPath in an isolated HOME, disables
// in-memory mode, and neutralizes embedding providers so app.Open never touches
// the network or real user configuration.
func isolateEnv(t *testing.T, dbPath string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")
	t.Setenv("CORTEX_SEARCH_EMBEDDING_PROVIDER", "none")
	t.Setenv("CORTEX_SEARCH_OLLAMA_AUTO_START", "false")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OLLAMA_BASE_URL", "")
	t.Setenv("OLLAMA_HOST", "")
}

// buildOldV1DB creates an old Cortex v1-style database (_migrations + sessions,
// no cortex_meta) at path.
func buildOldV1DB(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, stmt := range []string{
		`CREATE TABLE _migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP);`,
		`INSERT INTO _migrations (version, name) VALUES (1, 'init');`,
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, project TEXT NOT NULL, directory TEXT NOT NULL);`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec v1 fixture: %v", err)
		}
	}
}

// fileSHA returns the hex SHA-256 of path (fatals if missing).
func fileSHA(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestAppOpen_RefusesIncompatibleDB proves the probe is wired before the
// writable open: an old Cortex v1 database prevents startup with the stable
// INCOMPATIBLE_DATABASE error (errors.Is ErrIncompatibleDatabase).
func TestAppOpen_RefusesIncompatibleDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cortex.db")
	buildOldV1DB(t, dbPath)
	isolateEnv(t, dbPath)

	_, err := Open(context.Background(), Options{})
	if err == nil {
		t.Fatal("Open() on an old v1 database succeeded; expected INCOMPATIBLE_DATABASE refusal")
	}
	if !errors.Is(err, migration.ErrIncompatibleDatabase) {
		t.Errorf("Open() error = %v; want errors.Is migration.ErrIncompatibleDatabase", err)
	}
	if !strings.Contains(err.Error(), migration.CodeIncompatibleDatabase) {
		t.Errorf("Open() error = %v; want stable code %q in message", err, migration.CodeIncompatibleDatabase)
	}
}

// TestAppOpen_ProbeRunsBeforeWritableOpen proves the probe precedes the WAL
// pragma / write-capable open: a refused old database is left byte-identical
// with NO -wal/-shm/-journal sidecar. If a regression opened for write first,
// the WAL pragma would create a -wal/-shm and the SHA-256 assertion would fail.
func TestAppOpen_ProbeRunsBeforeWritableOpen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cortex.db")
	buildOldV1DB(t, dbPath)
	isolateEnv(t, dbPath)

	hashBefore := fileSHA(t, dbPath)

	_, err := Open(context.Background(), Options{})
	if err == nil {
		t.Fatal("expected refusal on old v1 database")
	}

	// File bytes unchanged.
	hashAfter := fileSHA(t, dbPath)
	if hashAfter != hashBefore {
		t.Errorf("old DB mutated by app.Open refusal\n  before=%s\n  after =%s", hashBefore, hashAfter)
	}
	// No WAL/SHM/journal sidecar created (proves NewManager's WAL pragma never ran).
	for _, sfx := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(dbPath + sfx); err == nil {
			t.Errorf("sidecar %q created during refusal — probe did not run before the writable open", sfx)
		}
	}
}

// TestAppOpen_FreshPath_CreatesV2AndReopensCompatible proves a fresh path
// creates a clean cortex-v2 database (cortex_meta identity present) and that
// reopening the same path is idempotent (probe classifies it Compatible). This
// preserves local fresh-install behavior (REQ-DB-001) and keeps the
// create-then-reopen contract consistent.
func TestAppOpen_FreshPath_CreatesV2AndReopensCompatible(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cortex.db")
	isolateEnv(t, dbPath)

	// Fresh: path does not exist yet.
	a, err := Open(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Open() fresh path: %v", err)
	}

	// The created database must carry a cortex-v2 identity.
	var family string
	if err := a.DB.DB().QueryRow(`SELECT value FROM cortex_meta WHERE key = 'schema_family'`).Scan(&family); err != nil {
		t.Fatalf("read schema_family: %v", err)
	}
	if family != migration.SchemaFamilyCortexV2 {
		t.Errorf("schema_family = %q, want %q", family, migration.SchemaFamilyCortexV2)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen the same path: probe must classify it Compatible and startup succeeds.
	a2, err := Open(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Open() reopen existing v2 path: %v", err)
	}
	defer func() { _ = a2.Close() }()

	// A store operation must work on the reopened v2 database.
	ctx := context.Background()
	if err := a2.Stores.Sessions.Create(ctx, &domain.Session{
		ID:        "w32-reopen",
		Project:   "demo",
		Directory: ".",
	}); err != nil {
		t.Fatalf("Sessions.Create on reopened v2 db: %v", err)
	}
}

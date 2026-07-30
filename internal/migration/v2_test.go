// Package migration — v2 baseline tests (W3.1, REQ-DB-001).
//
// These tests drive the v2 clean-baseline migration runner through all six
// mandated scenarios: up, down (forward-only), fresh/idempotent,
// integrity-gate, path-not-writable, and v1-retired.
//
// STRICT TDD: this file was written BEFORE the implementation (v2.go) existed.
// Every test here must drive the public API surface of the v2 runner.
package migration

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

	_ "modernc.org/sqlite"
)

// --- helpers ---------------------------------------------------------------

// openMem opens an in-memory SQLite DB for testing.
func openMem(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// tableExists reports whether a table (or view or trigger) exists in the DB.
func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type IN ('table','view','trigger') AND name = ?`,
		name,
	).Scan(&n)
	return err == nil
}

// metaValue reads a key from cortex_meta. Returns ("", false) if absent.
func metaValue(t *testing.T, db *sql.DB, key string) (string, bool) {
	t.Helper()
	var val string
	err := db.QueryRow(`SELECT value FROM cortex_meta WHERE key = ?`, key).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatalf("read cortex_meta[%s]: %v", key, err)
	}
	return val, true
}

// fileSHA256 returns the hex-encoded SHA-256 of a file, or ("", false) if the
// file does not exist.
func fileSHA256(t *testing.T, path string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatalf("read file %s for sha256: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), true
}

// --- 1. UP: baseline applied to empty DB -----------------------------------

// TestV2Baseline_Up applies the v2 baseline to an empty in-memory DB and
// verifies that the full schema exists, cortex_meta carries the schema
// identity (family + version + checksum), and PRAGMA integrity_check passes.
func TestV2Baseline_Up(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()

	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}

	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Core tables must exist.
	for _, tbl := range []string{
		"cortex_meta", "sessions", "observations", "user_prompts",
		"edges", "importance_scores", "observation_vectors", "entity_links",
		"metrics", "quality_metrics", "temporal_snapshots", "sync_chunks",
		"search_feedback", "index_outbox", "index_state", "audit_events",
	} {
		if !tableExists(t, db, tbl) {
			t.Errorf("expected table %q to exist after Apply", tbl)
		}
	}

	// FTS virtual tables.
	for _, tbl := range []string{"observations_fts", "prompts_fts"} {
		if !tableExists(t, db, tbl) {
			t.Errorf("expected FTS table %q to exist after Apply", tbl)
		}
	}

	// FTS sync triggers.
	for _, trg := range []string{
		"obs_fts_insert", "obs_fts_delete", "obs_fts_update",
		"prompt_fts_insert", "prompt_fts_delete", "prompt_fts_update",
		"importance_init",
	} {
		if !tableExists(t, db, trg) {
			t.Errorf("expected trigger %q to exist after Apply", trg)
		}
	}

	// Schema identity recorded in cortex_meta.
	family, ok := metaValue(t, db, "schema_family")
	if !ok || family != SchemaFamilyCortexV2 {
		t.Errorf("schema_family = %q, ok=%v; want %q", family, ok, SchemaFamilyCortexV2)
	}
	version, ok := metaValue(t, db, "schema_version")
	if !ok || version != V2BaselineVersion {
		t.Errorf("schema_version = %q, ok=%v; want %q", version, ok, V2BaselineVersion)
	}
	checksum, ok := metaValue(t, db, "schema_checksum")
	if !ok || checksum == "" {
		t.Errorf("schema_checksum missing or empty; want non-empty SHA-256")
	}
	// Checksum must match the baseline's own identity.
	if checksum != baseline.Identity().Checksum {
		t.Errorf("recorded checksum %q != baseline checksum %q", checksum, baseline.Identity().Checksum)
	}

	// Integrity check must report "ok".
	var integ string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integ); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if integ != "ok" {
		t.Errorf("integrity_check = %q; want \"ok\"", integ)
	}
}

// TestV2Baseline_UpCorrectedTypeRegistry verifies that the corrected type
// registry from W1.1 is enforced: session_summary and passive are valid types.
func TestV2Baseline_UpCorrectedTypeRegistry(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()

	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Insert a session first (observations FK references sessions).
	if _, err := db.Exec(`INSERT INTO sessions (id, project, directory) VALUES ('s1', 'p', '/d')`); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	for _, typ := range []string{"session_summary", "passive"} {
		_, err := db.Exec(
			`INSERT INTO observations (session_id, type, title, content) VALUES ('s1', ?, 't', 'c')`,
			typ,
		)
		if err != nil {
			t.Errorf("type %q rejected by CHECK constraint: %v", typ, err)
		}
	}

	// An invalid type MUST be rejected by the CHECK constraint.
	_, err = db.Exec(
		`INSERT INTO observations (session_id, type, title, content) VALUES ('s1', 'invalid_type', 't', 'c')`,
	)
	if err == nil {
		t.Error("invalid type 'invalid_type' was accepted; CHECK constraint failed to reject")
	}
}

// --- 2. DOWN: forward-only, no mutation ------------------------------------

// TestV2Baseline_DownForwardOnly verifies that Down() returns a clear
// forward-only error and does NOT mutate the database: schema tables remain,
// SHA-256 of the file is unchanged, and no WAL/journal sidecar appears.
func TestV2Baseline_DownForwardOnly(t *testing.T) {
	ctx := context.Background()
	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}

	// Use a file-based DB so we can assert byte-level immutability.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	dsn := dbPath + "?_pragma=foreign_keys%3dON"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Snapshot the file hash + existence of WAL/journal before Down.
	hashBefore, _ := fileSHA256(t, dbPath)
	walBefore := fileExists(filepath.Join(dir, "test.db-wal"))
	journalBefore := fileExists(filepath.Join(dir, "test.db-journal"))

	// Down MUST return a forward-only error.
	err = baseline.Down(ctx, db)
	if err == nil {
		t.Fatal("Down() returned nil; expected a forward-only error")
	}
	if !errors.Is(err, ErrForwardOnly) {
		t.Errorf("Down() error = %v; want errors.Is ErrForwardOnly", err)
	}

	// File hash MUST be unchanged.
	hashAfter, exists := fileSHA256(t, dbPath)
	if !exists {
		t.Fatal("DB file disappeared after Down")
	}
	if hashAfter != hashBefore {
		t.Errorf("DB file SHA-256 changed after Down: before=%s after=%s", hashBefore, hashAfter)
	}

	// No new WAL/journal sidecar.
	if wal := fileExists(filepath.Join(dir, "test.db-wal")); wal && !walBefore {
		t.Error("WAL sidecar appeared after Down")
	}
	if journal := fileExists(filepath.Join(dir, "test.db-journal")); journal && !journalBefore {
		t.Error("journal sidecar appeared after Down")
	}

	// Schema MUST still be intact.
	if !tableExists(t, db, "cortex_meta") {
		t.Error("cortex_meta table disappeared after Down — Down mutated the schema")
	}
	if !tableExists(t, db, "observations") {
		t.Error("observations table disappeared after Down — Down mutated the schema")
	}
}

// --- 3. FRESH + IDEMPOTENT --------------------------------------------------

// TestV2Baseline_FreshAndIdempotent verifies that:
// (a) an empty/absent path gets a clean baseline; and
// (b) a second Apply on an already-initialized v2 DB is a no-op that does
//
//	NOT mutate the schema identity or data.
func TestV2Baseline_FreshAndIdempotent(t *testing.T) {
	ctx := context.Background()
	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fresh.db")
	dsn := dbPath + "?_pragma=foreign_keys%3dON"

	// (a) Fresh: file does not exist yet.
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("first Apply (fresh): %v", err)
	}
	if !fileExists(dbPath) {
		t.Fatal("fresh DB file was not created")
	}

	checksum1, _ := metaValue(t, db, "schema_checksum")

	// Insert some data to prove idempotent re-init does not wipe it.
	if _, err := db.Exec(`INSERT INTO sessions (id, project, directory) VALUES ('sx', 'p', '/d')`); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	// (b) Idempotent: second Apply must be a no-op.
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("second Apply (idempotent): %v", err)
	}

	// Checksum unchanged.
	checksum2, _ := metaValue(t, db, "schema_checksum")
	if checksum1 != checksum2 {
		t.Errorf("schema_checksum changed on re-init: %q -> %q", checksum1, checksum2)
	}

	// Data must survive the no-op re-init.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = 'sx'`).Scan(&count); err != nil {
		t.Fatalf("count sessions after re-init: %v", err)
	}
	if count != 1 {
		t.Errorf("session data lost after idempotent re-init: count=%d, want 1", count)
	}
}

// --- 4. INTEGRITY GATE ------------------------------------------------------

// TestV2Baseline_IntegrityGate verifies that the DB is only considered "ready"
// after integrity checks pass, and that a deliberately-tampered schema
// identity is detected (not silently accepted as ready).
func TestV2Baseline_IntegrityGate(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()

	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// A freshly-applied DB must pass VerifyIntegrity (ready).
	if err := baseline.VerifyIntegrity(ctx, db); err != nil {
		t.Errorf("VerifyIntegrity on fresh DB failed: %v", err)
	}

	// Tamper: corrupt the recorded checksum in cortex_meta.
	if _, err := db.Exec(
		`UPDATE cortex_meta SET value = 'tampered' WHERE key = 'schema_checksum'`,
	); err != nil {
		t.Fatalf("tamper checksum: %v", err)
	}

	// VerifyIntegrity MUST now fail (tampered identity detected).
	err = baseline.VerifyIntegrity(ctx, db)
	if err == nil {
		t.Fatal("VerifyIntegrity succeeded on tampered checksum; expected failure")
	}
	if !errors.Is(err, ErrSchemaTampered) {
		t.Errorf("VerifyIntegrity error = %v; want errors.Is ErrSchemaTampered", err)
	}

	// IsV2 MUST report false for the tampered DB (not ready).
	ready, err := baseline.IsV2(ctx, db)
	if err == nil {
		t.Errorf("IsV2 on tampered DB: err=nil, expected integrity/tamper error")
	}
	if ready {
		t.Error("IsV2 reported ready=true on a tampered DB; expected false")
	}
}

// --- 5. PATH NOT WRITABLE ---------------------------------------------------

// TestV2Baseline_PathNotWritable verifies that when the target path is not
// writable (parent directory cannot be created), InitV2Database fails BEFORE
// any mutation and leaves no partial DB file behind.
func TestV2Baseline_PathNotWritable(t *testing.T) {
	ctx := context.Background()

	// Create a blocker file; a DB path "under" it cannot have its parent
	// directory created (the parent component is a file, not a directory).
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("create blocker file: %v", err)
	}
	badPath := filepath.Join(blocker, "sub", "cortex.db")

	_, err := InitV2Database(ctx, badPath)
	if err == nil {
		t.Fatal("InitV2Database succeeded on an unwritable path; expected error")
	}

	// No partial DB file must be left behind.
	if fileExists(badPath) {
		t.Errorf("partial DB file created at unwritable path %s", badPath)
	}
}

// --- 6. V1 RETIRED ----------------------------------------------------------

// TestV2Registry_V1Retired verifies that v1 migration versions 001-014 are
// retired from the v2 line: they do not appear as active v2 migrations.
func TestV2Registry_V1Retired(t *testing.T) {
	reg, err := NewV2Registry()
	if err != nil {
		t.Fatalf("NewV2Registry: %v", err)
	}

	// Every v1 version 1-14 MUST be retired.
	for v := 1; v <= 14; v++ {
		if !reg.IsV1Retired(v) {
			t.Errorf("v1 migration %d should be retired in the v2 line", v)
		}
	}

	// The retired set MUST be exactly 1-14.
	retired := reg.RetiredV1Versions()
	if len(retired) != 14 {
		t.Errorf("RetiredV1Versions len = %d; want 14", len(retired))
	}

	// v2 migrations must NOT include any v1 version.
	for _, m := range reg.V2Migrations() {
		if m.Version >= 1 && m.Version <= 14 {
			t.Errorf("v2 migration list contains v1 version %d — should be retired", m.Version)
		}
	}
}

// --- 7. INIT FILE PATH (coverage + happy path) ------------------------------

// TestInitV2Database_FilePath verifies the file-based initialization path:
// a fresh file is created, the baseline is applied, and a second init on the
// same path is idempotent (no re-run, data preserved).
func TestInitV2Database_FilePath(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "v2", "cortex.db")

	// Fresh: file does not exist, parent dir must be created.
	db, err := InitV2Database(ctx, dbPath)
	if err != nil {
		t.Fatalf("InitV2Database (fresh): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if !fileExists(dbPath) {
		t.Fatal("DB file was not created")
	}

	// Verify schema identity is recorded.
	baseline, _ := NewV2Baseline()
	if err := baseline.VerifyIntegrity(ctx, db); err != nil {
		t.Errorf("VerifyIntegrity after InitV2Database: %v", err)
	}

	// Insert data to prove idempotency preserves it.
	if _, err := db.Exec(`INSERT INTO sessions (id, project, directory) VALUES ('s1', 'p', '/d')`); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	// Second init on the same path: idempotent, data survives.
	db2, err := InitV2Database(ctx, dbPath)
	if err != nil {
		t.Fatalf("InitV2Database (idempotent): %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })

	var count int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = 's1'`).Scan(&count); err != nil {
		t.Fatalf("count after re-init: %v", err)
	}
	if count != 1 {
		t.Errorf("data lost after idempotent re-init: count=%d", count)
	}
}

// TestInitV2Database_InMemory verifies the in-memory path.
func TestInitV2Database_InMemory(t *testing.T) {
	ctx := context.Background()
	db, err := InitV2Database(ctx, ":memory:")
	if err != nil {
		t.Fatalf("InitV2Database (:memory:): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	baseline, _ := NewV2Baseline()
	if err := baseline.VerifyIntegrity(ctx, db); err != nil {
		t.Errorf("VerifyIntegrity after in-memory init: %v", err)
	}
}

// TestV2Baseline_IncompatibleFamily verifies that Apply on a DB with a
// different schema family (non-cortex-v2) returns ErrIncompatibleDatabase.
func TestV2Baseline_IncompatibleFamily(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()

	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	// Tamper the schema_family to a foreign value.
	if _, err := db.Exec(`UPDATE cortex_meta SET value = 'foreign-db' WHERE key = 'schema_family'`); err != nil {
		t.Fatalf("tamper family: %v", err)
	}

	// Second Apply MUST refuse (incompatible family).
	err = baseline.Apply(ctx, db)
	if err == nil {
		t.Fatal("Apply on foreign-family DB returned nil; expected error")
	}
	if !errors.Is(err, ErrIncompatibleDatabase) {
		t.Errorf("Apply error = %v; want errors.Is ErrIncompatibleDatabase", err)
	}
}

// TestInitV2Database_DefaultPath verifies that DefaultV2DBPath resolves to a
// path under ~/.cortex/v2/ (distinct from the v1 path ~/.cortex/cortex.db).
func TestDefaultV2DBPath(t *testing.T) {
	p := DefaultV2DBPath()
	if !strings.HasSuffix(p, filepath.Join(".cortex", "v2", "cortex.db")) {
		t.Errorf("DefaultV2DBPath = %q; want suffix .cortex/v2/cortex.db", p)
	}
	// Must NOT be the v1 path.
	if strings.HasSuffix(p, filepath.Join(".cortex", "cortex.db")) &&
		!strings.Contains(p, filepath.Join("v2", "cortex.db")) {
		t.Errorf("DefaultV2DBPath = %q collides with v1 path", p)
	}
}

// fileExists is a small helper (test-only).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

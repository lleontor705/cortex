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

	v2migrations "github.com/lleontor705/cortex/migrations/v2"
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

// --- 8. V2 FOLLOW-UP MIGRATION LINE (002 handoff receipts) ------------------

// v2FollowUpLedgerTable is the additive ledger that tracks applied v2
// follow-up migrations (versions above the 001 baseline, e.g. 2002).
const v2FollowUpLedgerTable = "cortex_v2_migrations"

// v2LedgerChecksum reads the checksum recorded for a version in the follow-up
// ledger. Returns ("", false) when the ledger table or the version row is
// absent (e.g. a database created by an older runtime).
func v2LedgerChecksum(t *testing.T, db *sql.DB, version int) (string, bool) {
	t.Helper()
	var checksum string
	err := db.QueryRow(
		`SELECT checksum FROM `+v2FollowUpLedgerTable+` WHERE version = ?`, version,
	).Scan(&checksum)
	if err != nil {
		return "", false
	}
	return checksum, true
}

// v2LedgerRowCount returns the number of ledger rows recorded for a version.
func v2LedgerRowCount(t *testing.T, db *sql.DB, version int) int {
	t.Helper()
	var rows int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM `+v2FollowUpLedgerTable+` WHERE version = ?`, version,
	).Scan(&rows); err != nil {
		return 0
	}
	return rows
}

// buildBaselineOnlyDB simulates a database created by the PREVIOUS runtime:
// the 001 baseline and its schema identity exist, but no follow-up ledger and
// no handoff_receipts table. Upgrading such a database MUST be additive.
func buildBaselineOnlyDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openMem(t)
	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	if _, err := db.Exec(v2migrations.BaselineSQL); err != nil {
		t.Fatalf("exec baseline SQL: %v", err)
	}
	if err := writeIdentity(db, baseline.Identity()); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	return db
}

// TestV2Registry_LineIncludesHandoffReceipts verifies the v2 line is the
// baseline (2001) followed by the checksummed handoff-receipts follow-up
// (2002) and the project-artifacts follow-up (2003), and that every entry
// stays forward-only (no DownSQL).
func TestV2Registry_LineIncludesHandoffReceipts(t *testing.T) {
	reg, err := NewV2Registry()
	if err != nil {
		t.Fatalf("NewV2Registry: %v", err)
	}
	migrations := reg.V2Migrations()
	if len(migrations) != 3 {
		t.Fatalf("v2 line length = %d, want 3 (baseline + handoff receipts + project artifacts)", len(migrations))
	}
	if migrations[0].Version != 2001 || migrations[1].Version != 2002 || migrations[2].Version != 2003 {
		t.Fatalf("v2 line versions = [%d, %d, %d], want [2001, 2002, 2003]",
			migrations[0].Version, migrations[1].Version, migrations[2].Version)
	}
	if migrations[1].UpSQL == "" || migrations[2].UpSQL == "" {
		t.Error("follow-up migration has empty UpSQL")
	}
	for _, migration := range migrations {
		if migration.DownSQL != "" {
			t.Errorf("v2 migration %d defines DownSQL; the v2 line is forward-only", migration.Version)
		}
	}
}

// TestV2FollowUp_FreshApplyCreatesHandoffReceipts verifies a fresh v2 database
// receives the handoff_receipts table and exactly one ledger row with a
// non-empty checksum, and still passes the integrity gate.
func TestV2FollowUp_FreshApplyCreatesHandoffReceipts(t *testing.T) {
	ctx := context.Background()
	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	db := openMem(t)
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !tableExists(t, db, "handoff_receipts") {
		t.Fatal("handoff_receipts table missing after fresh Apply")
	}
	ledgerChecksum, ok := v2LedgerChecksum(t, db, 2002)
	if !ok || ledgerChecksum == "" {
		t.Fatalf("ledger row for 2002 missing or empty: checksum=%q ok=%v", ledgerChecksum, ok)
	}
	if rows := v2LedgerRowCount(t, db, 2002); rows != 1 {
		t.Errorf("ledger rows for 2002 = %d, want 1", rows)
	}
	if err := baseline.VerifyIntegrity(ctx, db); err != nil {
		t.Errorf("VerifyIntegrity with follow-ups applied: %v", err)
	}
}

// TestV2FollowUp_UpgradeFromBaselineOnly verifies an existing 001-only
// database (previous runtime) is upgraded additively: the follow-up is
// applied, pre-existing data survives, and the 001 identity is untouched.
func TestV2FollowUp_UpgradeFromBaselineOnly(t *testing.T) {
	ctx := context.Background()
	db := buildBaselineOnlyDB(t)
	if _, err := db.Exec(`INSERT INTO sessions (id, project, directory) VALUES ('old', 'p', '/d')`); err != nil {
		t.Fatalf("seed old-runtime data: %v", err)
	}

	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("upgrade Apply: %v", err)
	}

	if !tableExists(t, db, "handoff_receipts") {
		t.Fatal("handoff_receipts table missing after upgrade from 001-only database")
	}
	if _, ok := v2LedgerChecksum(t, db, 2002); !ok {
		t.Fatal("ledger row for 2002 missing after upgrade")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = 'old'`).Scan(&count); err != nil {
		t.Fatalf("count old data after upgrade: %v", err)
	}
	if count != 1 {
		t.Errorf("upgrade lost pre-existing data: count=%d, want 1", count)
	}
	version, _ := metaValue(t, db, "schema_version")
	if version != V2BaselineVersion {
		t.Errorf("schema_version = %q after upgrade, want %q (001 is immutable)", version, V2BaselineVersion)
	}
}

// TestV2FollowUp_ReapplyIsIdempotent verifies a second Apply records no
// duplicate ledger row, does not drift the checksum, and preserves data.
func TestV2FollowUp_ReapplyIsIdempotent(t *testing.T) {
	ctx := context.Background()
	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	db := openMem(t)
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	first, _ := v2LedgerChecksum(t, db, 2002)

	if _, err := db.Exec(`INSERT INTO sessions (id, project, directory) VALUES ('keep', 'p', '/d')`); err != nil {
		t.Fatalf("seed data: %v", err)
	}
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("reapply: %v", err)
	}

	second, _ := v2LedgerChecksum(t, db, 2002)
	if first == "" || first != second {
		t.Fatalf("ledger checksum drifted on reapply: first=%q second=%q", first, second)
	}
	if rows := v2LedgerRowCount(t, db, 2002); rows != 1 {
		t.Errorf("ledger rows for 2002 after reapply = %d, want 1", rows)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = 'keep'`).Scan(&count); err != nil {
		t.Fatalf("count data after reapply: %v", err)
	}
	if count != 1 {
		t.Errorf("data lost after reapply: count=%d, want 1", count)
	}
}

// TestV2FollowUp_ChecksumTamperFailsClosed verifies a tampered follow-up
// ledger checksum is refused by Apply AND VerifyIntegrity (fail closed).
func TestV2FollowUp_ChecksumTamperFailsClosed(t *testing.T) {
	ctx := context.Background()
	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	db := openMem(t)
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if _, err := db.Exec(
		`UPDATE ` + v2FollowUpLedgerTable + ` SET checksum = 'tampered' WHERE version = 2002`); err != nil {
		t.Fatalf("tamper follow-up checksum: %v", err)
	}

	if err := baseline.Apply(ctx, db); err == nil {
		t.Fatal("Apply succeeded with tampered follow-up checksum; expected fail-closed error")
	} else if !errors.Is(err, ErrSchemaTampered) {
		t.Errorf("Apply error = %v; want errors.Is ErrSchemaTampered", err)
	}
	if err := baseline.VerifyIntegrity(ctx, db); err == nil || !errors.Is(err, ErrSchemaTampered) {
		t.Errorf("VerifyIntegrity error = %v; want errors.Is ErrSchemaTampered", err)
	}
}

// TestV2FollowUp_FailureFailsClosedWithoutPartialState verifies that a
// follow-up whose DDL fails (stale unledgered artifact with the same table
// name) leaves NO partial state: no ledger row, no schema mutation, and the
// baseline identity intact. Startup must fail closed rather than half-apply.
func TestV2FollowUp_FailureFailsClosedWithoutPartialState(t *testing.T) {
	ctx := context.Background()
	db := buildBaselineOnlyDB(t)
	// Stale, unledgered artifact from an interrupted/foreign run.
	if _, err := db.Exec(`CREATE TABLE handoff_receipts (stub INTEGER)`); err != nil {
		t.Fatalf("create stale artifact: %v", err)
	}

	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	if err := baseline.Apply(ctx, db); err == nil {
		t.Fatal("Apply succeeded over a stale unledgered artifact; expected fail-closed error")
	}

	if _, ok := v2LedgerChecksum(t, db, 2002); ok {
		t.Error("ledger row for 2002 recorded despite failed follow-up")
	}
	if !tableExists(t, db, "handoff_receipts") {
		t.Error("stale artifact was mutated/dropped during the failed Apply")
	}
	family, _ := metaValue(t, db, "schema_family")
	if family != SchemaFamilyCortexV2 {
		t.Errorf("schema_family = %q after failed follow-up, want %q", family, SchemaFamilyCortexV2)
	}
}

// --- 9. V2 FOLLOW-UP LEDGER: FUTURE VERSIONS FAIL CLOSED (R1F) --------------

// v2SchemaSnapshot returns a deterministic dump of every schema object
// (type|name|SQL, sorted) for zero-mutation proofs.
func v2SchemaSnapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(
		`SELECT type || '|' || name || '|' || COALESCE(sql, '') FROM sqlite_master ` +
			`WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		t.Fatalf("read sqlite_master snapshot: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close sqlite_master snapshot rows: %v", err)
		}
	}()
	var b strings.Builder
	for rows.Next() {
		var entry string
		if err := rows.Scan(&entry); err != nil {
			t.Fatalf("scan sqlite_master snapshot: %v", err)
		}
		b.WriteString(entry)
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite_master snapshot: %v", err)
	}
	return b.String()
}

// TestV2FollowUp_FutureLedgerVersionFailsClosed verifies that a follow-up
// ledger recording a version NEWER than this runtime's head (e.g. 2004
// written by a newer runtime) is refused by BOTH Apply and VerifyIntegrity,
// with zero mutation: schema snapshot and ledger rows stay identical.
func TestV2FollowUp_FutureLedgerVersionFailsClosed(t *testing.T) {
	ctx := context.Background()
	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	db := openMem(t)
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// A newer runtime recorded follow-up 2004 in the ledger.
	if _, err := db.Exec(
		`INSERT INTO cortex_v2_migrations (version, name, checksum) VALUES (2004, 'future_runtime', 'future')`,
	); err != nil {
		t.Fatalf("seed future ledger row: %v", err)
	}
	snapshot := v2SchemaSnapshot(t, db)

	// Apply MUST fail closed instead of silently running below a newer head.
	if err := baseline.Apply(ctx, db); err == nil || !errors.Is(err, ErrFutureMigration) {
		t.Fatalf("Apply err=%v; want errors.Is ErrFutureMigration", err)
	}
	// VerifyIntegrity MUST fail closed too.
	if err := baseline.VerifyIntegrity(ctx, db); err == nil || !errors.Is(err, ErrFutureMigration) {
		t.Fatalf("VerifyIntegrity err=%v; want errors.Is ErrFutureMigration", err)
	}
	// Zero mutation: schema identical, both ledger rows intact.
	if after := v2SchemaSnapshot(t, db); after != snapshot {
		t.Error("schema changed during fail-closed Apply/VerifyIntegrity over a future ledger version")
	}
	if rows := v2LedgerRowCount(t, db, 2003); rows != 1 {
		t.Errorf("ledger rows for 2003 = %d after fail-closed Apply, want 1", rows)
	}
	if rows := v2LedgerRowCount(t, db, 2004); rows != 1 {
		t.Errorf("ledger rows for 2004 = %d after fail-closed Apply, want 1 (row must be preserved)", rows)
	}
}

// TestV2FollowUp_FutureVersionBlocksUpgradeBeforeDDL verifies the fail-closed
// future-version check runs BEFORE any follow-up DDL: on a baseline-only
// database whose ledger carries a 2004 row, the upgrade refuses without
// creating handoff_receipts/project artifacts and without recording 2002/2003.
func TestV2FollowUp_FutureVersionBlocksUpgradeBeforeDDL(t *testing.T) {
	ctx := context.Background()
	db := buildBaselineOnlyDB(t)
	if _, err := db.Exec(v2FollowUpLedgerDDL); err != nil {
		t.Fatalf("create follow-up ledger: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO cortex_v2_migrations (version, name, checksum) VALUES (2004, 'future_runtime', 'future')`,
	); err != nil {
		t.Fatalf("seed future ledger row: %v", err)
	}

	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	if err := baseline.Apply(ctx, db); err == nil || !errors.Is(err, ErrFutureMigration) {
		t.Fatalf("upgrade Apply err=%v; want errors.Is ErrFutureMigration", err)
	}

	if tableExists(t, db, "handoff_receipts") {
		t.Error("2002 DDL executed despite a future ledger version; the guard must precede any DDL")
	}
	if tableExists(t, db, "project_artifacts") {
		t.Error("2003 DDL executed despite a future ledger version; the guard must precede any DDL")
	}
	if _, ok := v2LedgerChecksum(t, db, 2002); ok {
		t.Error("ledger row for 2002 recorded despite fail-closed upgrade")
	}
	if _, ok := v2LedgerChecksum(t, db, 2003); ok {
		t.Error("ledger row for 2003 recorded despite fail-closed upgrade")
	}
	if rows := v2LedgerRowCount(t, db, 2004); rows != 1 {
		t.Errorf("ledger rows for 2004 = %d after fail-closed upgrade, want 1", rows)
	}
	if err := baseline.VerifyIntegrity(ctx, db); err == nil || !errors.Is(err, ErrFutureMigration) {
		t.Fatalf("VerifyIntegrity err=%v; want errors.Is ErrFutureMigration", err)
	}
}

// --- 10. V2 FOLLOW-UP 003: PROJECT CONTEXT ARTIFACTS (SQLite) ---------------

// artifactDigest64 is a 64-char hex digest placeholder valid for the schema
// CHECK constraints.
const artifactDigest64 = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

// enableSQLiteFKs turns on foreign key enforcement for the connection
// (modernc.org/sqlite defaults to DEFERRED/off per connection).
func enableSQLiteFKs(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable foreign_keys pragma: %v", err)
	}
}

// seedArtifact inserts one minimal active artifact with revision 1, its
// activation pointer, and a created event, then returns the artifact id.
func seedArtifact(t *testing.T, db *sql.DB, suffix string) int64 {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO project_artifacts
			(public_id, project, kind, key, source_scope, current_revision,
			 content_bytes, metadata_bytes, digest)
		VALUES ('pub-`+suffix+`', 'proj', 'skill', 'key-`+suffix+`', 'project', 1, 3, 5, ?)`,
		artifactDigest64)
	if err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("artifact id: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO project_artifact_revisions
			(public_id, artifact_id, revision, content, content_bytes, metadata, metadata_bytes, digest, created_by)
		VALUES ('rev-`+suffix+`', ?, 1, 'abc', 3, '{}', 2, ?, 'actor')`, id, artifactDigest64); err != nil {
		t.Fatalf("seed revision: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO project_artifact_activations (artifact_id, revision, activation_revision, activated_by)
		VALUES (?, 1, 1, 'actor')`, id); err != nil {
		t.Fatalf("seed activation: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO project_artifact_events (artifact_id, event_type, revision, actor, payload)
		VALUES (?, 'created', 1, 'actor', '{}')`, id); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return id
}

// TestV2ProjectArtifacts_FreshUpgradeReapplyTamper covers the migration
// lifecycle for follow-up 2003: fresh databases create the artifact tables in
// the same transaction, 001-only databases upgrade additively with data
// preserved, reapply is idempotent with a stable ledger checksum, and a
// tampered ledger checksum fails closed on Apply and VerifyIntegrity.
func TestV2ProjectArtifacts_FreshUpgradeReapplyTamper(t *testing.T) {
	ctx := context.Background()
	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	wantChecksum := hex.EncodeToString(sumSHA256(t, v2migrations.ProjectArtifactsSQL))

	t.Run("fresh", func(t *testing.T) {
		db := openMem(t)
		if err := baseline.Apply(ctx, db); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		for _, table := range []string{
			"project_artifacts", "project_artifact_revisions", "project_artifact_events",
			"project_artifact_activations", "project_artifact_idempotency", "project_storage_usage",
		} {
			if !tableExists(t, db, table) {
				t.Errorf("table %s missing after fresh Apply", table)
			}
		}
		got, ok := v2LedgerChecksum(t, db, 2003)
		if !ok || got != wantChecksum {
			t.Fatalf("ledger 2003 checksum=%q ok=%v; want %q", got, ok, wantChecksum)
		}
		if err := baseline.VerifyIntegrity(ctx, db); err != nil {
			t.Fatalf("VerifyIntegrity after fresh apply: %v", err)
		}
	})

	t.Run("upgrade from 001-only", func(t *testing.T) {
		db := buildBaselineOnlyDB(t)
		if _, err := db.Exec(`INSERT INTO sessions (id, project, directory) VALUES ('old003', 'p', '/d')`); err != nil {
			t.Fatalf("seed old-runtime data: %v", err)
		}
		if err := baseline.Apply(ctx, db); err != nil {
			t.Fatalf("upgrade Apply: %v", err)
		}
		if !tableExists(t, db, "project_artifacts") {
			t.Fatal("project_artifacts missing after upgrade from 001-only database")
		}
		if _, ok := v2LedgerChecksum(t, db, 2003); !ok {
			t.Fatal("ledger row for 2003 missing after upgrade")
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = 'old003'`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("upgrade lost pre-existing data: count=%d err=%v", count, err)
		}
		if version, _ := metaValue(t, db, "schema_version"); version != V2BaselineVersion {
			t.Errorf("schema_version = %q after upgrade, want %q", version, V2BaselineVersion)
		}
	})

	t.Run("reapply is idempotent", func(t *testing.T) {
		db := openMem(t)
		if err := baseline.Apply(ctx, db); err != nil {
			t.Fatalf("first Apply: %v", err)
		}
		first, _ := v2LedgerChecksum(t, db, 2003)
		if _, err := db.Exec(`INSERT INTO sessions (id, project, directory) VALUES ('keep003', 'p', '/d')`); err != nil {
			t.Fatalf("seed data: %v", err)
		}
		if err := baseline.Apply(ctx, db); err != nil {
			t.Fatalf("reapply: %v", err)
		}
		second, _ := v2LedgerChecksum(t, db, 2003)
		if first == "" || first != second {
			t.Fatalf("ledger checksum drifted on reapply: first=%q second=%q", first, second)
		}
		if rows := v2LedgerRowCount(t, db, 2003); rows != 1 {
			t.Errorf("ledger rows for 2003 after reapply = %d, want 1", rows)
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = 'keep003'`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("data lost after reapply: count=%d err=%v", count, err)
		}
	})

	t.Run("checksum tamper fails closed", func(t *testing.T) {
		db := openMem(t)
		if err := baseline.Apply(ctx, db); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if _, err := db.Exec(`UPDATE cortex_v2_migrations SET checksum = 'tampered' WHERE version = 2003`); err != nil {
			t.Fatalf("tamper: %v", err)
		}
		if err := baseline.Apply(ctx, db); err == nil || !errors.Is(err, ErrSchemaTampered) {
			t.Fatalf("Apply err=%v; want errors.Is ErrSchemaTampered", err)
		}
		if err := baseline.VerifyIntegrity(ctx, db); err == nil || !errors.Is(err, ErrSchemaTampered) {
			t.Fatalf("VerifyIntegrity err=%v; want errors.Is ErrSchemaTampered", err)
		}
	})
}

// sumSHA256 is a test helper returning the raw SHA-256 sum of a string.
func sumSHA256(t *testing.T, s string) []byte {
	t.Helper()
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// TestV2ProjectArtifacts_SoftDeleteRetainsHistory proves soft delete is a
// pure state transition: the deleted triple is enforced, and revisions,
// events, activations, idempotency receipts, and usage counters survive.
func TestV2ProjectArtifacts_SoftDeleteRetainsHistory(t *testing.T) {
	ctx := context.Background()
	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	db := openMem(t)
	enableSQLiteFKs(t, db)
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	id := seedArtifact(t, db, "soft")

	// A partial delete (missing actor/reason) is rejected by the CHECK.
	if _, err := db.Exec(`UPDATE project_artifacts SET status='deleted', deleted_at=datetime('now'), deleted_by='actor' WHERE id=?`, id); err == nil {
		t.Fatal("soft delete without reason accepted; want CHECK violation")
	}
	if _, err := db.Exec(`UPDATE project_artifacts SET status='deleted', deleted_at=datetime('now'), deleted_by='actor', delete_reason='cleanup' WHERE id=?`, id); err != nil {
		t.Fatalf("full soft delete rejected: %v", err)
	}

	for _, probe := range []struct{ table string }{
		{"project_artifact_revisions"}, {"project_artifact_events"}, {"project_artifact_activations"},
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM `+probe.table+` WHERE artifact_id = ?`, id).Scan(&count); err != nil || count == 0 {
			t.Errorf("%s lost after soft delete: count=%d err=%v", probe.table, count, err)
		}
	}

	// The active-key window is free after soft delete: a fresh artifact may
	// take the same (project, scope, kind, key) coordinates.
	res, err := db.Exec(`
		INSERT INTO project_artifacts (public_id, project, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
		VALUES ('pub-soft2', 'proj', 'skill', 'key-soft', 'project', 1, 3, 5, ?)`, artifactDigest64)
	if err != nil {
		t.Fatalf("re-create under freed active coordinates rejected: %v", err)
	}
	newID, _ := res.LastInsertId()
	if newID == id {
		t.Fatal("re-created artifact reused the soft-deleted row")
	}
}

// TestV2ProjectArtifacts_ActiveKeyUniqueness proves at most one active
// artifact exists per coordinate: per (project, kind, key) for project
// scope and per (kind, key) for workspace defaults (absent project).
func TestV2ProjectArtifacts_ActiveKeyUniqueness(t *testing.T) {
	ctx := context.Background()
	baseline, _ := NewV2Baseline()
	db := openMem(t)
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	seedArtifact(t, db, "uniq")
	if _, err := db.Exec(`
		INSERT INTO project_artifacts (public_id, project, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
		VALUES ('pub-clash', 'proj', 'skill', 'key-uniq', 'project', 1, 3, 5, ?)`, artifactDigest64); err == nil {
		t.Fatal("duplicate active (project,scope,kind,key) accepted; want UNIQUE violation")
	}
	// A workspace default under the same key is a distinct resolution
	// coordinate and must be representable with an absent project.
	if _, err := db.Exec(`
		INSERT INTO project_artifacts (public_id, project, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
		VALUES ('pub-wsd', NULL, 'skill', 'key-uniq', 'workspace_default', 1, 3, 5, ?)`, artifactDigest64); err != nil {
		t.Fatalf("workspace_default scope rejected under the same key: %v", err)
	}
	// Exactly one active workspace default per (kind, key) in the local
	// workspace.
	if _, err := db.Exec(`
		INSERT INTO project_artifacts (public_id, project, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
		VALUES ('pub-wsd2', NULL, 'skill', 'key-uniq', 'workspace_default', 1, 3, 5, ?)`, artifactDigest64); err == nil {
		t.Fatal("duplicate active workspace_default (kind,key) accepted; want UNIQUE violation")
	}
	// The same key under a different project is a distinct coordinate.
	if _, err := db.Exec(`
		INSERT INTO project_artifacts (public_id, project, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
		VALUES ('pub-other', 'proj2', 'skill', 'key-uniq', 'project', 1, 3, 5, ?)`, artifactDigest64); err != nil {
		t.Fatalf("same key under a different project rejected: %v", err)
	}
	// Source scope and project presence must agree in both directions.
	if _, err := db.Exec(`
		INSERT INTO project_artifacts (public_id, project, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
		VALUES ('pub-wsd-proj', 'proj', 'skill', 'key-wsd-proj', 'workspace_default', 1, 3, 5, ?)`, artifactDigest64); err == nil {
		t.Fatal("workspace_default with a project accepted; want CHECK violation")
	}
	if _, err := db.Exec(`
		INSERT INTO project_artifacts (public_id, project, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
		VALUES ('pub-proj-null', NULL, 'skill', 'key-proj-null', 'project', 1, 3, 5, ?)`, artifactDigest64); err == nil {
		t.Fatal("project scope without a project accepted; want CHECK violation")
	}
}

// TestV2ProjectArtifacts_ImmutableRevisionsAndEvents proves the append-only
// history guard aborts UPDATE and DELETE on both history tables.
func TestV2ProjectArtifacts_ImmutableRevisionsAndEvents(t *testing.T) {
	ctx := context.Background()
	baseline, _ := NewV2Baseline()
	db := openMem(t)
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	id := seedArtifact(t, db, "imm")
	if _, err := db.Exec(`UPDATE project_artifact_revisions SET content = 'rewritten' WHERE artifact_id = ?`, id); err == nil {
		t.Fatal("revision UPDATE accepted; want immutability abort")
	}
	if _, err := db.Exec(`DELETE FROM project_artifact_revisions WHERE artifact_id = ?`, id); err == nil {
		t.Fatal("revision DELETE accepted; want immutability abort")
	}
	if _, err := db.Exec(`UPDATE project_artifact_events SET payload = 'rewritten' WHERE artifact_id = ?`, id); err == nil {
		t.Fatal("event UPDATE accepted; want immutability abort")
	}
	if _, err := db.Exec(`DELETE FROM project_artifact_events WHERE artifact_id = ?`, id); err == nil {
		t.Fatal("event DELETE accepted; want immutability abort")
	}
}

// TestV2ProjectArtifacts_HardDeleteRestricted proves RESTRICT semantics:
// hard-deleting an artifact with history, an activation, or an idempotency
// receipt fails closed, and hard-deleting the parent project rows is not
// part of this schema (project scope is a TEXT key, not an FK).
func TestV2ProjectArtifacts_HardDeleteRestricted(t *testing.T) {
	ctx := context.Background()
	baseline, _ := NewV2Baseline()
	db := openMem(t)
	enableSQLiteFKs(t, db)
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	id := seedArtifact(t, db, "restrict")
	if _, err := db.Exec(`DELETE FROM project_artifacts WHERE id = ?`, id); err == nil {
		t.Fatal("hard DELETE of artifact with history accepted; want FOREIGN KEY RESTRICT violation")
	}
	// With history gone the artifact row itself remains protected by its
	// idempotency receipt (durable evidence is never cascade-removed). The
	// committed receipt stores the exact result revision of the same
	// coordinate's artifact.
	if _, err := db.Exec(`
		INSERT INTO project_artifact_idempotency (project, scope, idem_key, payload_hash, state, artifact_id, initial_status, result_revision, committed_at)
		VALUES ('proj', 'artifact:save', 'k1', x'`+"0000000000000000000000000000000000000000000000000000000000000000"+`', 'committed', ?, 'created', 1, datetime('now'))`, id); err != nil {
		t.Fatalf("seed committed idempotency receipt: %v", err)
	}
	// Activation pointers are retained evidence too (REQ-RET-001): removal
	// is not a legitimate transition, and the artifact row stays restricted
	// by its immutable revisions while they exist.
	if _, err := db.Exec(`DELETE FROM project_artifact_activations WHERE artifact_id = ?`, id); err == nil {
		t.Fatal("activation pointer deletion accepted; want retention abort")
	}
	if _, err := db.Exec(`DELETE FROM project_artifact_revisions WHERE artifact_id = ?`, id); err == nil {
		t.Fatal("revision hard delete accepted despite immutability trigger")
	}
}

// TestV2ProjectArtifacts_ActivationAndIdempotencyShapes proves exactly one
// activation pointer per artifact guarded by the monotonic activation CAS
// token. The pointer is the authoritative token: the artifact-level mirror
// is synced from it and can only be written to the pointer's current token,
// so the two can never drift. The durable pending/committed receipt state
// machine stores the exact immutable result revision of the SAME
// coordinate's artifact for exact replay.
func TestV2ProjectArtifacts_ActivationAndIdempotencyShapes(t *testing.T) {
	ctx := context.Background()
	baseline, _ := NewV2Baseline()
	db := openMem(t)
	enableSQLiteFKs(t, db)
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	id := seedArtifact(t, db, "act")
	if _, err := db.Exec(`
		INSERT INTO project_artifact_revisions (public_id, artifact_id, revision, content, content_bytes, metadata, metadata_bytes, digest, created_by)
		VALUES ('rev-act-2', ?, 2, 'def', 3, '{}', 2, ?, 'actor')`, id, artifactDigest64); err != nil {
		t.Fatalf("seed revision 2: %v", err)
	}
	// Seeding the pointer auto-syncs the artifact mirror to its token.
	var mirror int64
	if err := db.QueryRow(`SELECT activation_revision FROM project_artifacts WHERE id = ?`, id).Scan(&mirror); err != nil || mirror != 1 {
		t.Fatalf("artifact mirror after pointer insert=%d err=%v; want synced token 1", mirror, err)
	}
	if _, err := db.Exec(`
		INSERT INTO project_artifact_activations (artifact_id, revision, activation_revision, activated_by)
		VALUES (?, 2, 1, 'actor')`, id); err == nil {
		t.Fatal("second activation pointer accepted; want PRIMARY KEY violation on artifact_id")
	}
	// Moving the pointer requires a strictly greater activation CAS token.
	if _, err := db.Exec(`UPDATE project_artifact_activations SET revision = 2 WHERE artifact_id = ?`, id); err == nil {
		t.Fatal("activation pointer moved without a token increment; want CAS abort")
	}
	// The mirror may not drift: it can only be set to the pointer's current
	// token, and only the pointer move advances it.
	if _, err := db.Exec(`UPDATE project_artifacts SET activation_revision = 2 WHERE id = ?`, id); err == nil {
		t.Fatal("direct mirror advance accepted; want authoritative-pointer abort")
	}
	if _, err := db.Exec(`
		UPDATE project_artifact_activations SET revision = 2, activation_revision = 2 WHERE artifact_id = ?`, id); err != nil {
		t.Fatalf("activation pointer move with token increment rejected: %v", err)
	}
	if err := db.QueryRow(`SELECT activation_revision FROM project_artifacts WHERE id = ?`, id).Scan(&mirror); err != nil || mirror != 2 {
		t.Fatalf("artifact mirror after pointer move=%d err=%v; want synced token 2", mirror, err)
	}
	if _, err := db.Exec(`UPDATE project_artifacts SET activation_revision = 3 WHERE id = ?`, id); err == nil {
		t.Fatal("direct mirror advance accepted after pointer move; want authoritative-pointer abort")
	}
	if _, err := db.Exec(`
		UPDATE project_artifact_activations SET revision = 1, activation_revision = 1 WHERE artifact_id = ?`, id); err == nil {
		t.Fatal("activation token regression accepted; want CAS abort")
	}
	if _, err := db.Exec(`UPDATE project_artifacts SET activation_revision = 1 WHERE id = ?`, id); err == nil {
		t.Fatal("artifact activation_revision regression accepted; want monotonic abort")
	}
	// Activation may not point at a revision that does not exist, even with
	// a fresh token.
	if _, err := db.Exec(`
		UPDATE project_artifact_activations SET revision = 3, activation_revision = 3 WHERE artifact_id = ?`, id); err == nil {
		t.Fatal("activation pointer moved to a missing revision; want FOREIGN KEY violation")
	}

	// Pending receipts reserve their coordinate namespace durably and carry
	// no result.
	if _, err := db.Exec(`
		INSERT INTO project_artifact_idempotency (project, scope, idem_key, payload_hash, state)
		VALUES ('proj', 'artifact:save', 'k2', x'` + "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20" + `', 'pending')`); err != nil {
		t.Fatalf("seed pending receipt: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO project_artifact_idempotency (project, scope, idem_key, payload_hash, state)
		VALUES ('proj', 'artifact:save', 'k2', x'` + "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20" + `', 'pending')`); err == nil {
		t.Fatal("duplicate (project, scope, idem_key) accepted; want UNIQUE violation")
	}
	// The same key under a different coordinate is a distinct namespace.
	if _, err := db.Exec(`
		INSERT INTO project_artifact_idempotency (project, scope, idem_key, payload_hash, state)
		VALUES ('other', 'artifact:save', 'k2', x'` + "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20" + `', 'pending')`); err != nil {
		t.Fatalf("same key under another project rejected: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE project_artifact_idempotency SET artifact_id = ? WHERE project='proj' AND scope='artifact:save' AND idem_key='k2'`, id); err == nil {
		t.Fatal("pending receipt accepted a partial result; want commit-guard abort")
	}
	// Committing without the exact result revision is rejected: replay must
	// return the original revision, not just the original artifact.
	if _, err := db.Exec(`
		UPDATE project_artifact_idempotency
		   SET state='committed', artifact_id=?, initial_status='created', committed_at=datetime('now')
		 WHERE project='proj' AND scope='artifact:save' AND idem_key='k2'`, id); err == nil {
		t.Fatal("committed receipt without result_revision accepted; want CHECK violation")
	}
	// The result revision must reference an existing revision of that
	// exact artifact.
	if _, err := db.Exec(`
		UPDATE project_artifact_idempotency
		   SET state='committed', artifact_id=?, initial_status='created', result_revision=99, committed_at=datetime('now')
		 WHERE project='proj' AND scope='artifact:save' AND idem_key='k2'`, id); err == nil {
		t.Fatal("committed receipt referencing a missing revision accepted; want exact-revision abort")
	}
	// The result must reference an artifact of the SAME coordinate: an
	// artifact from another project cannot satisfy this receipt.
	otherRes, err := db.Exec(`
		INSERT INTO project_artifacts (public_id, project, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
		VALUES ('pub-act-other', 'other', 'skill', 'key-act-other', 'project', 1, 3, 5, ?)`, artifactDigest64)
	if err != nil {
		t.Fatalf("seed other-project artifact: %v", err)
	}
	otherID, _ := otherRes.LastInsertId()
	if _, err := db.Exec(`
		INSERT INTO project_artifact_revisions (public_id, artifact_id, revision, content, content_bytes, metadata, metadata_bytes, digest, created_by)
		VALUES ('rev-act-other', ?, 1, 'xyz', 3, '{}', 2, ?, 'actor')`, otherID, artifactDigest64); err != nil {
		t.Fatalf("seed other-project revision: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE project_artifact_idempotency
		   SET state='committed', artifact_id=?, initial_status='created', result_revision=1, committed_at=datetime('now')
		 WHERE project='proj' AND scope='artifact:save' AND idem_key='k2'`, otherID); err == nil {
		t.Fatal("committed receipt referencing another coordinate's artifact accepted; want same-coordinate abort")
	}
	if _, err := db.Exec(`
		UPDATE project_artifact_idempotency
		   SET state='committed', artifact_id=?, initial_status='created', result_revision=1, committed_at=datetime('now')
		 WHERE project='proj' AND scope='artifact:save' AND idem_key='k2'`, id); err != nil {
		t.Fatalf("commit receipt rejected: %v", err)
	}
	// The committed result is immutable: exact replay evidence never moves.
	if _, err := db.Exec(`
		UPDATE project_artifact_idempotency SET result_revision = 9 WHERE project='proj' AND scope='artifact:save' AND idem_key='k2'`); err == nil {
		t.Fatal("committed receipt mutation accepted; want immutability abort")
	}
	if _, err := db.Exec(`DELETE FROM project_artifact_idempotency WHERE project='proj' AND scope='artifact:save' AND idem_key='k2'`); err == nil {
		t.Fatal("committed receipt deletion accepted; want retention abort")
	}
	var replay int
	if err := db.QueryRow(`
		SELECT result_revision FROM project_artifact_idempotency
		 WHERE project='proj' AND scope='artifact:save' AND idem_key='k2' AND state='committed'`).Scan(&replay); err != nil || replay != 1 {
		t.Fatalf("committed result_revision=%d err=%v; want exact replay revision 1", replay, err)
	}
	// A committed receipt cannot be fabricated by direct INSERT either:
	// the result must reference an existing revision of the same
	// coordinate's artifact.
	if _, err := db.Exec(`
		INSERT INTO project_artifact_idempotency (project, scope, idem_key, payload_hash, state, artifact_id, initial_status, result_revision, committed_at)
		VALUES ('proj', 'artifact:save', 'fabricated', x'`+"0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"+`', 'committed', ?, 'created', 42, datetime('now'))`, id); err == nil {
		t.Fatal("fabricated committed receipt with a phantom result revision accepted; want exact-revision abort")
	}
	if _, err := db.Exec(`
		INSERT INTO project_artifact_idempotency (project, scope, idem_key, payload_hash, state, artifact_id, initial_status, result_revision, committed_at)
		VALUES ('other', 'artifact:save', 'fabricated', x'`+"0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"+`', 'committed', ?, 'created', 1, datetime('now'))`, id); err == nil {
		t.Fatal("fabricated committed receipt crossing coordinates accepted; want same-coordinate abort")
	}
	if _, err := db.Exec(`
		INSERT INTO project_artifact_idempotency (project, scope, idem_key, payload_hash, state, artifact_id, initial_status, result_revision, committed_at)
		VALUES ('proj', 'artifact:save', 'fabricated-ok', x'`+"0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"+`', 'committed', ?, 'created', 1, datetime('now'))`, id); err != nil {
		t.Fatalf("committed receipt insert with a valid same-coordinate result rejected: %v", err)
	}
}

// TestV2ProjectArtifacts_UsageCounters proves the scope-coordinate usage
// counters accumulate transactionally, reject negative drift, and keep
// project rows and the single workspace-default row separate.
func TestV2ProjectArtifacts_UsageCounters(t *testing.T) {
	ctx := context.Background()
	baseline, _ := NewV2Baseline()
	db := openMem(t)
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO project_storage_usage (source_scope, project, content_bytes, metadata_bytes, event_bytes)
		VALUES ('project', 'proj', 100, 40, 2)`); err != nil {
		t.Fatalf("seed usage: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE project_storage_usage
		   SET content_bytes = content_bytes + 50,
		       metadata_bytes = metadata_bytes + 10,
		       event_bytes = event_bytes + 1,
		       updated_at = datetime('now')
		 WHERE source_scope = 'project' AND project = 'proj'`); err != nil {
		t.Fatalf("accumulate usage: %v", err)
	}
	var content, metadata, events int
	if err := db.QueryRow(`
		SELECT content_bytes, metadata_bytes, event_bytes FROM project_storage_usage
		 WHERE source_scope='project' AND project='proj'`).Scan(&content, &metadata, &events); err != nil {
		t.Fatalf("read usage: %v", err)
	}
	if content != 150 || metadata != 50 || events != 3 {
		t.Fatalf("usage counters = %d/%d/%d; want 150/50/3", content, metadata, events)
	}
	if _, err := db.Exec(`UPDATE project_storage_usage SET content_bytes = -1 WHERE project='proj'`); err == nil {
		t.Fatal("negative usage counter accepted; want CHECK violation")
	}
	// Workspace defaults are tracked under an absent project, exactly one row.
	if _, err := db.Exec(`
		INSERT INTO project_storage_usage (source_scope, project, content_bytes)
		VALUES ('workspace_default', NULL, 7)`); err != nil {
		t.Fatalf("seed workspace-default usage: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO project_storage_usage (source_scope, project, content_bytes)
		VALUES ('workspace_default', NULL, 7)`); err == nil {
		t.Fatal("second workspace-default counter row accepted; want UNIQUE violation")
	}
	if _, err := db.Exec(`
		INSERT INTO project_storage_usage (source_scope, project, content_bytes)
		VALUES ('workspace_default', 'proj', 7)`); err == nil {
		t.Fatal("workspace-default counter with a project accepted; want CHECK violation")
	}
	if _, err := db.Exec(`
		INSERT INTO project_storage_usage (source_scope, project, content_bytes)
		VALUES ('project', 'proj', 1)`); err == nil {
		t.Fatal("duplicate project counter row accepted; want UNIQUE violation")
	}
}

// TestV2ProjectArtifacts_UsageNoReset proves the storage quota counters
// cannot be reset or removed: retention is indefinite, so byte totals never
// decrease and counter rows are never deleted (REQ-RET-001/REQ-RET-003).
func TestV2ProjectArtifacts_UsageNoReset(t *testing.T) {
	ctx := context.Background()
	baseline, _ := NewV2Baseline()
	db := openMem(t)
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO project_storage_usage (source_scope, project, content_bytes, metadata_bytes, event_bytes)
		VALUES ('project', 'proj', 100, 40, 2)`); err != nil {
		t.Fatalf("seed usage: %v", err)
	}
	if _, err := db.Exec(`UPDATE project_storage_usage SET content_bytes = 0 WHERE project='proj'`); err == nil {
		t.Fatal("usage counter zeroed; want monotonic abort (quota reset)")
	}
	if _, err := db.Exec(`UPDATE project_storage_usage SET event_bytes = event_bytes - 1 WHERE project='proj'`); err == nil {
		t.Fatal("usage counter decrease accepted; want monotonic abort")
	}
	if _, err := db.Exec(`DELETE FROM project_storage_usage WHERE project='proj'`); err == nil {
		t.Fatal("usage counter row deleted; want retention abort")
	}
	// Monotonic folds stay legal.
	if _, err := db.Exec(`UPDATE project_storage_usage SET content_bytes = content_bytes + 1 WHERE project='proj'`); err != nil {
		t.Fatalf("monotonic counter fold rejected: %v", err)
	}
}

// TestV2ProjectArtifacts_UsageCoordinatesImmutable proves the quota
// coordinate (source_scope, project) of a usage row is frozen at insert:
// moving a row to another project or to/from the workspace-default
// coordinate aborts before any counter check, so accumulated usage can
// never be relocated away or shadowed by a fresh coordinate (the
// move-then-reinsert quota reset). Legal usage updates keep passing:
// updated_at refreshes, nondecreasing counter folds, and same-value
// coordinate rewrites.
func TestV2ProjectArtifacts_UsageCoordinatesImmutable(t *testing.T) {
	ctx := context.Background()
	// freshUsageDB gives every subtest its own database so a refused or
	// (pre-fix) accepted move in one subtest cannot contaminate the others.
	freshUsageDB := func(t *testing.T) *sql.DB {
		t.Helper()
		db := openMem(t)
		baseline, _ := NewV2Baseline()
		if err := baseline.Apply(ctx, db); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return db
	}
	seedUsage := func(t *testing.T, db *sql.DB) {
		t.Helper()
		if _, err := db.Exec(`
			INSERT INTO project_storage_usage (source_scope, project, content_bytes, metadata_bytes, event_bytes)
			VALUES ('project', 'proj', 100, 40, 2)`); err != nil {
			t.Fatalf("seed project usage: %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO project_storage_usage (source_scope, project, content_bytes, metadata_bytes, event_bytes)
			VALUES ('workspace_default', NULL, 7, 3, 1)`); err != nil {
			t.Fatalf("seed workspace-default usage: %v", err)
		}
	}
	requireCoordinateAbort := func(t *testing.T, what string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s accepted; want coordinate immutability abort", what)
		}
		if !strings.Contains(err.Error(), "coordinates are immutable") {
			t.Fatalf("%s aborted with unexpected error: %v", what, err)
		}
	}
	readCounters := func(t *testing.T, db *sql.DB, scope string, project any) (int, int, int) {
		t.Helper()
		var content, metadata, events int
		if err := db.QueryRow(`
			SELECT content_bytes, metadata_bytes, event_bytes FROM project_storage_usage
			 WHERE source_scope = ? AND project IS ?`, scope, project).Scan(&content, &metadata, &events); err != nil {
			t.Fatalf("read %s usage counters: %v", scope, err)
		}
		return content, metadata, events
	}

	t.Run("coordinate moves abort", func(t *testing.T) {
		db := freshUsageDB(t)
		seedUsage(t, db)
		_, err := db.Exec(`UPDATE project_storage_usage SET project = 'other' WHERE source_scope='project' AND project='proj'`)
		requireCoordinateAbort(t, "project rename move", err)
		_, err = db.Exec(`UPDATE project_storage_usage SET source_scope='workspace_default', project=NULL WHERE source_scope='project' AND project='proj'`)
		requireCoordinateAbort(t, "project-to-workspace-default move", err)
		_, err = db.Exec(`UPDATE project_storage_usage SET source_scope='project', project='moved' WHERE source_scope='workspace_default'`)
		requireCoordinateAbort(t, "workspace-default-to-project move", err)
		// Nothing moved: both rows keep their original coordinates and totals.
		if c, m, e := readCounters(t, db, "project", "proj"); c != 100 || m != 40 || e != 2 {
			t.Fatalf("project counters after refused moves = %d/%d/%d; want 100/40/2", c, m, e)
		}
		if c, m, e := readCounters(t, db, "workspace_default", nil); c != 7 || m != 3 || e != 1 {
			t.Fatalf("workspace-default counters after refused moves = %d/%d/%d; want 7/3/1", c, m, e)
		}
	})

	t.Run("move-then-reinsert reset impossible", func(t *testing.T) {
		db := freshUsageDB(t)
		seedUsage(t, db)
		// The relocation move aborts...
		_, err := db.Exec(`UPDATE project_storage_usage SET source_scope='workspace_default', project=NULL WHERE source_scope='project' AND project='proj'`)
		requireCoordinateAbort(t, "coordinate move", err)
		// ...the row cannot be removed either...
		if _, err := db.Exec(`DELETE FROM project_storage_usage WHERE source_scope='project' AND project='proj'`); err == nil {
			t.Fatal("usage row deleted; want retention abort")
		}
		// ...and the occupied coordinate rejects a fresh zero row (UNIQUE).
		if _, err := db.Exec(`
			INSERT INTO project_storage_usage (source_scope, project, content_bytes, metadata_bytes, event_bytes)
			VALUES ('project', 'proj', 0, 0, 0)`); err == nil {
			t.Fatal("fresh zero row accepted at the occupied coordinate; want UNIQUE violation")
		}
		// Exactly the two seeded rows survive with original counters.
		var rows int
		if err := db.QueryRow(`SELECT COUNT(*) FROM project_storage_usage`).Scan(&rows); err != nil || rows != 2 {
			t.Fatalf("usage rows=%d err=%v; want exactly 2 (no relocation, no reset)", rows, err)
		}
		if c, m, e := readCounters(t, db, "project", "proj"); c != 100 || m != 40 || e != 2 {
			t.Fatalf("project counters after refused reset = %d/%d/%d; want 100/40/2", c, m, e)
		}
	})

	t.Run("failed transaction preserves original counters", func(t *testing.T) {
		db := freshUsageDB(t)
		seedUsage(t, db)
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		// A legal fold lands inside the transaction first.
		if _, err := tx.Exec(`UPDATE project_storage_usage SET content_bytes = content_bytes + 50 WHERE source_scope='project' AND project='proj'`); err != nil {
			t.Fatalf("legal fold inside transaction: %v", err)
		}
		// A statement mixing another fold with a coordinate move aborts and
		// is reverted at statement level: the smuggled fold must not stick.
		_, err = tx.Exec(`UPDATE project_storage_usage SET content_bytes = content_bytes + 10, project='elsewhere' WHERE source_scope='project' AND project='proj'`)
		requireCoordinateAbort(t, "fold-plus-move statement", err)
		var content int
		if err := tx.QueryRow(`SELECT content_bytes FROM project_storage_usage WHERE source_scope='project' AND project='proj'`).Scan(&content); err != nil || content != 150 {
			t.Fatalf("in-transaction content_bytes=%d err=%v; want 150 (legal fold kept, aborted fold reverted)", content, err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatalf("rollback: %v", err)
		}
		// After the failed transaction the original counters are untouched.
		if c, m, e := readCounters(t, db, "project", "proj"); c != 100 || m != 40 || e != 2 {
			t.Fatalf("counters after failed transaction = %d/%d/%d; want original 100/40/2", c, m, e)
		}
	})

	t.Run("legal monotonic folds and updated_at pass", func(t *testing.T) {
		db := freshUsageDB(t)
		seedUsage(t, db)
		// Fold all three counters and refresh updated_at in one statement.
		if _, err := db.Exec(`
			UPDATE project_storage_usage
			   SET content_bytes = content_bytes + 5,
			       metadata_bytes = metadata_bytes + 2,
			       event_bytes = event_bytes + 1,
			       updated_at = datetime('now')
			 WHERE source_scope='project' AND project='proj'`); err != nil {
			t.Fatalf("legal monotonic fold rejected: %v", err)
		}
		// updated_at-only maintenance is legal on both coordinate shapes.
		if _, err := db.Exec(`UPDATE project_storage_usage SET updated_at = datetime('now') WHERE source_scope='workspace_default'`); err != nil {
			t.Fatalf("updated_at-only update rejected: %v", err)
		}
		// Rewriting the same coordinate values is a no-op, not a move.
		if _, err := db.Exec(`UPDATE project_storage_usage SET source_scope='project', project='proj' WHERE source_scope='project' AND project='proj'`); err != nil {
			t.Fatalf("same-value coordinate rewrite rejected: %v", err)
		}
		// Equal (nondecreasing) counters stay legal.
		if _, err := db.Exec(`UPDATE project_storage_usage SET content_bytes = content_bytes WHERE source_scope='workspace_default'`); err != nil {
			t.Fatalf("equal counter fold rejected: %v", err)
		}
		if c, _, _ := readCounters(t, db, "project", "proj"); c != 105 {
			t.Fatalf("project content_bytes after legal fold = %d; want 105", c)
		}
	})
}

// TestV2ProjectArtifacts_UsageCoordinateErrorPrecedesMonotonic proves the
// coordinate freeze and the monotonic counter validation share a single
// BEFORE UPDATE guard whose body checks the coordinate first: SQLite fires
// same-event triggers in an unspecified order, so a competing update that
// both moves the quota coordinate and decreases counters deterministically
// reports the coordinate immutability violation, while a pure counter
// decrease still reports the monotonic violation.
func TestV2ProjectArtifacts_UsageCoordinateErrorPrecedesMonotonic(t *testing.T) {
	ctx := context.Background()
	baseline, _ := NewV2Baseline()
	db := openMem(t)
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Exactly ONE BEFORE UPDATE guard owns both invariants; two separate
	// same-event triggers would leave the reported error to SQLite's
	// unspecified firing order.
	var updateGuards int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND tbl_name = 'project_storage_usage' AND sql LIKE '%BEFORE UPDATE%'`,
	).Scan(&updateGuards); err != nil {
		t.Fatalf("count BEFORE UPDATE guards: %v", err)
	}
	if updateGuards != 1 {
		t.Fatalf("BEFORE UPDATE triggers on project_storage_usage = %d; want exactly 1 (combined coordinate+monotonic guard)", updateGuards)
	}

	if _, err := db.Exec(`
		INSERT INTO project_storage_usage (source_scope, project, content_bytes, metadata_bytes, event_bytes)
		VALUES ('project', 'proj', 100, 40, 2)`); err != nil {
		t.Fatalf("seed usage: %v", err)
	}

	// Competing failure: one statement both moves the coordinate and
	// decreases counters. The coordinate immutability error must win.
	_, err := db.Exec(`
		UPDATE project_storage_usage
		   SET source_scope = 'workspace_default', project = NULL,
		       content_bytes = 0, metadata_bytes = 0, event_bytes = 0
		 WHERE source_scope = 'project' AND project = 'proj'`)
	if err == nil {
		t.Fatal("competing coordinate move + counter decrease accepted; want abort")
	}
	if !strings.Contains(err.Error(), "coordinates are immutable") {
		t.Fatalf("competing move+decrease error = %v; want coordinate immutability abort to precede monotonic validation", err)
	}
	// The row survived untouched at its frozen coordinate.
	var content int
	if err := db.QueryRow(`SELECT content_bytes FROM project_storage_usage WHERE source_scope='project' AND project='proj'`).Scan(&content); err != nil || content != 100 {
		t.Fatalf("content_bytes after refused competing update = %d err=%v; want 100", content, err)
	}

	// A pure counter decrease (no coordinate change) still reports the
	// monotonic violation, proving the combined guard did not swallow it.
	_, err = db.Exec(`UPDATE project_storage_usage SET content_bytes = 99 WHERE source_scope='project' AND project='proj'`)
	if err == nil {
		t.Fatal("pure counter decrease accepted; want monotonic abort")
	}
	if !strings.Contains(err.Error(), "counters never decrease") {
		t.Fatalf("pure decrease error = %v; want monotonic counter abort", err)
	}
}

// TestV2ProjectArtifacts_WorkspaceDefaultScope proves a workspace default is
// a first-class artifact with an absent project: the scope CHECK keeps
// source_scope and project presence in agreement in both directions, and
// defaults resolve per (kind, key) across the local workspace independently
// of project artifacts under the same key.
func TestV2ProjectArtifacts_WorkspaceDefaultScope(t *testing.T) {
	ctx := context.Background()
	baseline, _ := NewV2Baseline()
	db := openMem(t)
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	insert := func(publicID string, project any, scope, key string) error {
		_, err := db.Exec(`
			INSERT INTO project_artifacts (public_id, project, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
			VALUES (?, ?, 'skill', ?, ?, 1, 3, 5, ?)`, publicID, project, key, scope, artifactDigest64)
		return err
	}
	if err := insert("wsd-1", nil, "workspace_default", "shared"); err != nil {
		t.Fatalf("workspace default with absent project rejected: %v", err)
	}
	if err := insert("wsd-2", nil, "workspace_default", "shared"); err == nil {
		t.Fatal("duplicate active workspace default accepted; want UNIQUE violation")
	}
	if err := insert("wsd-3", nil, "workspace_default", "other"); err != nil {
		t.Fatalf("second workspace default key rejected: %v", err)
	}
	// Project artifacts under the same key coexist per project.
	if err := insert("prj-1", "p1", "project", "shared"); err != nil {
		t.Fatalf("project artifact under default key rejected: %v", err)
	}
	if err := insert("prj-2", "p2", "project", "shared"); err != nil {
		t.Fatalf("same key under another project rejected: %v", err)
	}
	if err := insert("prj-3", "p1", "project", "shared"); err == nil {
		t.Fatal("duplicate active project artifact accepted; want UNIQUE violation")
	}
	// Scope and project presence must agree in both directions.
	if err := insert("bad-1", "p1", "workspace_default", "mixed"); err == nil {
		t.Fatal("workspace_default with a project accepted; want CHECK violation")
	}
	if err := insert("bad-2", nil, "project", "mixed"); err == nil {
		t.Fatal("project scope without a project accepted; want CHECK violation")
	}
	var defaults, projectRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM project_artifacts WHERE source_scope='workspace_default' AND project IS NULL`).Scan(&defaults); err != nil || defaults != 2 {
		t.Fatalf("workspace defaults=%d err=%v; want 2", defaults, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM project_artifacts WHERE source_scope='project' AND project IS NOT NULL`).Scan(&projectRows); err != nil || projectRows != 2 {
		t.Fatalf("project artifacts=%d err=%v; want 2", projectRows, err)
	}
}

// TestV2ProjectArtifacts_NoDestructiveSQL pins the retention policy at the
// SQL source level: no drops, no truncate/delete paths, no cascade, no TTL.
func TestV2ProjectArtifacts_NoDestructiveSQL(t *testing.T) {
	src := strings.ToUpper(v2migrations.ProjectArtifactsSQL)
	for _, banned := range []string{
		"DROP TABLE", "DROP INDEX", "DROP TRIGGER", "TRUNCATE", "DELETE FROM",
		"ON DELETE CASCADE", "ON DELETE SET NULL", "ON DELETE SET DEFAULT",
		"TTL", "EXPIRE", "VACUUM",
	} {
		if strings.Contains(src, banned) {
			t.Errorf("003 SQL contains banned destructive token %q", banned)
		}
	}
	if strings.Contains(v2migrations.ProjectArtifactsSQL, "CREATE TABLE IF NOT EXISTS project_artifacts") {
		t.Error("003 uses IF NOT EXISTS; a stale unledgered artifact must fail closed, not be adopted")
	}
}

// --- 11. ROLLOUT PREFLIGHT, POST-APPLY CHECKS, AND CHECKSUM PINS (IDP-T05) ---

// v2HistoricalChecksums pins the canonical (LF-normalized) SHA-256 of every
// embedded SQLite v2 migration SQL, keyed by registry version. Applied
// databases ledger the embedded checksum and fail closed on mismatch, so
// editing a SHIPPED file bricks existing databases; these literals make such
// drift visible in plain unit tests on every platform (LF or CRLF checkout).
// 2001 (001_init.sql) and 2002 (002_handoff_receipts.sql) shipped with the
// v2.0/v2.1 line and MUST remain byte-identical forever. 2003
// (003_project_artifacts.sql) is still unshipped, so its pin moves with the
// reviewed bytes until release.
var v2HistoricalChecksums = map[int]string{
	2001: "e966fa751ea8367084d28e970887811bc514d7d101c310e2ea7c1f854496d1e4",
	2002: "e21846ac7082f8cf2d57308a220fd07c0eb47f8c80feb9058d84a0ab38dd4ae5",
	2003: "a5172e1a8979d60b067c367c253bd7ce5e37058f4392d6e6e5c5137a8ec24a96",
}

// v2SQLForVersion returns the embedded UpSQL of one registry version from
// the public V2Migrations line.
func v2SQLForVersion(t *testing.T, version int) string {
	t.Helper()
	reg, err := NewV2Registry()
	if err != nil {
		t.Fatalf("NewV2Registry: %v", err)
	}
	for _, m := range reg.V2Migrations() {
		if m.Version == version {
			return m.UpSQL
		}
	}
	t.Fatalf("registry version %d not found in the v2 line", version)
	return ""
}

// TestV2HistoricalMigrationsAreImmutable pins the canonical SHA-256 of the
// embedded SQLite v2 SQL. Shipped migrations (2001/2002) MUST remain
// byte-identical: applied databases refuse checksum mismatches, and
// rewriting history would be a destructive schema change. The unshipped 2003
// pin moves with the reviewed bytes until its release freezes it.
func TestV2HistoricalMigrationsAreImmutable(t *testing.T) {
	for version, pinned := range v2HistoricalChecksums {
		if got := canonicalChecksum(v2SQLForVersion(t, version)); got != pinned {
			t.Errorf("v2 migration %d SQL changed: canonical sha256=%s, pinned=%s", version, got, pinned)
		}
	}
}

// TestV2Preflight_ExpectedUnledgeredState verifies the read-only rollout
// preflight for follow-up 2003 returns the EXPECTED unledgered verdict (nil
// error) both when the ledger table is absent entirely (baseline-only
// database from an older runtime) and when the ledger exists but records no
// 2003 row. It also proves the preflight is strictly read-only: no ledger
// table is created and the schema snapshot is unchanged.
func TestV2Preflight_ExpectedUnledgeredState(t *testing.T) {
	ctx := context.Background()
	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	wantChecksum := hex.EncodeToString(sumSHA256(t, v2migrations.ProjectArtifactsSQL))

	t.Run("baseline-only database has no ledger table", func(t *testing.T) {
		db := buildBaselineOnlyDB(t)
		snapshot := v2SchemaSnapshot(t, db)

		state, err := baseline.PreflightFollowUp(ctx, db, V2ProjectArtifactsMigrationVersion)
		if err != nil {
			t.Fatalf("preflight on unledgered baseline-only DB err=%v; want nil (expected unledgered state)", err)
		}
		if state.LedgerTable {
			t.Error("preflight reported a ledger table on a baseline-only DB")
		}
		if state.Ledgered {
			t.Error("preflight reported 2003 as ledgered on a baseline-only DB")
		}
		if state.Version != V2ProjectArtifactsMigrationVersion {
			t.Errorf("preflight version = %d, want %d", state.Version, V2ProjectArtifactsMigrationVersion)
		}
		if state.ExpectedChecksum != wantChecksum {
			t.Errorf("preflight expected checksum = %q, want embedded 003 checksum %q", state.ExpectedChecksum, wantChecksum)
		}
		if verr := state.Verdict(); verr != nil {
			t.Errorf("Verdict on unledgered state = %v; want nil", verr)
		}

		// Strictly read-only: the ledger table must NOT have been created and
		// the schema must be byte-identical.
		if tableExists(t, db, v2FollowUpLedgerTable) {
			t.Error("preflight created the follow-up ledger table; it must stay read-only")
		}
		if after := v2SchemaSnapshot(t, db); after != snapshot {
			t.Error("schema changed during the read-only preflight")
		}
	})

	t.Run("ledger exists without a 2003 row", func(t *testing.T) {
		db := openMem(t)
		if err := baseline.Apply(ctx, db); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM cortex_v2_migrations WHERE version = ?`, V2ProjectArtifactsMigrationVersion); err != nil {
			t.Fatalf("remove 2003 ledger row: %v", err)
		}
		rows := v2LedgerRowCount(t, db, V2ProjectArtifactsMigrationVersion)

		state, err := baseline.PreflightFollowUp(ctx, db, V2ProjectArtifactsMigrationVersion)
		if err != nil {
			t.Fatalf("preflight with ledger-but-no-row err=%v; want nil (expected unledgered state)", err)
		}
		if !state.LedgerTable {
			t.Error("preflight missed the existing ledger table")
		}
		if state.Ledgered {
			t.Error("preflight reported 2003 as ledgered with no ledger row")
		}
		// Read-only: the deleted row stays deleted.
		if after := v2LedgerRowCount(t, db, V2ProjectArtifactsMigrationVersion); after != rows {
			t.Errorf("ledger rows for 2003 changed during preflight: before=%d after=%d", rows, after)
		}
	})
}

// TestV2Preflight_AnyRecordedChecksumStops verifies the stop/escalate rule:
// ANY checksum recorded for 2003 — a prior pre-release checksum or even the
// current one — stops a rollout that expects the unledgered state, and a
// ledger row from a newer runtime stops it too. Every stop is proven
// zero-mutation: the schema snapshot and the ledger rows survive unchanged.
func TestV2Preflight_AnyRecordedChecksumStops(t *testing.T) {
	ctx := context.Background()
	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}

	t.Run("prior checksum stops and escalates", func(t *testing.T) {
		db := buildBaselineOnlyDB(t)
		if _, err := db.Exec(v2FollowUpLedgerDDL); err != nil {
			t.Fatalf("create ledger: %v", err)
		}
		const prior = "old-prerelease-checksum"
		if _, err := db.Exec(
			`INSERT INTO cortex_v2_migrations (version, name, checksum) VALUES (?, 'v2_003_project_artifacts', ?)`,
			V2ProjectArtifactsMigrationVersion, prior,
		); err != nil {
			t.Fatalf("seed prior ledger row: %v", err)
		}
		snapshot := v2SchemaSnapshot(t, db)

		state, err := baseline.PreflightFollowUp(ctx, db, V2ProjectArtifactsMigrationVersion)
		if err == nil {
			t.Fatal("preflight accepted a prior 2003 checksum; want stop")
		}
		if !errors.Is(err, ErrPreflightStop) {
			t.Errorf("preflight err=%v; want errors.Is ErrPreflightStop", err)
		}
		if !errors.Is(err, ErrSchemaTampered) {
			t.Errorf("preflight err=%v; want errors.Is ErrSchemaTampered (prior checksum is tamper-class)", err)
		}
		if !state.Ledgered || state.RecordedChecksum != prior {
			t.Errorf("preflight state = (ledgered=%v, recorded=%q); want the prior row verbatim", state.Ledgered, state.RecordedChecksum)
		}

		// Zero mutation: snapshot identical, prior row preserved.
		if after := v2SchemaSnapshot(t, db); after != snapshot {
			t.Error("schema changed during the stop preflight")
		}
		if got, ok := v2LedgerChecksum(t, db, V2ProjectArtifactsMigrationVersion); !ok || got != prior {
			t.Errorf("prior ledger row mutated by preflight: got=%q ok=%v, want %q", got, ok, prior)
		}
	})

	t.Run("current checksum stops as already applied", func(t *testing.T) {
		db := openMem(t)
		if err := baseline.Apply(ctx, db); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		snapshot := v2SchemaSnapshot(t, db)

		_, err := baseline.PreflightFollowUp(ctx, db, V2ProjectArtifactsMigrationVersion)
		if err == nil {
			t.Fatal("preflight accepted an already-applied 2003; want stop (rollout expects unledgered)")
		}
		if !errors.Is(err, ErrPreflightStop) {
			t.Errorf("preflight err=%v; want errors.Is ErrPreflightStop", err)
		}
		if errors.Is(err, ErrSchemaTampered) {
			t.Errorf("already-applied stop must not be tamper-class: %v", err)
		}

		if after := v2SchemaSnapshot(t, db); after != snapshot {
			t.Error("schema changed during the stop preflight")
		}
		if rows := v2LedgerRowCount(t, db, V2ProjectArtifactsMigrationVersion); rows != 1 {
			t.Errorf("ledger rows for 2003 = %d after preflight, want 1 (unchanged)", rows)
		}
	})

	t.Run("future ledger version stops", func(t *testing.T) {
		db := openMem(t)
		if err := baseline.Apply(ctx, db); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if _, err := db.Exec(
			`INSERT INTO cortex_v2_migrations (version, name, checksum) VALUES (2004, 'future_runtime', 'future')`,
		); err != nil {
			t.Fatalf("seed future ledger row: %v", err)
		}
		snapshot := v2SchemaSnapshot(t, db)

		state, err := baseline.PreflightFollowUp(ctx, db, V2ProjectArtifactsMigrationVersion)
		if err == nil {
			t.Fatal("preflight accepted a newer-runtime ledger; want stop")
		}
		if !errors.Is(err, ErrPreflightStop) || !errors.Is(err, ErrFutureMigration) {
			t.Errorf("preflight err=%v; want errors.Is ErrPreflightStop AND ErrFutureMigration", err)
		}
		if state.FutureLedgerVersion != 2004 {
			t.Errorf("preflight future version = %d, want 2004", state.FutureLedgerVersion)
		}

		if after := v2SchemaSnapshot(t, db); after != snapshot {
			t.Error("schema changed during the stop preflight")
		}
		if rows := v2LedgerRowCount(t, db, 2004); rows != 1 {
			t.Errorf("ledger rows for 2004 = %d after preflight, want 1 (preserved)", rows)
		}
	})
}

// TestV2Preflight_RejectsUnknownVersionAndNilDB pins the preflight's precise
// scope: it only preflights versions this runtime embeds, and refuses a nil
// connection.
func TestV2Preflight_RejectsUnknownVersionAndNilDB(t *testing.T) {
	ctx := context.Background()
	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	db := openMem(t)

	if _, err := baseline.PreflightFollowUp(ctx, db, 9999); err == nil {
		t.Error("preflight accepted unknown version 9999; want error")
	}
	if _, err := baseline.PreflightFollowUp(ctx, nil, V2ProjectArtifactsMigrationVersion); err == nil {
		t.Error("preflight accepted a nil connection; want error")
	}
}

// TestV2VerifyFollowUpApplied_PostApplyCheck verifies the post-apply check
// for the follow-up ledger: after a successful Apply the ledger records the
// EXACT embedded checksum for 2002 and 2003, a drifted checksum fails with
// ErrSchemaTampered, a missing row fails as not-applied, and unknown
// versions are rejected.
func TestV2VerifyFollowUpApplied_PostApplyCheck(t *testing.T) {
	ctx := context.Background()
	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	db := openMem(t)
	if err := baseline.Apply(ctx, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for _, version := range []int{V2HandoffReceiptsMigrationVersion, V2ProjectArtifactsMigrationVersion} {
		if err := baseline.VerifyFollowUpApplied(ctx, db, version); err != nil {
			t.Errorf("post-apply check for %d err=%v; want nil", version, err)
		}
	}

	if _, err := db.Exec(`UPDATE cortex_v2_migrations SET checksum = 'drifted' WHERE version = ?`, V2ProjectArtifactsMigrationVersion); err != nil {
		t.Fatalf("drift 2003 checksum: %v", err)
	}
	if err := baseline.VerifyFollowUpApplied(ctx, db, V2ProjectArtifactsMigrationVersion); err == nil || !errors.Is(err, ErrSchemaTampered) {
		t.Errorf("post-apply check on drifted checksum err=%v; want errors.Is ErrSchemaTampered", err)
	}

	if _, err := db.Exec(`DELETE FROM cortex_v2_migrations WHERE version = ?`, V2ProjectArtifactsMigrationVersion); err != nil {
		t.Fatalf("remove 2003 ledger row: %v", err)
	}
	if err := baseline.VerifyFollowUpApplied(ctx, db, V2ProjectArtifactsMigrationVersion); err == nil {
		t.Error("post-apply check accepted a missing 2003 ledger row; want not-applied error")
	}

	if err := baseline.VerifyFollowUpApplied(ctx, db, 9999); err == nil {
		t.Error("post-apply check accepted unknown version 9999; want error")
	}
}

// TestV2FollowUp_PriorLedgerChecksumRejectedAtApply proves the apply-path
// rejection that backs the preflight escalation: a database whose ledger
// already records a PRIOR 2003 checksum (an unshipped pre-release build)
// fails closed with ErrSchemaTampered and leaves ZERO partial state — no
// follow-up DDL (2002 or 2003 tables), no new ledger rows, the prior row
// verbatim, user data intact, and the 001 identity untouched.
func TestV2FollowUp_PriorLedgerChecksumRejectedAtApply(t *testing.T) {
	ctx := context.Background()
	db := buildBaselineOnlyDB(t)
	if _, err := db.Exec(`INSERT INTO sessions (id, project, directory) VALUES ('data', 'p', '/d')`); err != nil {
		t.Fatalf("seed data: %v", err)
	}
	if _, err := db.Exec(v2FollowUpLedgerDDL); err != nil {
		t.Fatalf("create ledger: %v", err)
	}
	const prior = "old-prerelease-checksum"
	if _, err := db.Exec(
		`INSERT INTO cortex_v2_migrations (version, name, checksum) VALUES (?, 'v2_003_project_artifacts', ?)`,
		V2ProjectArtifactsMigrationVersion, prior,
	); err != nil {
		t.Fatalf("seed prior ledger row: %v", err)
	}

	baseline, err := NewV2Baseline()
	if err != nil {
		t.Fatalf("NewV2Baseline: %v", err)
	}
	if err := baseline.Apply(ctx, db); err == nil || !errors.Is(err, ErrSchemaTampered) {
		t.Fatalf("Apply over a prior 2003 checksum err=%v; want errors.Is ErrSchemaTampered", err)
	}

	// Zero partial state: no follow-up DDL landed, the 2002 apply in the same
	// transaction rolled back with the refusal.
	if tableExists(t, db, "handoff_receipts") {
		t.Error("2002 DDL executed despite the prior-checksum refusal")
	}
	if tableExists(t, db, "project_artifacts") {
		t.Error("2003 DDL executed despite the prior-checksum refusal")
	}
	if _, ok := v2LedgerChecksum(t, db, V2HandoffReceiptsMigrationVersion); ok {
		t.Error("ledger row for 2002 recorded despite the prior-checksum refusal")
	}
	if got, ok := v2LedgerChecksum(t, db, V2ProjectArtifactsMigrationVersion); !ok || got != prior {
		t.Errorf("prior 2003 ledger row not preserved verbatim: got=%q ok=%v, want %q", got, ok, prior)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = 'data'`).Scan(&count); err != nil || count != 1 {
		t.Errorf("user data lost during the refusal: count=%d err=%v", count, err)
	}
	if version, _ := metaValue(t, db, "schema_version"); version != V2BaselineVersion {
		t.Errorf("schema_version = %q after refusal, want %q (001 identity is immutable)", version, V2BaselineVersion)
	}
}

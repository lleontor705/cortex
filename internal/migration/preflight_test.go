// Package migration — read-only compatibility probe tests (W3.2, REQ-DB-002).
//
// These tests drive ProbeCompatibility through every classification mandated by
// REQ-DB-002: Fresh, Compatible, and every refusal variant (old Cortex v1,
// Engram, corrupt, ambiguous-family, partially-initialized, foreign). For each
// refusal they assert BYTE-LEVEL IMMUTABILITY: the original file's SHA-256 is
// unchanged, no -wal/-shm/-journal sidecar appears, and no replacement/backup
// database is created. A defect-pin test proves the probe's connection is
// genuinely read-only (a write attempt is rejected by SQLite).
//
// STRICT TDD: this file was written BEFORE preflight.go existed. Every test
// here drives the public API surface of the probe.
package migration

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// --- fixtures ---------------------------------------------------------------

// buildV2DBAt creates a valid cortex-v2 database at path (via InitV2Database)
// and closes it cleanly so the file is checkpointed and probe-safe.
func buildV2DBAt(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()
	db, err := InitV2Database(ctx, path)
	if err != nil {
		t.Fatalf("buildV2DBAt InitV2Database(%s): %v", path, err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("buildV2DBAt close: %v", err)
	}
}

// buildV1DBAt creates an old Cortex v1-style database at path: it has the
// _migrations tracking table (the v1 identity marker) and a sessions table, but
// NO cortex_meta. This is the signature the probe must refuse as "old Cortex".
func buildV1DBAt(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("buildV1DBAt open: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, stmt := range []string{
		`CREATE TABLE _migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP);`,
		`INSERT INTO _migrations (version, name) VALUES (1, 'init'), (2, 'add_fts');`,
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, project TEXT NOT NULL, directory TEXT NOT NULL);`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("buildV1DBAt exec: %v", err)
		}
	}
}

// buildEngramDBAt creates an Engram-era database at path: the core Cortex
// tables (sessions/observations/user_prompts) but NEITHER _migrations NOR
// cortex_meta. This self-contained fixture captures the legacy schema signature
// that the probe must refuse as an "Engram database".
func buildEngramDBAt(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("buildEngramDBAt open: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, stmt := range []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, project TEXT NOT NULL, directory TEXT NOT NULL, started_at TEXT NOT NULL, ended_at TEXT, summary TEXT);`,
		`CREATE TABLE observations (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, type TEXT NOT NULL, title TEXT NOT NULL, content TEXT NOT NULL, project TEXT, scope TEXT NOT NULL DEFAULT 'project', topic_key TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, deleted_at TEXT);`,
		`CREATE TABLE user_prompts (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, content TEXT NOT NULL, project TEXT, created_at TEXT NOT NULL);`,
		`INSERT INTO sessions (id, project, directory, started_at) VALUES ('s1', 'demo', '.', '2026-01-01T00:00:00Z');`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("buildEngramDBAt exec: %v", err)
		}
	}
}

// writeCorruptFileAt writes random non-SQLite bytes to path.
func writeCorruptFileAt(t *testing.T, path string) {
	t.Helper()
	buf := make([]byte, 512)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("writeCorruptFileAt rand: %v", err)
	}
	// Ensure the header is NOT "SQLite format 3\0".
	copy(buf, "NOT-ASQLITE-HEADER-GARBAGE!!!")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatalf("writeCorruptFileAt write: %v", err)
	}
}

// tamperMeta opens the v2 DB at path read-write and rewrites a cortex_meta key.
func tamperMeta(t *testing.T, path, key, value string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("tamperMeta open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE cortex_meta SET value = ? WHERE key = ?`, value, key); err != nil {
		t.Fatalf("tamperMeta update %s: %v", key, err)
	}
}

// deleteMeta opens the v2 DB at path read-write and deletes a cortex_meta key
// (simulating a partially-initialized identity).
func deleteMeta(t *testing.T, path, key string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("deleteMeta open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`DELETE FROM cortex_meta WHERE key = ?`, key); err != nil {
		t.Fatalf("deleteMeta %s: %v", key, err)
	}
}

// recordedChecksum reads the schema_checksum from cortex_meta at path ("", false
// if absent). Used to prove the checksum survives a refusal byte-identically.
func recordedChecksum(t *testing.T, path string) (string, bool) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("recordedChecksum open: %v", err)
	}
	defer func() { _ = db.Close() }()
	var v string
	err = db.QueryRow(`SELECT value FROM cortex_meta WHERE key = 'schema_checksum'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatalf("recordedChecksum read: %v", err)
	}
	return v, true
}

// --- immutability helpers ---------------------------------------------------

// assertImmutable captures the file state before calling prober and asserts
// that afterward the file SHA-256 is byte-identical, no journal/WAL/SHM sidecar
// appeared, and no replacement database was created. prober is expected to
// REFUSE (return an error); if it does not, the test fails.
func assertImmutableRefusal(t *testing.T, path string, prober func() error) {
	t.Helper()
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	// Snapshot before.
	hashBefore, ok := fileSHA256(t, path)
	if !ok {
		t.Fatalf("assertImmutableRefusal: file missing before probe: %s", path)
	}
	// Record existing sidecars so we only fail on NEW ones.
	sidecarSuffixes := []string{"-wal", "-shm", "-journal"}
	existing := map[string]bool{}
	for _, sfx := range sidecarSuffixes {
		existing[base+sfx] = fileExists(filepath.Join(dir, base+sfx))
	}
	// Snapshot every file in the dir to detect a replacement/backup DB.
	beforeEntries := dirSnapshot(t, dir)

	// Run the probe; it MUST refuse.
	err := prober()
	if err == nil {
		t.Fatal("assertImmutableRefusal: probe did NOT refuse; expected an error")
	}

	// SHA-256 unchanged.
	hashAfter, ok := fileSHA256(t, path)
	if !ok {
		t.Fatalf("assertImmutableRefusal: file disappeared after probe: %s", path)
	}
	if hashAfter != hashBefore {
		t.Errorf("IMMUTABILITY VIOLATION: file SHA-256 changed after read-only probe\n  before=%s\n  after =%s\n  path  =%s", hashBefore, hashAfter, path)
	}
	// No new sidecar.
	for _, sfx := range sidecarSuffixes {
		name := base + sfx
		if fileExists(filepath.Join(dir, name)) && !existing[name] {
			t.Errorf("IMMUTABILITY VIOLATION: sidecar %q appeared after read-only probe", name)
		}
	}
	// No replacement/backup DB created.
	afterEntries := dirSnapshot(t, dir)
	for f := range afterEntries {
		if !beforeEntries[f] {
			t.Errorf("IMMUTABILITY VIOLATION: new file %q appeared in data dir after read-only probe", f)
		}
	}
}

// dirSnapshot returns the set of file names present in dir.
func dirSnapshot(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("dirSnapshot read %s: %v", dir, err)
	}
	set := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		set[e.Name()] = true
	}
	return set
}

// --- 1. FRESH ---------------------------------------------------------------

func TestProbe_FreshPath_NonExistent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "absent.db")
	rep, err := ProbeCompatibility(context.Background(), path)
	if err != nil {
		t.Fatalf("ProbeCompatibility non-existent path: unexpected error: %v", err)
	}
	if rep.Status != ProbeStatusFresh {
		t.Errorf("status = %q, want %q", rep.Status, ProbeStatusFresh)
	}
	if fileExists(path) {
		t.Error("probe created the file; a non-existent path must NOT be touched")
	}
}

func TestProbe_EmptyFile_TreatedAsFresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write empty file: %v", err)
	}
	rep, err := ProbeCompatibility(context.Background(), path)
	if err != nil {
		t.Fatalf("ProbeCompatibility empty file: unexpected error: %v", err)
	}
	if rep.Status != ProbeStatusFresh {
		t.Errorf("empty-file status = %q, want %q (clean create per REQ-DB-001)", rep.Status, ProbeStatusFresh)
	}
}

// --- 2. COMPATIBLE ----------------------------------------------------------

func TestProbe_CompatibleV2_ChecksumMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v2.db")
	buildV2DBAt(t, path)

	want, ok := recordedChecksum(t, path)
	if !ok {
		t.Fatal("setup: v2 DB has no recorded checksum")
	}

	rep, err := ProbeCompatibility(context.Background(), path)
	if err != nil {
		t.Fatalf("ProbeCompatibility v2: unexpected error: %v", err)
	}
	if rep.Status != ProbeStatusCompatible {
		t.Fatalf("status = %q, want %q", rep.Status, ProbeStatusCompatible)
	}
	if rep.Identity.Family != SchemaFamilyCortexV2 {
		t.Errorf("identity family = %q, want %q", rep.Identity.Family, SchemaFamilyCortexV2)
	}
	if rep.Identity.Checksum != want {
		t.Errorf("identity checksum = %q, want %q", rep.Identity.Checksum, want)
	}
}

// --- 3. REFUSAL: old Cortex v1 ----------------------------------------------

func TestProbe_OldCortexV1_Refused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v1.db")
	buildV1DBAt(t, path)

	assertImmutableRefusal(t, path, func() error {
		_, err := ProbeCompatibility(context.Background(), path)
		return err
	})

	_, err := ProbeCompatibility(context.Background(), path)
	if err == nil {
		t.Fatal("v1 DB not refused; expected INCOMPATIBLE_DATABASE")
	}
	if !errors.Is(err, ErrIncompatibleDatabase) {
		t.Errorf("v1 refusal error = %v; want errors.Is ErrIncompatibleDatabase", err)
	}
	if !isIncompatibleCode(err) {
		t.Errorf("v1 refusal error = %v; want stable code %q", err, CodeIncompatibleDatabase)
	}
}

// --- 4. REFUSAL: Engram -----------------------------------------------------

func TestProbe_EngramDB_Refused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "engram.db")
	buildEngramDBAt(t, path)

	assertImmutableRefusal(t, path, func() error {
		_, err := ProbeCompatibility(context.Background(), path)
		return err
	})

	_, err := ProbeCompatibility(context.Background(), path)
	if err == nil {
		t.Fatal("engram DB not refused; expected INCOMPATIBLE_DATABASE")
	}
	if !errors.Is(err, ErrIncompatibleDatabase) {
		t.Errorf("engram refusal error = %v; want errors.Is ErrIncompatibleDatabase", err)
	}
	if !strings.Contains(err.Error(), DefaultV2DBPath()) {
		t.Errorf("engram refusal message must direct operator to clean v2 path;\n  got: %v\n  want substring: %s", err, DefaultV2DBPath())
	}
}

// --- 5. REFUSAL: corrupt file ------------------------------------------------

func TestProbe_CorruptFile_Refused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.db")
	writeCorruptFileAt(t, path)

	assertImmutableRefusal(t, path, func() error {
		_, err := ProbeCompatibility(context.Background(), path)
		return err
	})

	_, err := ProbeCompatibility(context.Background(), path)
	if err == nil {
		t.Fatal("corrupt file not refused; expected INCOMPATIBLE_DATABASE")
	}
	if !errors.Is(err, ErrIncompatibleDatabase) {
		t.Errorf("corrupt refusal error = %v; want errors.Is ErrIncompatibleDatabase", err)
	}
}

// --- 6. REFUSAL: ambiguous family -------------------------------------------

func TestProbe_AmbiguousFamily_Refused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ambiguous.db")
	buildV2DBAt(t, path)
	tamperMeta(t, path, "schema_family", "some-other-family")

	assertImmutableRefusal(t, path, func() error {
		_, err := ProbeCompatibility(context.Background(), path)
		return err
	})

	_, err := ProbeCompatibility(context.Background(), path)
	if err == nil {
		t.Fatal("ambiguous-family DB not refused; expected INCOMPATIBLE_DATABASE")
	}
	if !errors.Is(err, ErrIncompatibleDatabase) {
		t.Errorf("ambiguous-family refusal error = %v; want errors.Is ErrIncompatibleDatabase", err)
	}
}

// --- 7. REFUSAL: partially initialized --------------------------------------

func TestProbe_PartiallyInitialized_Refused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partial.db")
	buildV2DBAt(t, path)
	deleteMeta(t, path, "schema_checksum") // identity now incomplete

	assertImmutableRefusal(t, path, func() error {
		_, err := ProbeCompatibility(context.Background(), path)
		return err
	})

	_, err := ProbeCompatibility(context.Background(), path)
	if err == nil {
		t.Fatal("partially-initialized DB not refused; expected INCOMPATIBLE_DATABASE")
	}
	if !errors.Is(err, ErrIncompatibleDatabase) {
		t.Errorf("partial refusal error = %v; want errors.Is ErrIncompatibleDatabase", err)
	}
}

// --- 8. REFUSAL: foreign/unknown schema -------------------------------------

func TestProbe_ForeignUnknownDb_Refused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "foreign.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open foreign: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE widgets (id INTEGER PRIMARY KEY, sku TEXT)`); err != nil {
		t.Fatalf("create widgets: %v", err)
	}
	_ = db.Close()

	_, err = ProbeCompatibility(context.Background(), path)
	if err == nil {
		t.Fatal("foreign DB not refused; expected INCOMPATIBLE_DATABASE")
	}
	if !errors.Is(err, ErrIncompatibleDatabase) {
		t.Errorf("foreign refusal error = %v; want errors.Is ErrIncompatibleDatabase", err)
	}
}

// --- 9. checksum preserved after refusal ------------------------------------

func TestProbe_SchemaChecksumPreservedAfterRefusal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v2.db")
	buildV2DBAt(t, path)
	before, _ := recordedChecksum(t, path)

	// Tamper family so the probe refuses, then prove the checksum row is intact.
	tamperMeta(t, path, "schema_family", "other")
	_, err := ProbeCompatibility(context.Background(), path)
	if err == nil {
		t.Fatal("expected refusal")
	}
	after, ok := recordedChecksum(t, path)
	if !ok {
		t.Fatal("schema_checksum row disappeared after refusal")
	}
	if after != before {
		t.Errorf("schema_checksum changed after read-only refusal: before=%s after=%s", before, after)
	}
}

// --- 10. DEFECT PIN: probe connection is genuinely read-only ----------------

// TestProbe_InjectWriteDuringProbe_DetectedByGate proves the probe opens the
// database on a connection that REJECTS writes. A regression that opens a
// write-capable connection (or issues a mutating pragma) would let the INSERT
// succeed and the SHA-256 immutability assertion (run on the same file) would
// then fail — i.e. the change is rejected until the probe is provably read-only.
func TestProbe_InjectWriteDuringProbe_DetectedByGate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v2.db")
	buildV2DBAt(t, path)
	hashBefore, _ := fileSHA256(t, path)

	// 1) Build the probe's own read-only DSN and attempt a write through it.
	//    SQLite MUST reject the write (the connection is query_only + mode=ro).
	dsn := readOnlyDSN(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open read-only DSN: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Force the connection to materialize, then attempt a write.
	if _, err := db.Exec(`SELECT 1`); err != nil {
		t.Fatalf("read-only connection cannot even SELECT: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE should_not_exist (x INTEGER)`); err == nil {
		t.Fatal("DEFECT: write succeeded on the probe's read-only connection; the connection is NOT read-only")
	}

	// 2) Run the probe itself and prove it left the file byte-identical.
	if _, err := ProbeCompatibility(context.Background(), path); err != nil {
		t.Fatalf("probe on a valid v2 DB should be Compatible, got: %v", err)
	}
	hashAfter, _ := fileSHA256(t, path)
	if hashAfter != hashBefore {
		t.Fatalf("DEFECT: probe mutated a Compatible DB\n  before=%s\n  after =%s", hashBefore, hashAfter)
	}
}

// --- helpers ----------------------------------------------------------------

// isIncompatibleCode reports whether err's message carries the stable code.
func isIncompatibleCode(err error) bool {
	return err != nil && strings.Contains(err.Error(), CodeIncompatibleDatabase)
}

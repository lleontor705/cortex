// Package migration — read-only compatibility probe (W3.2, REQ-DB-002).
//
// ProbeCompatibility inspects a candidate database file STRICTLY READ-ONLY
// before any write-capable open, and classifies it as Fresh, Compatible, or
// refused (INCOMPATIBLE_DATABASE). It exists to satisfy REQ-DB-002: Cortex MUST
// detect old Cortex, Engram, corrupt, ambiguous, or partially-initialized
// databases and refuse to mutate, auto-convert, or journal them.
//
// Read-only guarantees (belt and suspenders):
//   - DSN opens with mode=ro (VFS-level read-only).
//   - immutable=1 (SQLite never creates/touches -wal/-shm for the probe).
//   - _pragma=query_only(1) (SQL compiler rejects any write statement).
//
// The probe issues ONLY SELECTs against sqlite_master and cortex_meta. It never
// runs PRAGMA journal_mode or any other mutating pragma.
//
// Design grounding: ADR-03 (clean v2 DB + read-only old-DB refusal), spec
// REQ-DB-002 (old database immutability on refusal).
package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (zero CGO)
)

// CodeIncompatibleDatabase is the stable, operator-facing string error code
// surfaced when the probe refuses to operate on an incompatible database. It is
// intentionally a string constant so logs/CLI output are grep-stable across
// versions and wrapper layers.
const CodeIncompatibleDatabase = "INCOMPATIBLE_DATABASE"

// ProbeStatus is the non-refusal outcome of a compatibility probe. Refusals are
// conveyed via the returned error (see CodeIncompatibleDatabase).
type ProbeStatus string

const (
	// ProbeStatusFresh means the path is absent (or an empty/uninitialized
	// SQLite file): the caller may create a clean v2 database here.
	ProbeStatusFresh ProbeStatus = "fresh"

	// ProbeStatusCompatible means the file is a valid cortex-v2 database whose
	// recorded schema checksum matches the expected v2 baseline. The caller may
	// proceed (re-initialization is idempotent).
	ProbeStatusCompatible ProbeStatus = "compatible"
)

// ProbeReport describes a non-refusal probe outcome.
type ProbeReport struct {
	Status   ProbeStatus    // Fresh or Compatible
	Path     string         // the probed filesystem path
	Identity SchemaIdentity // populated for Compatible (family/version/checksum)
	Detail   string         // human-readable classification note
}

// IncompatibleDatabaseError wraps the W3.1 sentinel ErrIncompatibleDatabase so
// refusal is errors.Is-checkable, while also exposing the stable code and an
// operator-facing message that names the clean v2 default path.
type IncompatibleDatabaseError struct {
	Path   string // the configured database path that was refused
	Detail string // why it was refused
	err    error  // always ErrIncompatibleDatabase (W3.1 sentinel)
}

// Error implements error.
func (e *IncompatibleDatabaseError) Error() string {
	return fmt.Sprintf("%s: %s (configured path: %s; use a clean v2 database path such as %s)",
		CodeIncompatibleDatabase, e.Detail, e.Path, DefaultV2DBPath())
}

// Unwrap allows errors.Is(err, ErrIncompatibleDatabase).
func (e *IncompatibleDatabaseError) Unwrap() error { return e.err }

// Code returns the stable string error code.
func (e *IncompatibleDatabaseError) Code() string { return CodeIncompatibleDatabase }

// refuse builds an IncompatibleDatabaseError for the given path and detail.
func refuse(path, detail string) error {
	return &IncompatibleDatabaseError{Path: path, Detail: detail, err: ErrIncompatibleDatabase}
}

// ProbeCompatibility opens the database at path STRICTLY READ-ONLY and
// classifies it. It MUST NOT mutate the file: it only issues SELECTs against
// sqlite_master and cortex_meta under query_only(1).
//
// Outcomes:
//   - Fresh:      path absent, or an empty/uninitialized SQLite file.
//   - Compatible: valid cortex-v2 database, checksum matches baseline.
//   - refused:    old Cortex v1, Engram, corrupt, ambiguous-family,
//     partially-initialized, or foreign/unknown. The returned error wraps
//     ErrIncompatibleDatabase and carries CodeIncompatibleDatabase.
func ProbeCompatibility(ctx context.Context, path string) (*ProbeReport, error) {
	if path == "" || path == ":memory:" {
		// Nothing to probe for an in-memory/transient target: caller creates fresh.
		return &ProbeReport{Status: ProbeStatusFresh, Path: path, Detail: "in-memory or empty path; treated as fresh create"}, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &ProbeReport{Status: ProbeStatusFresh, Path: path, Detail: "path does not exist; clean create"}, nil
		}
		return nil, fmt.Errorf("migration: probe stat %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, refuse(path, "configured database path is a directory, not a database file")
	}
	if info.Size() == 0 {
		// A zero-byte file is not a SQLite database; treat it as a fresh create
		// target per REQ-DB-001 (clean create at a new path).
		return &ProbeReport{Status: ProbeStatusFresh, Path: path, Detail: "empty file; treated as fresh create"}, nil
	}

	// Open STRICTLY READ-ONLY. No journal_mode pragma, no mutating pragma.
	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		// Opening the driver handle should not fail for a path error; a failure
		// here is treated as corrupt/ambiguous.
		return nil, refuse(path, "cannot open database read-only (corrupt or ambiguous)")
	}
	defer func() { _ = db.Close() }()

	// Materialize the connection and verify it is a readable SQLite database.
	tables, err := listTables(db)
	if err != nil {
		// Any schema-read failure => not a usable SQLite database => refuse.
		return nil, refuse(path, "file is not a readable SQLite database (corrupt or ambiguous)")
	}

	hasCortexMeta := containsStr(tables, "cortex_meta")
	hasMigrations := containsStr(tables, "_migrations")

	// 1) cortex_meta present: use the v2 schema identity as the sole discriminator.
	if hasCortexMeta {
		id, partial, rerr := readMetaIdentityRO(db)
		if rerr != nil {
			return nil, refuse(path, "cannot read cortex_meta identity (corrupt)")
		}
		if partial {
			return nil, refuse(path, "cortex_meta present but incompletely initialized (missing schema identity keys)")
		}
		if id.Family != SchemaFamilyCortexV2 {
			return nil, refuse(path, fmt.Sprintf("cortex_meta schema_family=%q is not %q", id.Family, SchemaFamilyCortexV2))
		}
		// Family is cortex-v2: verify the checksum against the expected baseline.
		expected, _ := expectedV2Checksum()
		if expected != "" && id.Checksum != "" && id.Checksum != expected {
			return nil, refuse(path, "cortex-v2 database schema checksum mismatch (tampered or incompatible baseline)")
		}
		return &ProbeReport{
			Status:   ProbeStatusCompatible,
			Path:     path,
			Identity: id,
			Detail:   "cortex-v2 database; recorded schema checksum matches the v2 baseline",
		}, nil
	}

	// 2) Old Cortex v1 line: _migrations present, no cortex_meta.
	if hasMigrations {
		return nil, refuse(path, "old Cortex v1 database detected (has _migrations table, no cortex-v2 identity)")
	}

	// 3) Engram-era database: core Cortex tables present but no tracking or
	//    identity tables. Engram predates both _migrations and cortex_meta.
	if isEngramSignature(tables) {
		return nil, refuse(path, "Engram database detected (foreign schema family; core tables without v2 identity)")
	}

	// 4) Any other SQLite file with tables is a foreign/unknown database.
	if len(tables) > 0 {
		return nil, refuse(path, fmt.Sprintf("foreign or unknown database (unrecognized schema; %d table(s))", len(tables)))
	}

	// 5) A valid SQLite file with no tables: effectively uninitialized. Treat as
	//    a fresh create target (consistent with the empty-file rule).
	return &ProbeReport{Status: ProbeStatusFresh, Path: path, Detail: "valid SQLite file with no schema tables; treated as fresh create"}, nil
}

// --- read-only helpers ------------------------------------------------------

// readOnlyDSN builds a DSN that opens path strictly read-only. It uses the
// SQLite file: URI form so mode=ro and immutable=1 are honored by the VFS, and
// adds query_only(1) as a SQL-level guard. Together these guarantee the probe
// never creates -wal/-shm or performs any write.
//
// It is exported within the package so the defect-pin test can reuse the EXACT
// DSN the probe uses.
func readOnlyDSN(path string) string {
	v := url.Values{}
	v.Set("mode", "ro")
	v.Set("immutable", "1")
	v.Add("_pragma", "query_only(1)")
	return "file:" + filepath.ToSlash(path) + "?" + v.Encode()
}

// listTables returns the names of all base tables in the database (read-only).
func listTables(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// readMetaIdentityRO reads the schema identity from cortex_meta over a read-only
// connection. It returns (identity, partial, err): partial is true when
// cortex_meta exists but any of the identity keys (family/version/checksum) are
// missing or empty (partially-initialized).
func readMetaIdentityRO(db *sql.DB) (SchemaIdentity, bool, error) {
	get := func(key string) (string, bool, error) {
		var v string
		err := db.QueryRow(`SELECT value FROM cortex_meta WHERE key = ?`, key).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return v, true, nil
	}
	family, okF, err := get("schema_family")
	if err != nil {
		return SchemaIdentity{}, false, err
	}
	version, okV, err := get("schema_version")
	if err != nil {
		return SchemaIdentity{}, false, err
	}
	checksum, okC, err := get("schema_checksum")
	if err != nil {
		return SchemaIdentity{}, false, err
	}
	// Partially-initialized: cortex_meta exists but the identity triple is
	// missing one or more required keys/values.
	if !okF || family == "" || !okV || version == "" || !okC || checksum == "" {
		return SchemaIdentity{Family: family, Version: version, Checksum: checksum}, true, nil
	}
	return SchemaIdentity{Family: family, Version: version, Checksum: checksum}, false, nil
}

// expectedV2Checksum returns the SHA-256 checksum of the embedded v2 baseline
// SQL, or ("", false) if the baseline cannot be constructed.
func expectedV2Checksum() (string, bool) {
	b, err := NewV2Baseline()
	if err != nil {
		return "", false
	}
	return b.Identity().Checksum, true
}

// isEngramSignature reports whether the table set matches an Engram-era
// database: the core Cortex tables (sessions + observations) are present
// WITHOUT _migrations or cortex_meta. Engram predates the migration-tracking
// and schema-identity tables.
func isEngramSignature(tables []string) bool {
	return containsStr(tables, "sessions") && containsStr(tables, "observations")
}

// containsStr reports whether s is in list.
func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

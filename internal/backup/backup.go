// Package backup implements the portable logical snapshot of the Cortex
// memory store: observations, graph relations (edges), and sessions, packaged
// as JSONL parts inside a tar.gz with a sha256-verified manifest.
//
// This is the portable logical layer, distinct from the physical SQLite
// snapshot (`cortex backup <path>` → VACUUM INTO). It follows the
// projection/obsidian package pattern: a pure library with narrow inputs,
// wired to the store bundle at the CLI composition root.
//
// Scope: local SQLite mode. Server (PostgreSQL) mode full-database backup
// remains the domain of pg_dump; this package's schema-projection queries are
// SQLite-specific and restore refuses non-v2 databases via the schema
// identity check.
//
// Security invariants:
//   - Only a fixed allow-list of columns is exported. Configuration secrets
//     (API keys, DSNs, web keys, bearer tokens) never enter the projection.
//   - Defense in depth: the caller may supply known secret values; Export
//     fail-closes if any of them appears in the serialized archive.
//   - Embedding vectors are NOT exported (regenerable, and potentially
//     re-identifying); per-observation embedding state (model/dimensions) is.
package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// Format identifies the archive family in the manifest.
	Format = "cortex-backup"

	// FormatVersion is the manifest/record layout version of this package.
	FormatVersion = 1

	// ManifestName is the manifest entry name inside the tar.gz.
	ManifestName = "manifest.json"

	// Part names (JSONL, one record per line).
	ObservationsPart = "observations.jsonl"
	EdgesPart        = "edges.jsonl"
	SessionsPart     = "sessions.jsonl"
)

// Part describes one JSONL member of the archive in the manifest.
type Part struct {
	Count  int    `json:"count"`
	SHA256 string `json:"sha256"`
}

// Manifest is the self-describing index stored inside every archive.
type Manifest struct {
	Format        string            `json:"format"`
	FormatVersion int               `json:"format_version"`
	SchemaFamily  string            `json:"schema_family"`
	SchemaVersion string            `json:"schema_version"`
	ToolVersion   string            `json:"tool_version"`
	CreatedAt     string            `json:"created_at"`
	Source        Source            `json:"source"`
	Parts         map[string]Part   `json:"parts"`
	Notes         map[string]string `json:"notes,omitempty"`
}

// Source records where the archive was produced.
type Source struct {
	Mode     string `json:"mode"` // "local-sqlite"
	TenantID string `json:"tenant_id,omitempty"`
}

// ErrIncompatibleSchema reports a restore refusal caused by schema-version
// incompatibility (the archive is newer than the target database).
type ErrIncompatibleSchema struct {
	Backup  string
	Current string
}

func (e *ErrIncompatibleSchema) Error() string {
	return fmt.Sprintf(
		"incompatible backup: archive schema version %s is newer than database schema version %s; upgrade cortex before restoring",
		e.Backup, e.Current)
}

// ErrTampered reports a sha256 mismatch between manifest and part payload.
type ErrTampered struct{ Part string }

func (e *ErrTampered) Error() string {
	return fmt.Sprintf("backup part %q failed sha256 verification (archive is corrupt or tampered)", e.Part)
}

// ErrSecretDetected reports that a configured secret value appeared in the
// exported archive; the export fail-closes rather than leak it.
type ErrSecretDetected struct{ Name string }

func (e *ErrSecretDetected) Error() string {
	return fmt.Sprintf("refusing to export: configured secret %q detected in archive output", e.Name)
}

// readSchemaIdentity reads the v2 schema identity (family + version) from
// cortex_meta. Restore refuses databases without a recognizable v2 identity
// (fail-closed): the portable projection is only guaranteed against the v2
// family it was exported from.
func readSchemaIdentity(ctx context.Context, db *sql.DB) (family, version string, err error) {
	const q = `SELECT value FROM cortex_meta WHERE key = ?`
	if err := db.QueryRowContext(ctx, q, "schema_family").Scan(&family); err != nil {
		if errors.Is(err, sql.ErrNoRows) || isMissingCortexMeta(err) {
			return "", "", fmt.Errorf("backup: database has no cortex-v2 schema identity (cortex_meta.schema_family missing); refusing to operate")
		}
		return "", "", fmt.Errorf("backup: read schema family: %w", err)
	}
	if family == "" {
		return "", "", fmt.Errorf("backup: database has empty schema family; refusing to operate")
	}
	if err := db.QueryRowContext(ctx, q, "schema_version").Scan(&version); err != nil {
		if errors.Is(err, sql.ErrNoRows) || isMissingCortexMeta(err) {
			return "", "", fmt.Errorf("backup: database has no schema version (cortex_meta.schema_version missing); refusing to operate")
		}
		return "", "", fmt.Errorf("backup: read schema version: %w", err)
	}
	return family, version, nil
}

// isMissingCortexMeta reports driver errors for a missing cortex_meta table
// (modernc.org/sqlite phrasing), treated as "no v2 identity".
func isMissingCortexMeta(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such table: cortex_meta")
}

// parseSchemaVersion parses a "001"-style numeric schema version.
func parseSchemaVersion(v string) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, fmt.Errorf("empty schema version")
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("malformed schema version %q", v)
	}
	if n < 0 {
		return 0, fmt.Errorf("negative schema version %q", v)
	}
	return n, nil
}

// checkSchemaCompatible refuses archives whose schema version is NEWER than
// the target database. Older archives are accepted: v2 migrations are
// forward-only and additive, so older projections remain restorable.
func checkSchemaCompatible(backupVersion, currentVersion string) error {
	b, err := parseSchemaVersion(backupVersion)
	if err != nil {
		return fmt.Errorf("backup: archive %s", err)
	}
	c, err := parseSchemaVersion(currentVersion)
	if err != nil {
		return fmt.Errorf("backup: target database %s", err)
	}
	if b > c {
		return &ErrIncompatibleSchema{Backup: backupVersion, Current: currentVersion}
	}
	return nil
}

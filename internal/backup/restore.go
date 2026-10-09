package backup

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Options control a logical restore.
type RestoreOptions struct {
	// From is the source archive path (tar.gz produced by Export).
	From string
	// Secrets are credential values that must never have been exported; if
	// any appears inside the archive the restore refuses (the archive was
	// produced by a non-Cortex tool or tampered with).
	Secrets []Secret
}

// RestoreResult summarizes a completed restore. Restored/Skipped counts make
// re-running a restore idempotent and observable.
type RestoreResult struct {
	ObservationsRestored int
	ObservationsSkipped  int
	EdgesRestored        int
	EdgesSkipped         int
	SessionsRestored     int
	SessionsSkipped      int
	SchemaVer            string
}

// Restorer applies a logical snapshot to a v2 SQLite database, preserving
// original observation/edge IDs so graph relations and idempotent re-runs
// stay coherent.
//
// Restore semantics:
//   - manifest validation: format, format version, per-part sha256 (tamper
//     rejection) and schema-version compatibility (refuse newer, see
//     checkSchemaCompatible);
//   - single transaction; any failure rolls the whole restore back;
//   - idempotent: existing IDs are skipped (ON CONFLICT DO NOTHING), never
//     overwritten;
//   - embeddings are NOT restored. The target's observation_vectors are
//     untouched; absent vectors mean the observation is pending re-embed and
//     `cortex reindex` (or the embedding worker) self-heals.
type Restorer struct {
	DB *sql.DB
}

// staged part files extracted from the archive before applying anything.
type staged struct {
	path  string
	count int
	hash  string
}

func (r *Restorer) Restore(ctx context.Context, opts RestoreOptions) (*RestoreResult, error) {
	if r.DB == nil {
		return nil, fmt.Errorf("restore: nil database handle")
	}
	if strings.TrimSpace(opts.From) == "" {
		return nil, fmt.Errorf("restore: --from is required")
	}

	f, err := os.Open(opts.From)
	if err != nil {
		return nil, fmt.Errorf("restore: open archive: %w", err)
	}
	defer func() { _ = f.Close() }()

	manifest, parts, tmpDir, err := extractArchive(f, opts.Secrets)
	if err != nil {
		return nil, err
	}
	defer cleanupTempDir(tmpDir)

	// Verify manifest hashes BEFORE touching the database (tamper rejection).
	// Only the known parts are verified against staged data; unknown entries
	// in a newer archive format are ignored (forward compatibility) and are
	// never written to disk by this version.
	for _, name := range []string{ObservationsPart, EdgesPart, SessionsPart} {
		want, ok := manifest.Parts[name]
		if !ok {
			continue
		}
		got, staged := parts[name]
		if !staged {
			return nil, fmt.Errorf("restore: manifest references missing part %q", name)
		}
		if got.hash != want.SHA256 {
			return nil, &ErrTampered{Part: name}
		}
		if got.count != want.Count {
			return nil, &ErrTampered{Part: name}
		}
	}

	// Schema compatibility BEFORE any write.
	family, version, err := readSchemaIdentity(ctx, r.DB)
	if err != nil {
		return nil, err
	}
	if manifest.SchemaFamily != "" && manifest.SchemaFamily != family {
		return nil, fmt.Errorf("restore: archive schema family %q does not match database family %q", manifest.SchemaFamily, family)
	}
	if err := checkSchemaCompatible(manifest.SchemaVersion, version); err != nil {
		return nil, err
	}

	res, err := r.apply(ctx, parts)
	if err != nil {
		return nil, err
	}
	res.SchemaVer = version
	return res, nil
}

// extractArchive streams the tar.gz, staging data parts to a temp directory
// while hashing them, and returns the parsed manifest. The manifest must
// exist; unknown extra entries are ignored (forward compatibility).
func extractArchive(f *os.File, secrets []Secret) (*Manifest, map[string]staged, string, error) {
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, nil, "", fmt.Errorf("restore: not a gzip archive: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	tmpDir, err := os.MkdirTemp("", "cortex-restore-")
	if err != nil {
		return nil, nil, "", fmt.Errorf("restore: temp dir: %w", err)
	}
	// The caller owns tmpDir removal (deferred cleanupTempDir in Restore);
	// extraction failures clean up before returning.
	parts := map[string]staged{}
	var manifest *Manifest

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanupTempDir(tmpDir)
			return nil, nil, "", fmt.Errorf("restore: read archive: %w", err)
		}

		// Allow-list archive entry names: nothing attacker-controlled ever
		// reaches a filesystem path (Zip Slip / CWE-022 prevention). Unknown
		// entries from a newer archive format are ignored, never written.
		// All allow-listed names are flat constants without separators.
		switch hdr.Name {
		case ManifestName, ObservationsPart, EdgesPart, SessionsPart:
		default:
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			cleanupTempDir(tmpDir)
			return nil, nil, "", fmt.Errorf("restore: refusing non-regular archive entry %q", hdr.Name)
		}

		hasher := sha256.New()
		body := io.TeeReader(tr, hasher)

		if hdr.Name == ManifestName {
			dec := json.NewDecoder(body)
			m := &Manifest{}
			if err := dec.Decode(m); err != nil {
				cleanupTempDir(tmpDir)
				return nil, nil, "", fmt.Errorf("restore: parse manifest: %w", err)
			}
			if m.Format != Format {
				cleanupTempDir(tmpDir)
				return nil, nil, "", fmt.Errorf("restore: not a %s archive (format %q)", Format, m.Format)
			}
			if m.FormatVersion > FormatVersion {
				cleanupTempDir(tmpDir)
				return nil, nil, "", fmt.Errorf("restore: archive format_version %d is newer than supported %d; upgrade cortex", m.FormatVersion, FormatVersion)
			}
			if err := assertNoSecrets(mustJSON(m), secrets); err != nil {
				cleanupTempDir(tmpDir)
				return nil, nil, "", err
			}
			manifest = m
			continue
		}

		// Stage data parts. hdr.Name is one of the flat allow-listed
		// constants, so the join cannot escape tmpDir.
		stagePath := filepath.Join(tmpDir, hdr.Name)
		out, err := os.OpenFile(stagePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			cleanupTempDir(tmpDir)
			return nil, nil, "", fmt.Errorf("restore: stage %q: %w", hdr.Name, err)
		}
		count := 0
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			if _, err := out.Write(append(bytes.Clone(line), '\n')); err != nil {
				_ = out.Close()
				cleanupTempDir(tmpDir)
				return nil, nil, "", fmt.Errorf("restore: stage %q: %w", hdr.Name, err)
			}
			count++
			if err := assertNoSecrets(line, secrets); err != nil {
				_ = out.Close()
				cleanupTempDir(tmpDir)
				return nil, nil, "", err
			}
		}
		if err := scanner.Err(); err != nil {
			_ = out.Close()
			cleanupTempDir(tmpDir)
			return nil, nil, "", fmt.Errorf("restore: scan %q: %w", hdr.Name, err)
		}
		if err := out.Close(); err != nil {
			cleanupTempDir(tmpDir)
			return nil, nil, "", fmt.Errorf("restore: stage %q: %w", hdr.Name, err)
		}
		parts[hdr.Name] = staged{path: stagePath, count: count, hash: hex.EncodeToString(hasher.Sum(nil))}
	}

	if manifest == nil {
		cleanupTempDir(tmpDir)
		return nil, nil, "", fmt.Errorf("restore: archive has no %s manifest", ManifestName)
	}
	return manifest, parts, tmpDir, nil
}

// apply runs the transactional restore: sessions, then observations, then
// edges (FK-safe order), each INSERT ... ON CONFLICT DO NOTHING so re-runs
// skip existing IDs.
func (r *Restorer) apply(ctx context.Context, parts map[string]staged) (*RestoreResult, error) {
	res := &RestoreResult{}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("restore: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if s, ok := parts[SessionsPart]; ok {
		n, err := restoreSessions(ctx, tx, s.path)
		if err != nil {
			return nil, err
		}
		res.SessionsRestored = n
		res.SessionsSkipped = s.count - n
	}
	if s, ok := parts[ObservationsPart]; ok {
		n, err := restoreObservations(ctx, tx, s.path)
		if err != nil {
			return nil, err
		}
		res.ObservationsRestored = n
		res.ObservationsSkipped = s.count - n
	}
	if s, ok := parts[EdgesPart]; ok {
		n, err := restoreEdges(ctx, tx, s.path)
		if err != nil {
			return nil, err
		}
		res.EdgesRestored = n
		res.EdgesSkipped = s.count - n
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("restore: commit: %w", err)
	}
	return res, nil
}

func restoreSessions(ctx context.Context, tx *sql.Tx, path string) (int, error) {
	rows, err := readJSONL[SessionRecord](path)
	if err != nil {
		return 0, err
	}
	const q = `
		INSERT INTO sessions (id, project, directory, started_at, ended_at, summary, tenant_id, workspace_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`
	n := 0
	for _, r := range rows {
		res, err := tx.ExecContext(ctx, q,
			r.ID, r.Project, r.Directory, r.StartedAt, nullString(r.EndedAt), nullString(r.Summary),
			nullString(r.TenantID), nullString(r.WorkspaceID))
		if err != nil {
			return n, fmt.Errorf("restore: session %q: %w", r.ID, err)
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			n++
		}
	}
	return n, nil
}

func restoreObservations(ctx context.Context, tx *sql.Tx, path string) (int, error) {
	rows, err := readJSONL[ObservationRecord](path)
	if err != nil {
		return 0, err
	}
	// ON CONFLICT(id) (not INSERT OR IGNORE): a CHECK-constraint violation
	// must fail the restore, not be silently swallowed as "already exists".
	const q = `
		INSERT INTO observations (id, session_id, type, title, content, tool_name, project,
		                          scope, topic_key, confidence, source, tags,
		                          tenant_id, workspace_id, owner_id, created_at, updated_at, valid_until)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`
	n := 0
	for _, r := range rows {
		res, err := tx.ExecContext(ctx, q,
			r.ID, r.SessionID, r.Type, r.Title, r.Content, nullString(r.ToolName), nullString(r.Project),
			r.Scope, nullString(r.TopicKey), r.Confidence, r.Source, nullString(r.Tags),
			nullString(r.TenantID), nullString(r.WorkspaceID), nullString(r.OwnerID),
			r.CreatedAt, r.UpdatedAt, nullString(r.ValidUntil))
		if err != nil {
			return n, fmt.Errorf("restore: observation %d: %w", r.ID, err)
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			n++
		}
	}
	return n, nil
}

func restoreEdges(ctx context.Context, tx *sql.Tx, path string) (int, error) {
	rows, err := readJSONL[EdgeRecord](path)
	if err != nil {
		return 0, err
	}
	const q = `
		INSERT INTO edges (id, from_obs_id, to_obs_id, relation_type, weight, confidence, source, reasoning,
		                   valid_from, invalid_at, valid_until, tenant_id, workspace_id,
		                   evolution_id, evolution_type, fact_state, change_reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`
	n := 0
	for _, r := range rows {
		res, err := tx.ExecContext(ctx, q,
			r.ID, r.FromObsID, r.ToObsID, r.RelationType, r.Weight, r.Confidence,
			nullString(r.Source), nullString(r.Reasoning), nullString(r.ValidFrom),
			nullString(r.InvalidAt), nullString(r.ValidUntil), nullString(r.TenantID),
			nullString(r.WorkspaceID), nullInt64(r.EvolutionID), r.EvolutionType, r.FactState,
			nullString(r.ChangeReason), nullString(r.CreatedAt))
		if err != nil {
			return n, fmt.Errorf("restore: edge %d: %w", r.ID, err)
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			n++
		}
	}
	return n, nil
}

// readJSONL decodes a staged JSONL part. Rows are buffered: a restore is a
// bounded administrative operation and a single transaction needs the full
// row set anyway.
func readJSONL[T any](path string) ([]T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("restore: read staged part: %w", err)
	}
	var out []T
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			return nil, fmt.Errorf("restore: decode record: %w", err)
		}
		out = append(out, v)
	}
	return out, nil
}

func nullString(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return s.String
}

func nullInt64(i sql.NullInt64) any {
	if !i.Valid {
		return nil
	}
	return i.Int64
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func cleanupTempDir(dir string) {
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

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
	"os"
	"strings"
	"time"
)

// Options control a logical export.
type ExportOptions struct {
	// Out is the destination file path (a tar.gz archive).
	Out string
	// Tenant optionally restricts the export to one tenant id.
	Tenant string
	// ToolVersion is recorded in the manifest (the CLI injects its version).
	ToolVersion string
	// Secrets are credential values that must never appear in the archive
	// (defense-in-depth; see package doc).
	Secrets []Secret
	// Now overrides the manifest timestamp (tests).
	Now func() time.Time
}

// ExportResult summarizes a completed export.
type ExportResult struct {
	Path         string
	SizeBytes    int64
	Observations int
	Edges        int
	Sessions     int
	SchemaVer    string
}

// ObservationRecord is one portable observation row. Timestamps are carried
// verbatim as stored (TEXT) so a restore is byte-faithful.
type ObservationRecord struct {
	ID             int64           `json:"id"`
	SessionID      string          `json:"session_id"`
	Type           string          `json:"type"`
	Title          string          `json:"title"`
	Content        string          `json:"content"`
	ToolName       sql.NullString  `json:"tool_name,omitempty"`
	Project        sql.NullString  `json:"project,omitempty"`
	Scope          string          `json:"scope"`
	TopicKey       sql.NullString  `json:"topic_key,omitempty"`
	Confidence     float64         `json:"confidence"`
	Source         string          `json:"source"`
	Tags           sql.NullString  `json:"tags,omitempty"`
	TenantID       sql.NullString  `json:"tenant_id,omitempty"`
	WorkspaceID    sql.NullString  `json:"workspace_id,omitempty"`
	OwnerID        sql.NullString  `json:"owner_id,omitempty"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
	ValidUntil     sql.NullString  `json:"valid_until,omitempty"`
	EmbeddingState *EmbeddingState `json:"embedding_state,omitempty"`
}

// EmbeddingState records the per-observation embedding metadata. The vector
// itself is intentionally NOT exported: it is regenerable and potentially
// re-identifying.
type EmbeddingState struct {
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
}

// EdgeRecord is one portable graph relation row.
type EdgeRecord struct {
	ID            int64          `json:"id"`
	FromObsID     int64          `json:"from_obs_id"`
	ToObsID       int64          `json:"to_obs_id"`
	RelationType  string         `json:"relation_type"`
	Weight        float64        `json:"weight"`
	Confidence    float64        `json:"confidence"`
	Source        sql.NullString `json:"source,omitempty"`
	Reasoning     sql.NullString `json:"reasoning,omitempty"`
	ValidFrom     sql.NullString `json:"valid_from,omitempty"`
	InvalidAt     sql.NullString `json:"invalid_at,omitempty"`
	ValidUntil    sql.NullString `json:"valid_until,omitempty"`
	TenantID      sql.NullString `json:"tenant_id,omitempty"`
	WorkspaceID   sql.NullString `json:"workspace_id,omitempty"`
	EvolutionID   sql.NullInt64  `json:"evolution_id,omitempty"`
	EvolutionType string         `json:"evolution_type"`
	FactState     string         `json:"fact_state"`
	ChangeReason  sql.NullString `json:"change_reason,omitempty"`
	CreatedAt     sql.NullString `json:"created_at,omitempty"`
}

// SessionRecord is one portable session row.
type SessionRecord struct {
	ID          string         `json:"id"`
	Project     string         `json:"project"`
	Directory   string         `json:"directory"`
	StartedAt   string         `json:"started_at"`
	EndedAt     sql.NullString `json:"ended_at,omitempty"`
	Summary     sql.NullString `json:"summary,omitempty"`
	TenantID    sql.NullString `json:"tenant_id,omitempty"`
	WorkspaceID sql.NullString `json:"workspace_id,omitempty"`
}

// Exporter produces logical snapshots from the SQLite memory store.
//
// The projection deliberately reads schema-owned columns through a fixed
// allow-list of SELECTs rather than the domain model: tenant/workspace/owner
// columns and verbatim stored timestamps are part of the portable snapshot
// but are deliberately absent from the local domain types. All other writes
// in the codebase keep flowing through the store bundle; this package is a
// read-only dumper plus an ID-preserving restorer (see restore.go).
type Exporter struct {
	DB *sql.DB
}

// Export writes the tar.gz archive to opts.Out.
func (e *Exporter) Export(ctx context.Context, opts ExportOptions) (*ExportResult, error) {
	if e.DB == nil {
		return nil, fmt.Errorf("backup: nil database handle")
	}
	if strings.TrimSpace(opts.Out) == "" {
		return nil, fmt.Errorf("backup: --out is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	family, version, err := readSchemaIdentity(ctx, e.DB)
	if err != nil {
		return nil, err
	}

	out, err := os.OpenFile(opts.Out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("backup: create %s: %w", opts.Out, err)
	}
	defer func() { _ = out.Close() }()

	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	manifest := Manifest{
		Format:        Format,
		FormatVersion: FormatVersion,
		SchemaFamily:  family,
		SchemaVersion: version,
		ToolVersion:   opts.ToolVersion,
		CreatedAt:     now().UTC().Format(time.RFC3339),
		Source:        Source{Mode: "local-sqlite", TenantID: opts.Tenant},
		Parts:         map[string]Part{},
		Notes: map[string]string{
			"embeddings": "vectors are not exported; run 'cortex reindex' (or let the embedding worker) regenerate them after restore",
		},
	}

	writePart := func(name string, rows func(w *bufio.Writer) (int, error)) error {
		var buf bytes.Buffer
		bw := bufio.NewWriter(&buf)
		n, err := rows(bw)
		if err != nil {
			return err
		}
		if err := bw.Flush(); err != nil {
			return err
		}
		payload := buf.Bytes()
		if err := assertNoSecrets(payload, opts.Secrets); err != nil {
			return err
		}
		sum := sha256.Sum256(payload)
		manifest.Parts[name] = Part{Count: n, SHA256: hex.EncodeToString(sum[:])}
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o600,
			Size: int64(len(payload)),
		}); err != nil {
			return err
		}
		if _, err := tw.Write(payload); err != nil {
			return err
		}
		return nil
	}

	// Observations (excluding soft-deleted rows; gc owns those).
	if err := writePart(ObservationsPart, func(w *bufio.Writer) (int, error) {
		return streamObservations(ctx, e.DB, opts.Tenant, w)
	}); err != nil {
		return nil, err
	}

	// Graph relations.
	if err := writePart(EdgesPart, func(w *bufio.Writer) (int, error) {
		return streamEdges(ctx, e.DB, opts.Tenant, w)
	}); err != nil {
		return nil, err
	}

	// Sessions.
	if err := writePart(SessionsPart, func(w *bufio.Writer) (int, error) {
		return streamSessions(ctx, e.DB, opts.Tenant, w)
	}); err != nil {
		return nil, err
	}

	// Manifest last: it hashes every part written above.
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("backup: marshal manifest: %w", err)
	}
	if err := assertNoSecrets(manifestBytes, opts.Secrets); err != nil {
		return nil, err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: ManifestName,
		Mode: 0o600,
		Size: int64(len(manifestBytes)),
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(manifestBytes); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("backup: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("backup: close gzip: %w", err)
	}
	if err := out.Sync(); err != nil {
		return nil, fmt.Errorf("backup: sync: %w", err)
	}
	if err := out.Close(); err != nil {
		return nil, fmt.Errorf("backup: close: %w", err)
	}

	fi, err := os.Stat(opts.Out)
	if err != nil {
		return nil, fmt.Errorf("backup: stat output: %w", err)
	}

	return &ExportResult{
		Path:         opts.Out,
		SizeBytes:    fi.Size(),
		Observations: manifest.Parts[ObservationsPart].Count,
		Edges:        manifest.Parts[EdgesPart].Count,
		Sessions:     manifest.Parts[SessionsPart].Count,
		SchemaVer:    version,
	}, nil
}

// Parts are hashed from their fully-buffered payload; corpus-scale parts are
// bounded by the store's own retention, keeping memory use acceptable.

func streamObservations(ctx context.Context, db *sql.DB, tenant string, w *bufio.Writer) (int, error) {
	query := `
		SELECT o.id, o.session_id, o.type, o.title, o.content, o.tool_name, o.project,
		       o.scope, o.topic_key, o.confidence, o.source, o.tags,
		       o.tenant_id, o.workspace_id, o.owner_id, o.created_at, o.updated_at, o.valid_until,
		       v.embedding_model, v.dimensions
		FROM observations o
		LEFT JOIN observation_vectors v ON v.observation_id = o.id
		WHERE o.deleted_at IS NULL`
	args := []any{}
	if tenant != "" {
		query += ` AND o.tenant_id = ?`
		args = append(args, tenant)
	}
	query += ` ORDER BY o.id`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("backup: query observations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	enc := json.NewEncoder(w)
	count := 0
	for rows.Next() {
		var r ObservationRecord
		var model sql.NullString
		var dims sql.NullInt64
		if err := rows.Scan(&r.ID, &r.SessionID, &r.Type, &r.Title, &r.Content, &r.ToolName,
			&r.Project, &r.Scope, &r.TopicKey, &r.Confidence, &r.Source, &r.Tags,
			&r.TenantID, &r.WorkspaceID, &r.OwnerID, &r.CreatedAt, &r.UpdatedAt, &r.ValidUntil,
			&model, &dims); err != nil {
			return count, fmt.Errorf("backup: scan observation: %w", err)
		}
		if model.Valid || dims.Valid {
			r.EmbeddingState = &EmbeddingState{Model: model.String, Dimensions: int(dims.Int64)}
		}
		if err := enc.Encode(r); err != nil {
			return count, fmt.Errorf("backup: encode observation: %w", err)
		}
		count++
	}
	return count, rows.Err()
}

func streamEdges(ctx context.Context, db *sql.DB, tenant string, w *bufio.Writer) (int, error) {
	query := `
		SELECT id, from_obs_id, to_obs_id, relation_type, weight, confidence, source, reasoning,
		       valid_from, invalid_at, valid_until, tenant_id, workspace_id,
		       evolution_id, evolution_type, fact_state, change_reason, created_at
		FROM edges`
	args := []any{}
	if tenant != "" {
		query += ` WHERE tenant_id = ?`
		args = append(args, tenant)
	}
	query += ` ORDER BY id`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("backup: query edges: %w", err)
	}
	defer func() { _ = rows.Close() }()

	enc := json.NewEncoder(w)
	count := 0
	for rows.Next() {
		var r EdgeRecord
		if err := rows.Scan(&r.ID, &r.FromObsID, &r.ToObsID, &r.RelationType, &r.Weight, &r.Confidence,
			&r.Source, &r.Reasoning, &r.ValidFrom, &r.InvalidAt, &r.ValidUntil,
			&r.TenantID, &r.WorkspaceID, &r.EvolutionID, &r.EvolutionType, &r.FactState,
			&r.ChangeReason, &r.CreatedAt); err != nil {
			return count, fmt.Errorf("backup: scan edge: %w", err)
		}
		if err := enc.Encode(r); err != nil {
			return count, fmt.Errorf("backup: encode edge: %w", err)
		}
		count++
	}
	return count, rows.Err()
}

func streamSessions(ctx context.Context, db *sql.DB, tenant string, w *bufio.Writer) (int, error) {
	query := `SELECT id, project, directory, started_at, ended_at, summary, tenant_id, workspace_id FROM sessions`
	args := []any{}
	if tenant != "" {
		query += ` WHERE tenant_id = ?`
		args = append(args, tenant)
	}
	query += ` ORDER BY started_at`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("backup: query sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	enc := json.NewEncoder(w)
	count := 0
	for rows.Next() {
		var r SessionRecord
		if err := rows.Scan(&r.ID, &r.Project, &r.Directory, &r.StartedAt, &r.EndedAt,
			&r.Summary, &r.TenantID, &r.WorkspaceID); err != nil {
			return count, fmt.Errorf("backup: scan session: %w", err)
		}
		if err := enc.Encode(r); err != nil {
			return count, fmt.Errorf("backup: encode session: %w", err)
		}
		count++
	}
	return count, rows.Err()
}

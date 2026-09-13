package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain/payload"
)

// TransientPayloadStore implements payload.Store using SQLite and FTS5.
type TransientPayloadStore struct {
	db *sql.DB
}

// NewTransientPayloadStore creates and initializes the TransientPayloadStore.
func NewTransientPayloadStore(db *sql.DB) (*TransientPayloadStore, error) {
	s := &TransientPayloadStore{db: db}
	if err := s.ensureSchema(context.Background()); err != nil {
		return nil, fmt.Errorf("sqlite: ensure transient payloads schema: %w", err)
	}
	return s, nil
}

func (s *TransientPayloadStore) ensureSchema(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS transient_payloads (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		project TEXT NOT NULL,
		source_tool TEXT NOT NULL,
		content_type TEXT NOT NULL,
		byte_count INTEGER NOT NULL,
		tokens_saved INTEGER NOT NULL,
		snippet TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);
	CREATE INDEX IF NOT EXISTS idx_transient_payloads_session ON transient_payloads(session_id);
	CREATE INDEX IF NOT EXISTS idx_transient_payloads_project ON transient_payloads(project);

	CREATE VIRTUAL TABLE IF NOT EXISTS transient_payloads_fts USING fts5(
		payload_id UNINDEXED,
		content,
		tokenize = 'porter unicode61'
	);
	`
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

// Save inserts a transient payload and indexes it in FTS5.
func (s *TransientPayloadStore) Save(ctx context.Context, p *payload.TransientPayload) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	query := `
	INSERT INTO transient_payloads (
		id, session_id, project, source_tool, content_type,
		byte_count, tokens_saved, snippet, content, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`
	createdAt := p.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	_, err = tx.ExecContext(ctx, query,
		p.ID, p.SessionID, p.Project, p.SourceTool, p.ContentType,
		p.ByteCount, p.TokensSaved, p.Snippet, p.Content, createdAt.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert transient payload: %w", err)
	}

	ftsQuery := `INSERT INTO transient_payloads_fts (payload_id, content) VALUES (?, ?);`
	if _, err := tx.ExecContext(ctx, ftsQuery, p.ID, p.Content); err != nil {
		return fmt.Errorf("insert fts transient payload: %w", err)
	}

	return tx.Commit()
}

// Get retrieves a transient payload by its unique ID.
func (s *TransientPayloadStore) Get(ctx context.Context, id string) (*payload.TransientPayload, error) {
	query := `
	SELECT id, session_id, project, source_tool, content_type,
	       byte_count, tokens_saved, snippet, content, created_at
	FROM transient_payloads WHERE id = ?;
	`
	row := s.db.QueryRowContext(ctx, query, id)

	var p payload.TransientPayload
	var createdAtStr string

	err := row.Scan(
		&p.ID, &p.SessionID, &p.Project, &p.SourceTool, &p.ContentType,
		&p.ByteCount, &p.TokensSaved, &p.Snippet, &p.Content, &createdAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("transient payload not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("query transient payload: %w", err)
	}

	p.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
	return &p, nil
}

// Search searches for matching snippets within a payload (or across all payloads if id is empty).
func (s *TransientPayloadStore) Search(ctx context.Context, id string, query string, limit int) ([]payload.SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}

	var rows *sql.Rows
	var err error

	if id != "" {
		sqlQuery := `
		SELECT snippet(transient_payloads_fts, 1, '<b>', '</b>', '...', 32), rank
		FROM transient_payloads_fts
		WHERE transient_payloads_fts MATCH ? AND payload_id = ?
		ORDER BY rank
		LIMIT ?;
		`
		rows, err = s.db.QueryContext(ctx, sqlQuery, query, id, limit)
	} else {
		sqlQuery := `
		SELECT snippet(transient_payloads_fts, 1, '<b>', '</b>', '...', 32), rank
		FROM transient_payloads_fts
		WHERE transient_payloads_fts MATCH ?
		ORDER BY rank
		LIMIT ?;
		`
		rows, err = s.db.QueryContext(ctx, sqlQuery, query, limit)
	}

	if err != nil {
		return nil, fmt.Errorf("search transient payloads fts: %w", err)
	}
	defer rows.Close()

	var results []payload.SearchResult
	for rows.Next() {
		var res payload.SearchResult
		if err := rows.Scan(&res.Snippet, &res.Rank); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		results = append(results, res)
	}
	return results, rows.Err()
}

// PurgeSession deletes all payloads associated with a specific session.
func (s *TransientPayloadStore) PurgeSession(ctx context.Context, sessionID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	delFTS := `DELETE FROM transient_payloads_fts WHERE payload_id IN (SELECT id FROM transient_payloads WHERE session_id = ?);`
	if _, err := tx.ExecContext(ctx, delFTS, sessionID); err != nil {
		return fmt.Errorf("delete fts session payloads: %w", err)
	}

	delPayloads := `DELETE FROM transient_payloads WHERE session_id = ?;`
	if _, err := tx.ExecContext(ctx, delPayloads, sessionID); err != nil {
		return fmt.Errorf("delete session payloads: %w", err)
	}

	return tx.Commit()
}

// Stats returns the aggregated tokens saved, bytes compressed, and payload count for a session.
func (s *TransientPayloadStore) Stats(ctx context.Context, sessionID string) (int, int, int, error) {
	var query string
	var row *sql.Row

	if sessionID != "" {
		query = `SELECT COALESCE(SUM(tokens_saved), 0), COALESCE(SUM(byte_count), 0), COUNT(*) FROM transient_payloads WHERE session_id = ?;`
		row = s.db.QueryRowContext(ctx, query, sessionID)
	} else {
		query = `SELECT COALESCE(SUM(tokens_saved), 0), COALESCE(SUM(byte_count), 0), COUNT(*) FROM transient_payloads;`
		row = s.db.QueryRowContext(ctx, query)
	}

	var saved, bytesTotal, count int
	if err := row.Scan(&saved, &bytesTotal, &count); err != nil {
		return 0, 0, 0, fmt.Errorf("scan transient stats: %w", err)
	}
	return saved, bytesTotal, count, nil
}

// Package postgres contains the server-mode repositories.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

// PostgresCodeStore implements code.Store over PostgreSQL.
type PostgresCodeStore struct {
	pool *pgxpool.Pool
}

// NewPostgresCodeStore creates a new PostgreSQL code store and ensures schema.
func NewPostgresCodeStore(pool *pgxpool.Pool) (*PostgresCodeStore, error) {
	s := &PostgresCodeStore{pool: pool}
	if err := s.ensureSchema(context.Background()); err != nil {
		return nil, fmt.Errorf("postgres: ensure code schema: %w", err)
	}
	return s, nil
}

func (s *PostgresCodeStore) ensureSchema(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS code_symbols (
		id VARCHAR(64) PRIMARY KEY,
		project VARCHAR(128) NOT NULL,
		file_path TEXT NOT NULL,
		line_number INT NOT NULL,
		end_line INT,
		start_col INT,
		end_col INT,
		kind VARCHAR(32) NOT NULL,
		name VARCHAR(255) NOT NULL,
		package_name VARCHAR(128),
		parent_id VARCHAR(128),
		visibility VARCHAR(32),
		signature TEXT,
		doc_summary TEXT,
		parameters JSONB,
		return_type TEXT,
		complexity INT DEFAULT 1,
		metadata JSONB,
		file_hash VARCHAR(64),
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_code_symbols_project ON code_symbols(project);
	CREATE INDEX IF NOT EXISTS idx_code_symbols_file ON code_symbols(project, file_path);
	CREATE INDEX IF NOT EXISTS idx_code_symbols_name ON code_symbols(project, name);

	CREATE TABLE IF NOT EXISTS code_relations (
		id BIGSERIAL PRIMARY KEY,
		project VARCHAR(128) NOT NULL,
		source_id VARCHAR(64) NOT NULL,
		target_id VARCHAR(64) NOT NULL,
		relation VARCHAR(32) NOT NULL,
		confidence REAL DEFAULT 1.0,
		reasoning TEXT,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE (project, source_id, target_id, relation)
	);
	CREATE INDEX IF NOT EXISTS idx_code_relations_project ON code_relations(project);
	CREATE INDEX IF NOT EXISTS idx_code_relations_source ON code_relations(source_id);
	CREATE INDEX IF NOT EXISTS idx_code_relations_target ON code_relations(target_id);
	`
	if _, err := s.pool.Exec(ctx, schema); err != nil {
		return err
	}

	// Safe column additions for existing PostgreSQL tables
	alterCols := []string{
		"ALTER TABLE code_symbols ADD COLUMN IF NOT EXISTS end_line INT;",
		"ALTER TABLE code_symbols ADD COLUMN IF NOT EXISTS start_col INT;",
		"ALTER TABLE code_symbols ADD COLUMN IF NOT EXISTS end_col INT;",
		"ALTER TABLE code_symbols ADD COLUMN IF NOT EXISTS parent_id VARCHAR(128);",
		"ALTER TABLE code_symbols ADD COLUMN IF NOT EXISTS visibility VARCHAR(32);",
		"ALTER TABLE code_symbols ADD COLUMN IF NOT EXISTS parameters JSONB;",
		"ALTER TABLE code_symbols ADD COLUMN IF NOT EXISTS return_type TEXT;",
		"ALTER TABLE code_symbols ADD COLUMN IF NOT EXISTS complexity INT DEFAULT 1;",
		"ALTER TABLE code_symbols ADD COLUMN IF NOT EXISTS metadata JSONB;",
	}
	for _, q := range alterCols {
		_, _ = s.pool.Exec(ctx, q)
	}

	return nil
}

// SaveSymbols writes code symbols in an atomic batch using ON CONFLICT DO UPDATE.
func (s *PostgresCodeStore) SaveSymbols(ctx context.Context, symbols []code.Symbol) error {
	if len(symbols) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	query := `
		INSERT INTO code_symbols (
			id, project, file_path, line_number, end_line, kind, name,
			package_name, parent_id, visibility, signature, doc_summary,
			parameters, return_type, complexity, metadata, file_hash, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		ON CONFLICT(id) DO UPDATE SET
			line_number = EXCLUDED.line_number,
			end_line = EXCLUDED.end_line,
			kind = EXCLUDED.kind,
			name = EXCLUDED.name,
			package_name = EXCLUDED.package_name,
			parent_id = EXCLUDED.parent_id,
			visibility = EXCLUDED.visibility,
			signature = EXCLUDED.signature,
			doc_summary = EXCLUDED.doc_summary,
			parameters = EXCLUDED.parameters,
			return_type = EXCLUDED.return_type,
			complexity = EXCLUDED.complexity,
			metadata = EXCLUDED.metadata,
			file_hash = EXCLUDED.file_hash,
			updated_at = EXCLUDED.updated_at;
	`

	now := time.Now().UTC()
	for _, sym := range symbols {
		createdAt := sym.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		updatedAt := sym.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = now
		}

		var paramsJSON, metaJSON []byte
		if len(sym.Parameters) > 0 {
			paramsJSON, _ = json.Marshal(sym.Parameters)
		}
		if len(sym.Metadata) > 0 {
			metaJSON, _ = json.Marshal(sym.Metadata)
		}

		endLine := sym.EndLine
		if endLine <= 0 {
			endLine = sym.LineNumber
		}
		complexity := sym.Complexity
		if complexity <= 0 {
			complexity = 1
		}

		if _, err := tx.Exec(ctx, query,
			sym.ID, sym.Project, sym.FilePath, sym.LineNumber, endLine, sym.Kind, sym.Name,
			sym.PackageName, sym.ParentID, sym.Visibility, sym.Signature, sym.DocSummary,
			paramsJSON, sym.ReturnType, complexity, metaJSON, sym.FileHash, createdAt, updatedAt,
		); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// GetSymbolByID retrieves a single symbol by its ID.
func (s *PostgresCodeStore) GetSymbolByID(ctx context.Context, id string) (*code.Symbol, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, project, file_path, line_number, COALESCE(end_line, line_number), kind, name,
		       COALESCE(package_name, ''), COALESCE(parent_id, ''), COALESCE(visibility, ''),
		       COALESCE(signature, ''), COALESCE(doc_summary, ''),
		       parameters, COALESCE(return_type, ''), COALESCE(complexity, 1), metadata,
		       COALESCE(file_hash, ''), created_at, updated_at
		  FROM code_symbols
		 WHERE id = $1
	`, id)

	var sym code.Symbol
	var paramsRaw, metaRaw []byte

	err := row.Scan(
		&sym.ID, &sym.Project, &sym.FilePath, &sym.LineNumber, &sym.EndLine, &sym.Kind, &sym.Name,
		&sym.PackageName, &sym.ParentID, &sym.Visibility, &sym.Signature, &sym.DocSummary,
		&paramsRaw, &sym.ReturnType, &sym.Complexity, &metaRaw,
		&sym.FileHash, &sym.CreatedAt, &sym.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if len(paramsRaw) > 0 {
		_ = json.Unmarshal(paramsRaw, &sym.Parameters)
	}
	if len(metaRaw) > 0 {
		_ = json.Unmarshal(metaRaw, &sym.Metadata)
	}

	return &sym, nil
}

// ListSymbols queries symbols matching filter parameters.
func (s *PostgresCodeStore) ListSymbols(ctx context.Context, filter code.SymbolFilter) ([]code.Symbol, error) {
	var sb strings.Builder
	sb.WriteString(`
		SELECT id, project, file_path, line_number, COALESCE(end_line, line_number), kind, name,
		       COALESCE(package_name, ''), COALESCE(parent_id, ''), COALESCE(visibility, ''),
		       COALESCE(signature, ''), COALESCE(doc_summary, ''),
		       parameters, COALESCE(return_type, ''), COALESCE(complexity, 1), metadata,
		       COALESCE(file_hash, ''), created_at, updated_at
		  FROM code_symbols
		 WHERE 1=1
	`)
	var args []any
	argIdx := 1

	if filter.Project != "" {
		fmt.Fprintf(&sb, " AND project = $%d", argIdx)
		args = append(args, filter.Project)
		argIdx++
	}
	if filter.FilePath != "" {
		fmt.Fprintf(&sb, " AND file_path = $%d", argIdx)
		args = append(args, filter.FilePath)
		argIdx++
	}
	if filter.Kind != "" {
		fmt.Fprintf(&sb, " AND kind = $%d", argIdx)
		args = append(args, filter.Kind)
		argIdx++
	}
	if filter.PackageName != "" {
		fmt.Fprintf(&sb, " AND package_name = $%d", argIdx)
		args = append(args, filter.PackageName)
		argIdx++
	}
	if filter.Query != "" {
		fmt.Fprintf(&sb, " AND (name ILIKE $%d OR signature ILIKE $%d OR doc_summary ILIKE $%d)", argIdx, argIdx, argIdx)
		pattern := "%" + filter.Query + "%"
		args = append(args, pattern)
		argIdx++
	}

	sb.WriteString(" ORDER BY file_path ASC, line_number ASC")

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	} else if limit > 100000 {
		limit = 100000
	}
	fmt.Fprintf(&sb, " LIMIT $%d", argIdx)
	args = append(args, limit)
	argIdx++

	if filter.Offset > 0 {
		fmt.Fprintf(&sb, " OFFSET $%d", argIdx)
		args = append(args, filter.Offset)
	}

	rows, err := s.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var symbols []code.Symbol
	for rows.Next() {
		var sym code.Symbol
		var paramsRaw, metaRaw []byte
		if err := rows.Scan(
			&sym.ID, &sym.Project, &sym.FilePath, &sym.LineNumber, &sym.EndLine, &sym.Kind, &sym.Name,
			&sym.PackageName, &sym.ParentID, &sym.Visibility, &sym.Signature, &sym.DocSummary,
			&paramsRaw, &sym.ReturnType, &sym.Complexity, &metaRaw,
			&sym.FileHash, &sym.CreatedAt, &sym.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if len(paramsRaw) > 0 {
			_ = json.Unmarshal(paramsRaw, &sym.Parameters)
		}
		if len(metaRaw) > 0 {
			_ = json.Unmarshal(metaRaw, &sym.Metadata)
		}
		symbols = append(symbols, sym)
	}

	return symbols, rows.Err()
}

// CountSymbols returns the total count of symbols matching the filter.
func (s *PostgresCodeStore) CountSymbols(ctx context.Context, filter code.SymbolFilter) (int, error) {
	var sb strings.Builder
	sb.WriteString("SELECT COUNT(*) FROM code_symbols WHERE 1=1")
	var args []any
	argIdx := 1

	if filter.Project != "" {
		fmt.Fprintf(&sb, " AND project = $%d", argIdx)
		args = append(args, filter.Project)
		argIdx++
	}
	if filter.FilePath != "" {
		fmt.Fprintf(&sb, " AND file_path = $%d", argIdx)
		args = append(args, filter.FilePath)
		argIdx++
	}
	if filter.Kind != "" {
		fmt.Fprintf(&sb, " AND kind = $%d", argIdx)
		args = append(args, filter.Kind)
	}

	var count int
	err := s.pool.QueryRow(ctx, sb.String(), args...).Scan(&count)
	return count, err
}

// DeleteSymbolsByProject removes all symbols for a project.
func (s *PostgresCodeStore) DeleteSymbolsByProject(ctx context.Context, project string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM code_symbols WHERE project = $1", project)
	return err
}

// DeleteSymbolsByFile removes symbols for a specific file.
func (s *PostgresCodeStore) DeleteSymbolsByFile(ctx context.Context, project, filePath string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM code_symbols WHERE project = $1 AND file_path = $2", project, filePath)
	return err
}

// SaveRelations writes code relationships in batch.
func (s *PostgresCodeStore) SaveRelations(ctx context.Context, relations []code.Relation) error {
	if len(relations) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	query := `
		INSERT INTO code_relations (
			project, source_id, target_id, relation, confidence, reasoning, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (project, source_id, target_id, relation) DO NOTHING;
	`

	now := time.Now().UTC()
	for _, rel := range relations {
		createdAt := rel.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		confidence := rel.Confidence
		if confidence <= 0 {
			confidence = 1.0
		}

		if _, err := tx.Exec(ctx, query,
			rel.Project, rel.SourceID, rel.TargetID, rel.Relation, confidence, rel.Reasoning, createdAt,
		); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// GetGraph retrieves the complete CodeGraph for a project.
func (s *PostgresCodeStore) GetGraph(ctx context.Context, project string) (*code.CodeGraph, error) {
	symbols, err := s.ListSymbols(ctx, code.SymbolFilter{Project: project, Limit: 100000})
	if err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, project, source_id, target_id, relation, confidence, COALESCE(reasoning, ''), created_at
		  FROM code_relations
		 WHERE project = $1
	`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var relations []code.Relation
	for rows.Next() {
		var rel code.Relation
		if err := rows.Scan(
			&rel.ID, &rel.Project, &rel.SourceID, &rel.TargetID, &rel.Relation,
			&rel.Confidence, &rel.Reasoning, &rel.CreatedAt,
		); err != nil {
			return nil, err
		}
		relations = append(relations, rel)
	}

	return &code.CodeGraph{
		Project:   project,
		Symbols:   symbols,
		Relations: relations,
	}, rows.Err()
}

// ListRelationsBySymbol retrieves all relationships for a symbol.
func (s *PostgresCodeStore) ListRelationsBySymbol(ctx context.Context, symbolID string) ([]code.Relation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, project, source_id, target_id, relation, confidence, COALESCE(reasoning, ''), created_at
		  FROM code_relations
		 WHERE source_id = $1 OR target_id = $1
	`, symbolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var relations []code.Relation
	for rows.Next() {
		var rel code.Relation
		if err := rows.Scan(
			&rel.ID, &rel.Project, &rel.SourceID, &rel.TargetID, &rel.Relation,
			&rel.Confidence, &rel.Reasoning, &rel.CreatedAt,
		); err != nil {
			return nil, err
		}
		relations = append(relations, rel)
	}

	return relations, rows.Err()
}

// DeleteRelationsByProject removes all relationships for a project.
func (s *PostgresCodeStore) DeleteRelationsByProject(ctx context.Context, project string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM code_relations WHERE project = $1", project)
	return err
}

// DeleteRelationsByFile removes relationships related to a file.
func (s *PostgresCodeStore) DeleteRelationsByFile(ctx context.Context, project, filePath string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM code_relations
		 WHERE project = $1
		   AND (source_id IN (SELECT id FROM code_symbols WHERE project = $1 AND file_path = $2)
		     OR target_id IN (SELECT id FROM code_symbols WHERE project = $1 AND file_path = $2))
	`, project, filePath)
	return err
}

// Package sqlite provides SQLite persistence for Cortex.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

// CodeStore implements code.Store over SQLite.
type CodeStore struct {
	db *sql.DB
}

// NewCodeStore creates and initializes a CodeStore, ensuring tables exist.
func NewCodeStore(db *sql.DB) (*CodeStore, error) {
	s := &CodeStore{db: db}
	if err := s.ensureSchema(context.Background()); err != nil {
		return nil, fmt.Errorf("sqlite: ensure code schema: %w", err)
	}
	return s, nil
}

func (s *CodeStore) ensureSchema(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS code_symbols (
		id TEXT PRIMARY KEY,
		project TEXT NOT NULL,
		file_path TEXT NOT NULL,
		line_number INTEGER NOT NULL,
		kind TEXT NOT NULL,
		name TEXT NOT NULL,
		package_name TEXT,
		signature TEXT,
		doc_summary TEXT,
		file_hash TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);
	CREATE INDEX IF NOT EXISTS idx_code_symbols_project ON code_symbols(project);
	CREATE INDEX IF NOT EXISTS idx_code_symbols_file ON code_symbols(project, file_path);
	CREATE INDEX IF NOT EXISTS idx_code_symbols_name ON code_symbols(project, name);

	CREATE TABLE IF NOT EXISTS code_relations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project TEXT NOT NULL,
		source_id TEXT NOT NULL,
		target_id TEXT NOT NULL,
		relation TEXT NOT NULL,
		confidence REAL DEFAULT 1.0,
		reasoning TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		UNIQUE (project, source_id, target_id, relation)
	);
	CREATE INDEX IF NOT EXISTS idx_code_relations_project ON code_relations(project);
	CREATE INDEX IF NOT EXISTS idx_code_relations_source ON code_relations(source_id);
	CREATE INDEX IF NOT EXISTS idx_code_relations_target ON code_relations(target_id);
	`
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

// SaveSymbols writes code symbols in an atomic transaction with UPSERT.
func (s *CodeStore) SaveSymbols(ctx context.Context, symbols []code.Symbol) error {
	if len(symbols) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO code_symbols (
			id, project, file_path, line_number, kind, name,
			package_name, signature, doc_summary, file_hash, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			line_number = excluded.line_number,
			kind = excluded.kind,
			name = excluded.name,
			package_name = excluded.package_name,
			signature = excluded.signature,
			doc_summary = excluded.doc_summary,
			file_hash = excluded.file_hash,
			updated_at = excluded.updated_at;
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, sym := range symbols {
		createdAt := now
		if !sym.CreatedAt.IsZero() {
			createdAt = sym.CreatedAt.UTC().Format(time.RFC3339)
		}
		updatedAt := now
		if !sym.UpdatedAt.IsZero() {
			updatedAt = sym.UpdatedAt.UTC().Format(time.RFC3339)
		}

		if _, err := stmt.ExecContext(ctx,
			sym.ID, sym.Project, sym.FilePath, sym.LineNumber, sym.Kind, sym.Name,
			sym.PackageName, sym.Signature, sym.DocSummary, sym.FileHash, createdAt, updatedAt,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetSymbolByID retrieves a single symbol by its ID.
func (s *CodeStore) GetSymbolByID(ctx context.Context, id string) (*code.Symbol, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, project, file_path, line_number, kind, name,
		       package_name, signature, doc_summary, file_hash, created_at, updated_at
		  FROM code_symbols
		 WHERE id = ?
	`, id)

	var sym code.Symbol
	var pkg, sig, doc, hash sql.NullString
	var created, updated string

	err := row.Scan(
		&sym.ID, &sym.Project, &sym.FilePath, &sym.LineNumber, &sym.Kind, &sym.Name,
		&pkg, &sig, &doc, &hash, &created, &updated,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if pkg.Valid {
		sym.PackageName = pkg.String
	}
	if sig.Valid {
		sym.Signature = sig.String
	}
	if doc.Valid {
		sym.DocSummary = doc.String
	}
	if hash.Valid {
		sym.FileHash = hash.String
	}
	sym.CreatedAt, _ = time.Parse(time.RFC3339, created)
	sym.UpdatedAt, _ = time.Parse(time.RFC3339, updated)

	return &sym, nil
}

// ListSymbols queries symbols matching filter parameters.
func (s *CodeStore) ListSymbols(ctx context.Context, filter code.SymbolFilter) ([]code.Symbol, error) {
	var sb strings.Builder
	sb.WriteString(`
		SELECT id, project, file_path, line_number, kind, name,
		       package_name, signature, doc_summary, file_hash, created_at, updated_at
		  FROM code_symbols
		 WHERE 1=1
	`)
	var args []any

	if filter.Project != "" {
		sb.WriteString(" AND project = ?")
		args = append(args, filter.Project)
	}
	if filter.FilePath != "" {
		sb.WriteString(" AND file_path = ?")
		args = append(args, filter.FilePath)
	}
	if filter.Kind != "" {
		sb.WriteString(" AND kind = ?")
		args = append(args, filter.Kind)
	}
	if filter.PackageName != "" {
		sb.WriteString(" AND package_name = ?")
		args = append(args, filter.PackageName)
	}
	if filter.Query != "" {
		sb.WriteString(" AND (name LIKE ? OR signature LIKE ?)")
		pattern := "%" + filter.Query + "%"
		args = append(args, pattern, pattern)
	}

	sb.WriteString(" ORDER BY file_path ASC, line_number ASC")

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	} else if limit > 100000 {
		limit = 100000
	}
	sb.WriteString(" LIMIT ?")
	args = append(args, limit)

	if filter.Offset > 0 {
		sb.WriteString(" OFFSET ?")
		args = append(args, filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var symbols []code.Symbol
	for rows.Next() {
		var sym code.Symbol
		var pkg, sig, doc, hash sql.NullString
		var created, updated string

		if err := rows.Scan(
			&sym.ID, &sym.Project, &sym.FilePath, &sym.LineNumber, &sym.Kind, &sym.Name,
			&pkg, &sig, &doc, &hash, &created, &updated,
		); err != nil {
			return nil, err
		}

		if pkg.Valid {
			sym.PackageName = pkg.String
		}
		if sig.Valid {
			sym.Signature = sig.String
		}
		if doc.Valid {
			sym.DocSummary = doc.String
		}
		if hash.Valid {
			sym.FileHash = hash.String
		}
		sym.CreatedAt, _ = time.Parse(time.RFC3339, created)
		sym.UpdatedAt, _ = time.Parse(time.RFC3339, updated)

		symbols = append(symbols, sym)
	}

	return symbols, rows.Err()
}

// CountSymbols returns the number of symbols matching the filter.
func (s *CodeStore) CountSymbols(ctx context.Context, filter code.SymbolFilter) (int, error) {
	var sb strings.Builder
	sb.WriteString("SELECT COUNT(*) FROM code_symbols WHERE 1=1")
	var args []any

	if filter.Project != "" {
		sb.WriteString(" AND project = ?")
		args = append(args, filter.Project)
	}
	if filter.FilePath != "" {
		sb.WriteString(" AND file_path = ?")
		args = append(args, filter.FilePath)
	}
	if filter.Kind != "" {
		sb.WriteString(" AND kind = ?")
		args = append(args, filter.Kind)
	}

	var count int
	err := s.db.QueryRowContext(ctx, sb.String(), args...).Scan(&count)
	return count, err
}

// DeleteSymbolsByProject removes all symbols for a given project.
func (s *CodeStore) DeleteSymbolsByProject(ctx context.Context, project string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM code_symbols WHERE project = ?", project)
	return err
}

// DeleteSymbolsByFile removes all symbols for a specific file in a project.
func (s *CodeStore) DeleteSymbolsByFile(ctx context.Context, project, filePath string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM code_symbols WHERE project = ? AND file_path = ?", project, filePath)
	return err
}

// SaveRelations writes code relations in an atomic transaction.
func (s *CodeStore) SaveRelations(ctx context.Context, relations []code.Relation) error {
	if len(relations) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO code_relations (
			project, source_id, target_id, relation, confidence, reasoning, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, rel := range relations {
		createdAt := now
		if !rel.CreatedAt.IsZero() {
			createdAt = rel.CreatedAt.UTC().Format(time.RFC3339)
		}
		if _, err := stmt.ExecContext(ctx,
			rel.Project, rel.SourceID, rel.TargetID, rel.Relation, rel.Confidence, rel.Reasoning, createdAt,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetGraph loads the complete symbols and relations graph for a project.
func (s *CodeStore) GetGraph(ctx context.Context, project string) (*code.CodeGraph, error) {
	symbols, err := s.ListSymbols(ctx, code.SymbolFilter{Project: project, Limit: 10000})
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project, source_id, target_id, relation, confidence, reasoning, created_at
		  FROM code_relations
		 WHERE project = ?
	`, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var relations []code.Relation
	for rows.Next() {
		var r code.Relation
		var reasoning sql.NullString
		var created string

		if err := rows.Scan(&r.ID, &r.Project, &r.SourceID, &r.TargetID, &r.Relation, &r.Confidence, &reasoning, &created); err != nil {
			return nil, err
		}
		if reasoning.Valid {
			r.Reasoning = reasoning.String
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339, created)
		relations = append(relations, r)
	}

	return &code.CodeGraph{
		Project:   project,
		Symbols:   symbols,
		Relations: relations,
	}, nil
}

// ListRelationsBySymbol finds all relations connected to a given symbol ID.
func (s *CodeStore) ListRelationsBySymbol(ctx context.Context, symbolID string) ([]code.Relation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project, source_id, target_id, relation, confidence, reasoning, created_at
		  FROM code_relations
		 WHERE source_id = ? OR target_id = ?
	`, symbolID, symbolID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var relations []code.Relation
	for rows.Next() {
		var r code.Relation
		var reasoning sql.NullString
		var created string

		if err := rows.Scan(&r.ID, &r.Project, &r.SourceID, &r.TargetID, &r.Relation, &r.Confidence, &reasoning, &created); err != nil {
			return nil, err
		}
		if reasoning.Valid {
			r.Reasoning = reasoning.String
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339, created)
		relations = append(relations, r)
	}
	return relations, nil
}

// DeleteRelationsByProject removes all relations for a given project.
func (s *CodeStore) DeleteRelationsByProject(ctx context.Context, project string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM code_relations WHERE project = ?", project)
	return err
}

// DeleteRelationsByFile removes relations connected to symbols in a specific file.
func (s *CodeStore) DeleteRelationsByFile(ctx context.Context, project, filePath string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM code_relations
		 WHERE project = ?
		   AND (source_id IN (SELECT id FROM code_symbols WHERE project = ? AND file_path = ?)
		     OR target_id IN (SELECT id FROM code_symbols WHERE project = ? AND file_path = ?))
	`, project, project, filePath, project, filePath)
	return err
}

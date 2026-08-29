// Package postgres contains the server-mode repositories.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

// ErrCodeIndexUnavailable reports that the scoped corpus has not completed a
// successful reindex. Legacy project-only rows are never used as fallback.
var ErrCodeIndexUnavailable = errors.New("code_index_unavailable")

type codeScope struct {
	tenantID    uuid.UUID
	workspaceID int64
	projectID   int64
	project     string
}

// PostgresCodeStore runs only inside an authorized, project-bound transaction.
// Schema ownership remains exclusively in forward migrations.
type PostgresCodeStore struct {
	tx    pgx.Tx
	scope codeScope
}

func normalizeCodeProject(project string) (uuid.UUID, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return uuid.Nil, errors.New("postgres code store: project scope is required")
	}
	id, err := uuid.Parse(project)
	if err != nil {
		return uuid.Nil, fmt.Errorf("postgres code store: project must be a public UUID: %w", err)
	}
	return id, nil
}

func newPostgresCodeStore(ctx context.Context, tx pgx.Tx, workspace, project string) (*PostgresCodeStore, error) {
	if tx == nil {
		return nil, errors.New("postgres code store: authorized transaction is required")
	}
	workspaceID, err := uuid.Parse(strings.TrimSpace(workspace))
	if err != nil {
		return nil, fmt.Errorf("postgres code store: workspace scope is required: %w", err)
	}
	projectID, err := normalizeCodeProject(project)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT public.cortex_bind_project_scope($1::uuid,$2::uuid)`, workspaceID, projectID); err != nil {
		return nil, fmt.Errorf("postgres code store: bind trusted project scope: %w", err)
	}
	scope := codeScope{project: projectID.String()}
	if err := tx.QueryRow(ctx, `SELECT public.cortex_current_tenant(),public.cortex_current_workspace(),public.cortex_current_project()`).
		Scan(&scope.tenantID, &scope.workspaceID, &scope.projectID); err != nil {
		return nil, fmt.Errorf("postgres code store: resolve bound scope: %w", err)
	}
	if scope.tenantID == uuid.Nil || scope.workspaceID <= 0 || scope.projectID <= 0 {
		return nil, errors.New("postgres code store: trusted project scope is incomplete")
	}
	return &PostgresCodeStore{tx: tx, scope: scope}, nil
}

func (s *PostgresCodeStore) requireReady(ctx context.Context) error {
	var state string
	err := s.tx.QueryRow(ctx, `SELECT state FROM scoped_code_index_state WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3 AND project=$4`,
		s.scope.tenantID, s.scope.workspaceID, s.scope.projectID, s.scope.project).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && state != "ready") {
		return ErrCodeIndexUnavailable
	}
	return err
}

func (s *PostgresCodeStore) validateProject(project string) error {
	id, err := normalizeCodeProject(project)
	if err != nil {
		return err
	}
	if id.String() != s.scope.project {
		return errors.New("postgres code store: project does not match trusted scope")
	}
	return nil
}

func (s *PostgresCodeStore) SaveSymbols(ctx context.Context, symbols []code.Symbol) error {
	query := `INSERT INTO scoped_code_symbols (
		tenant_id,workspace_id,project_id,id,project,file_path,line_number,end_line,start_col,end_col,
		kind,name,package_name,parent_id,visibility,signature,doc_summary,parameters,return_type,
		complexity,metadata,file_hash,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)
		ON CONFLICT (tenant_id,workspace_id,project_id,id) DO UPDATE SET
		project=EXCLUDED.project,file_path=EXCLUDED.file_path,line_number=EXCLUDED.line_number,
		end_line=EXCLUDED.end_line,start_col=EXCLUDED.start_col,end_col=EXCLUDED.end_col,
		kind=EXCLUDED.kind,name=EXCLUDED.name,package_name=EXCLUDED.package_name,
		parent_id=EXCLUDED.parent_id,visibility=EXCLUDED.visibility,signature=EXCLUDED.signature,
		doc_summary=EXCLUDED.doc_summary,parameters=EXCLUDED.parameters,return_type=EXCLUDED.return_type,
		complexity=EXCLUDED.complexity,metadata=EXCLUDED.metadata,file_hash=EXCLUDED.file_hash,
		updated_at=EXCLUDED.updated_at`
	now := time.Now().UTC()
	for _, sym := range symbols {
		if err := s.validateProject(sym.Project); err != nil {
			return err
		}
		createdAt, updatedAt := sym.CreatedAt, sym.UpdatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		if updatedAt.IsZero() {
			updatedAt = now
		}
		endLine := sym.EndLine
		if endLine <= 0 {
			endLine = sym.LineNumber
		}
		complexity := sym.Complexity
		if complexity <= 0 {
			complexity = 1
		}
		paramsJSON, err := json.Marshal(sym.Parameters)
		if err != nil {
			return fmt.Errorf("postgres code store: parameters: %w", err)
		}
		metaJSON, err := json.Marshal(sym.Metadata)
		if err != nil {
			return fmt.Errorf("postgres code store: metadata: %w", err)
		}
		if _, err := s.tx.Exec(ctx, query,
			s.scope.tenantID, s.scope.workspaceID, s.scope.projectID, sym.ID, s.scope.project, sym.FilePath,
			sym.LineNumber, endLine, sym.StartColumn, sym.EndColumn, sym.Kind, sym.Name, sym.PackageName, sym.ParentID,
			sym.Visibility, sym.Signature, sym.DocSummary, paramsJSON, sym.ReturnType, complexity, metaJSON,
			sym.FileHash, createdAt, updatedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresCodeStore) GetSymbolByID(ctx context.Context, id string) (*code.Symbol, error) {
	if err := s.requireReady(ctx); err != nil {
		return nil, err
	}
	row := s.tx.QueryRow(ctx, `SELECT id,project,file_path,line_number,end_line,COALESCE(start_col,0),COALESCE(end_col,0),kind,name,
		COALESCE(package_name,''),COALESCE(parent_id,''),COALESCE(visibility,''),COALESCE(signature,''),
		COALESCE(doc_summary,''),parameters,COALESCE(return_type,''),complexity,metadata,
		COALESCE(file_hash,''),created_at,updated_at FROM scoped_code_symbols
		WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3 AND id=$4`,
		s.scope.tenantID, s.scope.workspaceID, s.scope.projectID, id)
	var sym code.Symbol
	var paramsRaw, metaRaw []byte
	err := row.Scan(&sym.ID, &sym.Project, &sym.FilePath, &sym.LineNumber, &sym.EndLine, &sym.StartColumn, &sym.EndColumn,
		&sym.Kind, &sym.Name, &sym.PackageName, &sym.ParentID, &sym.Visibility, &sym.Signature, &sym.DocSummary,
		&paramsRaw, &sym.ReturnType, &sym.Complexity, &metaRaw, &sym.FileHash, &sym.CreatedAt, &sym.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	decodeSymbolJSON(&sym, paramsRaw, metaRaw)
	return &sym, nil
}

func (s *PostgresCodeStore) ListSymbols(ctx context.Context, filter code.SymbolFilter) ([]code.Symbol, error) {
	if filter.Project != "" {
		if err := s.validateProject(filter.Project); err != nil {
			return nil, err
		}
	}
	if err := s.requireReady(ctx); err != nil {
		return nil, err
	}
	return s.listSymbols(ctx, filter)
}

func (s *PostgresCodeStore) listSymbols(ctx context.Context, filter code.SymbolFilter) ([]code.Symbol, error) {
	var sb strings.Builder
	sb.WriteString(`SELECT id,project,file_path,line_number,end_line,COALESCE(start_col,0),COALESCE(end_col,0),kind,name,
		COALESCE(package_name,''),COALESCE(parent_id,''),COALESCE(visibility,''),COALESCE(signature,''),
		COALESCE(doc_summary,''),parameters,COALESCE(return_type,''),complexity,metadata,COALESCE(file_hash,''),created_at,updated_at
		FROM scoped_code_symbols WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3`)
	args := []any{s.scope.tenantID, s.scope.workspaceID, s.scope.projectID}
	add := func(clause string, value any) {
		args = append(args, value)
		fmt.Fprintf(&sb, clause, len(args))
	}
	if filter.FilePath != "" {
		add(" AND file_path=$%d", filter.FilePath)
	}
	if filter.Kind != "" {
		add(" AND kind=$%d", filter.Kind)
	}
	if filter.PackageName != "" {
		add(" AND package_name=$%d", filter.PackageName)
	}
	if filter.Query != "" {
		args = append(args, "%"+filter.Query+"%")
		fmt.Fprintf(&sb, " AND (name ILIKE $%d OR file_path ILIKE $%d OR signature ILIKE $%d OR doc_summary ILIKE $%d)", len(args), len(args), len(args), len(args))
	}
	sb.WriteString(" ORDER BY file_path,line_number")
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	} else if limit > 100000 {
		limit = 100000
	}
	add(" LIMIT $%d", limit)
	if filter.Offset > 0 {
		add(" OFFSET $%d", filter.Offset)
	}
	rows, err := s.tx.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	symbols := make([]code.Symbol, 0)
	for rows.Next() {
		var sym code.Symbol
		var paramsRaw, metaRaw []byte
		if err := rows.Scan(&sym.ID, &sym.Project, &sym.FilePath, &sym.LineNumber, &sym.EndLine, &sym.StartColumn, &sym.EndColumn,
			&sym.Kind, &sym.Name, &sym.PackageName, &sym.ParentID, &sym.Visibility, &sym.Signature, &sym.DocSummary,
			&paramsRaw, &sym.ReturnType, &sym.Complexity, &metaRaw, &sym.FileHash, &sym.CreatedAt, &sym.UpdatedAt); err != nil {
			return nil, err
		}
		decodeSymbolJSON(&sym, paramsRaw, metaRaw)
		symbols = append(symbols, sym)
	}
	return symbols, rows.Err()
}

func decodeSymbolJSON(sym *code.Symbol, paramsRaw, metaRaw []byte) {
	if len(paramsRaw) > 0 {
		_ = json.Unmarshal(paramsRaw, &sym.Parameters)
	}
	if len(metaRaw) > 0 {
		_ = json.Unmarshal(metaRaw, &sym.Metadata)
	}
}

func (s *PostgresCodeStore) CountSymbols(ctx context.Context, filter code.SymbolFilter) (int, error) {
	if filter.Project != "" {
		if err := s.validateProject(filter.Project); err != nil {
			return 0, err
		}
	}
	if err := s.requireReady(ctx); err != nil {
		return 0, err
	}
	var count int
	err := s.tx.QueryRow(ctx, `SELECT count(*) FROM scoped_code_symbols WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3`,
		s.scope.tenantID, s.scope.workspaceID, s.scope.projectID).Scan(&count)
	return count, err
}

func (s *PostgresCodeStore) DeleteSymbolsByProject(ctx context.Context, project string) error {
	if err := s.validateProject(project); err != nil {
		return err
	}
	_, err := s.tx.Exec(ctx, `DELETE FROM scoped_code_symbols WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3`, s.scope.tenantID, s.scope.workspaceID, s.scope.projectID)
	return err
}

func (s *PostgresCodeStore) DeleteSymbolsByFile(ctx context.Context, project, filePath string) error {
	if err := s.validateProject(project); err != nil {
		return err
	}
	_, err := s.tx.Exec(ctx, `DELETE FROM scoped_code_symbols WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3 AND file_path=$4`, s.scope.tenantID, s.scope.workspaceID, s.scope.projectID, filePath)
	return err
}

func (s *PostgresCodeStore) SaveRelations(ctx context.Context, relations []code.Relation) error {
	query := `INSERT INTO scoped_code_relations (tenant_id,workspace_id,project_id,project,source_id,target_id,relation,confidence,reasoning,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (tenant_id,workspace_id,project_id,source_id,target_id,relation) DO UPDATE SET confidence=EXCLUDED.confidence,reasoning=EXCLUDED.reasoning`
	now := time.Now().UTC()
	for _, rel := range relations {
		if err := s.validateProject(rel.Project); err != nil {
			return err
		}
		confidence := rel.Confidence
		if confidence <= 0 {
			confidence = 1
		}
		createdAt := rel.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		if _, err := s.tx.Exec(ctx, query, s.scope.tenantID, s.scope.workspaceID, s.scope.projectID, s.scope.project,
			rel.SourceID, rel.TargetID, rel.Relation, confidence, rel.Reasoning, createdAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresCodeStore) GetGraph(ctx context.Context, project string) (*code.CodeGraph, error) {
	if err := s.validateProject(project); err != nil {
		return nil, err
	}
	if err := s.requireReady(ctx); err != nil {
		return nil, err
	}
	symbols, err := s.listSymbols(ctx, code.SymbolFilter{Project: project, Limit: 100000})
	if err != nil {
		return nil, err
	}
	relations, err := s.listRelations(ctx, "")
	if err != nil {
		return nil, err
	}
	return &code.CodeGraph{Project: s.scope.project, Symbols: symbols, Relations: relations}, nil
}

func (s *PostgresCodeStore) ListRelationsBySymbol(ctx context.Context, symbolID string) ([]code.Relation, error) {
	if err := s.requireReady(ctx); err != nil {
		return nil, err
	}
	return s.listRelations(ctx, symbolID)
}

func (s *PostgresCodeStore) listRelations(ctx context.Context, symbolID string) ([]code.Relation, error) {
	query := `SELECT id,project,source_id,target_id,relation,confidence,COALESCE(reasoning,''),created_at
		FROM scoped_code_relations WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3`
	args := []any{s.scope.tenantID, s.scope.workspaceID, s.scope.projectID}
	if symbolID != "" {
		query += " AND (source_id=$4 OR target_id=$4)"
		args = append(args, symbolID)
	}
	rows, err := s.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	relations := make([]code.Relation, 0)
	for rows.Next() {
		var rel code.Relation
		if err := rows.Scan(&rel.ID, &rel.Project, &rel.SourceID, &rel.TargetID, &rel.Relation, &rel.Confidence, &rel.Reasoning, &rel.CreatedAt); err != nil {
			return nil, err
		}
		relations = append(relations, rel)
	}
	return relations, rows.Err()
}

func (s *PostgresCodeStore) DeleteRelationsByProject(ctx context.Context, project string) error {
	if err := s.validateProject(project); err != nil {
		return err
	}
	_, err := s.tx.Exec(ctx, `DELETE FROM scoped_code_relations WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3`, s.scope.tenantID, s.scope.workspaceID, s.scope.projectID)
	return err
}

func (s *PostgresCodeStore) DeleteRelationsByFile(ctx context.Context, project, filePath string) error {
	if err := s.validateProject(project); err != nil {
		return err
	}
	_, err := s.tx.Exec(ctx, `DELETE FROM scoped_code_relations WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3 AND
		(source_id IN (SELECT id FROM scoped_code_symbols WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3 AND file_path=$4)
		 OR target_id IN (SELECT id FROM scoped_code_symbols WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3 AND file_path=$4))`,
		s.scope.tenantID, s.scope.workspaceID, s.scope.projectID, filePath)
	return err
}

func codeGraphChecksum(graph *code.CodeGraph) (string, error) {
	payload, err := json.Marshal(graph)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func (s *PostgresCodeStore) replaceGraph(ctx context.Context, graph *code.CodeGraph) error {
	if graph == nil {
		return errors.New("postgres code store: graph is required")
	}
	if err := s.validateProject(graph.Project); err != nil {
		return err
	}
	checksum, err := codeGraphChecksum(graph)
	if err != nil {
		return err
	}
	var existing string
	err = s.tx.QueryRow(ctx, `SELECT COALESCE(index_checksum,'') FROM scoped_code_index_state WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3 AND state='ready'`,
		s.scope.tenantID, s.scope.workspaceID, s.scope.projectID).Scan(&existing)
	if err == nil && existing == checksum {
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = s.tx.Exec(ctx, `INSERT INTO scoped_code_index_state (tenant_id,workspace_id,project_id,project,state,symbol_count,relation_count,index_checksum,last_error_code,indexed_at,updated_at)
		VALUES ($1,$2,$3,$4,'indexing',0,0,NULL,NULL,NULL,now())
		ON CONFLICT (tenant_id,workspace_id,project_id) DO UPDATE SET state='indexing',symbol_count=0,relation_count=0,index_checksum=NULL,last_error_code=NULL,indexed_at=NULL,updated_at=now()`,
		s.scope.tenantID, s.scope.workspaceID, s.scope.projectID, s.scope.project)
	if err != nil {
		return err
	}
	if _, err := s.tx.Exec(ctx, `DELETE FROM scoped_code_relations WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3`, s.scope.tenantID, s.scope.workspaceID, s.scope.projectID); err != nil {
		return err
	}
	if _, err := s.tx.Exec(ctx, `DELETE FROM scoped_code_symbols WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3`, s.scope.tenantID, s.scope.workspaceID, s.scope.projectID); err != nil {
		return err
	}
	if err := s.SaveSymbols(ctx, graph.Symbols); err != nil {
		return err
	}
	if err := s.SaveRelations(ctx, graph.Relations); err != nil {
		return err
	}
	_, err = s.tx.Exec(ctx, `UPDATE scoped_code_index_state SET state='ready',symbol_count=$4,relation_count=$5,index_checksum=$6,last_error_code=NULL,indexed_at=now(),updated_at=now()
		WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3`, s.scope.tenantID, s.scope.workspaceID, s.scope.projectID, len(graph.Symbols), len(graph.Relations), checksum)
	return err
}

var _ code.Store = (*PostgresCodeStore)(nil)

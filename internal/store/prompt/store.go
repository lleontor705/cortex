// Package prompt implements the SQLite prompt store for Cortex.
//
// It provides CRUD operations and FTS5 full-text search for user prompts.
// The store implements the domain.PromptRepository interface with additional
// methods for session-scoped queries and deletion.
package prompt

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// Store implements the SQLite prompt store.
// It provides CRUD operations and FTS5 full-text search for user prompts.
type Store struct {
	db *sql.DB
}

// NewStore creates a new prompt store with the given database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Save inserts a new prompt into the database.
// It sets the CreatedAt timestamp if not already set.
// Returns the prompt with the ID populated.
func (s *Store) Save(ctx context.Context, prompt *domain.Prompt) error {
	if prompt == nil {
		return &domain.ValidationError{
			Field:   "prompt",
			Message: "prompt cannot be nil",
		}
	}

	if prompt.Content == "" {
		return &domain.ValidationError{
			Field:   "content",
			Message: "content cannot be empty",
		}
	}

	// Set timestamp if not provided
	if prompt.CreatedAt.IsZero() {
		prompt.CreatedAt = time.Now()
	}

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO user_prompts (content, project, session_id, created_at)
		VALUES (?, ?, ?, ?)
	`, prompt.Content, prompt.Project, prompt.SessionID, prompt.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("prompt store: insert prompt: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("prompt store: get last insert id: %w", err)
	}

	prompt.ID = id
	return nil
}

// GetByID retrieves a prompt by its ID.
// Returns ErrNotFound if the prompt does not exist.
func (s *Store) GetByID(ctx context.Context, id int64) (*domain.Prompt, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, content, project, session_id, created_at
		FROM user_prompts
		WHERE id = ?
	`, id)

	var prompt domain.Prompt
	var createdAtStr string

	err := row.Scan(&prompt.ID, &prompt.Content, &prompt.Project, &prompt.SessionID, &createdAtStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, &domain.NotFoundError{Type: "prompt", ID: id}
		}
		return nil, fmt.Errorf("prompt store: get by id: %w", err)
	}

	prompt.CreatedAt, err = time.Parse(time.RFC3339, createdAtStr)
	if err != nil {
		prompt.CreatedAt = time.Time{} // Use zero time if parsing fails
	}

	return &prompt, nil
}

// List retrieves prompts for a project, ordered by most recent first.
// If limit is <= 0, a default limit of 20 is used.
func (s *Store) List(ctx context.Context, project string, limit int) ([]*domain.Prompt, error) {
	if limit <= 0 {
		limit = 20
	}

	query := `
		SELECT id, content, project, session_id, created_at
		FROM user_prompts
		WHERE project = ?
		ORDER BY created_at DESC
		LIMIT ?
	`

	rows, err := s.db.QueryContext(ctx, query, project, limit)
	if err != nil {
		return nil, fmt.Errorf("prompt store: list prompts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanPrompts(rows)
}

// ListBySession retrieves prompts for a specific session, ordered by most recent first.
func (s *Store) ListBySession(ctx context.Context, sessionID string) ([]*domain.Prompt, error) {
	if sessionID == "" {
		return nil, &domain.ValidationError{
			Field:   "session_id",
			Message: "session_id cannot be empty",
		}
	}

	query := `
		SELECT id, content, project, session_id, created_at
		FROM user_prompts
		WHERE session_id = ?
		ORDER BY created_at DESC
	`

	rows, err := s.db.QueryContext(ctx, query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("prompt store: list by session: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanPrompts(rows)
}

// Delete removes a prompt from the database.
// Returns ErrNotFound if the prompt does not exist.
func (s *Store) Delete(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM user_prompts WHERE id = ?
	`, id)
	if err != nil {
		return fmt.Errorf("prompt store: delete prompt: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("prompt store: get rows affected: %w", err)
	}

	if affected == 0 {
		return &domain.NotFoundError{Type: "prompt", ID: id}
	}

	return nil
}

// Search performs FTS5 full-text search on prompts.
// If project is non-empty, results are filtered by project.
// If limit is <= 0, a default limit of 10 is used.
func (s *Store) Search(ctx context.Context, query string, project string, limit int) ([]*domain.Prompt, error) {
	if limit <= 0 {
		limit = 10
	}

	// Sanitize query for FTS5 - wrap terms in quotes to avoid syntax errors
	ftsQuery := sanitizeFTS(query)

	sqlQuery := `
		SELECT p.id, p.content, p.project, p.session_id, p.created_at
		FROM prompts_fts fts
		JOIN user_prompts p ON p.id = fts.rowid
		WHERE prompts_fts MATCH ?
	`
	args := []any{ftsQuery}

	if project != "" {
		sqlQuery += " AND p.project = ?"
		args = append(args, project)
	}

	sqlQuery += " ORDER BY fts.rank LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("prompt store: search prompts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanPrompts(rows)
}

// scanPrompts is a helper function to scan multiple prompt rows.
func (s *Store) scanPrompts(rows *sql.Rows) ([]*domain.Prompt, error) {
	var prompts []*domain.Prompt

	for rows.Next() {
		var prompt domain.Prompt
		var createdAtStr string

		err := rows.Scan(&prompt.ID, &prompt.Content, &prompt.Project, &prompt.SessionID, &createdAtStr)
		if err != nil {
			return nil, fmt.Errorf("prompt store: scan prompt: %w", err)
		}

		prompt.CreatedAt, err = time.Parse(time.RFC3339, createdAtStr)
		if err != nil {
			prompt.CreatedAt = time.Time{} // Use zero time if parsing fails
		}

		prompts = append(prompts, &prompt)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("prompt store: iterate prompts: %w", err)
	}

	return prompts, nil
}

// sanitizeFTS sanitizes a query string for FTS5 full-text search.
// It wraps each term in double quotes to prevent FTS5 syntax errors
// from special characters.
func sanitizeFTS(query string) string {
	// Split on whitespace
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return ""
	}

	// Wrap each term in double quotes
	var quoted []string
	for _, term := range terms {
		// Remove any existing quotes
		term = strings.Trim(term, `"`)
		if term != "" {
			quoted = append(quoted, fmt.Sprintf(`"%s"`, term))
		}
	}

	return strings.Join(quoted, " ")
}

// Ensure Store implements domain.PromptRepository
var _ domain.PromptRepository = (*Store)(nil)

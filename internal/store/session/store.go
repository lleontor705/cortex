// Package session implements the SQLite session store for Cortex.
//
// It provides lifecycle management for coding sessions, including creation,
// retrieval, ending, and querying sessions and their associated observations.
// The store implements the domain.SessionRepository interface with additional
// methods for session stats and observations by session queries.
package session

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// Store implements the SQLite session store.
// It provides CRUD operations for sessions and related queries.
type Store struct {
	db *sql.DB
}

// NewStore creates a new session store with the given database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create inserts a new session into the database.
// It sets the StartedAt timestamp if not already set.
// Returns an error if a session with the same ID already exists.
func (s *Store) Create(ctx context.Context, session *domain.Session) error {
	if session == nil {
		return &domain.ValidationError{
			Field:   "session",
			Message: "session cannot be nil",
		}
	}

	if session.ID == "" {
		return &domain.ValidationError{
			Field:   "id",
			Message: "session ID is required",
		}
	}

	if session.Project == "" {
		return &domain.ValidationError{
			Field:   "project",
			Message: "project name is required",
		}
	}

	if session.Directory == "" {
		return &domain.ValidationError{
			Field:   "directory",
			Message: "directory path is required",
		}
	}

	// Set timestamp if not provided
	if session.StartedAt.IsZero() {
		session.StartedAt = time.Now()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, project, directory, started_at, ended_at, summary)
		VALUES (?, ?, ?, ?, NULL, ?)
	`, session.ID, session.Project, session.Directory, session.StartedAt.Format(time.RFC3339), session.Summary)
	if err != nil {
		return fmt.Errorf("session store: create session: %w", err)
	}

	return nil
}

// GetByID retrieves a session by its ID.
// Returns ErrNotFound if the session does not exist.
func (s *Store) GetByID(ctx context.Context, id string) (*domain.Session, error) {
	if id == "" {
		return nil, &domain.ValidationError{
			Field:   "id",
			Message: "session ID is required",
		}
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT id, project, directory, started_at, ended_at, summary
		FROM sessions
		WHERE id = ?
	`, id)

	var session domain.Session
	var startedAtStr string
	var endedAtStr sql.NullString
	var summary sql.NullString

	err := row.Scan(&session.ID, &session.Project, &session.Directory, &startedAtStr, &endedAtStr, &summary)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, &domain.NotFoundError{Type: "session", ID: id}
		}
		return nil, fmt.Errorf("session store: get by id: %w", err)
	}

	session.StartedAt, err = time.Parse(time.RFC3339, startedAtStr)
	if err != nil {
		session.StartedAt = time.Time{}
	}

	if endedAtStr.Valid {
		endedAt, err := time.Parse(time.RFC3339, endedAtStr.String)
		if err == nil {
			session.EndedAt = &endedAt
		}
	}

	if summary.Valid {
		session.Summary = summary.String
	}

	return &session, nil
}

// End marks a session as completed with an optional summary.
// It sets the ended_at timestamp to the current time and updates the summary.
// Returns ErrNotFound if the session does not exist.
// Returns ErrSessionEnded if the session has already ended.
func (s *Store) End(ctx context.Context, id string, summary string) error {
	if id == "" {
		return &domain.ValidationError{
			Field:   "id",
			Message: "session ID is required",
		}
	}

	// First check if session exists and is not already ended
	session, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if session.EndedAt != nil {
		return domain.ErrSessionEnded
	}

	// Update the session
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET ended_at = ?, summary = ?
		WHERE id = ? AND ended_at IS NULL
	`, now.Format(time.RFC3339), summary, id)
	if err != nil {
		return fmt.Errorf("session store: end session: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("session store: get rows affected: %w", err)
	}

	if affected == 0 {
		// This can happen if another process ended the session between our check and update
		return domain.ErrSessionEnded
	}

	return nil
}

// List retrieves sessions for a project, ordered by most recent first.
// If project is empty, returns all sessions.
// Sessions are ordered by started_at descending.
func (s *Store) List(ctx context.Context, project string) ([]*domain.Session, error) {
	query := `
		SELECT id, project, directory, started_at, ended_at, summary
		FROM sessions
	`
	args := []any{}

	if project != "" {
		query += " WHERE project = ?"
		args = append(args, project)
	}

	query += " ORDER BY started_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("session store: list sessions: %w", err)
	}
	defer rows.Close()

	return s.scanSessions(rows)
}

// Recent retrieves the most recent sessions with optional project filter.
// Sessions are ordered by started_at descending with a limit.
// If limit is <= 0, a default limit of 5 is used.
func (s *Store) Recent(ctx context.Context, project string, limit int) ([]*domain.Session, error) {
	if limit <= 0 {
		limit = 5
	}

	query := `
		SELECT id, project, directory, started_at, ended_at, summary
		FROM sessions
	`
	args := []any{}

	if project != "" {
		query += " WHERE project = ?"
		args = append(args, project)
	}

	query += " ORDER BY started_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("session store: recent sessions: %w", err)
	}
	defer rows.Close()

	return s.scanSessions(rows)
}

// SessionStats represents statistics about a session.
type SessionStats struct {
	Session          *domain.Session
	ObservationCount int
}

// GetWithStats retrieves a session with its observation count.
// Returns ErrNotFound if the session does not exist.
func (s *Store) GetWithStats(ctx context.Context, id string) (*SessionStats, error) {
	session, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	var count int
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM observations WHERE session_id = ? AND deleted_at IS NULL
	`, id).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("session store: count observations: %w", err)
	}

	return &SessionStats{
		Session:          session,
		ObservationCount: count,
	}, nil
}

// RecentWithStats retrieves recent sessions with their observation counts.
// Sessions are ordered by most recent activity first.
// If limit is <= 0, a default limit of 5 is used.
func (s *Store) RecentWithStats(ctx context.Context, project string, limit int) ([]*SessionStats, error) {
	if limit <= 0 {
		limit = 5
	}

	query := `
		SELECT s.id, s.project, s.directory, s.started_at, s.ended_at, s.summary,
		       COUNT(o.id) as observation_count
		FROM sessions s
		LEFT JOIN observations o ON o.session_id = s.id AND o.deleted_at IS NULL
	`
	args := []any{}

	if project != "" {
		query += " WHERE s.project = ?"
		args = append(args, project)
	}

	query += " GROUP BY s.id ORDER BY MAX(COALESCE(o.created_at, s.started_at)) DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("session store: recent sessions with stats: %w", err)
	}
	defer rows.Close()

	var results []*SessionStats
	for rows.Next() {
		var stats SessionStats
		var session domain.Session
		var startedAtStr string
		var endedAtStr sql.NullString
		var summary sql.NullString

		err := rows.Scan(
			&session.ID, &session.Project, &session.Directory,
			&startedAtStr, &endedAtStr, &summary,
			&stats.ObservationCount,
		)
		if err != nil {
			return nil, fmt.Errorf("session store: scan session stats: %w", err)
		}

		session.StartedAt, err = time.Parse(time.RFC3339, startedAtStr)
		if err != nil {
			session.StartedAt = time.Time{}
		}

		if endedAtStr.Valid {
			endedAt, err := time.Parse(time.RFC3339, endedAtStr.String)
			if err == nil {
				session.EndedAt = &endedAt
			}
		}

		if summary.Valid {
			session.Summary = summary.String
		}

		stats.Session = &session
		results = append(results, &stats)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session store: iterate session stats: %w", err)
	}

	return results, nil
}

// GetCurrent retrieves the most recent active session for a project.
// An active session is one where ended_at is NULL.
// Returns ErrNotFound if no active session exists for the project.
func (s *Store) GetCurrent(ctx context.Context, project string) (*domain.Session, error) {
	if project == "" {
		return nil, &domain.ValidationError{
			Field:   "project",
			Message: "project name is required",
		}
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT id, project, directory, started_at, ended_at, summary
		FROM sessions
		WHERE project = ? AND ended_at IS NULL
		ORDER BY started_at DESC
		LIMIT 1
	`, project)

	var session domain.Session
	var startedAtStr string
	var summary sql.NullString

	err := row.Scan(&session.ID, &session.Project, &session.Directory, &startedAtStr, &session.EndedAt, &summary)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, &domain.NotFoundError{Type: "active session", ID: project}
		}
		return nil, fmt.Errorf("session store: get current session: %w", err)
	}

	session.StartedAt, err = time.Parse(time.RFC3339, startedAtStr)
	if err != nil {
		session.StartedAt = time.Time{}
	}

	if summary.Valid {
		session.Summary = summary.String
	}

	return &session, nil
}

// Stats represents overall session statistics.
type Stats struct {
	TotalSessions     int
	ActiveSessions    int
	EndedSessions     int
	TotalObservations int
	Projects          []string
}

// GetStats retrieves overall session statistics.
func (s *Store) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{}

	// Session counts in a single query
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN ended_at IS NULL THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN ended_at IS NOT NULL THEN 1 ELSE 0 END), 0)
		FROM sessions
	`).Scan(&stats.TotalSessions, &stats.ActiveSessions, &stats.EndedSessions)
	if err != nil {
		return nil, fmt.Errorf("session store: count sessions: %w", err)
	}

	// Count total observations
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL").Scan(&stats.TotalObservations)
	if err != nil {
		return nil, fmt.Errorf("session store: count observations: %w", err)
	}

	// Get distinct projects
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT project FROM sessions ORDER BY project
	`)
	if err != nil {
		return nil, fmt.Errorf("session store: list projects: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var project string
		if err := rows.Scan(&project); err != nil {
			return nil, fmt.Errorf("session store: scan project: %w", err)
		}
		stats.Projects = append(stats.Projects, project)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session store: iterate projects: %w", err)
	}

	return stats, nil
}

// scanSessions is a helper function to scan multiple session rows.
func (s *Store) scanSessions(rows *sql.Rows) ([]*domain.Session, error) {
	var sessions []*domain.Session

	for rows.Next() {
		var session domain.Session
		var startedAtStr string
		var endedAtStr sql.NullString
		var summary sql.NullString

		err := rows.Scan(
			&session.ID, &session.Project, &session.Directory,
			&startedAtStr, &endedAtStr, &summary,
		)
		if err != nil {
			return nil, fmt.Errorf("session store: scan session: %w", err)
		}

		session.StartedAt, err = time.Parse(time.RFC3339, startedAtStr)
		if err != nil {
			session.StartedAt = time.Time{}
		}

		if endedAtStr.Valid {
			endedAt, err := time.Parse(time.RFC3339, endedAtStr.String)
			if err == nil {
				session.EndedAt = &endedAt
			}
		}

		if summary.Valid {
			session.Summary = summary.String
		}

		sessions = append(sessions, &session)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session store: iterate sessions: %w", err)
	}

	return sessions, nil
}

// Ensure Store implements domain.SessionRepository
var _ domain.SessionRepository = (*Store)(nil)

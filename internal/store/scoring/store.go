// Package scoring implements the SQLite scoring store for Cortex.
//
// It provides importance scoring persistence operations including score
// retrieval, updates, access tracking, and batch operations. The store
// implements the scoring.Repository interface.
package scoring

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	scoringdomain "github.com/lleontor705/cortex/internal/domain/scoring"
)

// sqliteDatetimeFormat is the format used by SQLite's datetime() function.
const sqliteDatetimeFormat = "2006-01-02 15:04:05"

// Store implements the SQLite scoring store.
type Store struct {
	db *sql.DB
}

// NewStore creates a new scoring store with the given database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// GetScore retrieves the importance score for an observation.
func (s *Store) GetScore(ctx context.Context, obsID int64) (*domain.ImportanceScore, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT observation_id, score, access_count, last_accessed, updated_at
		 FROM importance_scores WHERE observation_id = ?`, obsID,
	)

	score, err := scanImportanceScore(row)
	if err != nil {
		if domain.IsNotFoundError(err) {
			return nil, &domain.NotFoundError{Type: "importance_score", ID: obsID}
		}
		return nil, err
	}
	return score, nil
}

// UpdateScore adjusts the importance score by the given increment, clamped to [0.0, 5.0].
func (s *Store) UpdateScore(ctx context.Context, obsID int64, increment float64) error {
	// Clamp using application logic since SQLite MIN/MAX with float needs care
	score, err := s.GetScore(ctx, obsID)
	if err != nil {
		return fmt.Errorf("scoring: get score for update: %w", err)
	}

	newScore := math.Max(0.0, math.Min(5.0, score.Score+increment))
	_, err = s.db.ExecContext(ctx,
		`UPDATE importance_scores SET score = ?, updated_at = datetime('now')
		 WHERE observation_id = ?`, newScore, obsID,
	)
	if err != nil {
		return fmt.Errorf("scoring: update score: %w", err)
	}

	return nil
}

// GetTop retrieves the most important observations for a project.
func (s *Store) GetTop(ctx context.Context, project string, limit int) ([]*domain.ImportanceScore, error) {
	return s.GetTopByScore(ctx, project, limit)
}

// RecordAccess increments the access count and updates last_accessed timestamp.
func (s *Store) RecordAccess(ctx context.Context, obsID int64) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE importance_scores
		 SET access_count = access_count + 1,
		     last_accessed = datetime('now'),
		     updated_at = datetime('now')
		 WHERE observation_id = ?`, obsID,
	)
	if err != nil {
		return fmt.Errorf("scoring: record access: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("scoring: rows affected: %w", err)
	}

	if rows == 0 {
		return &domain.NotFoundError{Type: "importance_score", ID: obsID}
	}

	return nil
}

// SetScore updates the score value for an observation.
func (s *Store) SetScore(ctx context.Context, obsID int64, score float64) error {
	score = math.Max(0.0, math.Min(5.0, score))

	result, err := s.db.ExecContext(ctx,
		`UPDATE importance_scores SET score = ?, updated_at = datetime('now')
		 WHERE observation_id = ?`, score, obsID,
	)
	if err != nil {
		return fmt.Errorf("scoring: set score: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("scoring: rows affected: %w", err)
	}

	if rows == 0 {
		return &domain.NotFoundError{Type: "importance_score", ID: obsID}
	}

	return nil
}

// GetAllScores retrieves all scores for batch operations like decay.
func (s *Store) GetAllScores(ctx context.Context) ([]*domain.ImportanceScore, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT observation_id, score, access_count, last_accessed, updated_at
		 FROM importance_scores ORDER BY score DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("scoring: get all scores: %w", err)
	}
	defer rows.Close()

	return scanImportanceScores(rows)
}

// GetTopByScore retrieves the top-N highest scored observations for a project.
func (s *Store) GetTopByScore(ctx context.Context, project string, limit int) ([]*domain.ImportanceScore, error) {
	if limit <= 0 {
		limit = 10
	}

	var rows *sql.Rows
	var err error

	if project != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT s.observation_id, s.score, s.access_count, s.last_accessed, s.updated_at
			 FROM importance_scores s
			 JOIN observations o ON o.id = s.observation_id
			 WHERE o.project = ? AND o.deleted_at IS NULL
			 ORDER BY s.score DESC
			 LIMIT ?`, project, limit,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT s.observation_id, s.score, s.access_count, s.last_accessed, s.updated_at
			 FROM importance_scores s
			 JOIN observations o ON o.id = s.observation_id
			 WHERE o.deleted_at IS NULL
			 ORDER BY s.score DESC
			 LIMIT ?`, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("scoring: get top by score: %w", err)
	}
	defer rows.Close()

	return scanImportanceScores(rows)
}

// GetIncomingEdgeCount returns the number of edges pointing to this observation.
func (s *Store) GetIncomingEdgeCount(ctx context.Context, obsID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM edges WHERE to_obs_id = ?`, obsID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("scoring: get incoming edge count: %w", err)
	}

	return count, nil
}

// GetObservation retrieves observation data needed for score calculation.
func (s *Store) GetObservation(ctx context.Context, obsID int64) (*domain.Observation, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, title, content, type, project, scope, session_id,
		        COALESCE(topic_key, '') AS topic_key, created_at, updated_at
		 FROM observations WHERE id = ? AND deleted_at IS NULL`, obsID,
	)

	obs := &domain.Observation{}
	var createdAt, updatedAt string
	err := row.Scan(
		&obs.ID, &obs.Title, &obs.Content, &obs.Type, &obs.Project,
		&obs.Scope, &obs.SessionID, &obs.TopicKey, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, &domain.NotFoundError{Type: "observation", ID: obsID}
	}
	if err != nil {
		return nil, fmt.Errorf("scoring: get observation: %w", err)
	}

	obs.CreatedAt, _ = time.Parse(sqliteDatetimeFormat, createdAt)
	obs.UpdatedAt, _ = time.Parse(sqliteDatetimeFormat, updatedAt)

	return obs, nil
}

// scanImportanceScore scans a single importance score row.
func scanImportanceScore(row *sql.Row) (*domain.ImportanceScore, error) {
	score := &domain.ImportanceScore{}
	var lastAccessed sql.NullString
	var updatedAt string

	err := row.Scan(
		&score.ObservationID, &score.Score, &score.AccessCount,
		&lastAccessed, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, &domain.NotFoundError{Type: "importance_score", ID: 0}
	}
	if err != nil {
		return nil, fmt.Errorf("scoring: scan score: %w", err)
	}

	if lastAccessed.Valid {
		score.LastAccessed, _ = time.Parse(sqliteDatetimeFormat, lastAccessed.String)
	}
	score.UpdatedAt, _ = time.Parse(sqliteDatetimeFormat, updatedAt)

	return score, nil
}

// scanImportanceScores scans multiple importance score rows.
func scanImportanceScores(rows *sql.Rows) ([]*domain.ImportanceScore, error) {
	var scores []*domain.ImportanceScore
	for rows.Next() {
		score := &domain.ImportanceScore{}
		var lastAccessed sql.NullString
		var updatedAt string

		if err := rows.Scan(
			&score.ObservationID, &score.Score, &score.AccessCount,
			&lastAccessed, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scoring: scan scores: %w", err)
		}

		if lastAccessed.Valid {
			score.LastAccessed, _ = time.Parse(sqliteDatetimeFormat, lastAccessed.String)
		}
		score.UpdatedAt, _ = time.Parse(sqliteDatetimeFormat, updatedAt)

		scores = append(scores, score)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scoring: rows iteration: %w", err)
	}

	return scores, nil
}

// Ensure Store implements scoring.Repository.
var _ scoringdomain.Repository = (*Store)(nil)

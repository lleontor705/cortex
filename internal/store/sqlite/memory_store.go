// Package sqlite implements the SQLite memory store for Cortex.
//
// It provides CRUD operations for observations with deduplication by normalized_hash,
// topic key upsert, and soft/hard delete support. The store implements the
// domain.ObservationRepository interface.
package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// sqliteDatetimeFormat is the format used by SQLite's datetime() function.
const sqliteDatetimeFormat = "2006-01-02 15:04:05"

const (
	maxObservationTitleLength   = 200
	maxObservationContentLength = 64 * 1024
	maxObservationSourceLength  = 64
)

// Store implements the SQLite observation store.
// It provides CRUD operations with deduplication, topic key upsert,
// and soft/hard delete support.
type Store struct {
	db *sql.DB
}

type observationRevisionSnapshot struct {
	Reason   string                   `json:"reason"`
	Captured time.Time                `json:"captured_at"`
	Previous observationRevisionState `json:"previous"`
}

type observationRevisionState struct {
	ID             int64      `json:"id"`
	SessionID      string     `json:"session_id"`
	Type           string     `json:"type"`
	Title          string     `json:"title"`
	Content        string     `json:"content"`
	Project        string     `json:"project,omitempty"`
	Scope          string     `json:"scope"`
	TopicKey       string     `json:"topic_key,omitempty"`
	Confidence     float64    `json:"confidence"`
	Source         string     `json:"source,omitempty"`
	Tags           []string   `json:"tags,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	RevisionCount  int        `json:"revision_count"`
	DuplicateCount int        `json:"duplicate_count"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
}

// NewStore creates a new observation store with the given database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Save creates a new observation or updates an existing one if topic_key matches.
// It implements deduplication by normalized_hash within a configurable window.
//
// Business Rules:
//   - If topic_key is provided and an observation with the same topic_key exists
//     in the same project, update it instead of creating a new one.
//   - If normalized_hash matches an existing observation within the deduplication
//     window, increment duplicate_count instead of creating a new one.
//   - Sets created_at and updated_at timestamps.
//   - Normalizes scope to "project" or "personal".
//
// This method opens and commits its OWN transaction (local-mode path). For
// cross-store atomic saves, use SaveInTx within a UnitOfWork (W2.1, REQ-TX-001).
func (s *Store) Save(ctx context.Context, obs *domain.Observation) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		return s.saveInTx(ctx, tx, obs)
	})
}

// saveInTx contains the core save logic (validation, normalization, insert/
// update/dedup) running within an externally-provided transaction. It is shared
// by Save (which wraps it in its own tx) and SaveInTx (which uses a shared tx).
// It MUST NOT call BeginTx/Commit/Rollback — the caller owns the tx lifecycle.
func (s *Store) saveInTx(ctx context.Context, tx *sql.Tx, obs *domain.Observation) error {
	if err := validateObservation(obs); err != nil {
		return err
	}

	// Set defaults
	now := time.Now().UTC()
	if obs.CreatedAt.IsZero() {
		obs.CreatedAt = now
	}
	obs.UpdatedAt = now

	// Normalize values
	scope := normalizeScope(obs.Scope)
	normHash := hashNormalized(obs.Content)
	topicKey := normalizeTopicKey(obs.TopicKey)
	// Track whether the caller explicitly provided a type (for dedup eligibility)
	callerType := obs.Type
	obsType := normalizeObservationType(obs.Type)
	obsSource := normalizeObservationSource(obs.Source)
	obsConfidence := normalizeConfidence(obs.Confidence)

	// 1. Check for topic_key upsert
	if topicKey != "" {
		existingID, err := s.findObservationByTopicKeyTx(tx, obs.Project, topicKey)
		if err != nil && !isNoRows(err) {
			return fmt.Errorf("memory store: find by topic key: %w", err)
		}
		if existingID > 0 {
			if err := s.captureObservationSnapshotTx(ctx, tx, existingID, "topic_key_upsert"); err != nil {
				return fmt.Errorf("memory store: snapshot before topic key upsert: %w", err)
			}
			// Update existing observation
			if err := s.updateObservationTx(tx, existingID, obs, obsType, scope, topicKey, normHash); err != nil {
				return fmt.Errorf("memory store: update by topic key: %w", err)
			}
			obs.ID = existingID
			return s.loadObservationTx(tx, obs)
		}
	}

	// 2. Check for deduplication by normalized_hash
	// Only deduplicate when the caller explicitly provides Type as "manual",
	// which signals an intentional duplicate observation.
	if callerType == domain.TypeManual {
		existingID, err := s.findDuplicateObservationTx(tx, obs.Project, scope, obsType, obs.Title, normHash)
		if err != nil && !isNoRows(err) {
			return fmt.Errorf("memory store: find duplicate: %w", err)
		}
		if existingID > 0 {
			// Increment duplicate_count and last_seen_at
			if err := s.incrementDuplicateCountTx(tx, existingID); err != nil {
				return fmt.Errorf("memory store: increment duplicate: %w", err)
			}
			obs.ID = existingID
			return s.loadObservationTx(tx, obs)
		}
	}

	// 3. Insert new observation — let SQLite set created_at/updated_at via defaults
	result, err := tx.ExecContext(ctx, `
		INSERT INTO observations (
			session_id, type, title, content, project, scope, topic_key,
			normalized_hash, revision_count, duplicate_count, last_seen_at,
			confidence, source, tags
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, 1, datetime('now'), ?, ?, ?)
	`,
		obs.SessionID, obsType, obs.Title, obs.Content, obs.Project, scope,
		nullableString(topicKey), normHash,
		obsConfidence, obsSource, tagsToJSON(obs.Tags),
	)
	if err != nil {
		return fmt.Errorf("memory store: insert observation: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("memory store: get last insert id: %w", err)
	}

	obs.ID = id
	obs.Scope = scope
	obs.TopicKey = topicKey
	obs.Type = obsType
	obs.Source = obsSource
	obs.Confidence = obsConfidence

	// Reload timestamps from DB so in-memory values match stored values
	return s.loadObservationTx(tx, obs)
}

// GetByID retrieves an observation by its ID.
// Returns ErrNotFound if the observation doesn't exist or is soft-deleted.
func (s *Store) GetByID(ctx context.Context, id int64) (*domain.Observation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, session_id, type, title, content, project, scope, topic_key,
		       confidence, source, tags, created_at, updated_at
		FROM observations
		WHERE id = ? AND deleted_at IS NULL
	`, id)

	return s.scanObservation(row)
}

// GetByTopicKey retrieves an observation by its topic key within a project.
// Returns ErrNotFound if no matching observation exists or is soft-deleted.
func (s *Store) GetByTopicKey(ctx context.Context, project, topicKey string) (*domain.Observation, error) {
	if topicKey == "" {
		return nil, &domain.ValidationError{
			Field:   "topic_key",
			Message: "topic_key cannot be empty",
		}
	}

	topicKey = normalizeTopicKey(topicKey)

	row := s.db.QueryRowContext(ctx, `
		SELECT id, session_id, type, title, content, project, scope, topic_key,
		       confidence, source, tags, created_at, updated_at
		FROM observations
		WHERE topic_key = ?
		  AND ifnull(project, '') = ifnull(?, '')
		  AND deleted_at IS NULL
		ORDER BY datetime(updated_at) DESC, datetime(created_at) DESC
		LIMIT 1
	`, topicKey, nullableString(project))

	return s.scanObservation(row)
}

// Update modifies an existing observation.
// Returns ErrNotFound if the observation doesn't exist or is soft-deleted.
func (s *Store) Update(ctx context.Context, obs *domain.Observation) error {
	if err := validateObservation(obs); err != nil {
		return err
	}

	now := time.Now().UTC()
	scope := normalizeScope(obs.Scope)
	normHash := hashNormalized(obs.Content)
	topicKey := normalizeTopicKey(obs.TopicKey)
	obsType := normalizeObservationType(obs.Type)
	obsSource := normalizeObservationSource(obs.Source)
	obsConfidence := normalizeConfidence(obs.Confidence)

	return s.withTx(ctx, func(tx *sql.Tx) error {
		// Check if observation exists and get created_at
		var createdAtStr string
		err := tx.QueryRowContext(ctx,
			"SELECT created_at FROM observations WHERE id = ? AND deleted_at IS NULL",
			obs.ID,
		).Scan(&createdAtStr)
		if err != nil {
			if isNoRows(err) {
				return &domain.NotFoundError{Type: "observation", ID: obs.ID}
			}
			return fmt.Errorf("memory store: get observation: %w", err)
		}

		// Parse created_at to preserve it -" handle both RFC3339 and SQLite formats
		createdAt := parseTime(createdAtStr)
		if createdAt.IsZero() {
			createdAt = now // Fallback to now if parsing fails
		}
		obs.CreatedAt = createdAt
		obs.UpdatedAt = now

		if err := s.captureObservationSnapshotTx(ctx, tx, obs.ID, "update"); err != nil {
			return fmt.Errorf("memory store: snapshot before update: %w", err)
		}

		// Update observation -" use Go timestamp for updated_at to preserve sub-second precision
		result, err := tx.ExecContext(ctx, `
			UPDATE observations
			SET type = ?, title = ?, content = ?, project = ?, scope = ?,
			    topic_key = ?, normalized_hash = ?, revision_count = revision_count + 1,
			    updated_at = ?,
			    confidence = ?, source = ?, tags = ?
			WHERE id = ? AND deleted_at IS NULL
		`,
			obsType, obs.Title, obs.Content, obs.Project, scope,
			nullableString(topicKey), normHash,
			now.Format(time.RFC3339Nano),
			obsConfidence, obsSource, tagsToJSON(obs.Tags),
			obs.ID,
		)
		if err != nil {
			return fmt.Errorf("memory store: update observation: %w", err)
		}

		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("memory store: get rows affected: %w", err)
		}

		if affected == 0 {
			return &domain.NotFoundError{Type: "observation", ID: obs.ID}
		}

		obs.Scope = scope
		obs.TopicKey = topicKey
		obs.Type = obsType
		obs.Source = obsSource
		obs.Confidence = obsConfidence

		// Reload to get the exact updated_at from the DB
		return s.loadObservationTx(tx, obs)
	})
}

// Delete removes an observation by ID (soft delete by default).
// Returns ErrNotFound if the observation doesn't exist or is already deleted.
func (s *Store) Delete(ctx context.Context, id int64) error {
	return s.SoftDelete(ctx, id)
}

// SoftDelete marks an observation as deleted without removing it from the database.
// Returns ErrNotFound if the observation doesn't exist or is already deleted.
func (s *Store) SoftDelete(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE observations
		SET deleted_at = datetime('now'), updated_at = datetime('now')
		WHERE id = ? AND deleted_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("memory store: soft delete observation: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("memory store: get rows affected: %w", err)
	}

	if affected == 0 {
		return &domain.NotFoundError{Type: "observation", ID: id}
	}

	return nil
}

// HardDelete permanently removes an observation from the database.
// Returns ErrNotFound if the observation doesn't exist.
func (s *Store) HardDelete(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM observations WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("memory store: hard delete observation: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("memory store: get rows affected: %w", err)
	}

	if affected == 0 {
		return &domain.NotFoundError{Type: "observation", ID: id}
	}

	return nil
}

// Restore restores a soft-deleted observation (alias for Unarchive).
func (s *Store) Restore(ctx context.Context, id int64) error {
	return s.Unarchive(ctx, id)
}

// Unarchive restores a soft-deleted observation by clearing its deleted_at field.
// Returns ErrNotFound if the observation doesn't exist or is not archived.
func (s *Store) Unarchive(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE observations
		SET deleted_at = NULL, updated_at = datetime('now')
		WHERE id = ? AND deleted_at IS NOT NULL
	`, id)
	if err != nil {
		return fmt.Errorf("memory store: unarchive observation: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("memory store: get rows affected: %w", err)
	}

	if affected == 0 {
		return &domain.NotFoundError{Type: "observation", ID: id}
	}

	return nil
}

// ListArchivable retrieves observations older than cutoff with score below minScore.
// Uses a JOIN with importance_scores to avoid N+1 queries during archival.
func (s *Store) ListArchivable(ctx context.Context, cutoff time.Time, minScore float64, limit int) ([]*domain.Observation, error) {
	if limit <= 0 {
		limit = 500
	}

	query := `
		SELECT o.id, o.session_id, o.type, o.title, o.content, o.project, o.scope,
		       o.topic_key, o.confidence, o.source, o.tags, o.created_at, o.updated_at
		FROM observations o
		LEFT JOIN importance_scores s ON s.observation_id = o.id
		WHERE o.deleted_at IS NULL
		  AND o.created_at < ?
		  AND (s.score IS NULL OR s.score < ?)
		ORDER BY o.created_at ASC
		LIMIT ?
	`

	rows, err := s.db.QueryContext(ctx, query,
		cutoff.Format(sqliteDatetimeFormat), minScore, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list archivable: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var observations []*domain.Observation
	for rows.Next() {
		obs := &domain.Observation{}
		var createdAt, updatedAt string
		var source, tagsJSON sql.NullString
		if err := rows.Scan(
			&obs.ID, &obs.SessionID, &obs.Type, &obs.Title, &obs.Content,
			&obs.Project, &obs.Scope, &obs.TopicKey,
			&obs.Confidence, &source, &tagsJSON,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("sqlite: scan archivable: %w", err)
		}
		obs.Source = source.String
		obs.Tags = tagsFromJSON(tagsJSON.String)
		obs.CreatedAt = parseTime(createdAt)
		obs.UpdatedAt = parseTime(updatedAt)
		observations = append(observations, obs)
	}

	return observations, rows.Err()
}

// List retrieves observations based on filter criteria.
// An empty filter returns all observations up to the default limit (20).
func (s *Store) List(ctx context.Context, filter domain.ObservationFilter) ([]*domain.Observation, error) {
	if filter.Limit <= 0 {
		filter.Limit = 20
	}

	query := `
		SELECT id, session_id, type, title, content, project, scope, topic_key,
		       confidence, source, tags, created_at, updated_at
		FROM observations
		WHERE deleted_at IS NULL
	`
	if filter.IncludeArchived {
		query = `
		SELECT id, session_id, type, title, content, project, scope, topic_key,
		       confidence, source, tags, created_at, updated_at
		FROM observations
		WHERE 1=1
	`
	}
	args := []any{}

	if filter.Project != "" {
		query += " AND project = ?"
		args = append(args, filter.Project)
	}

	if filter.Scope != "" {
		query += " AND scope = ?"
		args = append(args, normalizeScope(filter.Scope))
	}

	if filter.Type != "" {
		query += " AND type = ?"
		args = append(args, filter.Type)
	}

	if filter.Source != "" {
		query += " AND source = ?"
		args = append(args, filter.Source)
	}

	if filter.SessionID != "" {
		query += " AND session_id = ?"
		args = append(args, filter.SessionID)
	}

	if filter.MinConfidence > 0 {
		query += " AND confidence >= ?"
		args = append(args, filter.MinConfidence)
	}

	for _, tag := range filter.Tags {
		query += " AND EXISTS (SELECT 1 FROM json_each(tags) WHERE json_each.value = ?)"
		args = append(args, tag)
	}

	if filter.CreatedBefore != nil {
		query += " AND created_at < ?"
		args = append(args, filter.CreatedBefore.UTC().Format(sqliteDatetimeFormat))
	}

	if filter.CreatedAfter != nil {
		query += " AND created_at > ?"
		args = append(args, filter.CreatedAfter.UTC().Format(sqliteDatetimeFormat))
	}

	if filter.OrderAsc {
		query += " ORDER BY created_at ASC"
	} else {
		query += " ORDER BY created_at DESC"
	}

	// In SQLite, LIMIT must come before OFFSET
	query += " LIMIT ?"
	args = append(args, filter.Limit)

	if filter.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("memory store: list observations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanObservations(rows)
}

// ListByTopicKey retrieves all observations for a project with the given topic key.
func (s *Store) ListByTopicKey(ctx context.Context, project, topicKey string) ([]*domain.Observation, error) {
	query := `
		SELECT id, session_id, type, title, content, project, scope, topic_key,
		       confidence, source, tags, created_at, updated_at
		FROM observations
		WHERE deleted_at IS NULL AND project = ? AND topic_key = ?
		ORDER BY created_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query, project, topicKey)
	if err != nil {
		return nil, fmt.Errorf("memory store: list by topic key: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return s.scanObservations(rows)
}

// ConsolidationGroup represents a topic key with multiple observations.
type ConsolidationGroup struct {
	TopicKey string
	Count    int
	Latest   string
}

// FindConsolidationCandidates finds topic keys with multiple observations in a project.
func (s *Store) FindConsolidationCandidates(ctx context.Context, project string, minCount int) ([]ConsolidationGroup, error) {
	if minCount <= 0 {
		minCount = 2
	}
	query := `
		SELECT topic_key, COUNT(*) as cnt, MAX(created_at) as latest
		FROM observations
		WHERE project = ? AND topic_key != '' AND deleted_at IS NULL
		GROUP BY topic_key
		HAVING cnt >= ?
		ORDER BY cnt DESC
	`
	rows, err := s.db.QueryContext(ctx, query, project, minCount)
	if err != nil {
		return nil, fmt.Errorf("memory store: find consolidation candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var groups []ConsolidationGroup
	for rows.Next() {
		var g ConsolidationGroup
		if err := rows.Scan(&g.TopicKey, &g.Count, &g.Latest); err != nil {
			return nil, fmt.Errorf("memory store: scan consolidation group: %w", err)
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// StaleObservations returns observations with high importance score but no recent access.
func (s *Store) StaleObservations(ctx context.Context, project string, minScore float64, daysSinceAccess int) ([]*domain.Observation, error) {
	cutoff := fmt.Sprintf("-%d days", daysSinceAccess)
	query := `
		SELECT o.id, o.session_id, o.type, o.title, o.content, o.project, o.scope, o.topic_key,
		       o.confidence, o.source, o.tags, o.created_at, o.updated_at
		FROM observations o
		JOIN importance_scores s ON s.observation_id = o.id
		WHERE o.deleted_at IS NULL
		  AND o.project = ?
		  AND s.score >= ?
		  AND s.last_accessed < datetime('now', ?)
		ORDER BY s.score DESC
		LIMIT 20
	`
	rows, err := s.db.QueryContext(ctx, query, project, minScore, cutoff)
	if err != nil {
		return nil, fmt.Errorf("memory store: stale observations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return s.scanObservations(rows)
}

// OrphanObservations returns observations with no graph edges.
func (s *Store) OrphanObservations(ctx context.Context, project string, limit int) ([]*domain.Observation, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `
		SELECT o.id, o.session_id, o.type, o.title, o.content, o.project, o.scope, o.topic_key,
		       o.confidence, o.source, o.tags, o.created_at, o.updated_at
		FROM observations o
		LEFT JOIN edges e1 ON e1.from_obs_id = o.id
		LEFT JOIN edges e2 ON e2.to_obs_id = o.id
		WHERE o.deleted_at IS NULL
		  AND o.project = ?
		  AND e1.id IS NULL
		  AND e2.id IS NULL
		ORDER BY o.created_at DESC
		LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, query, project, limit)
	if err != nil {
		return nil, fmt.Errorf("memory store: orphan observations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return s.scanObservations(rows)
}

// CountAll counts all non-deleted observations in the system.
func (s *Store) CountAll(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM observations
		WHERE deleted_at IS NULL
	`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("memory store: count all observations: %w", err)
	}
	return count, nil
}

// CountByRoot counts distinct observations reachable from a root observation.
func (s *Store) CountByRoot(ctx context.Context, rootObsID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		WITH RECURSIVE related(id) AS (
			SELECT ?
			UNION
			SELECT CASE
				WHEN e.from_obs_id = related.id THEN e.to_obs_id
				ELSE e.from_obs_id
			END
			FROM edges e
			JOIN related ON e.from_obs_id = related.id OR e.to_obs_id = related.id
		)
		SELECT COUNT(DISTINCT o.id)
		FROM observations o
		JOIN related r ON r.id = o.id
		WHERE o.deleted_at IS NULL
	`, rootObsID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("memory store: count observations by root: %w", err)
	}
	return count, nil
}

// GetBySource retrieves observations filtered by source type.
func (s *Store) GetBySource(ctx context.Context, source string, limit int) ([]*domain.Observation, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, type, title, content, project, scope, topic_key,
		       confidence, source, tags, created_at, updated_at
		FROM observations
		WHERE deleted_at IS NULL AND source = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, normalizeObservationSource(source), limit)
	if err != nil {
		return nil, fmt.Errorf("memory store: get observations by source: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanObservations(rows)
}

// GetByType retrieves observations filtered by type.
func (s *Store) GetByType(ctx context.Context, obsType string, limit int) ([]*domain.Observation, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, type, title, content, project, scope, topic_key,
		       confidence, source, tags, created_at, updated_at
		FROM observations
		WHERE deleted_at IS NULL AND type = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, normalizeObservationType(obsType), limit)
	if err != nil {
		return nil, fmt.Errorf("memory store: get observations by type: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanObservations(rows)
}

// Stats returns aggregated statistics about observations.
func (s *Store) Stats(ctx context.Context) (*Stats, error) {
	stats := &Stats{}

	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL",
	).Scan(&stats.TotalObservations)
	if err != nil {
		return nil, fmt.Errorf("memory store: count observations: %w", err)
	}

	// Get projects
	rows, err := s.db.QueryContext(ctx, `
		SELECT project
		FROM observations
		WHERE project IS NOT NULL AND project != '' AND deleted_at IS NULL
		GROUP BY project
		ORDER BY MAX(created_at) DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("memory store: get projects: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var project string
		if err := rows.Scan(&project); err != nil {
			return nil, fmt.Errorf("memory store: scan project: %w", err)
		}
		stats.Projects = append(stats.Projects, project)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory store: iterate projects: %w", err)
	}

	// Count by type
	typeRows, err := s.db.QueryContext(ctx, `
		SELECT type, COUNT(*)
		FROM observations
		WHERE deleted_at IS NULL
		GROUP BY type
	`)
	if err != nil {
		return nil, fmt.Errorf("memory store: count by type: %w", err)
	}
	defer func() { _ = typeRows.Close() }()

	stats.ByType = make(map[string]int)
	for typeRows.Next() {
		var obsType string
		var count int
		if err := typeRows.Scan(&obsType, &count); err != nil {
			return nil, fmt.Errorf("memory store: scan type count: %w", err)
		}
		stats.ByType[obsType] = count
	}

	if err := typeRows.Err(); err != nil {
		return nil, fmt.Errorf("memory store: iterate type counts: %w", err)
	}

	return stats, nil
}

// Stats holds aggregated statistics about observations.
type Stats struct {
	TotalObservations int            `json:"total_observations"`
	Projects          []string       `json:"projects"`
	ByType            map[string]int `json:"by_type"`
}

// --- Transaction Helpers ------------------------------------------------------

func (s *Store) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	// Use IMMEDIATE isolation to prevent write conflicts during topic_key upsert
	// and deduplication checks under concurrent access.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("memory store: begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory store: commit transaction: %w", err)
	}

	return nil
}

// DB returns the underlying *sql.DB shared by all SQLite stores. The
// UnitOfWork (W2.1) uses this to open ONE transaction that threads through
// every TxParticipant (ADR-02, REQ-TX-001).
func (s *Store) DB() *sql.DB {
	return s.db
}

// --- Cross-Store Transaction Seam (W2.1, REQ-TX-001) -------------------------
//
// The TxParticipant seam lets the Store enlist in a shared *sql.Tx owned by a
// UnitOfWork, instead of opening its own transaction. The local-mode Save()
// path is unchanged; the new atomic path goes through WithinTx + SaveInTx.

// txKey is the context key under which WithinTx stashes the shared *sql.Tx.
type txKey struct{}

// txFromContext returns the *sql.Tx stashed by WithinTx, or nil if none is
// active. Tx-aware Store methods (SaveInTx) use this to find the shared tx.
func txFromContext(ctx context.Context) *sql.Tx {
	tx, _ := ctx.Value(txKey{}).(*sql.Tx)
	return tx
}

// WithinTx implements domain.TxParticipant. It type-asserts the handle to
// *sql.Tx, stashes it into the context, and invokes fn within that context.
// The fn closure can then call tx-aware Store methods (e.g. SaveInTx) that
// read the shared tx via txFromContext.
//
// WithinTx does NOT begin, commit, or roll back the transaction — the
// UnitOfWork that owns the shared tx is responsible for its lifecycle.
func (s *Store) WithinTx(ctx context.Context, handle any, fn func(context.Context) error) error {
	tx, ok := handle.(*sql.Tx)
	if !ok {
		return fmt.Errorf("sqlite store: WithinTx expected *sql.Tx handle, got %T", handle)
	}
	return fn(context.WithValue(ctx, txKey{}, tx))
}

// SaveInTx saves an observation using a shared transaction previously stashed
// in the context by WithinTx. It MUST be called from within a WithinTx closure
// (or any context that carries a txKey). It does NOT begin/commit its own
// transaction — the UnitOfWork owns the lifecycle.
//
// This is the atomic-path counterpart to Save() (REQ-TX-001).
func (s *Store) SaveInTx(ctx context.Context, obs *domain.Observation) error {
	tx := txFromContext(ctx)
	if tx == nil {
		return fmt.Errorf("sqlite store: SaveInTx requires an active shared transaction (call within WithinTx)")
	}
	return s.saveInTx(ctx, tx, obs)
}

// Ensure Store implements domain.TxParticipant (W2.1 adoption of the W1 port).
var _ domain.TxParticipant = (*Store)(nil)

// --- Transaction-Scoped Operations -------------------------------------------

func (s *Store) findObservationByTopicKeyTx(tx *sql.Tx, project, topicKey string) (int64, error) {
	var id int64
	err := tx.QueryRow(`
		SELECT id FROM observations
		WHERE topic_key = ?
		  AND ifnull(project, '') = ifnull(?, '')
		  AND deleted_at IS NULL
		ORDER BY datetime(updated_at) DESC, datetime(created_at) DESC
		LIMIT 1
	`, topicKey, nullableString(project)).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) findDuplicateObservationTx(tx *sql.Tx, project, scope, obsType, title, normHash string) (int64, error) {
	// Look for exact hash match within deduplication window (15 minutes)
	var id int64
	err := tx.QueryRow(`
		SELECT id FROM observations
		WHERE normalized_hash = ?
		  AND ifnull(project, '') = ifnull(?, '')
		  AND scope = ?
		  AND type = ?
		  AND title = ?
		  AND deleted_at IS NULL
		  AND datetime(created_at) >= datetime('now', '-15 minutes')
		ORDER BY created_at DESC
		LIMIT 1
	`, normHash, nullableString(project), scope, obsType, title).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) updateObservationTx(tx *sql.Tx, id int64, obs *domain.Observation, obsType, scope, topicKey, normHash string) error {
	_, err := tx.Exec(`
		UPDATE observations
		SET type = ?, title = ?, content = ?, project = ?, scope = ?,
		    topic_key = ?, normalized_hash = ?, revision_count = revision_count + 1,
		    last_seen_at = datetime('now'), updated_at = datetime('now'),
		    confidence = ?, source = ?, tags = ?
		WHERE id = ? AND deleted_at IS NULL
	`,
		obsType, obs.Title, obs.Content, obs.Project, scope,
		nullableString(topicKey), normHash,
		obs.Confidence, obs.Source, tagsToJSON(obs.Tags), id,
	)
	return err
}

func (s *Store) incrementDuplicateCountTx(tx *sql.Tx, id int64) error {
	_, err := tx.Exec(`
		UPDATE observations
		SET duplicate_count = duplicate_count + 1,
		    last_seen_at = datetime('now'),
		    updated_at = datetime('now')
		WHERE id = ?
	`, id)
	return err
}

func (s *Store) loadObservationTx(tx *sql.Tx, obs *domain.Observation) error {
	row := tx.QueryRow(`
		SELECT id, session_id, type, title, content, project, scope, topic_key,
		       confidence, source, tags, created_at, updated_at
		FROM observations
		WHERE id = ? AND deleted_at IS NULL
	`, obs.ID)

	return s.scanObservationRow(row, obs)
}

func (s *Store) captureObservationSnapshotTx(ctx context.Context, tx *sql.Tx, id int64, reason string) error {
	state, err := s.loadObservationRevisionStateTx(tx, id)
	if err != nil {
		if isMissingTableError(err, "temporal_snapshots") {
			log.Printf("memory store: temporal_snapshots table not found, skipping snapshot for observation %d", id)
			return nil
		}
		return err
	}

	capturedAt := time.Now().UTC()
	payload, err := json.Marshal(observationRevisionSnapshot{
		Reason:   reason,
		Captured: capturedAt,
		Previous: *state,
	})
	if err != nil {
		return fmt.Errorf("memory store: marshal revision snapshot: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
        INSERT INTO temporal_snapshots (
            snapshot_key, timestamp, description, observation_count, edge_count, root_observation_id
        ) VALUES (?, ?, ?, ?, ?, ?)
    `,
		fmt.Sprintf("observation-history/%d", id),
		capturedAt.Format(time.RFC3339Nano),
		string(payload),
		state.RevisionCount,
		0,
		id,
	)
	if err != nil && isMissingTableError(err, "temporal_snapshots") {
		log.Printf("memory store: temporal_snapshots table not found, skipping snapshot insert for observation %d", id)
		return nil
	}
	if err != nil {
		return fmt.Errorf("memory store: insert revision snapshot: %w", err)
	}
	return nil
}

func (s *Store) loadObservationRevisionStateTx(tx *sql.Tx, id int64) (*observationRevisionState, error) {
	row := tx.QueryRow(`
        SELECT id, session_id, type, title, content, project, scope, topic_key,
               confidence, source, tags, created_at, updated_at,
               revision_count, duplicate_count, last_seen_at
        FROM observations
        WHERE id = ? AND deleted_at IS NULL
    `, id)

	state := &observationRevisionState{}
	var project, topicKey, source sql.NullString
	var tagsJSON sql.NullString
	var createdAtStr, updatedAtStr string
	var lastSeenAtStr sql.NullString
	err := row.Scan(
		&state.ID,
		&state.SessionID,
		&state.Type,
		&state.Title,
		&state.Content,
		&project,
		&state.Scope,
		&topicKey,
		&state.Confidence,
		&source,
		&tagsJSON,
		&createdAtStr,
		&updatedAtStr,
		&state.RevisionCount,
		&state.DuplicateCount,
		&lastSeenAtStr,
	)
	if err != nil {
		if isNoRows(err) {
			return nil, &domain.NotFoundError{Type: "observation", ID: id}
		}
		return nil, fmt.Errorf("memory store: load revision state: %w", err)
	}

	state.Project = project.String
	state.TopicKey = topicKey.String
	state.Source = source.String
	state.Tags = tagsFromJSON(tagsJSON.String)
	state.CreatedAt = parseTime(createdAtStr)
	state.UpdatedAt = parseTime(updatedAtStr)
	if lastSeenAtStr.Valid {
		lastSeenAt := parseTime(lastSeenAtStr.String)
		if !lastSeenAt.IsZero() {
			state.LastSeenAt = &lastSeenAt
		}
	}

	return state, nil
}

// "-"-"- Scan Helpers "-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-

func (s *Store) scanObservation(row *sql.Row) (*domain.Observation, error) {
	var obs domain.Observation
	err := s.scanObservationRow(row, &obs)
	if err != nil {
		return nil, err
	}
	return &obs, nil
}

func (s *Store) scanObservationRow(row *sql.Row, obs *domain.Observation) error {
	var createdAtStr, updatedAtStr string
	var project, topicKey, source sql.NullString
	var tagsJSON sql.NullString

	err := row.Scan(
		&obs.ID, &obs.SessionID, &obs.Type, &obs.Title, &obs.Content,
		&project, &obs.Scope, &topicKey,
		&obs.Confidence, &source, &tagsJSON,
		&createdAtStr, &updatedAtStr,
	)
	if err != nil {
		if isNoRows(err) {
			return &domain.NotFoundError{Type: "observation", ID: obs.ID}
		}
		return fmt.Errorf("memory store: scan observation: %w", err)
	}

	obs.Project = project.String
	obs.TopicKey = topicKey.String
	obs.Source = source.String
	obs.Tags = tagsFromJSON(tagsJSON.String)

	obs.CreatedAt = parseTime(createdAtStr)
	obs.UpdatedAt = parseTime(updatedAtStr)

	return nil
}

func (s *Store) scanObservations(rows *sql.Rows) ([]*domain.Observation, error) {
	var observations []*domain.Observation

	for rows.Next() {
		var obs domain.Observation
		var createdAtStr, updatedAtStr string
		var project, topicKey, source sql.NullString
		var tagsJSON sql.NullString

		err := rows.Scan(
			&obs.ID, &obs.SessionID, &obs.Type, &obs.Title, &obs.Content,
			&project, &obs.Scope, &topicKey,
			&obs.Confidence, &source, &tagsJSON,
			&createdAtStr, &updatedAtStr,
		)
		if err != nil {
			return nil, fmt.Errorf("memory store: scan observations: %w", err)
		}

		obs.Project = project.String
		obs.TopicKey = topicKey.String
		obs.Source = source.String
		obs.Tags = tagsFromJSON(tagsJSON.String)

		obs.CreatedAt = parseTime(createdAtStr)
		obs.UpdatedAt = parseTime(updatedAtStr)

		observations = append(observations, &obs)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory store: iterate observations: %w", err)
	}

	return observations, nil
}

// "-"-"- Tag JSON Helpers "-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-

func tagsToJSON(tags []string) interface{} {
	if len(tags) == 0 {
		return nil
	}
	b, _ := json.Marshal(tags)
	return string(b)
}

func tagsFromJSON(s string) []string {
	if s == "" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(s), &tags); err != nil {
		return nil
	}
	return tags
}

// "-"-"- Utility Functions "-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-"-

func normalizeScope(scope string) string {
	v := strings.TrimSpace(strings.ToLower(scope))
	if v == "personal" {
		return "personal"
	}
	return "project"
}

func normalizeObservationType(obsType string) string {
	v := strings.TrimSpace(strings.ToLower(obsType))
	if v == "" {
		return domain.TypeManual
	}
	return v
}

func normalizeObservationSource(source string) string {
	v := strings.TrimSpace(strings.ToLower(source))
	if v == "" {
		return domain.SourceManual
	}
	return v
}

func normalizeConfidence(confidence float64) float64 {
	if confidence <= 0 {
		return 1.0
	}
	if confidence > 1 {
		return 1.0
	}
	return confidence
}

func normalizeTopicKey(topicKey string) string {
	v := strings.TrimSpace(strings.ToLower(topicKey))
	if v == "" {
		return ""
	}
	// Replace spaces with hyphens
	v = strings.Join(strings.Fields(v), "-")
	if len(v) > 120 {
		v = v[:120]
	}
	return v
}

func hashNormalized(content string) string {
	h := sha256.New()
	fields := strings.Fields(content)
	for i, f := range fields {
		if i > 0 {
			_, _ = io.WriteString(h, " ")
		}
		_, _ = io.WriteString(h, strings.ToLower(f))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func isNoRows(err error) bool {
	return err == sql.ErrNoRows
}

func isMissingTableError(err error, table string) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "no such table: "+strings.ToLower(table))
}

// parseTime parses a time string in RFC3339, RFC3339Nano, or SQLite datetime format.
func parseTime(s string) time.Time {
	// Try RFC3339Nano first (most precise)
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	// Try RFC3339
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	// Try SQLite datetime format
	if t, err := time.Parse(sqliteDatetimeFormat, s); err == nil {
		return t
	}
	if s != "" {
		log.Printf("sqlite: failed to parse time %q", s)
	}
	return time.Time{}
}

func validateObservation(obs *domain.Observation) error {
	if obs == nil {
		return &domain.ValidationError{
			Field:   "observation",
			Message: "observation cannot be nil",
		}
	}

	title := strings.TrimSpace(obs.Title)
	if title == "" {
		return &domain.ValidationError{
			Field:   "title",
			Message: "title is required",
		}
	}
	if len(title) > maxObservationTitleLength {
		return &domain.ValidationError{
			Field:   "title",
			Message: fmt.Sprintf("title exceeds %d characters", maxObservationTitleLength),
		}
	}

	content := strings.TrimSpace(obs.Content)
	if content == "" {
		return &domain.ValidationError{
			Field:   "content",
			Message: "content is required",
		}
	}
	if len(content) > maxObservationContentLength {
		return &domain.ValidationError{
			Field:   "content",
			Message: fmt.Sprintf("content exceeds %d characters", maxObservationContentLength),
		}
	}

	if source := strings.TrimSpace(obs.Source); len(source) > maxObservationSourceLength {
		return &domain.ValidationError{
			Field:   "source",
			Message: fmt.Sprintf("source exceeds %d characters", maxObservationSourceLength),
		}
	}

	if obs.Type != "" && !isAllowedObservationType(normalizeObservationType(obs.Type)) {
		return &domain.ValidationError{
			Field:   "type",
			Message: "type must be one of manual, tool_use, decision, bugfix, pattern, config, discovery, learning",
		}
	}

	if obs.Source != "" && !isAllowedObservationSource(normalizeObservationSource(obs.Source)) {
		return &domain.ValidationError{
			Field:   "source",
			Message: "source must be one of manual, ai, auto, import",
		}
	}

	if obs.Confidence < 0 || obs.Confidence > 1 {
		return &domain.ValidationError{
			Field:   "confidence",
			Message: "confidence must be between 0.0 and 1.0",
		}
	}

	if obs.TopicKey != "" && normalizeTopicKey(obs.TopicKey) == "" {
		return &domain.ValidationError{
			Field:   "topic_key",
			Message: "topic_key cannot be blank",
		}
	}

	return nil
}

func isAllowedObservationType(obsType string) bool {
	switch obsType {
	case domain.TypeManual, domain.TypeToolUse, domain.TypeDecision, domain.TypeBugfix,
		domain.TypePattern, domain.TypeConfig, domain.TypeDiscovery, domain.TypeLearning,
		domain.TypeSessionSummary, domain.TypePassive:
		return true
	default:
		return false
	}
}

func isAllowedObservationSource(source string) bool {
	switch source {
	case domain.SourceManual, domain.SourceAI, domain.SourceAuto, domain.SourceImport:
		return true
	default:
		return false
	}
}

// RecordSearchFeedback logs an implicit signal: the user accessed an observation
// after performing a search. This data enables Learning-to-Rank model training.
func (s *Store) RecordSearchFeedback(ctx context.Context, query string, observationID int64, rankPosition int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO search_feedback(query, observation_id, rank_position) VALUES(?, ?, ?)`,
		query, observationID, rankPosition)
	if err != nil && isMissingTableError(err, "search_feedback") {
		return nil // table doesn't exist yet
	}
	return err
}

// GetSearchFeedbackStats returns basic stats about search feedback data.
func (s *Store) GetSearchFeedbackStats(ctx context.Context) (totalEntries int, uniqueQueries int, err error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COUNT(DISTINCT query) FROM search_feedback`)
	err = row.Scan(&totalEntries, &uniqueQueries)
	if err != nil && isMissingTableError(err, "search_feedback") {
		return 0, 0, nil
	}
	return totalEntries, uniqueQueries, err
}

// GetSyncedChunks returns a set of chunk IDs that have been imported/exported.
func (s *Store) GetSyncedChunks(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT chunk_id FROM sync_chunks`)
	if err != nil {
		// Table may not exist yet (pre-migration 012)
		if isMissingTableError(err, "sync_chunks") {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("memory store: get synced chunks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	chunks := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		chunks[id] = true
	}
	return chunks, rows.Err()
}

// RecordSyncedChunk marks a chunk as imported/exported (idempotent).
func (s *Store) RecordSyncedChunk(ctx context.Context, chunkID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO sync_chunks(chunk_id) VALUES(?)`, chunkID)
	if err != nil && isMissingTableError(err, "sync_chunks") {
		return nil // graceful if table doesn't exist yet
	}
	return err
}

// ExportData holds all data for sync export.
type ExportData struct {
	Version      string               `json:"version"`
	ExportedAt   string               `json:"exported_at"`
	Sessions     []*domain.Session    `json:"sessions"`
	Observations []*domain.Observation `json:"observations"`
	Prompts      []*domain.Prompt     `json:"prompts"`
}

// ExportAll exports all sessions, observations, and prompts for sync.
func (s *Store) ExportAll(ctx context.Context) (*ExportData, error) {
	data := &ExportData{
		Version:    "0.1.0",
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// Export sessions
	sessRows, err := s.db.QueryContext(ctx,
		`SELECT id, project, directory, started_at, ended_at, summary FROM sessions ORDER BY started_at`)
	if err != nil {
		return nil, fmt.Errorf("export sessions: %w", err)
	}
	defer func() { _ = sessRows.Close() }()
	for sessRows.Next() {
		sess := &domain.Session{}
		var startedAt string
		var endedAt, summary sql.NullString
		if err := sessRows.Scan(&sess.ID, &sess.Project, &sess.Directory, &startedAt, &endedAt, &summary); err != nil {
			return nil, err
		}
		sess.StartedAt = parseTime(startedAt)
		if endedAt.Valid {
			t := parseTime(endedAt.String)
			sess.EndedAt = &t
		}
		if summary.Valid {
			sess.Summary = summary.String
		}
		data.Sessions = append(data.Sessions, sess)
	}
	if err := sessRows.Err(); err != nil {
		return nil, err
	}

	// Export observations
	obs, err := s.List(ctx, domain.ObservationFilter{Limit: 100000})
	if err != nil {
		return nil, fmt.Errorf("export observations: %w", err)
	}
	data.Observations = obs

	// Export prompts
	promptRows, err := s.db.QueryContext(ctx,
		`SELECT id, content, project, session_id, created_at FROM user_prompts ORDER BY id`)
	if err != nil {
		if !isMissingTableError(err, "user_prompts") {
			return nil, fmt.Errorf("export prompts: %w", err)
		}
	} else {
		defer func() { _ = promptRows.Close() }()
		for promptRows.Next() {
			p := &domain.Prompt{}
			var createdAt string
			if err := promptRows.Scan(&p.ID, &p.Content, &p.Project, &p.SessionID, &createdAt); err != nil {
				return nil, err
			}
			p.CreatedAt = parseTime(createdAt)
			data.Prompts = append(data.Prompts, p)
		}
		if err := promptRows.Err(); err != nil {
			return nil, err
		}
	}

	return data, nil
}

// SyncImportResult holds the outcome of a sync import.
type SyncImportResult struct {
	SessionsImported     int `json:"sessions_imported"`
	ObservationsImported int `json:"observations_imported"`
	PromptsImported      int `json:"prompts_imported"`
}

// ImportData imports sessions, observations and prompts from an export.
// Sessions are skipped if they already exist (by ID).
// Observations and prompts get new auto-increment IDs.
func (s *Store) ImportData(ctx context.Context, data *ExportData) (*SyncImportResult, error) {
	result := &SyncImportResult{}

	return result, s.withTx(ctx, func(tx *sql.Tx) error {
		// Import sessions (skip duplicates)
		for _, sess := range data.Sessions {
			var endedAt interface{}
			if sess.EndedAt != nil {
				endedAt = sess.EndedAt.Format(time.RFC3339)
			}
			res, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO sessions(id, project, directory, started_at, ended_at, summary)
				 VALUES(?, ?, ?, ?, ?, ?)`,
				sess.ID, sess.Project, sess.Directory,
				sess.StartedAt.Format(time.RFC3339), endedAt, nullableString(sess.Summary))
			if err != nil {
				return fmt.Errorf("import session %s: %w", sess.ID, err)
			}
			n, _ := res.RowsAffected()
			result.SessionsImported += int(n)
		}

		// Import observations (new IDs)
		for _, obs := range data.Observations {
			scope := normalizeScope(obs.Scope)
			topicKey := normalizeTopicKey(obs.TopicKey)
			obsType := normalizeObservationType(obs.Type)
			obsSource := normalizeObservationSource(obs.Source)

			_, err := tx.ExecContext(ctx,
				`INSERT INTO observations(session_id, type, title, content, project, scope, topic_key,
				  normalized_hash, revision_count, duplicate_count, confidence, source, tags,
				  created_at, updated_at)
				 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				obs.SessionID, obsType, obs.Title, obs.Content, obs.Project, scope,
				nullableString(topicKey), hashNormalized(obs.Content),
				1, 1,
				normalizeConfidence(obs.Confidence), obsSource, tagsToJSON(obs.Tags),
				obs.CreatedAt.Format(time.RFC3339), obs.UpdatedAt.Format(time.RFC3339))
			if err != nil {
				return fmt.Errorf("import observation %q: %w", obs.Title, err)
			}
			result.ObservationsImported++
		}

		// Import prompts (new IDs)
		for _, p := range data.Prompts {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO user_prompts(session_id, content, project, created_at)
				 VALUES(?, ?, ?, ?)`,
				p.SessionID, p.Content, p.Project, p.CreatedAt.Format(time.RFC3339))
			if err != nil {
				if isMissingTableError(err, "user_prompts") {
					continue
				}
				return fmt.Errorf("import prompt: %w", err)
			}
			result.PromptsImported++
		}

		return nil
	})
}

// MergeResult holds the outcome of a project merge operation.
type MergeResult struct {
	Canonical           string   `json:"canonical"`
	SourcesMerged       []string `json:"sources_merged"`
	ObservationsUpdated int64    `json:"observations_updated"`
	SessionsUpdated     int64    `json:"sessions_updated"`
}

// MergeProjects moves all observations and sessions from source projects into a canonical project name.
// Sources that normalize to the canonical name are silently skipped.
func (s *Store) MergeProjects(ctx context.Context, sources []string, canonical string) (*MergeResult, error) {
	canonical = strings.TrimSpace(strings.ToLower(canonical))
	if canonical == "" {
		return nil, &domain.ValidationError{Field: "canonical", Message: "canonical project name must not be empty"}
	}

	result := &MergeResult{Canonical: canonical}

	return result, s.withTx(ctx, func(tx *sql.Tx) error {
		for _, src := range sources {
			src = strings.TrimSpace(strings.ToLower(src))
			if src == "" || src == canonical {
				continue
			}

			res, err := tx.ExecContext(ctx, `UPDATE observations SET project = ? WHERE project = ?`, canonical, src)
			if err != nil {
				return fmt.Errorf("merge observations %q -> %q: %w", src, canonical, err)
			}
			n, _ := res.RowsAffected()
			result.ObservationsUpdated += n

			res, err = tx.ExecContext(ctx, `UPDATE sessions SET project = ? WHERE project = ?`, canonical, src)
			if err != nil {
				return fmt.Errorf("merge sessions %q -> %q: %w", src, canonical, err)
			}
			n, _ = res.RowsAffected()
			result.SessionsUpdated += n

			result.SourcesMerged = append(result.SourcesMerged, src)
		}
		return nil
	})
}

// Ensure Store implements domain.ObservationRepository
var _ domain.ObservationRepository = (*Store)(nil)

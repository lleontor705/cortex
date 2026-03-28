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

// Store implements the SQLite observation store.
// It provides CRUD operations with deduplication, topic key upsert,
// and soft/hard delete support.
type Store struct {
	db *sql.DB
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
func (s *Store) Save(ctx context.Context, obs *domain.Observation) error {
	if obs == nil {
		return &domain.ValidationError{
			Field:   "observation",
			Message: "observation cannot be nil",
		}
	}

	if obs.Title == "" {
		return &domain.ValidationError{
			Field:   "title",
			Message: "title is required",
		}
	}

	if obs.Content == "" {
		return &domain.ValidationError{
			Field:   "content",
			Message: "content is required",
		}
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
	obsType := obs.Type
	if obsType == "" {
		obsType = domain.TypeManual
	}

	return s.withTx(ctx, func(tx *sql.Tx) error {
		// 1. Check for topic_key upsert
		if topicKey != "" {
			existingID, err := s.findObservationByTopicKeyTx(tx, obs.Project, topicKey)
			if err != nil && !isNoRows(err) {
				return fmt.Errorf("memory store: find by topic key: %w", err)
			}
			if existingID > 0 {
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
			obs.Confidence, obs.Source, tagsToJSON(obs.Tags),
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

		// Reload timestamps from DB so in-memory values match stored values
		return s.loadObservationTx(tx, obs)
	})
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
	if obs == nil {
		return &domain.ValidationError{
			Field:   "observation",
			Message: "observation cannot be nil",
		}
	}

	if obs.Title == "" {
		return &domain.ValidationError{
			Field:   "title",
			Message: "title is required",
		}
	}

	if obs.Content == "" {
		return &domain.ValidationError{
			Field:   "content",
			Message: "content is required",
		}
	}

	now := time.Now().UTC()
	scope := normalizeScope(obs.Scope)
	normHash := hashNormalized(obs.Content)
	topicKey := normalizeTopicKey(obs.TopicKey)
	obsType := obs.Type
	if obsType == "" {
		obsType = domain.TypeManual
	}

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

		// Parse created_at to preserve it — handle both RFC3339 and SQLite formats
		createdAt := parseTime(createdAtStr)
		if createdAt.IsZero() {
			createdAt = now // Fallback to now if parsing fails
		}
		obs.CreatedAt = createdAt
		obs.UpdatedAt = now

		// Update observation — use Go timestamp for updated_at to preserve sub-second precision
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
			obs.Confidence, obs.Source, tagsToJSON(obs.Tags),
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
	defer rows.Close()

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
	defer rows.Close()

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
	defer rows.Close()

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
	defer typeRows.Close()

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

// ─── Transaction Helpers ─────────────────────────────────────────────────────

func (s *Store) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
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

// ─── Transaction-Scoped Operations ───────────────────────────────────────────

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

// ─── Scan Helpers ─────────────────────────────────────────────────────────────

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

// ─── Tag JSON Helpers ───────────────────────────────────────────────────────

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

// ─── Utility Functions ───────────────────────────────────────────────────────

func normalizeScope(scope string) string {
	v := strings.TrimSpace(strings.ToLower(scope))
	if v == "personal" {
		return "personal"
	}
	return "project"
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
			io.WriteString(h, " ")
		}
		io.WriteString(h, strings.ToLower(f))
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

// Ensure Store implements domain.ObservationRepository
var _ domain.ObservationRepository = (*Store)(nil)

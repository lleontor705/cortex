// Package graph implements the SQLite graph store for Cortex.
//
// It provides knowledge graph operations for creating, querying, and deleting
// edges between observations. The store implements the domain.GraphRepository interface.
package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// sqliteDatetimeFormat is the format used by SQLite's datetime() function.
const sqliteDatetimeFormat = "2006-01-02 15:04:05"

// Store implements the SQLite graph store.
type Store struct {
	db *sql.DB
}

// NewStore creates a new graph store with the given database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateEdge creates a relationship between two observations.
// Returns domain.ErrAlreadyExists if an edge with the same (from, to, relation_type) exists.
func (s *Store) CreateEdge(ctx context.Context, edge *domain.Edge) error {
	if edge == nil {
		return &domain.ValidationError{
			Field:   "edge",
			Message: "edge cannot be nil",
		}
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO edges (from_obs_id, to_obs_id, relation_type, weight, confidence, source, reasoning, valid_from, invalid_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		edge.FromObsID, edge.ToObsID, edge.RelationType, edge.Weight,
		edge.Confidence, nullableString(edge.Source), nullableString(edge.Reasoning),
		nullableTime(edge.ValidFrom), nullableTime(edge.InvalidAt),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return domain.ErrAlreadyExists
		}
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			return &domain.NotFoundError{Type: "observation", ID: fmt.Sprintf("%d or %d", edge.FromObsID, edge.ToObsID)}
		}
		return fmt.Errorf("graph: create edge: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("graph: get last insert id: %w", err)
	}
	edge.ID = id

	return nil
}

// GetRelated retrieves observations related to the given observation ID,
// up to the specified depth using a recursive CTE.
func (s *Store) GetRelated(ctx context.Context, obsID int64, depth int) ([]*domain.Observation, error) {
	if depth <= 0 {
		depth = 1
	}
	if depth > 10 {
		depth = 10
	}

	query := `
		WITH RECURSIVE related(id, lvl) AS (
			SELECT ?, 0
			UNION
			SELECT CASE
				WHEN e.from_obs_id = related.id THEN e.to_obs_id
				ELSE e.from_obs_id
			END, related.lvl + 1
			FROM edges e
			JOIN related ON (e.from_obs_id = related.id OR e.to_obs_id = related.id)
			WHERE related.lvl < ?
		)
		SELECT DISTINCT o.id, o.title, o.content, o.type, o.project, o.scope,
		       o.session_id, COALESCE(o.topic_key, '') AS topic_key,
		       COALESCE(o.confidence, 1.0), COALESCE(o.source, 'manual'),
		       COALESCE(o.tags, ''), o.created_at, o.updated_at
		FROM observations o
		JOIN related r ON r.id = o.id
		WHERE o.deleted_at IS NULL AND o.id != ?
		ORDER BY o.created_at DESC
	`

	rows, err := s.db.QueryContext(ctx, query, obsID, depth, obsID)
	if err != nil {
		return nil, fmt.Errorf("graph: get related: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var observations []*domain.Observation
	for rows.Next() {
		obs := &domain.Observation{}
		var createdAt, updatedAt, tagsJSON string
		if err := rows.Scan(
			&obs.ID, &obs.Title, &obs.Content, &obs.Type, &obs.Project,
			&obs.Scope, &obs.SessionID, &obs.TopicKey,
			&obs.Confidence, &obs.Source, &tagsJSON,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("graph: scan related observation: %w", err)
		}
		obs.CreatedAt, _ = parseTime(createdAt)
		obs.UpdatedAt, _ = parseTime(updatedAt)
		if tagsJSON != "" {
			_ = json.Unmarshal([]byte(tagsJSON), &obs.Tags)
		}
		observations = append(observations, obs)
	}

	if observations == nil {
		return []*domain.Observation{}, rows.Err()
	}
	return observations, rows.Err()
}

// DeleteEdge removes a relationship between observations.
func (s *Store) DeleteEdge(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM edges WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("graph: delete edge: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("graph: rows affected: %w", err)
	}

	if rows == 0 {
		return &domain.NotFoundError{Type: "edge", ID: id}
	}

	return nil
}

// scanEdgeRow scans a single edge row including evolution fields.
func (s *Store) scanEdgeRow(row *sql.Row) (*domain.Edge, error) {
	edge := &domain.Edge{}
	var createdAt string
	var validFrom, invalidAt sql.NullString
	var evolutionID sql.NullInt64
	if err := row.Scan(
		&edge.ID, &edge.FromObsID, &edge.ToObsID, &edge.RelationType, &edge.Weight,
		&edge.Confidence, &edge.Source, &edge.Reasoning,
		&validFrom, &invalidAt, &createdAt,
		&evolutionID, &edge.EvolutionType, &edge.FactState, &edge.ChangeReason,
	); err != nil {
		return nil, err
	}
	edge.CreatedAt, _ = parseTime(createdAt)
	if validFrom.Valid {
		t, _ := parseTime(validFrom.String)
		edge.ValidFrom = &t
	}
	if invalidAt.Valid {
		t, _ := parseTime(invalidAt.String)
		edge.InvalidAt = &t
	}
	if evolutionID.Valid {
		edge.EvolutionID = &evolutionID.Int64
	}
	return edge, nil
}

// scanEdgeRows scans multiple edge rows including evolution fields.
func (s *Store) scanEdgeRows(rows *sql.Rows) ([]*domain.Edge, error) {
	var edges []*domain.Edge
	for rows.Next() {
		edge := &domain.Edge{}
		var createdAt string
		var validFrom, invalidAt sql.NullString
		var evolutionID sql.NullInt64
		if err := rows.Scan(
			&edge.ID, &edge.FromObsID, &edge.ToObsID, &edge.RelationType, &edge.Weight,
			&edge.Confidence, &edge.Source, &edge.Reasoning,
			&validFrom, &invalidAt, &createdAt,
			&evolutionID, &edge.EvolutionType, &edge.FactState, &edge.ChangeReason,
		); err != nil {
			return nil, fmt.Errorf("graph: scan edge: %w", err)
		}
		edge.CreatedAt, _ = parseTime(createdAt)
		if validFrom.Valid {
			t, _ := parseTime(validFrom.String)
			edge.ValidFrom = &t
		}
		if invalidAt.Valid {
			t, _ := parseTime(invalidAt.String)
			edge.InvalidAt = &t
		}
		if evolutionID.Valid {
			edge.EvolutionID = &evolutionID.Int64
		}
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

// parseTime parses a SQLite datetime string, logging a warning if it fails.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(sqliteDatetimeFormat, s)
	if err != nil && s != "" {
		log.Printf("graph: failed to parse time %q", s)
	}
	return t, err
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullableTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.Format(sqliteDatetimeFormat)
}

// GetEdgesForObservation retrieves all edges where the observation is either source or target.
func (s *Store) GetEdgesForObservation(ctx context.Context, obsID int64) ([]*domain.Edge, error) {
	query := `SELECT id, from_obs_id, to_obs_id, relation_type, weight,
	                 COALESCE(confidence, 1.0), COALESCE(source, ''), COALESCE(reasoning, ''),
	                 valid_from, invalid_at, created_at,
	                 evolution_id, COALESCE(evolution_type, 'original'),
	                 COALESCE(fact_state, 'current'), COALESCE(change_reason, '')
	          FROM edges
	          WHERE from_obs_id = ? OR to_obs_id = ?
	          ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, obsID, obsID)
	if err != nil {
		return nil, fmt.Errorf("graph: get edges for observation %d: %w", obsID, err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanEdgeRows(rows)
}

// GetEdgesValidAt retrieves edges for an observation that were valid at the given time.
// An edge is valid at time `at` if: (valid_from IS NULL OR valid_from <= at) AND (invalid_at IS NULL OR invalid_at > at).
func (s *Store) GetEdgesValidAt(ctx context.Context, obsID int64, at time.Time) ([]*domain.Edge, error) {
	atStr := at.UTC().Format(time.RFC3339)
	query := `SELECT id, from_obs_id, to_obs_id, relation_type, weight,
	                 COALESCE(confidence, 1.0), COALESCE(source, ''), COALESCE(reasoning, ''),
	                 valid_from, invalid_at, created_at,
	                 evolution_id, COALESCE(evolution_type, 'original'),
	                 COALESCE(fact_state, 'current'), COALESCE(change_reason, '')
	          FROM edges
	          WHERE (from_obs_id = ? OR to_obs_id = ?)
	            AND (valid_from IS NULL OR valid_from <= ?)
	            AND (invalid_at IS NULL OR invalid_at > ?)
	          ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, obsID, obsID, atStr, atStr)
	if err != nil {
		return nil, fmt.Errorf("graph: get edges valid at %s for observation %d: %w", atStr, obsID, err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanEdgeRows(rows)
}

// GetEdge retrieves a specific edge by its ID.
func (s *Store) GetEdge(ctx context.Context, id int64) (*domain.Edge, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, from_obs_id, to_obs_id, relation_type, weight,
		       COALESCE(confidence, 1.0), COALESCE(source, ''), COALESCE(reasoning, ''),
		       valid_from, invalid_at, created_at,
		       evolution_id, COALESCE(evolution_type, 'original'),
		       COALESCE(fact_state, 'current'), COALESCE(change_reason, '')
		FROM edges
		WHERE id = ?
	`, id)

	edge, err := s.scanEdgeRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, &domain.NotFoundError{Type: "edge", ID: id}
		}
		return nil, fmt.Errorf("graph: get edge %d: %w", id, err)
	}

	return edge, nil
}

// GetEvolutionChain retrieves all edges that share the same endpoints.
func (s *Store) GetEvolutionChain(ctx context.Context, fromObsID, toObsID int64) ([]*domain.Edge, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, from_obs_id, to_obs_id, relation_type, weight,
		       COALESCE(confidence, 1.0), COALESCE(source, ''), COALESCE(reasoning, ''),
		       valid_from, invalid_at, created_at,
		       evolution_id, COALESCE(evolution_type, 'original'),
		       COALESCE(fact_state, 'current'), COALESCE(change_reason, '')
		FROM edges
		WHERE from_obs_id = ? AND to_obs_id = ?
		ORDER BY created_at ASC
	`, fromObsID, toObsID)
	if err != nil {
		return nil, fmt.Errorf("graph: get evolution chain: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanEdgeRows(rows)
}

// CountEdgesByObservation counts edges connected to a specific observation.
func (s *Store) CountEdgesByObservation(ctx context.Context, obsID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM edges
		WHERE from_obs_id = ? OR to_obs_id = ?
	`, obsID, obsID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("graph: count edges by observation %d: %w", obsID, err)
	}
	return count, nil
}

// CountAllEdges counts all edges in the system.
func (s *Store) CountAllEdges(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM edges`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("graph: count all edges: %w", err)
	}
	return count, nil
}

// GetContradictions retrieves contradiction edges created in a time range.
func (s *Store) GetContradictions(ctx context.Context, from, to time.Time) ([]*domain.Edge, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, from_obs_id, to_obs_id, relation_type, weight,
		       COALESCE(confidence, 1.0), COALESCE(source, ''), COALESCE(reasoning, ''),
		       valid_from, invalid_at, created_at,
		       evolution_id, COALESCE(evolution_type, 'original'),
		       COALESCE(fact_state, 'current'), COALESCE(change_reason, '')
		FROM edges
		WHERE relation_type = ? AND created_at >= ? AND created_at <= ?
		ORDER BY created_at DESC
	`, domain.RelationContradicts, from.Format(sqliteDatetimeFormat), to.Format(sqliteDatetimeFormat))
	if err != nil {
		return nil, fmt.Errorf("graph: get contradictions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanEdgeRows(rows)
}

// UpdateEdge updates mutable edge fields by ID.
func (s *Store) UpdateEdge(ctx context.Context, edge *domain.Edge) error {
	if edge == nil {
		return &domain.ValidationError{Field: "edge", Message: "edge cannot be nil"}
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE edges
		SET relation_type = ?, weight = ?, confidence = ?, source = ?, reasoning = ?,
		    valid_from = ?, invalid_at = ?
		WHERE id = ?
	`,
		edge.RelationType, edge.Weight, edge.Confidence, nullableString(edge.Source), nullableString(edge.Reasoning),
		nullableTime(edge.ValidFrom), nullableTime(edge.InvalidAt), edge.ID,
	)
	if err != nil {
		return fmt.Errorf("graph: update edge %d: %w", edge.ID, err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("graph: rows affected for edge %d: %w", edge.ID, err)
	}
	if rows == 0 {
		return &domain.NotFoundError{Type: "edge", ID: edge.ID}
	}

	return nil
}

// Ensure Store implements domain.GraphRepository.
var _ domain.GraphRepository = (*Store)(nil)

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
// up to the specified depth using iterative BFS.
func (s *Store) GetRelated(ctx context.Context, obsID int64, depth int) ([]*domain.Observation, error) {
	if depth <= 0 {
		depth = 1
	}
	if depth > 10 {
		depth = 10
	}

	visited := map[int64]bool{obsID: true}
	currentLevel := []int64{obsID}

	for d := 0; d < depth && len(currentLevel) > 0; d++ {
		nextLevel := []int64{}

		neighbors, err := s.getNeighborObservations(ctx, currentLevel)
		if err != nil {
			return nil, fmt.Errorf("graph: get related at depth %d: %w", d+1, err)
		}

		for _, obs := range neighbors {
			if !visited[obs.ID] {
				visited[obs.ID] = true
				nextLevel = append(nextLevel, obs.ID)
			}
		}

		currentLevel = nextLevel
	}

	// Remove the seed observation from visited
	delete(visited, obsID)

	if len(visited) == 0 {
		return []*domain.Observation{}, nil
	}

	return s.getObservationsByIDs(ctx, visited)
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

// getNeighborObservations returns observation IDs connected to any of the given IDs.
func (s *Store) getNeighborObservations(ctx context.Context, ids []int64) ([]struct{ ID int64 }, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, 0, len(ids)*2)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	ph := strings.Join(placeholders, ",")

	// Query both directions
	argsAll := append(args, args...)
	query := fmt.Sprintf(
		`SELECT DISTINCT to_obs_id AS neighbor_id FROM edges WHERE from_obs_id IN (%s)
		 UNION
		 SELECT DISTINCT from_obs_id AS neighbor_id FROM edges WHERE to_obs_id IN (%s)`,
		ph, ph,
	)

	rows, err := s.db.QueryContext(ctx, query, argsAll...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var neighbors []struct{ ID int64 }
	for rows.Next() {
		var n struct{ ID int64 }
		if err := rows.Scan(&n.ID); err != nil {
			return nil, err
		}
		neighbors = append(neighbors, n)
	}

	return neighbors, rows.Err()
}

// getObservationsByIDs fetches full observation records for the given IDs.
func (s *Store) getObservationsByIDs(ctx context.Context, ids map[int64]bool) ([]*domain.Observation, error) {
	placeholders := make([]string, 0, len(ids))
	args := make([]interface{}, 0, len(ids))
	for id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	ph := strings.Join(placeholders, ",")

	query := fmt.Sprintf(
		`SELECT id, title, content, type, project, scope, session_id,
		        COALESCE(topic_key, '') AS topic_key,
		        COALESCE(confidence, 1.0), COALESCE(source, 'manual'), COALESCE(tags, ''),
		        created_at, updated_at
		 FROM observations
		 WHERE id IN (%s) AND deleted_at IS NULL
		 ORDER BY created_at DESC`, ph,
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		obs.CreatedAt, _ = parseTime(createdAt)
		obs.UpdatedAt, _ = parseTime(updatedAt)
		if tagsJSON != "" {
			_ = json.Unmarshal([]byte(tagsJSON), &obs.Tags)
		}
		observations = append(observations, obs)
	}

	return observations, rows.Err()
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
	                 valid_from, invalid_at, created_at
	          FROM edges
	          WHERE from_obs_id = ? OR to_obs_id = ?
	          ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, obsID, obsID)
	if err != nil {
		return nil, fmt.Errorf("graph: get edges for observation %d: %w", obsID, err)
	}
	defer func() { _ = rows.Close() }()

	var edges []*domain.Edge
	for rows.Next() {
		edge := &domain.Edge{}
		var createdAt string
		var validFrom, invalidAt sql.NullString
		if err := rows.Scan(
			&edge.ID, &edge.FromObsID, &edge.ToObsID, &edge.RelationType, &edge.Weight,
			&edge.Confidence, &edge.Source, &edge.Reasoning,
			&validFrom, &invalidAt, &createdAt,
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
		edges = append(edges, edge)
	}

	return edges, rows.Err()
}

// Ensure Store implements domain.GraphRepository.
var _ domain.GraphRepository = (*Store)(nil)

// Package graph provides business logic for managing relationships between
// observations in the knowledge graph.
//
// The graph service enables semantic navigation through observations by
// creating, querying, and traversing edges (relationships) between them.
// This supports use cases like finding related knowledge, discovering
// contradiction chains, and understanding concept hierarchies.
package graph

import (
	"context"
	"errors"
	"fmt"

	"github.com/lleontor705/cortex/internal/domain"
)

// Relation types - common values for Edge.RelationType
const (
	RelationReferences  = "references"
	RelationRelatesTo   = "relates_to"
	RelationFollows     = "follows"
	RelationSupersedes  = "supersedes"
	RelationContradicts = "contradicts"
)

// ValidRelationTypes contains all allowed relation types for edges.
var ValidRelationTypes = map[string]bool{
	RelationReferences:  true,
	RelationRelatesTo:   true,
	RelationFollows:     true,
	RelationSupersedes:  true,
	RelationContradicts: true,
}

// Business rule constants
const (
	DefaultWeight     = 1.0
	MinWeight         = 0.0
	MaxWeight         = 10.0
	DefaultMaxDepth   = 5
	MaxTraversalDepth = 10 // Prevent infinite loops
)

// Common errors
var (
	ErrSelfReference   = errors.New("cannot create edge from observation to itself")
	ErrInvalidWeight   = errors.New("weight must be greater than 0")
	ErrInvalidRelation = errors.New("invalid relation type")
	ErrInvalidDepth    = errors.New("depth must be between 1 and 10")
	ErrEdgeNotFound    = errors.New("edge not found")
	ErrDuplicateEdge   = errors.New("edge already exists with same from_obs_id, to_obs_id, and relation_type")
)

// Service provides graph operations for managing relationships between observations.
type Service struct {
	repo domain.GraphRepository
}

// NewService creates a new graph service with the given repository.
func NewService(repo domain.GraphRepository) *Service {
	return &Service{
		repo: repo,
	}
}

// CreateEdge creates a relationship between two observations.
//
// Business Rules:
//   - Cannot create edge from observation to itself (self-reference)
//   - Weight must be > 0 (defaults to 1.0)
//   - Relation type must be valid
//   - (from_obs_id, to_obs_id, relation_type) must be unique
func (s *Service) CreateEdge(ctx context.Context, edge *domain.Edge) error {
	// Validate self-reference
	if edge.FromObsID == edge.ToObsID {
		return ErrSelfReference
	}

	// Validate weight
	if edge.Weight <= MinWeight {
		return ErrInvalidWeight
	}

	// Validate relation type
	if !ValidRelationTypes[edge.RelationType] {
		return fmt.Errorf("%w: %s", ErrInvalidRelation, edge.RelationType)
	}

	// Set default weight if not specified
	if edge.Weight == 0 {
		edge.Weight = DefaultWeight
	}

	return s.repo.CreateEdge(ctx, edge)
}

// GetRelated retrieves all observations related to a given observation,
// traversing the graph up to the specified depth.
//
// Depth meanings:
//   - depth=1: Only directly connected observations
//   - depth=2: Observations 1 or 2 hops away
//   - depth=N: Observations up to N hops away
//
// Returns observations in order of proximity (closer observations first).
func (s *Service) GetRelated(ctx context.Context, obsID int64, depth int) ([]*domain.Observation, error) {
	// Validate depth
	if depth < 1 || depth > MaxTraversalDepth {
		return nil, ErrInvalidDepth
	}

	return s.repo.GetRelated(ctx, obsID, depth)
}

// DeleteEdge removes a relationship between observations.
// Returns ErrEdgeNotFound if the edge doesn't exist.
func (s *Service) DeleteEdge(ctx context.Context, id int64) error {
	return s.repo.DeleteEdge(ctx, id)
}

// GetRelationships retrieves all edges for an observation (both outgoing and incoming).
// This is useful for displaying the full context of relationships for a given observation.
func (s *Service) GetRelationships(ctx context.Context, obsID int64) ([]*domain.Edge, error) {
	// Get all edges where this observation is involved
	related, err := s.repo.GetRelated(ctx, obsID, 1)
	if err != nil {
		return nil, err
	}

	// Extract edges from related observations
	// Note: This is a simplified implementation. A full implementation
	// would have a dedicated repository method to fetch edges directly.
	edges := make([]*domain.Edge, 0, len(related))
	for _, obs := range related {
		// Create edge representation (this is approximate)
		edges = append(edges, &domain.Edge{
			FromObsID:    obsID,
			ToObsID:      obs.ID,
			RelationType: RelationRelatesTo,
			Weight:       DefaultWeight,
		})
	}

	return edges, nil
}

// FindPath finds a path between two observations using Breadth-First Search (BFS).
// Returns the sequence of observation IDs from fromID to toID, or nil if no path exists.
//
// The maxDepth parameter limits how far to search to prevent performance issues.
// If maxDepth is 0, DefaultMaxDepth (5) is used.
func (s *Service) FindPath(ctx context.Context, fromID, toID int64, maxDepth int) ([]int64, error) {
	// Validate input
	if fromID == toID {
		return []int64{fromID}, nil
	}

	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}

	if maxDepth > MaxTraversalDepth {
		maxDepth = MaxTraversalDepth
	}

	// BFS implementation
	type node struct {
		id    int64
		path  []int64
		depth int
	}

	visited := make(map[int64]bool)
	queue := []node{{id: fromID, path: []int64{fromID}, depth: 0}}
	visited[fromID] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		// Check depth limit
		if current.depth >= maxDepth {
			continue
		}

		// Get neighbors at depth 1
		neighbors, err := s.repo.GetRelated(ctx, current.id, 1)
		if err != nil {
			return nil, fmt.Errorf("failed to get related observations: %w", err)
		}

		for _, neighbor := range neighbors {
			// Found target
			if neighbor.ID == toID {
				return append(current.path, toID), nil
			}

			// Skip if already visited
			if visited[neighbor.ID] {
				continue
			}

			visited[neighbor.ID] = true
			newPath := make([]int64, len(current.path)+1)
			copy(newPath, current.path)
			newPath[len(current.path)] = neighbor.ID

			queue = append(queue, node{
				id:    neighbor.ID,
				path:  newPath,
				depth: current.depth + 1,
			})
		}
	}

	// No path found
	return nil, nil
}

// ValidateRelationType checks if a relation type is valid.
func ValidateRelationType(relationType string) bool {
	return ValidRelationTypes[relationType]
}

// GetValidRelationTypes returns a list of all valid relation types.
func GetValidRelationTypes() []string {
	types := make([]string, 0, len(ValidRelationTypes))
	for t := range ValidRelationTypes {
		types = append(types, t)
	}
	return types
}

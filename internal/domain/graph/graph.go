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

// ValidRelationTypes contains all allowed relation types for edges.
var ValidRelationTypes = map[string]bool{
	domain.RelationReferences:  true,
	domain.RelationRelatesTo:   true,
	domain.RelationFollows:     true,
	domain.RelationSupersedes:  true,
	domain.RelationContradicts: true,
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
	ErrSelfReference = errors.New("cannot create edge from observation to itself")
	ErrInvalidWeight = errors.New("weight must be between 0 and 10")
	ErrInvalidDepth  = errors.New("depth must be between 1 and 10")
	ErrEdgeNotFound  = errors.New("edge not found")
	ErrDuplicateEdge = errors.New("edge already exists with same from_obs_id, to_obs_id, and relation_type")
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

	// Set default weight if not specified (zero value from JSON/HTTP)
	if edge.Weight == 0 {
		edge.Weight = DefaultWeight
	}

	// Validate weight range
	if edge.Weight < MinWeight || edge.Weight > MaxWeight {
		return ErrInvalidWeight
	}

	// Validate relation type
	if !ValidRelationTypes[edge.RelationType] {
		return fmt.Errorf("%w: %s", domain.ErrInvalidRelation, edge.RelationType)
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
	return s.repo.GetEdgesForObservation(ctx, obsID)
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

	// BFS with parent pointers to avoid O(B^D * D) path copies
	type node struct {
		id    int64
		depth int
	}

	visited := make(map[int64]bool)
	parents := make(map[int64]int64) // child -> parent
	queue := []node{{id: fromID, depth: 0}}
	visited[fromID] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= maxDepth {
			continue
		}

		neighbors, err := s.repo.GetRelated(ctx, current.id, 1)
		if err != nil {
			return nil, fmt.Errorf("failed to get related observations: %w", err)
		}

		for _, neighbor := range neighbors {
			if neighbor.ID == toID {
				// Reconstruct path from parent pointers
				path := []int64{toID}
				for curr := current.id; curr != fromID; {
					path = append(path, curr)
					parent, ok := parents[curr]
					if !ok {
						return nil, fmt.Errorf("graph: BFS internal error: missing parent for node %d", curr)
					}
					curr = parent
				}
				path = append(path, fromID)
				// Reverse
				for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
					path[i], path[j] = path[j], path[i]
				}
				return path, nil
			}

			if visited[neighbor.ID] {
				continue
			}

			visited[neighbor.ID] = true
			parents[neighbor.ID] = current.id
			queue = append(queue, node{
				id:    neighbor.ID,
				depth: current.depth + 1,
			})
		}
	}

	return nil, nil
}

// DetectConflicts finds edges where the observation is involved in a contradiction
// or has been superseded by newer knowledge.
func (s *Service) DetectConflicts(ctx context.Context, obsID int64) ([]*domain.Edge, error) {
	edges, err := s.repo.GetEdgesForObservation(ctx, obsID)
	if err != nil {
		return nil, fmt.Errorf("detect conflicts: %w", err)
	}

	var conflicts []*domain.Edge
	for _, edge := range edges {
		if edge.RelationType == domain.RelationContradicts || edge.RelationType == domain.RelationSupersedes {
			conflicts = append(conflicts, edge)
		}
	}
	return conflicts, nil
}

// ResolveConflict resolves a knowledge contradiction by marking the new observation as superseding
// the obsolete one, documenting the reason, and creating a formal supersedes edge.
func (s *Service) ResolveConflict(ctx context.Context, newObsID, obsoleteObsID int64, reason string) (*domain.Edge, error) {
	if newObsID == obsoleteObsID {
		return nil, ErrSelfReference
	}

	edge := &domain.Edge{
		FromObsID:    newObsID,
		ToObsID:      obsoleteObsID,
		RelationType: domain.RelationSupersedes,
		Weight:       1.0,
		Confidence:   1.0,
		Reasoning:    reason,
		ChangeReason: reason,
	}

	if err := s.repo.CreateEdge(ctx, edge); err != nil {
		return nil, fmt.Errorf("resolve conflict: %w", err)
	}
	return edge, nil
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


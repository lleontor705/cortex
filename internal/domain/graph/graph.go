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
	"sort"

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

	// DefaultMaxVisited is the default global admission budget for bounded
	// traversal; it counts the root plus every unique admitted node.
	DefaultMaxVisited = 1000
	// MaxVisitedCap bounds the user-supplied max_visited budget.
	MaxVisitedCap = 10000
	// DefaultMaxResults is the default emitted-row budget for bounded
	// traversal; it counts only emitted unique non-root observations.
	DefaultMaxResults = 100
	// MaxResultsCap bounds the user-supplied max_results budget.
	MaxResultsCap = 1000
)

// Common errors
var (
	ErrSelfReference = errors.New("cannot create edge from observation to itself")
	ErrInvalidWeight = errors.New("weight must be between 0 and 10")
	ErrInvalidDepth  = errors.New("depth must be between 1 and 10")
	ErrEdgeNotFound  = errors.New("edge not found")
	ErrDuplicateEdge = errors.New("edge already exists with same from_obs_id, to_obs_id, and relation_type")
	// ErrTraversalTruncated is the stable resource-limit error returned when
	// the max_visited budget is exhausted while eligible nodes remain and the
	// existence of a path has been neither proved nor disproved (GRAPH-01).
	// It is never returned for a proven no-path result.
	ErrTraversalTruncated = errors.New("graph: traversal truncated: max_visited budget exhausted")
)

// LevelNeighborBatcher is the optional repository capability (GRAPH-01) that
// resolves one-hop adjacency for an entire BFS frontier in a single lookup.
// Implementations MUST return hydrated neighbor observations for every
// requested frontier ID (missing IDs may map to an empty or absent entry) and
// SHOULD deduplicate and order each adjacency list by ascending observation
// ID; the service normalizes ordering defensively so shuffled rows cannot
// change traversal outcomes.
type LevelNeighborBatcher interface {
	GetLevelNeighborObservations(ctx context.Context, frontier []int64) (map[int64][]*domain.Observation, error)
}

// normalizeMaxVisited clamps the admission budget to its default and cap.
func normalizeMaxVisited(v int) int {
	if v <= 0 {
		return DefaultMaxVisited
	}
	if v > MaxVisitedCap {
		return MaxVisitedCap
	}
	return v
}

// normalizeMaxResults clamps the emitted-row budget to its default and cap.
func normalizeMaxResults(v int) int {
	if v <= 0 {
		return DefaultMaxResults
	}
	if v > MaxResultsCap {
		return MaxResultsCap
	}
	return v
}

// fetchAdjacency resolves one-hop adjacency for the frontier, using the batch
// capability when the repository provides it and a bounded deterministic
// per-node fallback otherwise.
func (s *Service) fetchAdjacency(ctx context.Context, frontier []int64) (map[int64][]*domain.Observation, error) {
	var adj map[int64][]*domain.Observation
	if batcher, ok := s.repo.(LevelNeighborBatcher); ok {
		got, err := batcher.GetLevelNeighborObservations(ctx, frontier)
		if err != nil {
			return nil, fmt.Errorf("failed to get related observations: %w", err)
		}
		adj = got
	} else {
		adj = make(map[int64][]*domain.Observation, len(frontier))
		for _, id := range frontier {
			obs, err := s.repo.GetRelated(ctx, id, 1)
			if err != nil {
				return nil, fmt.Errorf("failed to get related observations: %w", err)
			}
			adj[id] = obs
		}
	}
	// Defensive determinism: deduplicate and sort every adjacency list by
	// ascending observation ID so shuffled rows cannot change outcomes.
	for id, list := range adj {
		seen := make(map[int64]bool, len(list))
		deduped := list[:0]
		for _, o := range list {
			if o == nil || seen[o.ID] {
				continue
			}
			seen[o.ID] = true
			deduped = append(deduped, o)
		}
		sort.Slice(deduped, func(i, j int) bool { return deduped[i].ID < deduped[j].ID })
		adj[id] = deduped
	}
	return adj, nil
}

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
// If maxDepth is 0, DefaultMaxDepth (5) is used. FindPath uses the default
// max_visited budget; use FindPathBounded for an explicit budget.
func (s *Service) FindPath(ctx context.Context, fromID, toID int64, maxDepth int) ([]int64, error) {
	return s.FindPathBounded(ctx, fromID, toID, maxDepth, 0)
}

// FindPathBounded finds the lexicographically smallest shortest path between
// two observations using level-batched BFS (GRAPH-01).
//
// Each BFS frontier and every neighbor list is processed in ascending
// observation ID order, and at most one adjacency lookup is issued per
// expanded level through the optional LevelNeighborBatcher capability
// (repositories without it fall back to a bounded deterministic per-node
// lookup). maxDepth defaults to DefaultMaxDepth and is capped at
// MaxTraversalDepth. maxVisited defaults to DefaultMaxVisited, is capped at
// MaxVisitedCap, and counts the root plus every unique admitted node
// including the destination. When the budget is exhausted while eligible
// nodes remain and the path has been neither proved nor disproved, the call
// returns ErrTraversalTruncated instead of a false no-path result.
func (s *Service) FindPathBounded(ctx context.Context, fromID, toID int64, maxDepth, maxVisited int) ([]int64, error) {
	if fromID == toID {
		return []int64{fromID}, nil
	}

	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	if maxDepth > MaxTraversalDepth {
		maxDepth = MaxTraversalDepth
	}
	maxVisited = normalizeMaxVisited(maxVisited)

	// Level-batched BFS with a global admission budget. visited maps each
	// admitted node to its BFS hop; levels[k] holds the nodes admitted at
	// hop k; adj caches the (normalized ascending) adjacency of every
	// expanded node for path reconstruction.
	visited := map[int64]int{fromID: 0}
	levels := [][]int64{{fromID}}
	adj := map[int64][]*domain.Observation{}
	admitted := 1
	frontier := []int64{fromID}
	found := false

	for level := 0; level < maxDepth && len(frontier) > 0 && !found; level++ {
		levelAdj, err := s.fetchAdjacency(ctx, frontier)
		if err != nil {
			return nil, err
		}
		for id, list := range levelAdj {
			adj[id] = list
		}

		var next []int64
		blocked := false
	scan:
		for _, u := range sortedIDs(frontier) {
			for _, o := range adj[u] {
				v := o.ID
				if _, seen := visited[v]; seen {
					continue
				}
				if v == toID {
					// The endpoint completes the proof; it must be admittable
					// within the budget, otherwise the answer is truncation.
					if admitted >= maxVisited {
						return nil, ErrTraversalTruncated
					}
					visited[v] = level + 1
					admitted++
					next = append(next, v)
					found = true
					break scan
				}
				if admitted >= maxVisited {
					blocked = true
					continue
				}
				visited[v] = level + 1
				admitted++
				next = append(next, v)
			}
		}
		next = sortedIDs(next)
		levels = append(levels, next)
		frontier = next
		if !found && blocked && level+1 < maxDepth {
			// Eligible nodes were omitted by the budget and would still be
			// expandable: the absence of a path cannot be proved.
			return nil, ErrTraversalTruncated
		}
	}

	if !found {
		return nil, nil
	}

	// Reconstruct the lexicographically smallest shortest path. First mark,
	// backward over the BFS DAG, every node that lies on some shortest path
	// (visited hop k+1 neighbor already marked). Then walk forward from the
	// root choosing the smallest-ID continuation, which is the first match in
	// the ascending adjacency list.
	L := len(levels) - 1
	onPath := make(map[int64]bool, L+1)
	onPath[toID] = true
	for k := L - 1; k >= 1; k-- {
		for _, u := range levels[k] {
			for _, o := range adj[u] {
				if hop, ok := visited[o.ID]; ok && hop == k+1 && onPath[o.ID] {
					onPath[u] = true
					break
				}
			}
		}
	}

	path := []int64{fromID}
	cur := fromID
	for cur != toID {
		var nextID int64
		for _, o := range adj[cur] {
			if hop, ok := visited[o.ID]; ok && hop == visited[cur]+1 && onPath[o.ID] {
				nextID = o.ID
				break
			}
		}
		if nextID == 0 {
			return nil, fmt.Errorf("graph: BFS internal error: no marked continuation from node %d", cur)
		}
		path = append(path, nextID)
		cur = nextID
	}
	return path, nil
}

// sortedIDs returns a copy of ids sorted ascending.
func sortedIDs(ids []int64) []int64 {
	out := make([]int64, len(ids))
	copy(out, ids)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// GetRelatedBounded performs bounded local traversal (GRAPH-02) with
// independent max_visited and max_results budgets.
//
// max_visited (default DefaultMaxVisited, cap MaxVisitedCap) counts the root
// plus unique admitted nodes; max_results (default DefaultMaxResults, cap
// MaxResultsCap) counts only emitted unique non-root observations. Rows are
// ordered by minimum hop ascending, then observation ID ascending, regardless
// of adjacency row order. The traversal probes one sentinel beyond each
// effective limit: truncated is reported (with reason max_visited,
// max_results, or both) ONLY when the sentinel proved eligible data was
// omitted. A result exactly equal to a limit is complete, not truncated.
// Legacy (non-v2) repositories obey the same semantics through the per-node
// fallback.
func (s *Service) GetRelatedBounded(ctx context.Context, obsID int64, opts domain.GraphTraversalOptions) (*domain.GraphTraversalResult, error) {
	depth := opts.Depth
	if depth < 1 || depth > MaxTraversalDepth {
		return nil, ErrInvalidDepth
	}
	maxVisited := normalizeMaxVisited(opts.MaxVisited)
	maxResults := normalizeMaxResults(opts.MaxResults)
	// One-past-the-limit sentinel probes: admitting/emitting the sentinel
	// proves eligible data exists beyond the limit; the sentinel is dropped.
	probeVisited := maxVisited + 1
	probeResults := maxResults + 1

	visited := map[int64]bool{obsID: true}
	admitted := 1
	frontier := []int64{obsID}
	emitted := make([]*domain.Observation, 0, maxResults)
	truncatedVisited := false
	truncatedResults := false

	for level := 0; level < depth && len(frontier) > 0 && !truncatedVisited && !truncatedResults; level++ {
		levelAdj, err := s.fetchAdjacency(ctx, frontier)
		if err != nil {
			return nil, err
		}

		var next []int64
		var levelObs []*domain.Observation
	scan:
		for _, u := range sortedIDs(frontier) {
			for _, o := range levelAdj[u] {
				v := o.ID
				if visited[v] {
					continue
				}
				visited[v] = true
				admitted++
				if admitted == probeVisited {
					// Sentinel node: evidence of omission, dropped from output.
					truncatedVisited = true
					break scan
				}
				next = append(next, v)
				levelObs = append(levelObs, o)
			}
		}

		// Emit the level's admitted nodes in ascending ID order (levels are
		// processed in hop order, so the global order is hop then ID).
		sort.Slice(levelObs, func(i, j int) bool { return levelObs[i].ID < levelObs[j].ID })
		for _, o := range levelObs {
			emitted = append(emitted, o)
			if len(emitted) == probeResults {
				// Sentinel row: evidence of omission, dropped from output.
				truncatedResults = true
				emitted = emitted[:len(emitted)-1]
				break
			}
		}

		frontier = next
	}

	var reasons []string
	if truncatedVisited {
		reasons = append(reasons, domain.TruncationReasonMaxVisited)
	}
	if truncatedResults {
		reasons = append(reasons, domain.TruncationReasonMaxResults)
	}
	return &domain.GraphTraversalResult{
		Observations:      emitted,
		Truncated:         len(reasons) > 0,
		TruncationReasons: reasons,
	}, nil
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

// Package temporal provides temporal graph semantics and evolution tracking
// for the knowledge graph in Cortex.
//
// This package extends the basic graph functionality with temporal awareness,
// enabling tracking of how facts evolve over time, handling temporal validity,
// and providing temporal reasoning capabilities similar to Zep's temporal knowledge graph.
package temporal

import (
	"context"
	"fmt"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// TemporalService provides enhanced temporal graph functionality.
type TemporalService struct {
	graphRepo       domain.GraphRepository
	observationRepo domain.ObservationRepository
	snapshotRepo    domain.TemporalSnapshotRepository
	metricsRepo     domain.MetricsRepository
}

// NewTemporalService creates a new temporal graph service.
func NewTemporalService(
	graphRepo domain.GraphRepository,
	observationRepo domain.ObservationRepository,
	snapshotRepo domain.TemporalSnapshotRepository,
	metricsRepo domain.MetricsRepository,
) *TemporalService {
	return &TemporalService{
		graphRepo:       graphRepo,
		observationRepo: observationRepo,
		snapshotRepo:    snapshotRepo,
		metricsRepo:     metricsRepo,
	}
}

// CreateTemporalEdge creates an edge with temporal validity and evolution tracking.
func (s *TemporalService) CreateTemporalEdge(ctx context.Context, edge *domain.Edge) error {
	now := time.Now()

	// Set default temporal validity if not specified
	if edge.ValidFrom == nil {
		edge.ValidFrom = &now
	}

	// Validate temporal constraints
	if edge.InvalidAt != nil && edge.ValidFrom != nil && edge.InvalidAt.Before(*edge.ValidFrom) {
		return fmt.Errorf("invalid_at must be after valid_from")
	}

	// Set initial evolution state
	if edge.EvolutionType == "" {
		edge.EvolutionType = domain.EvolutionOriginal
	}

	// Set initial fact state
	if edge.FactState == "" {
		edge.FactState = domain.FactStateCurrent
	}

	return s.graphRepo.CreateEdge(ctx, edge)
}

// GetTemporalEdges retrieves edges that are valid at a specific time point.
func (s *TemporalService) GetTemporalEdges(ctx context.Context, obsID int64, at time.Time) ([]*domain.Edge, error) {
	allEdges, err := s.graphRepo.GetEdgesForObservation(ctx, obsID)
	if err != nil {
		return nil, err
	}

	var validEdges []*domain.Edge
	for _, edge := range allEdges {
		if s.isValidAtTime(edge, at) {
			validEdges = append(validEdges, edge)
		}
	}

	return validEdges, nil
}

// GetEvolutionPath retrieves the evolution history of an edge.
func (s *TemporalService) GetEvolutionPath(ctx context.Context, edgeID int64) ([]*domain.Edge, error) {
	// Get the base edge
	edge, err := s.graphRepo.GetEdge(ctx, edgeID)
	if err != nil {
		return nil, err
	}

	// Get all edges that share the same evolution chain
	allEdges, err := s.graphRepo.GetEvolutionChain(ctx, edge.FromObsID, edge.ToObsID)
	if err != nil {
		return nil, err
	}

	// Filter and return evolution path in chronological order
	var evolutionPath []*domain.Edge
	for _, e := range allEdges {
		if e.EvolutionID != nil && edge.EvolutionID != nil && *e.EvolutionID == *edge.EvolutionID {
			evolutionPath = append(evolutionPath, e)
		}
	}

	return evolutionPath, nil
}

// GetCurrentFactState determines the current state of facts related to an observation.
func (s *TemporalService) GetCurrentFactState(ctx context.Context, obsID int64) (map[string]*domain.Edge, error) {
	now := time.Now()

	allEdges, err := s.graphRepo.GetEdgesForObservation(ctx, obsID)
	if err != nil {
		return nil, err
	}

	currentFacts := make(map[string]*domain.Edge)
	for _, edge := range allEdges {
		if s.isValidAtTime(edge, now) && edge.FactState == domain.FactStateCurrent {
			currentFacts[fmt.Sprintf("%s_%d", edge.RelationType, edge.ToObsID)] = edge
		}
	}

	return currentFacts, nil
}

// HandleTemporalContradiction handles contradictions between facts with temporal reasoning.
func (s *TemporalService) HandleTemporalContradiction(ctx context.Context, factA, factB *domain.Edge) (*domain.Edge, error) {
	now := time.Now()

	// Determine which fact is more current/reliable
	olderFact, newerFact := s.getOlderFact(factA, factB)

	// Mark the older fact as contradicted
	contradictionEdge := &domain.Edge{
		FromObsID:     olderFact.FromObsID,
		ToObsID:       olderFact.ToObsID,
		RelationType:  domain.RelationContradicts,
		Weight:        olderFact.Weight,
		Confidence:    0.9, // High confidence in contradiction
		Source:        "system",
		Reasoning:     fmt.Sprintf("Contradicted by fact %d at %s", newerFact.ID, now.Format(time.RFC3339)),
		ValidFrom:     &now,
		EvolutionType: domain.EvolutionContradicted,
		FactState:     domain.FactStateDeprecated,
	}

	// Create the contradiction edge
	if err := s.graphRepo.CreateEdge(ctx, contradictionEdge); err != nil {
		return nil, err
	}

	// Mark the original fact as superseded
	now = time.Now()
	supersededEdge := &domain.Edge{
		FromObsID:     newerFact.FromObsID,
		ToObsID:       newerFact.ToObsID,
		RelationType:  newerFact.RelationType,
		Weight:        newerFact.Weight,
		Confidence:    newerFact.Confidence,
		Source:        "system",
		Reasoning:     fmt.Sprintf("Superseded due to contradiction at %s", now.Format(time.RFC3339)),
		ValidFrom:     &now,
		EvolutionType: domain.EvolutionSuperseded,
		FactState:     domain.FactStateCurrent,
	}

	return supersededEdge, s.graphRepo.UpdateEdge(ctx, supersededEdge)
}

// CreateTemporalSnapshot creates a point-in-time snapshot of the knowledge graph.
func (s *TemporalService) CreateTemporalSnapshot(ctx context.Context, snapshotKey string, rootObsID int64, description string) (*domain.TemporalSnapshot, error) {
	now := time.Now()

	// Count observations and edges related to root observation
	obsCount, err := s.observationRepo.CountByRoot(ctx, rootObsID)
	if err != nil {
		return nil, err
	}

	edgeCount, err := s.graphRepo.CountEdgesByObservation(ctx, rootObsID)
	if err != nil {
		return nil, err
	}

	snapshot := &domain.TemporalSnapshot{
		SnapshotKey:       snapshotKey,
		Timestamp:         now,
		Description:       description,
		ObservationCount:  obsCount,
		EdgeCount:         edgeCount,
		RootObservationID: rootObsID,
	}

	if err := s.snapshotRepo.CreateSnapshot(ctx, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// GetTemporalRelevant retrieves observations relevant at a specific time point,
// respecting temporal validity and fact evolution.
func (s *TemporalService) GetTemporalRelevant(ctx context.Context, obsID int64, at time.Time, depth int) ([]*domain.Observation, error) {
	// Get edges valid at the specified time
	if _, err := s.GetTemporalEdges(ctx, obsID, at); err != nil {
		return nil, err
	}

	// Build knowledge graph from temporal edges
	relatedObs := make([]*domain.Observation, 0)
	visited := make(map[int64]bool)

	var traverse func(currentID int64, currentDepth int)
	traverse = func(currentID int64, currentDepth int) {
		if currentDepth > depth || visited[currentID] {
			return
		}

		visited[currentID] = true

		// Get observation
		obs, err := s.observationRepo.GetByID(ctx, currentID)
		if err == nil {
			relatedObs = append(relatedObs, obs)
		}

		// Get temporal edges from this observation
		edges, err := s.GetTemporalEdges(ctx, currentID, at)
		if err == nil {
			for _, edge := range edges {
				traverse(edge.ToObsID, currentDepth+1)
			}
		}
	}

	// Start traversal from the root observation
	traverse(obsID, 0)

	return relatedObs, nil
}

// isValidAtTime checks if an edge is valid at a specific time point.
func (s *TemporalService) isValidAtTime(edge *domain.Edge, at time.Time) bool {
	// Check if edge is valid from the specified time
	if edge.ValidFrom != nil && at.Before(*edge.ValidFrom) {
		return false
	}

	// Check if edge is still valid (no invalidation time)
	if edge.InvalidAt == nil {
		return true
	}

	return !at.After(*edge.InvalidAt)
}

// getOlderFact determines which of two facts is older based on creation time.
func (s *TemporalService) getOlderFact(factA, factB *domain.Edge) (older, newer *domain.Edge) {
	if factA.CreatedAt.Before(factB.CreatedAt) {
		return factA, factB
	}
	return factB, factA
}

// GetTemporalMetrics retrieves metrics related to temporal operations.
func (s *TemporalService) GetTemporalMetrics(ctx context.Context, sessionID string, from, to time.Time) ([]*domain.Metrics, error) {
	return s.metricsRepo.GetTemporalMetrics(ctx, sessionID, from, to)
}

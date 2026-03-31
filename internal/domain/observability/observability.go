// Package observability provides metrics and quality evaluation for the Cortex memory system.
//
// This package implements observability features similar to Mem0, including performance metrics,
// memory quality evaluation, and continuous monitoring of the memory system.
package observability

import (
	"context"
	"fmt"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// ObservabilityService manages metrics collection and quality evaluation.
type ObservabilityService struct {
	metricsRepo     domain.MetricsRepository
	qualityRepo    domain.QualityMetricsRepository
	temporalRepo    domain.TemporalSnapshotRepository
	graphRepo      domain.GraphRepository
	observationRepo domain.ObservationRepository
}

// NewObservabilityService creates a new observability service.
func NewObservabilityService(
	metricsRepo domain.MetricsRepository,
	qualityRepo domain.QualityMetricsRepository,
	temporalRepo domain.TemporalSnapshotRepository,
	graphRepo domain.GraphRepository,
	observationRepo domain.ObservationRepository,
) *ObservabilityService {
	return &ObservabilityService{
		metricsRepo:     metricsRepo,
		qualityRepo:    qualityRepo,
		temporalRepo:    temporalRepo,
		graphRepo:      graphRepo,
		observationRepo: observationRepo,
	}
}

// RecordOperation records an operation with performance metrics.
func (s *ObservabilityService) RecordOperation(ctx context.Context, operation *domain.Metrics) error {
	return s.metricsRepo.CreateMetric(ctx, operation)
}

// GetSystemMetrics retrieves system-wide performance metrics.
func (s *ObservabilityService) GetSystemMetrics(ctx context.Context, sessionID string, from, to time.Time) (*domain.SystemMetrics, error) {
	// Get operation metrics
	operations, err := s.metricsRepo.GetTemporalMetrics(ctx, sessionID, from, to)
	if err != nil {
		return nil, err
	}
	
	// Calculate aggregated metrics
	var totalDuration, totalMemory int64
	var successCount, totalCount int
	var totalObservations, totalEdges int
	var totalComplexity, totalConfidence float64
	
	for _, op := range operations {
		totalDuration += op.Duration
		totalMemory += op.MemoryUsage
		totalCount++
		if op.Success {
			successCount++
		}
		totalObservations += op.ObservationCount
		totalEdges += op.EdgeCount
		totalComplexity += op.QueryComplexity
		totalConfidence += op.ConfidenceScore
	}
	
	avgDuration := float64(0)
	if totalCount > 0 {
		avgDuration = float64(totalDuration) / float64(totalCount)
	}
	
	avgComplexity := float64(0)
	if totalCount > 0 {
		avgComplexity = totalComplexity / float64(totalCount)
	}
	
	avgConfidence := float64(0)
	if totalCount > 0 {
		avgConfidence = totalConfidence / float64(totalCount)
	}
	
	systemMetrics := &domain.SystemMetrics{
		SessionID:         sessionID,
		TimeRange:         &domain.TimeRange{From: from, To: to},
		TotalOperations:   totalCount,
		SuccessfulOps:     successCount,
		FailedOps:         totalCount - successCount,
		AvgDurationMs:     avgDuration,
		TotalMemoryUsage:  totalMemory,
		TotalObservations: totalObservations,
		TotalEdges:        totalEdges,
		AvgQueryComplexity: avgComplexity,
		AvgConfidence:     avgConfidence,
		EvaluatedAt:       time.Now(),
	}
	
	return systemMetrics, nil
}

// EvaluateMemoryQuality evaluates the quality of the memory system.
func (s *ObservabilityService) EvaluateMemoryQuality(ctx context.Context, sessionID string, evalType string) (*domain.QualityMetrics, error) {
	now := time.Now()
	
	// Get recent operations
	from := now.Add(-24 * time.Hour) // Evaluate last 24 hours
	operations, err := s.metricsRepo.GetTemporalMetrics(ctx, sessionID, from, now)
	if err != nil {
		return nil, err
	}
	
	// Calculate quality scores based on evaluation type
	var quality *domain.QualityMetrics
	
	switch evalType {
	case "relevance":
		quality = s.evaluateRelevance(ctx, operations, from, now)
	case "completeness":
		quality = s.evaluateCompleteness(ctx, operations, from, now)
	case "consistency":
		quality = s.evaluateConsistency(ctx, operations, from, now)
	case "temporal_accuracy":
		quality = s.evaluateTemporalAccuracy(ctx, operations, from, now)
	case "overall":
		quality = s.evaluateOverallQuality(ctx, operations, from, now)
	default:
		return nil, fmt.Errorf("unsupported evaluation type: %s", evalType)
	}
	
	quality.SessionID = sessionID
	quality.EvaluationType = evalType
	quality.EvaluatedAt = now
	
	// Save evaluation results
	if err := s.qualityRepo.CreateQualityMetric(ctx, quality); err != nil {
		return nil, err
	}
	
	return quality, nil
}

// evaluateRelevance evaluates how well the system retrieves relevant information.
func (s *ObservabilityService) evaluateRelevance(ctx context.Context, operations []*domain.Metrics, from, to time.Time) *domain.QualityMetrics {
	var totalQueries, successfulRetrievals int
	var totalLatency, totalRelevance float64
	
	for _, op := range operations {
		if op.OperationType == "search" {
			totalQueries++
			if op.Success && op.ResultCount > 0 {
				successfulRetrievals++
				totalLatency += float64(op.Duration)
				totalRelevance += op.ConfidenceScore
			}
		}
	}
	
	avgLatency := float64(0)
	if successfulRetrievals > 0 {
		avgLatency = totalLatency / float64(successfulRetrievals)
	}
	
	avgRelevance := float64(0)
	if successfulRetrievals > 0 {
		avgRelevance = totalRelevance / float64(successfulRetrievals)
	}
	
	score := float64(0)
	if totalQueries > 0 {
		score = float64(successfulRetrievals) / float64(totalQueries)
	}

	return &domain.QualityMetrics{
		Score:             score,
		TotalQueries:      totalQueries,
		SuccessfulRetrievals: successfulRetrievals,
		AverageLatency:    avgLatency,
		AverageRelevance:  avgRelevance,
	}
}

// evaluateCompleteness evaluates how complete the memory coverage is.
func (s *ObservabilityService) evaluateCompleteness(ctx context.Context, operations []*domain.Metrics, from, to time.Time) *domain.QualityMetrics {
	// Get total system state
	totalObservations, _ := s.observationRepo.CountAll(ctx)
	_, _ = s.graphRepo.CountAllEdges(ctx)
	
	var saveOps, relatedOps int
	var coverageScore float64
	
	for _, op := range operations {
		if op.OperationType == "save" {
			saveOps++
		} else if op.OperationType == "get_related" {
			relatedOps++
			// Coverage based on connections
			if totalObservations > 0 {
				coverageScore += float64(op.ResultCount) / float64(totalObservations)
			}
		}
	}
	
	// Calculate coverage score
	finalScore := float64(0)
	if saveOps > 0 {
		finalScore = coverageScore / float64(saveOps)
	}
	
	return &domain.QualityMetrics{
		Score:        finalScore,
		TotalQueries: saveOps + relatedOps,
		SuccessfulRetrievals: relatedOps,
		AverageLatency: 0, // Not applicable for completeness
		AverageRelevance: finalScore,
		KnowledgeCoverage: finalScore,
	}
}

// evaluateConsistency evaluates consistency of the knowledge graph.
func (s *ObservabilityService) evaluateConsistency(ctx context.Context, operations []*domain.Metrics, from, to time.Time) *domain.QualityMetrics {
	// Check for contradictions and inconsistencies
	contradictions, _ := s.graphRepo.GetContradictions(ctx, from, to)
	totalEdges, _ := s.graphRepo.CountAllEdges(ctx)
	
	var consistencyScore float64
	if totalEdges > 0 {
		consistencyScore = 1.0 - (float64(len(contradictions)) / float64(totalEdges))
	}
	
	return &domain.QualityMetrics{
		Score:             consistencyScore,
		TotalQueries:      len(operations),
		SuccessfulRetrievals: len(operations),
		AverageLatency:    0, // Not applicable
		AverageRelevance:  consistencyScore,
	}
}

// evaluateTemporalAccuracy evaluates how well temporal facts are preserved.
func (s *ObservabilityService) evaluateTemporalAccuracy(ctx context.Context, operations []*domain.Metrics, from, to time.Time) *domain.QualityMetrics {
	// Get temporal snapshots and their accuracy
	snapshots, err := s.temporalRepo.GetSnapshotsInRange(ctx, from, to)
	if err != nil {
		return &domain.QualityMetrics{Score: 0.5} // Default moderate score
	}
	
	var accuracyScore float64
	if len(snapshots) > 0 {
		// Calculate accuracy based on snapshot consistency
		for _, snapshot := range snapshots {
			// Compare with current state
			currentEdges, _ := s.graphRepo.CountEdgesByObservation(ctx, snapshot.RootObservationID)
			expectedEdges := snapshot.EdgeCount
			
			if expectedEdges > 0 {
				snapshotAccuracy := float64(currentEdges) / float64(expectedEdges)
				accuracyScore += snapshotAccuracy
			}
		}
		accuracyScore /= float64(len(snapshots))
	}
	
	return &domain.QualityMetrics{
		Score:              accuracyScore,
		TotalQueries:       len(snapshots),
		SuccessfulRetrievals: len(snapshots),
		AverageLatency:     0, // Not applicable
		TemporalAccuracy:   accuracyScore,
	}
}

// evaluateOverallQuality provides an overall quality assessment.
func (s *ObservabilityService) evaluateOverallQuality(ctx context.Context, operations []*domain.Metrics, from, to time.Time) *domain.QualityMetrics {
	// Evaluate individual dimensions
	relevance := s.evaluateRelevance(ctx, operations, from, to)
	completeness := s.evaluateCompleteness(ctx, operations, from, to)
	consistency := s.evaluateConsistency(ctx, operations, from, to)
	temporal := s.evaluateTemporalAccuracy(ctx, operations, from, to)
	
	// Weighted average (adjustable weights based on importance)
	finalScore := (relevance.Score * 0.3) + (completeness.Score * 0.25) + 
		(consistency.Score * 0.25) + (temporal.Score * 0.2)
	
	return &domain.QualityMetrics{
		Score:              finalScore,
		TotalQueries:       len(operations),
		SuccessfulRetrievals: len(operations),
		AverageLatency:     (relevance.AverageLatency + temporal.AverageLatency) / 2,
		AverageRelevance:   (relevance.AverageRelevance + completeness.AverageRelevance) / 2,
		TemporalAccuracy:   temporal.TemporalAccuracy,
		KnowledgeCoverage: completeness.KnowledgeCoverage,
	}
}

// GetHealthCheck provides system health status.
func (s *ObservabilityService) GetHealthCheck(ctx context.Context) (*domain.HealthCheck, error) {
	now := time.Now()
	
	// Get recent metrics for health assessment
	from := now.Add(-1 * time.Hour) // Last hour
	operations, err := s.metricsRepo.GetTemporalMetrics(ctx, "", from, now)
	if err != nil {
		return nil, err
	}
	
	// Calculate health metrics
	var failedOps, slowOps int
	var avgDuration float64
	
	for _, op := range operations {
		if !op.Success {
			failedOps++
		}
		if op.Duration > 5000 { // 5 seconds threshold for slow operations
			slowOps++
		}
		avgDuration += float64(op.Duration)
	}
	
	if len(operations) > 0 {
		avgDuration /= float64(len(operations))
	}
	
	// Determine health status
	status := "healthy"
	totalOps := len(operations)
	if float64(failedOps) > float64(totalOps)*0.1 { // > 10% failure rate
		status = "degraded"
	}
	if float64(slowOps) > float64(totalOps)*0.3 { // > 30% slow operations
		status = "degraded"
	}
	
	health := &domain.HealthCheck{
		Status:           status,
		CheckTime:        now,
		TotalOperations:  len(operations),
		FailedOperations: failedOps,
		SlowOperations:   slowOps,
		AvgDurationMs:   avgDuration,
		Message:         fmt.Sprintf("System %s with %d operations in last hour", status, len(operations)),
	}
	
	return health, nil
}


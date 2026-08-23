// Package scoring implements the importance scoring business logic for Cortex.
//
// The scoring system tracks observation importance based on access patterns,
// recency, relationships, type, and age. Scores are used to prioritize
// observations in search results and recommendations.
package scoring

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// Repository extends domain.ScoringRepository with additional methods needed
// for scoring operations. The store package implements this interface.
type Repository interface {
	domain.ScoringRepository

	// GetScore retrieves the importance score for an observation.
	// Returns ErrNotFound if the observation exists but has no score record.
	GetScore(ctx context.Context, obsID int64) (*domain.ImportanceScore, error)

	// RecordAccess increments the access count and updates last_accessed timestamp.
	// Creates a new score record if one doesn't exist.
	RecordAccess(ctx context.Context, obsID int64) error

	// SetScore updates the score value for an observation.
	SetScore(ctx context.Context, obsID int64, score float64) error

	// GetAllScores retrieves all scores for batch operations like decay.
	GetAllScores(ctx context.Context) ([]*domain.ImportanceScore, error)

	// GetTopByScore retrieves the top-N highest scored observations for a project.
	GetTopByScore(ctx context.Context, project string, limit int) ([]*domain.ImportanceScore, error)

	// GetIncomingEdgeCount returns the number of edges pointing to this observation.
	GetIncomingEdgeCount(ctx context.Context, obsID int64) (int, error)

	// GetObservation retrieves observation data needed for score calculation.
	GetObservation(ctx context.Context, obsID int64) (*domain.Observation, error)
}

// Scoring constants defining the formula coefficients.
const (
	// BaseScore is the starting score for all observations.
	BaseScore = 0.5

	// AccessBonusPerAccess is added for each access (0.1 per access).
	AccessBonusPerAccess = 0.1

	// MaxAccessBonus caps the total access bonus.
	MaxAccessBonus = 1.0

	// RecencyBonus is added if accessed within the recency window.
	RecencyBonus = 0.5

	// RecencyWindow is the time window for recency bonus (24 hours).
	RecencyWindow = 24 * time.Hour

	// EdgeBonusPerEdge is added for each incoming edge (0.2 per edge).
	EdgeBonusPerEdge = 0.2

	// MaxEdgeBonus caps the total edge bonus.
	MaxEdgeBonus = 1.0

	// AgePenaltyPerDay is subtracted per day since creation (0.01 per day).
	AgePenaltyPerDay = 0.01

	// MaxAgePenalty caps the age penalty.
	MaxAgePenalty = 0.5

	// DecayFactor is the multiplier applied during decay operations.
	DecayFactor = 0.95

	// MinScore is the minimum allowed score (floor).
	MinScore = 0.0

	// MaxScore is the maximum allowed score (ceiling).
	MaxScore = 5.0
)

// Type bonuses for different observation types.
var typeBonuses = map[string]float64{
	domain.TypeDecision:     0.5,
	domain.TypeArchitecture: 0.5,
	domain.TypeBugfix:       0.3,
	domain.TypePattern:      0.2,
	domain.TypeDiscovery:    0.15,
	domain.TypeConfig:       0.1,
	domain.TypeLearning:     0.1,
}

// Service implements the scoring business logic.
type Service struct {
	repo Repository
	now  func() time.Time // Injected for testing
}

// NewService creates a new scoring service.
func NewService(repo Repository) *Service {
	return &Service{
		repo: repo,
		now:  time.Now,
	}
}

// GetScore retrieves the importance score for an observation.
func (s *Service) GetScore(ctx context.Context, obsID int64) (*domain.ImportanceScore, error) {
	score, err := s.repo.GetScore(ctx, obsID)
	if err != nil {
		return nil, fmt.Errorf("get score for observation %d: %w", obsID, err)
	}
	return score, nil
}

// RecordAccess increments access count and updates last_accessed timestamp.
// Score recalculation is deferred to the next explicit CalculateScore call
// to avoid 5 cascading DB queries per access.
func (s *Service) RecordAccess(ctx context.Context, obsID int64) error {
	if err := s.repo.RecordAccess(ctx, obsID); err != nil {
		return fmt.Errorf("record access for observation %d: %w", obsID, err)
	}
	return nil
}

// CalculateScore computes the importance score based on multiple factors:
//
//	Score = base + accessBonus + recencyBonus + edgeBonus + typeBonus - agePenalty
//
// Where:
//   - base: BaseScore (0.5)
//   - accessBonus: +0.1 per access (max 1.0)
//   - recencyBonus: +0.5 if accessed in last 24 hours
//   - edgeBonus: +0.2 per incoming edge (max 1.0)
//   - typeBonus: +0.5 (decision), +0.3 (bugfix), +0.2 (pattern), etc.
//   - agePenalty: -0.01 per day since creation (max 0.5)
//
// Score is clamped to [0.0, 5.0].
func (s *Service) CalculateScore(ctx context.Context, obsID int64) (float64, error) {
	now := s.now()

	// Get observation for type and creation date
	obs, err := s.repo.GetObservation(ctx, obsID)
	if err != nil {
		return 0, fmt.Errorf("get observation %d for scoring: %w", obsID, err)
	}

	// Get current score record for access info
	scoreRecord, err := s.repo.GetScore(ctx, obsID)
	if err != nil {
		// If no score record exists, create initial state
		scoreRecord = &domain.ImportanceScore{
			ObservationID: obsID,
			Score:         BaseScore,
			AccessCount:   0,
			LastAccessed:  obs.CreatedAt,
		}
	}

	// Get incoming edge count
	edgeCount, err := s.repo.GetIncomingEdgeCount(ctx, obsID)
	if err != nil {
		// Log but continue with 0 edges
		edgeCount = 0
	}

	// Calculate score components
	score := BaseScore

	// Access bonus: +0.1 per access, max 1.0
	accessBonus := math.Min(
		float64(scoreRecord.AccessCount)*AccessBonusPerAccess,
		MaxAccessBonus,
	)
	score += accessBonus

	// Recency bonus: +0.5 if accessed in last 24 hours
	if now.Sub(scoreRecord.LastAccessed) <= RecencyWindow {
		score += RecencyBonus
	}

	// Edge bonus: +0.2 per incoming edge, max 1.0
	edgeBonus := math.Min(
		float64(edgeCount)*EdgeBonusPerEdge,
		MaxEdgeBonus,
	)
	score += edgeBonus

	// Type bonus
	if bonus, ok := typeBonuses[obs.Type]; ok {
		score += bonus
	}

	// Age penalty: -0.01 per day since creation, max 0.5
	ageDays := now.Sub(obs.CreatedAt).Hours() / 24
	agePenalty := math.Min(ageDays*AgePenaltyPerDay, MaxAgePenalty)
	score -= agePenalty

	// Clamp score to valid range
	score = math.Max(MinScore, math.Min(MaxScore, score))

	// Persist the calculated score
	if err := s.repo.SetScore(ctx, obsID, score); err != nil {
		return score, fmt.Errorf("save score for observation %d: %w", obsID, err)
	}

	return score, nil
}

// GetTop retrieves the top-N most important observations for a project.
// Results are sorted by score in descending order.
func (s *Service) GetTop(ctx context.Context, project string, limit int) ([]*domain.ImportanceScore, error) {
	if limit <= 0 {
		limit = 10 // Default limit
	}
	if limit > 100 {
		limit = 100 // Cap at 100 to prevent excessive queries
	}

	scores, err := s.repo.GetTopByScore(ctx, project, limit)
	if err != nil {
		return nil, fmt.Errorf("get top scores for project %q: %w", project, err)
	}

	return scores, nil
}

// DecayScores reduces all scores by the decay factor (0.95).
// This should be run periodically to naturally reduce importance of
// unused observations over time.
func (s *Service) DecayScores(ctx context.Context) error {
	scores, err := s.repo.GetAllScores(ctx)
	if err != nil {
		return fmt.Errorf("get all scores for decay: %w", err)
	}

	for _, score := range scores {
		// Apply decay factor
		newScore := score.Score * DecayFactor

		// Ensure minimum score
		newScore = math.Max(MinScore, newScore)

		// Skip update if score unchanged (already at minimum)
		if newScore == score.Score {
			continue
		}

		if err := s.repo.SetScore(ctx, score.ObservationID, newScore); err != nil {
			log.Printf("scoring: decay set score for observation %d: %v", score.ObservationID, err)
			continue
		}
	}

	return nil
}

// RecalculateAll forces recalculation of all scores.
// This is useful after bulk changes or migration.
func (s *Service) RecalculateAll(ctx context.Context) error {
	scores, err := s.repo.GetAllScores(ctx)
	if err != nil {
		return fmt.Errorf("get all scores for recalculation: %w", err)
	}

	for _, score := range scores {
		if _, err := s.CalculateScore(ctx, score.ObservationID); err != nil {
			log.Printf("scoring: recalculate score for observation %d: %v", score.ObservationID, err)
			continue
		}
	}

	return nil
}

// SetNowFunc allows injecting a custom time function for testing.
func (s *Service) SetNowFunc(fn func() time.Time) {
	s.now = fn
}

// GetTypeBonus returns the bonus for a given observation type.
// Returns 0 for unknown types.
func GetTypeBonus(obsType string) float64 {
	return typeBonuses[obsType]
}

// GetAllTypeBonuses returns a copy of the type bonus map.
func GetAllTypeBonuses() map[string]float64 {
	result := make(map[string]float64, len(typeBonuses))
	for k, v := range typeBonuses {
		result[k] = v
	}
	return result
}

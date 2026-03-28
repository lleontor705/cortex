// Package lifecycle implements auto-archival and lifecycle management for Cortex.
//
// The archival service periodically checks for observations that are old and
// have low importance scores, soft-deleting them to keep the memory store focused.
package lifecycle

import (
	"context"
	"fmt"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// ArchivalRepository defines the store methods needed for auto-archival.
type ArchivalRepository interface {
	// List retrieves observations matching the filter.
	List(ctx context.Context, filter domain.ObservationFilter) ([]*domain.Observation, error)
	// Delete soft-deletes an observation.
	Delete(ctx context.Context, id int64) error
}

// ScoringReader provides read-only access to importance scores.
type ScoringReader interface {
	// GetScore retrieves the importance score for an observation.
	GetScore(ctx context.Context, obsID int64) (*domain.ImportanceScore, error)
}

// ArchivalConfig holds configuration for the archival service.
type ArchivalConfig struct {
	MaxAgeDays      int
	MinArchiveScore float64
	CheckInterval   time.Duration
}

// ArchivalService manages automatic archival of low-importance observations.
type ArchivalService struct {
	repo    ArchivalRepository
	scoring ScoringReader
	config  ArchivalConfig
	now     func() time.Time
}

// NewArchivalService creates a new archival service.
func NewArchivalService(repo ArchivalRepository, scoring ScoringReader, cfg ArchivalConfig) *ArchivalService {
	return &ArchivalService{
		repo:    repo,
		scoring: scoring,
		config:  cfg,
		now:     time.Now,
	}
}

// RunArchivalCheck checks all observations and archives those that are old
// enough and have a score below the minimum threshold.
func (s *ArchivalService) RunArchivalCheck(ctx context.Context) (int, error) {
	cutoff := s.now().AddDate(0, 0, -s.config.MaxAgeDays)
	archived := 0

	// Fetch observations in batches
	filter := domain.ObservationFilter{
		Limit: 500,
	}

	observations, err := s.repo.List(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("lifecycle: list observations: %w", err)
	}

	for _, obs := range observations {
		if obs.CreatedAt.After(cutoff) {
			continue
		}

		score, err := s.scoring.GetScore(ctx, obs.ID)
		if err != nil {
			continue
		}

		if score.Score < s.config.MinArchiveScore {
			if err := s.repo.Delete(ctx, obs.ID); err != nil {
				continue
			}
			archived++
		}
	}

	return archived, nil
}

// Start begins periodic archival checks in a background goroutine.
// Returns a cancel function to stop the service.
func (s *ArchivalService) Start(ctx context.Context) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		ticker := time.NewTicker(s.config.CheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.RunArchivalCheck(ctx) //nolint:errcheck
			}
		}
	}()

	return cancel
}

// SetNowFunc allows injecting a custom time function for testing.
func (s *ArchivalService) SetNowFunc(fn func() time.Time) {
	s.now = fn
}

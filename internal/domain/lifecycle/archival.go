// Package lifecycle implements auto-archival and lifecycle management for Cortex.
//
// The archival service periodically checks for observations that are old and
// have low importance scores, soft-deleting them to keep the memory store focused.
package lifecycle

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// ArchivalRepository defines the store methods needed for auto-archival.
type ArchivalRepository interface {
	// ListArchivable retrieves observations older than cutoff with score below minScore.
	ListArchivable(ctx context.Context, cutoff time.Time, minScore float64, limit int) ([]*domain.Observation, error)
	// Delete soft-deletes an observation.
	Delete(ctx context.Context, id int64) error
}

// ArchivalConfig holds configuration for the archival service.
type ArchivalConfig struct {
	MaxAgeDays      int
	MinArchiveScore float64
	CheckInterval   time.Duration
}

// ArchivalService manages automatic archival of low-importance observations.
type ArchivalService struct {
	repo   ArchivalRepository
	config ArchivalConfig
	now    func() time.Time
	done   chan struct{}
}

// NewArchivalService creates a new archival service.
func NewArchivalService(repo ArchivalRepository, cfg ArchivalConfig) *ArchivalService {
	return &ArchivalService{
		repo:   repo,
		config: cfg,
		now:    time.Now,
	}
}

// RunArchivalCheck checks all observations and archives those that are old
// enough and have a score below the minimum threshold.
func (s *ArchivalService) RunArchivalCheck(ctx context.Context) (int, error) {
	cutoff := s.now().AddDate(0, 0, -s.config.MaxAgeDays)

	observations, err := s.repo.ListArchivable(ctx, cutoff, s.config.MinArchiveScore, 500)
	if err != nil {
		return 0, fmt.Errorf("lifecycle: list archivable observations: %w", err)
	}

	archived := 0
	for _, obs := range observations {
		if err := s.repo.Delete(ctx, obs.ID); err != nil {
			log.Printf("lifecycle: failed to archive observation %d: %v", obs.ID, err)
			continue
		}
		archived++
	}

	return archived, nil
}

// Start begins periodic archival checks in a background goroutine.
// Returns a cancel function to stop the service.
func (s *ArchivalService) Start(ctx context.Context) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	s.done = make(chan struct{})

	var running int32

	go func() {
		defer close(s.done)
		ticker := time.NewTicker(s.config.CheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Skip if a previous check is still running
				if !atomic.CompareAndSwapInt32(&running, 0, 1) {
					continue
				}

				checkCtx, checkCancel := context.WithTimeout(ctx, s.config.CheckInterval)
				archived, err := s.RunArchivalCheck(checkCtx)
				checkCancel()

				if err != nil {
					log.Printf("lifecycle: archival check failed: %v", err)
				} else if archived > 0 {
					log.Printf("lifecycle: archived %d observations", archived)
				}

				atomic.StoreInt32(&running, 0)
			}
		}
	}()

	return cancel
}

// Stop waits for the periodic archival goroutine to exit. It is safe to call
// before Start or repeatedly; composition roots use it to guarantee no
// lifecycle check can touch a repository after the database is closed.
func (s *ArchivalService) Stop() {
	if s == nil || s.done == nil {
		return
	}
	<-s.done
}

// SetNowFunc allows injecting a custom time function for testing.
func (s *ArchivalService) SetNowFunc(fn func() time.Time) {
	s.now = fn
}

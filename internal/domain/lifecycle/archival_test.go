package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// mockArchivalRepo implements ArchivalRepository for testing.
type mockArchivalRepo struct {
	observations []*domain.Observation
	deleted      []int64
}

func (m *mockArchivalRepo) List(_ context.Context, _ domain.ObservationFilter) ([]*domain.Observation, error) {
	return m.observations, nil
}

func (m *mockArchivalRepo) Delete(_ context.Context, id int64) error {
	m.deleted = append(m.deleted, id)
	return nil
}

// mockScoringReader implements ScoringReader for testing.
type mockScoringReader struct {
	scores map[int64]*domain.ImportanceScore
}

func (m *mockScoringReader) GetScore(_ context.Context, obsID int64) (*domain.ImportanceScore, error) {
	if s, ok := m.scores[obsID]; ok {
		return s, nil
	}
	return nil, &domain.NotFoundError{Type: "importance_score", ID: obsID}
}

func TestRunArchivalCheck(t *testing.T) {
	now := time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC)
	oldDate := now.AddDate(0, 0, -100) // 100 days old
	recentDate := now.AddDate(0, 0, -10) // 10 days old

	repo := &mockArchivalRepo{
		observations: []*domain.Observation{
			{ID: 1, Title: "Old low score", CreatedAt: oldDate},
			{ID: 2, Title: "Old high score", CreatedAt: oldDate},
			{ID: 3, Title: "Recent low score", CreatedAt: recentDate},
		},
	}

	scoring := &mockScoringReader{
		scores: map[int64]*domain.ImportanceScore{
			1: {ObservationID: 1, Score: 0.05},
			2: {ObservationID: 2, Score: 2.0},
			3: {ObservationID: 3, Score: 0.05},
		},
	}

	svc := NewArchivalService(repo, scoring, ArchivalConfig{
		MaxAgeDays:      90,
		MinArchiveScore: 0.1,
		CheckInterval:   time.Hour,
	})
	svc.SetNowFunc(func() time.Time { return now })

	archived, err := svc.RunArchivalCheck(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only obs 1 should be archived (old AND low score)
	// obs 2: old but high score
	// obs 3: low score but recent
	if archived != 1 {
		t.Errorf("expected 1 archived, got %d", archived)
	}

	if len(repo.deleted) != 1 || repo.deleted[0] != 1 {
		t.Errorf("expected obs 1 deleted, got %v", repo.deleted)
	}
}

func TestRunArchivalCheck_NoObservations(t *testing.T) {
	repo := &mockArchivalRepo{observations: []*domain.Observation{}}
	scoring := &mockScoringReader{scores: map[int64]*domain.ImportanceScore{}}

	svc := NewArchivalService(repo, scoring, ArchivalConfig{
		MaxAgeDays:      90,
		MinArchiveScore: 0.1,
		CheckInterval:   time.Hour,
	})

	archived, err := svc.RunArchivalCheck(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if archived != 0 {
		t.Errorf("expected 0 archived, got %d", archived)
	}
}

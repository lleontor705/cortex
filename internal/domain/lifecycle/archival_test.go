package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// mockArchivalRepo implements ArchivalRepository for testing.
type mockArchivalRepo struct {
	observations []*domain.Observation
	deleted      []int64
}

func (m *mockArchivalRepo) ListArchivable(_ context.Context, _ time.Time, _ float64, _ int) ([]*domain.Observation, error) {
	return m.observations, nil
}

func (m *mockArchivalRepo) Delete(_ context.Context, id int64) error {
	m.deleted = append(m.deleted, id)
	return nil
}

func TestRunArchivalCheck(t *testing.T) {
	now := time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC)
	oldDate := now.AddDate(0, 0, -100) // 100 days old

	// Mock returns only archivable observations (old + low score)
	// The filtering is now done by the repository via ListArchivable
	repo := &mockArchivalRepo{
		observations: []*domain.Observation{
			{ID: 1, Title: "Old low score", CreatedAt: oldDate},
		},
	}

	svc := NewArchivalService(repo, ArchivalConfig{
		MaxAgeDays:      90,
		MinArchiveScore: 0.1,
		CheckInterval:   time.Hour,
	})
	svc.SetNowFunc(func() time.Time { return now })

	archived, err := svc.RunArchivalCheck(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if archived != 1 {
		t.Errorf("expected 1 archived, got %d", archived)
	}

	if len(repo.deleted) != 1 || repo.deleted[0] != 1 {
		t.Errorf("expected obs 1 deleted, got %v", repo.deleted)
	}
}

func TestRunArchivalCheck_NoObservations(t *testing.T) {
	repo := &mockArchivalRepo{observations: []*domain.Observation{}}

	svc := NewArchivalService(repo, ArchivalConfig{
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

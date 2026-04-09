package scoring

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// mockRepository implements the scoring.Repository interface for testing.
type mockRepository struct {
	scores       map[int64]*domain.ImportanceScore
	observations map[int64]*domain.Observation
	edgeCounts   map[int64]int
	allScores    []*domain.ImportanceScore
	topScores    []*domain.ImportanceScore

	// Error injection
	getScoreErr          error
	getObservationErr    error
	getIncomingEdgeErr   error
	setScoreErr          error
	getAllScoresErr       error
	getTopByScoreErr     error
	recordAccessErr      error
	updateScoreErr       error
	getTopErr            error

	// Track calls
	setScoreCalls []setScoreCall
}

type setScoreCall struct {
	obsID int64
	score float64
}

func newMockRepository() *mockRepository {
	return &mockRepository{
		scores:       make(map[int64]*domain.ImportanceScore),
		observations: make(map[int64]*domain.Observation),
		edgeCounts:   make(map[int64]int),
	}
}

func (m *mockRepository) GetScore(ctx context.Context, obsID int64) (*domain.ImportanceScore, error) {
	if m.getScoreErr != nil {
		return nil, m.getScoreErr
	}
	s, ok := m.scores[obsID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return s, nil
}

func (m *mockRepository) UpdateScore(ctx context.Context, obsID int64, increment float64) error {
	if m.updateScoreErr != nil {
		return m.updateScoreErr
	}
	if s, ok := m.scores[obsID]; ok {
		s.Score += increment
	}
	return nil
}

func (m *mockRepository) GetTop(ctx context.Context, project string, limit int) ([]*domain.ImportanceScore, error) {
	if m.getTopErr != nil {
		return nil, m.getTopErr
	}
	return m.topScores, nil
}

func (m *mockRepository) RecordAccess(ctx context.Context, obsID int64) error {
	if m.recordAccessErr != nil {
		return m.recordAccessErr
	}
	if s, ok := m.scores[obsID]; ok {
		s.AccessCount++
		s.LastAccessed = time.Now()
	}
	return nil
}

func (m *mockRepository) SetScore(ctx context.Context, obsID int64, score float64) error {
	if m.setScoreErr != nil {
		return m.setScoreErr
	}
	m.setScoreCalls = append(m.setScoreCalls, setScoreCall{obsID: obsID, score: score})
	if s, ok := m.scores[obsID]; ok {
		s.Score = score
	}
	return nil
}

func (m *mockRepository) GetAllScores(ctx context.Context) ([]*domain.ImportanceScore, error) {
	if m.getAllScoresErr != nil {
		return nil, m.getAllScoresErr
	}
	return m.allScores, nil
}

func (m *mockRepository) GetTopByScore(ctx context.Context, project string, limit int) ([]*domain.ImportanceScore, error) {
	if m.getTopByScoreErr != nil {
		return nil, m.getTopByScoreErr
	}
	if limit > len(m.topScores) {
		return m.topScores, nil
	}
	return m.topScores[:limit], nil
}

func (m *mockRepository) GetIncomingEdgeCount(ctx context.Context, obsID int64) (int, error) {
	if m.getIncomingEdgeErr != nil {
		return 0, m.getIncomingEdgeErr
	}
	return m.edgeCounts[obsID], nil
}

func (m *mockRepository) GetObservation(ctx context.Context, obsID int64) (*domain.Observation, error) {
	if m.getObservationErr != nil {
		return nil, m.getObservationErr
	}
	obs, ok := m.observations[obsID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return obs, nil
}

func TestCalculateScore_Success(t *testing.T) {
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		obsType     string
		createdAt   time.Time
		accessCount int
		lastAccessed time.Time
		edgeCount   int
		wantMin     float64
		wantMax     float64
		desc        string
	}{
		{
			name:        "decision type, recent access, edges",
			obsType:     domain.TypeDecision,
			createdAt:   now.Add(-2 * 24 * time.Hour), // 2 days old
			accessCount: 5,
			lastAccessed: now.Add(-1 * time.Hour), // accessed 1 hour ago (within recency window)
			edgeCount:   3,
			// base(0.5) + access(min(5*0.1,1.0)=0.5) + recency(0.5) + edges(min(3*0.2,1.0)=0.6) + type(0.5) - age(2*0.01=0.02) = 2.58
			wantMin: 2.5,
			wantMax: 2.7,
			desc:    "score=0.5+0.5+0.5+0.6+0.5-0.02=2.58",
		},
		{
			name:        "bugfix type, no recent access, no edges",
			obsType:     domain.TypeBugfix,
			createdAt:   now.Add(-30 * 24 * time.Hour), // 30 days old
			accessCount: 0,
			lastAccessed: now.Add(-48 * time.Hour), // accessed 2 days ago (outside recency window)
			edgeCount:   0,
			// base(0.5) + access(0) + recency(0) + edges(0) + type(0.3) - age(min(30*0.01,0.5)=0.3) = 0.5
			wantMin: 0.45,
			wantMax: 0.55,
			desc:    "score=0.5+0+0+0+0.3-0.3=0.5",
		},
		{
			name:        "manual type (no type bonus), max access bonus",
			obsType:     domain.TypeManual,
			createdAt:   now.Add(-1 * 24 * time.Hour), // 1 day old
			accessCount: 15,                             // over max access bonus
			lastAccessed: now.Add(-30 * time.Minute),
			edgeCount:   0,
			// base(0.5) + access(min(15*0.1,1.0)=1.0) + recency(0.5) + edges(0) + type(0) - age(1*0.01=0.01) = 1.99
			wantMin: 1.9,
			wantMax: 2.1,
			desc:    "score=0.5+1.0+0.5+0+0-0.01=1.99",
		},
		{
			name:        "max edge bonus capped",
			obsType:     domain.TypePattern,
			createdAt:   now,
			accessCount: 0,
			lastAccessed: now,
			edgeCount:   10, // would be 10*0.2=2.0, capped to 1.0
			// base(0.5) + access(0) + recency(0.5) + edges(1.0) + type(0.2) - age(0) = 2.2
			wantMin: 2.1,
			wantMax: 2.3,
			desc:    "edge bonus capped at 1.0",
		},
		{
			name:        "very old observation, age penalty capped",
			obsType:     domain.TypeConfig,
			createdAt:   now.Add(-365 * 24 * time.Hour), // 365 days old
			accessCount: 0,
			lastAccessed: now.Add(-365 * 24 * time.Hour),
			edgeCount:   0,
			// base(0.5) + access(0) + recency(0) + edges(0) + type(0.1) - age(min(365*0.01,0.5)=0.5) = 0.1
			wantMin: 0.05,
			wantMax: 0.15,
			desc:    "age penalty capped at 0.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockRepository()
			repo.observations[1] = &domain.Observation{
				ID:        1,
				Type:      tt.obsType,
				CreatedAt: tt.createdAt,
			}
			repo.scores[1] = &domain.ImportanceScore{
				ObservationID: 1,
				Score:         BaseScore,
				AccessCount:   tt.accessCount,
				LastAccessed:  tt.lastAccessed,
			}
			repo.edgeCounts[1] = tt.edgeCount

			svc := NewService(repo)
			svc.SetNowFunc(func() time.Time { return now })

			score, err := svc.CalculateScore(context.Background(), 1)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("%s: got score %.4f, want between %.2f and %.2f", tt.desc, score, tt.wantMin, tt.wantMax)
			}

			// Verify score was persisted
			if len(repo.setScoreCalls) != 1 {
				t.Fatalf("expected 1 SetScore call, got %d", len(repo.setScoreCalls))
			}
			if repo.setScoreCalls[0].obsID != 1 {
				t.Errorf("SetScore called with obsID %d, want 1", repo.setScoreCalls[0].obsID)
			}
		})
	}
}

func TestCalculateScore_NotFound(t *testing.T) {
	repo := newMockRepository()
	// No observation in the map -- GetObservation will return ErrNotFound

	svc := NewService(repo)

	_, err := svc.CalculateScore(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error for non-existent observation, got nil")
	}
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound wrapped, got: %v", err)
	}
}

func TestCalculateScore_NoScoreRecord(t *testing.T) {
	// When no score record exists, CalculateScore should use initial defaults
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	repo := newMockRepository()
	repo.observations[1] = &domain.Observation{
		ID:        1,
		Type:      domain.TypeDecision,
		CreatedAt: now,
	}
	// No score record -- GetScore will return ErrNotFound
	// edgeCounts defaults to 0

	svc := NewService(repo)
	svc.SetNowFunc(func() time.Time { return now })

	score, err := svc.CalculateScore(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// base(0.5) + access(0) + recency(0.5, since lastAccessed=createdAt=now) + edges(0) + type(0.5) - age(0) = 1.5
	if score < 1.4 || score > 1.6 {
		t.Errorf("got score %.4f, want ~1.5 (base + recency + type bonus for decision)", score)
	}
}

func TestCalculateScore_SetScoreError(t *testing.T) {
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	repo := newMockRepository()
	repo.observations[1] = &domain.Observation{
		ID:        1,
		Type:      domain.TypeManual,
		CreatedAt: now,
	}
	repo.scores[1] = &domain.ImportanceScore{
		ObservationID: 1,
		AccessCount:   0,
		LastAccessed:  now,
	}
	repo.setScoreErr = errors.New("db write failed")

	svc := NewService(repo)
	svc.SetNowFunc(func() time.Time { return now })

	// CalculateScore returns the score even if SetScore fails, but also returns an error
	score, err := svc.CalculateScore(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error when SetScore fails")
	}
	// Score should still be computed even though persistence failed
	if score <= 0 {
		t.Errorf("expected a computed score > 0, got %.4f", score)
	}
}

func TestGetScore_Success(t *testing.T) {
	repo := newMockRepository()
	now := time.Now()
	repo.scores[42] = &domain.ImportanceScore{
		ObservationID: 42,
		Score:         3.14,
		AccessCount:   7,
		LastAccessed:  now,
	}

	svc := NewService(repo)

	score, err := svc.GetScore(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score.ObservationID != 42 {
		t.Errorf("got ObservationID %d, want 42", score.ObservationID)
	}
	if score.Score != 3.14 {
		t.Errorf("got Score %.2f, want 3.14", score.Score)
	}
	if score.AccessCount != 7 {
		t.Errorf("got AccessCount %d, want 7", score.AccessCount)
	}
}

func TestGetScore_NotFound(t *testing.T) {
	repo := newMockRepository()

	svc := NewService(repo)

	_, err := svc.GetScore(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error for non-existent score, got nil")
	}
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound wrapped, got: %v", err)
	}
}

func TestGetTop_Success(t *testing.T) {
	repo := newMockRepository()
	repo.topScores = []*domain.ImportanceScore{
		{ObservationID: 1, Score: 4.5, AccessCount: 20},
		{ObservationID: 2, Score: 3.8, AccessCount: 15},
		{ObservationID: 3, Score: 2.1, AccessCount: 5},
	}

	svc := NewService(repo)

	scores, err := svc.GetTop(context.Background(), "myproject", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scores) != 3 {
		t.Fatalf("got %d scores, want 3", len(scores))
	}
	if scores[0].Score != 4.5 {
		t.Errorf("first score = %.1f, want 4.5", scores[0].Score)
	}
	if scores[2].Score != 2.1 {
		t.Errorf("last score = %.1f, want 2.1", scores[2].Score)
	}
}

func TestGetTop_Empty(t *testing.T) {
	repo := newMockRepository()
	repo.topScores = []*domain.ImportanceScore{}

	svc := NewService(repo)

	scores, err := svc.GetTop(context.Background(), "empty-project", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scores) != 0 {
		t.Errorf("got %d scores, want 0", len(scores))
	}
}

func TestGetTop_DefaultAndMaxLimit(t *testing.T) {
	repo := newMockRepository()
	repo.topScores = []*domain.ImportanceScore{
		{ObservationID: 1, Score: 4.0},
	}

	svc := NewService(repo)

	// Zero limit should default to 10 (no error)
	_, err := svc.GetTop(context.Background(), "proj", 0)
	if err != nil {
		t.Fatalf("unexpected error with zero limit: %v", err)
	}

	// Negative limit should default to 10 (no error)
	_, err = svc.GetTop(context.Background(), "proj", -5)
	if err != nil {
		t.Fatalf("unexpected error with negative limit: %v", err)
	}

	// Very large limit should be capped to 100 (no error)
	_, err = svc.GetTop(context.Background(), "proj", 500)
	if err != nil {
		t.Fatalf("unexpected error with large limit: %v", err)
	}
}

func TestGetTop_Error(t *testing.T) {
	repo := newMockRepository()
	repo.getTopByScoreErr = errors.New("db error")

	svc := NewService(repo)

	_, err := svc.GetTop(context.Background(), "proj", 10)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRecordAccess(t *testing.T) {
	repo := newMockRepository()
	now := time.Now()
	repo.scores[1] = &domain.ImportanceScore{
		ObservationID: 1,
		AccessCount:   3,
		LastAccessed:  now,
	}

	svc := NewService(repo)

	err := svc.RecordAccess(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.scores[1].AccessCount != 4 {
		t.Errorf("access count = %d, want 4", repo.scores[1].AccessCount)
	}
}

func TestRecordAccess_Error(t *testing.T) {
	repo := newMockRepository()
	repo.recordAccessErr = errors.New("db error")

	svc := NewService(repo)

	err := svc.RecordAccess(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetTypeBonus(t *testing.T) {
	tests := []struct {
		obsType string
		want    float64
	}{
		{domain.TypeDecision, 0.5},
		{domain.TypeBugfix, 0.3},
		{domain.TypePattern, 0.2},
		{domain.TypeDiscovery, 0.15},
		{domain.TypeConfig, 0.1},
		{domain.TypeLearning, 0.1},
		{domain.TypeManual, 0.0},
		{"unknown_type", 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.obsType, func(t *testing.T) {
			got := GetTypeBonus(tt.obsType)
			if got != tt.want {
				t.Errorf("GetTypeBonus(%q) = %.2f, want %.2f", tt.obsType, got, tt.want)
			}
		})
	}
}

func TestGetAllTypeBonuses(t *testing.T) {
	bonuses := GetAllTypeBonuses()

	// Should be a copy, not the original
	if len(bonuses) != 6 {
		t.Errorf("got %d type bonuses, want 6", len(bonuses))
	}

	// Modifying the returned map should not affect the original
	bonuses["test"] = 99.0
	if GetTypeBonus("test") != 0.0 {
		t.Error("modifying returned map should not affect internal state")
	}
}

func TestDecayScores(t *testing.T) {
	repo := newMockRepository()
	repo.allScores = []*domain.ImportanceScore{
		{ObservationID: 1, Score: 2.0},
		{ObservationID: 2, Score: 1.0},
		{ObservationID: 3, Score: 0.0}, // already at minimum, should be skipped
	}

	svc := NewService(repo)

	err := svc.DecayScores(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Two SetScore calls expected (score=0.0 should be skipped since 0*0.95=0)
	if len(repo.setScoreCalls) != 2 {
		t.Fatalf("expected 2 SetScore calls, got %d", len(repo.setScoreCalls))
	}

	// Check decayed values
	if repo.setScoreCalls[0].score != 2.0*DecayFactor {
		t.Errorf("first decayed score = %.4f, want %.4f", repo.setScoreCalls[0].score, 2.0*DecayFactor)
	}
	if repo.setScoreCalls[1].score != 1.0*DecayFactor {
		t.Errorf("second decayed score = %.4f, want %.4f", repo.setScoreCalls[1].score, 1.0*DecayFactor)
	}
}

func TestDecayScores_GetAllError(t *testing.T) {
	repo := newMockRepository()
	repo.getAllScoresErr = errors.New("db error")

	svc := NewService(repo)

	err := svc.DecayScores(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestScoreClampedToRange(t *testing.T) {
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	repo := newMockRepository()
	repo.observations[1] = &domain.Observation{
		ID:        1,
		Type:      domain.TypeDecision,
		CreatedAt: now,
	}
	repo.scores[1] = &domain.ImportanceScore{
		ObservationID: 1,
		AccessCount:   100, // massive access count
		LastAccessed:  now,
	}
	repo.edgeCounts[1] = 50 // massive edge count

	svc := NewService(repo)
	svc.SetNowFunc(func() time.Time { return now })

	score, err := svc.CalculateScore(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if score > MaxScore {
		t.Errorf("score %.4f exceeds MaxScore %.1f", score, MaxScore)
	}
	if score < MinScore {
		t.Errorf("score %.4f below MinScore %.1f", score, MinScore)
	}
}

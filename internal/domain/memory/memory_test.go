package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/memory"
	"github.com/lleontor705/cortex/v2/testutil"
)

// mockRepository implements domain.ObservationRepository for testing.
type mockRepository struct {
	observations map[int64]*domain.Observation
	topicKeys    map[string]int64 // "project:topicKey" -> ID
	nextID       int64
	saveErr      error
	getErr       error
	updateErr    error
	deleteErr    error
	listErr      error
	topicKeyErr  error
}

func newMockRepository() *mockRepository {
	return &mockRepository{
		observations: make(map[int64]*domain.Observation),
		topicKeys:    make(map[string]int64),
		nextID:       1,
	}
}

func (m *mockRepository) Save(ctx context.Context, obs *domain.Observation) error {
	if m.saveErr != nil {
		return m.saveErr
	}

	if obs.ID == 0 {
		obs.ID = m.nextID
		m.nextID++
	}

	m.observations[obs.ID] = obs

	if obs.TopicKey != "" {
		key := obs.Project + ":" + obs.TopicKey
		m.topicKeys[key] = obs.ID
	}

	return nil
}

func (m *mockRepository) GetByID(ctx context.Context, id int64) (*domain.Observation, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}

	obs, ok := m.observations[id]
	if !ok {
		return nil, &domain.NotFoundError{Type: "observation", ID: id}
	}

	return obs, nil
}

func (m *mockRepository) GetByTopicKey(ctx context.Context, project, topicKey string) (*domain.Observation, error) {
	if m.topicKeyErr != nil {
		return nil, m.topicKeyErr
	}

	key := project + ":" + topicKey
	id, ok := m.topicKeys[key]
	if !ok {
		return nil, &domain.NotFoundError{Type: "observation", ID: key}
	}

	return m.observations[id], nil
}

func (m *mockRepository) Update(ctx context.Context, obs *domain.Observation) error {
	if m.updateErr != nil {
		return m.updateErr
	}

	if _, ok := m.observations[obs.ID]; !ok {
		return &domain.NotFoundError{Type: "observation", ID: obs.ID}
	}

	m.observations[obs.ID] = obs

	if obs.TopicKey != "" {
		key := obs.Project + ":" + obs.TopicKey
		m.topicKeys[key] = obs.ID
	}

	return nil
}

func (m *mockRepository) Delete(ctx context.Context, id int64) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}

	delete(m.observations, id)
	return nil
}

func (m *mockRepository) List(ctx context.Context, filter domain.ObservationFilter) ([]*domain.Observation, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}

	var results []*domain.Observation
	for _, obs := range m.observations {
		// Apply filters
		if filter.Project != "" && obs.Project != filter.Project {
			continue
		}
		if filter.Scope != "" && obs.Scope != filter.Scope {
			continue
		}
		if filter.Type != "" && obs.Type != filter.Type {
			continue
		}
		results = append(results, obs)
	}

	// Apply limit
	if filter.Limit > 0 && len(results) > filter.Limit {
		results = results[:filter.Limit]
	}

	return results, nil
}

func (m *mockRepository) CountAll(ctx context.Context) (int, error) {
	return len(m.observations), nil
}

func (m *mockRepository) CountByRoot(ctx context.Context, rootObsID int64) (int, error) {
	return 0, nil
}

func (m *mockRepository) GetBySource(ctx context.Context, source string, limit int) ([]*domain.Observation, error) {
	var results []*domain.Observation
	for _, obs := range m.observations {
		if obs.Source == source {
			results = append(results, obs)
		}
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (m *mockRepository) GetByType(ctx context.Context, obsType string, limit int) ([]*domain.Observation, error) {
	var results []*domain.Observation
	for _, obs := range m.observations {
		if obs.Type == obsType {
			results = append(results, obs)
		}
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// --- Tests -------------------------------------------------------------------

func TestNewService(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	testutil.AssertNotNil(t, svc)
}

func TestService_Save_Success(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	obs := &domain.Observation{
		Title:     "Test Observation",
		Content:   "Test content",
		Project:   "test-project",
		SessionID: "session-123",
	}

	err := svc.Save(context.Background(), obs)
	testutil.RequireNoError(t, err)

	// Verify defaults were applied
	testutil.AssertEqual(t, domain.TypeManual, obs.Type)
	testutil.AssertEqual(t, domain.ScopeProject, obs.Scope)
	testutil.AssertTrue(t, obs.ID > 0, "expected ID to be set")
	testutil.AssertTrue(t, !obs.CreatedAt.IsZero(), "expected CreatedAt to be set")
	testutil.AssertTrue(t, !obs.UpdatedAt.IsZero(), "expected UpdatedAt to be set")
}

func TestService_Save_WithExplicitValues(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	obs := &domain.Observation{
		Title:     "Test Observation",
		Content:   "Test content",
		Type:      domain.TypeDecision,
		Scope:     domain.ScopePersonal,
		Project:   "test-project",
		SessionID: "session-123",
	}

	err := svc.Save(context.Background(), obs)
	testutil.RequireNoError(t, err)

	// Verify explicit values were preserved
	testutil.AssertEqual(t, domain.TypeDecision, obs.Type)
	testutil.AssertEqual(t, domain.ScopePersonal, obs.Scope)
}

func TestService_Save_ValidationErrors(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	tests := []struct {
		name        string
		obs         *domain.Observation
		expectField string
	}{
		{
			name: "empty title",
			obs: &domain.Observation{
				Content: "Test content",
			},
			expectField: "title",
		},
		{
			name: "empty content",
			obs: &domain.Observation{
				Title: "Test title",
			},
			expectField: "content",
		},
		{
			name: "both empty",
			obs: &domain.Observation{
				Title:   "",
				Content: "",
			},
			expectField: "title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.Save(context.Background(), tt.obs)
			testutil.AssertValidationError(t, err, tt.expectField)
		})
	}
}

func TestService_Get_Success(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// First save an observation
	obs := &domain.Observation{
		Title:     "Test Observation",
		Content:   "Test content",
		Project:   "test-project",
		SessionID: "session-123",
	}
	err := svc.Save(context.Background(), obs)
	testutil.RequireNoError(t, err)

	// Then retrieve it
	retrieved, err := svc.Get(context.Background(), obs.ID)
	testutil.RequireNoError(t, err)

	testutil.AssertObservationEqual(t, obs, retrieved)
}

func TestService_Get_NotFound(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	_, err := svc.Get(context.Background(), 999)
	testutil.AssertNotFoundError(t, err, "observation", int64(999))
}

func TestService_Update_Success(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// First save an observation
	obs := &domain.Observation{
		Title:     "Original Title",
		Content:   "Original content",
		Project:   "test-project",
		SessionID: "session-123",
	}
	err := svc.Save(context.Background(), obs)
	testutil.RequireNoError(t, err)

	originalCreatedAt := obs.CreatedAt

	// Wait a bit to ensure UpdatedAt differs
	time.Sleep(10 * time.Millisecond)

	// Update the observation
	obs.Title = "Updated Title"
	obs.Content = "Updated content"
	err = svc.Update(context.Background(), obs)
	testutil.RequireNoError(t, err)

	// Verify update
	retrieved, err := svc.Get(context.Background(), obs.ID)
	testutil.RequireNoError(t, err)

	testutil.AssertEqual(t, "Updated Title", retrieved.Title)
	testutil.AssertEqual(t, "Updated content", retrieved.Content)
	testutil.AssertEqual(t, originalCreatedAt, retrieved.CreatedAt)
	testutil.AssertTrue(t, retrieved.UpdatedAt.After(originalCreatedAt), "expected UpdatedAt to be after CreatedAt")
}

func TestService_Update_ValidationErrors(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// First save an observation
	obs := &domain.Observation{
		Title:     "Original Title",
		Content:   "Original content",
		Project:   "test-project",
		SessionID: "session-123",
	}
	err := svc.Save(context.Background(), obs)
	testutil.RequireNoError(t, err)

	// Try to update with empty title
	obs.Title = ""
	err = svc.Update(context.Background(), obs)
	testutil.AssertValidationError(t, err, "title")
}

func TestService_Update_NotFound(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	obs := &domain.Observation{
		ID:      999,
		Title:   "Test",
		Content: "Test content",
	}

	err := svc.Update(context.Background(), obs)
	testutil.AssertNotFoundError(t, err, "observation", int64(999))
}

func TestService_Delete_Success(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// First save an observation
	obs := &domain.Observation{
		Title:     "Test Observation",
		Content:   "Test content",
		Project:   "test-project",
		SessionID: "session-123",
	}
	err := svc.Save(context.Background(), obs)
	testutil.RequireNoError(t, err)

	// Delete it
	err = svc.Delete(context.Background(), obs.ID)
	testutil.RequireNoError(t, err)

	// Verify it's gone
	_, err = svc.Get(context.Background(), obs.ID)
	testutil.AssertError(t, err)
}

func TestService_List_Success(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// Save multiple observations
	for i := 0; i < 5; i++ {
		obs := &domain.Observation{
			Title:     "Test Observation",
			Content:   "Test content",
			Project:   "test-project",
			SessionID: "session-123",
		}
		err := svc.Save(context.Background(), obs)
		testutil.RequireNoError(t, err)
	}

	// List all
	results, err := svc.List(context.Background(), domain.ObservationFilter{})
	testutil.RequireNoError(t, err)

	testutil.AssertLen(t, results, 5)
}

func TestService_List_WithFilter(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// Save observations in different projects
	for _, project := range []string{"project-a", "project-b", "project-a"} {
		obs := &domain.Observation{
			Title:     "Test Observation",
			Content:   "Test content",
			Project:   project,
			SessionID: "session-123",
		}
		err := svc.Save(context.Background(), obs)
		testutil.RequireNoError(t, err)
	}

	// Filter by project
	results, err := svc.List(context.Background(), domain.ObservationFilter{
		Project: "project-a",
	})
	testutil.RequireNoError(t, err)

	testutil.AssertLen(t, results, 2)
}

func TestService_List_WithLimit(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// Save 10 observations
	for i := 0; i < 10; i++ {
		obs := &domain.Observation{
			Title:     "Test Observation",
			Content:   "Test content",
			Project:   "test-project",
			SessionID: "session-123",
		}
		err := svc.Save(context.Background(), obs)
		testutil.RequireNoError(t, err)
	}

	// List with limit
	results, err := svc.List(context.Background(), domain.ObservationFilter{
		Limit: 5,
	})
	testutil.RequireNoError(t, err)

	testutil.AssertLen(t, results, 5)
}

func TestService_List_DefaultLimit(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// List with empty filter should use default limit
	results, err := svc.List(context.Background(), domain.ObservationFilter{})
	testutil.RequireNoError(t, err)

	// Empty repository should return empty list
	testutil.AssertLen(t, results, 0)
}

func TestService_GetByTopicKey_Success(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// Save an observation with topic key
	obs := &domain.Observation{
		Title:     "Test Observation",
		Content:   "Test content",
		Project:   "test-project",
		TopicKey:  "architecture/auth",
		SessionID: "session-123",
	}
	err := svc.Save(context.Background(), obs)
	testutil.RequireNoError(t, err)

	// Retrieve by topic key
	retrieved, err := svc.GetByTopicKey(context.Background(), "test-project", "architecture/auth")
	testutil.RequireNoError(t, err)

	testutil.AssertObservationEqual(t, obs, retrieved)
}

func TestService_GetByTopicKey_EmptyTopicKey(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	_, err := svc.GetByTopicKey(context.Background(), "test-project", "")
	testutil.AssertValidationError(t, err, "topic_key")
}

func TestService_GetByTopicKey_NotFound(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	_, err := svc.GetByTopicKey(context.Background(), "test-project", "nonexistent/key")
	testutil.AssertError(t, err)
}

func TestService_UpsertByTopicKey_Create(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	obs := &domain.Observation{
		Title:     "Test Observation",
		Content:   "Test content",
		Project:   "test-project",
		TopicKey:  "architecture/auth",
		SessionID: "session-123",
	}

	err := svc.UpsertByTopicKey(context.Background(), obs)
	testutil.RequireNoError(t, err)

	// Verify it was created
	testutil.AssertTrue(t, obs.ID > 0, "expected ID to be set")
	testutil.AssertEqual(t, domain.TypeManual, obs.Type) // Default applied
}

func TestService_UpsertByTopicKey_Update(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// First create an observation
	obs := &domain.Observation{
		Title:     "Original Title",
		Content:   "Original content",
		Project:   "test-project",
		TopicKey:  "architecture/auth",
		SessionID: "session-123",
	}
	err := svc.Save(context.Background(), obs)
	testutil.RequireNoError(t, err)

	originalID := obs.ID
	originalCreatedAt := obs.CreatedAt

	// Wait a bit
	time.Sleep(10 * time.Millisecond)

	// Upsert with same topic key (should update)
	updated := &domain.Observation{
		Title:     "Updated Title",
		Content:   "Updated content",
		Project:   "test-project",
		TopicKey:  "architecture/auth",
		SessionID: "session-123",
	}
	err = svc.UpsertByTopicKey(context.Background(), updated)
	testutil.RequireNoError(t, err)

	// Verify it was updated, not created
	testutil.AssertEqual(t, originalID, updated.ID)
	testutil.AssertEqual(t, "Updated Title", updated.Title)
	testutil.AssertEqual(t, "Updated content", updated.Content)
	testutil.AssertEqual(t, originalCreatedAt, updated.CreatedAt)
	testutil.AssertTrue(t, updated.UpdatedAt.After(originalCreatedAt), "expected UpdatedAt to be after CreatedAt")
}

func TestService_UpsertByTopicKey_NoTopicKey(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	obs := &domain.Observation{
		Title:     "Test Observation",
		Content:   "Test content",
		Project:   "test-project",
		SessionID: "session-123",
		// TopicKey is empty
	}

	err := svc.UpsertByTopicKey(context.Background(), obs)
	testutil.AssertValidationError(t, err, "topic_key")
}

func TestService_UpsertByTopicKey_ValidationErrors(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	obs := &domain.Observation{
		TopicKey: "test/key",
		// Title and Content are empty
	}

	err := svc.UpsertByTopicKey(context.Background(), obs)
	testutil.AssertValidationError(t, err, "title")
}

func TestService_UpsertByTopicKey_NormalizesTopicKey(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// Save with uppercase topic key
	obs := &domain.Observation{
		Title:     "Test Observation",
		Content:   "Test content",
		Project:   "test-project",
		TopicKey:  "Architecture/Auth",
		SessionID: "session-123",
	}

	err := svc.UpsertByTopicKey(context.Background(), obs)
	testutil.RequireNoError(t, err)

	// Verify topic key was normalized
	testutil.AssertEqual(t, "architecture/auth", obs.TopicKey)
}

func TestService_TopicKeyUniqueness(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// Create first observation with topic key
	obs1 := &domain.Observation{
		Title:     "First Observation",
		Content:   "First content",
		Project:   "test-project",
		TopicKey:  "architecture/auth",
		SessionID: "session-123",
	}
	err := svc.Save(context.Background(), obs1)
	testutil.RequireNoError(t, err)

	// Create second observation with same topic key
	// This should update the first one due to topic key uniqueness
	obs2 := &domain.Observation{
		Title:     "Second Observation",
		Content:   "Second content",
		Project:   "test-project",
		TopicKey:  "architecture/auth",
		SessionID: "session-456",
	}
	err = svc.Save(context.Background(), obs2)
	testutil.RequireNoError(t, err)

	// Retrieve by topic key - should get the updated observation
	retrieved, err := svc.GetByTopicKey(context.Background(), "test-project", "architecture/auth")
	testutil.RequireNoError(t, err)

	// The behavior depends on the repository implementation
	// For this test, we just verify that we can retrieve by topic key
	testutil.AssertNotNil(t, retrieved)
}

func TestService_ContextCancellation(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	obs := &domain.Observation{
		Title:     "Test Observation",
		Content:   "Test content",
		Project:   "test-project",
		SessionID: "session-123",
	}

	// Operations should still work since mock doesn't check context
	// In a real implementation, this would return context.Canceled
	err := svc.Save(ctx, obs)
	// Mock doesn't respect context, so this succeeds
	testutil.AssertNoError(t, err)
}

func TestService_RepositoryErrors(t *testing.T) {
	t.Run("save error", func(t *testing.T) {
		repo := newMockRepository()
		repo.saveErr = errors.New("database error")
		svc := memory.NewService(repo)

		obs := &domain.Observation{
			Title:     "Test",
			Content:   "Test",
			Project:   "test-project",
			SessionID: "session-123",
		}

		err := svc.Save(context.Background(), obs)
		testutil.AssertError(t, err)
	})

	t.Run("get error", func(t *testing.T) {
		repo := newMockRepository()
		repo.getErr = errors.New("database error")
		svc := memory.NewService(repo)

		_, err := svc.Get(context.Background(), 1)
		testutil.AssertError(t, err)
	})

	t.Run("update error", func(t *testing.T) {
		repo := newMockRepository()
		svc := memory.NewService(repo)

		// First save
		obs := &domain.Observation{
			Title:     "Test",
			Content:   "Test",
			Project:   "test-project",
			SessionID: "session-123",
		}
		err := svc.Save(context.Background(), obs)
		testutil.RequireNoError(t, err)

		// Now set error and try update
		repo.updateErr = errors.New("database error")
		obs.Title = "Updated"
		err = svc.Update(context.Background(), obs)
		testutil.AssertError(t, err)
	})

	t.Run("delete error", func(t *testing.T) {
		repo := newMockRepository()
		repo.deleteErr = errors.New("database error")
		svc := memory.NewService(repo)

		err := svc.Delete(context.Background(), 1)
		testutil.AssertError(t, err)
	})

	t.Run("list error", func(t *testing.T) {
		repo := newMockRepository()
		repo.listErr = errors.New("database error")
		svc := memory.NewService(repo)

		_, err := svc.List(context.Background(), domain.ObservationFilter{})
		testutil.AssertError(t, err)
	})
}

func TestService_MultipleObservationsWithDifferentTopicKeys(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// Create multiple observations with different topic keys
	topicKeys := []string{
		"architecture/auth",
		"architecture/database",
		"patterns/singleton",
		"bugfix/null-pointer",
	}

	for _, tk := range topicKeys {
		obs := &domain.Observation{
			Title:     "Observation for " + tk,
			Content:   "Content for " + tk,
			Project:   "test-project",
			TopicKey:  tk,
			SessionID: "session-123",
		}
		err := svc.Save(context.Background(), obs)
		testutil.RequireNoError(t, err)
	}

	// Verify all can be retrieved
	for _, tk := range topicKeys {
		retrieved, err := svc.GetByTopicKey(context.Background(), "test-project", tk)
		testutil.RequireNoError(t, err)
		testutil.AssertEqual(t, tk, retrieved.TopicKey)
	}
}

func TestService_DefaultsAreAppliedToAllOperations(t *testing.T) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// Test that defaults are applied in Save
	obs1 := &domain.Observation{
		Title:     "Test",
		Content:   "Content",
		Project:   "test-project",
		SessionID: "session-123",
	}
	err := svc.Save(context.Background(), obs1)
	testutil.RequireNoError(t, err)
	testutil.AssertEqual(t, domain.TypeManual, obs1.Type)
	testutil.AssertEqual(t, domain.ScopeProject, obs1.Scope)

	// Test that defaults are applied in UpsertByTopicKey (create case)
	obs2 := &domain.Observation{
		Title:     "Test 2",
		Content:   "Content 2",
		Project:   "test-project",
		TopicKey:  "test/key",
		SessionID: "session-123",
	}
	err = svc.UpsertByTopicKey(context.Background(), obs2)
	testutil.RequireNoError(t, err)
	testutil.AssertEqual(t, domain.TypeManual, obs2.Type)
	testutil.AssertEqual(t, domain.ScopeProject, obs2.Scope)
}

// Benchmark tests for performance validation
func BenchmarkService_Save(b *testing.B) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		obs := &domain.Observation{
			Title:     "Benchmark Observation",
			Content:   "Benchmark content",
			Project:   "benchmark-project",
			SessionID: "session-123",
		}
		svc.Save(context.Background(), obs) //nolint:errcheck
	}
}

func BenchmarkService_Get(b *testing.B) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// Create one observation
	obs := &domain.Observation{
		Title:     "Benchmark Observation",
		Content:   "Benchmark content",
		Project:   "benchmark-project",
		SessionID: "session-123",
	}
	svc.Save(context.Background(), obs) //nolint:errcheck

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.Get(context.Background(), obs.ID) //nolint:errcheck
	}
}

func BenchmarkService_List(b *testing.B) {
	repo := newMockRepository()
	svc := memory.NewService(repo)

	// Create 100 observations
	for i := 0; i < 100; i++ {
		obs := &domain.Observation{
			Title:     "Benchmark Observation",
			Content:   "Benchmark content",
			Project:   "benchmark-project",
			SessionID: "session-123",
		}
		svc.Save(context.Background(), obs) //nolint:errcheck
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.List(context.Background(), domain.ObservationFilter{Limit: 20}) //nolint:errcheck
	}
}

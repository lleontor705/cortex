package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// mockSessionRepository is a mock implementation of SessionRepository for testing.
type mockSessionRepository struct {
	sessions map[string]*domain.Session
	err      error
}

func newMockRepository() *mockSessionRepository {
	return &mockSessionRepository{
		sessions: make(map[string]*domain.Session),
	}
}

func (m *mockSessionRepository) Create(ctx context.Context, session *domain.Session) error {
	if m.err != nil {
		return m.err
	}
	m.sessions[session.ID] = session
	return nil
}

func (m *mockSessionRepository) GetByID(ctx context.Context, id string) (*domain.Session, error) {
	if m.err != nil {
		return nil, m.err
	}
	session, ok := m.sessions[id]
	if !ok {
		return nil, &domain.NotFoundError{Type: "session", ID: id}
	}
	return session, nil
}

func (m *mockSessionRepository) End(ctx context.Context, id string, summary string) error {
	if m.err != nil {
		return m.err
	}
	session, ok := m.sessions[id]
	if !ok {
		return &domain.NotFoundError{Type: "session", ID: id}
	}
	now := time.Now()
	session.EndedAt = &now
	session.Summary = summary
	return nil
}

func (m *mockSessionRepository) List(ctx context.Context, project string) ([]*domain.Session, error) {
	if m.err != nil {
		return nil, m.err
	}
	var result []*domain.Session
	for _, session := range m.sessions {
		if project == "" || session.Project == project {
			result = append(result, session)
		}
	}
	// Sort by most recent first (simple implementation for tests)
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].StartedAt.Before(result[j].StartedAt) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result, nil
}

func TestNewService(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	if service == nil {
		t.Fatal("NewService returned nil")
	}
	if service.repo == nil {
		t.Fatal("Service repo is nil")
	}
}

func TestStart_ValidInput(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	ctx := context.Background()

	session, err := service.Start(ctx, "", "test-project", "/test/dir")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if session.ID == "" {
		t.Error("Session ID should be generated")
	}
	if session.Project != "test-project" {
		t.Errorf("Project = %q, want %q", session.Project, "test-project")
	}
	if session.Directory != "/test/dir" {
		t.Errorf("Directory = %q, want %q", session.Directory, "/test/dir")
	}
	if session.EndedAt != nil {
		t.Error("New session should not have EndedAt")
	}
	if session.Summary != "" {
		t.Error("New session should not have summary")
	}
	if session.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}
}

func TestStart_WithProvidedID(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	ctx := context.Background()

	session, err := service.Start(ctx, "custom-id", "test-project", "/test/dir")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if session.ID != "custom-id" {
		t.Errorf("ID = %q, want %q", session.ID, "custom-id")
	}
}

func TestStart_MissingProject(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	ctx := context.Background()

	_, err := service.Start(ctx, "", "", "/test/dir")
	if err == nil {
		t.Fatal("Expected error for missing project")
	}

	var valErr *domain.ValidationError
	if !errors.As(err, &valErr) {
		t.Errorf("Expected ValidationError, got %T", err)
	}
	if valErr.Field != "project" {
		t.Errorf("Field = %q, want %q", valErr.Field, "project")
	}
}

func TestStart_MissingDirectory(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	ctx := context.Background()

	_, err := service.Start(ctx, "", "test-project", "")
	if err == nil {
		t.Fatal("Expected error for missing directory")
	}

	var valErr *domain.ValidationError
	if !errors.As(err, &valErr) {
		t.Errorf("Expected ValidationError, got %T", err)
	}
	if valErr.Field != "directory" {
		t.Errorf("Field = %q, want %q", valErr.Field, "directory")
	}
}

func TestGet_ExistingSession(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	ctx := context.Background()

	// Create a session first
	created, _ := service.Start(ctx, "test-id", "test-project", "/test/dir")

	// Retrieve it
	session, err := service.Get(ctx, "test-id")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if session.ID != created.ID {
		t.Errorf("ID = %q, want %q", session.ID, created.ID)
	}
}

func TestGet_NonexistentSession(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	ctx := context.Background()

	_, err := service.Get(ctx, "nonexistent")
	if err == nil {
		t.Fatal("Expected error for nonexistent session")
	}

	var notFoundErr *domain.NotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Errorf("Expected NotFoundError, got %T", err)
	}
}

func TestGet_EmptyID(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	ctx := context.Background()

	_, err := service.Get(ctx, "")
	if err == nil {
		t.Fatal("Expected error for empty ID")
	}

	var valErr *domain.ValidationError
	if !errors.As(err, &valErr) {
		t.Errorf("Expected ValidationError, got %T", err)
	}
}

func TestEnd_ActiveSession(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	ctx := context.Background()

	// Create a session
	_, _ = service.Start(ctx, "test-id", "test-project", "/test/dir")

	// End it
	err := service.End(ctx, "test-id", "Test summary")
	if err != nil {
		t.Fatalf("End failed: %v", err)
	}

	// Verify it's ended
	session, _ := service.Get(ctx, "test-id")
	if session.EndedAt == nil {
		t.Error("Session should have EndedAt set")
	}
	if session.Summary != "Test summary" {
		t.Errorf("Summary = %q, want %q", session.Summary, "Test summary")
	}
}

func TestEnd_AlreadyEnded(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	ctx := context.Background()

	// Create and end a session
	_, _ = service.Start(ctx, "test-id", "test-project", "/test/dir")
	_ = service.End(ctx, "test-id", "First summary")

	// Try to end it again
	err := service.End(ctx, "test-id", "Second summary")
	if err == nil {
		t.Fatal("Expected error for already ended session")
	}

	if !errors.Is(err, domain.ErrSessionEnded) {
		t.Errorf("Expected ErrSessionEnded, got %v", err)
	}
}

func TestEnd_EmptyID(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	ctx := context.Background()

	err := service.End(ctx, "", "summary")
	if err == nil {
		t.Fatal("Expected error for empty ID")
	}

	var valErr *domain.ValidationError
	if !errors.As(err, &valErr) {
		t.Errorf("Expected ValidationError, got %T", err)
	}
}

func TestList_AllSessions(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	ctx := context.Background()

	// Create multiple sessions
	_, _ = service.Start(ctx, "id1", "project1", "/dir1")
	time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	_, _ = service.Start(ctx, "id2", "project2", "/dir2")

	// List all
	sessions, err := service.List(ctx, "")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(sessions) != 2 {
		t.Errorf("Got %d sessions, want 2", len(sessions))
	}
}

func TestList_FilterByProject(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	ctx := context.Background()

	// Create sessions for different projects
	_, _ = service.Start(ctx, "id1", "project1", "/dir1")
	_, _ = service.Start(ctx, "id2", "project2", "/dir2")
	_, _ = service.Start(ctx, "id3", "project1", "/dir3")

	// List project1 sessions
	sessions, err := service.List(ctx, "project1")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(sessions) != 2 {
		t.Errorf("Got %d sessions, want 2", len(sessions))
	}
	for _, s := range sessions {
		if s.Project != "project1" {
			t.Errorf("Project = %q, want %q", s.Project, "project1")
		}
	}
}

func TestGetCurrent_ActiveSessionExists(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	ctx := context.Background()

	// Create an active session
	_, _ = service.Start(ctx, "active-id", "test-project", "/test/dir")
	// Create and end another session
	_, _ = service.Start(ctx, "ended-id", "test-project", "/test/dir2")
	_ = service.End(ctx, "ended-id", "Ended")

	// Get current session
	session, err := service.GetCurrent(ctx, "test-project")
	if err != nil {
		t.Fatalf("GetCurrent failed: %v", err)
	}

	if session.ID != "active-id" {
		t.Errorf("ID = %q, want %q", session.ID, "active-id")
	}
	if session.EndedAt != nil {
		t.Error("Current session should be active")
	}
}

func TestGetCurrent_NoActiveSession(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	ctx := context.Background()

	// Create and end all sessions
	_, _ = service.Start(ctx, "id1", "test-project", "/dir1")
	_ = service.End(ctx, "id1", "Ended")

	// Try to get current session
	_, err := service.GetCurrent(ctx, "test-project")
	if err == nil {
		t.Fatal("Expected error for no active session")
	}

	var notFoundErr *domain.NotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Errorf("Expected NotFoundError, got %T", err)
	}
}

func TestGetCurrent_EmptyProject(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	ctx := context.Background()

	_, err := service.GetCurrent(ctx, "")
	if err == nil {
		t.Fatal("Expected error for empty project")
	}

	var valErr *domain.ValidationError
	if !errors.As(err, &valErr) {
		t.Errorf("Expected ValidationError, got %T", err)
	}
}

func TestIsActive_ActiveSession(t *testing.T) {
	service := NewService(newMockRepository())

	session := &domain.Session{
		ID:        "test-id",
		StartedAt: time.Now(),
		EndedAt:   nil,
	}

	if !service.IsActive(session) {
		t.Error("Session with nil EndedAt should be active")
	}
}

func TestIsActive_EndedSession(t *testing.T) {
	service := NewService(newMockRepository())
	now := time.Now()

	session := &domain.Session{
		ID:        "test-id",
		StartedAt: time.Now().Add(-1 * time.Hour),
		EndedAt:   &now,
	}

	if service.IsActive(session) {
		t.Error("Session with EndedAt should not be active")
	}
}

func TestIsActive_NilSession(t *testing.T) {
	service := NewService(newMockRepository())

	if service.IsActive(nil) {
		t.Error("Nil session should not be active")
	}
}

func TestDuration_ActiveSession(t *testing.T) {
	service := NewService(newMockRepository())

	startTime := time.Now().Add(-1 * time.Hour)
	session := &domain.Session{
		ID:        "test-id",
		StartedAt: startTime,
		EndedAt:   nil,
	}

	duration := service.Duration(session)
	if duration < time.Hour {
		t.Errorf("Duration = %v, want at least 1 hour", duration)
	}
}

func TestDuration_EndedSession(t *testing.T) {
	service := NewService(newMockRepository())

	startTime := time.Now().Add(-2 * time.Hour)
	endTime := startTime.Add(1 * time.Hour)
	session := &domain.Session{
		ID:        "test-id",
		StartedAt: startTime,
		EndedAt:   &endTime,
	}

	duration := service.Duration(session)
	expected := time.Hour
	// Allow 1 second tolerance
	if duration < expected-time.Second || duration > expected+time.Second {
		t.Errorf("Duration = %v, want %v", duration, expected)
	}
}

func TestDuration_NilSession(t *testing.T) {
	service := NewService(newMockRepository())

	duration := service.Duration(nil)
	if duration != 0 {
		t.Errorf("Duration = %v, want 0", duration)
	}
}

func TestRepositoryError(t *testing.T) {
	repo := newMockRepository()
	repo.err = errors.New("database error")
	service := NewService(repo)
	ctx := context.Background()

	_, err := service.Start(ctx, "", "test-project", "/test/dir")
	if err == nil {
		t.Fatal("Expected repository error")
	}
	if !errors.Is(err, repo.err) {
		t.Errorf("Error = %v, want %v", err, repo.err)
	}
}

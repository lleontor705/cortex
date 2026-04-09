// Package session provides business logic for session management.
//
// Sessions group related observations during coding work and track
// when work started and ended. This service implements the core
// session lifecycle operations: start, end, get, list, and current.
package session

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/internal/domain"
)

// Service provides session management operations.
type Service struct {
	repo domain.SessionRepository
}

// NewService creates a new session service with the given repository.
func NewService(repo domain.SessionRepository) *Service {
	return &Service{
		repo: repo,
	}
}

// Start creates a new coding session.
//
// Business rules:
//   - ID: If empty, generates a UUID
//   - Project: Required, must not be empty
//   - Directory: Required, must not be empty
//   - StartedAt: Set to current time
//
// Returns an error if project or directory is empty.
func (s *Service) Start(ctx context.Context, id, project, directory string) (*domain.Session, error) {
	// Validate required fields
	if project == "" {
		return nil, &domain.ValidationError{
			Field:   "project",
			Message: "project name is required",
		}
	}
	if directory == "" {
		return nil, &domain.ValidationError{
			Field:   "directory",
			Message: "directory path is required",
		}
	}

	// Generate ID if not provided
	if id == "" {
		id = uuid.New().String()
	}

	// Create session
	session := &domain.Session{
		ID:        id,
		Project:   project,
		Directory: directory,
		StartedAt: time.Now(),
		EndedAt:   nil,
		Summary:   "",
	}

	// Persist session
	if err := s.repo.Create(ctx, session); err != nil {
		return nil, err
	}

	return session, nil
}

// Get retrieves a session by its ID.
//
// Returns ErrNotFound if the session does not exist.
func (s *Service) Get(ctx context.Context, id string) (*domain.Session, error) {
	if id == "" {
		return nil, &domain.ValidationError{
			Field:   "id",
			Message: "session ID is required",
		}
	}

	return s.repo.GetByID(ctx, id)
}

// End marks a session as completed with an optional summary.
//
// Business rules:
//   - Can only end active sessions (EndedAt is null)
//   - Summary is optional (empty string is valid)
//   - Sets EndedAt to current time
//
// Returns ErrSessionEnded if the session has already ended.
func (s *Service) End(ctx context.Context, id, summary string) error {
	if id == "" {
		return &domain.ValidationError{
			Field:   "id",
			Message: "session ID is required",
		}
	}

	// Get current session to check if it's active
	session, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Check if session is already ended
	if session.EndedAt != nil {
		return domain.ErrSessionEnded
	}

	// End the session
	return s.repo.End(ctx, id, summary)
}

// List retrieves sessions for a project, ordered by most recent first.
//
// If project is empty, returns all sessions.
func (s *Service) List(ctx context.Context, project string) ([]*domain.Session, error) {
	return s.repo.List(ctx, project)
}

// GetCurrent retrieves the most recent active session for a project.
//
// An active session is one where EndedAt is null.
// Returns ErrNotFound if no active session exists for the project.
func (s *Service) GetCurrent(ctx context.Context, project string) (*domain.Session, error) {
	if project == "" {
		return nil, &domain.ValidationError{
			Field:   "project",
			Message: "project name is required",
		}
	}

	// List all sessions for the project
	sessions, err := s.repo.List(ctx, project)
	if err != nil {
		return nil, err
	}

	// Find the first active session (most recent)
	for _, session := range sessions {
		if session.EndedAt == nil {
			return session, nil
		}
	}

	// No active session found
	return nil, &domain.NotFoundError{
		Type: "active session",
		ID:   project,
	}
}

// IsActive checks if a session is currently active.
//
// A session is active if EndedAt is null.
func (s *Service) IsActive(session *domain.Session) bool {
	if session == nil {
		return false
	}
	return session.EndedAt == nil
}

// Duration returns the duration of a session.
//
// For active sessions, returns duration from start to now.
// For ended sessions, returns duration from start to end.
func (s *Service) Duration(session *domain.Session) time.Duration {
	if session == nil {
		return 0
	}

	if session.EndedAt == nil {
		return time.Since(session.StartedAt)
	}

	return session.EndedAt.Sub(session.StartedAt)
}

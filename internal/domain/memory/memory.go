// Package memory provides the business logic layer for observation management.
//
// The Service implements domain-level validation, default value handling, and
// orchestrates persistence operations through the ObservationRepository interface.
// This is the primary entry point for all observation-related operations.
package memory

import (
	"context"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// Service provides business logic for observation management.
// It wraps an ObservationRepository and adds validation, defaults, and
// domain rules enforcement.
type Service struct {
	repo domain.ObservationRepository
}

// NewService creates a new memory Service with the given repository.
func NewService(repo domain.ObservationRepository) *Service {
	return &Service{repo: repo}
}

// Save creates or updates an observation.
//
// Business Rules:
//   - Title and Content are required (validation error if empty)
//   - Type defaults to "manual" if empty
//   - Scope defaults to "project" if empty
//   - CreatedAt is set to now for new observations
//   - UpdatedAt is always set to now
func (s *Service) Save(ctx context.Context, obs *domain.Observation) error {
	if err := s.validate(obs); err != nil {
		return err
	}

	s.applyDefaults(obs)
	s.setTimestamps(obs, true)

	return s.repo.Save(ctx, obs)
}

// Get retrieves an observation by its ID.
// Returns ErrNotFound if the observation doesn't exist.
func (s *Service) Get(ctx context.Context, id int64) (*domain.Observation, error) {
	return s.repo.GetByID(ctx, id)
}

// Update modifies an existing observation.
//
// Business Rules:
//   - Title and Content are required (validation error if empty)
//   - UpdatedAt is always set to now
//   - CreatedAt is preserved
func (s *Service) Update(ctx context.Context, obs *domain.Observation) error {
	if err := s.validate(obs); err != nil {
		return err
	}

	// Get existing to preserve CreatedAt
	existing, err := s.repo.GetByID(ctx, obs.ID)
	if err != nil {
		return err
	}

	obs.CreatedAt = existing.CreatedAt
	obs.UpdatedAt = time.Now()

	return s.repo.Update(ctx, obs)
}

// Delete removes an observation by ID.
// This performs a soft delete by default.
func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

// List retrieves observations based on filter criteria.
// An empty filter returns all observations up to the default limit.
func (s *Service) List(ctx context.Context, filter domain.ObservationFilter) ([]*domain.Observation, error) {
	// Apply default limit if not specified
	if filter.Limit == 0 {
		filter.Limit = 20
	}

	return s.repo.List(ctx, filter)
}

// GetByTopicKey retrieves an observation by its topic key within a project.
// This is used for upsert operations where topic_key enforces uniqueness.
// Returns ErrNotFound if no matching observation exists.
func (s *Service) GetByTopicKey(ctx context.Context, project, topicKey string) (*domain.Observation, error) {
	if topicKey == "" {
		return nil, &domain.ValidationError{
			Field:   "topic_key",
			Message: "cannot be empty",
		}
	}

	return s.repo.GetByTopicKey(ctx, project, topicKey)
}

// UpsertByTopicKey creates or updates an observation based on its topic key.
//
// Business Rules:
//   - If an observation with the same topic_key exists in the project, update it
//   - Otherwise, create a new observation
//   - Topic key is normalized (lowercase, trimmed)
//   - All validation rules from Save apply
func (s *Service) UpsertByTopicKey(ctx context.Context, obs *domain.Observation) error {
	if obs.TopicKey == "" {
		return &domain.ValidationError{
			Field:   "topic_key",
			Message: "required for upsert operations",
		}
	}

	if err := s.validate(obs); err != nil {
		return err
	}

	// Normalize topic key
	obs.TopicKey = normalizeTopicKey(obs.TopicKey)

	// Check for existing observation
	existing, err := s.repo.GetByTopicKey(ctx, obs.Project, obs.TopicKey)
	if err != nil {
		if domain.IsNotFoundError(err) {
			// Create new observation
			s.applyDefaults(obs)
			s.setTimestamps(obs, true)
			return s.repo.Save(ctx, obs)
		}
		return err
	}

	// Update existing observation
	obs.ID = existing.ID
	obs.CreatedAt = existing.CreatedAt
	obs.UpdatedAt = time.Now()

	return s.repo.Update(ctx, obs)
}

// validate checks that required fields are present.
func (s *Service) validate(obs *domain.Observation) error {
	if obs.Title == "" {
		return &domain.ValidationError{
			Field:   "title",
			Message: "is required",
		}
	}

	if obs.Content == "" {
		return &domain.ValidationError{
			Field:   "content",
			Message: "is required",
		}
	}

	return nil
}

// applyDefaults sets default values for optional fields.
func (s *Service) applyDefaults(obs *domain.Observation) {
	if obs.Type == "" {
		obs.Type = domain.TypeManual
	}

	if obs.Scope == "" {
		obs.Scope = domain.ScopeProject
	}

	if obs.Confidence < 0 || obs.Confidence > 1 {
		obs.Confidence = 1.0
	}

	if obs.Source == "" {
		obs.Source = domain.SourceManual
	}
}

// setTimestamps sets CreatedAt and UpdatedAt.
// For new observations, both are set to now.
// For updates, only UpdatedAt is changed (handled in Update method).
func (s *Service) setTimestamps(obs *domain.Observation, isNew bool) {
	now := time.Now()

	if isNew || obs.CreatedAt.IsZero() {
		obs.CreatedAt = now
	}

	obs.UpdatedAt = now
}

// normalizeTopicKey normalizes a topic key for consistent lookups.
// Topic keys are lowercased and trimmed of whitespace.
func normalizeTopicKey(key string) string {
	// Simple normalization: lowercase and trim
	// In a real implementation, this might also:
	// - Replace spaces with underscores
	// - Remove special characters
	// - Validate format
	return strings.TrimSpace(strings.ToLower(key))
}

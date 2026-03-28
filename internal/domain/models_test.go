package domain_test

import (
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

func TestObservationModel(t *testing.T) {
	now := time.Now()
	obs := &domain.Observation{
		ID:        1,
		Title:     "Test Observation",
		Content:   "This is a test observation",
		Type:      domain.TypeManual,
		Project:   "test-project",
		Scope:     domain.ScopeProject,
		SessionID: "session-123",
		TopicKey:  "test/topic",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if obs.ID != 1 {
		t.Errorf("expected ID 1, got %d", obs.ID)
	}
	if obs.Type != "manual" {
		t.Errorf("expected type 'manual', got %s", obs.Type)
	}
	if obs.Scope != "project" {
		t.Errorf("expected scope 'project', got %s", obs.Scope)
	}
}

func TestSessionModel(t *testing.T) {
	now := time.Now()
	sess := &domain.Session{
		ID:        "session-123",
		Project:   "test-project",
		Directory: "/path/to/project",
		StartedAt: now,
		Summary:   "Test session",
	}

	if sess.ID != "session-123" {
		t.Errorf("expected ID 'session-123', got %s", sess.ID)
	}
	if sess.EndedAt != nil {
		t.Error("expected EndedAt to be nil for active session")
	}
}

func TestEdgeModel(t *testing.T) {
	now := time.Now()
	edge := &domain.Edge{
		ID:           1,
		FromObsID:    100,
		ToObsID:      200,
		RelationType: domain.RelationReferences,
		Weight:       0.85,
		CreatedAt:    now,
	}

	if edge.RelationType != "references" {
		t.Errorf("expected relation type 'references', got %s", edge.RelationType)
	}
	if edge.Weight < 0 || edge.Weight > 1 {
		t.Errorf("expected weight between 0 and 1, got %f", edge.Weight)
	}
}

func TestPromptModel(t *testing.T) {
	now := time.Now()
	prompt := &domain.Prompt{
		ID:        1,
		Content:   "How do I implement authentication?",
		Project:   "test-project",
		SessionID: "session-123",
		CreatedAt: now,
	}

	if prompt.Content == "" {
		t.Error("expected non-empty prompt content")
	}
}

func TestImportanceScoreModel(t *testing.T) {
	now := time.Now()
	score := &domain.ImportanceScore{
		ObservationID: 100,
		Score:         0.75,
		AccessCount:   10,
		LastAccessed:  now,
		UpdatedAt:     now,
	}

	if score.Score < 0 || score.Score > 1 {
		t.Errorf("expected score between 0 and 1, got %f", score.Score)
	}
	if score.AccessCount != 10 {
		t.Errorf("expected access count 10, got %d", score.AccessCount)
	}
}

func TestDomainConstants(t *testing.T) {
	// Test observation type constants
	if domain.TypeManual != "manual" {
		t.Errorf("expected TypeManual='manual', got %s", domain.TypeManual)
	}
	if domain.TypeDecision != "decision" {
		t.Errorf("expected TypeDecision='decision', got %s", domain.TypeDecision)
	}

	// Test scope constants
	if domain.ScopeProject != "project" {
		t.Errorf("expected ScopeProject='project', got %s", domain.ScopeProject)
	}
	if domain.ScopePersonal != "personal" {
		t.Errorf("expected ScopePersonal='personal', got %s", domain.ScopePersonal)
	}

	// Test relation type constants
	if domain.RelationReferences != "references" {
		t.Errorf("expected RelationReferences='references', got %s", domain.RelationReferences)
	}
	if domain.RelationRelatesTo != "relates_to" {
		t.Errorf("expected RelationRelatesTo='relates_to', got %s", domain.RelationRelatesTo)
	}
}

func TestDomainErrors(t *testing.T) {
	// Test NotFoundError
	notFound := &domain.NotFoundError{Type: "observation", ID: int64(123)}
	if !domain.IsNotFoundError(notFound) {
		t.Error("expected IsNotFoundError to return true")
	}
	if notFound.Error() != "observation with ID 123 not found" {
		t.Errorf("unexpected error message: %s", notFound.Error())
	}

	// Test ValidationError
	validation := &domain.ValidationError{Field: "title", Message: "cannot be empty"}
	if !domain.IsValidationError(validation) {
		t.Error("expected IsValidationError to return true")
	}

	// Test ConflictError
	conflict := &domain.ConflictError{Entity: "session", Reason: "already ended"}
	if !domain.IsConflictError(conflict) {
		t.Error("expected IsConflictError to return true")
	}
}

func TestFilterTypes(t *testing.T) {
	filter := domain.ObservationFilter{
		Project: "test-project",
		Scope:   domain.ScopeProject,
		Type:    domain.TypeDecision,
		Limit:   10,
		Offset:  0,
	}

	if filter.Project != "test-project" {
		t.Errorf("expected project 'test-project', got %s", filter.Project)
	}
	if filter.Limit != 10 {
		t.Errorf("expected limit 10, got %d", filter.Limit)
	}
}

func TestSearchOptions(t *testing.T) {
	opts := domain.SearchOptions{
		Query:   "authentication",
		Type:    domain.TypeDecision,
		Project: "test-project",
		Scope:   domain.ScopeProject,
		Limit:   20,
	}

	if opts.Query != "authentication" {
		t.Errorf("expected query 'authentication', got %s", opts.Query)
	}
	if opts.Limit != 20 {
		t.Errorf("expected limit 20, got %d", opts.Limit)
	}
}

func TestSearchResult(t *testing.T) {
	now := time.Now()
	result := domain.SearchResult{
		Observation: domain.Observation{
			ID:        1,
			Title:     "Test",
			Content:   "Content",
			Type:      domain.TypeManual,
			CreatedAt: now,
			UpdatedAt: now,
		},
		Rank: 0.95,
	}

	if result.Rank != 0.95 {
		t.Errorf("expected rank 0.95, got %f", result.Rank)
	}
	if result.ID != 1 {
		t.Errorf("expected ID 1, got %d", result.ID)
	}
}

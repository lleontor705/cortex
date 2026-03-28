package testutil

import (
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// Fixtures provides test data factories for creating domain objects.
// It uses the functional options pattern to allow overriding default values.
//
// Example:
//
//	fixtures := &testutil.Fixtures{}
//
//	// Create observation with defaults
//	obs := fixtures.Observation()
//
//	// Create observation with custom title
//	obs := fixtures.Observation(func(o *domain.Observation) {
//	    o.Title = "Custom Title"
//	    o.Project = "my-project"
//	})
type Fixtures struct{}

// NewFixtures creates a new Fixtures instance.
func NewFixtures() *Fixtures {
	return &Fixtures{}
}

// Observation creates a test observation with sensible defaults.
// Override any field by passing functional options.
//
// Default values:
//   - ID: 1
//   - Title: "Test Observation"
//   - Content: "Test content for observation"
//   - Type: "manual"
//   - Project: "test-project"
//   - Scope: "project"
//   - SessionID: "test-session-123"
//   - CreatedAt/UpdatedAt: time.Now()
func (f *Fixtures) Observation(overrides ...func(*domain.Observation)) *domain.Observation {
	now := time.Now()
	obs := &domain.Observation{
		ID:        1,
		Title:     "Test Observation",
		Content:   "Test content for observation",
		Type:      domain.TypeManual,
		Project:   "test-project",
		Scope:     domain.ScopeProject,
		SessionID: "test-session-123",
		TopicKey:  "",
		CreatedAt: now,
		UpdatedAt: now,
	}

	for _, override := range overrides {
		override(obs)
	}

	return obs
}

// ObservationDecision creates a test observation with type "decision".
func (f *Fixtures) ObservationDecision(overrides ...func(*domain.Observation)) *domain.Observation {
	return f.Observation(append(overrides, func(o *domain.Observation) {
		o.Type = domain.TypeDecision
		o.Title = "Architecture Decision"
		o.Content = "We decided to use SQLite for storage"
	})...)
}

// ObservationBugfix creates a test observation with type "bugfix".
func (f *Fixtures) ObservationBugfix(overrides ...func(*domain.Observation)) *domain.Observation {
	return f.Observation(append(overrides, func(o *domain.Observation) {
		o.Type = domain.TypeBugfix
		o.Title = "Fixed N+1 Query Bug"
		o.Content = "Added eager loading to prevent N+1 queries"
	})...)
}

// ObservationPattern creates a test observation with type "pattern".
func (f *Fixtures) ObservationPattern(overrides ...func(*domain.Observation)) *domain.Observation {
	return f.Observation(append(overrides, func(o *domain.Observation) {
		o.Type = domain.TypePattern
		o.Title = "Repository Pattern"
		o.Content = "Using repository pattern for data access"
	})...)
}

// ObservationDiscovery creates a test observation with type "discovery".
func (f *Fixtures) ObservationDiscovery(overrides ...func(*domain.Observation)) *domain.Observation {
	return f.Observation(append(overrides, func(o *domain.Observation) {
		o.Type = domain.TypeDiscovery
		o.Title = "API Rate Limit"
		o.Content = "API has rate limit of 100 requests/minute"
	})...)
}

// ObservationToolUse creates a test observation with type "tool_use".
func (f *Fixtures) ObservationToolUse(overrides ...func(*domain.Observation)) *domain.Observation {
	return f.Observation(append(overrides, func(o *domain.Observation) {
		o.Type = domain.TypeToolUse
		o.Title = "Used grep tool"
		o.Content = "Searched for 'TODO' in codebase"
	})...)
}

// ObservationConfig creates a test observation with type "config".
func (f *Fixtures) ObservationConfig(overrides ...func(*domain.Observation)) *domain.Observation {
	return f.Observation(append(overrides, func(o *domain.Observation) {
		o.Type = domain.TypeConfig
		o.Title = "Database Configuration"
		o.Content = "Set up connection pool with 25 max connections"
	})...)
}

// ObservationLearning creates a test observation with type "learning".
func (f *Fixtures) ObservationLearning(overrides ...func(*domain.Observation)) *domain.Observation {
	return f.Observation(append(overrides, func(o *domain.Observation) {
		o.Type = domain.TypeLearning
		o.Title = "Go Concurrency Pattern"
		o.Content = "Learned about worker pools in Go"
	})...)
}

// ObservationPersonal creates a test observation with personal scope.
func (f *Fixtures) ObservationPersonal(overrides ...func(*domain.Observation)) *domain.Observation {
	return f.Observation(append(overrides, func(o *domain.Observation) {
		o.Scope = domain.ScopePersonal
	})...)
}

// Session creates a test session with sensible defaults.
// Override any field by passing functional options.
//
// Default values:
//   - ID: "test-session-123"
//   - Project: "test-project"
//   - Directory: "/tmp/test-project"
//   - StartedAt: time.Now()
//   - EndedAt: nil
//   - Summary: ""
func (f *Fixtures) Session(overrides ...func(*domain.Session)) *domain.Session {
	now := time.Now()
	sess := &domain.Session{
		ID:        "test-session-123",
		Project:   "test-project",
		Directory: "/tmp/test-project",
		StartedAt: now,
		EndedAt:   nil,
		Summary:   "",
	}

	for _, override := range overrides {
		override(sess)
	}

	return sess
}

// SessionEnded creates a test session that has already ended.
func (f *Fixtures) SessionEnded(overrides ...func(*domain.Session)) *domain.Session {
	endTime := time.Now().Add(-1 * time.Hour)
	return f.Session(append(overrides, func(s *domain.Session) {
		s.EndedAt = &endTime
		s.Summary = "Completed session"
	})...)
}

// SessionWithSummary creates a test session with a summary.
func (f *Fixtures) SessionWithSummary(summary string, overrides ...func(*domain.Session)) *domain.Session {
	return f.Session(append(overrides, func(s *domain.Session) {
		s.Summary = summary
	})...)
}

// Edge creates a test edge with sensible defaults.
// Override any field by passing functional options.
//
// Default values:
//   - ID: 1
//   - FromObsID: fromID parameter
//   - ToObsID: toID parameter
//   - RelationType: "references"
//   - Weight: 0.8
//   - CreatedAt: time.Now()
func (f *Fixtures) Edge(fromID, toID int64, overrides ...func(*domain.Edge)) *domain.Edge {
	now := time.Now()
	edge := &domain.Edge{
		ID:           1,
		FromObsID:    fromID,
		ToObsID:      toID,
		RelationType: domain.RelationReferences,
		Weight:       0.8,
		CreatedAt:    now,
	}

	for _, override := range overrides {
		override(edge)
	}

	return edge
}

// EdgeRelatesTo creates an edge with "relates_to" relation type.
func (f *Fixtures) EdgeRelatesTo(fromID, toID int64, overrides ...func(*domain.Edge)) *domain.Edge {
	return f.Edge(fromID, toID, append(overrides, func(e *domain.Edge) {
		e.RelationType = domain.RelationRelatesTo
	})...)
}

// EdgeFollows creates an edge with "follows" relation type.
func (f *Fixtures) EdgeFollows(fromID, toID int64, overrides ...func(*domain.Edge)) *domain.Edge {
	return f.Edge(fromID, toID, append(overrides, func(e *domain.Edge) {
		e.RelationType = domain.RelationFollows
	})...)
}

// EdgeContradicts creates an edge with "contradicts" relation type.
func (f *Fixtures) EdgeContradicts(fromID, toID int64, overrides ...func(*domain.Edge)) *domain.Edge {
	return f.Edge(fromID, toID, append(overrides, func(e *domain.Edge) {
		e.RelationType = domain.RelationContradicts
	})...)
}

// EdgeSupersedes creates an edge with "supersedes" relation type.
func (f *Fixtures) EdgeSupersedes(fromID, toID int64, overrides ...func(*domain.Edge)) *domain.Edge {
	return f.Edge(fromID, toID, append(overrides, func(e *domain.Edge) {
		e.RelationType = domain.RelationSupersedes
	})...)
}

// Prompt creates a test prompt with sensible defaults.
// Override any field by passing functional options.
//
// Default values:
//   - ID: 1
//   - Content: "Test prompt content"
//   - Project: "test-project"
//   - SessionID: "test-session-123"
//   - CreatedAt: time.Now()
func (f *Fixtures) Prompt(overrides ...func(*domain.Prompt)) *domain.Prompt {
	now := time.Now()
	prompt := &domain.Prompt{
		ID:        1,
		Content:   "Test prompt content",
		Project:   "test-project",
		SessionID: "test-session-123",
		CreatedAt: now,
	}

	for _, override := range overrides {
		override(prompt)
	}

	return prompt
}

// PromptWithContent creates a prompt with custom content.
func (f *Fixtures) PromptWithContent(content string, overrides ...func(*domain.Prompt)) *domain.Prompt {
	return f.Prompt(append(overrides, func(p *domain.Prompt) {
		p.Content = content
	})...)
}

// ImportanceScore creates a test importance score with sensible defaults.
// Override any field by passing functional options.
//
// Default values:
//   - ObservationID: obsID parameter
//   - Score: 0.75
//   - AccessCount: 5
//   - LastAccessed: time.Now()
//   - UpdatedAt: time.Now()
func (f *Fixtures) ImportanceScore(obsID int64, overrides ...func(*domain.ImportanceScore)) *domain.ImportanceScore {
	now := time.Now()
	score := &domain.ImportanceScore{
		ObservationID: obsID,
		Score:         0.75,
		AccessCount:   5,
		LastAccessed:  now,
		UpdatedAt:     now,
	}

	for _, override := range overrides {
		override(score)
	}

	return score
}

// ObservationFilter creates a test observation filter with defaults.
func (f *Fixtures) ObservationFilter(overrides ...func(*domain.ObservationFilter)) *domain.ObservationFilter {
	filter := &domain.ObservationFilter{
		Project: "test-project",
		Scope:   domain.ScopeProject,
		Type:    "",
		Limit:   10,
		Offset:  0,
	}

	for _, override := range overrides {
		override(filter)
	}

	return filter
}

// SearchOptions creates test search options with defaults.
func (f *Fixtures) SearchOptions(overrides ...func(*domain.SearchOptions)) *domain.SearchOptions {
	opts := &domain.SearchOptions{
		Query:   "test query",
		Type:    "",
		Project: "test-project",
		Scope:   domain.ScopeProject,
		Limit:   10,
	}

	for _, override := range overrides {
		override(opts)
	}

	return opts
}

// SearchResult creates a test search result with defaults.
func (f *Fixtures) SearchResult(overrides ...func(*domain.SearchResult)) *domain.SearchResult {
	result := &domain.SearchResult{
		Observation: *f.Observation(),
		Rank:        0.95,
	}

	for _, override := range overrides {
		override(result)
	}

	return result
}

// ObservationList creates multiple test observations.
// The count parameter specifies how many observations to create.
func (f *Fixtures) ObservationList(count int, overrides ...func(*domain.Observation, int)) []*domain.Observation {
	observations := make([]*domain.Observation, count)
	for i := 0; i < count; i++ {
		obs := f.Observation(func(o *domain.Observation) {
			o.ID = int64(i + 1)
			o.Title = "Test Observation " + string(rune('A'+i))
		})

		// Apply index-specific overrides
		for _, override := range overrides {
			override(obs, i)
		}

		observations[i] = obs
	}
	return observations
}

// EdgeList creates multiple test edges between consecutive observations.
func (f *Fixtures) EdgeList(count int, overrides ...func(*domain.Edge, int)) []*domain.Edge {
	edges := make([]*domain.Edge, count)
	for i := 0; i < count; i++ {
		edge := f.Edge(int64(i+1), int64(i+2), func(e *domain.Edge) {
			e.ID = int64(i + 1)
		})

		// Apply index-specific overrides
		for _, override := range overrides {
			override(edge, i)
		}

		edges[i] = edge
	}
	return edges
}

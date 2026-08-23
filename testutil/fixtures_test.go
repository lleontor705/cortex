package testutil

import (
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestFixtures_Observation(t *testing.T) {
	f := NewFixtures()

	obs := f.Observation()

	if obs.ID != 1 {
		t.Errorf("expected ID 1, got %d", obs.ID)
	}
	if obs.Title != "Test Observation" {
		t.Errorf("expected 'Test Observation', got %q", obs.Title)
	}
	if obs.Type != domain.TypeManual {
		t.Errorf("expected type %q, got %q", domain.TypeManual, obs.Type)
	}
	if obs.Project != "test-project" {
		t.Errorf("expected project 'test-project', got %q", obs.Project)
	}
	if obs.Scope != domain.ScopeProject {
		t.Errorf("expected scope %q, got %q", domain.ScopeProject, obs.Scope)
	}
	if obs.SessionID != "test-session-123" {
		t.Errorf("expected session 'test-session-123', got %q", obs.SessionID)
	}
}

func TestFixtures_Observation_Overrides(t *testing.T) {
	f := NewFixtures()

	obs := f.Observation(func(o *domain.Observation) {
		o.Title = "Custom Title"
		o.Project = "custom-project"
		o.Type = domain.TypeDecision
	})

	if obs.Title != "Custom Title" {
		t.Errorf("expected 'Custom Title', got %q", obs.Title)
	}
	if obs.Project != "custom-project" {
		t.Errorf("expected 'custom-project', got %q", obs.Project)
	}
	if obs.Type != domain.TypeDecision {
		t.Errorf("expected type %q, got %q", domain.TypeDecision, obs.Type)
	}
}

func TestFixtures_ObservationVariants(t *testing.T) {
	f := NewFixtures()

	t.Run("decision", func(t *testing.T) {
		obs := f.ObservationDecision()
		if obs.Type != domain.TypeDecision {
			t.Errorf("expected type %q, got %q", domain.TypeDecision, obs.Type)
		}
	})

	t.Run("bugfix", func(t *testing.T) {
		obs := f.ObservationBugfix()
		if obs.Type != domain.TypeBugfix {
			t.Errorf("expected type %q, got %q", domain.TypeBugfix, obs.Type)
		}
	})

	t.Run("pattern", func(t *testing.T) {
		obs := f.ObservationPattern()
		if obs.Type != domain.TypePattern {
			t.Errorf("expected type %q, got %q", domain.TypePattern, obs.Type)
		}
	})

	t.Run("discovery", func(t *testing.T) {
		obs := f.ObservationDiscovery()
		if obs.Type != domain.TypeDiscovery {
			t.Errorf("expected type %q, got %q", domain.TypeDiscovery, obs.Type)
		}
	})

	t.Run("tool_use", func(t *testing.T) {
		obs := f.ObservationToolUse()
		if obs.Type != domain.TypeToolUse {
			t.Errorf("expected type %q, got %q", domain.TypeToolUse, obs.Type)
		}
	})

	t.Run("config", func(t *testing.T) {
		obs := f.ObservationConfig()
		if obs.Type != domain.TypeConfig {
			t.Errorf("expected type %q, got %q", domain.TypeConfig, obs.Type)
		}
	})

	t.Run("learning", func(t *testing.T) {
		obs := f.ObservationLearning()
		if obs.Type != domain.TypeLearning {
			t.Errorf("expected type %q, got %q", domain.TypeLearning, obs.Type)
		}
	})

	t.Run("personal", func(t *testing.T) {
		obs := f.ObservationPersonal()
		if obs.Scope != domain.ScopePersonal {
			t.Errorf("expected scope %q, got %q", domain.ScopePersonal, obs.Scope)
		}
	})
}

func TestFixtures_Session(t *testing.T) {
	f := NewFixtures()

	sess := f.Session()

	if sess.ID != "test-session-123" {
		t.Errorf("expected ID 'test-session-123', got %q", sess.ID)
	}
	if sess.Project != "test-project" {
		t.Errorf("expected project 'test-project', got %q", sess.Project)
	}
	if sess.EndedAt != nil {
		t.Error("expected EndedAt to be nil")
	}
}

func TestFixtures_Session_Overrides(t *testing.T) {
	f := NewFixtures()

	sess := f.Session(func(s *domain.Session) {
		s.ID = "custom-session"
		s.Project = "custom-project"
	})

	if sess.ID != "custom-session" {
		t.Errorf("expected 'custom-session', got %q", sess.ID)
	}
	if sess.Project != "custom-project" {
		t.Errorf("expected 'custom-project', got %q", sess.Project)
	}
}

func TestFixtures_SessionEnded(t *testing.T) {
	f := NewFixtures()

	sess := f.SessionEnded()

	if sess.EndedAt == nil {
		t.Fatal("expected EndedAt to be non-nil")
	}
	if sess.Summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestFixtures_SessionWithSummary(t *testing.T) {
	f := NewFixtures()

	sess := f.SessionWithSummary("Test summary")

	if sess.Summary != "Test summary" {
		t.Errorf("expected 'Test summary', got %q", sess.Summary)
	}
}

func TestFixtures_Edge(t *testing.T) {
	f := NewFixtures()

	edge := f.Edge(100, 200)

	if edge.FromObsID != 100 {
		t.Errorf("expected FromObsID 100, got %d", edge.FromObsID)
	}
	if edge.ToObsID != 200 {
		t.Errorf("expected ToObsID 200, got %d", edge.ToObsID)
	}
	if edge.RelationType != domain.RelationReferences {
		t.Errorf("expected relation %q, got %q", domain.RelationReferences, edge.RelationType)
	}
	if edge.Weight != 0.8 {
		t.Errorf("expected weight 0.8, got %f", edge.Weight)
	}
}

func TestFixtures_EdgeVariants(t *testing.T) {
	f := NewFixtures()

	t.Run("relates_to", func(t *testing.T) {
		edge := f.EdgeRelatesTo(1, 2)
		if edge.RelationType != domain.RelationRelatesTo {
			t.Errorf("expected %q, got %q", domain.RelationRelatesTo, edge.RelationType)
		}
	})

	t.Run("follows", func(t *testing.T) {
		edge := f.EdgeFollows(1, 2)
		if edge.RelationType != domain.RelationFollows {
			t.Errorf("expected %q, got %q", domain.RelationFollows, edge.RelationType)
		}
	})

	t.Run("contradicts", func(t *testing.T) {
		edge := f.EdgeContradicts(1, 2)
		if edge.RelationType != domain.RelationContradicts {
			t.Errorf("expected %q, got %q", domain.RelationContradicts, edge.RelationType)
		}
	})

	t.Run("supersedes", func(t *testing.T) {
		edge := f.EdgeSupersedes(1, 2)
		if edge.RelationType != domain.RelationSupersedes {
			t.Errorf("expected %q, got %q", domain.RelationSupersedes, edge.RelationType)
		}
	})
}

func TestFixtures_Prompt(t *testing.T) {
	f := NewFixtures()

	prompt := f.Prompt()

	if prompt.ID != 1 {
		t.Errorf("expected ID 1, got %d", prompt.ID)
	}
	if prompt.Content == "" {
		t.Error("expected non-empty content")
	}
	if prompt.Project != "test-project" {
		t.Errorf("expected project 'test-project', got %q", prompt.Project)
	}
}

func TestFixtures_PromptWithContent(t *testing.T) {
	f := NewFixtures()

	prompt := f.PromptWithContent("Custom prompt content")

	if prompt.Content != "Custom prompt content" {
		t.Errorf("expected 'Custom prompt content', got %q", prompt.Content)
	}
}

func TestFixtures_ImportanceScore(t *testing.T) {
	f := NewFixtures()

	score := f.ImportanceScore(42)

	if score.ObservationID != 42 {
		t.Errorf("expected ObservationID 42, got %d", score.ObservationID)
	}
	if score.Score != 0.75 {
		t.Errorf("expected score 0.75, got %f", score.Score)
	}
	if score.AccessCount != 5 {
		t.Errorf("expected access count 5, got %d", score.AccessCount)
	}
}

func TestFixtures_ObservationFilter(t *testing.T) {
	f := NewFixtures()

	filter := f.ObservationFilter()

	if filter.Project != "test-project" {
		t.Errorf("expected project 'test-project', got %q", filter.Project)
	}
	if filter.Limit != 10 {
		t.Errorf("expected limit 10, got %d", filter.Limit)
	}
}

func TestFixtures_SearchOptions(t *testing.T) {
	f := NewFixtures()

	opts := f.SearchOptions()

	if opts.Query != "test query" {
		t.Errorf("expected query 'test query', got %q", opts.Query)
	}
	if opts.Limit != 10 {
		t.Errorf("expected limit 10, got %d", opts.Limit)
	}
}

func TestFixtures_SearchResult(t *testing.T) {
	f := NewFixtures()

	result := f.SearchResult()

	if result.Rank != 0.95 {
		t.Errorf("expected rank 0.95, got %f", result.Rank)
	}
	if result.ID != 1 {
		t.Errorf("expected ID 1, got %d", result.ID)
	}
}

func TestFixtures_ObservationList(t *testing.T) {
	f := NewFixtures()

	observations := f.ObservationList(5)

	if len(observations) != 5 {
		t.Fatalf("expected 5 observations, got %d", len(observations))
	}

	// Verify each has unique ID
	ids := make(map[int64]bool)
	for _, obs := range observations {
		if ids[obs.ID] {
			t.Errorf("duplicate ID: %d", obs.ID)
		}
		ids[obs.ID] = true
	}
}

func TestFixtures_ObservationList_WithIndexOverride(t *testing.T) {
	f := NewFixtures()

	observations := f.ObservationList(3, func(o *domain.Observation, i int) {
		o.Project = "project-" + string(rune('A'+i))
	})

	if observations[0].Project != "project-A" {
		t.Errorf("expected 'project-A', got %q", observations[0].Project)
	}
}

func TestFixtures_EdgeList(t *testing.T) {
	f := NewFixtures()

	edges := f.EdgeList(3)

	if len(edges) != 3 {
		t.Fatalf("expected 3 edges, got %d", len(edges))
	}

	// Verify chain: 1->2, 2->3, 3->4
	if edges[0].FromObsID != 1 || edges[0].ToObsID != 2 {
		t.Errorf("expected edge 1->2, got %d->%d", edges[0].FromObsID, edges[0].ToObsID)
	}
	if edges[1].FromObsID != 2 || edges[1].ToObsID != 3 {
		t.Errorf("expected edge 2->3, got %d->%d", edges[1].FromObsID, edges[1].ToObsID)
	}
}

func TestFixtures_Timestamps(t *testing.T) {
	f := NewFixtures()

	before := time.Now()
	obs := f.Observation()
	after := time.Now()

	// Verify timestamps are within reasonable range
	if obs.CreatedAt.Before(before.Add(-time.Second)) || obs.CreatedAt.After(after.Add(time.Second)) {
		t.Error("CreatedAt timestamp is not within expected range")
	}
	if obs.UpdatedAt.Before(before.Add(-time.Second)) || obs.UpdatedAt.After(after.Add(time.Second)) {
		t.Error("UpdatedAt timestamp is not within expected range")
	}
}

package extraction

import (
	"context"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

func TestExtractHeuristically_DecisionAndBugfix(t *testing.T) {
	svc := NewService(Config{Timeout: 5 * time.Second})

	input := `We decided to migrate the database from SQLite to PostgreSQL for high availability.
Fixed bug in the query planner where negative limits caused a panic.
Pattern: Always validate foreign keys before performing cascade deletion.`

	res, err := svc.Extract(context.Background(), ExtractionRequest{
		Text:    input,
		Project: "cortex-core",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.Observations) < 3 {
		t.Fatalf("expected at least 3 extracted observations, got %d", len(res.Observations))
	}

	if res.SourceMethod != "heuristic" {
		t.Errorf("expected source_method heuristic, got %s", res.SourceMethod)
	}

	if len(res.Edges) == 0 {
		t.Errorf("expected generated relation edges, got 0")
	}
}

func TestSynthesizeHeuristically(t *testing.T) {
	svc := NewService(Config{})

	obs := []*domain.Observation{
		{ID: 1, Title: "Chose PostgreSQL", Content: "Selected PostgreSQL for RLS support", Type: "decision", Project: "proj-1"},
		{ID: 2, Title: "Repository Pattern", Content: "Use repository interface boundaries", Type: "pattern", Project: "proj-1"},
		{ID: 3, Title: "Fix deadlock", Content: "Fixed transaction deadlock in batch inserts", Type: "bugfix", Project: "proj-1"},
	}

	res, err := svc.Synthesize(context.Background(), SynthesisRequest{
		Project:      "proj-1",
		Observations: obs,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.KeyDecisions) == 0 {
		t.Errorf("expected key decisions in synthesis result")
	}
	if len(res.Patterns) == 0 {
		t.Errorf("expected patterns in synthesis result")
	}
	if len(res.OpenIssues) == 0 {
		t.Errorf("expected open issues in synthesis result")
	}
}

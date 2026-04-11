package dna

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

type mockLister struct {
	obs []*domain.Observation
}

func (m *mockLister) List(_ context.Context, _ domain.ObservationFilter) ([]*domain.Observation, error) {
	return m.obs, nil
}

func TestGenerateEmpty(t *testing.T) {
	svc := NewService(&mockLister{}, nil, nil)
	result, err := svc.Generate(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No observations found") {
		t.Fatalf("expected empty message, got: %s", result)
	}
}

func TestGenerateWithObservations(t *testing.T) {
	now := time.Now()
	obs := []*domain.Observation{
		{ID: 1, Type: domain.TypeDecision, Title: "Use SQLite", Content: "Chose SQLite for simplicity", CreatedAt: now},
		{ID: 2, Type: domain.TypePattern, Title: "Table-driven tests", Content: "All tests use table pattern", CreatedAt: now},
		{ID: 3, Type: domain.TypeBugfix, Title: "Fix N+1 query", Content: "Added eager loading in UserList", CreatedAt: now},
		{ID: 4, Type: domain.TypeConfig, Title: "WAL mode enabled", Content: "journal_mode=WAL", CreatedAt: now},
		{ID: 5, Type: domain.TypeDiscovery, Title: "FTS5 triggers", Content: "Must update on schema change", CreatedAt: now},
	}

	svc := NewService(&mockLister{obs: obs}, nil, nil)
	result, err := svc.Generate(context.Background(), "cortex")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []string{
		"Project DNA: cortex",
		"Key Decisions",
		"Use SQLite",
		"Patterns",
		"Table-driven tests",
		"Known Gotchas",
		"Fix N+1 query",
		"Configuration",
		"WAL mode",
		"Discoveries",
		"FTS5 triggers",
		"5 observations",
	}

	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("missing %q in DNA output", check)
		}
	}
}

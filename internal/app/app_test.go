package app

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
)

func TestOpenInMemoryProvidesWorkingStores(t *testing.T) {
	ctx := context.Background()

	a, err := Open(ctx, Options{InMemory: true})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer a.Close()

	session := &domain.Session{
		ID:        "session-1",
		Project:   "demo",
		Directory: ".",
	}
	if err := a.Stores.Sessions.Create(ctx, session); err != nil {
		t.Fatalf("Sessions.Create() error = %v", err)
	}

	obs := &domain.Observation{
		SessionID: "session-1",
		Title:     "JWT auth middleware",
		Content:   "Switched auth flow to JWT",
		Type:      domain.TypeDecision,
		Project:   "demo",
		Scope:     domain.ScopeProject,
	}
	if err := a.Stores.Observations.Save(ctx, obs); err != nil {
		t.Fatalf("Observations.Save() error = %v", err)
	}

	results, err := a.Stores.Search.Search(ctx, "JWT auth", domain.SearchOptions{Project: "demo", Limit: 5})
	if err != nil {
		t.Fatalf("Search.Search() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("Search.Search() returned no results")
	}
}

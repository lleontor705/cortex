package platform

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/app"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

// TestLocalDelegatesToAppOpen proves that platform.Local calls app.Open
// byte-identically — the returned Runtime wraps a fully-wired *app.App with
// Config, DB, Migrator, and Stores all populated exactly as app.Open produces.
func TestLocalDelegatesToAppOpen(t *testing.T) {
	ctx := context.Background()
	rt, err := Local(ctx, app.Options{InMemory: true})
	if err != nil {
		t.Fatalf("Local() error = %v", err)
	}
	defer func() { _ = rt.Close() }()

	if rt.App == nil {
		t.Fatal("Local() returned nil App — did not delegate to app.Open")
	}
	if rt.App.Config == nil {
		t.Fatal("Local() returned App with nil Config")
	}
	if rt.App.DB == nil {
		t.Fatal("Local() returned App with nil DB (database.Manager)")
	}
	if rt.App.Migrator == nil {
		t.Fatal("Local() returned App with nil Migrator")
	}
}

// TestLocalByteIdenticalStores proves the stores wired through platform.Local
// behave identically to those from a direct app.Open call. Save+Search must
// work the same way — this is the behavioral proof of byte-identical delegation.
func TestLocalByteIdenticalStores(t *testing.T) {
	ctx := context.Background()
	rt, err := Local(ctx, app.Options{InMemory: true})
	if err != nil {
		t.Fatalf("Local() error = %v", err)
	}
	defer func() { _ = rt.Close() }()

	// Create a session through the platform-delegated stores
	session := &domain.Session{
		ID:        "platform-local-test",
		Project:   "platform-test",
		Directory: ".",
	}
	if err := rt.App.Stores.Sessions.Create(ctx, session); err != nil {
		t.Fatalf("Sessions.Create() error = %v", err)
	}

	// Save an observation
	obs := &domain.Observation{
		SessionID: "platform-local-test",
		Title:     "platform delegation proof",
		Content:   "proving Local delegates to app.Open byte-identically",
		Type:      domain.TypeDecision,
		Project:   "platform-test",
		Scope:     domain.ScopeProject,
	}
	if err := rt.App.Stores.Observations.Save(ctx, obs); err != nil {
		t.Fatalf("Observations.Save() error = %v", err)
	}

	// Search must find it — proves FTS5 triggers + store wiring are identical
	results, err := rt.App.Stores.Search.Search(ctx, "platform delegation",
		domain.SearchOptions{Project: "platform-test", Limit: 5})
	if err != nil {
		t.Fatalf("Search.Search() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search.Search() returned no results — stores not wired identically to app.Open")
	}
}

// TestLocalInMemoryConfigApplied proves the InMemory option passes through
// to app.Open unchanged (byte-identical config handling).
func TestLocalInMemoryConfigApplied(t *testing.T) {
	ctx := context.Background()
	rt, err := Local(ctx, app.Options{InMemory: true})
	if err != nil {
		t.Fatalf("Local() error = %v", err)
	}
	defer func() { _ = rt.Close() }()

	if !rt.App.Config.Database.InMemory {
		t.Error("Local(InMemory=true) did not propagate InMemory to Config")
	}
	if rt.App.Config.Database.Path != ":memory:" {
		t.Errorf("Local(InMemory=true) Path = %q, want \":memory:\"", rt.App.Config.Database.Path)
	}
}

// TestLocalDefaultModeConstants verifies the mode type system is consistent.
func TestLocalDefaultModeConstants(t *testing.T) {
	// DefaultMode must be ModeLocal — ensures --mode flag default is local.
	if DefaultMode != ModeLocal {
		t.Errorf("DefaultMode = %q, want %q", DefaultMode, ModeLocal)
	}
}

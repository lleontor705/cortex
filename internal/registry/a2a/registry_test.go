package a2a_registry

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain/a2a"
)

func TestNewAgentRegistry(t *testing.T) {
	registry := NewAgentRegistry()
	if registry == nil {
		t.Fatal("registry should not be nil")
	}
}

func TestNewRegistryService(t *testing.T) {
	registry := NewAgentRegistry()
	service := NewRegistryService(registry)
	if service == nil {
		t.Fatal("service should not be nil")
	}
}

func TestRegisterAgent(t *testing.T) {
	registry := NewAgentRegistry()
	ctx := context.Background()

	agent := &a2a.Agent{
		ID:      "agent-1",
		Name:    "Test Agent",
		Version: "1.0.0",
		Status:  a2a.StatusActive,
	}

	err := registry.RegisterAgent(ctx, agent)
	if err != nil {
		t.Fatalf("failed to register agent: %v", err)
	}

	// Verify agent was registered
	retrieved, err := registry.GetAgent(ctx, "agent-1")
	if err != nil {
		t.Fatalf("failed to get agent: %v", err)
	}
	if retrieved.Name != "Test Agent" {
		t.Errorf("expected name Test Agent, got %s", retrieved.Name)
	}
}

func TestRegisterAgentEmptyID(t *testing.T) {
	registry := NewAgentRegistry()
	ctx := context.Background()

	agent := &a2a.Agent{
		ID:      "",
		Name:    "Test Agent",
		Status:  a2a.StatusActive,
	}

	err := registry.RegisterAgent(ctx, agent)
	if err != ErrInvalidAgentID {
		t.Errorf("expected ErrInvalidAgentID, got %v", err)
	}
}

func TestRegisterAgentUpdate(t *testing.T) {
	registry := NewAgentRegistry()
	ctx := context.Background()

	// Register initial agent
	agent := &a2a.Agent{
		ID:      "agent-1",
		Name:    "Test Agent",
		Version: "1.0.0",
		Status:  a2a.StatusActive,
	}
	registry.RegisterAgent(ctx, agent)

	// Update agent
	updated := &a2a.Agent{
		ID:      "agent-1",
		Name:    "Updated Agent",
		Version: "2.0.0",
		Status:  a2a.StatusInactive,
	}
	registry.RegisterAgent(ctx, updated)

	// Verify update
	retrieved, _ := registry.GetAgent(ctx, "agent-1")
	if retrieved.Name != "Updated Agent" {
		t.Errorf("expected name Updated Agent, got %s", retrieved.Name)
	}
	if retrieved.Version != "2.0.0" {
		t.Errorf("expected version 2.0.0, got %s", retrieved.Version)
	}
	if retrieved.Status != a2a.StatusInactive {
		t.Errorf("expected status inactive, got %s", retrieved.Status)
	}
}

func TestGetAgentNotFound(t *testing.T) {
	registry := NewAgentRegistry()
	ctx := context.Background()

	_, err := registry.GetAgent(ctx, "nonexistent")
	if err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound, got %v", err)
	}
}

func TestGetAgents(t *testing.T) {
	registry := NewAgentRegistry()
	ctx := context.Background()

	// Register multiple agents
	agents := []*a2a.Agent{
		{ID: "agent-1", Name: "Agent 1", Status: a2a.StatusActive},
		{ID: "agent-2", Name: "Agent 2", Status: a2a.StatusInactive},
		{ID: "agent-3", Name: "Agent 3", Status: a2a.StatusActive},
	}

	for _, agent := range agents {
		registry.RegisterAgent(ctx, agent)
	}

	retrieved, err := registry.GetAgents(ctx)
	if err != nil {
		t.Fatalf("failed to get agents: %v", err)
	}
	if len(retrieved) != 3 {
		t.Errorf("expected 3 agents, got %d", len(retrieved))
	}
}

func TestGetActiveAgents(t *testing.T) {
	registry := NewAgentRegistry()
	ctx := context.Background()

	// Register agents with different statuses
	registry.RegisterAgent(ctx, &a2a.Agent{ID: "active-1", Name: "Active 1", Status: a2a.StatusActive, LastSeen: time.Now()})
	registry.RegisterAgent(ctx, &a2a.Agent{ID: "inactive-1", Name: "Inactive 1", Status: a2a.StatusInactive, LastSeen: time.Now()})
	registry.RegisterAgent(ctx, &a2a.Agent{ID: "active-2", Name: "Active 2", Status: a2a.StatusActive, LastSeen: time.Now()})
	registry.RegisterAgent(ctx, &a2a.Agent{ID: "error-1", Name: "Error 1", Status: a2a.StatusError, LastSeen: time.Now()})

	active, err := registry.GetActiveAgents(ctx)
	if err != nil {
		t.Fatalf("failed to get active agents: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("expected 2 active agents, got %d", len(active))
	}
}

func TestGetAgentsByCapability(t *testing.T) {
	registry := NewAgentRegistry()
	ctx := context.Background()

	// Register agents with capabilities
	registry.RegisterAgent(ctx, &a2a.Agent{
		ID:   "memory-agent",
		Name: "Memory Agent",
		Capabilities: []a2a.Capability{
			{Name: "memory-search"},
			{Name: "temporal-graph"},
		},
		Status: a2a.StatusActive,
	})

	registry.RegisterAgent(ctx, &a2a.Agent{
		ID:   "code-agent",
		Name: "Code Agent",
		Capabilities: []a2a.Capability{
			{Name: "code-generation"},
			{Name: "memory-search"},
		},
		Status: a2a.StatusActive,
	})

	registry.RegisterAgent(ctx, &a2a.Agent{
		ID:   "test-agent",
		Name: "Test Agent",
		Capabilities: []a2a.Capability{
			{Name: "testing"},
		},
		Status: a2a.StatusActive,
	})

	// Find agents with memory-search capability
	agents, err := registry.GetAgentsByCapability(ctx, "memory-search")
	if err != nil {
		t.Fatalf("failed to get agents by capability: %v", err)
	}
	if len(agents) != 2 {
		t.Errorf("expected 2 agents with memory-search, got %d", len(agents))
	}

	// Find agents with temporal-graph capability
	agents, err = registry.GetAgentsByCapability(ctx, "temporal-graph")
	if err != nil {
		t.Fatalf("failed to get agents by capability: %v", err)
	}
	if len(agents) != 1 {
		t.Errorf("expected 1 agent with temporal-graph, got %d", len(agents))
	}
}

func TestUnregisterAgent(t *testing.T) {
	registry := NewAgentRegistry()
	ctx := context.Background()

	registry.RegisterAgent(ctx, &a2a.Agent{ID: "agent-1", Name: "Agent 1", Status: a2a.StatusActive})

	err := registry.UnregisterAgent(ctx, "agent-1")
	if err != nil {
		t.Fatalf("failed to unregister agent: %v", err)
	}

	// Verify agent was removed
	_, err = registry.GetAgent(ctx, "agent-1")
	if err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound after unregister, got %v", err)
	}
}

func TestHeartbeat(t *testing.T) {
	registry := NewAgentRegistry()
	ctx := context.Background()

	registry.RegisterAgent(ctx, &a2a.Agent{ID: "agent-1", Name: "Agent 1", Status: a2a.StatusActive})

	err := registry.Heartbeat(ctx, "agent-1")
	if err != nil {
		t.Fatalf("failed to send heartbeat: %v", err)
	}

	// Test heartbeat for nonexistent agent
	err = registry.Heartbeat(ctx, "nonexistent")
	if err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound, got %v", err)
	}
}

func TestUpdateAgentStatus(t *testing.T) {
	registry := NewAgentRegistry()
	ctx := context.Background()

	registry.RegisterAgent(ctx, &a2a.Agent{ID: "agent-1", Name: "Agent 1", Status: a2a.StatusActive})

	err := registry.UpdateAgentStatus(ctx, "agent-1", a2a.StatusInactive)
	if err != nil {
		t.Fatalf("failed to update status: %v", err)
	}

	agent, _ := registry.GetAgent(ctx, "agent-1")
	if agent.Status != a2a.StatusInactive {
		t.Errorf("expected status inactive, got %s", agent.Status)
	}
}

func TestUpdateAgentStatusNotFound(t *testing.T) {
	registry := NewAgentRegistry()
	ctx := context.Background()

	err := registry.UpdateAgentStatus(ctx, "nonexistent", a2a.StatusActive)
	if err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound, got %v", err)
	}
}

func TestGetAgentStats(t *testing.T) {
	registry := NewAgentRegistry()
	ctx := context.Background()

	registry.RegisterAgent(ctx, &a2a.Agent{
		ID:   "agent-1",
		Name: "Agent 1",
		Status: a2a.StatusActive,
		Capabilities: []a2a.Capability{{Name: "memory"}, {Name: "graph"}},
	})
	registry.RegisterAgent(ctx, &a2a.Agent{
		ID:   "agent-2",
		Name: "Agent 2",
		Status: a2a.StatusInactive,
		Capabilities: []a2a.Capability{{Name: "memory"}},
	})
	registry.RegisterAgent(ctx, &a2a.Agent{
		ID:   "agent-3",
		Name: "Agent 3",
		Status: a2a.StatusError,
	})

	stats, err := registry.GetAgentStats(ctx)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}

	if stats.TotalAgents != 3 {
		t.Errorf("expected 3 total agents, got %d", stats.TotalAgents)
	}
	if stats.ActiveAgents != 1 {
		t.Errorf("expected 1 active agent, got %d", stats.ActiveAgents)
	}
	if stats.InactiveAgents != 1 {
		t.Errorf("expected 1 inactive agent, got %d", stats.InactiveAgents)
	}
	if stats.ErrorAgents != 1 {
		t.Errorf("expected 1 error agent, got %d", stats.ErrorAgents)
	}
	if stats.CapabilityMap["memory"] != 2 {
		t.Errorf("expected 2 memory capabilities, got %d", stats.CapabilityMap["memory"])
	}
}

func TestDiscoverAgents(t *testing.T) {
	registry := NewAgentRegistry()
	service := NewRegistryService(registry)
	ctx := context.Background()

	// Register agents
	registry.RegisterAgent(ctx, &a2a.Agent{
		ID:   "memory-1",
		Name: "Memory Agent 1",
		Status: a2a.StatusActive,
		Capabilities: []a2a.Capability{{Name: "memory-search"}},
		LastSeen: time.Now(),
	})
	registry.RegisterAgent(ctx, &a2a.Agent{
		ID:   "memory-2",
		Name: "Memory Agent 2",
		Status: a2a.StatusActive,
		Capabilities: []a2a.Capability{{Name: "memory-search"}, {Name: "graph"}},
		LastSeen: time.Now(),
	})
	registry.RegisterAgent(ctx, &a2a.Agent{
		ID:   "code-1",
		Name: "Code Agent",
		Status: a2a.StatusActive,
		Capabilities: []a2a.Capability{{Name: "code-gen"}},
		LastSeen: time.Now(),
	})

	// Discover by capability
	criteria := DiscoveryCriteria{
		Capabilities: []string{"memory-search"},
	}
	agents, err := service.DiscoverAgents(ctx, criteria)
	if err != nil {
		t.Fatalf("failed to discover agents: %v", err)
	}
	if len(agents) != 2 {
		t.Errorf("expected 2 agents with memory-search, got %d", len(agents))
	}

	// Discover by multiple capabilities
	criteria = DiscoveryCriteria{
		Capabilities: []string{"memory-search", "graph"},
	}
	agents, err = service.DiscoverAgents(ctx, criteria)
	if err != nil {
		t.Fatalf("failed to discover agents: %v", err)
	}
	if len(agents) != 1 {
		t.Errorf("expected 1 agent with both capabilities, got %d", len(agents))
	}
}

func TestExportImportRegistry(t *testing.T) {
	registry := NewAgentRegistry()
	ctx := context.Background()

	// Register agents
	registry.RegisterAgent(ctx, &a2a.Agent{ID: "agent-1", Name: "Agent 1", Status: a2a.StatusActive})
	registry.RegisterAgent(ctx, &a2a.Agent{ID: "agent-2", Name: "Agent 2", Status: a2a.StatusInactive})
	registry.Heartbeat(ctx, "agent-1")

	// Export
	data, err := registry.ExportRegistry(ctx)
	if err != nil {
		t.Fatalf("failed to export: %v", err)
	}

	// Import into new registry
	newRegistry := NewAgentRegistry()
	err = newRegistry.ImportRegistry(ctx, data)
	if err != nil {
		t.Fatalf("failed to import: %v", err)
	}

	// Verify
	agents, _ := newRegistry.GetAgents(ctx)
	if len(agents) != 2 {
		t.Errorf("expected 2 agents after import, got %d", len(agents))
	}
}

func TestConcurrentAccess(t *testing.T) {
	registry := NewAgentRegistry()
	ctx := context.Background()
	var wg sync.WaitGroup

	// Concurrent registrations
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			agent := &a2a.Agent{
				ID:      fmt.Sprintf("agent-%d", id),
				Name:    fmt.Sprintf("Agent %d", id),
				Status:  a2a.StatusActive,
			}
			registry.RegisterAgent(ctx, agent)
		}(i)
	}

	wg.Wait()

	agents, _ := registry.GetAgents(ctx)
	if len(agents) != 100 {
		t.Errorf("expected 100 agents, got %d", len(agents))
	}
}
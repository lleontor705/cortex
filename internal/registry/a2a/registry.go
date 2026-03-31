// Package a2a_registry provides A2A protocol agent registry and discovery
//
// This package manages A2A agent registration, discovery, and health monitoring.
package a2a_registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lleontor705/cortex/internal/domain/a2a"
)

// AgentRegistry manages A2A agent registration and discovery
type AgentRegistry struct {
	agents      map[string]*a2a.Agent
	mu          sync.RWMutex
	heartbeat   map[string]time.Time
}

// RegistryService provides high-level registry operations
type RegistryService struct {
	registry *AgentRegistry
}

// NewAgentRegistry creates a new agent registry
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{
		agents:    make(map[string]*a2a.Agent),
		heartbeat: make(map[string]time.Time),
	}
}

// NewRegistryService creates a new registry service
func NewRegistryService(registry *AgentRegistry) *RegistryService {
	return &RegistryService{registry: registry}
}

// RegisterAgent registers an A2A agent in the registry
func (r *AgentRegistry) RegisterAgent(ctx context.Context, agent *a2a.Agent) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if agent.ID == "" {
		return ErrInvalidAgentID
	}

	if existing, exists := r.agents[agent.ID]; exists {
		existing.Name = agent.Name
		existing.Version = agent.Version
		existing.Capabilities = agent.Capabilities
		existing.Metadata = agent.Metadata
		existing.Endpoint = agent.Endpoint
		existing.Status = agent.Status
	} else {
		r.agents[agent.ID] = agent
	}

	r.heartbeat[agent.ID] = time.Now()
	return nil
}

// GetAgent retrieves an agent by ID
func (r *AgentRegistry) GetAgent(ctx context.Context, agentID string) (*a2a.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, exists := r.agents[agentID]
	if !exists {
		return nil, ErrAgentNotFound
	}
	return agent, nil
}

// GetAgents returns all registered agents
func (r *AgentRegistry) GetAgents(ctx context.Context) ([]*a2a.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agents := make([]*a2a.Agent, 0, len(r.agents))
	for _, agent := range r.agents {
		agents = append(agents, agent)
	}
	return agents, nil
}

// GetActiveAgents returns only active agents with recent heartbeats
func (r *AgentRegistry) GetActiveAgents(ctx context.Context) ([]*a2a.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var active []*a2a.Agent
	now := time.Now()

	for _, agent := range r.agents {
		if agent.Status != a2a.StatusActive {
			continue
		}
		if heartbeat, exists := r.heartbeat[agent.ID]; exists {
			if now.Sub(heartbeat) <= 5*time.Minute {
				active = append(active, agent)
			}
		} else if now.Sub(agent.LastSeen) <= 5*time.Minute {
			active = append(active, agent)
		}
	}
	return active, nil
}

// GetAgentsByCapability returns agents that have a specific capability
func (r *AgentRegistry) GetAgentsByCapability(ctx context.Context, capability string) ([]*a2a.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var matching []*a2a.Agent
	for _, agent := range r.agents {
		if agent.Status != a2a.StatusActive {
			continue
		}
		for _, cap := range agent.Capabilities {
			if cap.Name == capability {
				matching = append(matching, agent)
				break
			}
		}
	}
	return matching, nil
}

// FindAgentsByTopic finds agents that handle specific topics
func (r *AgentRegistry) FindAgentsByTopic(ctx context.Context, topic string) ([]*a2a.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var matching []*a2a.Agent
	for _, agent := range r.agents {
		if agent.Status != a2a.StatusActive {
			continue
		}
		for _, cap := range agent.Capabilities {
			if contains(cap.Topics, topic) {
				matching = append(matching, agent)
				break
			}
		}
	}
	return matching, nil
}

// UnregisterAgent removes an agent from the registry
func (r *AgentRegistry) UnregisterAgent(ctx context.Context, agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.agents, agentID)
	delete(r.heartbeat, agentID)
	return nil
}

// Heartbeat updates the heartbeat time for an agent
func (r *AgentRegistry) Heartbeat(ctx context.Context, agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.agents[agentID]; !exists {
		return ErrAgentNotFound
	}
	r.heartbeat[agentID] = time.Now()
	return nil
}

// UpdateAgentStatus updates the status of an agent
func (r *AgentRegistry) UpdateAgentStatus(ctx context.Context, agentID string, status a2a.AgentStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, exists := r.agents[agentID]
	if !exists {
		return ErrAgentNotFound
	}
	agent.Status = status
	agent.LastSeen = time.Now()
	return nil
}

// GetAgentStats returns statistics about the registry
func (r *AgentRegistry) GetAgentStats(ctx context.Context) (*RegistryStats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := &RegistryStats{
		TotalAgents:   len(r.agents),
		CapabilityMap: make(map[string]int),
	}

	for _, agent := range r.agents {
		switch agent.Status {
		case a2a.StatusActive:
			stats.ActiveAgents++
		case a2a.StatusInactive:
			stats.InactiveAgents++
		case a2a.StatusError:
			stats.ErrorAgents++
		}
		for _, cap := range agent.Capabilities {
			stats.CapabilityMap[cap.Name]++
		}
	}
	return stats, nil
}

// DiscoverAgents discovers agents based on criteria
func (r *RegistryService) DiscoverAgents(ctx context.Context, criteria DiscoveryCriteria) ([]*a2a.Agent, error) {
	activeAgents, err := r.registry.GetActiveAgents(ctx)
	if err != nil {
		return nil, err
	}

	var candidates []*a2a.Agent
	for _, agent := range activeAgents {
		if matchesCriteria(agent, criteria) {
			candidates = append(candidates, agent)
		}
	}
	return candidates, nil
}

// StartCleanup starts background cleanup of stale agents
func (r *AgentRegistry) StartCleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		r.cleanupStaleAgents()
	}
}

func (r *AgentRegistry) cleanupStaleAgents() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	for agentID, heartbeat := range r.heartbeat {
		if now.Sub(heartbeat) > 10*time.Minute {
			delete(r.agents, agentID)
			delete(r.heartbeat, agentID)
		}
	}
}

// ExportRegistry exports the registry state to JSON
func (r *AgentRegistry) ExportRegistry(ctx context.Context) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	data := struct {
		Agents     map[string]*a2a.Agent `json:"agents"`
		Heartbeats map[string]time.Time  `json:"heartbeats"`
		ExportedAt time.Time             `json:"exported_at"`
	}{
		Agents:     r.agents,
		Heartbeats: r.heartbeat,
		ExportedAt: time.Now(),
	}
	return json.Marshal(data)
}

// ImportRegistry imports registry state from JSON
func (r *AgentRegistry) ImportRegistry(ctx context.Context, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var imported struct {
		Agents     map[string]*a2a.Agent `json:"agents"`
		Heartbeats map[string]time.Time  `json:"heartbeats"`
	}
	if err := json.Unmarshal(data, &imported); err != nil {
		return err
	}

	for id, agent := range imported.Agents {
		r.agents[id] = agent
	}
	for id, heartbeat := range imported.Heartbeats {
		r.heartbeat[id] = heartbeat
	}
	return nil
}

// RegistryStats provides statistics about the agent registry
type RegistryStats struct {
	TotalAgents    int            `json:"total_agents"`
	ActiveAgents   int            `json:"active_agents"`
	InactiveAgents int            `json:"inactive_agents"`
	ErrorAgents    int            `json:"error_agents"`
	CapabilityMap  map[string]int `json:"capability_map"`
}

// DiscoveryCriteria defines criteria for agent discovery
type DiscoveryCriteria struct {
	Capabilities []string          `json:"capabilities,omitempty"`
	Topics       []string          `json:"topics,omitempty"`
	Status       a2a.AgentStatus   `json:"status,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

func matchesCriteria(agent *a2a.Agent, criteria DiscoveryCriteria) bool {
	if criteria.Status != "" && agent.Status != criteria.Status {
		return false
	}

	if len(criteria.Capabilities) > 0 {
		agentCaps := make(map[string]bool)
		for _, cap := range agent.Capabilities {
			agentCaps[cap.Name] = true
		}
		for _, required := range criteria.Capabilities {
			if !agentCaps[required] {
				return false
			}
		}
	}

	if len(criteria.Topics) > 0 {
		for _, topic := range criteria.Topics {
			found := false
			for _, cap := range agent.Capabilities {
				if contains(cap.Topics, topic) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}

	if len(criteria.Metadata) > 0 {
		for key, value := range criteria.Metadata {
			if val, exists := agent.Metadata[key]; exists {
				if fmt.Sprintf("%v", val) != value {
					return false
				}
			} else {
				return false
			}
		}
	}
	return true
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

var (
	ErrInvalidAgentID = errors.New("agent ID cannot be empty")
	ErrAgentNotFound  = errors.New("agent not found")
	ErrInvalidStatus  = errors.New("invalid agent status")
)
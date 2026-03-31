# A2A Protocol Integration for Cortex

## Overview

This document outlines the integration of A2A (Agent-to-Agent) Protocol support into Cortex, making it compatible with the emerging standard for inter-agent communication.

## A2A Protocol Background

A2A Protocol is the first open standard protocol for AI agent collaboration, designed to solve challenges of AI agents developed by different organizations. It's preferred for cloud-based and distributed multi-agent scenarios.

### Key A2A Concepts

- **Agent Identity**: Unique identifiers for agents
- **Message Format**: Structured messages with headers and payload
- **Transport Agnostic**: Works over various transports (HTTP, WebSocket, stdio)
- **Message Types**: Request/Response, Notification, Event, Stream
- **Session Management**: Context preservation across interactions
- **Error Handling**: Standardized error responses

## Integration Architecture

### 1. A2A Message Format Support

```go
// Package a2a provides A2A Protocol message format and handling
package a2a

import (
	"encoding/json"
	"time"
)

// MessageType defines A2A message types
type MessageType string

const (
	MessageTypeRequest  MessageType = "request"
	MessageTypeResponse MessageType = "response"
	MessageTypeNotify   MessageType = "notify"
	MessageTypeEvent    MessageType = "event"
	MessageTypeStream   MessageType = "stream"
)

// Message represents an A2A protocol message
type Message struct {
	// Standard A2A headers
	ID        string                 `json:"id"`
	Type      MessageType            `json:"type"`
	From      string                 `json:"from"`
	To        []string               `json:"to"`
	Topic     string                 `json:"topic,omitempty"`
	Headers   map[string]interface{} `json:"headers,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	
	// Payload and metadata
	Payload   interface{}            `json:"payload"`
	Error     *Error                 `json:"error,omitempty"`
	
	// A2A specific fields
	SessionID string                 `json:"session_id,omitempty"`
	TraceID   string                 `json:"trace_id,omitempty"`
	ReplyTo   string                 `json:"reply_to,omitempty"`
	Sequence  int64                  `json:"sequence,omitempty"`
}

// Error represents A2A error format
type Error struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// Agent represents an A2A agent
type Agent struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Version     string                 `json:"version"`
	Capabilities []string              `json:"capabilities"`
	Metadata    map[string]interface{} `json:"metadata"`
	Endpoint    string                 `json:"endpoint,omitempty"`
	Status      string                 `json:"status"` // active, inactive, error
	LastSeen    time.Time              `json:"last_seen"`
}
```

### 2. A2A Transport Layer

```go
// Package transport provides A2A transport implementations
package transport

import (
	"context"
	"encoding/json"
	"errors"
	
	"github.com/lleontor705/cortex/internal/domain/a2a"
)

// Transport defines the interface for A2A message transport
type Transport interface {
	// Send a message to specific agents
	Send(ctx context.Context, message *a2a.Message, to ...string) error
	
	// Broadcast a message to all agents
	Broadcast(ctx context.Context, message *a2a.Message) error
	
	// Start listening for incoming messages
	Listen(ctx context.Context, handler MessageHandler) error
	
	// Register an agent
	RegisterAgent(ctx context.Context, agent *a2a.Agent) error
	
	// Unregister an agent
	UnregisterAgent(ctx context.Context, agentID string) error
	
	// Get registered agents
	GetAgents(ctx context.Context) ([]*a2a.Agent, error)
	
	// Close the transport
	Close() error
}

// MessageHandler handles incoming A2A messages
type MessageHandler func(ctx context.Context, message *a2a.Message) error

// StdioTransport implements A2A over stdio (MCP style)
type StdioTransport struct {
	agents      map[string]*a2a.Agent
	messageChan chan *a2a.Message
	handler     MessageHandler
}

// NewStdioTransport creates a new stdio transport
func NewStdioTransport(handler MessageHandler) *StdioTransport {
	return &StdioTransport{
		agents:      make(map[string]*a2a.Agent),
		messageChan: make(chan *a2a.Message, 100),
		handler:     handler,
	}
}

// HTTPTransport implements A2A over HTTP
type HTTPTransport struct {
	baseURL     string
	agents      map[string]*a2a.Agent
	messageChan chan *a2a.Message
	handler     MessageHandler
}
```

### 3. A2A Agent Registry

```go
// Package registry provides A2A agent management
package registry

import (
	"context"
	"sync"
	"time"
	
	"github.com/lleontor705/cortex/internal/domain/a2a"
)

// AgentRegistry manages A2A agent registration and discovery
type AgentRegistry struct {
	agents      map[string]*a2a.Agent
	mu          sync.RWMutex
	heartbeat   map[string]time.Time
	cleanupChan <-chan time.Time
}

// NewAgentRegistry creates a new agent registry
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{
		agents:    make(map[string]*a2a.Agent),
		heartbeat: make(map[string]time.Time),
	}
}

// RegisterAgent registers an A2A agent
func (r *AgentRegistry) RegisterAgent(ctx context.Context, agent *a2a.Agent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	// Validate agent
	if agent.ID == "" {
		return errors.New("agent ID cannot be empty")
	}
	
	// Check if agent already exists
	if existing, exists := r.agents[agent.ID]; exists {
		// Update existing agent
		existing.Name = agent.Name
		existing.Version = agent.Version
		existing.Capabilities = agent.Capabilities
		existing.Metadata = agent.Metadata
		existing.Endpoint = agent.Endpoint
		existing.Status = agent.Status
	} else {
		// Add new agent
		r.agents[agent.ID] = agent
	}
	
	// Update heartbeat
	r.heartbeat[agent.ID] = time.Now()
	
	return nil
}

// GetAgent retrieves an agent by ID
func (r *AgentRegistry) GetAgent(ctx context.Context, agentID string) (*a2a.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	agent, exists := r.agents[agentID]
	if !exists {
		return nil, errors.New("agent not found")
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

// GetActiveAgents returns only active agents
func (r *AgentRegistry) GetActiveAgents(ctx context.Context) ([]*a2a.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	var active []*a2a.Agent
	now := time.Now()
	
	for _, agent := range r.agents {
		// Check if agent is active and recent heartbeat
		if agent.Status == "active" {
			if heartbeat, exists := r.heartbeat[agent.ID]; exists {
				if now.Sub(heartbeat) < 5*time.Minute { // 5 minute timeout
					active = append(active, agent)
				}
			} else {
				// No heartbeat recorded, assume active
				active = append(active, agent)
			}
		}
	}
	
	return active, nil
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
		return errors.New("agent not found")
	}
	
	r.heartbeat[agentID] = time.Now()
	return nil
}
```

### 4. A2A Protocol Tools (MCP)

```go
// Package mcp_a2a provides MCP tools for A2A protocol operations
package mcp_a2a

import (
	"context"
	"encoding/json"
	"fmt"
	
	"github.com/lleontor705/cortex/internal/domain/a2a"
	"github.com/lleontor705/cortex/internal/domain/registry"
	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterA2ATools registers A2A protocol tools on the MCP server
func RegisterA2ATools(srv *server.MCPServer, agentRegistry *registry.AgentRegistry, transport a2a.Transport, allowlist map[string]bool) {
	h := NewA2AToolsHandler(agentRegistry, transport)

	type toolDef struct {
		name string
		desc string
		fn   func(context.Context, *protocol.CallToolRequest) (*protocol.CallToolResult, error)
	}

	tools := []toolDef{
		{"a2a_register_agent", "Register an A2A agent in the registry", h.RegisterAgent},
		{"a2a_get_agent", "Get information about a specific agent", h.GetAgent},
		{"a2a_list_agents", "List all registered A2A agents", h.ListAgents},
		{"a2a_send_message", "Send an A2A message to specific agents", h.SendMessage},
		{"a2a_broadcast_message", "Broadcast an A2A message to all agents", h.BroadcastMessage},
		{"a2a_create_session", "Create an A2A session for context sharing", h.CreateSession},
		{"a2a_list_sessions", "List active A2A sessions", h.ListSessions},
		{"a2a_heartbeat", "Send heartbeat for agent liveness", h.Heartbeat},
		{"a2a_agent_capabilities", "Get capabilities of an agent", h.GetAgentCapabilities},
	}

	for _, td := range tools {
		if !shouldRegister(td.name, allowlist) {
			continue
		}
		fn := td.fn // capture for closure
		tool := protocol.NewTool(td.name, protocol.WithDescription(td.desc))
		srv.AddTool(tool, func(ctx context.Context, req protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			return fn(ctx, &req)
		})
	}
}

// A2AToolsHandler handles A2A protocol MCP tools
type A2AToolsHandler struct {
	agentRegistry *registry.AgentRegistry
	transport     a2a.Transport
}

func NewA2AToolsHandler(agentRegistry *registry.AgentRegistry, transport a2a.Transport) *A2AToolsHandler {
	return &A2AToolsHandler{
		agentRegistry: agentRegistry,
		transport:     transport,
	}
}

// RegisterAgent handles agent registration
func (h *A2AToolsHandler) RegisterAgent(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		ID          string                 `json:"id"`
		Name        string                 `json:"name"`
		Version     string                 `json:"version"`
		Capabilities []string              `json:"capabilities"`
		Metadata    map[string]interface{} `json:"metadata"`
		Endpoint    string                 `json:"endpoint,omitempty"`
		Status      string                 `json:"status"`
	}

	if err := req.BindArguments(&params); err != nil {
		return toolErrorResult("Invalid parameters: %v", err), nil
	}

	if params.ID == "" {
		return toolErrorResult("Agent ID is required"), nil
	}

	agent := &a2a.Agent{
		ID:          params.ID,
		Name:        params.Name,
		Version:     params.Version,
		Capabilities: params.Capabilities,
		Metadata:    params.Metadata,
		Endpoint:    params.Endpoint,
		Status:      params.Status,
		LastSeen:    time.Now(),
	}

	if err := h.agentRegistry.RegisterAgent(ctx, agent); err != nil {
		return toolErrorResult("Failed to register agent: %v", err), nil
	}

	return toolTextResult(fmt.Sprintf("Agent %s registered successfully", agent.ID)), nil
}

// GetAgent retrieves agent information
func (h *A2AToolsHandler) GetAgent(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		AgentID string `json:"agent_id"`
	}

	if err := req.BindArguments(&params); err != nil {
		return toolErrorResult("Invalid parameters: %v", err), nil
	}

	if params.AgentID == "" {
		return toolErrorResult("Agent ID is required"), nil
	}

	agent, err := h.agentRegistry.GetAgent(ctx, params.AgentID)
	if err != nil {
		return toolErrorResult("Failed to get agent: %v", err), nil
	}

	agentJSON, err := json.Marshal(agent)
	if err != nil {
		return toolErrorResult("Failed to serialize agent: %v", err), nil
	}

	return toolTextResult(string(agentJSON)), nil
}

// ListAgents lists all registered agents
func (h *A2AToolsHandler) ListAgents(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	agents, err := h.agentRegistry.GetAgents(ctx)
	if err != nil {
		return toolErrorResult("Failed to list agents: %v", err), nil
	}

	agentsJSON, err := json.Marshal(agents)
	if err != nil {
		return toolErrorResult("Failed to serialize agents: %v", err), nil
	}

	return toolTextResult(string(agentsJSON)), nil
}

// SendMessage sends a message to specific agents
func (h *A2AToolsHandler) SendMessage(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		From      string                 `json:"from"`
		To        []string               `json:"to"`
		Type      string                 `json:"type"`
		Topic     string                 `json:"topic"`
		Payload   interface{}            `json:"payload"`
		Headers   map[string]interface{} `json:"headers,omitempty"`
		SessionID string                 `json:"session_id,omitempty"`
		TraceID   string                 `json:"trace_id,omitempty"`
	}

	if err := req.BindArguments(&params); err != nil {
		return toolErrorResult("Invalid parameters: %v", err), nil
	}

	if params.From == "" || len(params.To) == 0 {
		return toolErrorResult("From and To parameters are required"), nil
	}

	message := &a2a.Message{
		ID:        generateMessageID(),
		Type:      a2a.MessageType(params.Type),
		From:      params.From,
		To:        params.To,
		Topic:     params.Topic,
		Headers:   params.Headers,
		Payload:   params.Payload,
		Timestamp: time.Now(),
		SessionID: params.SessionID,
		TraceID:   params.TraceID,
	}

	if err := h.transport.Send(ctx, message, params.To...); err != nil {
		return toolErrorResult("Failed to send message: %v", err), nil
	}

	return toolTextResult(fmt.Sprintf("Message %s sent to %v", message.ID, params.To)), nil
}

// BroadcastMessage broadcasts to all agents
func (h *A2AToolsHandler) BroadcastMessage(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		From      string                 `json:"from"`
		Type      string                 `json:"type"`
		Topic     string                 `json:"topic"`
		Payload   interface{}            `json:"payload"`
		Headers   map[string]interface{} `json:"headers,omitempty"`
		TraceID   string                 `json:"trace_id,omitempty"`
	}

	if err := req.BindArguments(&params); err != nil {
		return toolErrorResult("Invalid parameters: %v", err), nil
	}

	if params.From == "" {
		return toolErrorResult("From parameter is required"), nil
	}

	message := &a2a.Message{
		ID:        generateMessageID(),
		Type:      a2a.MessageType(params.Type),
		From:      params.From,
		To:        []string{}, // Empty means broadcast to all
		Topic:     params.Topic,
		Headers:   params.Headers,
		Payload:   params.Payload,
		Timestamp: time.Now(),
		TraceID:   params.TraceID,
	}

	if err := h.transport.Broadcast(ctx, message); err != nil {
		return toolErrorResult("Failed to broadcast message: %v", err), nil
	}

	return toolTextResult(fmt.Sprintf("Message %s broadcast successfully", message.ID)), nil
}

// Helper functions
func toolTextResult(text string) *protocol.CallToolResult {
	return &protocol.CallToolResult{
		Content: []protocol.Content{protocol.NewTextContent(text)},
	}
}

func toolErrorResult(format string, args ...interface{}) *protocol.CallToolResult {
	return &protocol.CallToolResult{
		Content: []protocol.Content{protocol.NewTextContent(fmt.Sprintf(format, args...))},
		IsError: true,
	}

func generateMessageID() string {
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}
```

## Integration Points

### 1. MCP Server Integration

Update `internal/mcp/server.go` to include A2A tools:

```go
// In main MCP server setup
registerA2ATools(srv, stores.AgentRegistry, stores.A2ATransport, allowlist)
```

### 2. Domain Layer Updates

Update `internal/domain/models.go` to include A2A-related types:

```go
// Add to existing models.go
package domain

// A2A Integration types
type A2AAgent struct {
	// Embed a2a.Agent or add relevant fields
}

type A2AMessage struct {
	// Embed a2a.Message or add relevant fields
}
```

### 3. Configuration Updates

Update `internal/config/config.go` to include A2A configuration:

```go
type A2AConfig struct {
	TransportType    string            `yaml:"transport_type"`    // stdio, http
	AgentID         string            `yaml:"agent_id"`
	AgentName       string            `yaml:"agent_name"`
	AgentCapabilities []string         `yaml:"agent_capabilities"`
	RegistryURL     string            `yaml:"registry_url"`
	EnableDiscovery bool              `yaml:"enable_discovery"`
	DiscoveryPort   int               `yaml:"discovery_port"`
}
```

## Usage Examples

### 1. Registering an Agent

```bash
cortex mcp --tools=a2a
```

```json
{
  "method": "tools/call",
  "params": {
    "name": "a2a_register_agent",
    "arguments": {
      "id": "cortex-memory",
      "name": "Cortex Memory Agent",
      "version": "1.0.0",
      "capabilities": ["memory-search", "temporal-graph"],
      "status": "active"
    }
  }
}
```

### 2. Sending a Message

```json
{
  "method": "tools/call",
  "params": {
    "name": "a2a_send_message",
    "arguments": {
      "from": "cortex-memory",
      "to": ["claude-code", "gemini-cli"],
      "type": "notify",
      "topic": "memory-updated",
      "payload": {
        "observation_id": 123,
        "action": "saved"
      }
    }
  }
}
```

### 3. Broadcasting Updates

```json
{
  "method": "tools/call",
  "params": {
    "name": "a2a_broadcast_message",
    "arguments": {
      "from": "cortex-memory",
      "type": "event",
      "topic": "system-metrics",
      "payload": {
        "observation_count": 1500,
        "edge_count": 8500,
        "timestamp": "2026-03-31T18:00:00Z"
      }
    }
  }
}
```

## Benefits

1. **Interoperability**: Cortex can communicate with any A2A-compatible agent
2. **Standardization**: Follows emerging A2A protocol standards
3. **Extensibility**: Easy to add new agents and capabilities
4. **Discovery**: Automatic agent discovery and registration
5. **Integration**: Seamless integration with existing MCP tools

## Testing Strategy

1. Unit tests for A2A message format validation
2. Integration tests for transport layer
3. End-to-end tests for agent registration and messaging
4. Performance tests for high message throughput
5. Compatibility tests with other A2A implementations
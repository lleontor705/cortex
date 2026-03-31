// Package mcp_a2a provides MCP tools for A2A protocol operations
//
// This package implements MCP (Model Context Protocol) tools that enable
// Cortex to participate in A2A (Agent-to-Agent) Protocol communication.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/lleontor705/cortex/internal/domain/a2a"
	a2a_registry "github.com/lleontor705/cortex/internal/registry/a2a"
	a2a_transport "github.com/lleontor705/cortex/internal/transport/a2a"
	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterA2ATools registers A2A protocol tools on the MCP server
func RegisterA2ATools(srv *server.MCPServer, registry *a2a_registry.AgentRegistry, transport a2a_transport.Transport, allowlist map[string]bool) {
	h := NewA2AToolsHandler(registry, transport)

	tools := []struct {
		name string
		desc string
		fn   func(context.Context, *protocol.CallToolRequest) (*protocol.CallToolResult, error)
	}{
		{"a2a_register_agent", "Register an A2A agent in the registry", h.RegisterAgent},
		{"a2a_get_agent", "Get information about a specific agent", h.GetAgent},
		{"a2a_list_agents", "List all registered A2A agents", h.ListAgents},
		{"a2a_list_active_agents", "List only active A2A agents", h.ListActiveAgents},
		{"a2a_send_message", "Send an A2A message to specific agents", h.SendMessage},
		{"a2a_broadcast_message", "Broadcast an A2A message to all agents", h.BroadcastMessage},
		{"a2a_create_session", "Create an A2A session for context sharing", h.CreateSession},
		{"a2a_list_sessions", "List active A2A sessions", h.ListSessions},
		{"a2a_heartbeat", "Send heartbeat for agent liveness", h.Heartbeat},
		{"a2a_agent_capabilities", "Get capabilities of an agent", h.GetAgentCapabilities},
		{"a2a_discover_agents", "Discover agents based on capabilities", h.DiscoverAgents},
		{"a2a_update_agent_status", "Update an agent's status", h.UpdateAgentStatus},
		{"a2a_get_registry_stats", "Get registry statistics", h.GetRegistryStats},
		{"a2a_transport_stats", "Get transport statistics", h.GetTransportStats},
	}

	for _, td := range tools {
		if !shouldRegister(td.name, allowlist) {
			continue
		}
		fn := td.fn
		tool := protocol.NewTool(td.name, protocol.WithDescription(td.desc))
		srv.AddTool(tool, func(ctx context.Context, req protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			return fn(ctx, &req)
		})
	}
}

// A2AToolsHandler handles A2A protocol MCP tools
type A2AToolsHandler struct {
	registry  *a2a_registry.AgentRegistry
	transport a2a_transport.Transport
}

// NewA2AToolsHandler creates a new handler for A2A tools
func NewA2AToolsHandler(registry *a2a_registry.AgentRegistry, transport a2a_transport.Transport) *A2AToolsHandler {
	return &A2AToolsHandler{
		registry:  registry,
		transport: transport,
	}
}

// RegisterAgent handles agent registration
func (h *A2AToolsHandler) RegisterAgent(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		ID           string                 `json:"id"`
		Name         string                 `json:"name"`
		Version      string                 `json:"version"`
		Capabilities []a2a.Capability       `json:"capabilities"`
		Metadata     map[string]interface{} `json:"metadata,omitempty"`
		Endpoint     string                 `json:"endpoint,omitempty"`
		Status       string                 `json:"status"`
	}

	if err := req.BindArguments(&params); err != nil {
		return toolErrorResult("Invalid parameters: %v", err), nil
	}
	if params.ID == "" {
		return toolErrorResult("Agent ID is required"), nil
	}
	if params.Name == "" {
		return toolErrorResult("Agent name is required"), nil
	}

	var status a2a.AgentStatus
	switch params.Status {
	case "active":
		status = a2a.StatusActive
	case "inactive":
		status = a2a.StatusInactive
	case "error":
		status = a2a.StatusError
	default:
		status = a2a.StatusUnknown
	}

	agent := &a2a.Agent{
		ID:           params.ID,
		Name:         params.Name,
		Version:      params.Version,
		Capabilities: params.Capabilities,
		Metadata:     params.Metadata,
		Endpoint:     params.Endpoint,
		Status:       status,
		LastSeen:     time.Now(),
	}

	if err := h.registry.RegisterAgent(ctx, agent); err != nil {
		return toolErrorResult("Failed to register agent: %v", err), nil
	}

	if params.Endpoint != "" {
		_ = h.transport.RegisterAgent(ctx, params.ID, params.Endpoint)
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

	agent, err := h.registry.GetAgent(ctx, params.AgentID)
	if err != nil {
		return toolErrorResult("Failed to get agent: %v", err), nil
	}

	agentJSON, err := json.MarshalIndent(agent, "", "  ")
	if err != nil {
		return toolErrorResult("Failed to serialize agent: %v", err), nil
	}

	return toolTextResult(string(agentJSON)), nil
}

// ListAgents lists all registered agents
func (h *A2AToolsHandler) ListAgents(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	agents, err := h.registry.GetAgents(ctx)
	if err != nil {
		return toolErrorResult("Failed to list agents: %v", err), nil
	}

	agentsJSON, err := json.MarshalIndent(agents, "", "  ")
	if err != nil {
		return toolErrorResult("Failed to serialize agents: %v", err), nil
	}

	return toolTextResult(string(agentsJSON)), nil
}

// ListActiveAgents lists only active agents
func (h *A2AToolsHandler) ListActiveAgents(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	agents, err := h.registry.GetActiveAgents(ctx)
	if err != nil {
		return toolErrorResult("Failed to list active agents: %v", err), nil
	}

	agentsJSON, err := json.MarshalIndent(agents, "", "  ")
	if err != nil {
		return toolErrorResult("Failed to serialize active agents: %v", err), nil
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

	var msgType a2a.MessageType
	switch params.Type {
	case "request":
		msgType = a2a.MessageTypeRequest
	case "response":
		msgType = a2a.MessageTypeResponse
	case "notify":
		msgType = a2a.MessageTypeNotify
	case "event":
		msgType = a2a.MessageTypeEvent
	case "stream":
		msgType = a2a.MessageTypeStream
	default:
		return toolErrorResult("Invalid message type: %s", params.Type), nil
	}

	message := a2a.NewMessage(msgType, params.From, params.To, params.Topic, params.Payload)

	if params.Headers != nil {
		for key, value := range params.Headers {
			message.AddHeader(key, value)
		}
	}
	if params.SessionID != "" {
		message.SessionID = params.SessionID
	}
	if params.TraceID != "" {
		message.TraceID = params.TraceID
	}

	if err := h.transport.Send(ctx, message, params.To...); err != nil {
		return toolErrorResult("Failed to send message: %v", err), nil
	}

	return toolTextResult(fmt.Sprintf("Message %s sent to %v", message.ID, params.To)), nil
}

// BroadcastMessage broadcasts to all agents
func (h *A2AToolsHandler) BroadcastMessage(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		From    string                 `json:"from"`
		Type    string                 `json:"type"`
		Topic   string                 `json:"topic"`
		Payload interface{}            `json:"payload"`
		Headers map[string]interface{} `json:"headers,omitempty"`
		TraceID string                 `json:"trace_id,omitempty"`
	}

	if err := req.BindArguments(&params); err != nil {
		return toolErrorResult("Invalid parameters: %v", err), nil
	}
	if params.From == "" {
		return toolErrorResult("From parameter is required"), nil
	}

	var msgType a2a.MessageType
	switch params.Type {
	case "notify":
		msgType = a2a.MessageTypeNotify
	case "event":
		msgType = a2a.MessageTypeEvent
	default:
		return toolErrorResult("Broadcast only supports notify and event message types"), nil
	}

	message := a2a.NewMessage(msgType, params.From, []string{}, params.Topic, params.Payload)

	if params.Headers != nil {
		for key, value := range params.Headers {
			message.AddHeader(key, value)
		}
	}
	if params.TraceID != "" {
		message.TraceID = params.TraceID
	}

	if err := h.transport.Broadcast(ctx, message); err != nil {
		return toolErrorResult("Failed to broadcast message: %v", err), nil
	}

	return toolTextResult(fmt.Sprintf("Message %s broadcast successfully", message.ID)), nil
}

// CreateSession creates an A2A session
func (h *A2AToolsHandler) CreateSession(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		Initiator    string                 `json:"initiator"`
		Participants []string               `json:"participants"`
		Topic        string                 `json:"topic"`
		Metadata     map[string]interface{} `json:"metadata,omitempty"`
	}

	if err := req.BindArguments(&params); err != nil {
		return toolErrorResult("Invalid parameters: %v", err), nil
	}
	if params.Initiator == "" {
		return toolErrorResult("Initiator parameter is required"), nil
	}

	session := &a2a.Session{
		ID:           generateSessionID(),
		Initiator:    params.Initiator,
		Participants: params.Participants,
		StartedAt:    time.Now(),
		Metadata:     params.Metadata,
	}

	sessionJSON, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return toolErrorResult("Failed to serialize session: %v", err), nil
	}

	return toolTextResult(fmt.Sprintf("Session %s created successfully:\n%s", session.ID, string(sessionJSON))), nil
}

// ListSessions lists active A2A sessions
func (h *A2AToolsHandler) ListSessions(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	sessions := []a2a.Session{}
	sessionsJSON, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return toolErrorResult("Failed to serialize sessions: %v", err), nil
	}
	return toolTextResult(string(sessionsJSON)), nil
}

// Heartbeat sends a heartbeat for agent liveness
func (h *A2AToolsHandler) Heartbeat(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		AgentID string `json:"agent_id"`
	}

	if err := req.BindArguments(&params); err != nil {
		return toolErrorResult("Invalid parameters: %v", err), nil
	}
	if params.AgentID == "" {
		return toolErrorResult("Agent ID is required"), nil
	}

	if err := h.registry.Heartbeat(ctx, params.AgentID); err != nil {
		return toolErrorResult("Failed to send heartbeat: %v", err), nil
	}

	return toolTextResult(fmt.Sprintf("Heartbeat sent successfully for agent %s", params.AgentID)), nil
}

// GetAgentCapabilities gets capabilities of an agent
func (h *A2AToolsHandler) GetAgentCapabilities(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		AgentID string `json:"agent_id"`
	}

	if err := req.BindArguments(&params); err != nil {
		return toolErrorResult("Invalid parameters: %v", err), nil
	}
	if params.AgentID == "" {
		return toolErrorResult("Agent ID is required"), nil
	}

	agent, err := h.registry.GetAgent(ctx, params.AgentID)
	if err != nil {
		return toolErrorResult("Failed to get agent: %v", err), nil
	}

	capabilitiesJSON, err := json.MarshalIndent(agent.Capabilities, "", "  ")
	if err != nil {
		return toolErrorResult("Failed to serialize capabilities: %v", err), nil
	}

	return toolTextResult(string(capabilitiesJSON)), nil
}

// DiscoverAgents discovers agents based on capabilities
func (h *A2AToolsHandler) DiscoverAgents(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		Capabilities []string          `json:"capabilities,omitempty"`
		Topics       []string          `json:"topics,omitempty"`
		Status       string            `json:"status,omitempty"`
		Metadata     map[string]string `json:"metadata,omitempty"`
	}

	if err := req.BindArguments(&params); err != nil {
		return toolErrorResult("Invalid parameters: %v", err), nil
	}

	var status a2a.AgentStatus
	switch params.Status {
	case "active":
		status = a2a.StatusActive
	case "inactive":
		status = a2a.StatusInactive
	case "error":
		status = a2a.StatusError
	default:
		status = ""
	}

	criteria := a2a_registry.DiscoveryCriteria{
		Capabilities: params.Capabilities,
		Topics:       params.Topics,
		Status:       status,
		Metadata:     params.Metadata,
	}

	service := a2a_registry.NewRegistryService(h.registry)
	agents, err := service.DiscoverAgents(ctx, criteria)
	if err != nil {
		return toolErrorResult("Failed to discover agents: %v", err), nil
	}

	agentsJSON, err := json.MarshalIndent(agents, "", "  ")
	if err != nil {
		return toolErrorResult("Failed to serialize agents: %v", err), nil
	}

	return toolTextResult(string(agentsJSON)), nil
}

// UpdateAgentStatus updates an agent's status
func (h *A2AToolsHandler) UpdateAgentStatus(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		AgentID string `json:"agent_id"`
		Status  string `json:"status"`
	}

	if err := req.BindArguments(&params); err != nil {
		return toolErrorResult("Invalid parameters: %v", err), nil
	}
	if params.AgentID == "" {
		return toolErrorResult("Agent ID is required"), nil
	}

	var status a2a.AgentStatus
	switch params.Status {
	case "active":
		status = a2a.StatusActive
	case "inactive":
		status = a2a.StatusInactive
	case "error":
		status = a2a.StatusError
	default:
		return toolErrorResult("Invalid status: %s", params.Status), nil
	}

	if err := h.registry.UpdateAgentStatus(ctx, params.AgentID, status); err != nil {
		return toolErrorResult("Failed to update agent status: %v", err), nil
	}

	return toolTextResult(fmt.Sprintf("Agent %s status updated to %s", params.AgentID, params.Status)), nil
}

// GetRegistryStats gets registry statistics
func (h *A2AToolsHandler) GetRegistryStats(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	stats, err := h.registry.GetAgentStats(ctx)
	if err != nil {
		return toolErrorResult("Failed to get registry stats: %v", err), nil
	}

	statsJSON, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return toolErrorResult("Failed to serialize stats: %v", err), nil
	}

	return toolTextResult(string(statsJSON)), nil
}

// GetTransportStats gets transport statistics
func (h *A2AToolsHandler) GetTransportStats(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	stats := h.transport.GetStats()

	statsJSON, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return toolErrorResult("Failed to serialize transport stats: %v", err), nil
	}

	return toolTextResult(string(statsJSON)), nil
}

func generateSessionID() string {
	return fmt.Sprintf("session_%d_%s", time.Now().UnixNano(), randomString(8))
}

func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}
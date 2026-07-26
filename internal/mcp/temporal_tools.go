// Package mcp provides MCP (Model Context Protocol) server implementation
// for Cortex, exposing all memory system capabilities as MCP tools.
//
// This package implements the MCP server with tool profiles, error handling,
// and domain logic delegation to the appropriate services.
package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/domain/observability"
	"github.com/lleontor705/cortex/internal/domain/temporal"
	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerTemporalTools registers temporal graph and observability tools on the
// MCP server using the cortex_temporal_* namespace. These tools require
// MetricsRepository and QualityMetricsRepository in Stores and belong to the
// temporal/advanced profile — they MUST NOT appear in ordinary agent discovery.
func registerTemporalTools(srv *server.MCPServer, stores *Stores, allowlist map[string]bool) {
	if stores.Metrics == nil || stores.QualityMetrics == nil {
		return
	}

	temporalSvc := temporal.NewTemporalService(stores.Graph, stores.Observations, stores.TemporalSnapshots, stores.Metrics)
	observabilitySvc := observability.NewObservabilityService(stores.Metrics, stores.QualityMetrics, stores.TemporalSnapshots, stores.Graph, stores.Observations)
	h := NewTemporalToolsHandler(temporalSvc, observabilitySvc)

	type toolDef struct {
		name string
		desc string
		fn   func(context.Context, *protocol.CallToolRequest) (*protocol.CallToolResult, error)
	}

	tools := []toolDef{
		{"cortex_temporal_create_edge", "Create an edge with temporal validity and evolution tracking", h.CreateTemporalEdge},
		{"cortex_temporal_get_edges", "Retrieve edges valid at a specific time point", h.GetTemporalEdges},
		{"cortex_temporal_get_relevant", "Retrieve observations relevant at a specific time", h.GetTemporalRelevant},
		{"cortex_temporal_create_snapshot", "Create a point-in-time snapshot of the knowledge graph", h.CreateTemporalSnapshot},
		{"cortex_temporal_record_operation", "Record an operation with performance metrics", h.RecordOperation},
		{"cortex_temporal_evaluate_quality", "Evaluate the quality of the memory system", h.EvaluateMemoryQuality},
		{"cortex_temporal_system_metrics", "Retrieve system-wide performance metrics", h.GetSystemMetrics},
		{"cortex_temporal_health_check", "Get system health status", h.GetHealthCheck},
		{"cortex_temporal_evolution_path", "Retrieve the evolution history of an edge", h.GetTemporalEvolutionPath},
		{"cortex_temporal_fact_state", "Get the current state of facts for an observation", h.GetCurrentFactState},
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

// TemporalToolsHandler handles temporal graph and observability MCP tools.
type TemporalToolsHandler struct {
	temporalService      *temporal.TemporalService
	observabilityService *observability.ObservabilityService
}

// NewTemporalToolsHandler creates a new handler for temporal and observability tools.
func NewTemporalToolsHandler(
	temporalService *temporal.TemporalService,
	observabilityService *observability.ObservabilityService,
) *TemporalToolsHandler {
	return &TemporalToolsHandler{
		temporalService:      temporalService,
		observabilityService: observabilityService,
	}
}

// CreateTemporalEdge creates an edge with temporal validity and evolution tracking.
func (h *TemporalToolsHandler) CreateTemporalEdge(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		FromObsID    int64   `json:"from_obs_id"`
		ToObsID      int64   `json:"to_obs_id"`
		RelationType string  `json:"relation_type"`
		Weight       float64 `json:"weight"`
		Confidence   float64 `json:"confidence"`
		Source       string  `json:"source"`
		Reasoning    string  `json:"reasoning"`
		ValidFrom    string  `json:"valid_from"`    // ISO format string
		InvalidAt    string  `json:"invalid_at"`    // ISO format string, optional
		EvolutionType string `json:"evolution_type"`
		FactState     string `json:"fact_state"`
		ChangeReason  string `json:"change_reason"`
	}

	if err := req.BindArguments(&params); err != nil {
		return errorResult("Invalid parameters: %v", err)
	}

	// Convert time strings to time.Time
	var validFrom, invalidAt *time.Time
	if params.ValidFrom != "" {
		t, err := time.Parse(time.RFC3339, params.ValidFrom)
		if err != nil {
			return errorResult("Invalid valid_from format: %v", err)
		}
		validFrom = &t
	}

	if params.InvalidAt != "" {
		t, err := time.Parse(time.RFC3339, params.InvalidAt)
		if err != nil {
			return errorResult("Invalid invalid_at format: %v", err)
		}
		invalidAt = &t
	}

	edge := &domain.Edge{
		FromObsID:    params.FromObsID,
		ToObsID:      params.ToObsID,
		RelationType: params.RelationType,
		Weight:       params.Weight,
		Confidence:   params.Confidence,
		Source:       params.Source,
		Reasoning:    params.Reasoning,
		ValidFrom:    validFrom,
		InvalidAt:    invalidAt,
		EvolutionType: params.EvolutionType,
		FactState:     params.FactState,
		ChangeReason:  params.ChangeReason,
	}

	if err := h.temporalService.CreateTemporalEdge(ctx, edge); err != nil {
		return errorResult("Failed to create temporal edge: %v", err)
	}

	return textResult("Temporal edge created successfully with ID %d", edge.ID)
}

// GetTemporalEdges retrieves edges valid at a specific time point.
func (h *TemporalToolsHandler) GetTemporalEdges(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		ObservationID int64  `json:"observation_id"`
		At            string `json:"at"` // ISO format string
	}

	if err := req.BindArguments(&params); err != nil {
		return errorResult("Invalid parameters: %v", err)
	}

	// Convert time string to time.Time
	atTime, err := time.Parse(time.RFC3339, params.At)
	if err != nil {
		return errorResult("Invalid 'at' format: %v", err)
	}

	edges, err := h.temporalService.GetTemporalEdges(ctx, params.ObservationID, atTime)
	if err != nil {
		return errorResult("Failed to get temporal edges: %v", err)
	}

	// Convert to JSON
	edgesJSON, err := json.Marshal(edges)
	if err != nil {
		return errorResult("Failed to serialize edges: %v", err)
	}

	return textResult("%s", string(edgesJSON))
}

// GetTemporalRelevant retrieves observations relevant at a specific time.
func (h *TemporalToolsHandler) GetTemporalRelevant(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		ObservationID int64  `json:"observation_id"`
		At            string `json:"at"`           // ISO format string
		Depth         int    `json:"depth"`        // Traversal depth
	}

	if err := req.BindArguments(&params); err != nil {
		return errorResult("Invalid parameters: %v", err)
	}

	// Convert time string to time.Time
	atTime, err := time.Parse(time.RFC3339, params.At)
	if err != nil {
		return errorResult("Invalid 'at' format: %v", err)
	}

	observations, err := h.temporalService.GetTemporalRelevant(ctx, params.ObservationID, atTime, params.Depth)
	if err != nil {
		return errorResult("Failed to get temporal relevant observations: %v", err)
	}

	// Convert to JSON
	obsJSON, err := json.Marshal(observations)
	if err != nil {
		return errorResult("Failed to serialize observations: %v", err)
	}

	return textResult("%s", string(obsJSON))
}

// CreateTemporalSnapshot creates a point-in-time snapshot of the knowledge graph.
func (h *TemporalToolsHandler) CreateTemporalSnapshot(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		SnapshotKey     string `json:"snapshot_key"`
		RootObservationID int64  `json:"root_observation_id"`
		Description     string `json:"description"`
	}

	if err := req.BindArguments(&params); err != nil {
		return errorResult("Invalid parameters: %v", err)
	}

	snapshot, err := h.temporalService.CreateTemporalSnapshot(ctx, params.SnapshotKey, params.RootObservationID, params.Description)
	if err != nil {
		return errorResult("Failed to create temporal snapshot: %v", err)
	}

	// Convert to JSON
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return errorResult("Failed to serialize snapshot: %v", err)
	}

	return textResult("%s", string(snapshotJSON))
}

// RecordOperation records an operation with performance metrics.
func (h *TemporalToolsHandler) RecordOperation(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		SessionID          string    `json:"session_id"`
		OperationType      string    `json:"operation_type"`
		Duration           int64     `json:"duration_ms"`
		ResultCount        int       `json:"result_count"`
		Success            bool      `json:"success"`
		Error              string    `json:"error"`
		MemoryUsage        int64     `json:"memory_usage_bytes"`
		Timestamp          string    `json:"timestamp"`  // ISO format
		ObservationCount   int       `json:"observation_count"`
		EdgeCount          int       `json:"edge_count"`
		QueryComplexity    float64   `json:"query_complexity"`
		ConfidenceScore    float64   `json:"confidence_score"`
	}

	if err := req.BindArguments(&params); err != nil {
		return errorResult("Invalid parameters: %v", err)
	}

	// Convert timestamp
	timestamp, err := time.Parse(time.RFC3339, params.Timestamp)
	if err != nil {
		return errorResult("Invalid timestamp format: %v", err)
	}

	metric := &domain.Metrics{
		SessionID:         params.SessionID,
		OperationType:     params.OperationType,
		Duration:          params.Duration,
		ResultCount:       params.ResultCount,
		Success:           params.Success,
		Error:             params.Error,
		MemoryUsage:       params.MemoryUsage,
		Timestamp:         timestamp,
		ObservationCount:  params.ObservationCount,
		EdgeCount:         params.EdgeCount,
		QueryComplexity:   params.QueryComplexity,
		ConfidenceScore:   params.ConfidenceScore,
	}

	if err := h.observabilityService.RecordOperation(ctx, metric); err != nil {
		return errorResult("Failed to record operation: %v", err)
	}

	return textResult("Operation recorded successfully")
}

// EvaluateMemoryQuality evaluates the quality of the memory system.
func (h *TemporalToolsHandler) EvaluateMemoryQuality(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		SessionID    string `json:"session_id"`
		EvaluationType string `json:"evaluation_type"` // relevance, completeness, consistency, temporal_accuracy, overall
	}

	if err := req.BindArguments(&params); err != nil {
		return errorResult("Invalid parameters: %v", err)
	}

	quality, err := h.observabilityService.EvaluateMemoryQuality(ctx, params.SessionID, params.EvaluationType)
	if err != nil {
		return errorResult("Failed to evaluate memory quality: %v", err)
	}

	// Convert to JSON
	qualityJSON, err := json.Marshal(quality)
	if err != nil {
		return errorResult("Failed to serialize quality metrics: %v", err)
	}

	return textResult("%s", string(qualityJSON))
}

// GetSystemMetrics retrieves system-wide performance metrics.
func (h *TemporalToolsHandler) GetSystemMetrics(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		SessionID string `json:"session_id"`
		From      string `json:"from"`  // ISO format
		To        string `json:"to"`    // ISO format
	}

	if err := req.BindArguments(&params); err != nil {
		return errorResult("Invalid parameters: %v", err)
	}

	// Convert time strings
	fromTime, err := time.Parse(time.RFC3339, params.From)
	if err != nil {
		return errorResult("Invalid 'from' format: %v", err)
	}

	toTime, err := time.Parse(time.RFC3339, params.To)
	if err != nil {
		return errorResult("Invalid 'to' format: %v", err)
	}

	metrics, err := h.observabilityService.GetSystemMetrics(ctx, params.SessionID, fromTime, toTime)
	if err != nil {
		return errorResult("Failed to get system metrics: %v", err)
	}

	// Convert to JSON
	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		return errorResult("Failed to serialize metrics: %v", err)
	}

	return textResult("%s", string(metricsJSON))
}

// GetHealthCheck provides system health status.
func (h *TemporalToolsHandler) GetHealthCheck(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	health, err := h.observabilityService.GetHealthCheck(ctx)
	if err != nil {
		return errorResult("Failed to get health check: %v", err)
	}

	// Convert to JSON
	healthJSON, err := json.Marshal(health)
	if err != nil {
		return errorResult("Failed to serialize health check: %v", err)
	}

	return textResult("%s", string(healthJSON))
}

// GetTemporalEvolutionPath retrieves the evolution history of an edge.
func (h *TemporalToolsHandler) GetTemporalEvolutionPath(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		EdgeID int64 `json:"edge_id"`
	}

	if err := req.BindArguments(&params); err != nil {
		return errorResult("Invalid parameters: %v", err)
	}

	evolutionPath, err := h.temporalService.GetEvolutionPath(ctx, params.EdgeID)
	if err != nil {
		return errorResult("Failed to get evolution path: %v", err)
	}

	// Convert to JSON
	pathJSON, err := json.Marshal(evolutionPath)
	if err != nil {
		return errorResult("Failed to serialize evolution path: %v", err)
	}

	return textResult("%s", string(pathJSON))
}

// GetCurrentFactState determines the current state of facts related to an observation.
func (h *TemporalToolsHandler) GetCurrentFactState(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		ObservationID int64 `json:"observation_id"`
	}

	if err := req.BindArguments(&params); err != nil {
		return errorResult("Invalid parameters: %v", err)
	}

	facts, err := h.temporalService.GetCurrentFactState(ctx, params.ObservationID)
	if err != nil {
		return errorResult("Failed to get current fact state: %v", err)
	}

	// Convert to JSON
	factsJSON, err := json.Marshal(facts)
	if err != nil {
		return errorResult("Failed to serialize facts: %v", err)
	}

	return textResult("%s", string(factsJSON))
}

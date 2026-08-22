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
		tool := temporalTool(td.name, td.desc)
		srv.AddTool(tool, func(ctx context.Context, req protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			return fn(ctx, &req)
		})
	}
}

func temporalTool(name, description string) protocol.Tool {
	desc := protocol.WithDescription(description)
	requiredString := func(name, description string) protocol.ToolOption {
		return protocol.WithString(name, protocol.Required(), protocol.Description(description))
	}
	switch name {
	case "cortex_temporal_create_edge":
		return protocol.NewTool(name, desc,
			withIntegerID("from_obs_id", "Source observation ID"),
			withIntegerID("to_obs_id", "Target observation ID"),
			requiredString("relation_type", "references, relates_to, follows, supersedes, or contradicts"),
			protocol.WithNumber("weight", protocol.Description("Relationship strength 0-10")),
			protocol.WithNumber("confidence", protocol.Description("Confidence 0-1")),
			protocol.WithString("source", protocol.Description("Edge source")),
			protocol.WithString("reasoning", protocol.Description("Relationship rationale")),
			protocol.WithString("valid_from", protocol.Description("RFC3339 validity start")),
			protocol.WithString("invalid_at", protocol.Description("RFC3339 invalidation time")),
			protocol.WithString("evolution_type", protocol.Description("original, modified, superseded, or contradicted")),
			protocol.WithString("fact_state", protocol.Description("current, historical, deprecated, or superseded")),
			protocol.WithString("change_reason", protocol.Description("Reason for the change")))
	case "cortex_temporal_get_edges":
		return protocol.NewTool(name, desc, withIntegerID("observation_id", "Observation ID"), requiredString("at", "RFC3339 point in time"))
	case "cortex_temporal_get_relevant":
		return protocol.NewTool(name, desc, withIntegerID("observation_id", "Observation ID"), requiredString("at", "RFC3339 point in time"), protocol.WithNumber("depth", protocol.Description("Traversal depth")))
	case "cortex_temporal_create_snapshot":
		return protocol.NewTool(name, desc, requiredString("snapshot_key", "Stable snapshot key"), withIntegerID("root_observation_id", "Root observation ID"), protocol.WithString("description", protocol.Description("Snapshot description")))
	case "cortex_temporal_record_operation":
		return protocol.NewTool(name, desc,
			requiredString("session_id", "Session ID"), requiredString("operation_type", "Operation type"),
			requiredString("timestamp", "RFC3339 operation timestamp"),
			protocol.WithNumber("duration_ms", protocol.Description("Duration in milliseconds")),
			protocol.WithNumber("result_count", protocol.Description("Result count")),
			protocol.WithBoolean("success", protocol.Description("Whether the operation succeeded")),
			protocol.WithString("error", protocol.Description("Error message")),
			protocol.WithNumber("memory_usage_bytes", protocol.Description("Memory usage in bytes")),
			protocol.WithNumber("observation_count", protocol.Description("Observation count")),
			protocol.WithNumber("edge_count", protocol.Description("Edge count")),
			protocol.WithNumber("query_complexity", protocol.Description("Query complexity score")),
			protocol.WithNumber("confidence_score", protocol.Description("Confidence score")))
	case "cortex_temporal_evaluate_quality":
		return protocol.NewTool(name, desc, requiredString("session_id", "Session ID"), requiredString("evaluation_type", "relevance, completeness, consistency, temporal_accuracy, or overall"))
	case "cortex_temporal_system_metrics":
		return protocol.NewTool(name, desc, requiredString("session_id", "Session ID"), requiredString("from", "RFC3339 range start"), requiredString("to", "RFC3339 range end"))
	case "cortex_temporal_evolution_path":
		return protocol.NewTool(name, desc, withIntegerID("edge_id", "Edge ID"))
	case "cortex_temporal_fact_state":
		return protocol.NewTool(name, desc, withIntegerID("observation_id", "Observation ID"))
	default:
		return protocol.NewTool(name, desc)
	}
}

// temporalValidationError renders a constant validation failure tagged with
// the stable validation code. Raw binder/parser internals and caller input
// are never interpolated (T08R / QW-02).
func temporalValidationError(message string) (*protocol.CallToolResult, error) {
	return errorResult("%s [code: validation]", message)
}

// temporalJSONResult renders a successful payload as a text result; a
// serialization failure is lowered to a constant redacted error, never the
// raw marshal cause (T08R / QW-02).
func temporalJSONResult(payload any, label string) (*protocol.CallToolResult, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return errorResult("Failed to serialize %s: %s", label, localErrorText(err))
	}
	return textResult("%s", string(data))
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
		FromObsID     int64   `json:"from_obs_id"`
		ToObsID       int64   `json:"to_obs_id"`
		RelationType  string  `json:"relation_type"`
		Weight        float64 `json:"weight"`
		Confidence    float64 `json:"confidence"`
		Source        string  `json:"source"`
		Reasoning     string  `json:"reasoning"`
		ValidFrom     string  `json:"valid_from"` // ISO format string
		InvalidAt     string  `json:"invalid_at"` // ISO format string, optional
		EvolutionType string  `json:"evolution_type"`
		FactState     string  `json:"fact_state"`
		ChangeReason  string  `json:"change_reason"`
	}

	if err := req.BindArguments(&params); err != nil {
		return temporalValidationError("Invalid parameters: arguments do not match the tool schema")
	}

	// Strict identifier validation before any store call: BindArguments
	// rejects non-numeric/fractional/overflowing JSON values itself, but
	// missing, null, zero, and negative values bind to 0/negative int64s
	// that must never reach the store layer (T07 / QW-01).
	if params.FromObsID <= 0 || params.ToObsID <= 0 {
		return errorResult("from_obs_id and to_obs_id must be positive integers")
	}

	// Convert time strings to time.Time
	var validFrom, invalidAt *time.Time
	if params.ValidFrom != "" {
		t, err := time.Parse(time.RFC3339, params.ValidFrom)
		if err != nil {
			return temporalValidationError("Invalid valid_from format: expected RFC3339 (e.g. 2025-06-15T10:00:00Z)")
		}
		validFrom = &t
	}

	if params.InvalidAt != "" {
		t, err := time.Parse(time.RFC3339, params.InvalidAt)
		if err != nil {
			return temporalValidationError("Invalid invalid_at format: expected RFC3339 (e.g. 2025-06-15T10:00:00Z)")
		}
		invalidAt = &t
	}

	edge := &domain.Edge{
		FromObsID:     params.FromObsID,
		ToObsID:       params.ToObsID,
		RelationType:  params.RelationType,
		Weight:        params.Weight,
		Confidence:    params.Confidence,
		Source:        params.Source,
		Reasoning:     params.Reasoning,
		ValidFrom:     validFrom,
		InvalidAt:     invalidAt,
		EvolutionType: params.EvolutionType,
		FactState:     params.FactState,
		ChangeReason:  params.ChangeReason,
	}

	if err := h.temporalService.CreateTemporalEdge(ctx, edge); err != nil {
		return errorResult("Failed to create temporal edge: %s", localErrorText(err))
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
		return temporalValidationError("Invalid parameters: arguments do not match the tool schema")
	}

	// Strict identifier validation before any store call (T07 / QW-01).
	if params.ObservationID <= 0 {
		return errorResult("observation_id must be a positive integer")
	}

	// Convert time string to time.Time
	atTime, err := time.Parse(time.RFC3339, params.At)
	if err != nil {
		return temporalValidationError("Invalid 'at' format: expected RFC3339 (e.g. 2025-06-15T10:00:00Z)")
	}

	edges, err := h.temporalService.GetTemporalEdges(ctx, params.ObservationID, atTime)
	if err != nil {
		return errorResult("Failed to get temporal edges: %s", localErrorText(err))
	}

	return temporalJSONResult(edges, "edges")
}

// GetTemporalRelevant retrieves observations relevant at a specific time.
func (h *TemporalToolsHandler) GetTemporalRelevant(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		ObservationID int64  `json:"observation_id"`
		At            string `json:"at"`    // ISO format string
		Depth         int    `json:"depth"` // Traversal depth
	}

	if err := req.BindArguments(&params); err != nil {
		return temporalValidationError("Invalid parameters: arguments do not match the tool schema")
	}

	// Strict identifier validation before any store call (T07 / QW-01).
	if params.ObservationID <= 0 {
		return errorResult("observation_id must be a positive integer")
	}

	// Convert time string to time.Time
	atTime, err := time.Parse(time.RFC3339, params.At)
	if err != nil {
		return temporalValidationError("Invalid 'at' format: expected RFC3339 (e.g. 2025-06-15T10:00:00Z)")
	}

	observations, err := h.temporalService.GetTemporalRelevant(ctx, params.ObservationID, atTime, params.Depth)
	if err != nil {
		return errorResult("Failed to get temporal relevant observations: %s", localErrorText(err))
	}

	return temporalJSONResult(observations, "observations")
}

// CreateTemporalSnapshot creates a point-in-time snapshot of the knowledge graph.
func (h *TemporalToolsHandler) CreateTemporalSnapshot(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		SnapshotKey       string `json:"snapshot_key"`
		RootObservationID int64  `json:"root_observation_id"`
		Description       string `json:"description"`
	}

	if err := req.BindArguments(&params); err != nil {
		return temporalValidationError("Invalid parameters: arguments do not match the tool schema")
	}

	// Strict identifier validation before any store call (T07 / QW-01).
	if params.RootObservationID <= 0 {
		return errorResult("root_observation_id must be a positive integer")
	}

	snapshot, err := h.temporalService.CreateTemporalSnapshot(ctx, params.SnapshotKey, params.RootObservationID, params.Description)
	if err != nil {
		return errorResult("Failed to create temporal snapshot: %s", localErrorText(err))
	}

	return temporalJSONResult(snapshot, "snapshot")
}

// RecordOperation records an operation with performance metrics.
func (h *TemporalToolsHandler) RecordOperation(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		SessionID        string  `json:"session_id"`
		OperationType    string  `json:"operation_type"`
		Duration         int64   `json:"duration_ms"`
		ResultCount      int     `json:"result_count"`
		Success          bool    `json:"success"`
		Error            string  `json:"error"`
		MemoryUsage      int64   `json:"memory_usage_bytes"`
		Timestamp        string  `json:"timestamp"` // ISO format
		ObservationCount int     `json:"observation_count"`
		EdgeCount        int     `json:"edge_count"`
		QueryComplexity  float64 `json:"query_complexity"`
		ConfidenceScore  float64 `json:"confidence_score"`
	}

	if err := req.BindArguments(&params); err != nil {
		return temporalValidationError("Invalid parameters: arguments do not match the tool schema")
	}

	// Convert timestamp
	timestamp, err := time.Parse(time.RFC3339, params.Timestamp)
	if err != nil {
		return temporalValidationError("Invalid timestamp format: expected RFC3339 (e.g. 2025-06-15T10:00:00Z)")
	}

	metric := &domain.Metrics{
		SessionID:        params.SessionID,
		OperationType:    params.OperationType,
		Duration:         params.Duration,
		ResultCount:      params.ResultCount,
		Success:          params.Success,
		Error:            params.Error,
		MemoryUsage:      params.MemoryUsage,
		Timestamp:        timestamp,
		ObservationCount: params.ObservationCount,
		EdgeCount:        params.EdgeCount,
		QueryComplexity:  params.QueryComplexity,
		ConfidenceScore:  params.ConfidenceScore,
	}

	if err := h.observabilityService.RecordOperation(ctx, metric); err != nil {
		return errorResult("Failed to record operation: %s", localErrorText(err))
	}

	return textResult("Operation recorded successfully")
}

// EvaluateMemoryQuality evaluates the quality of the memory system.
func (h *TemporalToolsHandler) EvaluateMemoryQuality(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		SessionID      string `json:"session_id"`
		EvaluationType string `json:"evaluation_type"` // relevance, completeness, consistency, temporal_accuracy, overall
	}

	if err := req.BindArguments(&params); err != nil {
		return temporalValidationError("Invalid parameters: arguments do not match the tool schema")
	}

	quality, err := h.observabilityService.EvaluateMemoryQuality(ctx, params.SessionID, params.EvaluationType)
	if err != nil {
		return errorResult("Failed to evaluate memory quality: %s", localErrorText(err))
	}

	return temporalJSONResult(quality, "quality metrics")
}

// GetSystemMetrics retrieves system-wide performance metrics.
func (h *TemporalToolsHandler) GetSystemMetrics(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		SessionID string `json:"session_id"`
		From      string `json:"from"` // ISO format
		To        string `json:"to"`   // ISO format
	}

	if err := req.BindArguments(&params); err != nil {
		return temporalValidationError("Invalid parameters: arguments do not match the tool schema")
	}

	// Convert time strings
	fromTime, err := time.Parse(time.RFC3339, params.From)
	if err != nil {
		return temporalValidationError("Invalid 'from' format: expected RFC3339 (e.g. 2025-06-15T10:00:00Z)")
	}

	toTime, err := time.Parse(time.RFC3339, params.To)
	if err != nil {
		return temporalValidationError("Invalid 'to' format: expected RFC3339 (e.g. 2025-06-15T10:00:00Z)")
	}

	metrics, err := h.observabilityService.GetSystemMetrics(ctx, params.SessionID, fromTime, toTime)
	if err != nil {
		return errorResult("Failed to get system metrics: %s", localErrorText(err))
	}

	return temporalJSONResult(metrics, "metrics")
}

// GetHealthCheck provides system health status.
func (h *TemporalToolsHandler) GetHealthCheck(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	health, err := h.observabilityService.GetHealthCheck(ctx)
	if err != nil {
		return errorResult("Failed to get health check: %s", localErrorText(err))
	}

	return temporalJSONResult(health, "health check")
}

// GetTemporalEvolutionPath retrieves the evolution history of an edge.
func (h *TemporalToolsHandler) GetTemporalEvolutionPath(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		EdgeID int64 `json:"edge_id"`
	}

	if err := req.BindArguments(&params); err != nil {
		return temporalValidationError("Invalid parameters: arguments do not match the tool schema")
	}

	// Strict identifier validation before any store call (T07 / QW-01).
	if params.EdgeID <= 0 {
		return errorResult("edge_id must be a positive integer")
	}

	evolutionPath, err := h.temporalService.GetEvolutionPath(ctx, params.EdgeID)
	if err != nil {
		return errorResult("Failed to get evolution path: %s", localErrorText(err))
	}

	return temporalJSONResult(evolutionPath, "evolution path")
}

// GetCurrentFactState determines the current state of facts related to an observation.
func (h *TemporalToolsHandler) GetCurrentFactState(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		ObservationID int64 `json:"observation_id"`
	}

	if err := req.BindArguments(&params); err != nil {
		return temporalValidationError("Invalid parameters: arguments do not match the tool schema")
	}

	// Strict identifier validation before any store call (T07 / QW-01).
	if params.ObservationID <= 0 {
		return errorResult("observation_id must be a positive integer")
	}

	facts, err := h.temporalService.GetCurrentFactState(ctx, params.ObservationID)
	if err != nil {
		return errorResult("Failed to get current fact state: %s", localErrorText(err))
	}

	return temporalJSONResult(facts, "facts")
}

// Package mcp provides MCP (Model Context Protocol) server implementation
// for Cortex, exposing all memory system capabilities as MCP tools.
//
// This package implements the MCP server with tool profiles, error handling,
// and domain logic delegation to the appropriate services.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/domain/observability"
	"github.com/lleontor705/cortex/internal/domain/temporal"
	"github.com/mark3labs/mcp-go/protocol"
)

// TemporalToolsHandler handles temporal graph and observability MCP tools.
type TemporalToolsHandler struct {
	temporalService    *temporal.Service
	observabilityService *observability.Service
	appConfig         *domain.AppConfig
}

// NewTemporalToolsHandler creates a new handler for temporal and observability tools.
func NewTemporalToolsHandler(
	temporalService *temporal.Service,
	observabilityService *observability.Service,
	appConfig *domain.AppConfig,
) *TemporalToolsHandler {
	return &TemporalToolsHandler{
		temporalService:      temporalService,
		observabilityService: observabilityService,
		appConfig:           appConfig,
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

	if err := json.Unmarshal(req.Params.Value, &params); err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid parameters: %v", err),
			}},
			IsError: true,
		}, nil
	}

	// Convert time strings to time.Time
	var validFrom, invalidAt *time.Time
	if params.ValidFrom != "" {
		t, err := time.Parse(time.RFC3339, params.ValidFrom)
		if err != nil {
			return &protocol.CallToolResult{
				Content: []protocol.TextContent{{
					Type: "text",
					Text: fmt.Sprintf("Invalid valid_from format: %v", err),
				}},
				IsError: true,
			}, nil
		}
		validFrom = &t
	}

	if params.InvalidAt != "" {
		t, err := time.Parse(time.RFC3339, params.InvalidAt)
		if err != nil {
			return &protocol.CallToolResult{
				Content: []protocol.TextContent{{
					Type: "text",
					Text: fmt.Sprintf("Invalid invalid_at format: %v", err),
				}},
				IsError: true,
			}, nil
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
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to create temporal edge: %v", err),
			}},
			IsError: true,
		}, nil
	}

	return &protocol.CallToolResult{
		Content: []protocol.TextContent{{
			Type: "text",
			Text: fmt.Sprintf("Temporal edge created successfully with ID %d", edge.ID),
		}},
	}, nil
}

// GetTemporalEdges retrieves edges valid at a specific time point.
func (h *TemporalToolsHandler) GetTemporalEdges(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		ObservationID int64  `json:"observation_id"`
		At            string `json:"at"` // ISO format string
	}

	if err := json.Unmarshal(req.Params.Value, &params); err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid parameters: %v", err),
			}},
			IsError: true,
		}, nil
	}

	// Convert time string to time.Time
	atTime, err := time.Parse(time.RFC3339, params.At)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid 'at' format: %v", err),
			}},
			IsError: true,
		}, nil
	}

	edges, err := h.temporalService.GetTemporalEdges(ctx, params.ObservationID, atTime)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to get temporal edges: %v", err),
			}},
			IsError: true,
		}, nil
	}

	// Convert to JSON
	edgesJSON, err := json.Marshal(edges)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to serialize edges: %v", err),
			}},
			IsError: true,
		}, nil
	}

	return &protocol.CallToolResult{
		Content: []protocol.TextContent{{
			Type: "text",
			Text: string(edgesJSON),
		}},
	}, nil
}

// GetTemporalRelevant retrieves observations relevant at a specific time.
func (h *TemporalToolsHandler) GetTemporalRelevant(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		ObservationID int64  `json:"observation_id"`
		At            string `json:"at"`           // ISO format string
		Depth         int    `json:"depth"`        // Traversal depth
	}

	if err := json.Unmarshal(req.Params.Value, &params); err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid parameters: %v", err),
			}},
			IsError: true,
		}, nil
	}

	// Convert time string to time.Time
	atTime, err := time.Parse(time.RFC3339, params.At)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid 'at' format: %v", err),
			}},
			IsError: true,
		}, nil
	}

	observations, err := h.temporalService.GetTemporalRelevant(ctx, params.ObservationID, atTime, params.Depth)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to get temporal relevant observations: %v", err),
			}},
			IsError: true,
		}, nil
	}

	// Convert to JSON
	obsJSON, err := json.Marshal(observations)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to serialize observations: %v", err),
			}},
			IsError: true,
		}, nil
	}

	return &protocol.CallToolResult{
		Content: []protocol.TextContent{{
			Type: "text",
			Text: string(obsJSON),
		}},
	}, nil
}

// CreateTemporalSnapshot creates a point-in-time snapshot of the knowledge graph.
func (h *TemporalToolsHandler) CreateTemporalSnapshot(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		SnapshotKey     string `json:"snapshot_key"`
		RootObservationID int64  `json:"root_observation_id"`
		Description     string `json:"description"`
	}

	if err := json.Unmarshal(req.Params.Value, &params); err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid parameters: %v", err),
			}},
			IsError: true,
		}, nil
	}

	snapshot, err := h.temporalService.CreateTemporalSnapshot(ctx, params.SnapshotKey, params.RootObservationID, params.Description)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to create temporal snapshot: %v", err),
			}},
			IsError: true,
		}, nil
	}

	// Convert to JSON
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to serialize snapshot: %v", err),
			}},
			IsError: true,
		}, nil
	}

	return &protocol.CallToolResult{
		Content: []protocol.TextContent{{
			Type: "text",
			Text: string(snapshotJSON),
		}},
	}, nil
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

	if err := json.Unmarshal(req.Params.Value, &params); err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid parameters: %v", err),
			}},
			IsError: true,
		}, nil
	}

	// Convert timestamp
	timestamp, err := time.Parse(time.RFC3339, params.Timestamp)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid timestamp format: %v", err),
			}},
			IsError: true,
		}, nil
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
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to record operation: %v", err),
			}},
			IsError: true,
		}, nil
	}

	return &protocol.CallToolResult{
		Content: []protocol.TextContent{{
			Type: "text",
			Text: "Operation recorded successfully",
		}},
	}, nil
}

// EvaluateMemoryQuality evaluates the quality of the memory system.
func (h *TemporalToolsHandler) EvaluateMemoryQuality(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		SessionID    string `json:"session_id"`
		EvaluationType string `json:"evaluation_type"` // relevance, completeness, consistency, temporal_accuracy, overall
	}

	if err := json.Unmarshal(req.Params.Value, &params); err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid parameters: %v", err),
			}},
			IsError: true,
		}, nil
	}

	quality, err := h.observabilityService.EvaluateMemoryQuality(ctx, params.SessionID, params.EvaluationType)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to evaluate memory quality: %v", err),
			}},
			IsError: true,
		}, nil
	}

	// Convert to JSON
	qualityJSON, err := json.Marshal(quality)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to serialize quality metrics: %v", err),
			}},
			IsError: true,
		}, nil
	}

	return &protocol.CallToolResult{
		Content: []protocol.TextContent{{
			Type: "text",
			Text: string(qualityJSON),
		}},
	}, nil
}

// GetSystemMetrics retrieves system-wide performance metrics.
func (h *TemporalToolsHandler) GetSystemMetrics(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		SessionID string `json:"session_id"`
		From      string `json:"from"`  // ISO format
		To        string `json:"to"`    // ISO format
	}

	if err := json.Unmarshal(req.Params.Value, &params); err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid parameters: %v", err),
			}},
			IsError: true,
		}, nil
	}

	// Convert time strings
	fromTime, err := time.Parse(time.RFC3339, params.From)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid 'from' format: %v", err),
			}},
			IsError: true,
		}, nil
	}

	toTime, err := time.Parse(time.RFC3339, params.To)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid 'to' format: %v", err),
			}},
			IsError: true,
		}, nil
	}

	metrics, err := h.observabilityService.GetSystemMetrics(ctx, params.SessionID, fromTime, toTime)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to get system metrics: %v", err),
			}},
			IsError: true,
		}, nil
	}

	// Convert to JSON
	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to serialize metrics: %v", err),
			}},
			IsError: true,
		}, nil
	}

	return &protocol.CallToolResult{
		Content: []protocol.TextContent{{
			Type: "text",
			Text: string(metricsJSON),
		}},
	}, nil
}

// GetHealthCheck provides system health status.
func (h *TemporalToolsHandler) GetHealthCheck(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	health, err := h.observabilityService.GetHealthCheck(ctx)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to get health check: %v", err),
			}},
			IsError: true,
		}, nil
	}

	// Convert to JSON
	healthJSON, err := json.Marshal(health)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to serialize health check: %v", err),
			}},
			IsError: true,
		}, nil
	}

	return &protocol.CallToolResult{
		Content: []protocol.TextContent{{
			Type: "text",
			Text: string(healthJSON),
		}},
	}, nil
}

// GetTemporalEvolutionPath retrieves the evolution history of an edge.
func (h *TemporalToolsHandler) GetTemporalEvolutionPath(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		EdgeID int64 `json:"edge_id"`
	}

	if err := json.Unmarshal(req.Params.Value, &params); err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid parameters: %v", err),
			}},
			IsError: true,
		}, nil
	}

	evolutionPath, err := h.temporalService.GetEvolutionPath(ctx, params.EdgeID)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to get evolution path: %v", err),
			}},
			IsError: true,
		}, nil
	}

	// Convert to JSON
	pathJSON, err := json.Marshal(evolutionPath)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to serialize evolution path: %v", err),
			}},
			IsError: true,
		}, nil
	}

	return &protocol.CallToolResult{
		Content: []protocol.TextContent{{
			Type: "text",
			Text: string(pathJSON),
		}},
	}, nil
}

// GetCurrentFactState determines the current state of facts related to an observation.
func (h *TemporalToolsHandler) GetCurrentFactState(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	var params struct {
		ObservationID int64 `json:"observation_id"`
	}

	if err := json.Unmarshal(req.Params.Value, &params); err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Invalid parameters: %v", err),
			}},
			IsError: true,
		}, nil
	}

	facts, err := h.temporalService.GetCurrentFactState(ctx, params.ObservationID)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to get current fact state: %v", err),
			}},
			IsError: true,
		}, nil
	}

	// Convert to JSON
	factsJSON, err := json.Marshal(facts)
	if err != nil {
		return &protocol.CallToolResult{
			Content: []protocol.TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Failed to serialize facts: %v", err),
			}},
			IsError: true,
		}, nil
	}

	return &protocol.CallToolResult{
		Content: []protocol.TextContent{{
			Type: "text",
			Text: string(factsJSON),
		}},
	}, nil
}
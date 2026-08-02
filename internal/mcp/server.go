// Package mcp implements the Model Context Protocol server for Cortex.
//
// This exposes memory tools via MCP stdio transport so agents can use
// Cortex as a persistent memory server.
//
// Tool profiles allow agents to load only the tools they need:
//
//	cortex mcp                        -> all tools (default)
//	cortex mcp --tools=agent          -> ordinary agent tools (cortex_* namespace)
//	cortex mcp --tools=admin          -> admin/diagnostic tools
//	cortex mcp --tools=temporal       -> temporal/advanced tools
package mcp

import (
	"fmt"
	"strings"

	"github.com/lleontor705/cortex/internal/store/bundle"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Stores is an alias for bundle.Stores.
type Stores = bundle.Stores

// --- Tool Profiles ---

// ProfileAgent contains the ordinary agent tool set in the cortex_* namespace.
// These are the tools an AI agent needs for proactive memory, search, context,
// and knowledge-graph workflows. Temporal and admin tools are intentionally
// absent — they belong to separate profiles (REQ-MCP-002).
var ProfileAgent = map[string]bool{
	"cortex_save":              true,
	"cortex_search":            true,
	"cortex_context":           true,
	"cortex_session_summary":   true,
	"cortex_session_start":     true,
	"cortex_session_end":       true,
	"cortex_get_observation":   true,
	"cortex_suggest_topic_key": true,
	"cortex_capture_passive":   true,
	"cortex_save_prompt":       true,
	"cortex_update":            true,
	"cortex_relate":            true,
	"cortex_graph":             true,
	"cortex_score":             true,
	"cortex_search_hybrid":     true,
	"cortex_revision_history":  true,
	// Additional agent-useful tools (no orphans — REQ-MCP-002).
	"cortex_consolidate": true,
	"cortex_project_dna": true,
}

// ProfileAdmin contains admin/diagnostic tools for manual curation
// (TUI, CLI, dashboards). Destructive tools carry destructive-hint annotations.
var ProfileAdmin = map[string]bool{
	"cortex_delete":         true,
	"cortex_stats":          true,
	"cortex_timeline":       true,
	"cortex_archive":        true,
	"cortex_merge_projects": true,
}

// ProfileTemporal contains temporal/advanced tools for bi-temporal graph
// queries, observability, and point-in-time analysis. These MUST NOT appear
// in ordinary agent discovery (REQ-MCP-002).
var ProfileTemporal = map[string]bool{
	"cortex_temporal_create_edge":      true,
	"cortex_temporal_create_snapshot":  true,
	"cortex_temporal_evaluate_quality": true,
	"cortex_temporal_evolution_path":   true,
	"cortex_temporal_fact_state":       true,
	"cortex_temporal_get_edges":        true,
	"cortex_temporal_get_relevant":     true,
	"cortex_temporal_health_check":     true,
	"cortex_temporal_record_operation": true,
	"cortex_temporal_system_metrics":   true,
	// Point-in-time search belongs with temporal tools, not ordinary agent.
	"cortex_search_temporal": true,
}

// Profiles maps profile names to their tool sets.
var Profiles = map[string]map[string]bool{
	"agent":    ProfileAgent,
	"admin":    ProfileAdmin,
	"temporal": ProfileTemporal,
}

// ResolveTools takes a comma-separated string of profile names and/or
// individual tool names and returns the set of tool names to register.
// An empty input or "all" means register everything.
func ResolveTools(input string) map[string]bool {
	input = strings.TrimSpace(input)
	if input == "" || input == "all" {
		return nil
	}

	result := make(map[string]bool)
	for _, token := range strings.Split(input, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if token == "all" {
			return nil
		}
		if profile, ok := Profiles[token]; ok {
			for tool := range profile {
				result[tool] = true
			}
		} else {
			result[token] = true
		}
	}
	return result
}

// serverVersion is the MCP server version reported to clients.
const serverVersion = "2.0.0"

const serverInstructions = `Cortex provides persistent memory for AI coding assistants.

CORE MEMORY:
  cortex_save - save decisions, bugs, discoveries PROACTIVELY
  cortex_search - find past work via FTS5 full-text search
  cortex_context - recent session history
  cortex_session_summary - MANDATORY before ending session
  cortex_get_observation - full content by ID
  cortex_save_prompt - save user prompt

KNOWLEDGE GRAPH & SCORING:
  cortex_relate - create relationship between observations
  cortex_graph - traverse knowledge graph from an observation
  cortex_score - get/recalculate importance score
  cortex_search_hybrid - FTS5 + vector search with Reciprocal Rank Fusion
  cortex_revision_history - structured revision snapshots for an observation

ADDITIONAL TOOLS (use ToolSearch):
  cortex_suggest_topic_key, cortex_capture_passive, cortex_session_start,
  cortex_session_end, cortex_update, cortex_consolidate, cortex_project_dna`

// NewServer creates an MCP server with ALL tools registered.
func NewServer(stores *Stores) *server.MCPServer {
	return NewServerWithTools(stores, nil)
}

// NewServerWithTools creates an MCP server registering only the tools in
// the allowlist. If allowlist is nil, all tools are registered.
func NewServerWithTools(stores *Stores, allowlist map[string]bool) *server.MCPServer {
	srv := server.NewMCPServer(
		"cortex",
		serverVersion,
		server.WithToolCapabilities(true),
		server.WithInstructions(serverInstructions),
	)

	registerMemoryTools(srv, stores, allowlist)
	registerCortexTools(srv, stores, allowlist)
	registerTemporalTools(srv, stores, allowlist)
	return srv
}

// shouldRegister returns true if the tool should be registered.
func shouldRegister(name string, allowlist map[string]bool) bool {
	if allowlist == nil {
		return true
	}
	return allowlist[name]
}

// --- Argument Helpers ---

func stringArg(req mcp.CallToolRequest, key string) string {
	v, _ := req.GetArguments()[key].(string)
	return v
}

func intArg(req mcp.CallToolRequest, key string, defaultVal int) int {
	v, ok := req.GetArguments()[key].(float64)
	if !ok {
		return defaultVal
	}
	return int(v)
}

func boolArg(req mcp.CallToolRequest, key string, defaultVal bool) bool {
	v, ok := req.GetArguments()[key].(bool)
	if !ok {
		return defaultVal
	}
	return v
}

// --- Response Helpers ---

func textResult(format string, args ...any) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(fmt.Sprintf(format, args...)), nil
}

func errorResult(format string, args ...any) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultError(fmt.Sprintf(format, args...)), nil
}

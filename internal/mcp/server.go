// Package mcp implements the Model Context Protocol server for Cortex.
//
// This exposes memory tools via MCP stdio transport so agents can use
// Cortex as a persistent memory server.
//
// Tool profiles allow agents to load only the tools they need:
//
//	cortex mcp                        -> all tools (default)
//	cortex mcp --tools=agent          -> Engram-compatible memory tools
//	cortex mcp --tools=admin          -> delete, stats, timeline
package mcp

import (
	"fmt"
	"strings"

	"github.com/lleontor705/cortex/internal/store/bundle"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Stores is an alias for bundle.Stores for backward compatibility.
type Stores = bundle.Stores

// --- Tool Profiles ---

// ProfileAgent contains 11 Engram-compatible memory tools for AI agent workflows
// plus Cortex-exclusive tools (graph, scoring, hybrid search).
var ProfileAgent = map[string]bool{
	"mem_save":              true,
	"mem_search":            true,
	"mem_context":           true,
	"mem_session_summary":   true,
	"mem_session_start":     true,
	"mem_session_end":       true,
	"mem_get_observation":   true,
	"mem_suggest_topic_key": true,
	"mem_capture_passive":   true,
	"mem_save_prompt":       true,
	"mem_update":            true,
	"mem_relate":            true,
	"mem_graph":             true,
	"mem_score":             true,
	"mem_search_hybrid":     true,
	"mem_revision_history":  true,
}

// ProfileAdmin contains tools for manual curation (TUI, CLI, dashboards).
var ProfileAdmin = map[string]bool{
	"mem_delete":           true,
	"mem_stats":            true,
	"mem_timeline":         true,
	"mem_revision_history": true,
	"mem_archive":          true,
	"mem_merge_projects":   true,
}

// Profiles maps profile names to their tool sets.
var Profiles = map[string]map[string]bool{
	"agent": ProfileAgent,
	"admin": ProfileAdmin,
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
	if len(result) == 0 {
		return nil
	}
	return result
}

const serverInstructions = `Cortex provides persistent memory for AI coding assistants.

MEMORY (Engram-compatible):
  mem_save - save decisions, bugs, discoveries PROACTIVELY
  mem_search - find past work via FTS5
  mem_context - recent session history
  mem_session_summary - MANDATORY before ending session
  mem_get_observation - full content by ID
  mem_save_prompt - save user prompt

CORTEX-EXCLUSIVE:
  mem_relate - create relationship between observations
  mem_graph - traverse knowledge graph from an observation
  mem_score - get/recalculate importance score
  mem_archive - archive an observation
  mem_search_hybrid - hybrid FTS5 + vector search
  mem_revision_history - structured revision snapshots for an observation

DEFERRED: mem_update, mem_suggest_topic_key, mem_session_start, mem_session_end,
  mem_stats, mem_delete, mem_timeline, mem_revision_history, mem_capture_passive`

// NewServer creates an MCP server with ALL tools registered.
func NewServer(stores *Stores) *server.MCPServer {
	return NewServerWithTools(stores, nil)
}

// NewServerWithTools creates an MCP server registering only the tools in
// the allowlist. If allowlist is nil, all tools are registered.
func NewServerWithTools(stores *Stores, allowlist map[string]bool) *server.MCPServer {
	srv := server.NewMCPServer(
		"cortex",
		"0.1.0",
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

// Package mcp implements the Model Context Protocol server for Cortex.
//
// This exposes memory tools via MCP stdio transport so agents can use
// Cortex as a persistent memory server.
//
// Tool profiles allow agents to load only the tools they need:
//
//	cortex mcp                        -> all tools (default)
//	cortex mcp --tools=agent          -> ordinary agent tools (cortex_* namespace)
//	cortex mcp --tools=dev            -> golden dev suite (11 tools: memory + AST impact)
//	cortex mcp --tools=minimal        -> essential core memory (5 tools: minimal token footprint)
//	cortex mcp --tools=admin          -> admin/diagnostic tools
//	cortex mcp --tools=temporal       -> temporal/advanced tools
package mcp

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/store/bundle"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Stores is an alias for bundle.Stores.
type Stores = bundle.Stores

// --- Tool Profiles ---

// ProfileAgent contains the consolidated agent tool set in the cortex_* namespace.
// These are the orthogonal, unified tools an AI agent needs for proactive memory,
// intelligent hybrid search, context packs, knowledge graphs, and codebase AST intelligence.
var ProfileAgent = map[string]bool{
	// Core Memory (5)
	"cortex_save":            true,
	"cortex_update":          true,
	"cortex_get_observation": true,
	"cortex_context":         true,
	"cortex_session_summary": true,

	// Retrieval & Prompt Context (2)
	"cortex_search":            true,
	"cortex_get_agent_context": true,

	// Knowledge Graph (3)
	"cortex_relate":     true,
	"cortex_graph":      true,
	"cortex_graph_path": true,

	// Directives & Governance (2)
	"cortex_get_rules": true,
	"cortex_save_rule": true,

	// Codebase AST & Test Impact (5)
	"cortex_ingest_code":      true,
	"cortex_get_blast_radius": true,
	"cortex_code_tests":       true,
	"cortex_get_code_symbols": true,
	"cortex_detect_cycles":    true,

	// Architecture & Repository Status (3)
	"cortex_analyze_architecture": true,
	"cortex_code_map":             true,
	"cortex_get_status":           true,

	// Durable Lineage & Multi-agent Handoff (2)
	"cortex_revision_history": true,
	"cortex_handoff":          true,
}

// ProfileAdmin contains administrative maintenance tools.
// DEPRECATED FOR AGENTS: Destructive and administrative operations (cortex_delete,
// cortex_merge_projects, cortex_consolidate) belong to operator interfaces (CLI, TUI, Web UI)
// and must NOT be exposed to autonomous agents.
var ProfileAdmin = map[string]bool{
	"cortex_delete":         true,
	"cortex_stats":          true,
	"cortex_timeline":       true,
	"cortex_score":          true,
	"cortex_consolidate":    true,
	"cortex_merge_projects": true,
}

// ProfileTemporal contains bi-temporal graph queries and telemetry tools.
// DEPRECATED FOR AGENTS: Infrastructure telemetry (memory usage, execution duration)
// and manual bitemporal timestamps belong to internal middleware and APM,
// not agentic LLM context. Use topic_key upserts and cortex_revision_history instead.
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

// ProfileMinimal contains the 5 core memory tools for ultra-low token
// footprint and fast model inference (e.g. local models, Haiku, Flash).
var ProfileMinimal = map[string]bool{
	"cortex_save":            true,
	"cortex_search":          true,
	"cortex_context":         true,
	"cortex_session_summary": true,
	"cortex_get_observation": true,
}

// ProfileDev contains the golden 11-tool suite for local software development:
// essential memory plus codebase AST analysis, blast radius, and test impact.
var ProfileDev = map[string]bool{
	"cortex_save":              true,
	"cortex_search":            true,
	"cortex_context":           true,
	"cortex_session_summary":   true,
	"cortex_get_observation":   true,
	"cortex_get_agent_context": true,
	"cortex_relate":            true,
	"cortex_get_rules":         true,
	"cortex_ingest_code":       true,
	"cortex_get_blast_radius":  true,
	"cortex_code_tests":        true,
}

// Profiles maps profile names to their tool sets.
// Canonical agentic profiles are "agent", "dev", "coder", and "minimal".
var Profiles = map[string]map[string]bool{
	"agent":    ProfileAgent,
	"dev":      ProfileDev,
	"coder":    ProfileDev,
	"minimal":  ProfileMinimal,
	"admin":    ProfileAdmin,    // Deprecated non-agentic profile
	"temporal": ProfileTemporal, // Deprecated non-agentic profile
}

// ResolveTools takes a comma-separated string of profile names and/or
// individual tool names and returns the set of tool names to register.
// An empty input or "all" resolves to ProfileAgent (the full canonical agent suite).
func ResolveTools(input string) map[string]bool {
	input = strings.TrimSpace(input)
	if input == "" || input == "all" {
		return ProfileAgent
	}

	result := make(map[string]bool)
	for _, token := range strings.Split(input, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if token == "all" {
			return ProfileAgent
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

const serverInstructions = `Cortex provides persistent memory and codebase intelligence for AI coding assistants.

CORE MEMORY & RETRIEVAL:
  cortex_save - save decisions, bugs, discoveries, patterns (type: prompt, bugfix, decision, etc.)
  cortex_update - surgical updates to existing observations by ID
  cortex_get_observation - full content of an observation by ID
  cortex_search - unified intelligent hybrid search (FTS5 + Vector + Adaptive-RAG + HippoRAG)
  cortex_context - recent session activity and context
  cortex_session_summary - structured end-of-session summary (Goal, Discoveries, Accomplished)
  cortex_get_agent_context - prompt-ready context pack (supports max_tokens for compact mode)

RULES & DIRECTIVES:
  cortex_get_rules - retrieve active project and global rules/directives
  cortex_save_rule - save/update persistent project or global rules

KNOWLEDGE GRAPH:
  cortex_relate - create typed relationship between observations
  cortex_graph - traverse knowledge graph or inspect relationships (format: summary | relationships)
  cortex_graph_path - find shortest path between observations
  cortex_revision_history - structured revision snapshots for an observation

CODEBASE AST & INTELLIGENCE:
  cortex_ingest_code - scan local files with Zero-CGO Static AST Extractor
  cortex_get_code_symbols - query symbols with filters or regex pattern
  cortex_get_blast_radius - calculate downstream impact (supports include_tests: true)
  cortex_code_tests - reverse call-graph to locate impacted tests for Fast-TDD
  cortex_detect_cycles - detect circular dependencies and import cycles
  cortex_analyze_architecture - analyze code communities and god nodes
  cortex_code_map - token-budgeted structural map of key repo symbols
  cortex_get_status - active mode and capabilities

DURABLE HANDOFF:
  cortex_handoff - exactly-once handoff with receipts between agents`

// NewServer creates an MCP server with ProfileAgent tools registered by default.
func NewServer(stores *Stores) *server.MCPServer {
	return NewServerWithTools(stores, ProfileAgent)
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
	registerSandboxTools(srv, stores, allowlist)
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
	raw, ok := req.GetArguments()[key]
	if !ok || raw == nil {
		return defaultVal
	}
	switch v := raw.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return parsed
		}
	}
	return defaultVal
}

// --- Strict Identifier Helpers (T07 / QW-01) ---
//
// Persisted Cortex identifiers are int64 row IDs. They must be published as
// JSON Schema integers and validated strictly BEFORE any store or domain
// call: float truncation must never redirect a destructive operation
// (cortex_delete(id:1.9) must NOT delete observation 1).

// maxInt64Float is 2^63 as float64: the smallest float that exceeds the
// int64 range. An integral float64 identifier must be strictly below it to
// convert safely (the largest safe value, 9223372036854774784, is exactly
// representable and <= math.MaxInt64).
const maxInt64Float = 9223372036854775808.0

// positiveIDArg extracts a strict positive int64 identifier argument. It
// rejects missing values and non-numeric types (strings, booleans, objects,
// arrays), NaN and infinities, fractional numbers, zero, negatives, and
// values outside the int64 range. Handlers MUST call it (and return the
// "<field> must be a positive integer" MCP error on failure) before any
// store or domain access.
func positiveIDArg(req mcp.CallToolRequest, key string) (int64, bool) {
	value, ok := req.GetArguments()[key].(float64)
	if !ok {
		return 0, false
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	if value != math.Trunc(value) {
		return 0, false
	}
	if value < 1 || value >= maxInt64Float {
		return 0, false
	}
	return int64(value), true
}

// integerIDSchema tightens a WithNumber property to a JSON Schema integer
// with minimum 1. mcp-go v0.46 has no WithInteger builder, but property
// options mutate the same map WithNumber seeds, so overriding "type" is the
// supported composition path.
func integerIDSchema(schema map[string]any) {
	schema["type"] = "integer"
	schema["minimum"] = float64(1)
}

// withIntegerID publishes a required identifier property as a strict
// positive integer.
func withIntegerID(name, description string) mcp.ToolOption {
	return mcp.WithNumber(name,
		integerIDSchema,
		mcp.Required(),
		mcp.Description(description),
	)
}

func boolArg(req mcp.CallToolRequest, key string, defaultVal bool) bool {
	raw, ok := req.GetArguments()[key]
	if !ok || raw == nil {
		return defaultVal
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		lower := strings.ToLower(strings.TrimSpace(v))
		switch lower {
		case "true", "1", "yes", "t", "y":
			return true
		case "false", "0", "no", "f", "n":
			return false
		}
	case float64:
		return v != 0
	case int:
		return v != 0
	case int64:
		return v != 0
	}
	return defaultVal
}

// --- Response Helpers ---

func textResult(format string, args ...any) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(fmt.Sprintf(format, args...)), nil
}

func errorResult(format string, args ...any) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultError(fmt.Sprintf(format, args...)), nil
}

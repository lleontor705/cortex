// Package domain defines the core domain models and business rules for Cortex.
//
// This package contains the pure domain types that represent the core concepts
// of the memory system: Observations, Sessions, Knowledge Graph Edges, Prompts,
// and Importance Scoring. These types are independent of storage mechanisms
// and can be used across different layers of the application.
package domain

import "time"

// Observation represents a single piece of knowledge or memory captured
// during an AI coding session. It can be a manual note, tool usage record,
// decision, bugfix, pattern, or any other type of observation.
type Observation struct {
	ID         int64     `json:"id"`
	Title      string    `json:"title"`
	Content    string    `json:"content"`
	Type       string    `json:"type"`    // manual, tool_use, decision, bugfix, etc.
	Project    string    `json:"project"` // Project name or identifier
	Scope      string    `json:"scope"`   // project, personal
	SessionID  string    `json:"session_id"`
	TopicKey   string    `json:"topic_key"`  // Optional topic key for upserts
	Confidence float64   `json:"confidence"` // Confidence score (0.0 to 1.0), default 1.0
	Source     string    `json:"source"`     // Origin: manual, ai, auto, import
	Tags       []string  `json:"tags,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Session represents a coding session that groups related observations.
// Sessions track when work started and ended, along with an optional summary.
type Session struct {
	ID        string     `json:"id"`
	Project   string     `json:"project"`
	Directory string     `json:"directory"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Summary   string     `json:"summary,omitempty"`
}

// Edge represents a relationship between two observations in the knowledge graph.
// Edges enable semantic navigation and discovery of related knowledge with temporal awareness.
type Edge struct {
	ID           int64      `json:"id"`
	FromObsID    int64      `json:"from_obs_id"`
	ToObsID      int64      `json:"to_obs_id"`
	RelationType string     `json:"relation_type"` // references, relates_to, follows
	Weight       float64    `json:"weight"`        // Strength of relationship (0.0 to 10.0, default 1.0)
	Confidence   float64    `json:"confidence"`    // Confidence in this relationship (0.0 to 1.0)
	Source       string     `json:"source,omitempty"`    // Who/what created this edge
	Reasoning    string     `json:"reasoning,omitempty"` // Why this relationship exists
	ValidFrom    *time.Time `json:"valid_from,omitempty"`  // Temporal validity start
	InvalidAt    *time.Time `json:"invalid_at,omitempty"`  // Temporal validity end (NULL = still valid)
	CreatedAt    time.Time  `json:"created_at"`
	
	// Enhanced temporal graph fields
	EvolutionID    *int64     `json:"evolution_id,omitempty"`     // Track edge evolution (NULL = original)
	EvolutionType  string     `json:"evolution_type"`            // evolution types: original, modified, superseded, contradicted
	FactState      string     `json:"fact_state"`                // fact states: current, historical, deprecated, superseded
	ChangeReason   string     `json:"change_reason,omitempty"`   // Why the edge changed
}

// Prompt represents a user prompt captured during a session for replay
// and context understanding.
type Prompt struct {
	ID        int64     `json:"id"`
	Content   string    `json:"content"`
	Project   string    `json:"project"`
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
}

// ImportanceScore tracks the importance of an observation based on
// access patterns, recency, and other metrics.
type ImportanceScore struct {
	ObservationID int64     `json:"observation_id"`
	Score         float64   `json:"score"`         // Importance score (0.0 to 5.0)
	AccessCount   int       `json:"access_count"`  // Number of times accessed
	LastAccessed  time.Time `json:"last_accessed"` // Last access timestamp
	UpdatedAt     time.Time `json:"updated_at"`
}

// ObservationFilter provides filtering options for listing observations.
type ObservationFilter struct {
	Project       string     `json:"project,omitempty"`
	Scope         string     `json:"scope,omitempty"`
	Type          string     `json:"type,omitempty"`
	Source        string     `json:"source,omitempty"`
	Tags          []string   `json:"tags,omitempty"`
	MinConfidence float64    `json:"min_confidence,omitempty"`
	Limit         int        `json:"limit,omitempty"`
	Offset        int        `json:"offset,omitempty"`
	CreatedBefore *time.Time `json:"created_before,omitempty"`
	CreatedAfter  *time.Time `json:"created_after,omitempty"`
	OrderAsc      bool       `json:"order_asc,omitempty"`
}

// SearchOptions provides options for full-text search queries.
type SearchOptions struct {
	Query   string `json:"query"`
	Type    string `json:"type,omitempty"`
	Project string `json:"project,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

// SearchResult represents a search result with relevance ranking.
type SearchResult struct {
	Observation
	Rank float64 `json:"rank"` // Relevance score from FTS
}

// Observation types - common values for the Type field
const (
	TypeManual    = "manual"
	TypeToolUse   = "tool_use"
	TypeDecision  = "decision"
	TypeBugfix    = "bugfix"
	TypePattern   = "pattern"
	TypeConfig    = "config"
	TypeDiscovery = "discovery"
	TypeLearning  = "learning"
)

// Scope types - common values for the Scope field
const (
	ScopeProject  = "project"
	ScopePersonal = "personal"
)

// Source types - common values for the Source field
const (
	SourceManual = "manual"
	SourceAI     = "ai"
	SourceAuto   = "auto"
	SourceImport = "import"
)

// Relation types - common values for Edge.RelationType
const (
	RelationReferences  = "references"
	RelationRelatesTo   = "relates_to"
	RelationFollows     = "follows"
	RelationContradicts = "contradicts"
	RelationSupersedes  = "supersedes"
)

// EntityLink represents an extracted entity from an observation.
type EntityLink struct {
	ID            int64     `json:"id"`
	ObservationID int64     `json:"observation_id"`
	EntityType    string    `json:"entity_type"`  // file, url, package, symbol, concept
	EntityValue   string    `json:"entity_value"`
	CreatedAt     time.Time `json:"created_at"`
}

// Metrics represents observability metrics for memory system performance.
type Metrics struct {
	ID                 int64     `json:"id"`
	SessionID          string    `json:"session_id"`
	OperationType      string    `json:"operation_type"`   // save, search, relate, get_related, etc.
	Duration           int64     `json:"duration_ms"`       // Operation duration in milliseconds
	ResultCount        int       `json:"result_count"`      // Number of results returned
	Success            bool      `json:"success"`           // Whether operation succeeded
	Error              string    `json:"error,omitempty"`   // Error message if failed
	MemoryUsage        int64     `json:"memory_usage_bytes"` // Memory usage in bytes
	Timestamp          time.Time `json:"timestamp"`
	ObservationCount   int       `json:"observation_count"` // Total observations in system
	EdgeCount          int       `json:"edge_count"`       // Total edges in knowledge graph
	QueryComplexity    float64   `json:"query_complexity"`  // Estimated query complexity (0.0-1.0)
	ConfidenceScore    float64   `json:"confidence_score"`  // Average confidence score
}

// QualityMetrics represents memory quality evaluation metrics.
type QualityMetrics struct {
	ID                    int64     `json:"id"`
	SessionID            string    `json:"session_id"`
	EvaluationType        string    `json:"evaluation_type"`   // relevance, completeness, consistency, temporal_accuracy
	Score                 float64   `json:"score"`             // Score 0.0-1.0
	TotalQueries          int       `json:"total_queries"`     // Number of queries evaluated
	SuccessfulRetrievals   int       `json:"successful_retrievals"` // Number of successful retrievals
	AverageLatency       float64   `json:"average_latency_ms"` // Average response time
	AverageRelevance      float64   `json:"average_relevance"` // Average relevance score
	TemporalAccuracy      float64   `json:"temporal_accuracy"` // How well temporal facts are preserved
	KnowledgeCoverage     float64   `json:"knowledge_coverage"` // How much relevant knowledge is covered
	EvaluatedAt           time.Time `json:"evaluated_at"`
}

// TemporalSnapshot represents a point-in-time snapshot of the knowledge graph.
type TemporalSnapshot struct {
	ID          int64     `json:"id"`
	SnapshotKey string    `json:"snapshot_key"` // Unique identifier for this snapshot
	Timestamp   time.Time `json:"timestamp"`
	Description string    `json:"description,omitempty"`
	ObservationCount int   `json:"observation_count"`
	EdgeCount        int   `json:"edge_count"`
	RootObservationID int64 `json:"root_observation_id,omitempty"` // Root observation for this snapshot
}

// Entity types
const (
	EntityFile     = "file"
	EntityURL      = "url"
	EntityPackage  = "package"
	EntitySymbol   = "symbol"
	EntityConcept  = "concept"
	EntitySQLTable = "sql_table"
	EntityEndpoint = "endpoint"
	EntityEnvVar   = "env_var"
	EntityVersion  = "version"
	EntityCLIFlag  = "cli_flag"
	EntityError    = "error"
)

// Evolution types for temporal graph edges
const (
	EvolutionOriginal   = "original"
	EvolutionModified   = "modified"
	EvolutionSuperseded = "superseded"
	EvolutionContradicted = "contradicted"
)

// Fact states for temporal graph edges
const (
	FactStateCurrent     = "current"
	FactStateHistorical  = "historical"
	FactStateDeprecated  = "deprecated"
	FactStateSuperseded  = "superseded"
)

// Relation types with enhanced temporal support
const (
	RelationReferences  = "references"
	RelationRelatesTo   = "relates_to"
	RelationFollows     = "follows"
	RelationContradicts = "contradicts"
	RelationSupersedes  = "supersedes"
	RelationTemporal    = "temporal" // New: tracks how facts evolve over time
)

// Package domain defines the core domain models and business rules for Cortex.
//
// This package contains the pure domain types that represent the core concepts
// of the memory system: Observations, Sessions, Knowledge Graph Edges, Prompts,
// and Importance Scoring. These types are independent of storage mechanisms
// and can be used across different layers of the application.
package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Observation represents a single piece of knowledge or memory captured
// during an AI coding session. It can be a manual note, tool usage record,
// decision, bugfix, pattern, or any other type of observation.
type Observation struct {
	ID int64 `json:"id"`
	// PublicID is the opaque server identifier. ID remains an internal/local
	// compatibility field and must not be used at a server API boundary.
	PublicID   string    `json:"-"`
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

func (o Observation) MarshalJSON() ([]byte, error) {
	type alias Observation
	var id any = o.ID
	if o.PublicID != "" {
		id = o.PublicID
	}
	return json.Marshal(struct {
		ID any `json:"id"`
		alias
	}{id, alias(o)})
}

// ObservationRef is the exclusive local/public identifier union for addressing
// an observation across storage namespaces. Exactly one namespace must be set;
// Validate enforces the XOR invariant.
type ObservationRef struct {
	// LocalID addresses the observation in the local SQLite namespace.
	LocalID *int64 `json:"local_id,omitempty"`
	// PublicID addresses the observation in the shared server namespace.
	PublicID *uuid.UUID `json:"public_id,omitempty"`
}

// NewLocalObservationRef returns a validated local-namespace reference.
// The local identifier must be positive.
func NewLocalObservationRef(id int64) (ObservationRef, error) {
	if id <= 0 {
		return ObservationRef{}, ErrHandoffValidation
	}
	return ObservationRef{LocalID: &id}, nil
}

// NewPublicObservationRef returns a validated public-namespace reference.
// The identifier must not be the nil UUID.
func NewPublicObservationRef(id uuid.UUID) (ObservationRef, error) {
	if id == uuid.Nil {
		return ObservationRef{}, ErrHandoffValidation
	}
	return ObservationRef{PublicID: &id}, nil
}

// WriteStatus classifies the durable effect of an observation write relative
// to previously persisted state. The set is closed: created, replayed, updated.
type WriteStatus string

const (
	// WriteStatusCreated marks the first durable materialization.
	WriteStatusCreated WriteStatus = "created"
	// WriteStatusReplayed marks an idempotent replay of an identical write.
	WriteStatusReplayed WriteStatus = "replayed"
	// WriteStatusUpdated marks an in-place update of an existing observation.
	WriteStatusUpdated WriteStatus = "updated"
)

// ObservationWriteResult is the transport-neutral outcome of executing a
// handoff: which observation namespace holds the payload and what kind of
// write materialized it.
type ObservationWriteResult struct {
	Ref    ObservationRef `json:"observation_ref"`
	Status WriteStatus    `json:"status"`
}

// SaveEffect reports the durable effect of a transactional observation save.
// Observation is nil only when the save did not commit; a non-nil value is the
// committed aggregate, never a speculation read back after the fact.
type SaveEffect struct {
	Observation *Observation `json:"observation"`
	Status      WriteStatus  `json:"status"`
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

// ServerStats contains tenant/workspace-scoped counters for the server dashboard.
type ServerStats struct {
	Observations   int `json:"observations"`
	Sessions       int `json:"sessions"`
	ActiveSessions int `json:"active_sessions"`
	Edges          int `json:"edges"`
	Projects       int `json:"projects"`
}

// AuditEntry is the public, non-hash portion of an authorization audit event.
type AuditEntry struct {
	ID           string    `json:"id"`
	ActorSubject string    `json:"actor_subject"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	Reason       string    `json:"reason"`
	Allowed      bool      `json:"allowed"`
	CreatedAt    time.Time `json:"created_at"`
}

// Edge represents a relationship between two observations in the knowledge graph.
// Edges enable semantic navigation and discovery of related knowledge with temporal awareness.
type Edge struct {
	ID           int64      `json:"id"`
	PublicID     string     `json:"-"`
	FromObsID    int64      `json:"from_obs_id"`
	ToObsID      int64      `json:"to_obs_id"`
	FromPublicID string     `json:"-"`
	ToPublicID   string     `json:"-"`
	RelationType string     `json:"relation_type"`         // references, relates_to, follows
	Weight       float64    `json:"weight"`                // Strength of relationship (0.0 to 10.0, default 1.0)
	Confidence   float64    `json:"confidence"`            // Confidence in this relationship (0.0 to 1.0)
	Source       string     `json:"source,omitempty"`      // Who/what created this edge
	Reasoning    string     `json:"reasoning,omitempty"`   // Why this relationship exists
	ValidFrom    *time.Time `json:"valid_from,omitempty"`  // Temporal validity start
	InvalidAt    *time.Time `json:"invalid_at,omitempty"`  // Temporal validity end (NULL = still valid)
	ValidUntil   *time.Time `json:"valid_until,omitempty"` // Bi-temporal valid-time end
	TxFrom       *time.Time `json:"tx_from,omitempty"`     // System/transaction-time start
	TxUntil      *time.Time `json:"tx_until,omitempty"`    // System/transaction-time end
	TenantID     string     `json:"tenant_id,omitempty"`
	WorkspaceID  string     `json:"workspace_id,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`

	// Enhanced temporal graph fields
	EvolutionID     *int64 `json:"evolution_id,omitempty"`  // Track edge evolution (NULL = original)
	EvolutionType   string `json:"evolution_type"`          // evolution types: original, modified, superseded, contradicted
	FactState       string `json:"fact_state"`              // fact states: current, historical, deprecated, superseded
	ChangeReason    string `json:"change_reason,omitempty"` // Why the edge changed
	AssertionKind   string `json:"assertion_kind,omitempty"`
	AssertionStatus string `json:"assertion_status,omitempty"`
}

// GraphNode and GraphLink form the transport-neutral heterogeneous graph read
// model. IDs are kind-prefixed so independently stored aggregates cannot
// collide when projected into one graph.
type GraphNode struct {
	ID       string         `json:"id"`
	Kind     string         `json:"kind"`
	Subtype  string         `json:"subtype,omitempty"`
	Label    string         `json:"label"`
	Project  string         `json:"project,omitempty"`
	Hop      int            `json:"hop"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type GraphLink struct {
	ID              string         `json:"id"`
	Source          string         `json:"source"`
	Target          string         `json:"target"`
	Type            string         `json:"type"`
	Weight          float64        `json:"weight,omitempty"`
	Confidence      float64        `json:"confidence,omitempty"`
	AssertionKind   string         `json:"assertion_kind,omitempty"`
	AssertionStatus string         `json:"assertion_status,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type GraphSubgraph struct {
	Root      string      `json:"root"`
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphLink `json:"edges"`
	Truncated bool        `json:"truncated"`
}

func (e Edge) MarshalJSON() ([]byte, error) {
	type alias Edge
	if e.PublicID == "" {
		return json.Marshal(alias(e))
	}
	b, err := json.Marshal(alias(e))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	if e.PublicID != "" {
		out["id"] = e.PublicID
	}
	delete(out, "from_obs_id")
	delete(out, "to_obs_id")
	if e.FromPublicID != "" {
		out["from_id"] = e.FromPublicID
	}
	if e.ToPublicID != "" {
		out["to_id"] = e.ToPublicID
	}
	return json.Marshal(out)
}

// Prompt represents a user prompt captured during a session for replay
// and context understanding.
type Prompt struct {
	ID        int64     `json:"id"`
	PublicID  string    `json:"-"`
	Content   string    `json:"content"`
	Project   string    `json:"project"`
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (p Prompt) MarshalJSON() ([]byte, error) {
	type alias Prompt
	var id any = p.ID
	if p.PublicID != "" {
		id = p.PublicID
	}
	return json.Marshal(struct {
		ID any `json:"id"`
		alias
	}{id, alias(p)})
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
	Project         string     `json:"project,omitempty"`
	Scope           string     `json:"scope,omitempty"`
	Type            string     `json:"type,omitempty"`
	Source          string     `json:"source,omitempty"`
	SessionID       string     `json:"session_id,omitempty"`
	Tags            []string   `json:"tags,omitempty"`
	MinConfidence   float64    `json:"min_confidence,omitempty"`
	Limit           int        `json:"limit,omitempty"`
	Offset          int        `json:"offset,omitempty"`
	CreatedBefore   *time.Time `json:"created_before,omitempty"`
	CreatedAfter    *time.Time `json:"created_after,omitempty"`
	OrderAsc        bool       `json:"order_asc,omitempty"`
	IncludeArchived bool       `json:"include_archived,omitempty"`
}

// SearchOptions provides options for full-text search queries.
type SearchOptions struct {
	Query       string     `json:"query"`
	Type        string     `json:"type,omitempty"`
	Project     string     `json:"project,omitempty"`
	Scope       string     `json:"scope,omitempty"`
	Limit       int        `json:"limit,omitempty"`
	FusionK     float64    `json:"fusion_k,omitempty"`     // RRF constant (default 60, lower = favor top ranks)
	GraphExpand bool       `json:"graph_expand,omitempty"` // Boost graph neighbors of top results
	AsOf        *time.Time `json:"as_of,omitempty"`        // Temporal point-in-time filter for graph expansion
	// Cursor is an opaque pagination cursor (REQ-RET-002). When set, the search
	// store resumes AFTER the encoded resume point. The cursor is bound to the
	// active filter context (query+project+scope+type+local identity); a cursor
	// from a different context is rejected and treated as a fresh page 0. This is
	// a storage-layer seam; the MCP/HTTP envelope is unified in W6. Local-mode
	// only: no tenant/principal/grant binding yet (W11/W13).
	Cursor string `json:"cursor,omitempty"`
}

// GraphTraversalOptions bounds graph expansion and carries server isolation
// filters. Empty tenant/project values are valid only for local mode.
type GraphTraversalOptions struct {
	Depth       int
	MaxVisited  int
	MaxResults  int
	TenantID    string
	WorkspaceID string
	Project     string
	AsOf        *time.Time
}

// Truncation reasons reported by bounded local graph traversal (GRAPH-02).
const (
	// TruncationReasonMaxVisited marks eligible nodes omitted because the
	// max_visited budget (root plus unique admitted nodes) was exhausted.
	TruncationReasonMaxVisited = "max_visited"
	// TruncationReasonMaxResults marks eligible rows omitted because the
	// max_results budget (emitted unique non-root observations) was exhausted.
	TruncationReasonMaxResults = "max_results"
)

// GraphTraversalResult is the bounded local traversal envelope. Truncated is
// true ONLY when a one-past-the-limit sentinel probe admitted/emitted eligible
// data that was then dropped; a result exactly equal to a limit is complete.
type GraphTraversalResult struct {
	Observations      []*Observation `json:"observations"`
	Truncated         bool           `json:"truncated"`
	TruncationReasons []string       `json:"truncation_reasons,omitempty"`
}

// SearchResult represents a search result with relevance ranking.
type SearchResult struct {
	Observation
	Rank           float64              `json:"rank"` // Relevance score from FTS
	ScoreBreakdown SearchScoreBreakdown `json:"score_breakdown,omitempty"`
	// SearchID is the request-scoped identifier of the search that produced this
	// result (REQ-RET-001). Feedback references this ID so attribution binds to
	// the originating search, not a shared global. It replaces the removed shared
	// mutable search-query field on the Stores bundle.
	SearchID SearchID `json:"search_id,omitempty"`
	// NextCursor is the opaque cursor for the following page. It is set ONLY on
	// the LAST result of a page when more results may exist; absent otherwise.
	// It is a storage-layer seam (REQ-RET-002): the unified response envelope at
	// the MCP/HTTP layer is introduced in W6. Opaque + context-bound, never a
	// secret.
	NextCursor string `json:"next_cursor,omitempty"`
}

func (s SearchResult) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(s.Observation)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	out["rank"] = s.Rank
	if s.ScoreBreakdown != (SearchScoreBreakdown{}) {
		out["score_breakdown"] = s.ScoreBreakdown
	}
	if s.SearchID != "" {
		out["search_id"] = s.SearchID
	}
	if s.NextCursor != "" {
		out["next_cursor"] = s.NextCursor
	}
	return json.Marshal(out)
}

// SearchScoreBreakdown explains which retrieval path produced a result.
type SearchScoreBreakdown struct {
	Strategy       string  `json:"strategy,omitempty"`         // keyword, topic_key, hybrid
	TopicKeyExact  bool    `json:"topic_key_exact,omitempty"`  // exact topic key hit
	TopicKeyExpand bool    `json:"topic_key_expand,omitempty"` // topic key expansion (LIKE match)
	KeywordBM25    float64 `json:"keyword_bm25,omitempty"`     // raw BM25 score for keyword search
	FusionScore    float64 `json:"fusion_score,omitempty"`     // RRF score for hybrid search
	RecencyBoost   float64 `json:"recency_boost,omitempty"`    // recency decay multiplier (0-1)
	ImportanceRank float64 `json:"importance_rank,omitempty"`  // importance score contribution
}

// Observation types - common values for the Type field
const (
	TypeManual         = "manual"
	TypeToolUse        = "tool_use"
	TypeDecision       = "decision"
	TypeBugfix         = "bugfix"
	TypePattern        = "pattern"
	TypeConfig         = "config"
	TypeDiscovery      = "discovery"
	TypeLearning       = "learning"
	TypeSessionSummary = "session_summary"
	TypePassive        = "passive"
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
	ID                  int64     `json:"id"`
	PublicID            string    `json:"-"`
	ObservationID       int64     `json:"observation_id"`
	ObservationPublicID string    `json:"-"`
	EntityType          string    `json:"entity_type"` // file, url, package, symbol, concept
	EntityValue         string    `json:"entity_value"`
	NormalizedValue     string    `json:"normalized_value,omitempty"`
	Provenance          string    `json:"provenance,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

func (e EntityLink) MarshalJSON() ([]byte, error) {
	type alias EntityLink
	if e.PublicID == "" {
		return json.Marshal(alias(e))
	}
	b, err := json.Marshal(alias(e))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	if e.PublicID != "" {
		out["id"] = e.PublicID
	}
	delete(out, "observation_id")
	if e.ObservationPublicID != "" {
		out["observation_id"] = e.ObservationPublicID
	}
	return json.Marshal(out)
}

// Metrics represents observability metrics for memory system performance.
type Metrics struct {
	ID               int64     `json:"id"`
	SessionID        string    `json:"session_id"`
	OperationType    string    `json:"operation_type"`     // save, search, relate, get_related, etc.
	Duration         int64     `json:"duration_ms"`        // Operation duration in milliseconds
	ResultCount      int       `json:"result_count"`       // Number of results returned
	Success          bool      `json:"success"`            // Whether operation succeeded
	Error            string    `json:"error,omitempty"`    // Error message if failed
	MemoryUsage      int64     `json:"memory_usage_bytes"` // Memory usage in bytes
	Timestamp        time.Time `json:"timestamp"`
	ObservationCount int       `json:"observation_count"` // Total observations in system
	EdgeCount        int       `json:"edge_count"`        // Total edges in knowledge graph
	QueryComplexity  float64   `json:"query_complexity"`  // Estimated query complexity (0.0-1.0)
	ConfidenceScore  float64   `json:"confidence_score"`  // Average confidence score
}

// AggregatedMetrics represents rolled-up performance metrics for a time range.
type AggregatedMetrics struct {
	TimeRange           *TimeRange `json:"time_range,omitempty"`
	TotalOperations     int        `json:"total_operations"`
	SuccessfulOps       int        `json:"successful_ops"`
	FailedOps           int        `json:"failed_ops"`
	AvgDurationMs       float64    `json:"avg_duration_ms"`
	TotalMemoryUsage    int64      `json:"total_memory_usage"`
	AvgObservationCount float64    `json:"avg_observation_count"`
	AvgEdgeCount        float64    `json:"avg_edge_count"`
	AvgQueryComplexity  float64    `json:"avg_query_complexity"`
	AvgConfidenceScore  float64    `json:"avg_confidence_score"`
	EvaluatedAt         time.Time  `json:"evaluated_at"`
}

// QualityMetrics represents memory quality evaluation metrics.
type QualityMetrics struct {
	ID                   int64     `json:"id"`
	SessionID            string    `json:"session_id"`
	EvaluationType       string    `json:"evaluation_type"`       // relevance, completeness, consistency, temporal_accuracy
	Score                float64   `json:"score"`                 // Score 0.0-1.0
	TotalQueries         int       `json:"total_queries"`         // Number of queries evaluated
	SuccessfulRetrievals int       `json:"successful_retrievals"` // Number of successful retrievals
	AverageLatency       float64   `json:"average_latency_ms"`    // Average response time
	AverageRelevance     float64   `json:"average_relevance"`     // Average relevance score
	TemporalAccuracy     float64   `json:"temporal_accuracy"`     // How well temporal facts are preserved
	KnowledgeCoverage    float64   `json:"knowledge_coverage"`    // How much relevant knowledge is covered
	EvaluatedAt          time.Time `json:"evaluated_at"`
}

// TemporalSnapshot represents a point-in-time snapshot of the knowledge graph.
type TemporalSnapshot struct {
	ID                int64     `json:"id"`
	SnapshotKey       string    `json:"snapshot_key"` // Unique identifier for this snapshot
	Timestamp         time.Time `json:"timestamp"`
	Description       string    `json:"description,omitempty"`
	ObservationCount  int       `json:"observation_count"`
	EdgeCount         int       `json:"edge_count"`
	RootObservationID int64     `json:"root_observation_id,omitempty"` // Root observation for this snapshot
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
	EvolutionOriginal     = "original"
	EvolutionModified     = "modified"
	EvolutionSuperseded   = "superseded"
	EvolutionContradicted = "contradicted"
)

// Fact states for temporal graph edges
const (
	FactStateCurrent    = "current"
	FactStateHistorical = "historical"
	FactStateDeprecated = "deprecated"
	FactStateSuperseded = "superseded"
)

// Additional temporal relation types.
const (
	RelationTemporal = "temporal" // Tracks how facts evolve over time
)

package domain

import (
	"context"
	"time"
)

// ObservationRepository defines the interface for observation persistence operations.
// Implementations must handle CRUD operations and support filtering.
type ObservationRepository interface {
	// Save creates a new observation or updates an existing one if it has a topic_key.
	Save(ctx context.Context, obs *Observation) error

	// GetByID retrieves an observation by its ID.
	GetByID(ctx context.Context, id int64) (*Observation, error)

	// GetByTopicKey retrieves an observation by its topic key within a project.
	// Returns ErrNotFound if no matching observation exists.
	GetByTopicKey(ctx context.Context, project, topicKey string) (*Observation, error)

	// Update modifies an existing observation.
	Update(ctx context.Context, obs *Observation) error

	// Delete removes an observation (soft delete by default).
	Delete(ctx context.Context, id int64) error

	// List retrieves observations based on filter criteria.
	List(ctx context.Context, filter ObservationFilter) ([]*Observation, error)

	// CountAll counts all observations in the system.
	CountAll(ctx context.Context) (int, error)

	// CountByRoot counts observations related to a root observation.
	CountByRoot(ctx context.Context, rootObsID int64) (int, error)

	// GetBySource retrieves observations filtered by source type.
	GetBySource(ctx context.Context, source string, limit int) ([]*Observation, error)

	// GetByType retrieves observations filtered by type.
	GetByType(ctx context.Context, obsType string, limit int) ([]*Observation, error)
}

// SessionRepository defines the interface for session lifecycle management.
type SessionRepository interface {
	// Create starts a new coding session.
	Create(ctx context.Context, session *Session) error

	// GetByID retrieves a session by its ID.
	GetByID(ctx context.Context, id string) (*Session, error)

	// End marks a session as completed with an optional summary.
	End(ctx context.Context, id string, summary string) error

	// List retrieves sessions for a project, ordered by most recent first.
	List(ctx context.Context, project string) ([]*Session, error)
}

// SearchRepository defines the interface for full-text search operations.
// Implementations should use FTS5 or similar for efficient text search.
type SearchRepository interface {
	// Search performs a full-text search with optional filters.
	Search(ctx context.Context, query string, opts SearchOptions) ([]*SearchResult, error)
}

// GraphRepository defines the interface for knowledge graph operations.
// This enables semantic relationships between observations.
type GraphRepository interface {
	// CreateEdge creates a relationship between two observations.
	CreateEdge(ctx context.Context, edge *Edge) error

	// GetRelated retrieves observations related to the given observation ID,
	// up to the specified depth (for graph traversal).
	GetRelated(ctx context.Context, obsID int64, depth int) ([]*Observation, error)

	// DeleteEdge removes a relationship between observations.
	DeleteEdge(ctx context.Context, id int64) error

	// GetEdgesForObservation retrieves all edges where the observation is either source or target.
	GetEdgesForObservation(ctx context.Context, obsID int64) ([]*Edge, error)

	// GetEdge retrieves a specific edge by its ID.
	GetEdge(ctx context.Context, id int64) (*Edge, error)

	// GetEvolutionChain retrieves all edges that share the same evolution chain.
	GetEvolutionChain(ctx context.Context, fromObsID, toObsID int64) ([]*Edge, error)

	// CountEdgesByObservation counts edges connected to a specific observation.
	CountEdgesByObservation(ctx context.Context, obsID int64) (int, error)

	// CountAllEdges counts all edges in the system.
	CountAllEdges(ctx context.Context) (int, error)

	// GetContradictions retrieves edges marked as contradictions in a time range.
	GetContradictions(ctx context.Context, from, to time.Time) ([]*Edge, error)

	// UpdateEdge updates an existing edge.
	UpdateEdge(ctx context.Context, edge *Edge) error
}

// PromptRepository defines the interface for user prompt storage.
type PromptRepository interface {
	// Save stores a user prompt for later retrieval.
	Save(ctx context.Context, prompt *Prompt) error

	// List retrieves recent prompts for a project.
	List(ctx context.Context, project string, limit int) ([]*Prompt, error)
}

// EntityRepository defines the interface for entity linking operations.
type EntityRepository interface {
	// SaveLinks stores extracted entity links for an observation.
	SaveLinks(ctx context.Context, links []*EntityLink) error

	// GetByObservation retrieves all entity links for an observation.
	GetByObservation(ctx context.Context, obsID int64) ([]*EntityLink, error)

	// FindByEntity retrieves observations that reference a given entity.
	FindByEntity(ctx context.Context, entityType, entityValue string) ([]*EntityLink, error)

	// DeleteByObservation removes all entity links for an observation.
	DeleteByObservation(ctx context.Context, obsID int64) error
}

// ScoringRepository defines the interface for importance scoring operations.
// This enables adaptive relevance ranking based on usage patterns.
type ScoringRepository interface {
	// GetScore retrieves the importance score for an observation.
	GetScore(ctx context.Context, obsID int64) (*ImportanceScore, error)

	// UpdateScore adjusts the importance score for an observation.
	// The increment can be positive (increase importance) or negative.
	UpdateScore(ctx context.Context, obsID int64, increment float64) error

	// GetTop retrieves the most important observations for a project.
	GetTop(ctx context.Context, project string, limit int) ([]*ImportanceScore, error)
}

// VectorSearchOptions provides options for vector similarity search.
type VectorSearchOptions struct {
	// Embedding is the query embedding vector (64-4096 dimensions depending on model).
	Embedding []float32
	// Limit is the maximum number of results to return.
	Limit int
	// Threshold is the minimum similarity score (0.0 to 1.0).
	// Results with similarity below this threshold are excluded.
	Threshold float64
	// Project filters results to a specific project (optional).
	Project string
	// Scope filters results to a specific scope (optional).
	Scope string
}

// VectorSearchResult represents a vector search result with similarity score.
type VectorSearchResult struct {
	Observation
	Similarity float64 `json:"similarity"` // Cosine similarity score (0.0 to 1.0)
}

// VectorRepository defines the interface for vector similarity search operations.
// This enables semantic search using embeddings with cosine distance.
//
// Note: This is an optional feature that requires the "cortex_vectors" build tag.
// When not enabled, all methods return ErrVectorSearchDisabled.
type VectorRepository interface {
	// StoreEmbedding stores an embedding vector for an observation.
	// The embedding dimensions must be between 64 and 4096.
	// Returns an error if the observation doesn't exist or embedding dimension is wrong.
	StoreEmbedding(ctx context.Context, observationID int64, embedding []float32, model string) error

	// SearchByVector performs a similarity search using the query embedding.
	// Returns results sorted by cosine similarity (descending).
	// Returns ErrVectorSearchDisabled if vector search is not available.
	SearchByVector(ctx context.Context, opts VectorSearchOptions) ([]*VectorSearchResult, error)

	// GetEmbedding retrieves the embedding for an observation.
	// Returns ErrNotFound if no embedding exists for the observation.
	GetEmbedding(ctx context.Context, observationID int64) ([]float32, string, error)

	// DeleteEmbedding removes the embedding for an observation.
	DeleteEmbedding(ctx context.Context, observationID int64) error

	// IsAvailable returns true if vector search is enabled and available.
	IsAvailable() bool
}

// MetricsRepository defines the interface for observability metrics.
type MetricsRepository interface {
	// CreateMetric records a performance metric.
	CreateMetric(ctx context.Context, metric *Metrics) error

	// GetTemporalMetrics retrieves metrics for a session within a time range.
	GetTemporalMetrics(ctx context.Context, sessionID string, from, to time.Time) ([]*Metrics, error)

	// GetByOperationType retrieves metrics filtered by operation type.
	GetByOperationType(ctx context.Context, operationType string, from, to time.Time) ([]*Metrics, error)

	// GetAggregatedMetrics gets aggregated metrics for a time range.
	GetAggregatedMetrics(ctx context.Context, from, to time.Time) (*AggregatedMetrics, error)
}

// QualityMetricsRepository defines the interface for quality evaluation.
type QualityMetricsRepository interface {
	// CreateQualityMetric records a quality evaluation result.
	CreateQualityMetric(ctx context.Context, quality *QualityMetrics) error

	// GetBySession retrieves quality metrics for a session.
	GetBySession(ctx context.Context, sessionID string, limit int) ([]*QualityMetrics, error)

	// GetByType retrieves quality metrics filtered by evaluation type.
	GetByType(ctx context.Context, evaluationType string, from, to time.Time) ([]*QualityMetrics, error)

	// GetLatest gets the most recent quality metrics.
	GetLatest(ctx context.Context, limit int) ([]*QualityMetrics, error)
}

// TemporalSnapshotRepository defines the interface for temporal snapshots.
type TemporalSnapshotRepository interface {
	// CreateSnapshot creates a point-in-time snapshot of the knowledge graph.
	CreateSnapshot(ctx context.Context, snapshot *TemporalSnapshot) error

	// GetByID retrieves a snapshot by its ID.
	GetByID(ctx context.Context, id int64) (*TemporalSnapshot, error)

	// GetBySnapshotKey retrieves snapshots by their key.
	GetBySnapshotKey(ctx context.Context, snapshotKey string) ([]*TemporalSnapshot, error)

	// GetSnapshotsInRange retrieves snapshots within a time range.
	GetSnapshotsInRange(ctx context.Context, from, to time.Time) ([]*TemporalSnapshot, error)

	// GetByRootObservation retrieves snapshots for a root observation.
	GetByRootObservation(ctx context.Context, rootObsID int64) ([]*TemporalSnapshot, error)
}

// SystemMetrics represents aggregated system metrics.
type SystemMetrics struct {
	SessionID         string          `json:"session_id"`
	TimeRange         *TimeRange      `json:"time_range"`
	TotalOperations   int             `json:"total_operations"`
	SuccessfulOps     int             `json:"successful_ops"`
	FailedOps         int             `json:"failed_ops"`
	AvgDurationMs     float64         `json:"avg_duration_ms"`
	TotalMemoryUsage  int64           `json:"total_memory_usage"`
	TotalObservations int             `json:"total_observations"`
	TotalEdges        int             `json:"total_edges"`
	AvgQueryComplexity float64         `json:"avg_query_complexity"`
	AvgConfidence     float64         `json:"avg_confidence"`
	EvaluatedAt       time.Time       `json:"evaluated_at"`
	OperationBreakdown map[string]int  `json:"operation_breakdown"`
	TopSlowOperations []string        `json:"top_slow_operations"`
}

// TimeRange represents a time range with start and end.
type TimeRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// HealthCheck represents system health status.
type HealthCheck struct {
	Status           string    `json:"status"`           // healthy, degraded, critical
	CheckTime        time.Time `json:"check_time"`
	TotalOperations  int       `json:"total_operations"`
	FailedOperations int       `json:"failed_operations"`
	SlowOperations   int       `json:"slow_operations"`
	AvgDurationMs   float64   `json:"avg_duration_ms"`
	Message          string    `json:"message"`
}

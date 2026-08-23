package domain

import (
	"context"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain/projectprotocol"
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
	SessionID          string         `json:"session_id"`
	TimeRange          *TimeRange     `json:"time_range"`
	TotalOperations    int            `json:"total_operations"`
	SuccessfulOps      int            `json:"successful_ops"`
	FailedOps          int            `json:"failed_ops"`
	AvgDurationMs      float64        `json:"avg_duration_ms"`
	TotalMemoryUsage   int64          `json:"total_memory_usage"`
	TotalObservations  int            `json:"total_observations"`
	TotalEdges         int            `json:"total_edges"`
	AvgQueryComplexity float64        `json:"avg_query_complexity"`
	AvgConfidence      float64        `json:"avg_confidence"`
	EvaluatedAt        time.Time      `json:"evaluated_at"`
	OperationBreakdown map[string]int `json:"operation_breakdown"`
	TopSlowOperations  []string       `json:"top_slow_operations"`
}

// TimeRange represents a time range with start and end.
type TimeRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// HealthCheck represents system health status.
type HealthCheck struct {
	Status           string    `json:"status"` // healthy, degraded, critical
	CheckTime        time.Time `json:"check_time"`
	TotalOperations  int       `json:"total_operations"`
	FailedOperations int       `json:"failed_operations"`
	SlowOperations   int       `json:"slow_operations"`
	AvgDurationMs    float64   `json:"avg_duration_ms"`
	Message          string    `json:"message"`
}

// ---------------------------------------------------------------------------
// Platform Mode Seams (W1 — REQ-FOUND-001)
//
// These ports enable a moded binary: local (SQLite, nil TenantContext) and
// server (Postgres, resolved Principal). They compile and unit-test in
// isolation. NO caller adopts them in W1 — adoption is deferred to dependent
// waves (W2+). See design ADR-01/ADR-02/ADR-05.
// ---------------------------------------------------------------------------

// TenantContext carries the resolved tenant/workspace/owner for a request.
// In local mode this is nil; in server mode it is resolved from the
// authenticated Principal (NEVER from client input — ADR-06/ADR-07).
type TenantContext struct {
	TenantID     string
	WorkspaceID  string
	OwnerSubject string
}

// SearchID is a request/session-scoped identifier for retrieval feedback
// attribution (REQ-RET-001). Replaces the removed shared mutable search-query
// field.
type SearchID string

// Tx abstracts a backend transaction. The concrete handle is exposed via
// Handle() so TxParticipant implementations can enlist in it.
type Tx interface {
	Commit() error
	Rollback() error
	// Handle returns the backend-specific transaction handle.
	// For SQLite this is *sql.Tx; for Postgres it is pgx.Tx.
	// The `any` return type is intentional: it keeps domain free of any
	// backend import (database/sql, pgx) so the port compiles in both
	// local-only and server builds without build tags.
	Handle() any
}

// TxParticipant enlists a store or service in a shared transaction.
// Each participant runs its work via WithinTx using the same handle.
type TxParticipant interface {
	// WithinTx enlists this participant in the given transaction handle and
	// runs fn within it. handle is the value returned by Tx.Handle().
	WithinTx(ctx context.Context, handle any, fn func(context.Context) error) error
}

// UnitOfWork coordinates multiple TxParticipants within one logical transaction,
// ensuring atomic cross-store saves (ADR-02, REQ-TX-001).
type UnitOfWork interface {
	// Do runs fn with all participants sharing ONE logical transaction.
	// On any participant failure, all prior work is rolled back (REQ-TX-001).
	// On SQLITE_BUSY, Do retries up to BusyRetryConfig.MaxRetries with capped
	// backoff before returning a stable retryable error (REQ-TX-002).
	Do(ctx context.Context, tctx *TenantContext, participants []TxParticipant, fn func(context.Context) error) error
}

// BusyRetryConfig bounds the SQLITE_BUSY retry behavior of a UnitOfWork
// implementation (REQ-TX-002). A save MUST NOT block unbounded; the retry cap
// and backoff ceiling keep latency within a measurable envelope (the envelope
// itself is registered separately by the latency-budget task).
type BusyRetryConfig struct {
	// MaxRetries is the maximum number of application-level retries on a
	// SQLITE_BUSY error after the driver-level busy_timeout has been exceeded.
	// Default: 3. A value of 0 disables application-level retry (the driver
	// busy_timeout is the only bound).
	MaxRetries int

	// BaseBackoff is the initial backoff duration before the first retry.
	// Default: 5ms. Each subsequent retry multiplies the backoff by 2
	// (exponential) up to MaxBackoff.
	BaseBackoff time.Duration

	// MaxBackoff caps the backoff duration for any single retry.
	// Default: 50ms. This prevents a single retry from sleeping too long.
	MaxBackoff time.Duration

	// JitterFactor is the fraction of randomness added to each backoff
	// (0.0–1.0) to decorrelate concurrent retries. Default: 0.2 (±20%).
	JitterFactor float64
}

// DefaultBusyRetryConfig returns a BusyRetryConfig with the W2 defaults:
// 3 retries, 5ms base backoff, 50ms cap, ±20% jitter. These keep total
// worst-case retry latency well under the 5s driver busy_timeout.
func DefaultBusyRetryConfig() BusyRetryConfig {
	return BusyRetryConfig{
		MaxRetries:   3,
		BaseBackoff:  5 * time.Millisecond,
		MaxBackoff:   50 * time.Millisecond,
		JitterFactor: 0.2,
	}
}

// Storage is the narrow port every backend (SQLite, Postgres) implements.
// It provides backend identification, transaction initiation, and health.
type Storage interface {
	// Backend returns the backend identifier: "sqlite" or "postgres".
	Backend() string
	// BeginTx starts a new transaction.
	BeginTx(ctx context.Context) (Tx, error)
	// Health reports the current backend health status.
	Health(ctx context.Context) Health
}

// Principal is the immutable authenticated identity resolved from a token
// (REQ-ID-001). It is NEVER populated from client input — always from the
// verified credential (ADR-08).
type Principal struct {
	Subject      string
	Type         string // user, service_account, agent
	OrgID        string
	WorkspaceIDs []string
	Roles        []string
	Scopes       []string
	AuthMethod   string // oidc, client_credentials, api_key, static
	GrantDigest  string
	GrantVersion int64
	// ProjectIDs and ClassificationClearance are verified grants. They are
	// intentionally separate from OAuth scopes: a client supplied project
	// selector can never create either grant.
	ProjectIDs              []string
	ClassificationClearance []string
}

// ScopesCopy returns a defensive copy of the granted scopes.
func (p Principal) ScopesCopy() []string { return append([]string(nil), p.Scopes...) }

// WorkspacesCopy returns a defensive copy of the workspace grants.
func (p Principal) WorkspacesCopy() []string { return append([]string(nil), p.WorkspaceIDs...) }

// RolesCopy returns a defensive copy of the role grants.
func (p Principal) RolesCopy() []string    { return append([]string(nil), p.Roles...) }
func (p Principal) ProjectsCopy() []string { return append([]string(nil), p.ProjectIDs...) }
func (p Principal) ClassificationClearanceCopy() []string {
	return append([]string(nil), p.ClassificationClearance...)
}

// ModelInfo describes the embedding model that produced a vector.
// Used for model-version namespacing to prevent dimension-mismatch
// corruption (ADR-05, REQ-VEC-001).
type ModelInfo struct {
	Name       string
	Dimension  int
	Version    string
	Normalized bool
}

// Health is the lightweight health status returned by Storage, VectorIndex,
// and EmbeddingProvider ports.
type Health struct {
	Status  string // healthy, degraded, unhealthy
	Message string
}

// EmbeddingProvider abstracts the embedding model (local Ollama, remote API).
// Declared as a port so the retrieval engine and worker depend on the
// interface, not a concrete provider (ADR-04/ADR-05).
type EmbeddingProvider interface {
	Embed(ctx context.Context, texts []string) ([][]float32, ModelInfo, error)
	ModelInfo() ModelInfo
	Health(ctx context.Context) Health
}

// VectorIndex abstracts vector storage backends (sqlite_blob, qdrant,
// pgvector). Each adapter declares Capabilities for strategy selection
// (ADR-05, REQ-VEC-001).
type VectorIndex interface {
	ID() string
	Upsert(ctx context.Context, points []VectorPoint) error
	Search(ctx context.Context, q VectorQuery) ([]VectorCandidate, error)
	Delete(ctx context.Context, ids []int64) error
	Health(ctx context.Context) Health
	Capabilities(ctx context.Context) (Capabilities, error)
	Close() error
}

// Capabilities declares what a VectorIndex adapter supports, enabling
// capability-driven strategy selection (filter push-down, batch size, etc.).
type Capabilities struct {
	IndexType       string   // sqlite_blob, qdrant, pgvector
	DistanceMetrics []string // cosine, dot, euclidean
	MaxDimensions   int
	Filters         string // PreFilter, PostFilter, none
	Hybrid          string // enabled, disabled
	Namespaces      string // supported, unsupported
	Consistency     string // strong, eventual
	BatchUpsert     bool
	MaxBatchSize    int
}

// VectorPoint is a single vector to upsert into a VectorIndex.
type VectorPoint struct {
	ID        int64
	Vector    []float32
	ModelInfo ModelInfo
	Metadata  map[string]any
}

// VectorQuery is a similarity search request against a VectorIndex.
type VectorQuery struct {
	Vector    []float32
	Limit     int
	Threshold float64
	Filters   map[string]any
	Namespace string
}

// VectorCandidate is a single search result from a VectorIndex.
type VectorCandidate struct {
	ID         int64
	Score      float64
	Provenance string // adapter that produced this candidate
}

// IsVectorIndexHealthy reports whether idx is non-nil and reports a healthy
// status. It is the W8 replacement for the legacy VectorRepository.IsAvailable()
// bool: consumers gate expensive work (embedding generation, reindex loops) on
// this check before calling Upsert/Search. A nil index or a degraded/unhealthy
// adapter returns false (REQ-VEC-001 zero-CGO default: the sqlite_blob stub
// reports unhealthy and operations return ErrVectorSearchDisabled).
func IsVectorIndexHealthy(ctx context.Context, idx VectorIndex) bool {
	if idx == nil {
		return false
	}
	return idx.Health(ctx).Status == StatusHealthy
}

// Health status constants used by Storage, VectorIndex, and EmbeddingProvider
// ports. Keeping them as typed constants (not magic strings) lets adapters and
// consumers compare deterministically.
const (
	StatusHealthy   = "healthy"
	StatusDegraded  = "degraded"
	StatusUnhealthy = "unhealthy"
)

// ---------------------------------------------------------------------------
// Project Context Protocol port (R1-T03 + LIM-T01)
//
// ProjectProtocolStore is the persistence port for skill/rule artifacts,
// their immutable revisions, activation CAS pointers and the deterministic
// effective protocol. Tenant/workspace/project identity always comes from
// the resolved Principal (server) or the local composition (local mode),
// NEVER from client input.
//
// v1 retention contract (REQ-RET-001): revisions, activations and audit
// events are immutable and retained indefinitely. Deletion exists ONLY as
// the SoftDelete state transition — this port deliberately defines NO
// hard-delete, purge, truncate or compaction method, and implementations
// MUST NOT add one behind it.
// ---------------------------------------------------------------------------

// ProjectProtocolStore is the store-side port of the Project Context
// Protocol. All validation limits and canonical forms live in the
// projectprotocol package so local, HTTP and MCP paths cannot diverge.
type ProjectProtocolStore interface {
	// SaveArtifact creates an artifact with its first revision. Input
	// validation (key, limits, canonical metadata, REQUIRED idempotency key)
	// is owned by projectprotocol.ValidateSaveArtifactInput; the store MUST
	// honor artifact-level idempotency (replay/conflict) via the input's
	// idempotency key and RequestDigest.
	SaveArtifact(ctx context.Context, in projectprotocol.SaveArtifactInput) (projectprotocol.Artifact, error)

	// SaveRevision appends an immutable revision under optimistic
	// concurrency (expected_revision or If-Match ETag). RevisionInput
	// carries a REQUIRED typed IdempotencyKey (REQ-ART-002): same
	// key+digest replays the original result, key reuse with a different
	// payload returns idempotency_conflict. A stale precondition returns
	// revision_conflict with zero effects.
	SaveRevision(ctx context.Context, artifactID string, in projectprotocol.RevisionInput, pre projectprotocol.Preconditions) (projectprotocol.Revision, error)

	// GetArtifact returns the artifact record, including soft-deleted ones
	// (authorized history remains readable; REQ-RET-002).
	GetArtifact(ctx context.Context, artifactID string) (projectprotocol.Artifact, error)

	// ListArtifacts returns a bounded, cursor-paginated artifact page
	// (REQ-PAGE-001): opaque snapshot-bound cursors, limit normalized by
	// PageRequest.Normalize (default 20, max 100).
	ListArtifacts(ctx context.Context, filter projectprotocol.ArtifactFilter, page projectprotocol.PageRequest) (projectprotocol.ArtifactPage, error)

	// ListRevisions returns a bounded, cursor-paginated revision history.
	ListRevisions(ctx context.Context, artifactID string, page projectprotocol.PageRequest) (projectprotocol.RevisionPage, error)

	// ListEvents returns a bounded, cursor-paginated audit-event history for
	// one artifact (activations, rollbacks, revision appends, soft delete).
	// Events are immutable and retained indefinitely, including for
	// soft-deleted artifacts (REQ-RET-001/002).
	ListEvents(ctx context.Context, artifactID string, page projectprotocol.PageRequest) (projectprotocol.ArtifactEventPage, error)

	// Activate points the artifact at one revision under activation CAS
	// (expected_activation_revision); stale tokens fail with
	// activation_conflict and leave exactly one active revision.
	Activate(ctx context.Context, in projectprotocol.ActivateInput) (projectprotocol.Activation, error)

	// Rollback repoints the activation at an earlier revision under
	// activation CAS, appending a new audited activation event.
	Rollback(ctx context.Context, in projectprotocol.RollbackInput) (projectprotocol.Activation, error)

	// SoftDelete marks the artifact deleted (actor, reason, time) and
	// excludes it from default lists and the effective protocol. The input
	// carries a REQUIRED If-Match ETag as the ONLY precondition form
	// (REQ-API-003: delete requires If-Match; there is no expected_revision
	// path for deletion): a stale ETag returns revision_conflict with zero
	// effects. DeletedBy and Reason are mandatory. The returned Artifact
	// carries the full delete provenance (deleted_at/deleted_by/
	// delete_reason) and a freshly derived canonical ETag. Revisions and
	// events are retained. This is the ONLY deletion transition in v1.
	SoftDelete(ctx context.Context, in projectprotocol.SoftDeleteInput) (projectprotocol.Artifact, error)

	// EffectiveProtocol resolves the deterministic effective protocol for a
	// project (project-over-workspace precedence), rejecting beyond
	// projectprotocol.MaxEffectiveArtifacts and
	// projectprotocol.MaxProtocolBundleBytes without partial results.
	EffectiveProtocol(ctx context.Context, project string) (projectprotocol.Protocol, error)
}

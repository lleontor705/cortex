package domain

import "context"

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
	// Embedding is the query embedding vector (384 dimensions).
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
	// The embedding must have exactly 384 dimensions.
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

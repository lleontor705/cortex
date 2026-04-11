// Package bundle provides the Stores struct that bundles all store dependencies.
// This avoids circular imports between app and mcp packages.
package bundle

import (
	"github.com/lleontor705/cortex/internal/embedding"
	entitystore "github.com/lleontor705/cortex/internal/store/entity"
	graphstore "github.com/lleontor705/cortex/internal/store/graph"
	"github.com/lleontor705/cortex/internal/store/prompt"
	scoringstore "github.com/lleontor705/cortex/internal/store/scoring"
	"github.com/lleontor705/cortex/internal/store/search"
	"github.com/lleontor705/cortex/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
)

// Stores bundles all store dependencies needed by MCP, HTTP, and CLI.
type Stores struct {
	Observations      *sqlitestore.Store
	Sessions          *session.Store
	Search            *search.Store
	Prompts           *prompt.Store
	Graph             *graphstore.Store
	Scoring           *scoringstore.Store
	Vectors           *sqlitestore.VectorStore
	TemporalSnapshots *sqlitestore.TemporalSnapshotRepository
	Entities          *entitystore.Store
	Metrics           *sqlitestore.MetricsRepository
	QualityMetrics    *sqlitestore.QualityMetricsRepository

	// Embeddings is the optional embedding service for vector search.
	Embeddings embedding.Service

	// LastSearchQuery tracks the most recent search query for implicit feedback.
	// When mem_get_observation is called after mem_search, we log the
	// query-to-observation mapping for future Learning-to-Rank training.
	LastSearchQuery string
}

package common

import (
	"context"
	"fmt"
	"strings"

	"github.com/lleontor705/cortex/internal/app"
	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/embedding"
)

// BenchStores wraps a Cortex app instance for benchmark evaluation.
type BenchStores struct {
	App       *app.App
	Embedder  embedding.Service
}

// NewBenchStores creates an in-memory Cortex instance for benchmarking.
// If useEmbeddings is true, tries to connect to Ollama for vector search.
func NewBenchStores() (*BenchStores, error) {
	a, err := app.Open(context.Background(), app.Options{InMemory: true})
	if err != nil {
		return nil, fmt.Errorf("bench: open app: %w", err)
	}
	return &BenchStores{App: a}, nil
}

// NewBenchStoresWithEmbeddings creates a bench store with embedding support.
func NewBenchStoresWithEmbeddings(cfg embedding.Config) (*BenchStores, error) {
	bs, err := NewBenchStores()
	if err != nil {
		return nil, err
	}

	svc := embedding.New(cfg)
	if svc == nil {
		return nil, fmt.Errorf("bench: failed to create embedding service with provider %q", cfg.Provider)
	}
	bs.Embedder = svc
	bs.App.Stores.Embeddings = svc

	return bs, nil
}

// Close cleans up the benchmark database.
func (bs *BenchStores) Close() error {
	return bs.App.Close()
}

// IngestSession creates a session and its observations.
// If embeddings are enabled, each observation is also embedded.
func (bs *BenchStores) IngestSession(ctx context.Context, sessionID, project string, observations []domain.Observation) error {
	sess := &domain.Session{
		ID:        sessionID,
		Project:   project,
		Directory: "/bench",
	}
	if err := bs.App.Stores.Sessions.Create(ctx, sess); err != nil {
		if !strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("bench: create session %s: %w", sessionID, err)
		}
	}

	for i := range observations {
		observations[i].SessionID = sessionID
		observations[i].Project = project
		if observations[i].Scope == "" {
			observations[i].Scope = "project"
		}
		if observations[i].Type == "" {
			observations[i].Type = "manual"
		}
		if err := bs.App.Stores.Observations.Save(ctx, &observations[i]); err != nil {
			return fmt.Errorf("bench: save observation %d: %w", i, err)
		}

		// Auto-embed if embeddings are enabled
		if bs.Embedder != nil && bs.App.Stores.Vectors != nil && bs.App.Stores.Vectors.IsAvailable() {
			text := observations[i].Title + "\n" + observations[i].Content
			vec, embErr := bs.Embedder.Embed(ctx, text)
			if embErr == nil && len(vec) > 0 {
				_ = bs.App.Stores.Vectors.StoreEmbedding(ctx, observations[i].ID, vec, bs.Embedder.Model())
			}
		}
	}

	return nil
}

// EmbedQuery generates an embedding for a search query.
// Returns nil if embeddings are not enabled.
func (bs *BenchStores) EmbedQuery(ctx context.Context, query string) []float32 {
	if bs.Embedder == nil {
		return nil
	}
	vec, err := bs.Embedder.Embed(ctx, query)
	if err != nil {
		return nil
	}
	return vec
}

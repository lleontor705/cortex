package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

// ragUnhealthyVectorIndex reports an unhealthy adapter: the vector provider
// is configured, but the index is unreachable right now.
type ragUnhealthyVectorIndex struct{}

func (*ragUnhealthyVectorIndex) ID() string { return "pgvector" }
func (*ragUnhealthyVectorIndex) Upsert(context.Context, []domain.VectorPoint) error {
	return errors.New("unreachable")
}
func (*ragUnhealthyVectorIndex) Search(context.Context, domain.VectorQuery) ([]domain.VectorCandidate, error) {
	return nil, errors.New("unreachable")
}
func (*ragUnhealthyVectorIndex) Delete(context.Context, []int64) error { return errors.New("unreachable") }
func (*ragUnhealthyVectorIndex) Health(context.Context) domain.Health {
	return domain.Health{Status: domain.StatusUnhealthy, Message: "connection unreachable"}
}
func (*ragUnhealthyVectorIndex) Capabilities(context.Context) (domain.Capabilities, error) {
	return domain.Capabilities{IndexType: "pgvector"}, nil
}
func (*ragUnhealthyVectorIndex) Close() error { return nil }

func ragStatsTestHandler(t *testing.T, cfg config.Config, vectors domain.VectorIndex) http.Handler {
	t.Helper()
	ops := newFakeOperations()
	auth := requestAuthenticator{
		verifier: verifierFunc(func(_ context.Context, secret, _ string) (domain.Principal, error) {
			if secret != cfg.HTTP.Token {
				return domain.Principal{}, errors.New("unknown credential")
			}
			return domain.Principal{Subject: "user-1", OrgID: "org-1"}, nil
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) { return ops, nil }),
	}
	handler, _ := newHTTPHandlerWithHybridSearch(cfg, requestOperations{}, func(context.Context) error { return nil }, auth.middleware, hybridSearchDependencies{
		vectors: vectors,
	})
	return handler
}

func fetchRAGStats(t *testing.T, handler http.Handler) domain.RAGStats {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/rag/stats?project=demo", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rag stats status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, `"vector_provider":"pgvector/hnsw"`) {
		t.Fatalf("rag stats body leaks hardcoded pgvector/hnsw provider: %s", body)
	}
	stats := decodeRAGStatsOrFatal(t, body)
	if stats.EmbeddingModel == "text-embedding-3-small" {
		t.Fatalf("rag stats leaks invented default embedding model: %s", body)
	}
	return stats
}

func decodeRAGStatsOrFatal(t *testing.T, body string) domain.RAGStats {
	t.Helper()
	var stats domain.RAGStats
	if err := json.Unmarshal([]byte(body), &stats); err != nil {
		t.Fatalf("decode rag stats: %v body=%s", err, body)
	}
	return stats
}

// TestRAGStatsHonestWhenNoVectorPipeline verifies that an uncomposed vector
// pipeline is reported as provider "none" with vector_indexed=false — even
// when the underlying operations layer still returns the legacy fake values.
func TestRAGStatsHonestWhenNoVectorPipeline(t *testing.T) {
	cfg := config.Config{
		HTTP:   config.HTTPConfig{Token: "test-token"},
		Search: config.SearchConfig{DefaultLimit: 10, MaxLimit: 20},
	}
	stats := fetchRAGStats(t, ragStatsTestHandler(t, cfg, nil))
	if stats.VectorProvider != "none" {
		t.Fatalf("vector_provider = %q, want none", stats.VectorProvider)
	}
	if stats.VectorIndexed {
		t.Fatalf("vector_indexed = true, want false when no vector index is composed")
	}
	if stats.VectorIndexType != "" {
		t.Fatalf("vector_index_type = %q, want empty", stats.VectorIndexType)
	}
	if stats.EmbeddingDim != 0 {
		t.Fatalf("embedding_dimensions = %d, want 0 when unconfigured", stats.EmbeddingDim)
	}
}

// TestRAGStatsHonestPgvectorExactScan verifies the production shape: pgvector
// with 4096-dim vectors stores exact-scan (pgvector ANN DDL limit is 2000
// dimensions), so the report must claim pgvector + 4096 + indexed but NOT an
// hnsw index.
func TestRAGStatsHonestPgvectorExactScan(t *testing.T) {
	cfg := config.Config{
		HTTP:   config.HTTPConfig{Token: "test-token"},
		Search: config.SearchConfig{DefaultLimit: 10, MaxLimit: 20, EmbeddingProvider: "openai-compatible", EmbeddingModel: "qwen3-embedding"},
		Vector: config.VectorConfig{Provider: "pgvector", Pgvector: config.PGVectorConfig{Dimension: 4096, IndexType: "hnsw"}},
	}
	stats := fetchRAGStats(t, ragStatsTestHandler(t, cfg, &recordingVectorIndex{}))
	if stats.VectorProvider != "pgvector" {
		t.Fatalf("vector_provider = %q, want pgvector", stats.VectorProvider)
	}
	if stats.EmbeddingDim != 4096 {
		t.Fatalf("embedding_dimensions = %d, want 4096 from vector config", stats.EmbeddingDim)
	}
	if !stats.VectorIndexed {
		t.Fatalf("vector_indexed = false, want true for a healthy pgvector adapter")
	}
	if stats.VectorIndexType != "" {
		t.Fatalf("vector_index_type = %q, want empty for 4096-dim exact-scan storage", stats.VectorIndexType)
	}
	if stats.EmbeddingModel != "qwen3-embedding" {
		t.Fatalf("embedding_model = %q, want qwen3-embedding from config", stats.EmbeddingModel)
	}
}

// TestRAGStatsPgvectorHnswWhenIndexExists verifies that an ANN index type is
// only claimed when the dimension actually allows one (<= 2000).
func TestRAGStatsPgvectorHnswWhenIndexExists(t *testing.T) {
	cfg := config.Config{
		HTTP:   config.HTTPConfig{Token: "test-token"},
		Search: config.SearchConfig{DefaultLimit: 10, MaxLimit: 20, EmbeddingProvider: "openai", EmbeddingModel: "text-embedding-3-small-x"},
		Vector: config.VectorConfig{Provider: "pgvector", Pgvector: config.PGVectorConfig{Dimension: 1536, IndexType: "hnsw"}},
	}
	stats := fetchRAGStats(t, ragStatsTestHandler(t, cfg, &recordingVectorIndex{}))
	if stats.VectorIndexType != "hnsw" {
		t.Fatalf("vector_index_type = %q, want hnsw for 1536-dim pgvector", stats.VectorIndexType)
	}
}

// TestRAGStatsConfiguredButUnreachable verifies that a configured pgvector
// adapter that fails its health check is reported as NOT indexed. The
// configured ANN index type is still reported: the bootstrap DDL ran at
// composition (the server refuses to start otherwise), so the type is known
// even while the adapter is unreachable.
func TestRAGStatsConfiguredButUnreachable(t *testing.T) {
	cfg := config.Config{
		HTTP:   config.HTTPConfig{Token: "test-token"},
		Search: config.SearchConfig{DefaultLimit: 10, MaxLimit: 20},
		Vector: config.VectorConfig{Provider: "pgvector", Pgvector: config.PGVectorConfig{Dimension: 1536, IndexType: "hnsw"}},
	}
	stats := fetchRAGStats(t, ragStatsTestHandler(t, cfg, &ragUnhealthyVectorIndex{}))
	if stats.VectorProvider != "pgvector" {
		t.Fatalf("vector_provider = %q, want pgvector (it is configured)", stats.VectorProvider)
	}
	if stats.VectorIndexed {
		t.Fatalf("vector_indexed = true, want false for an unhealthy adapter")
	}
	if stats.VectorIndexType != "hnsw" {
		t.Fatalf("vector_index_type = %q, want hnsw (configured and bootstrapped at composition)", stats.VectorIndexType)
	}
}

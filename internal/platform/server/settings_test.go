package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

func settingsTestConfig() config.Config {
	return config.Config{
		HTTP: config.HTTPConfig{Token: "test-token", Port: 7438},
		AI: config.AIConfig{
			Provider: "openai",
			Model:    "glm5.3-flash",
			BaseURL:  "https://api.nan.builders/v1",
		},
		Search: config.SearchConfig{
			EmbeddingProvider: "openai-compatible",
			EmbeddingModel:    "qwen3-embedding",
			EmbeddingBaseURL:  "https://api.nan.builders/v1",
			RerankProvider:    "openai-compatible",
			RerankModel:       "qwen3-reranker",
			RerankBaseURL:     "https://rerank.internal/v1",
		},
		Vector: config.VectorConfig{Pgvector: config.PGVectorConfig{Dimension: 4096}},
		Server: config.ServerConfig{Storage: config.ServerStorageConfig{Driver: "postgres"}},
	}
}

func TestSettingsEndpointReportsResolvedRuntimeConfiguration(t *testing.T) {
	ops := newFakeOperations()
	cfg := settingsTestConfig()
	h, _ := newVerifiedHTTPHandler(cfg, ops, func(context.Context) error { return nil })
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		LLM struct {
			Provider       string `json:"provider"`
			Model          string `json:"model"`
			BaseURL        string `json:"base_url"`
			Configured     bool   `json:"configured"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		} `json:"llm"`
		Embedding struct {
			Provider   string `json:"provider"`
			Model      string `json:"model"`
			BaseURL    string `json:"base_url"`
			Configured bool   `json:"configured"`
			Dimensions int    `json:"dimensions"`
		} `json:"embedding"`
		Rerank struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
			BaseURL  string `json:"base_url"`
		} `json:"rerank"`
		Storage struct {
			Driver         string `json:"driver"`
			VectorProvider string `json:"vector_provider"`
		} `json:"storage"`
		HTTP struct {
			Port int `json:"port"`
		} `json:"http"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if payload.LLM.Provider != "openai" || payload.LLM.Model != "glm5.3-flash" || !payload.LLM.Configured {
		t.Fatalf("llm = %+v", payload.LLM)
	}
	if payload.LLM.BaseURL != "https://api.nan.builders/v1" {
		t.Fatalf("llm base_url = %q, want resolved provider destination", payload.LLM.BaseURL)
	}
	if payload.Embedding.Provider != "openai-compatible" || payload.Embedding.Model != "qwen3-embedding" || !payload.Embedding.Configured {
		t.Fatalf("embedding = %+v", payload.Embedding)
	}
	if payload.Embedding.BaseURL != "https://api.nan.builders/v1" {
		t.Fatalf("embedding base_url = %q", payload.Embedding.BaseURL)
	}
	if payload.Embedding.Dimensions != 4096 {
		t.Fatalf("embedding dimensions = %d, want 4096", payload.Embedding.Dimensions)
	}
	if payload.Rerank.Provider != "openai-compatible" || payload.Rerank.Model != "qwen3-reranker" || payload.Rerank.BaseURL != "https://rerank.internal/v1" {
		t.Fatalf("rerank = %+v", payload.Rerank)
	}
	if payload.Storage.Driver != "postgres" {
		t.Fatalf("storage driver = %q, want postgres", payload.Storage.Driver)
	}
	if payload.Storage.VectorProvider != "pgvector" {
		t.Fatalf("vector provider = %q, want pgvector (embedding-derived default)", payload.Storage.VectorProvider)
	}
	if payload.HTTP.Port != 7438 {
		t.Fatalf("http port = %d, want 7438", payload.HTTP.Port)
	}
}

// TestSettingsEndpointReportsConfiguredEmbeddingDimension verifies the
// settings surface reports the operator's native MRL embedding dimension
// (search.embedding_dimensions / CORTEX_EMBEDDING_DIMENSIONS) even before
// the embedder has served traffic — the dimension is authoritative at
// composition time, which is what unlocks the pgvector HNSW chain at boot.
func TestSettingsEndpointReportsConfiguredEmbeddingDimension(t *testing.T) {
	ops := newFakeOperations()
	cfg := settingsTestConfig()
	cfg.Search.EmbeddingDimensions = 1024
	cfg.Vector.Pgvector.Dimension = 4096 // vector config must NOT outrank the operator's embedding dimension
	h, _ := newVerifiedHTTPHandler(cfg, ops, func(context.Context) error { return nil })
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Embedding struct {
			Dimensions int `json:"dimensions"`
		} `json:"embedding"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Embedding.Dimensions != 1024 {
		t.Fatalf("embedding dimensions = %d, want configured 1024", payload.Embedding.Dimensions)
	}
}

func TestSettingsEndpointOmitsCredentials(t *testing.T) {
	ops := newFakeOperations()
	cfg := settingsTestConfig()
	// Only the migration DSN carries a secret here: setting Storage.DSN
	// would route the composition into the durable-verifier path, which fails
	// closed without a live database and never reaches the handler.
	cfg.Server.Storage.MigrationDSN = "postgres://cortex_migration:othersecret@db.internal:5432/postgres"
	h, _ := newVerifiedHTTPHandler(cfg, ops, func(context.Context) error { return nil })
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"othersecret", "postgres://", "dsn", "api_key", "sk-"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("settings response leaked credential marker %q: %s", forbidden, body)
		}
	}
}

func TestSettingsEndpointFailsClosedBeforeConfigurationDisclosure(t *testing.T) {
	ops := newFakeOperations()
	ops.authorizeAdminErr = errors.New(authz.DenyRole)
	cfg := settingsTestConfig()
	auth := requestAuthenticator{
		verifier: verifierFunc(func(context.Context, string, string) (domain.Principal, error) {
			return domain.Principal{Subject: "member", OrgID: "tenant"}, nil
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) { return ops, nil }),
	}
	h, _ := newHTTPHandlerWithHybridSearch(cfg, requestOperations{}, func(context.Context) error { return nil }, auth.middleware, hybridSearchDependencies{})
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("status=%d body=%s, want sanitized forbidden", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "api.nan.builders") || strings.Contains(rec.Body.String(), "qwen3") {
		t.Fatalf("denied response leaked configuration: %s", rec.Body.String())
	}
}

package setup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
)

func TestSetupOllama_WithMockServer(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cortex-setup-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{
					{"name": "nomic-embed-text:latest"},
					{"name": "llama3:latest"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	res, err := SetupOllama(mockServer.URL, "nomic-embed-text")
	if err != nil {
		t.Fatalf("SetupOllama failed: %v", err)
	}

	if !res.OllamaOnline {
		t.Errorf("expected OllamaOnline=true")
	}
	if !res.ModelAvailable {
		t.Errorf("expected ModelAvailable=true")
	}
	if len(res.AvailableModels) != 2 {
		t.Errorf("expected 2 available models, got %d", len(res.AvailableModels))
	}

	// Verify file was written
	cfgPath := filepath.Join(tmpDir, "cortex.yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		cfgPath = res.ConfigPath
		if _, err2 := os.Stat(cfgPath); err2 != nil {
			t.Fatalf("config file was not created: %v", err2)
		}
	}

	savedCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load saved config: %v", err)
	}
	if savedCfg.Search.EmbeddingProvider != "ollama" {
		t.Errorf("expected Search.EmbeddingProvider=ollama, got %s", savedCfg.Search.EmbeddingProvider)
	}
	if savedCfg.Search.EmbeddingModel != "nomic-embed-text" {
		t.Errorf("expected Search.EmbeddingModel=nomic-embed-text, got %s", savedCfg.Search.EmbeddingModel)
	}
	if savedCfg.Search.EmbeddingBaseURL != mockServer.URL {
		t.Errorf("expected Search.EmbeddingBaseURL=%s, got %s", mockServer.URL, savedCfg.Search.EmbeddingBaseURL)
	}
	if savedCfg.AI.Model != "" {
		t.Errorf("expected AI.Model to remain empty (not set to embedding model), got %s", savedCfg.AI.Model)
	}
	if savedCfg.AI.Provider != "ollama" {
		t.Errorf("expected AI.Provider=ollama, got %s", savedCfg.AI.Provider)
	}
	if savedCfg.AI.BaseURL != mockServer.URL {
		t.Errorf("expected AI.BaseURL=%s, got %s", mockServer.URL, savedCfg.AI.BaseURL)
	}
}

func TestSetupOllama_PreservesExistingAIModelAndProvider(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cortex-setup-preserve-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	// Pre-populate cortex.yaml with existing AI configuration
	existingCfg := config.DefaultConfig()
	existingCfg.AI.Provider = "openai"
	existingCfg.AI.Model = "gpt-4o"
	existingCfg.AI.BaseURL = "https://api.openai.com/v1"
	if err := os.MkdirAll(config.CortexDir(), 0o755); err != nil {
		t.Fatalf("mkdir cortex dir: %v", err)
	}
	cfgPath := filepath.Join(config.CortexDir(), "cortex.yaml")
	if err := config.Save(existingCfg, cfgPath); err != nil {
		t.Fatalf("save initial config: %v", err)
	}

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{
					{"name": "nomic-embed-text:latest"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	res, err := SetupOllama(mockServer.URL, "nomic-embed-text")
	if err != nil {
		t.Fatalf("SetupOllama failed: %v", err)
	}
	if !res.ModelAvailable {
		t.Errorf("expected ModelAvailable=true")
	}

	savedCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load updated config: %v", err)
	}

	// Embeddings must be updated
	if savedCfg.Search.EmbeddingProvider != "ollama" {
		t.Errorf("expected Search.EmbeddingProvider=ollama, got %s", savedCfg.Search.EmbeddingProvider)
	}
	if savedCfg.Search.EmbeddingModel != "nomic-embed-text" {
		t.Errorf("expected Search.EmbeddingModel=nomic-embed-text, got %s", savedCfg.Search.EmbeddingModel)
	}
	if savedCfg.Search.EmbeddingBaseURL != mockServer.URL {
		t.Errorf("expected Search.EmbeddingBaseURL=%s, got %s", mockServer.URL, savedCfg.Search.EmbeddingBaseURL)
	}

	// AI config must be preserved (not overwritten with ollama/nomic-embed-text)
	if savedCfg.AI.Provider != "openai" {
		t.Errorf("expected AI.Provider to be preserved as 'openai', got %s", savedCfg.AI.Provider)
	}
	if savedCfg.AI.Model != "gpt-4o" {
		t.Errorf("expected AI.Model to be preserved as 'gpt-4o', got %s", savedCfg.AI.Model)
	}
	if savedCfg.AI.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("expected AI.BaseURL to be preserved as 'https://api.openai.com/v1', got %s", savedCfg.AI.BaseURL)
	}
}

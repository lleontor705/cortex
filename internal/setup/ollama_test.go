package setup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSetupOllama_WithMockServer(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cortex-setup-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	t.Setenv("CORTEX_HOME", tmpDir)

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
	if _, err := os.Stat(filepath.Join(tmpDir, "cortex.yaml")); err != nil {
		// Could be in res.ConfigPath
		if _, err2 := os.Stat(res.ConfigPath); err2 != nil {
			t.Errorf("config file was not created: %v", err2)
		}
	}
}

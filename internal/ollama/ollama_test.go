package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsRunning_WhenOllamaResponds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(tagsResponse{})
		}
	}))
	defer srv.Close()

	mgr := NewManager(srv.URL)
	ctx := context.Background()

	if !mgr.IsRunning(ctx) {
		t.Fatal("expected IsRunning to return true")
	}
}

func TestIsRunning_WhenOllamaDown(t *testing.T) {
	mgr := NewManager("http://localhost:19999")
	ctx := context.Background()

	if mgr.IsRunning(ctx) {
		t.Fatal("expected IsRunning to return false for unreachable server")
	}
}

func TestHasModel_Found(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(tagsResponse{
			Models: []struct {
				Name string `json:"name"`
			}{
				{Name: "nomic-embed-text:latest"},
				{Name: "qwen3-embedding:8b"},
			},
		})
	}))
	defer srv.Close()

	mgr := NewManager(srv.URL)
	ctx := context.Background()

	tests := []struct {
		model string
		found bool
	}{
		{"qwen3-embedding:8b", true},
		{"nomic-embed-text", true},  // matches nomic-embed-text:latest
		{"nomic-embed-text:latest", true},
		{"nonexistent-model", false},
	}

	for _, tt := range tests {
		has, err := mgr.HasModel(ctx, tt.model)
		if err != nil {
			t.Fatalf("HasModel(%q) error: %v", tt.model, err)
		}
		if has != tt.found {
			t.Errorf("HasModel(%q) = %v, want %v", tt.model, has, tt.found)
		}
	}
}

func TestHasModel_OllamaDown(t *testing.T) {
	mgr := NewManager("http://localhost:19999")
	ctx := context.Background()

	_, err := mgr.HasModel(ctx, "test-model")
	if err == nil {
		t.Fatal("expected error when Ollama is not reachable")
	}
}

func TestWaitReady_AlreadyRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(tagsResponse{})
	}))
	defer srv.Close()

	mgr := NewManager(srv.URL)
	ctx := context.Background()

	err := mgr.WaitReady(ctx, 2*time.Second)
	if err != nil {
		t.Fatalf("WaitReady should succeed immediately: %v", err)
	}
}

func TestWaitReady_Timeout(t *testing.T) {
	mgr := NewManager("http://localhost:19999")
	ctx := context.Background()

	err := mgr.WaitReady(ctx, 1*time.Second)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestNewManager_DefaultURL(t *testing.T) {
	mgr := NewManager("")
	if mgr.BaseURL() != "http://localhost:11434" {
		t.Errorf("expected default URL, got %s", mgr.BaseURL())
	}
}

func TestNewManager_CustomURL(t *testing.T) {
	mgr := NewManager("http://gpu-server:11434")
	if mgr.BaseURL() != "http://gpu-server:11434" {
		t.Errorf("expected custom URL, got %s", mgr.BaseURL())
	}
}

func TestStartedByUs_DefaultFalse(t *testing.T) {
	mgr := NewManager("")
	if mgr.StartedByUs() {
		t.Error("expected StartedByUs to be false by default")
	}
}

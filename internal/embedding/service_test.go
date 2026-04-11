package embedding

import (
	"context"
	"testing"
)

func TestNewReturnsNilForNone(t *testing.T) {
	svc := New(Config{Provider: "none"})
	if svc != nil {
		t.Fatal("expected nil for 'none' provider")
	}
}

func TestNewReturnsNilForEmpty(t *testing.T) {
	svc := New(Config{})
	if svc != nil {
		t.Fatal("expected nil for empty provider")
	}
}

func TestNewReturnsNilForOpenAIWithoutKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	svc := New(Config{Provider: "openai"})
	if svc != nil {
		t.Fatal("expected nil when no API key available")
	}
}

func TestNewReturnsServiceForOpenAIWithKey(t *testing.T) {
	svc := New(Config{Provider: "openai", APIKey: "test-key"})
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.Dimensions() != 1536 {
		t.Fatalf("dimensions = %d, want 1536", svc.Dimensions())
	}
	if svc.Model() != "text-embedding-3-small" {
		t.Fatalf("model = %q", svc.Model())
	}
}

func TestNewReturnsServiceForOllama(t *testing.T) {
	svc := New(Config{Provider: "ollama"})
	if svc == nil {
		t.Fatal("expected non-nil service for ollama")
	}
	if svc.Dimensions() != 768 {
		t.Fatalf("dimensions = %d, want 768", svc.Dimensions())
	}
	if svc.Model() != "nomic-embed-text" {
		t.Fatalf("model = %q", svc.Model())
	}
}

func TestNewOllamaCustomModel(t *testing.T) {
	svc := New(Config{Provider: "ollama", Model: "mxbai-embed-large", BaseURL: "http://localhost:11434"})
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.Model() != "mxbai-embed-large" {
		t.Fatalf("model = %q", svc.Model())
	}
}

func TestOllamaEmbedIntegration(t *testing.T) {
	// This test requires a running Ollama instance with nomic-embed-text
	svc := New(Config{Provider: "ollama"})
	if svc == nil {
		t.Skip("ollama service not available")
	}

	ctx := context.Background()
	vec, err := svc.Embed(ctx, "Hello world, this is a test sentence for embedding.")
	if err != nil {
		t.Skipf("ollama not running or model not pulled: %v", err)
	}

	if len(vec) != 768 {
		t.Fatalf("dimensions = %d, want 768", len(vec))
	}

	// Verify non-zero
	allZero := true
	for _, v := range vec {
		if v != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatal("embedding is all zeros")
	}

	t.Logf("Embedding: %d dimensions, first 5 values: %v", len(vec), vec[:5])
}

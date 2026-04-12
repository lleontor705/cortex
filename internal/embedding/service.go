// Package embedding provides text-to-vector embedding for Cortex.
//
// Embeddings enable semantic search: instead of matching keywords,
// search by meaning. The service supports multiple backends:
//   - ollama: Local Ollama server (default: nomic-embed-text, 768 dims)
//   - openai: OpenAI API (text-embedding-3-small, 384 dims)
//   - none: Disabled (default)
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// Service generates embeddings from text.
type Service interface {
	// Embed returns a vector embedding for the given text.
	Embed(ctx context.Context, text string) ([]float32, error)

	// Dimensions returns the embedding dimension size.
	Dimensions() int

	// Model returns the model identifier.
	Model() string
}

// Config configures the embedding service.
type Config struct {
	Provider string // "ollama", "openai", "none"
	APIKey   string // API key (OpenAI only; defaults to env var)
	Model    string // Model name override
	BaseURL  string // Base URL override (Ollama: default http://localhost:11434)
}

// New creates an embedding service from config.
// Returns nil if provider is "none" or empty.
func New(cfg Config) Service {
	switch cfg.Provider {
	case "ollama":
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		model := cfg.Model
		if model == "" {
			model = "nomic-embed-text"
		}
		return &ollamaService{baseURL: baseURL, model: model}

	case "openai":
		key := cfg.APIKey
		if key == "" {
			key = os.Getenv("OPENAI_API_KEY")
		}
		if key == "" {
			return nil
		}
		model := cfg.Model
		if model == "" {
			model = "text-embedding-3-small"
		}
		return &openAIService{apiKey: key, model: model}

	default:
		return nil
	}
}

// --- Ollama Backend ----------------------------------------------------------

type ollamaService struct {
	baseURL string
	model   string
	dims    int // cached after first call
	mu      sync.Mutex
}

func (s *ollamaService) Embed(ctx context.Context, text string) ([]float32, error) {
	body := map[string]any{
		"model": s.model,
		"input": text,
	}
	data, _ := json.Marshal(body)

	url := s.baseURL + "/api/embed"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: request failed (is Ollama running?): %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: API returned status %d", resp.StatusCode)
	}

	var result struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ollama: decode: %w", err)
	}
	if len(result.Embeddings) == 0 || len(result.Embeddings[0]) == 0 {
		return nil, fmt.Errorf("ollama: no embeddings returned")
	}

	// Convert float64 to float32
	raw := result.Embeddings[0]
	vec := make([]float32, len(raw))
	for i, v := range raw {
		vec[i] = float32(v)
	}

	// Cache dimensions from first result
	s.mu.Lock()
	if s.dims == 0 {
		s.dims = len(vec)
	}
	s.mu.Unlock()

	return vec, nil
}

func (s *ollamaService) Dimensions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dims > 0 {
		return s.dims
	}
	// Default for nomic-embed-text
	return 768
}

func (s *ollamaService) Model() string { return s.model }

// --- OpenAI Backend ----------------------------------------------------------

type openAIService struct {
	apiKey string
	model  string
	dims   int
	mu     sync.Mutex
}

func (s *openAIService) Embed(ctx context.Context, text string) ([]float32, error) {
	body := map[string]any{
		"model": s.model,
		"input": text,
	}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/embeddings", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: API returned status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("openai: decode: %w", err)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("openai: no data returned")
	}

	vec := result.Data[0].Embedding

	// Cache dimensions from first result
	s.mu.Lock()
	if s.dims == 0 {
		s.dims = len(vec)
	}
	s.mu.Unlock()

	return vec, nil
}

func (s *openAIService) Dimensions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dims > 0 {
		return s.dims
	}
	// Default for text-embedding-3-small
	return 1536
}
func (s *openAIService) Model() string   { return s.model }

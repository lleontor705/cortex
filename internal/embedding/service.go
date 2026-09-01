// Package embedding provides text-to-vector embedding for Cortex.
//
// Embeddings enable semantic search: instead of matching keywords,
// search by meaning. The service supports multiple backends:
//   - ollama: Local Ollama server (default: nomic-embed-text, 768 dims)
//   - openai: OpenAI API (text-embedding-3-small, 1536 dims)
//   - none: Disabled (default)
//
// HTTP client lifecycle: each backend holds a single reusable *http.Client
// (constructed once in New) so HTTP keepalive connections are pooled across
// all Embed() calls rather than re-created per call. The concrete types
// (*ollamaService, *openAIService) implement io.Closer: Close() calls
// CloseIdleConnections on the shared client, reaping the Transport's
// persistConn read/write goroutines. The composition root (app.Close,
// bench Close) type-asserts to io.Closer to invoke it — the Service
// interface itself is NOT bloated with Close(), so test fakes and stubs
// need no changes.
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
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

func newWithClient(cfg Config, client *http.Client, maxBody int64, maxConcurrent int) Service {
	if maxBody <= 0 {
		maxBody = defaultMaxEmbeddingResponse
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	sem := make(chan struct{}, maxConcurrent)
	switch cfg.Provider {
	case "ollama":
		baseURL, model := cfg.BaseURL, cfg.Model
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		if model == "" {
			model = "nomic-embed-text"
		}
		return &ollamaService{baseURL: strings.TrimRight(baseURL, "/"), model: model, client: client, maxResponseBody: maxBody, sem: sem}
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
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		return &openAIService{apiKey: key, model: model, baseURL: strings.TrimRight(baseURL, "/"), client: client, maxResponseBody: maxBody, sem: sem}
	default:
		return nil
	}
}

// defaultHTTPClient returns a shared *http.Client configured for embedding
// API calls with a generous timeout and keepalive. One client per service
// instance means HTTP connections are pooled and reused across Embed calls
// instead of being re-created (and leaked) per call.
func defaultHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			// Defaults are fine; the important thing is that the Transport
			// and its connection pool live as long as the client, which lives
			// as long as the service. Close() calls CloseIdleConnections to
			// reap the persistConn goroutines on shutdown.
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 5,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

// New creates an embedding service from config.
// Returns nil if provider is "none" or empty.
func New(cfg Config) Service {
	timeout := 30 * time.Second
	if cfg.Provider == "ollama" {
		timeout = 60 * time.Second
	}
	return newWithClient(cfg, defaultHTTPClient(timeout), defaultMaxEmbeddingResponse, 4)
}

// --- Ollama Backend ----------------------------------------------------------

type ollamaService struct {
	baseURL         string
	model           string
	dims            int // cached after first call
	client          *http.Client
	mu              sync.Mutex
	maxResponseBody int64
	sem             chan struct{}
}

func (s *ollamaService) Embed(ctx context.Context, text string) ([]float32, error) {
	if err := acquire(ctx, s.sem); err != nil {
		return nil, err
	}
	defer func() { <-s.sem }()
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

	resp, err := s.client.Do(req)
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
	if err := decodeBounded(resp.Body, s.maxResponseBody, &result); err != nil {
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

// Close releases idle HTTP keepalive connections held by the shared client.
// It is safe to call multiple times (idempotent). This implements io.Closer
// so the composition root (app.Close, bench Close) can type-assert and
// invoke it without bloating the Service interface.
func (s *ollamaService) Close() error {
	if s.client != nil {
		s.client.CloseIdleConnections()
	}
	return nil
}

// --- OpenAI Backend ----------------------------------------------------------

type openAIService struct {
	apiKey          string
	model           string
	baseURL         string
	dims            int
	client          *http.Client
	mu              sync.Mutex
	maxResponseBody int64
	sem             chan struct{}
}

func (s *openAIService) Embed(ctx context.Context, text string) ([]float32, error) {
	if err := acquire(ctx, s.sem); err != nil {
		return nil, err
	}
	defer func() { <-s.sem }()
	body := map[string]any{
		"model": s.model,
		"input": text,
	}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/embeddings", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.client.Do(req)
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
	if err := decodeBounded(resp.Body, s.maxResponseBody, &result); err != nil {
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

func acquire(ctx context.Context, sem chan struct{}) error {
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func decodeBounded(r io.Reader, max int64, dst any) error {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return err
	}
	if int64(len(b)) > max {
		return errors.New("embedding: response exceeded size limit")
	}
	return json.Unmarshal(b, dst)
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
func (s *openAIService) Model() string { return s.model }

// Close releases idle HTTP keepalive connections held by the shared client.
// It is safe to call multiple times (idempotent). This implements io.Closer
// so the composition root (app.Close, bench Close) can type-assert and
// invoke it without bloating the Service interface.
func (s *openAIService) Close() error {
	if s.client != nil {
		s.client.CloseIdleConnections()
	}
	return nil
}

// Compile-time assertions: both concrete services implement io.Closer.
var (
	_ interface{ Close() error } = (*ollamaService)(nil)
	_ interface{ Close() error } = (*openAIService)(nil)
)

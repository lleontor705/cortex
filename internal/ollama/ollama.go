// Package ollama manages the Ollama process lifecycle for Cortex.
//
// It provides health checking, automatic startup, model detection,
// and model pulling — all used by the TUI, CLI, and app initialization.
package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Manager handles checking, starting, and provisioning Ollama.
type Manager struct {
	baseURL     string
	startedByUs bool
	cmd         *exec.Cmd
}

// NewManager creates a new Ollama manager.
// If baseURL is empty, defaults to http://localhost:11434.
func NewManager(baseURL string) *Manager {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &Manager{baseURL: baseURL}
}

// IsRunning checks whether Ollama is responding at the configured URL.
func (m *Manager) IsRunning(ctx context.Context) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", m.baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Start launches `ollama serve` as a background process.
// It does not wait for readiness — use WaitReady after Start.
func (m *Manager) Start(ctx context.Context) error {
	binary := "ollama"
	if runtime.GOOS == "windows" {
		binary = "ollama.exe"
	}

	path, err := exec.LookPath(binary)
	if err != nil {
		return fmt.Errorf("ollama binary not found in PATH: %w", err)
	}

	m.cmd = exec.CommandContext(ctx, path, "serve")
	detachProcess(m.cmd)

	if err := m.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ollama serve: %w", err)
	}

	m.startedByUs = true
	return nil
}

// WaitReady polls the health endpoint until Ollama responds or timeout expires.
func (m *Manager) WaitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		if m.IsRunning(ctx) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ollama did not become ready within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// tagsResponse represents the Ollama /api/tags JSON response.
type tagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// HasModel checks if a specific model is available in Ollama.
func (m *Manager) HasModel(ctx context.Context, model string) (bool, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", m.baseURL+"/api/tags", nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("ollama not reachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var tags tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return false, fmt.Errorf("failed to decode tags: %w", err)
	}

	// Normalize: "qwen3-embedding:8b" matches "qwen3-embedding:8b" or "qwen3-embedding:8b-..."
	// Also handle bare names without tag (e.g., "nomic-embed-text" matches "nomic-embed-text:latest")
	for _, t := range tags.Models {
		if t.Name == model || strings.HasPrefix(t.Name, model+":") || strings.TrimSuffix(t.Name, ":latest") == model {
			return true, nil
		}
	}
	return false, nil
}

// PullModel runs `ollama pull <model>` and reports progress via the callback.
// The progressFn is called with status strings; it may be nil.
func (m *Manager) PullModel(ctx context.Context, model string, progressFn func(string)) error {
	binary := "ollama"
	if runtime.GOOS == "windows" {
		binary = "ollama.exe"
	}

	cmd := exec.CommandContext(ctx, binary, "pull", model)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ollama pull: %w", err)
	}

	buf := make([]byte, 4096)
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 && progressFn != nil {
			progressFn(strings.TrimSpace(string(buf[:n])))
		}
		if readErr != nil {
			break
		}
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ollama pull failed: %w", err)
	}
	return nil
}

// EnsureRunning checks if Ollama is running, starts it if not, and waits for readiness.
func (m *Manager) EnsureRunning(ctx context.Context) error {
	if m.IsRunning(ctx) {
		return nil
	}
	if err := m.Start(ctx); err != nil {
		return err
	}
	return m.WaitReady(ctx, 30*time.Second)
}

// StartedByUs returns true if this manager started the Ollama process.
func (m *Manager) StartedByUs() bool {
	return m.startedByUs
}

// BaseURL returns the configured Ollama base URL.
func (m *Manager) BaseURL() string {
	return m.baseURL
}

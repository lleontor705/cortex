package setup

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"
)

// OllamaSetupResult holds results of configuring Ollama embeddings.
type OllamaSetupResult struct {
	BaseURL         string   `json:"base_url"`
	Model           string   `json:"model"`
	OllamaOnline    bool     `json:"ollama_online"`
	AvailableModels []string `json:"available_models"`
	ModelAvailable  bool     `json:"model_available"`
	ConfigPath      string   `json:"config_path"`
}

// SetupOllama probes Ollama, validates the embedding model, and persists settings into ~/.cortex/cortex.yaml.
func SetupOllama(baseURL string, model string) (*OllamaSetupResult, error) {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	if model == "" {
		model = "nomic-embed-text"
	}

	result := &OllamaSetupResult{
		BaseURL:         baseURL,
		Model:           model,
		AvailableModels: []string{},
	}

	// 1. Probe Ollama daemon
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL + "/api/tags")
	if err != nil {
		result.OllamaOnline = false
	} else {
		result.OllamaOnline = true
		defer func() { _ = resp.Body.Close() }()

		type tagsResponse struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		var tags tagsResponse
		if json.NewDecoder(resp.Body).Decode(&tags) == nil {
			for _, m := range tags.Models {
				result.AvailableModels = append(result.AvailableModels, m.Name)
				if strings.EqualFold(m.Name, model) ||
					strings.EqualFold(m.Name, model+":latest") ||
					strings.HasPrefix(strings.ToLower(m.Name), strings.ToLower(model)+":") {
					result.ModelAvailable = true
				}
			}
		}
	}

	// 2. Load and update configuration
	cfg, err := config.Load("")
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	cfg.Search.EmbeddingProvider = "ollama"
	cfg.Search.EmbeddingModel = model
	cfg.Search.EmbeddingBaseURL = baseURL
	if cfg.AI.Provider == "" {
		cfg.AI.Provider = "ollama"
		cfg.AI.BaseURL = baseURL
	}

	targetPath := cfg.LoadedFrom
	if targetPath == "" {
		targetPath = fmt.Sprintf("%s/cortex.yaml", config.CortexDir())
	}

	if err := config.Save(cfg, targetPath); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}

	result.ConfigPath = targetPath
	return result, nil
}

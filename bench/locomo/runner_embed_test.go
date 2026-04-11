package locomo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lleontor705/cortex/internal/embedding"
)

func TestRunWithOllamaEmbeddings(t *testing.T) {
	dataPath := filepath.Join("..", "datasets", "locomo10.json")
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		t.Skip("LOCOMO dataset not downloaded")
	}

	// Verify Ollama is running
	svc := embedding.New(embedding.Config{Provider: "ollama"})
	if svc == nil {
		t.Skip("Ollama not available")
	}

	result, err := Run(Config{
		DataPath: dataPath,
		Limit:    50, // Subset — full run takes too long with embedding per obs
		EmbeddingCfg: &embedding.Config{
			Provider: "ollama",
			Model:    "nomic-embed-text",
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	_ = os.WriteFile(filepath.Join("..", "results", "locomo_ollama_50.json"), data, 0644)

	t.Logf("LOCOMO with Ollama embeddings (50 questions)")
	t.Logf("  Overall accuracy: %.1f%% (%d/%d)", result.Overall*100, result.Correct, result.Total)
	for cat, score := range result.ByType {
		t.Logf("  %-15s: %.3f avg F1", cat, score)
	}
}

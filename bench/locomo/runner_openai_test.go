package locomo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lleontor705/cortex/internal/embedding"
)

func TestRunWithOpenAIEmbeddings(t *testing.T) {
	dataPath := filepath.Join("..", "datasets", "locomo10.json")
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		t.Skip("LOCOMO dataset not downloaded")
	}

	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	result, err := Run(Config{
		DataPath: dataPath,
		Limit:    50,
		EmbeddingCfg: &embedding.Config{
			Provider: "openai",
			APIKey:   key,
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	_ = os.WriteFile(filepath.Join("..", "results", "locomo_openai_50.json"), data, 0644)

	t.Logf("LOCOMO with OpenAI embeddings (50 questions)")
	t.Logf("  Overall accuracy: %.1f%% (%d/%d)", result.Overall*100, result.Correct, result.Total)
	for cat, score := range result.ByType {
		t.Logf("  %-15s: %.3f avg F1", cat, score)
	}
}

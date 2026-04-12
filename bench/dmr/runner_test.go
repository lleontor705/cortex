package dmr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunWithRealDataset(t *testing.T) {
	dataPath := filepath.Join("..", "datasets", "msc_self_instruct.jsonl")
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		t.Skip("DMR dataset not downloaded — run bench/download.sh first")
	}

	result, err := Run(Config{
		DataPath: dataPath,
		Limit:    100, // First 100 Q&A pairs
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	t.Logf("DMR (first 100 Q&A)")
	t.Logf("  Overall accuracy: %.1f%% (%d/%d)", result.Overall*100, result.Correct, result.Total)
	for cat, score := range result.ByType {
		t.Logf("  %-15s: %.3f avg score", cat, score)
	}
}

func TestRunFullDataset(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full benchmark in short mode")
	}

	dataPath := filepath.Join("..", "datasets", "msc_self_instruct.jsonl")
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		t.Skip("DMR dataset not downloaded — run bench/download.sh first")
	}

	result, err := Run(Config{
		DataPath: dataPath,
		Limit:    0,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	resultsPath := filepath.Join("..", "results", "dmr.json")
	data, _ := json.MarshalIndent(result, "", "  ")
	_ = os.WriteFile(resultsPath, data, 0644)

	t.Logf("DMR (full dataset)")
	t.Logf("  Total Q&A: %d", result.Total)
	t.Logf("  Overall accuracy: %.1f%% (%d/%d)", result.Overall*100, result.Correct, result.Total)
	for cat, score := range result.ByType {
		t.Logf("  %-15s: %.3f avg score", cat, score)
	}
}

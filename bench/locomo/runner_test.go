package locomo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Ensure json import is used in test fixtures
var _ = json.RawMessage{}

func TestRunWithRealDataset(t *testing.T) {
	dataPath := filepath.Join("..", "datasets", "locomo10.json")
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		t.Skip("LOCOMO dataset not downloaded — run bench/download.sh first")
	}

	result, err := Run(Config{
		DataPath: dataPath,
		Limit:    50, // Subset for test speed
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	t.Logf("LOCOMO (first 50 questions)")
	t.Logf("  Overall accuracy: %.1f%% (%d/%d)", result.Overall*100, result.Correct, result.Total)
	for cat, score := range result.ByType {
		t.Logf("  %-15s: %.3f", cat, score)
	}
}

func TestRunFullDataset(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full benchmark in short mode")
	}

	dataPath := filepath.Join("..", "datasets", "locomo10.json")
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		t.Skip("LOCOMO dataset not downloaded — run bench/download.sh first")
	}

	result, err := Run(Config{
		DataPath: dataPath,
		Limit:    0, // All questions
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// Write results to file
	resultsPath := filepath.Join("..", "results", "locomo.json")
	data, _ := json.MarshalIndent(result, "", "  ")
	_ = os.WriteFile(resultsPath, data, 0644)

	t.Logf("LOCOMO (full dataset)")
	t.Logf("  Total questions: %d", result.Total)
	t.Logf("  Overall accuracy: %.1f%% (%d/%d)", result.Overall*100, result.Correct, result.Total)
	for cat, score := range result.ByType {
		t.Logf("  %-15s: %.3f avg F1", cat, score)
	}
}

func TestMinimalDataset(t *testing.T) {
	// Test with synthetic data that matches the real LOCOMO structure
	conversations := []Conversation{{
		SampleID: "test-1",
		Conversation: ConversationData{
			SpeakerA: "Alice",
			SpeakerB: "Bob",
			Sessions: map[string][]Turn{
				"session_1": {
					{Speaker: "Alice", Text: "I just adopted a Maine Coon cat named Whiskers."},
					{Speaker: "Bob", Text: "That sounds wonderful! How old is Whiskers?"},
					{Speaker: "Alice", Text: "About 2 years old. Got her from the shelter."},
				},
			},
			Dates: map[string]string{
				"session_1": "15 Jan 2024",
			},
		},
		QA: []QA{
			{Question: "What kind of cat does Alice have?", Answer: json.RawMessage(`"Maine Coon"`), Category: 1},
			{Question: "What is Alice's cat named?", Answer: json.RawMessage(`"Whiskers"`), Category: 1},
		},
		Observation: json.RawMessage(`{
			"session_1_observation": {
				"Alice": [
					["Alice adopted a 2-year-old Maine Coon cat named Whiskers from the shelter.", "D1:1"]
				]
			}
		}`),
	}}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	data, _ := json.Marshal(conversations)
	os.WriteFile(path, data, 0644)

	result, err := Run(Config{DataPath: path})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if result.Total != 2 {
		t.Fatalf("total = %d, want 2", result.Total)
	}

	t.Logf("Minimal test: %.1f%% accuracy (%d/%d)", result.Overall*100, result.Correct, result.Total)
	for _, d := range result.Details {
		t.Logf("  Q: %s | Expected: %s | F1: %.3f | Correct: %v", d.Query, d.Expected, d.Score, d.Correct)
	}
}

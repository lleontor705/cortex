package longmemeval

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lleontor705/cortex/v2/bench/common"
)

func writeDatasetJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	p := filepath.Join(t.TempDir(), "dataset.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return p
}

func datasetFixture() Dataset {
	return Dataset{Questions: []Question{
		{ID: "q1", Question: "What color is the sky?", Answer: "blue", Category: "IE",
			ChatHistory: []ChatTurn{
				{Role: "user", Content: "I like the sky.", SessionID: 1, Timestamp: "2024-01-01T00:00:00Z"},
				{Role: "assistant", Content: "The sky is blue.", SessionID: 1, Timestamp: "2024-01-01T00:01:00Z"},
			}},
		{ID: "q2", Question: "What did Alice say?", Answer: "hello", Category: "MR",
			ChatHistory: []ChatTurn{
				{Role: "user", Content: "Alice says hello.", SessionID: 2, Timestamp: "2024-01-02T00:00:00Z"},
			}},
	}}
}

func TestRunDatasetFormat(t *testing.T) {
	result, err := Run(Config{DataPath: writeDatasetJSON(t, datasetFixture()), Limit: 10})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Benchmark != "LongMemEval" {
		t.Errorf("Benchmark = %q, want LongMemEval", result.Benchmark)
	}
	if result.Total != 2 {
		t.Errorf("Total = %d, want 2", result.Total)
	}
	if len(result.Details) != 2 {
		t.Fatalf("Details len = %d, want 2", len(result.Details))
	}
	if result.Details[0].ID != "q1" || result.Details[1].ID != "q2" {
		t.Errorf("IDs = %q, %q", result.Details[0].ID, result.Details[1].ID)
	}
}

func TestRunBareArrayFormat(t *testing.T) {
	questions := []Question{
		{ID: "b1", Question: "What color?", Answer: "red", Category: "TR",
			ChatHistory: []ChatTurn{
				{Role: "user", Content: "My car is red.", SessionID: 1, Timestamp: "2024-01-01T00:00:00Z"},
			}},
	}
	result, err := Run(Config{DataPath: writeDatasetJSON(t, questions), Limit: 10})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("Total = %d, want 1", result.Total)
	}
	if result.Details[0].ID != "b1" {
		t.Errorf("ID = %q", result.Details[0].ID)
	}
}

func TestRunBothFormatsSameQuestions(t *testing.T) {
	dataset := Dataset{Questions: []Question{
		{ID: "d1", Question: "q?", Answer: "a", Category: "IE", ChatHistory: []ChatTurn{
			{Role: "user", Content: "context", SessionID: 1, Timestamp: "2024-01-01T00:00:00Z"},
		}},
	}}
	qArray := []Question{dataset.Questions[0]}

	dResult, err := Run(Config{DataPath: writeDatasetJSON(t, dataset), Limit: 10})
	if err != nil {
		t.Fatalf("Run dataset: %v", err)
	}
	aResult, err := Run(Config{DataPath: writeDatasetJSON(t, qArray), Limit: 10})
	if err != nil {
		t.Fatalf("Run array: %v", err)
	}
	if dResult.Total != aResult.Total {
		t.Errorf("total mismatch: dataset=%d array=%d", dResult.Total, aResult.Total)
	}
	if dResult.Details[0].Query != aResult.Details[0].Query {
		t.Errorf("query mismatch: %q vs %q", dResult.Details[0].Query, aResult.Details[0].Query)
	}
}

func TestRunFileNotFound(t *testing.T) {
	_, err := Run(Config{DataPath: filepath.Join(t.TempDir(), "nonexistent.json")})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestRunInvalidJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(p, []byte("{not valid json["), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Run(Config{DataPath: p})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestRunEmptyDataset(t *testing.T) {
	_, err := Run(Config{DataPath: writeDatasetJSON(t, Dataset{})})
	if err == nil {
		t.Fatal("expected error for empty dataset")
	}
}

func TestRunLimit(t *testing.T) {
	f := Dataset{Questions: []Question{
		{ID: "q1", Question: "q1", Answer: "a1", Category: "IE", ChatHistory: []ChatTurn{
			{Role: "user", Content: "c1", SessionID: 1, Timestamp: "2024-01-01T00:00:00Z"},
		}},
		{ID: "q2", Question: "q2", Answer: "a2", Category: "MR", ChatHistory: []ChatTurn{
			{Role: "user", Content: "c2", SessionID: 2, Timestamp: "2024-01-01T00:00:00Z"},
		}},
		{ID: "q3", Question: "q3", Answer: "a3", Category: "TR", ChatHistory: []ChatTurn{
			{Role: "user", Content: "c3", SessionID: 3, Timestamp: "2024-01-01T00:00:00Z"},
		}},
	}}
	result, err := Run(Config{DataPath: writeDatasetJSON(t, f), Limit: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("Total = %d, want 2", result.Total)
	}
}

func TestTruncateBoundary(t *testing.T) {
	if s := truncate("short", 10); s != "short" {
		t.Errorf("short = %q", s)
	}
	if s := truncate("abcdefghij", 5); s != "abcde..." {
		t.Errorf("long = %q", s)
	}
	if s := truncate("abcde", 5); s != "abcde" {
		t.Errorf("exact = %q", s)
	}
	if s := truncate("", 5); s != "" {
		t.Errorf("empty = %q", s)
	}
}

func TestMultiCategoryWithMultiSessionAndEmptyHistory(t *testing.T) {
	f := Dataset{Questions: []Question{
		{ID: "ie", Question: "Extract info", Answer: "info", Category: "IE", ChatHistory: []ChatTurn{
			{Role: "user", Content: "info context", SessionID: 1, Timestamp: "2024-01-01T00:00:00Z"},
		}},
		{ID: "mr", Question: "Multi session", Answer: "reason", Category: "MR", ChatHistory: []ChatTurn{
			{Role: "user", Content: "session one", SessionID: 2, Timestamp: "2024-01-01T00:00:00Z"},
			{Role: "user", Content: "session two", SessionID: 3, Timestamp: "2024-01-02T00:00:00Z"},
		}},
		{ID: "tr", Question: "Time reasoning", Answer: "temporal", Category: "TR", ChatHistory: []ChatTurn{
			{Role: "user", Content: "yesterday event", SessionID: 4, Timestamp: "2024-01-01T00:00:00Z"},
		}},
		{ID: "ku", Question: "Knowledge update", Answer: "updated", Category: "KU", ChatHistory: []ChatTurn{
			{Role: "user", Content: "new knowledge", SessionID: 5, Timestamp: "2024-01-01T00:00:00Z"},
		}},
		{ID: "abs", Question: "No answer available", Answer: "unknown", Category: "ABS", ChatHistory: []ChatTurn{
			{Role: "user", Content: "nothing known", SessionID: 6, Timestamp: "2024-01-01T00:00:00Z"},
		}},
		{ID: "ms", Question: "multi session grouping", Answer: "answer", Category: "MR", ChatHistory: []ChatTurn{
			{Role: "user", Content: "s10 turn 1", SessionID: 10, Timestamp: "2024-01-01T00:00:00Z"},
			{Role: "assistant", Content: "reply", SessionID: 10, Timestamp: "2024-01-01T00:01:00Z"},
			{Role: "user", Content: "s20 turn 1", SessionID: 20, Timestamp: "2024-01-02T00:00:00Z"},
			{Role: "assistant", Content: "reply 2", SessionID: 20, Timestamp: "2024-01-02T00:01:00Z"},
		}},
		{ID: "empty_hist", Question: "bare question", Answer: "answer", Category: "ABS", ChatHistory: nil},
	}}
	result, err := Run(Config{DataPath: writeDatasetJSON(t, f), Limit: 10})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Total != 7 {
		t.Errorf("Total = %d, want 7", result.Total)
	}
	if result.Benchmark != "LongMemEval" {
		t.Errorf("Benchmark = %q", result.Benchmark)
	}
}

func TestJudgePathWithMockOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "test",
			"message": map[string]any{
				"role":    "assistant",
				"content": `{"verdict":"correct","score":0.9,"reasoning":"match"}`,
			},
			"done": true,
		})
	}))
	defer srv.Close()

	cfg := &common.JudgeConfig{Endpoint: srv.URL, Model: "test", Timeout: 5e9}
	f := Dataset{Questions: []Question{
		{ID: "judge-q", Question: "What color?", Answer: "blue", Category: "IE", ChatHistory: []ChatTurn{
			{Role: "user", Content: "sky is blue", SessionID: 1, Timestamp: "2024-01-01T00:00:00Z"},
		}},
	}}
	result, err := Run(Config{DataPath: writeDatasetJSON(t, f), JudgeCfg: cfg, Limit: 10})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("Total = %d, want 1", result.Total)
	}
	d := result.Details[0]
	if !d.Correct {
		t.Errorf("Correct = false, want true (judge verdict correct, score 0.9)")
	}
	if d.Score != 0.0 {
		t.Errorf("Score = %f, want 0.0 (Score is F1, not judge; judge only affects Correct)", d.Score)
	}
}

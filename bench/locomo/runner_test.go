package locomo

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lleontor705/cortex/v2/bench/common"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/retrieval"
)

// Ensure json import is used in test fixtures
var _ = json.RawMessage{}

const (
	archQuery = "Explain the architecture of the retrieval system"
	pipeQuery = "retrieval pipeline"
	archProbe = "probe-1"
)

// seedAdaptiveCorpus ingests a pattern-typed observation — the adaptive
// engine's architectural tier boosts its rank by +2.5 while plain FTS never
// does — plus two manual observations sharing "retrieval pipeline" so the
// probe queries below hit one and two-plus rows.
func seedAdaptiveCorpus(t *testing.T) *common.BenchStores {
	t.Helper()
	stores, err := common.NewBenchStores()
	if err != nil {
		t.Fatalf("bench stores: %v", err)
	}
	t.Cleanup(func() { _ = stores.Close() })
	ctx := context.Background()
	for i, obs := range []domain.Observation{
		{Title: "architecture overview", Content: archQuery + " in detail", Type: "pattern"},
		{Title: "routing note", Content: "the retrieval pipeline modules handle query routing and fusion", Type: "manual"},
		{Title: "stage note", Content: "retrieval pipeline stages emit ranked candidates for downstream scoring", Type: "manual"},
	} {
		if err := stores.IngestSession(ctx, "s"+string(rune('0'+i)), archProbe, []domain.Observation{obs}); err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
	}
	return stores
}

func ftsHits(t *testing.T, stores *common.BenchStores, query string) []*domain.SearchResult {
	t.Helper()
	hits, err := stores.App.Stores.Search.Search(context.Background(), query, domain.SearchOptions{
		Limit: 10, Project: archProbe,
	})
	if err != nil {
		t.Fatalf("fts search(%q): %v", query, err)
	}
	return hits
}

func requireSameResults(t *testing.T, got, want []*domain.SearchResult, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: len = %d, want %d", label, len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Content != want[i].Content {
			t.Fatalf("%s[%d]: got (id=%d, content=%q), want (id=%d, content=%q)",
				label, i, got[i].ID, got[i].Content, want[i].ID, want[i].Content)
		}
		if math.Abs(got[i].Rank-want[i].Rank) > 1e-6 {
			t.Errorf("%s[%d]: rank = %.9f, want %.9f", label, i, got[i].Rank, want[i].Rank)
		}
	}
}

// TestQueryPathAdaptiveDefaultVsLegacyPath pins both routing rows: LegacyPath
// reproduces the original FTS + RRF fusion query byte-for-byte, and the
// default path matches an independently assembled ExecuteAdaptiveSearch call
// carrying the prior stage's FusionScores, including the architectural-tier
// +2.5 rank boost that only ExecuteAdaptiveSearch applies.
func TestQueryPathAdaptiveDefaultVsLegacyPath(t *testing.T) {
	stores := seedAdaptiveCorpus(t)
	ctx := context.Background()

	for _, query := range []string{archQuery, pipeQuery} {
		legacy, err := queryPath(ctx, stores, query, archProbe, Config{LegacyPath: true})
		if err != nil {
			t.Fatalf("legacy queryPath(%q): %v", query, err)
		}
		requireSameResults(t, legacy, ftsHits(t, stores, query), "LegacyPath vs direct FTS")

		prior := ftsHits(t, stores, query)
		if len(prior) == 0 {
			t.Fatalf("prior(%q) empty; corpus needs hits", query)
		}
		scores := fusionScores(prior)
		if len(scores) != len(prior) {
			t.Fatalf("fusionScores(%q): len = %d, want %d", query, len(scores), len(prior))
		}
		for i, r := range prior {
			if scores[i] != r.Rank {
				t.Errorf("fusionScores(%q)[%d] = %f, want prior rank %f", query, i, scores[i], r.Rank)
			}
		}
		if avail := retrieval.ComputeFusionFeatures(scores).Available; avail != (len(prior) >= 2) {
			t.Errorf("fusionScores(%q): SkewRoute features available = %v on %d hits", query, avail, len(prior))
		}

		cfg := Config{}
		adaptive, err := queryPath(ctx, stores, query, archProbe, cfg)
		if err != nil {
			t.Fatalf("adaptive queryPath(%q): %v", query, err)
		}
		want, err := retrieval.ExecuteAdaptiveSearch(ctx, query, retrieval.AdaptiveSearchOptions{
			Mode: "auto", Project: archProbe, Limit: 10, FusionScores: scores,
		}, lexicalSearch(stores, cfg), nil)
		if err != nil {
			t.Fatalf("ExecuteAdaptiveSearch(%q): %v", query, err)
		}
		requireSameResults(t, adaptive, want.Results, "default path vs ExecuteAdaptiveSearch")
	}

	legacy, err := queryPath(ctx, stores, archQuery, archProbe, Config{LegacyPath: true})
	if err != nil {
		t.Fatalf("legacy queryPath: %v", err)
	}
	adaptive, err := queryPath(ctx, stores, archQuery, archProbe, Config{})
	if err != nil {
		t.Fatalf("adaptive queryPath: %v", err)
	}
	if len(adaptive) != len(legacy) || len(adaptive) == 0 {
		t.Fatalf("adaptive/legacy lens = %d/%d, want equal non-zero", len(adaptive), len(legacy))
	}
	for i := range legacy {
		if adaptive[i].ID != legacy[i].ID {
			t.Fatalf("adaptive[%d]: id = %d, want legacy id %d", i, adaptive[i].ID, legacy[i].ID)
		}
		boost := adaptive[i].Rank - legacy[i].Rank
		if adaptive[i].Type == "pattern" {
			if math.Abs(boost-2.5) > 1e-6 {
				t.Errorf("pattern rank boost = %.9f, want 2.5 (adaptive engine not invoked)", boost)
			}
		} else if math.Abs(boost) > 1e-6 {
			t.Errorf("%s rank delta = %.9f, want 0", adaptive[i].Type, boost)
		}
	}
}

// adaptiveRunFixture writes one conversation whose sessions contain every
// term of both probe queries (FTS matches are ANDed per observation).
func adaptiveRunFixture(t *testing.T) string {
	t.Helper()
	doc := []map[string]any{{
		"sample_id": archProbe,
		"conversation": map[string]any{
			"speaker_a": "Alice", "speaker_b": "Bob",
			"session_1": []map[string]string{
				{"speaker": "Alice", "text": archQuery + " in detail"},
			},
			"session_1_date_time": "15 Jan 2024",
			"session_2": []map[string]string{
				{"speaker": "Alice", "text": "the retrieval pipeline modules handle query routing and fusion"},
				{"speaker": "Bob", "text": "retrieval pipeline stages emit ranked candidates for downstream scoring"},
			},
			"session_2_date_time": "16 Jan 2024",
		},
		"qa": []map[string]any{
			{"question": archQuery, "answer": "retrieval system architecture", "category": 4},
			{"question": pipeQuery, "answer": "ranking", "category": 1},
		},
	}}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "conversation.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// TestRunBothPathsPublishScoresAndJudgeBlockedGatesScoring exercises the two
// routing rows through Run on one small corpus (the committed port of the
// legacy-vs-adaptive overlay comparison), then pins the rag-t10 fail-closed
// contract: unreachable probe and mid-scoring judge failures both return a
// nil result with *common.BlockedError and exit code 2 — scores are never
// fabricated.
func TestRunBothPathsPublishScoresAndJudgeBlockedGatesScoring(t *testing.T) {
	path := adaptiveRunFixture(t)

	for label, cfg := range map[string]Config{"adaptive": {}, "legacy": {LegacyPath: true}} {
		cfg.DataPath, cfg.Limit = path, 10
		result, err := Run(cfg)
		if err != nil {
			t.Fatalf("%s Run: %v", label, err)
		}
		if result.Total != 2 || len(result.Details) != 2 {
			t.Fatalf("%s: total/details = %d/%d, want 2/2", label, result.Total, len(result.Details))
		}
		for i, d := range result.Details {
			if d.Score < 0 || d.Score > 1 || d.Got == "" {
				t.Errorf("%s: details[%d] score=%f got=%q; want published per-query evidence", label, i, d.Score, d.Got)
			}
		}
	}

	judgeConfig := func(endpoint string) Config {
		return Config{DataPath: path, JudgeCfg: &common.JudgeConfig{Endpoint: endpoint, Model: "test", Timeout: 5e9}}
	}
	assertBlocked := func(label string, result *common.BenchmarkResult, err error) {
		t.Helper()
		if err == nil || result != nil {
			t.Fatalf("%s: (result, err) = (%v, %v), want (nil, BLOCKED)", label, result, err)
		}
		var blocked *common.BlockedError
		if !errors.As(err, &blocked) {
			t.Fatalf("%s: err = %v, want *common.BlockedError", label, err)
		}
		if code := common.EvalExitCode(err); code != 2 {
			t.Errorf("%s: EvalExitCode = %d, want 2", label, code)
		}
	}
	result, err := Run(judgeConfig("http://127.0.0.1:1"))
	assertBlocked("unreachable probe", result, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[]}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	result, err = Run(judgeConfig(srv.URL))
	assertBlocked("judge fails during scoring", result, err)
}

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
	_ = os.WriteFile(path, data, 0644)

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

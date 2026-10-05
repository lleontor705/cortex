package longmemeval

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/bench/common"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/retrieval"
)

const (
	archQuery = "Explain the architecture of the retrieval system"
	pipeQuery = "retrieval pipeline"
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
		if err := stores.IngestSession(ctx, "s"+string(rune('0'+i)), "probe", []domain.Observation{obs}); err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
	}
	return stores
}

func ftsHits(t *testing.T, stores *common.BenchStores, query string) []*domain.SearchResult {
	t.Helper()
	hits, err := stores.App.Stores.Search.Search(context.Background(), query, domain.SearchOptions{Limit: 10})
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

// TestRetrieveAdaptiveDefaultVsLegacyPath pins both routing rows: LegacyPath
// reproduces the original FTS+RRF store search byte-for-byte, and the default
// path matches an independently assembled ExecuteAdaptiveSearch call carrying
// the prior stage's FusionScores, including the architectural-tier +2.5 rank
// boost that only ExecuteAdaptiveSearch applies.
func TestRetrieveAdaptiveDefaultVsLegacyPath(t *testing.T) {
	stores := seedAdaptiveCorpus(t)
	ctx := context.Background()

	for _, query := range []string{archQuery, pipeQuery} {
		legacy, err := retrieve(ctx, stores, query, Config{LegacyPath: true})
		if err != nil {
			t.Fatalf("legacy retrieve(%q): %v", query, err)
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
		adaptive, err := retrieve(ctx, stores, query, cfg)
		if err != nil {
			t.Fatalf("adaptive retrieve(%q): %v", query, err)
		}
		want, err := retrieval.ExecuteAdaptiveSearch(ctx, query, retrieval.AdaptiveSearchOptions{
			Mode: "auto", Limit: 10, FusionScores: scores,
		}, lexicalSearch(stores, cfg), nil)
		if err != nil {
			t.Fatalf("ExecuteAdaptiveSearch(%q): %v", query, err)
		}
		requireSameResults(t, adaptive, want.Results, "default path vs ExecuteAdaptiveSearch")
	}

	legacy, err := retrieve(ctx, stores, archQuery, Config{LegacyPath: true})
	if err != nil {
		t.Fatalf("legacy retrieve: %v", err)
	}
	adaptive, err := retrieve(ctx, stores, archQuery, Config{})
	if err != nil {
		t.Fatalf("adaptive retrieve: %v", err)
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

// TestRunBothPathsPublishScoresAndJudgeBlockedGatesScoring exercises the two
// routing rows through Run on one small corpus, then pins the rag-t10
// fail-closed contract: unreachable probe and mid-scoring judge failures both
// return a nil result with *common.BlockedError and exit code 2 — scores are
// never fabricated — and the refreshed eval limitations note names the
// adaptive pipeline the runners now default to.
func TestRunBothPathsPublishScoresAndJudgeBlockedGatesScoring(t *testing.T) {
	fixture := Dataset{Questions: []Question{
		{ID: "q1", Question: archQuery, Answer: "retrieval system architecture", Category: "IE",
			ChatHistory: []ChatTurn{{Role: "user", Content: archQuery + " in detail", SessionID: 1, Timestamp: "2024-01-01T00:00:00Z"}}},
		{ID: "q2", Question: pipeQuery, Answer: "ranking", Category: "MR",
			ChatHistory: []ChatTurn{
				{Role: "user", Content: "the retrieval pipeline modules handle query routing and fusion", SessionID: 2, Timestamp: "2024-01-01T00:01:00Z"},
				{Role: "user", Content: "retrieval pipeline stages emit ranked candidates for downstream scoring", SessionID: 3, Timestamp: "2024-01-01T00:02:00Z"},
			}},
	}}
	path := writeDatasetJSON(t, fixture)

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

	report, err := common.BuildEvalReport(common.EvalBenchmarkLongMemEval, common.BenchmarkResult{
		Total: 1, Correct: 1, Overall: 1,
		Details: []common.QuestionResult{{ID: "q1", Type: "IE", Score: 1, Correct: true}},
	},
		false, common.JudgeProtocolSummary{Enabled: true, Protocol: common.FixedJudgeProtocolVersion, Temperature: 0, Seed: 42, Format: "json"}, nil, 0)
	if err != nil {
		t.Fatalf("BuildEvalReport: %v", err)
	}
	namedAdaptive := false
	for _, note := range report.Limitations {
		if strings.Contains(note, "ExecuteAdaptiveSearch") {
			namedAdaptive = true
		}
	}
	if !namedAdaptive {
		t.Errorf("limitations note does not name the adaptive default pipeline: %v", report.Limitations)
	}
}

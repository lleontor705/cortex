package retrieval

import (
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/graph"
)

func recognitionResults(ranks ...float64) []*domain.SearchResult {
	out := make([]*domain.SearchResult, 0, len(ranks))
	for i, r := range ranks {
		out = append(out, &domain.SearchResult{
			Observation: domain.Observation{ID: int64(i + 1), Title: "candidate"},
			Rank:        r,
		})
	}
	return out
}

// TestRecognitionFilterPrunesWeakAndFillsTopK pins the recognition-memory
// contract: post-PPR candidates below the relative/absolute floors are pruned,
// the requested top-k is always filled from the strongest pruned candidates,
// and the surviving order follows descending score.
func TestRecognitionFilterPrunesWeakAndFillsTopK(t *testing.T) {
	// Post-PPR ranks: 3 clearly recognized candidates, then a weak tail.
	results := recognitionResults(0.90, 0.50, 0.30, 0.02, 0.015, 0.012, 0.010, 0.008, 0.006, 0.004)

	cfg := DefaultRecognitionConfig()
	cfg.TopK = 5

	got := ApplyRecognitionFilter(results, cfg)

	want := []float64{0.90, 0.50, 0.30, 0.02, 0.015}
	if len(got) != len(want) {
		t.Fatalf("recognition filter returned %d candidates, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Rank != w {
			t.Errorf("position %d: rank = %f, want %f", i, got[i].Rank, w)
		}
	}

	// Top-k smaller than the recognized set keeps only the recognized strong ones.
	cfg.TopK = 3
	got = ApplyRecognitionFilter(results, cfg)
	if len(got) != 3 {
		t.Fatalf("TopK=3 returned %d candidates, want 3", len(got))
	}
	for i, r := range got {
		if r.Rank < 0.30 {
			t.Errorf("TopK=3 must prune weak tail, position %d rank = %f", i, r.Rank)
		}
	}

	// TopK unset: pure pruning, no fill.
	cfg.TopK = 0
	got = ApplyRecognitionFilter(results, cfg)
	if len(got) != 3 {
		t.Fatalf("pure prune returned %d candidates, want the 3 recognized ones", len(got))
	}
}

// TestRecognitionFilterFloorsDeterminismAndImmutability pins the noise floor,
// deterministic tie-breaking (descending ID, mirroring the RRF contract),
// empty-input safety, and caller-slice immutability.
func TestRecognitionFilterFloorsDeterminismAndImmutability(t *testing.T) {
	top := recognitionResults(1.0, 0.004, 0.5, 0.5)
	// Give the tied candidates distinct IDs: ID 2 = 0.004, ID 3 = 0.5, ID 4 = 0.5.
	top[0].ID = 10
	top[2].ID = 3
	top[3].ID = 4

	cfg := DefaultRecognitionConfig()
	cfg.TopK = 0

	got := ApplyRecognitionFilter(top, cfg)

	if len(got) != 3 {
		t.Fatalf("expected noise-floor pruning to keep 3 candidates, got %d", len(got))
	}
	if got[0].ID != 10 || got[0].Rank != 1.0 {
		t.Errorf("top candidate must stay first, got ID=%d rank=%f", got[0].ID, got[0].Rank)
	}
	if got[1].ID != 4 {
		t.Errorf("score tie must break by descending ID (RRF mirror), got ID=%d", got[1].ID)
	}
	if got[2].ID != 3 {
		t.Errorf("score tie must break by descending ID (RRF mirror), got ID=%d", got[2].ID)
	}

	// Caller slice must be untouched.
	if top[1].Rank != 0.004 || top[2].ID != 3 {
		t.Error("ApplyRecognitionFilter mutated its input slice")
	}

	if empty := ApplyRecognitionFilter(nil, cfg); len(empty) != 0 {
		t.Errorf("nil input must yield empty output, got %d", len(empty))
	}
}

// TestCRAGRecognitionKeepsStrongCandidatesAligned pins that the recognition
// filter never drops a candidate that the CRAG evaluation keeps: both agree on
// the strong set when the requested top-k covers it.
func TestCRAGRecognitionKeepsStrongCandidatesAligned(t *testing.T) {
	results := recognitionResults(0.90, 0.80, 0.70, 0.65, 0.55)

	cragEval := EvaluateCRAG(results, DefaultCRAGConfig())
	cfg := DefaultRecognitionConfig()
	cfg.TopK = len(results)
	recognition := ApplyRecognitionFilter(results, cfg)

	if len(cragEval.FilteredResults) != len(results) {
		t.Fatalf("expected CRAG to keep all strong candidates, got %d", len(cragEval.FilteredResults))
	}
	if len(recognition) != len(cragEval.FilteredResults) {
		t.Fatalf("recognition kept %d candidates, CRAG kept %d", len(recognition), len(cragEval.FilteredResults))
	}
	for i := range recognition {
		if recognition[i].ID != cragEval.FilteredResults[i].ID {
			t.Errorf("position %d: recognition ID=%d, CRAG ID=%d", i, recognition[i].ID, cragEval.FilteredResults[i].ID)
		}
	}
}

// TestCRAGSparseGraphPipelineFallsBackToRRFFusion composes the sparse-graph
// fallback with the existing dense-plus-lexical fusion path: when the graph
// cannot propagate, no error occurs and no graph score exists — the
// recognition filter prunes the weak pre-fusion tail while filling top-k, and
// RRF fusion then runs with its contract (position-only inputs, overlap
// accumulation) fully intact.
func TestCRAGSparseGraphPipelineFallsBackToRRFFusion(t *testing.T) {
	ftsHits := []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1, Title: "auth gateway"}, Rank: 0.9},
		{Observation: domain.Observation{ID: 2, Title: "jwt policy"}, Rank: 0.8},
		{Observation: domain.Observation{ID: 3, Title: "session store"}, Rank: 0.7},
		{Observation: domain.Observation{ID: 5, Title: "noise candidate"}, Rank: 0.02},
	}
	denseHits := []*domain.VectorSearchResult{
		{Observation: domain.Observation{ID: 2, Title: "jwt policy"}, Similarity: 0.95},
		{Observation: domain.Observation{ID: 4, Title: "token validator"}, Similarity: 0.70},
	}

	out := graph.RunHippoRAG2(graph.HippoRAG2Request{
		PassageSeeds: []graph.PassageSeed{{PassageID: 1, DenseScore: 0.9}},
	})
	if out.Applied {
		t.Fatal("empty graph must not apply PPR so the fusion path stays authoritative")
	}
	if len(out.PassageScores) != 0 {
		t.Fatalf("fallback must contribute no graph scores, got %d", len(out.PassageScores))
	}

	cfg := DefaultRecognitionConfig()
	cfg.TopK = 3
	recognized := ApplyRecognitionFilter(ftsHits, cfg)

	if len(recognized) != cfg.TopK {
		t.Fatalf("recognition must fill the requested top-k, got %d want %d", len(recognized), cfg.TopK)
	}
	for _, r := range recognized {
		if r.ID == 5 {
			t.Error("weak post-PPR candidate must be pruned before final fusion")
		}
	}

	fused := FuseResults(recognized, denseHits, 5)
	if len(fused) == 0 {
		t.Fatal("dense-plus-lexical fusion must produce candidates after fallback")
	}
	if fused[0].ID != 2 {
		t.Errorf("RRF contract changed: overlapping candidate must rank first, got ID=%d", fused[0].ID)
	}
	for _, r := range fused {
		if r.ID == 5 {
			t.Error("pruned weak candidate leaked into the fused output")
		}
	}
}

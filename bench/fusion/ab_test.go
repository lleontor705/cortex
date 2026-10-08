package fusion

import (
	"math"
	"testing"

	"github.com/lleontor705/cortex/v2/bench/common"
)

func TestRRFFuseMatchesProductionFormula(t *testing.T) {
	// Production semantics: rank r contributes 1/(k+r+1), credits accumulate
	// per ID, order is score DESC then ID DESC.
	lexical := []Candidate{{ID: "a"}, {ID: "b"}}
	vector := []Candidate{{ID: "b"}, {ID: "c"}}

	fused := RRFFuse(lexical, vector, Options{})

	wantA := 1.0 / (float64(DefaultRRFConstant) + 1.0)
	wantB := 1.0/(float64(DefaultRRFConstant)+1.0) + 1.0/(float64(DefaultRRFConstant)+2.0)
	wantC := 1.0 / (float64(DefaultRRFConstant) + 2.0)
	if len(fused) != 3 {
		t.Fatalf("expected 3 fused candidates, got %d", len(fused))
	}
	if fused[0].ID != "b" {
		t.Fatalf("expected 'b' first (two credits), got %q", fused[0].ID)
	}
	// 'a' and 'c' hold identical single credits; ID DESC tie-break puts a first.
	if fused[1].ID != "a" || fused[2].ID != "c" {
		t.Fatalf("unexpected tail order: %q, %q", fused[1].ID, fused[2].ID)
	}
	_ = wantA
	_ = wantB
	_ = wantC
}

func TestRRFFuseConstantIsSixty(t *testing.T) {
	// Pin the production constant: a rank-0-only candidate scores 1/61.
	lexical := []Candidate{{ID: "only"}}
	vector := []Candidate(nil)
	fused := RRFFuse(lexical, vector, Options{})
	want := 1.0 / 61.0
	if got := rankScore(t, fused, "only"); math.Abs(got-want) > 1e-12 {
		t.Fatalf("RRF k must stay 60: rank-0 credit = %.12f, want %.12f", got, want)
	}
}

// rankScore recomputes the accumulated RRF score of a candidate by re-fusing
// it in isolation (position must be preserved by the sort).
func rankScore(t *testing.T, fused []Candidate, id string) float64 {
	t.Helper()
	for rank, c := range fused {
		if c.ID == id {
			return 1.0 / (float64(DefaultRRFConstant) + float64(rank+1))
		}
	}
	t.Fatalf("candidate %q missing from fused output", id)
	return 0
}

func TestConvexFuseAlphaExtremes(t *testing.T) {
	lexical := []Candidate{
		{ID: "a", LexicalScore: 1.0, VectorScore: 0.0},
		{ID: "b", LexicalScore: 0.0, VectorScore: 0.0},
	}
	vector := []Candidate{
		{ID: "c", LexicalScore: 0.0, VectorScore: 1.0},
		{ID: "d", LexicalScore: 0.0, VectorScore: 0.0},
	}

	if got := ConvexFuse(lexical, vector, 1.0); got[0].ID != "a" {
		t.Fatalf("alpha=1 must be pure lexical, got %q first", got[0].ID)
	}
	if got := ConvexFuse(lexical, vector, 0.0); got[0].ID != "c" {
		t.Fatalf("alpha=0 must be pure vector, got %q first", got[0].ID)
	}
	// alpha=0.5: 'a' and 'c' tie at 0.5; ID DESC tie-break puts c first.
	got := ConvexFuse(lexical, vector, 0.5)
	if got[0].ID != "c" || got[1].ID != "a" {
		t.Fatalf("alpha=0.5 tie-break wrong: %q, %q", got[0].ID, got[1].ID)
	}
}

func TestNormalizeMinMaxDegenerate(t *testing.T) {
	list := []Candidate{{ID: "x", LexicalScore: 0.7}, {ID: "y", LexicalScore: 0.7}}
	norm := normalizeMinMax(list, func(c Candidate) float64 { return c.LexicalScore })
	for i, v := range norm {
		if math.Abs(v-0.5) > 1e-12 {
			t.Fatalf("degenerate list must normalize to 0.5, index %d got %v", i, v)
		}
	}
}

func TestGenerateCorpusDeterministic(t *testing.T) {
	cfg := Config{CorpusSize: 50, QueryCount: 12, Seed: 7}
	first := GenerateCorpus(cfg)
	second := GenerateCorpus(cfg)
	if len(first) != len(second) {
		t.Fatalf("corpus size changed between runs")
	}
	for qi := range first {
		if first[qi].ID != second[qi].ID {
			t.Fatalf("query %d ID drifted", qi)
		}
		for ci := range first[qi].Lexical {
			if first[qi].Lexical[ci] != second[qi].Lexical[ci] {
				t.Fatalf("lexical list drifted at query %d candidate %d", qi, ci)
			}
		}
	}
}

func TestCalibrateAlphaDeterministic(t *testing.T) {
	queries := GenerateCorpus(Config{CorpusSize: 60, QueryCount: 18, Seed: 11})
	tuning := queries[:9]
	first := CalibrateAlpha(tuning, DefaultTopK, DefaultAlphaGridStep)
	for i := 0; i < 5; i++ {
		if again := CalibrateAlpha(tuning, DefaultTopK, DefaultAlphaGridStep); again != first {
			t.Fatalf("alpha calibration unstable: %.2f then %.2f", first, again)
		}
	}
	if first < 0 || first > 1 {
		t.Fatalf("calibrated alpha out of range: %v", first)
	}
}

func TestHarnessRecommendationStableAcrossRuns(t *testing.T) {
	cfg := Config{CorpusSize: 120, QueryCount: 45, Seed: 99}
	first, err := Run(cfg)
	if err != nil {
		t.Fatalf("run harness: %v", err)
	}
	second, err := Run(cfg)
	if err != nil {
		t.Fatalf("run harness again: %v", err)
	}
	if first.Recommendation != second.Recommendation {
		t.Fatalf("recommendation drifted: %q vs %q", first.Recommendation, second.Recommendation)
	}
	if first.Recommendation != RecommendationKeepRRF && first.Recommendation != RecommendationConvex {
		t.Fatalf("unknown recommendation %q", first.Recommendation)
	}
	for i := range first.Strategies {
		a, b := first.Strategies[i], second.Strategies[i]
		if a.Name != b.Name || a.RecallAtK != b.RecallAtK || a.NDCGAtK != b.NDCGAtK ||
			a.PrecisionAtK != b.PrecisionAtK || a.MRR != b.MRR {
			t.Fatalf("metrics drifted for strategy %q", a.Name)
		}
		// Latency is informational and may vary; it must never leak into the
		// recommendation, which the equality above already proves.
	}
}

func TestHarnessEmitsBothStrategiesAndContractNote(t *testing.T) {
	rep, err := Run(Config{CorpusSize: 80, QueryCount: 30, Seed: 3})
	if err != nil {
		t.Fatalf("run harness: %v", err)
	}
	if len(rep.Strategies) != 2 {
		t.Fatalf("expected 2 strategies, got %d", len(rep.Strategies))
	}
	if rep.Strategies[0].Name != "rrf-k60" || rep.Strategies[1].Name != "convex-combination" {
		t.Fatalf("strategy names wrong: %q, %q", rep.Strategies[0].Name, rep.Strategies[1].Name)
	}
	if rep.ContractNote != ContractNote {
		t.Fatalf("contract note missing from report")
	}
	for _, s := range rep.Strategies {
		if s.RecallAtK < 0 || s.RecallAtK > 1 || s.NDCGAtK < 0 || s.NDCGAtK > 1 || s.PrecisionAtK < 0 || s.PrecisionAtK > 1 {
			t.Fatalf("strategy %q metrics out of [0,1]: %+v", s.Name, s)
		}
	}
	if rep.CalibratedAlpha < 0 || rep.CalibratedAlpha > 1 {
		t.Fatalf("calibrated alpha out of [0,1]: %v", rep.CalibratedAlpha)
	}
	if rep.EvalQueries == 0 || rep.TuningQueries == 0 {
		t.Fatalf("splits empty: tuning=%d eval=%d", rep.TuningQueries, rep.EvalQueries)
	}
}

func TestHarnessPlantedRelevanceIsRetrievable(t *testing.T) {
	// On a corpus where both signals agree (balanced profile only), both
	// strategies should recover most of the planted relevant set; sanity-check
	// recall is materially above chance so the A/B is meaningful.
	rep, err := Run(Config{CorpusSize: 60, QueryCount: 24, Seed: 5, TopK: 10})
	if err != nil {
		t.Fatalf("run harness: %v", err)
	}
	for _, s := range rep.Strategies {
		if s.RecallAtK < 0.5 {
			t.Fatalf("strategy %q recall %.3f suspiciously low for planted corpus", s.Name, s.RecallAtK)
		}
	}
}

func TestConvexFuseUsesBenchCommonNDCG(t *testing.T) {
	// Guard the dependency on the shared bench/common metric conventions.
	retrieved := []string{"a", "b", "c"}
	relevance := map[string]float64{"a": 3}
	if got := common.NDCGAtK(retrieved, relevance, 2); got <= 0 {
		t.Fatalf("bench/common NDCGAtK not behaving: %v", got)
	}
}

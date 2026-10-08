package mrl

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
)

func TestFloat16KnownBitPatterns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		f    float32
		want uint16
	}{
		{0, 0x0000},
		{float32(math.Copysign(0, -1)), 0x8000},
		{1, 0x3C00},
		{-1, 0xBC00},
		{2, 0x4000},
		{-2, 0xC000},
		{0.5, 0x3800},
		{65504, 0x7BFF},        // max half normal
		{65520, 0x7C00},        // rounds up to half infinity
		{6.1035156e-5, 0x0400}, // min half normal 2^-14
		{7.6293945e-6, 0x0080}, // half subnormal 128 * 2^-24
		{5.9604645e-8, 0x0001}, // min half subnormal 2^-24
		{2.9802322e-8, 0x0000}, // below half subnormal -> zero
		{float32(math.Inf(1)), 0x7C00},
		{float32(math.Inf(-1)), 0xFC00},
		{float32(math.NaN()), 0x7E00},
	}
	for _, tc := range cases {
		if got := Float16bits(tc.f); got != tc.want {
			t.Errorf("Float16bits(%v) = %#04x, want %#04x", tc.f, got, tc.want)
		}
	}
}

func TestFloat16RoundTrip(t *testing.T) {
	t.Parallel()
	// Every binary16 bit pattern must convert back to the exact float32 value
	// that pattern represents; and re-quantizing a roundtripped float32 must
	// be the identity (halfvec storage semantics).
	for bits := uint32(0); bits <= 0xFFFF; bits += 1 {
		h := uint16(bits)
		f := HalfFloat(h)
		if math.IsNaN(float64(f)) {
			continue
		}
		if got := Float16bits(f); got != h {
			t.Fatalf("round trip failed for %#04x: -> %v -> %#04x", h, f, got)
		}
	}
}

func TestFloat16RoundingMatchesExpectedPrecision(t *testing.T) {
	t.Parallel()
	// halfvec distance math sees values quantized to ~11 bits of mantissa.
	f := float32(0.123456789)
	q := HalfFloat(Float16bits(f))
	if q == f {
		t.Fatalf("expected quantization loss for %v", f)
	}
	if rel := math.Abs(float64(q-f)) / math.Abs(float64(f)); rel > 0.001 {
		t.Fatalf("relative quantization error %v exceeds half precision bound", rel)
	}
}

func TestCorpusDeterministicAndUnitNorm(t *testing.T) {
	t.Parallel()
	cfg := Config{CorpusSize: 60, RandomQueries: 4, HardQueries: 5, DistractorsPerAnchor: 4, LatencyPasses: 1}

	h1 := newHarness(cfg.normalized())
	h2 := newHarness(cfg.normalized())

	if len(h1.corpusF32) != cfg.CorpusSize || len(h1.queries) != cfg.RandomQueries+cfg.HardQueries {
		t.Fatalf("unexpected harness shape: corpus=%d queries=%d", len(h1.corpusF32), len(h1.queries))
	}
	for i := range h1.corpusF32 {
		for j := range h1.corpusF32[i] {
			if h1.corpusF32[i][j] != h2.corpusF32[i][j] {
				t.Fatalf("corpus not deterministic at doc %d dim %d", i, j)
			}
		}
		if got, want := len(h1.corpusF32[i]), DefaultDims; got != want {
			t.Fatalf("doc %d has %d dims, want %d", i, got, want)
		}
		var sum float64
		for _, x := range h1.corpusF32[i] {
			sum += float64(x) * float64(x)
		}
		if math.Abs(math.Sqrt(sum)-1) > 1e-5 {
			t.Fatalf("doc %d is not unit norm: %v", i, math.Sqrt(sum))
		}
	}
	for _, q := range h1.queries {
		var sum float64
		for _, x := range q.vec {
			sum += float64(x) * float64(x)
		}
		if math.Abs(math.Sqrt(sum)-1) > 1e-5 {
			t.Fatalf("query %s is not unit norm: %v", q.id, math.Sqrt(sum))
		}
	}
}

func TestTruncationLossPresentInSyntheticMRLData(t *testing.T) {
	t.Parallel()
	// Gate integrity: the 2048 prefix must NOT be a perfect proxy for the full
	// vector, otherwise the harness trivially passes. The suffix carries
	// independent signal, so prefix-cosine and full-cosine must differ.
	rng := rand.New(rand.NewSource(DefaultSeed))
	half := DefaultDims / 2
	for i := 0; i < 50; i++ {
		a := generateMRLVector(rng, DefaultDims)
		b := generateMRLVector(rng, DefaultDims)
		full := cosineDistance(a, b)
		prefix := cosineDistance(a[:half], b[:half])
		if math.Abs(full-prefix) < 1e-6 {
			t.Fatalf("pair %d: prefix cosine is a perfect proxy (full=%v prefix=%v)", i, full, prefix)
		}
		if prefix < 0 || prefix > 2 {
			t.Fatalf("pair %d: prefix cosine out of range: %v", i, prefix)
		}
	}
}

func TestBaselineSelfRecallIsOne(t *testing.T) {
	t.Parallel()
	cfg := Config{CorpusSize: 80, RandomQueries: 6, HardQueries: 5, DistractorsPerAnchor: 4, LatencyPasses: 1}
	rep, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Baseline.RecallAt10Combined != 1.0 {
		t.Fatalf("baseline recall vs itself = %v, want exactly 1.0", rep.Baseline.RecallAt10Combined)
	}
	if rep.Baseline.RecallAt10Hard != 1.0 || rep.Baseline.RecallAt10Random != 1.0 {
		t.Fatalf("baseline per-set recall not 1.0: random=%v hard=%v", rep.Baseline.RecallAt10Random, rep.Baseline.RecallAt10Hard)
	}
}

func TestRerankRecallAtLeastDirect(t *testing.T) {
	t.Parallel()
	// Full-precision re-rank over a superset shortlist can only improve (or
	// tie) the direct subvector ranking relative to the exact baseline.
	cfg := Config{CorpusSize: 150, RandomQueries: 10, HardQueries: 8, DistractorsPerAnchor: 6, LatencyPasses: 1}
	rep, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.SubvectorRerank.RecallAt10Combined+1e-9 < rep.SubvectorDirect.RecallAt10Combined {
		t.Fatalf("rerank recall %.4f < direct recall %.4f; harness invariant broken",
			rep.SubvectorRerank.RecallAt10Combined, rep.SubvectorDirect.RecallAt10Combined)
	}
}

func TestRunDeterministicRecallAndVerdict(t *testing.T) {
	t.Parallel()
	cfg := Config{CorpusSize: 120, RandomQueries: 8, HardQueries: 6, DistractorsPerAnchor: 5, LatencyPasses: 1}
	rep1, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rep2, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep1.Verdict != rep2.Verdict {
		t.Fatalf("verdict not deterministic: %s vs %s", rep1.Verdict, rep2.Verdict)
	}
	if rep1.SubvectorRerank.RecallAt10Combined != rep2.SubvectorRerank.RecallAt10Combined ||
		rep1.SubvectorDirect.RecallAt10Combined != rep2.SubvectorDirect.RecallAt10Combined {
		t.Fatalf("recall fields not deterministic: %+v vs %+v", rep1.SubvectorRerank, rep2.SubvectorRerank)
	}
}

func TestDecideGateLogic(t *testing.T) {
	t.Parallel()
	passRerank := StrategyReport{Name: "rerank", RecallAt10Combined: 0.97, RecallAt10Hard: 0.93}
	failRecall := StrategyReport{Name: "rerank", RecallAt10Combined: 0.90, RecallAt10Hard: 0.85}
	hardCollapse := StrategyReport{Name: "rerank", RecallAt10Combined: 0.96, RecallAt10Hard: 0.80}
	direct := StrategyReport{Name: "direct", RecallAt10Combined: 0.5}
	control := StrategyReport{Name: "control", RecallAt10Combined: 0.99}
	baseline := StrategyReport{Name: "baseline"}
	fastProj := LatencyProjection{Speedup: 50}
	slowProj := LatencyProjection{Speedup: 1.5}

	tests := []struct {
		name         string
		rerank       StrategyReport
		proj         LatencyProjection
		wantVerdict  string
		wantRecallOK bool
		wantLatOK    bool
	}{
		{"both gates pass", passRerank, fastProj, VerdictGO, true, true},
		{"recall fails", failRecall, fastProj, VerdictNoGo, false, true},
		{"hard floor fails", hardCollapse, fastProj, VerdictNoGo, false, true},
		{"latency fails", passRerank, slowProj, VerdictNoGo, true, false},
		{"both fail", failRecall, slowProj, VerdictNoGo, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recallPass, latPass, verdict, rationale := decide(baseline, tc.rerank, direct, control, tc.proj)
			if verdict != tc.wantVerdict || recallPass != tc.wantRecallOK || latPass != tc.wantLatOK {
				t.Fatalf("decide() = (%v, %v, %s), want (%v, %v, %s)", recallPass, latPass, verdict, tc.wantRecallOK, tc.wantLatOK, tc.wantVerdict)
			}
			if rationale == "" {
				t.Fatal("rationale must be populated")
			}
			if tc.wantVerdict == VerdictNoGo && !strings.Contains(rationale, "ret-206") {
				t.Fatalf("NO-GO rationale must route to ret-206, got: %s", rationale)
			}
		})
	}
}

func TestReportEmbedsCaveatDDLAndContract(t *testing.T) {
	t.Parallel()
	rep, err := Run(Config{CorpusSize: 60, RandomQueries: 4, HardQueries: 5, DistractorsPerAnchor: 4, LatencyPasses: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.SyntheticDataCaveat == "" || !strings.Contains(rep.SyntheticDataCaveat, "synthetic") {
		t.Fatal("report must embed the synthetic-data caveat")
	}
	for _, marker := range []string{"USING hnsw", "halfvec(2048)", "CONCURRENTLY", "cortex_vector.embeddings", "DROP INDEX CONCURRENTLY"} {
		if !strings.Contains(rep.DDLOutlook, marker) {
			t.Fatalf("DDLOutlook missing %q", marker)
		}
	}
	if !strings.Contains(rep.ContractNote, "REQ-RET-103") || !strings.Contains(rep.ContractNote, "ret-206") {
		t.Fatal("contract note must name the gate boundary")
	}
	// JSON round trip: the artifact is the machine-readable contract.
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Report
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Verdict != rep.Verdict {
		t.Fatal("verdict lost in JSON round trip")
	}
}

func TestRunRejectsDegenerateConfigs(t *testing.T) {
	t.Parallel()
	if _, err := Run(Config{RandomQueries: 0, HardQueries: 0}); err == nil {
		t.Fatal("expected error for empty query set")
	}
	if _, err := Run(Config{CorpusSize: 50, RandomQueries: 2, HardQueries: 2, DistractorsPerAnchor: 2, TopK: 10, ShortlistK: 5}); err == nil {
		t.Fatal("expected error when top-k exceeds shortlist")
	}
}

func TestProjectionMathAnchoredOnMeasuredCosts(t *testing.T) {
	t.Parallel()
	cfg := Config{CorpusSize: 100, RandomQueries: 4, HardQueries: 4, DistractorsPerAnchor: 4, LatencyPasses: 1}
	rep, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	proj := rep.Projection
	if proj.CorpusRows != ProjectionCorpusRows || proj.EFSearch != ProjectionEFSearch {
		t.Fatalf("unexpected projection constants: %+v", proj)
	}
	if proj.TExactDistanceNS <= 0 || proj.THalfDistanceNS <= 0 {
		t.Fatalf("measured per-distance costs must be positive: %+v", proj)
	}
	wantExact := float64(ProjectionCorpusRows) * proj.TExactDistanceNS
	if math.Abs(proj.ProjectedExactNS-wantExact) > wantExact*1e-9 {
		t.Fatalf("projected exact scan = %v, want %v", proj.ProjectedExactNS, wantExact)
	}
	wantANN := float64(ProjectionEFSearch)*ProjectionGraphHopFactor*proj.THalfDistanceNS + float64(rep.Config.ShortlistK)*proj.TExactDistanceNS
	if math.Abs(proj.ProjectedANNNS-wantANN) > wantANN*1e-9 {
		t.Fatalf("projected ANN cost = %v, want %v", proj.ProjectedANNNS, wantANN)
	}
	wantSpeedup := wantExact / wantANN
	if math.Abs(proj.Speedup-wantSpeedup) > wantSpeedup*1e-9 {
		t.Fatalf("speedup = %v, want %v", proj.Speedup, wantSpeedup)
	}
	// The half-dimension scan must measure cheaper per element than the full
	// scan; otherwise the halfvec representation is not paying its way even
	// before HNSW sublinearity.
	if proj.THalfDistanceNS > proj.TExactDistanceNS {
		t.Fatalf("halfvec per-element cost %.2f exceeds full-precision %.2f", proj.THalfDistanceNS, proj.TExactDistanceNS)
	}
}

func TestDefaultConfigSatisfiesGateThresholds(t *testing.T) {
	// Documentation-by-test: the stated GO criteria in the report contract.
	if GoRecallThreshold != 0.95 || GoHardRecallFloor != 0.90 || GoLatencySpeedup != 3.0 {
		t.Fatalf("gate thresholds changed: %.2f/%.2f/%.0f — update the report contract and planning review",
			GoRecallThreshold, GoHardRecallFloor, GoLatencySpeedup)
	}
	if DefaultShortlistK != 50 || DefaultTopK != 10 || DefaultDims != 4096 {
		t.Fatalf("harness defaults drifted from the designed production pattern: K=%d topk=%d dims=%d",
			DefaultShortlistK, DefaultTopK, DefaultDims)
	}
	if fmt.Sprint(DefaultSeed) == "" {
		t.Fatal("unreachable")
	}
}

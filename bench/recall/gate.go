// Package recall implements the P8 recall regression gate (issue #162).
//
// It measures recall@k / nDCG@k of the CURRENT production fusion semantics —
// position-only Reciprocal Rank Fusion (RRF, k=60) as mirrored by
// bench/fusion.RRFFuse from internal/retrieval — over the golden
// deterministic corpus (fixed seed, pure Go), and compares the measurements
// against pinned thresholds loaded from a checked-in JSON.
//
// DETERMINISM CONTRACT: no network, no live embedding provider, no wall-clock
// influence on the verdict (fusion latency is excluded), fixed corpus seed,
// and stable sorting inside the harness. The same binary on any platform must
// measure byte-identical metrics; a drift means a fusion/ranking semantics
// change, which is exactly what the gate exists to surface.
//
// The gate is ADVISORY in CI first (non-required "Recall Gate (Advisory)"
// job). Promotion path to required: (1) let the advisory job run for a few
// weeks across model/provider changes; (2) confirm thresholds still leave
// headroom over the measured values; (3) flip the job to required in
// .github/workflows/ci.yml and note it in docs/BENCHMARKS.md.
package recall

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/lleontor705/cortex/v2/bench/fusion"
)

// SchemaVersion identifies the gate's threshold/report contract.
const SchemaVersion = "1"

// RRFStrategyName is the production fusion strategy measured by the gate.
const RRFStrategyName = "rrf-k60"

// gateConfig pins the corpus parameters. Changing ANY of these changes the
// measured numbers and therefore requires a reviewed thresholds update in the
// same PR — that coupling is intentional.
var gateConfig = fusion.Config{
	CorpusSize:     200,
	QueryCount:     60,
	TopK:           fusion.DefaultTopK,
	Seed:           fusion.DefaultSeed,
	TuningFraction: fusion.DefaultTuningFraction,
	AlphaGridStep:  fusion.DefaultAlphaGridStep,
}

// Thresholds is the checked-in gate contract (bench/recall/thresholds.json).
type Thresholds struct {
	SchemaVersion string  `json:"schema_version"`
	Fusion        string  `json:"fusion"`
	Seed          int64   `json:"seed"`
	TopK          int     `json:"top_k"`
	CorpusSize    int     `json:"corpus_size"`
	QueryCount    int     `json:"query_count"`
	MinRecallAtK  float64 `json:"min_recall_at_k"`
	MinNDCGAtK    float64 `json:"min_ndcg_at_k"`
	Note          string  `json:"note"`
}

// Result is the gate verdict for one run.
type Result struct {
	MeasuredRecallAtK float64 `json:"measured_recall_at_k"`
	MeasuredNDCGAtK   float64 `json:"measured_ndcg_at_k"`
	Passed            bool    `json:"passed"`
	Reasons           []string `json:"reasons,omitempty"`
}

// DefaultThresholds returns the gate contract embedded in code. It MUST stay
// in sync with bench/recall/thresholds.json (a test enforces equality); the
// duplicate exists so the gate CLI works from any working directory without
// depending on file resolution.
func DefaultThresholds() Thresholds {
	return Thresholds{
		SchemaVersion: SchemaVersion,
		Fusion:        RRFStrategyName,
		Seed:          gateConfig.Seed,
		TopK:          gateConfig.TopK,
		CorpusSize:    gateConfig.CorpusSize,
		QueryCount:    gateConfig.QueryCount,
		// Headroom below the measured golden values (recall@10 0.6022,
		// nDCG@10 0.5140): small enough to catch real degradations, wide
		// enough to tolerate int/float platform noise (none expected).
		MinRecallAtK: 0.58,
		MinNDCGAtK:   0.48,
		Note:         "Pinned from the golden corpus run committed at bench/recall/golden-report.json. Update in the same PR as any fusion/corpus change.",
	}
}

// LoadThresholds reads and validates a thresholds JSON file.
func LoadThresholds(path string) (Thresholds, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Thresholds{}, fmt.Errorf("recall gate: read thresholds: %w", err)
	}
	var th Thresholds
	if err := json.Unmarshal(data, &th); err != nil {
		return Thresholds{}, fmt.Errorf("recall gate: parse thresholds: %w", err)
	}
	if err := th.validate(); err != nil {
		return Thresholds{}, err
	}
	return th, nil
}

func (t Thresholds) validate() error {
	if t.SchemaVersion != SchemaVersion {
		return fmt.Errorf("recall gate: thresholds schema %q, want %q", t.SchemaVersion, SchemaVersion)
	}
	if t.Fusion != RRFStrategyName {
		return fmt.Errorf("recall gate: thresholds pin fusion %q, want %q", t.Fusion, RRFStrategyName)
	}
	if t.Seed != gateConfig.Seed {
		return fmt.Errorf("recall gate: thresholds seed %d does not match the golden corpus seed %d — update thresholds together with the corpus", t.Seed, gateConfig.Seed)
	}
	if t.TopK != gateConfig.TopK {
		return fmt.Errorf("recall gate: thresholds top_k %d does not match the golden corpus top_k %d", t.TopK, gateConfig.TopK)
	}
	if t.MinRecallAtK <= 0 || t.MinRecallAtK > 1 {
		return fmt.Errorf("recall gate: min_recall_at_k %v out of (0,1]", t.MinRecallAtK)
	}
	if t.MinNDCGAtK <= 0 || t.MinNDCGAtK > 1 {
		return fmt.Errorf("recall gate: min_ndcg_at_k %v out of (0,1]", t.MinNDCGAtK)
	}
	return nil
}

// Measure runs the fusion harness over the golden corpus and returns the
// production RRF strategy report plus the full advisory report (useful for
// the committed golden artifact).
func Measure() (fusion.StrategyReport, fusion.Report, error) {
	rep, err := fusion.Run(gateConfig)
	if err != nil {
		return fusion.StrategyReport{}, fusion.Report{}, fmt.Errorf("recall gate: fusion harness: %w", err)
	}
	for _, s := range rep.Strategies {
		if s.Name == RRFStrategyName {
			return s, rep, nil
		}
	}
	return fusion.StrategyReport{}, rep, errors.New("recall gate: fusion report missing the production rrf-k60 strategy")
}

// Gate compares the measured production-fusion metrics against thresholds.
func Gate(th Thresholds) (Result, error) {
	if err := th.validate(); err != nil {
		return Result{}, err
	}
	measured, _, err := Measure()
	if err != nil {
		return Result{}, err
	}
	res := Result{
		MeasuredRecallAtK: measured.RecallAtK,
		MeasuredNDCGAtK:   measured.NDCGAtK,
	}
	if measured.RecallAtK < th.MinRecallAtK {
		res.Reasons = append(res.Reasons, fmt.Sprintf(
			"recall@%d %.4f below pinned floor %.4f — a fusion/ranking semantics change degraded retrieval quality",
			th.TopK, measured.RecallAtK, th.MinRecallAtK))
	}
	if measured.NDCGAtK < th.MinNDCGAtK {
		res.Reasons = append(res.Reasons, fmt.Sprintf(
			"nDCG@%d %.4f below pinned floor %.4f — a fusion/ranking semantics change degraded ranking quality",
			th.TopK, measured.NDCGAtK, th.MinNDCGAtK))
	}
	res.Passed = len(res.Reasons) == 0
	return res, nil
}

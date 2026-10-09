package recall

// Tests for the recall regression gate. The golden corpus is deterministic
// (fixed seed, pure Go), so the measured values here must equal the committed
// golden report byte-for-byte on every platform — that equality IS the
// regression contract.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/bench/fusion"
)

// TestGoldenReportMatchesMeasurement is the core determinism assertion: the
// committed golden artifact must be exactly what the current code measures.
// Any diff means a fusion/ranking semantics change and a conscious golden
// update is required in the same PR.
func TestGoldenReportMatchesMeasurement(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("golden-report.json"))
	if err != nil {
		t.Fatalf("read committed golden report: %v", err)
	}
	var golden fusion.Report
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatalf("parse committed golden report: %v", err)
	}

	measured, _, err := Measure()
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	if golden.SchemaVersion != fusion.SchemaVersion {
		t.Fatalf("golden schema %q, want %q", golden.SchemaVersion, fusion.SchemaVersion)
	}
	for _, s := range golden.Strategies {
		if s.Name != RRFStrategyName {
			continue
		}
		if diff := s.RecallAtK - measured.RecallAtK; diff > 1e-9 || diff < -1e-9 {
			t.Fatalf("golden recall@k %.6f != measured %.6f — fusion semantics changed; re-pin the golden artifact consciously", s.RecallAtK, measured.RecallAtK)
		}
		if diff := s.NDCGAtK - measured.NDCGAtK; diff > 1e-9 || diff < -1e-9 {
			t.Fatalf("golden nDCG@k %.6f != measured %.6f — ranking semantics changed; re-pin the golden artifact consciously", s.NDCGAtK, measured.NDCGAtK)
		}
		return
	}
	t.Fatal("committed golden report missing the rrf-k60 strategy")
}

func TestGatePassesOnPinnedThresholds(t *testing.T) {
	res, err := Gate(DefaultThresholds())
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !res.Passed {
		t.Fatalf("gate must PASS on the pinned thresholds; reasons: %v", res.Reasons)
	}
	if res.MeasuredRecallAtK < DefaultThresholds().MinRecallAtK || res.MeasuredNDCGAtK < DefaultThresholds().MinNDCGAtK {
		t.Fatalf("pass verdict inconsistent with measurements: %+v", res)
	}
}

func TestGateDetectsRegression(t *testing.T) {
	th := DefaultThresholds()
	th.MinRecallAtK = 0.99 // impossible floor → forced regression
	res, err := Gate(th)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if res.Passed {
		t.Fatal("gate must FAIL with an impossible recall floor")
	}
	if len(res.Reasons) != 1 || !strings.Contains(res.Reasons[0], "recall@10") {
		t.Fatalf("reasons must name recall: %v", res.Reasons)
	}

	th = DefaultThresholds()
	th.MinNDCGAtK = 0.99
	res, err = Gate(th)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if res.Passed || len(res.Reasons) != 1 || !strings.Contains(res.Reasons[0], "nDCG@10") {
		t.Fatalf("nDCG regression not surfaced: %+v", res)
	}
}

func TestThresholdsFileMatchesEmbedded(t *testing.T) {
	th, err := LoadThresholds(filepath.Join("thresholds.json"))
	if err != nil {
		t.Fatalf("load checked-in thresholds: %v", err)
	}
	if th != DefaultThresholds() {
		t.Fatalf("checked-in thresholds.json drifted from the embedded contract:\nfile: %+v\nembedded: %+v", th, DefaultThresholds())
	}
}

func TestValidateRejectsCorpusDrift(t *testing.T) {
	th := DefaultThresholds()
	th.Seed = 12345 // corpus drift without a thresholds update must refuse
	if _, err := LoadThresholdsFrom(t, th); err == nil {
		t.Fatal("seed drift must be rejected")
	}

	th = DefaultThresholds()
	th.Fusion = "convex-combination"
	if _, err := LoadThresholdsFrom(t, th); err == nil {
		t.Fatal("gate must measure the production rrf-k60 strategy only")
	}

	th = DefaultThresholds()
	th.MinRecallAtK = 1.5
	if _, err := LoadThresholdsFrom(t, th); err == nil {
		t.Fatal("out-of-range floor must be rejected")
	}
}

// LoadThresholdsFrom validates an in-memory threshold set through the same
// path as LoadThresholds (serialize → parse → validate).
func LoadThresholdsFrom(t *testing.T, th Thresholds) (Thresholds, error) {
	t.Helper()
	data, err := json.Marshal(th)
	if err != nil {
		return Thresholds{}, err
	}
	tmp := filepath.Join(t.TempDir(), "thresholds.json")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return Thresholds{}, err
	}
	return LoadThresholds(tmp)
}

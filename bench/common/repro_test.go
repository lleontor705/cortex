package common

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestReproRejectsMismatchedIdentity(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*IndependentRun)
		wantErr string
	}{
		{name: "seed", mutate: func(run *IndependentRun) { run.Seed = "seed-2" }, wantErr: "seed"},
		{name: "build commit", mutate: func(run *IndependentRun) { run.Report.Build.Commit = "different" }, wantErr: "build"},
		{name: "corpus", mutate: func(run *IndependentRun) { run.Report.CorpusVersion = "corpus-v2" }, wantErr: "corpus"},
		{name: "hardware", mutate: func(run *IndependentRun) { run.Report.Hardware.CPU = "different-cpu" }, wantErr: "hardware"},
		{name: "protocol", mutate: func(run *IndependentRun) { run.Report.ProtocolVersion = "protocol-v2" }, wantErr: "protocol"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runs := []IndependentRun{reproRun(t, "run-1"), reproRun(t, "run-2")}
			tt.mutate(&runs[1])

			_, err := AnalyzeReproducibility(runs, approvedReproProtocol(0.25))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("AnalyzeReproducibility() error = %v, want identity error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestReproRejectsDirtyBuild(t *testing.T) {
	runs := []IndependentRun{reproRun(t, "run-1"), reproRun(t, "run-2")}
	runs[0].Report.Build.Dirty = true

	_, err := AnalyzeReproducibility(runs, approvedReproProtocol(0.25))
	if err == nil || !strings.Contains(err.Error(), "clean committed build") {
		t.Fatalf("AnalyzeReproducibility() error = %v, want clean-build rejection", err)
	}
}

func TestReproDeterministicComparisonPreservesIndependentRuns(t *testing.T) {
	runs := []IndependentRun{reproRun(t, "run-1"), reproRun(t, "run-2")}

	analysis, err := AnalyzeReproducibility(runs, approvedReproProtocol(0.25))
	if err != nil {
		t.Fatalf("AnalyzeReproducibility() error = %v", err)
	}
	if !analysis.DeterministicMatch || len(analysis.DeterministicDifferences) != 0 {
		t.Fatalf("deterministic comparison = (%v, %v), want exact match", analysis.DeterministicMatch, analysis.DeterministicDifferences)
	}
	if len(analysis.Runs) != 2 || analysis.Runs[0].RunID != "run-1" || analysis.Runs[1].RunID != "run-2" {
		t.Fatalf("preserved runs = %#v, want independent run evidence", analysis.Runs)
	}

	runs[1].Report.Queries[0].CurrentOutput[0].StableID = "different-result"
	analysis, err = AnalyzeReproducibility(runs, approvedReproProtocol(0.25))
	if err != nil {
		t.Fatalf("AnalyzeReproducibility(mismatch) error = %v", err)
	}
	if analysis.DeterministicMatch || len(analysis.DeterministicDifferences) == 0 {
		t.Fatalf("deterministic comparison = (%v, %v), want disclosed mismatch", analysis.DeterministicMatch, analysis.DeterministicDifferences)
	}
}

func TestVarianceReportsApprovedDispersionOutliersAndTolerance(t *testing.T) {
	runs := []IndependentRun{reproRun(t, "run-1"), reproRun(t, "run-2")}
	metricKey := "lexical-fast@1.0.0/single-hop/recall_at_10"
	runs[0].Report.Profiles[0].Metrics["recall_at_10"] = 0.8
	runs[1].Report.Profiles[0].Metrics["recall_at_10"] = 1.0
	runs[1].Outliers = []OutlierDisclosure{{MetricKey: metricKey, Reason: "scheduled antivirus scan overlapped the run"}}

	analysis, err := AnalyzeReproducibility(runs, approvedReproProtocol(0.25))
	if err != nil {
		t.Fatalf("AnalyzeReproducibility() error = %v", err)
	}
	variance, ok := findVariance(analysis.Variance, metricKey)
	if !ok {
		t.Fatalf("variance missing metric %q: %#v", metricKey, analysis.Variance)
	}
	if variance.SampleSize != 2 || math.Abs(variance.Mean-0.9) > 1e-12 || math.Abs(variance.Range-0.2) > 1e-12 || math.Abs(variance.SampleStandardDeviation-math.Sqrt(0.02)) > 1e-12 {
		t.Fatalf("variance = %#v, want sample size 2, mean .9, range .2, sample stddev sqrt(.02)", variance)
	}
	if variance.DispersionMethod != "sample_standard_deviation" || variance.DispersionApprovedBy != "retrieval-reviewers" {
		t.Fatalf("dispersion registration = %#v, want approved method", variance)
	}
	if len(analysis.Outliers) != 1 || analysis.Outliers[0].RunID != "run-2" || analysis.Outliers[0].Reason == "" {
		t.Fatalf("outliers = %#v, want preserved run-specific disclosure", analysis.Outliers)
	}
	evaluation, ok := findTolerance(analysis.ToleranceEvaluations, metricKey)
	if !ok || !evaluation.Passed || evaluation.ObservedRange != variance.Range || evaluation.RegisteredMaximum != 0.25 {
		t.Fatalf("tolerance evaluation = %#v, found %v, want preregistered passing range", evaluation, ok)
	}
}

func TestVarianceRequiresApprovedPreregisteredPolicy(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ReproProtocol)
		wantErr string
	}{
		{name: "protocol version", mutate: func(p *ReproProtocol) { p.Version = "" }, wantErr: "protocol version"},
		{name: "dispersion method", mutate: func(p *ReproProtocol) { p.DispersionMethod = "" }, wantErr: "dispersion method"},
		{name: "reviewer approval", mutate: func(p *ReproProtocol) { p.ApprovedBy = "" }, wantErr: "approved_by"},
		{name: "tolerance approval", mutate: func(p *ReproProtocol) { p.Tolerances[0].ApprovedBy = "" }, wantErr: "tolerance"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protocol := approvedReproProtocol(0.25)
			tt.mutate(&protocol)
			_, err := AnalyzeReproducibility([]IndependentRun{reproRun(t, "run-1"), reproRun(t, "run-2")}, protocol)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("AnalyzeReproducibility() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func reproRun(t *testing.T, runID string) IndependentRun {
	t.Helper()
	report := cloneEvidenceReport(t, validEvidenceReport())
	report.ReportID = runID
	return IndependentRun{RunID: runID, Seed: "seed-1", Report: report}
}

func cloneEvidenceReport(t *testing.T, report EvidenceReport) EvidenceReport {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report clone: %v", err)
	}
	var clone EvidenceReport
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatalf("unmarshal report clone: %v", err)
	}
	return clone
}

func approvedReproProtocol(maxRange float64) ReproProtocol {
	return ReproProtocol{
		Version:          "repro-protocol-v1",
		DispersionMethod: "sample_standard_deviation",
		ApprovedBy:       "retrieval-reviewers",
		Tolerances: []MetricTolerance{{
			MetricKey:  metricKey("lexical-fast", "1.0.0", "single-hop", "recall_at_10"),
			MaxRange:   maxRange,
			ApprovedBy: "retrieval-reviewers",
		}},
	}
}

func findVariance(values []MetricVariance, key string) (MetricVariance, bool) {
	for _, value := range values {
		if value.MetricKey == key {
			return value, true
		}
	}
	return MetricVariance{}, false
}

func findTolerance(values []ToleranceEvaluation, key string) (ToleranceEvaluation, bool) {
	for _, value := range values {
		if value.MetricKey == key {
			return value, true
		}
	}
	return ToleranceEvaluation{}, false
}

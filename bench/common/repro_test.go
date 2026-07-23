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
		{name: "binary", mutate: func(run *IndependentRun) { run.BinarySHA256 = strings.Repeat("b", 64) }, wantErr: "binary"},
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
	if len(variance.Samples) != 2 || variance.Samples[0] != 0.8 || variance.Samples[1] != 1.0 {
		t.Fatalf("variance samples = %#v, want every observed sample retained in run order", variance.Samples)
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

func TestVarianceIncludesPerformanceResourceAndAllocationSeries(t *testing.T) {
	runs := []IndependentRun{reproRun(t, "run-1"), reproRun(t, "run-2")}
	runs[1].Report.Profiles[0].Latency = LatencyReport{Unit: "milliseconds", P50: 5.5, P95: 10, P99: 13}
	runs[1].Report.Profiles[0].Throughput.QueriesPerSecond = 180
	runs[1].Report.Resources.CPUSeconds = 1.5
	runs[1].Report.Resources.PeakRSSBytes = 70 << 20
	runs[1].Report.Resources.StorageBytes = 2 << 20
	runs[1].Report.Resources.IndexBytes = 512 << 10
	runs[1].HeapAllocBytes = 2 << 20
	runs[1].TotalAllocBytes = 4 << 20

	analysis, err := AnalyzeReproducibility(runs, approvedReproProtocol(0.25))
	if err != nil {
		t.Fatalf("AnalyzeReproducibility() error = %v", err)
	}

	for _, key := range []string{
		"lexical-fast@1.0.0/single-hop/latency_p50_milliseconds",
		"lexical-fast@1.0.0/single-hop/latency_p95_milliseconds",
		"lexical-fast@1.0.0/single-hop/latency_p99_milliseconds",
		"lexical-fast@1.0.0/single-hop/throughput_queries_per_second",
		"resources/cpu_seconds",
		"resources/peak_rss_bytes",
		"resources/storage_bytes",
		"resources/index_bytes",
		"resources/heap_alloc_bytes",
		"resources/total_alloc_bytes",
	} {
		variance, ok := findVariance(analysis.Variance, key)
		if !ok || variance.SampleSize != 2 || len(variance.Samples) != 2 {
			t.Errorf("variance[%q] = %#v, found %v; want two retained samples", key, variance, ok)
		}
	}
}

func TestVarianceRejectsAbsentResourceSamples(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*IndependentRun)
		want   string
	}{
		{name: "cpu", mutate: func(run *IndependentRun) { run.Report.Resources.CPUAvailable = boolPointer(false) }, want: "cpu"},
		{name: "peak rss", mutate: func(run *IndependentRun) { run.Report.Resources.PeakRSSAvailable = boolPointer(false) }, want: "peak RSS"},
		{name: "storage", mutate: func(run *IndependentRun) { run.Report.Resources.StorageAvailable = boolPointer(false) }, want: "storage"},
		{name: "index", mutate: func(run *IndependentRun) { run.Report.Resources.IndexAvailable = boolPointer(false) }, want: "index"},
		{name: "allocations", mutate: func(run *IndependentRun) { run.AllocationsAvailable = false }, want: "allocation"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runs := []IndependentRun{reproRun(t, "run-1"), reproRun(t, "run-2")}
			tt.mutate(&runs[1])
			_, err := AnalyzeReproducibility(runs, approvedReproProtocol(0.25))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("AnalyzeReproducibility() error = %v, want absent %s sample rejection", err, tt.want)
			}
		})
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
	return IndependentRun{
		RunID:                runID,
		Seed:                 "seed-1",
		BinarySHA256:         strings.Repeat("a", 64),
		Report:               report,
		HeapAllocBytes:       1 << 20,
		TotalAllocBytes:      2 << 20,
		AllocationsAvailable: true,
	}
}

func boolPointer(value bool) *bool { return &value }

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

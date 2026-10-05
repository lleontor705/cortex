package common

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReportDeterministicSerialization(t *testing.T) {
	report := validEvidenceReport()

	first, err := SerializeEvidenceReport(report)
	if err != nil {
		t.Fatalf("SerializeEvidenceReport() error = %v", err)
	}

	reordered := report
	reordered.MetricDefinitions = reverseMetricDefinitions(report.MetricDefinitions)
	reordered.Profiles = reverseProfileReports(report.Profiles)
	reordered.Queries = reverseQueryReports(report.Queries)
	reordered.Limitations = []string{report.Limitations[1], report.Limitations[0]}
	second, err := SerializeEvidenceReport(reordered)
	if err != nil {
		t.Fatalf("SerializeEvidenceReport(reordered) error = %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Fatalf("serialization is not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if firstRank, secondRank := bytes.Index(first, []byte(`"stable_id": "fact-current"`)), bytes.Index(first, []byte(`"stable_id": "fact-current-second"`)); firstRank < 0 || secondRank < 0 || firstRank > secondRank {
		t.Fatalf("serialization changed observed ranked order:\n%s", first)
	}
	for _, required := range []string{
		`"run_id": "run-001"`,
		`"corpus_version": "corpus-v1"`,
		`"protocol_version": "protocol-v1"`,
		`"p50": 4.5`,
		`"p95": 9`,
		`"p99": 12`,
		`"queries_per_second": 200`,
		`"cpu_seconds": 1.25`,
		`"peak_rss_bytes": 67108864`,
		`"storage_bytes": 1048576`,
		`"index_bytes": 262144`,
		`"cpu_available": true`,
		`"peak_rss_available": true`,
		`"storage_available": true`,
		`"index_available": true`,
		`"current_output"`,
		`"candidate_output"`,
		`"uncertainty"`,
		`"limitations"`,
	} {
		if !strings.Contains(string(first), required) {
			t.Errorf("serialized report missing %s", required)
		}
	}
}

func TestReportValidationRejectsDuplicateIdentity(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*EvidenceReport)
		wantErr string
	}{
		{name: "run and report", mutate: func(r *EvidenceReport) { r.RunID = r.ReportID }, wantErr: "run_id and report_id"},
		{name: "profile", mutate: func(r *EvidenceReport) { r.Profiles = append(r.Profiles, r.Profiles[0]) }, wantErr: "profile identity"},
		{name: "query", mutate: func(r *EvidenceReport) { r.Queries[1].QueryID = r.Queries[0].QueryID }, wantErr: "query identity"},
		{name: "current output stable ID", mutate: func(r *EvidenceReport) {
			r.Queries[0].CurrentOutput[1].StableID = r.Queries[0].CurrentOutput[0].StableID
		}, wantErr: "stable_id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := validEvidenceReport()
			tt.mutate(&report)
			if err := report.Validate(); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestReportValidationPreservesContiguousRankedOrder(t *testing.T) {
	tests := []struct {
		name    string
		outputs []RankedOutput
		wantErr string
	}{
		{name: "contiguous", outputs: []RankedOutput{{StableID: "first", Rank: 1, Score: 0.9}, {StableID: "second", Rank: 2, Score: 0.8}}},
		{name: "gap", outputs: []RankedOutput{{StableID: "first", Rank: 1, Score: 0.9}, {StableID: "third", Rank: 3, Score: 0.8}}, wantErr: "contiguous"},
		{name: "observed order", outputs: []RankedOutput{{StableID: "second", Rank: 2, Score: 0.8}, {StableID: "first", Rank: 1, Score: 0.9}}, wantErr: "contiguous"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := validEvidenceReport()
			report.Queries[0].CurrentOutput = tt.outputs
			err := report.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Validate() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestReportResourceAvailabilityDistinguishesUnavailableFromZero(t *testing.T) {
	unavailable := false
	available := true

	report := validEvidenceReport()
	report.Resources.CPUAvailable = &unavailable
	report.Resources.CPUSeconds = 0
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate() unavailable zero error = %v", err)
	}

	report.Resources.CPUSeconds = 1
	if err := report.Validate(); err == nil || !strings.Contains(err.Error(), "cpu availability") {
		t.Fatalf("Validate() unavailable non-zero error = %v, want cpu availability error", err)
	}

	report.Resources.CPUAvailable = &available
	report.Resources.CPUSeconds = 0
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate() measured zero error = %v", err)
	}
}

func TestReportValidationRequiresReproducibleEvidence(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*EvidenceReport)
		wantErr string
	}{
		{name: "metric definitions", mutate: func(r *EvidenceReport) { r.MetricDefinitions = nil }, wantErr: "metric_definitions"},
		{name: "corpus version", mutate: func(r *EvidenceReport) { r.CorpusVersion = "" }, wantErr: "corpus_version"},
		{name: "build version", mutate: func(r *EvidenceReport) { r.Build.Commit = "" }, wantErr: "build"},
		{name: "hardware version", mutate: func(r *EvidenceReport) { r.Hardware.ProfileID = "" }, wantErr: "hardware"},
		{name: "protocol version", mutate: func(r *EvidenceReport) { r.ProtocolVersion = "" }, wantErr: "protocol_version"},
		{name: "per profile results", mutate: func(r *EvidenceReport) { r.Profiles = nil }, wantErr: "profiles"},
		{name: "per query results", mutate: func(r *EvidenceReport) { r.Queries = nil }, wantErr: "queries"},
		{name: "latency unit", mutate: func(r *EvidenceReport) { r.Profiles[0].Latency.Unit = "" }, wantErr: "latency.unit"},
		{name: "throughput unit", mutate: func(r *EvidenceReport) { r.Profiles[0].Throughput.Unit = "" }, wantErr: "throughput.unit"},
		{name: "resource units", mutate: func(r *EvidenceReport) { r.Resources.PeakRSSUnit = "" }, wantErr: "resources"},
		{name: "current output trace", mutate: func(r *EvidenceReport) { r.Queries[0].CurrentOutput = nil }, wantErr: "current_output"},
		{name: "candidate output trace", mutate: func(r *EvidenceReport) { r.Queries[0].CandidateOutput = nil }, wantErr: "candidate_output"},
		{name: "uncertainty", mutate: func(r *EvidenceReport) { r.Uncertainty.Method = "" }, wantErr: "uncertainty"},
		{name: "limitations", mutate: func(r *EvidenceReport) { r.Limitations = nil }, wantErr: "limitations"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := validEvidenceReport()
			tt.mutate(&report)
			if err := report.Validate(); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func validEvidenceReport() EvidenceReport {
	return EvidenceReport{
		SchemaVersion:   "retrieval-evidence-report/v1",
		RunID:           "run-001",
		ReportID:        "baseline-run-001",
		CorpusVersion:   "corpus-v1",
		ProtocolVersion: "protocol-v1",
		Build:           BuildMetadata{Commit: "0123456789abcdef", Dirty: false},
		Hardware: HardwareMetadata{
			ProfileID: "developer-laptop-v1",
			OS:        "linux",
			Arch:      "amd64",
			CPU:       "example-cpu",
			MemoryMB:  16384,
		},
		MetricDefinitions: []MetricDefinition{
			{Name: "recall_at_10", Unit: "ratio", Direction: "higher_is_better", Description: "Relevant stable-ID recall in the first ten results."},
			{Name: "isolation_violations", Unit: "count", Direction: "lower_is_better", Description: "Returned IDs excluded by authoritative eligibility."},
		},
		Profiles: []ProfileReport{
			{
				ProfileID:      "lexical-fast",
				ProfileVersion: "1.0.0",
				QueryClass:     "single-hop",
				Metrics:        map[string]float64{"recall_at_10": 0.9, "isolation_violations": 0},
				Latency:        LatencyReport{Unit: "milliseconds", P50: 4.5, P95: 9, P99: 12},
				Throughput:     ThroughputReport{Unit: "queries_per_second", QueriesPerSecond: 200},
			},
			{
				ProfileID:      "hybrid-quality",
				ProfileVersion: "1.0.0",
				QueryClass:     "multi-hop",
				Metrics:        map[string]float64{"recall_at_10": 0.8, "isolation_violations": 0},
				Latency:        LatencyReport{Unit: "milliseconds", P50: 8, P95: 16, P99: 22},
				Throughput:     ThroughputReport{Unit: "queries_per_second", QueriesPerSecond: 100},
			},
		},
		Queries: []QueryReport{
			{
				QueryID:        "query-001",
				ProfileID:      "lexical-fast",
				ProfileVersion: "1.0.0",
				QueryClass:     "single-hop",
				Metrics:        map[string]float64{"recall_at_10": 1, "isolation_violations": 0},
				CurrentOutput: []RankedOutput{
					{StableID: "fact-current", Rank: 1, Score: 0.91},
					{StableID: "fact-current-second", Rank: 2, Score: 0.81},
				},
				CandidateOutput: []RankedOutput{
					{StableID: "fact-candidate", Rank: 1, Score: 0.93},
					{StableID: "fact-candidate-second", Rank: 2, Score: 0.83},
				},
			},
			{
				QueryID:         "query-002",
				ProfileID:       "hybrid-quality",
				ProfileVersion:  "1.0.0",
				QueryClass:      "multi-hop",
				Metrics:         map[string]float64{"recall_at_10": 0.5, "isolation_violations": 0},
				CurrentOutput:   []RankedOutput{},
				CandidateOutput: []RankedOutput{},
			},
		},
		Resources: ResourceReport{
			CPUSeconds:   1.25,
			CPUUnit:      "cpu_seconds",
			PeakRSSBytes: 67108864,
			PeakRSSUnit:  "bytes",
			StorageBytes: 1048576,
			StorageUnit:  "bytes",
			IndexBytes:   262144,
			IndexUnit:    "bytes",
		},
		Uncertainty: UncertaintyReport{
			Method:          "bootstrap",
			ConfidenceLevel: 0.95,
			SampleSize:      100,
			Notes:           "Intervals are reported per metric and profile by the protocol runner.",
		},
		Limitations: []string{"No external vector provider was measured.", "Representative hardware only."},
	}
}

func reverseMetricDefinitions(values []MetricDefinition) []MetricDefinition {
	result := append([]MetricDefinition(nil), values...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func reverseProfileReports(values []ProfileReport) []ProfileReport {
	result := append([]ProfileReport(nil), values...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func reverseQueryReports(values []QueryReport) []QueryReport {
	result := append([]QueryReport(nil), values...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func TestRequireJudgeFailsClosedWithActionableRemediation(t *testing.T) {
	unreachable := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	unreachable.Close()

	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "judge unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(refusing.Close)

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"judge-model"}]}`))
	}))
	t.Cleanup(healthy.Close)

	tests := []struct {
		name            string
		cfg             *JudgeConfig
		wantBlocked     bool
		wantRemediation []string
	}{
		{
			name:            "nil config",
			cfg:             nil,
			wantBlocked:     true,
			wantRemediation: []string{"OLLAMA_ENDPOINT", "ollama pull"},
		},
		{
			name:            "empty endpoint",
			cfg:             &JudgeConfig{Endpoint: "  ", Model: "judge-model"},
			wantBlocked:     true,
			wantRemediation: []string{"OLLAMA_ENDPOINT"},
		},
		{
			name:            "unreachable endpoint",
			cfg:             &JudgeConfig{Endpoint: unreachable.URL, Model: "judge-model", Timeout: 2 * time.Second},
			wantBlocked:     true,
			wantRemediation: []string{"ollama serve", "ollama pull judge-model", "OLLAMA_ENDPOINT"},
		},
		{
			name:            "non-2xx endpoint",
			cfg:             &JudgeConfig{Endpoint: refusing.URL, Model: "judge-model", Timeout: 2 * time.Second},
			wantBlocked:     true,
			wantRemediation: []string{"OLLAMA_ENDPOINT"},
		},
		{
			name:        "healthy endpoint",
			cfg:         &JudgeConfig{Endpoint: healthy.URL, Model: "judge-model", Timeout: 2 * time.Second},
			wantBlocked: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RequireJudge(context.Background(), tt.cfg)
			if !tt.wantBlocked {
				if err != nil {
					t.Fatalf("RequireJudge() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("RequireJudge() error = nil, want *BlockedError")
			}
			var blocked *BlockedError
			if !errors.As(err, &blocked) {
				t.Fatalf("RequireJudge() error = %v, want *BlockedError", err)
			}
			if !IsBlocked(err) {
				t.Errorf("IsBlocked(%v) = false, want true", err)
			}
			if !strings.HasPrefix(err.Error(), "BLOCKED:") {
				t.Errorf("Error() = %q, want BLOCKED: prefix", err.Error())
			}
			for _, want := range tt.wantRemediation {
				if !strings.Contains(blocked.Remediation, want) {
					t.Errorf("Remediation = %q, want substring %q", blocked.Remediation, want)
				}
			}
			if code := EvalExitCode(err); code != 2 {
				t.Errorf("EvalExitCode() = %d, want 2", code)
			}
		})
	}
}

func TestBuildEvalReportRegressionBelowGate(t *testing.T) {
	baseline := EvalBaseline{
		BaselineKey(EvalBenchmarkLOCOMO, EvalSliceOverall): 0.80,
		BaselineKey(EvalBenchmarkLOCOMO, "multi-hop"):      0.90,
	}

	report, err := BuildEvalReport(EvalBenchmarkLOCOMO, evalLocomoResult(0.75), false, enabledEvalJudge(), baseline, 0)
	if err != nil {
		t.Fatalf("BuildEvalReport() error = %v", err)
	}
	if report.BaselineStatus != "recorded" {
		t.Errorf("BaselineStatus = %q, want recorded", report.BaselineStatus)
	}
	if report.Gate.Status != EvalGateFailed {
		t.Fatalf("Gate.Status = %q, want %q", report.Gate.Status, EvalGateFailed)
	}

	row, ok := findEvalTask(report.Tasks, EvalTaskLongMemEvalPack)
	if !ok {
		t.Fatalf("task %s missing from report rows", EvalTaskLongMemEvalPack)
	}
	if row.Status != EvalRowCompared {
		t.Errorf("row.Status = %q, want %q", row.Status, EvalRowCompared)
	}
	if row.Before == nil || *row.Before != 0.80 {
		t.Errorf("row.Before = %v, want 0.80", row.Before)
	}
	if row.After == nil || *row.After != 0.75 {
		t.Errorf("row.After = %v, want 0.75", row.After)
	}
	if row.Delta == nil {
		t.Fatal("row.Delta = nil, want a measured delta")
	}
	if row.Passed == nil || *row.Passed {
		t.Errorf("row.Passed = %v, want false", row.Passed)
	}

	regression := report.RegressionError()
	if regression == nil {
		t.Fatal("RegressionError() = nil, want *RegressionError")
	}
	var target *RegressionError
	if !errors.As(regression, &target) {
		t.Fatalf("RegressionError() = %v, want *RegressionError", regression)
	}
	message := regression.Error()
	for _, want := range []string{
		EvalTaskLongMemEvalPack,
		EvalTaskSkewRoute,
		"before 0.8000",
		"after 0.7500",
		"delta -0.0500",
		"below gate 0.0000",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("RegressionError() = %q, want substring %q", message, want)
		}
	}
	if code := EvalExitCode(regression); code != 1 {
		t.Errorf("EvalExitCode() = %d, want 1", code)
	}
}

func TestBuildEvalReportGatePassesWhenDeltasHold(t *testing.T) {
	baseline := EvalBaseline{
		BaselineKey(EvalBenchmarkLOCOMO, EvalSliceOverall): 0.50,
		BaselineKey(EvalBenchmarkLOCOMO, "multi-hop"):      0.50,
	}

	report, err := BuildEvalReport(EvalBenchmarkLOCOMO, evalLocomoResult(0.75), false, enabledEvalJudge(), baseline, 0)
	if err != nil {
		t.Fatalf("BuildEvalReport() error = %v", err)
	}
	if report.Gate.Status != EvalGatePassed {
		t.Fatalf("Gate.Status = %q, want %q", report.Gate.Status, EvalGatePassed)
	}
	if err := report.RegressionError(); err != nil {
		t.Fatalf("RegressionError() = %v, want nil", err)
	}
	for _, row := range report.Tasks {
		if row.Status != EvalRowCompared {
			t.Errorf("task %s status = %q, want %q", row.TaskID, row.Status, EvalRowCompared)
		}
		if row.Passed == nil || !*row.Passed {
			t.Errorf("task %s passed = %v, want true", row.TaskID, row.Passed)
		}
	}
}

func TestBuildEvalReportMissingBaselineNeverInventsNumbers(t *testing.T) {
	recordedBaseline := EvalBaseline{
		BaselineKey(EvalBenchmarkLOCOMO, EvalSliceOverall): 0.50,
		BaselineKey(EvalBenchmarkLOCOMO, "multi-hop"):      0.50,
	}
	unmeasured := BenchmarkResult{
		Benchmark: EvalBenchmarkLOCOMO,
		Overall:   0.75,
		Total:     4,
		Correct:   3,
		Details: []QuestionResult{
			{ID: "q-1", Correct: true},
			{ID: "q-2", Correct: true},
			{ID: "q-3", Correct: true},
			{ID: "q-4", Correct: false},
		},
	}

	tests := []struct {
		name     string
		baseline EvalBaseline
		result   BenchmarkResult
		verify   func(*testing.T, EvalReport)
	}{
		{
			name:     "no baseline recorded",
			baseline: nil,
			result:   evalLocomoResult(0.75),
			verify: func(t *testing.T, report EvalReport) {
				if report.BaselineStatus != "not_recorded" {
					t.Errorf("BaselineStatus = %q, want not_recorded", report.BaselineStatus)
				}
				if report.Gate.Status != EvalGateNotEvaluated {
					t.Errorf("Gate.Status = %q, want %q", report.Gate.Status, EvalGateNotEvaluated)
				}
				if len(report.Gate.Failures) != 0 {
					t.Errorf("Gate.Failures = %v, want none without a baseline", report.Gate.Failures)
				}
				for _, row := range report.Tasks {
					if row.Status != EvalRowBaselineNotRecorded {
						t.Errorf("task %s status = %q, want %q", row.TaskID, row.Status, EvalRowBaselineNotRecorded)
					}
					if row.After == nil {
						t.Errorf("task %s after = nil, want the measured score", row.TaskID)
					}
					if row.Before != nil || row.Delta != nil || row.Passed != nil {
						t.Errorf("task %s invented comparison values: before=%v delta=%v passed=%v", row.TaskID, row.Before, row.Delta, row.Passed)
					}
				}
				encoded, err := SerializeEvalReport(report)
				if err != nil {
					t.Fatalf("SerializeEvalReport() error = %v", err)
				}
				published := string(encoded)
				for _, want := range []string{`"baseline_status": "not_recorded"`, `"status": "not_evaluated"`} {
					if !strings.Contains(published, want) {
						t.Errorf("published report missing %s:\n%s", want, published)
					}
				}
				for _, forbidden := range []string{`"before":`, `"delta":`} {
					if strings.Contains(published, forbidden) {
						t.Errorf("published report contains %s without a baseline:\n%s", forbidden, published)
					}
				}
			},
		},
		{
			name:     "baseline recorded but slice unmeasured",
			baseline: recordedBaseline,
			result:   unmeasured,
			verify: func(t *testing.T, report EvalReport) {
				if report.BaselineStatus != "recorded" {
					t.Errorf("BaselineStatus = %q, want recorded", report.BaselineStatus)
				}
				if report.Gate.Status != EvalGateNotEvaluated {
					t.Errorf("Gate.Status = %q, want %q", report.Gate.Status, EvalGateNotEvaluated)
				}
				row, ok := findEvalTask(report.Tasks, EvalTaskHippoRAG2Wiring)
				if !ok {
					t.Fatalf("task %s missing from report rows", EvalTaskHippoRAG2Wiring)
				}
				if row.Status != EvalRowNotMeasured {
					t.Errorf("row.Status = %q, want %q", row.Status, EvalRowNotMeasured)
				}
				if row.After != nil || row.Before != nil || row.Delta != nil || row.Passed != nil {
					t.Errorf("unmeasured row invented values: after=%v before=%v delta=%v passed=%v", row.After, row.Before, row.Delta, row.Passed)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report, err := BuildEvalReport(EvalBenchmarkLOCOMO, tt.result, false, enabledEvalJudge(), tt.baseline, 0)
			if err != nil {
				t.Fatalf("BuildEvalReport() error = %v", err)
			}
			tt.verify(t, report)
		})
	}
}

func TestEvalExitCodeMapping(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil", err: nil, want: 0},
		{name: "blocked", err: &BlockedError{Reason: "judge unreachable"}, want: 2},
		{name: "wrapped blocked", err: fmt.Errorf("probe judge: %w", &BlockedError{Reason: "judge unreachable"}), want: 2},
		{name: "regression", err: &RegressionError{Failures: []string{"task fell below gate"}}, want: 1},
		{name: "wrapped regression", err: fmt.Errorf("eval gate: %w", &RegressionError{Failures: []string{"task fell below gate"}}), want: 1},
		{name: "other error", err: errors.New("score aggregate is inconsistent"), want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EvalExitCode(tt.err); got != tt.want {
				t.Errorf("EvalExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestSerializeEvalReportRefusesWithoutFixedJudgeProtocol(t *testing.T) {
	valid := EvalReport{
		SchemaVersion:   EvalReportSchemaVersion,
		ProtocolVersion: FixedJudgeProtocolVersion,
		Benchmark:       EvalBenchmarkLOCOMO,
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		Judge:           enabledEvalJudge(),
		BaselineStatus:  "recorded",
		Gate:            EvalGate{Status: EvalGateNotEvaluated},
		Limitations:     []string{"Scores produced under the fixed Ollama-only judge."},
	}

	tests := []struct {
		name    string
		mutate  func(*EvalReport)
		wantErr string
	}{
		{name: "judge disabled", mutate: func(r *EvalReport) { r.Judge.Enabled = false }, wantErr: "fixed-judge protocol"},
		{name: "foreign judge protocol", mutate: func(r *EvalReport) { r.Judge.Protocol = "judge/v0" }, wantErr: "fixed-judge protocol"},
		{name: "foreign schema", mutate: func(r *EvalReport) { r.SchemaVersion = "eval-comparison/v0" }, wantErr: "schema_version"},
		{name: "missing benchmark", mutate: func(r *EvalReport) { r.Benchmark = "" }, wantErr: "benchmark is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := valid
			tt.mutate(&report)
			encoded, err := SerializeEvalReport(report)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("SerializeEvalReport() error = %v, want error containing %q", err, tt.wantErr)
			}
			if encoded != nil {
				t.Errorf("SerializeEvalReport() bytes = %q, want nil on refusal", encoded)
			}
		})
	}

	encoded, err := SerializeEvalReport(valid)
	if err != nil {
		t.Fatalf("SerializeEvalReport(valid) error = %v", err)
	}
	published := string(encoded)
	for _, want := range []string{
		`"schema_version": "eval-comparison/v1"`,
		`"protocol_version": "fixed-judge/ollama/v1"`,
		`"enabled": true`,
	} {
		if !strings.Contains(published, want) {
			t.Errorf("published report missing %s:\n%s", want, published)
		}
	}

	disabled := JudgeProtocolSummary{Protocol: FixedJudgeProtocolVersion, Enabled: false}
	if _, err := BuildEvalReport(EvalBenchmarkLOCOMO, evalLocomoResult(0.75), false, disabled, nil, 0); err == nil || !strings.Contains(err.Error(), "fixed-judge protocol") {
		t.Errorf("BuildEvalReport() with disabled judge error = %v, want fixed-judge protocol refusal", err)
	}
}

func enabledEvalJudge() JudgeProtocolSummary {
	return DescribeJudgeProtocol(&JudgeConfig{
		Endpoint: "http://127.0.0.1:11434",
		Model:    "judge-model",
	})
}

// evalLocomoResult builds a coherent four-question LOCOMO run whose overall
// and multi-hop slice accuracies both equal score at quarter granularity.
func evalLocomoResult(score float64) BenchmarkResult {
	const totalQuestions = 4
	correct := int(math.Round(score * totalQuestions))
	details := make([]QuestionResult, totalQuestions)
	for i := range details {
		details[i] = QuestionResult{
			ID:      fmt.Sprintf("q-%03d", i+1),
			Type:    "multi-hop",
			Correct: i < correct,
		}
	}
	return BenchmarkResult{
		Benchmark: EvalBenchmarkLOCOMO,
		Overall:   score,
		Total:     totalQuestions,
		Correct:   correct,
		Details:   details,
	}
}

func findEvalTask(rows []TaskComparison, taskID string) (TaskComparison, bool) {
	for _, row := range rows {
		if row.TaskID == taskID {
			return row, true
		}
	}
	return TaskComparison{}, false
}

package common

import (
	"bytes"
	"strings"
	"testing"
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
	for _, required := range []string{
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
				QueryID:         "query-001",
				ProfileID:       "lexical-fast",
				ProfileVersion:  "1.0.0",
				QueryClass:      "single-hop",
				Metrics:         map[string]float64{"recall_at_10": 1, "isolation_violations": 0},
				CurrentOutput:   []RankedOutput{{StableID: "fact-current", Rank: 1, Score: 0.91}},
				CandidateOutput: []RankedOutput{{StableID: "fact-candidate", Rank: 1, Score: 0.93}},
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

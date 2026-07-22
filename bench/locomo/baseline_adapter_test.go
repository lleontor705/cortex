package locomo

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/bench/common"
)

func TestAdaptBaselineEvidencePreservesExternalProvenance(t *testing.T) {
	conversations := []Conversation{{
		SampleID: "sample-1",
		QA: []QA{
			{
				Question: "Where did Caroline go?",
				Answer:   json.RawMessage(`"Lisbon"`),
				Evidence: []string{"D1:3", "D2:1"},
				Category: 2,
			},
			{
				Question: "When did Caroline leave?",
				Answer:   json.RawMessage(`"May 2023"`),
				Evidence: []string{"D2:4"},
				Category: 3,
			},
		},
	}}
	legacy := common.BenchmarkResult{
		Benchmark: "LOCOMO",
		Overall:   0.625,
		Total:     2,
		Correct:   1,
		Details: []common.QuestionResult{
			{Query: "Where did Caroline go?", Expected: "Lisbon", Score: 0.75},
			{Query: "When did Caroline leave?", Expected: "May 2023", Score: 0.5},
		},
	}

	evidence, err := AdaptBaselineEvidence(BaselineAdapterInput{
		Dataset: DatasetProvenance{
			Name:        "LOCOMO",
			Version:     "locomo10-v1",
			SourceURL:   "https://github.com/snap-research/locomo",
			License:     "dataset licence supplied by LOCOMO",
			SplitPolicy: "upstream LOCOMO evaluation split",
		},
		ProtocolVersion: "cortex-locomo-adapter/v1",
		ReportID:        "locomo-external-001",
		Build:           common.BuildMetadata{Commit: "0123456789abcdef"},
		Hardware: common.HardwareMetadata{
			ProfileID: "offline-test", OS: "test", Arch: "amd64", CPU: "test-cpu", MemoryMB: 1024,
		},
		Conversations: conversations,
		LegacyResult:  legacy,
	})
	if err != nil {
		t.Fatalf("AdaptBaselineEvidence() error = %v", err)
	}

	if evidence.EvidenceOrigin != EvidenceOriginExternal || evidence.CortexReproduction {
		t.Fatalf("origin = %q, CortexReproduction = %v; want external and false", evidence.EvidenceOrigin, evidence.CortexReproduction)
	}
	if evidence.ReleaseComparable {
		t.Fatal("LOCOMO evidence without stable Cortex relevance labels was marked release-comparable")
	}
	if len(evidence.BlockingLimitations) == 0 {
		t.Fatal("missing comparability limitation")
	}
	if len(evidence.Queries) != 2 {
		t.Fatalf("queries = %d, want 2", len(evidence.Queries))
	}
	first := evidence.Queries[0]
	if first.QueryID != "locomo/sample-1/qa-0001" || first.Category != "multi-hop" {
		t.Fatalf("first query mapping = %+v", first)
	}
	if got := strings.Join(first.ExternalEvidenceLocators, ","); got != "D1:3,D2:1" {
		t.Fatalf("external evidence = %q, want D1:3,D2:1", got)
	}
	if first.RelevantStableIDs != nil || first.RelevantEvidenceSpans != nil {
		t.Fatalf("adapter fabricated Cortex relevance labels: %+v", first)
	}
	if evidence.Report.Queries[0].Metrics[LegacyAnswerF1Metric] != 0.75 {
		t.Fatalf("legacy F1 = %v, want 0.75", evidence.Report.Queries[0].Metrics[LegacyAnswerF1Metric])
	}
	if len(evidence.Report.Queries[0].CurrentOutput) != 0 || len(evidence.Report.Queries[0].CandidateOutput) != 0 {
		t.Fatal("external LOCOMO evidence was converted into fabricated Cortex ranked outputs")
	}
	if err := evidence.Report.Validate(); err != nil {
		t.Fatalf("adapted report is invalid: %v", err)
	}
}

func TestAdaptBaselineEvidenceRejectsMissingDatasetAttribution(t *testing.T) {
	_, err := AdaptBaselineEvidence(BaselineAdapterInput{
		Dataset:         DatasetProvenance{Name: "LOCOMO", Version: "v1"},
		ProtocolVersion: "adapter/v1",
		ReportID:        "report-1",
		Build:           common.BuildMetadata{Commit: "abc"},
		Hardware: common.HardwareMetadata{
			ProfileID: "test", OS: "test", Arch: "amd64", CPU: "test", MemoryMB: 1,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "source URL, licence, and split policy") {
		t.Fatalf("AdaptBaselineEvidence() error = %v, want missing attribution error", err)
	}
}

func TestAdaptBaselineEvidenceRejectsDuplicateStableQueryIDs(t *testing.T) {
	qa := QA{Question: "What city?", Answer: json.RawMessage(`"Lisbon"`), Category: 1}
	conversations := []Conversation{
		{SampleID: "duplicate", QA: []QA{qa}},
		{SampleID: "duplicate", QA: []QA{qa}},
	}
	legacy := common.BenchmarkResult{
		Benchmark: "LOCOMO", Total: 2,
		Details: []common.QuestionResult{
			{Query: qa.Question, Expected: "Lisbon", Score: 1},
			{Query: qa.Question, Expected: "Lisbon", Score: 1},
		},
	}

	_, err := AdaptBaselineEvidence(validBaselineAdapterInput(conversations, legacy))
	if err == nil || !strings.Contains(err.Error(), "duplicate stable query ID") {
		t.Fatalf("AdaptBaselineEvidence() error = %v, want duplicate stable query ID error", err)
	}
}

func TestAdaptBaselineEvidenceDoesNotTreatAnswerOverlapAsRetrievalRelevance(t *testing.T) {
	conversations := []Conversation{{
		SampleID: "sample-1",
		QA:       []QA{{Question: "What city?", Answer: json.RawMessage(`"Lisbon"`), Evidence: []string{"D1:1"}, Category: 1}},
	}}
	legacy := common.BenchmarkResult{
		Benchmark: "LOCOMO", Total: 1,
		Details: []common.QuestionResult{{Query: "What city?", Expected: "Lisbon", Got: "Lisbon", Score: 1}},
	}

	evidence, err := AdaptBaselineEvidence(validBaselineAdapterInput(conversations, legacy))
	if err != nil {
		t.Fatalf("AdaptBaselineEvidence() error = %v", err)
	}
	for _, definition := range evidence.Report.MetricDefinitions {
		if strings.Contains(strings.ToLower(definition.Name), "recall") || strings.Contains(strings.ToLower(definition.Description), "retrieval relevance") {
			t.Fatalf("answer metric misrepresented as retrieval relevance: %+v", definition)
		}
	}
	if _, exists := evidence.Report.Queries[0].Metrics["recall_at_10"]; exists {
		t.Fatal("answer-token overlap was exposed as retrieval recall")
	}
}

func validBaselineAdapterInput(conversations []Conversation, legacy common.BenchmarkResult) BaselineAdapterInput {
	return BaselineAdapterInput{
		Dataset: DatasetProvenance{
			Name: "LOCOMO", Version: "v1", SourceURL: "https://example.test/locomo",
			License: "upstream licence", SplitPolicy: "upstream split",
		},
		ProtocolVersion: "adapter/v1",
		ReportID:        "report-1",
		Build:           common.BuildMetadata{Commit: "abc"},
		Hardware: common.HardwareMetadata{
			ProfileID: "test", OS: "test", Arch: "amd64", CPU: "test", MemoryMB: 1,
		},
		Conversations: conversations,
		LegacyResult:  legacy,
	}
}

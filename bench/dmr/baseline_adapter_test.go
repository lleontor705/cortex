package dmr

import (
	"reflect"
	"testing"

	"github.com/lleontor705/cortex/bench/common"
)

func TestAdaptBaselineEvidenceMapsStableQueryAndPreservesLegacyResult(t *testing.T) {
	legacy := common.QuestionResult{
		ID:       "conv-7",
		Type:     "deep-retrieval",
		Query:    "What instrument does Alex play?",
		Expected: "guitar",
		Got:      "Alex plays guitar.",
		Score:    0.75,
		Correct:  true,
	}

	evidence, err := AdaptBaselineEvidence(7, legacy, EvaluationCombinedF1RougeWithJudge)
	if err != nil {
		t.Fatalf("adapt DMR evidence: %v", err)
	}

	if evidence.QueryID != "msc-self-instruct/conversation-000007/self-instruct-b" {
		t.Errorf("query ID = %q", evidence.QueryID)
	}
	if !reflect.DeepEqual(evidence.LegacyResult, legacy) {
		t.Fatalf("legacy result changed:\n got: %#v\nwant: %#v", evidence.LegacyResult, legacy)
	}
	if evidence.EvaluationMode != EvaluationCombinedF1RougeWithJudge {
		t.Errorf("evaluation mode = %q", evidence.EvaluationMode)
	}
	if evidence.ScoreSemantics != "mean of answer-token F1 and ROUGE-L; correctness may be overridden by the configured judge" {
		t.Errorf("score semantics = %q", evidence.ScoreSemantics)
	}
}

func TestAdaptBaselineEvidenceRetainsDatasetAttributionAndReportMetadata(t *testing.T) {
	evidence, err := AdaptBaselineEvidence(0, common.QuestionResult{
		ID:    "conv-0",
		Type:  "deep-retrieval",
		Query: "Where did Sam move?",
	}, EvaluationCombinedF1Rouge)
	if err != nil {
		t.Fatalf("adapt DMR evidence: %v", err)
	}

	if evidence.SchemaVersion != DMRBaselineAdapterSchemaVersion {
		t.Errorf("schema version = %q", evidence.SchemaVersion)
	}
	if evidence.Benchmark != "DMR" || evidence.Dataset.Name != "MSC-Self-Instruct" {
		t.Errorf("benchmark attribution = %q / %q", evidence.Benchmark, evidence.Dataset.Name)
	}
	if evidence.Dataset.Source != "https://huggingface.co/datasets/MemGPT/MSC-Self-Instruct" {
		t.Errorf("dataset source = %q", evidence.Dataset.Source)
	}
	if evidence.Dataset.License != "Apache-2.0" || evidence.Dataset.Attribution != "MemGPT MSC-Self-Instruct (arXiv:2310.08560)" {
		t.Errorf("dataset license/attribution = %q / %q", evidence.Dataset.License, evidence.Dataset.Attribution)
	}
	if evidence.QueryClass != "deep-retrieval" || evidence.QuerySourceField != "self_instruct.B" {
		t.Errorf("query metadata = %q / %q", evidence.QueryClass, evidence.QuerySourceField)
	}
	if evidence.ReportKind != ReportKindExternalBenchmarkAdapter || evidence.CortexReproduction {
		t.Errorf("report provenance = %q, Cortex reproduction = %t", evidence.ReportKind, evidence.CortexReproduction)
	}
}

func TestAdaptBaselineEvidenceDisclosesIncompleteRetrievalLabels(t *testing.T) {
	evidence, err := AdaptBaselineEvidence(1, common.QuestionResult{
		ID:    "conv-1",
		Type:  "deep-retrieval",
		Query: "What food does Lee prefer?",
	}, EvaluationCombinedF1Rouge)
	if err != nil {
		t.Fatalf("adapt DMR evidence: %v", err)
	}

	if evidence.RetrievalLabelsComplete {
		t.Fatal("DMR adapter fabricated complete retrieval labels")
	}
	if evidence.ReleaseRetrievalEvidenceEligible {
		t.Fatal("incompletely labelled DMR result was marked release-quality retrieval evidence")
	}

	want := []string{
		"MSC-Self-Instruct does not provide Cortex stable relevant episode/fact IDs or evidence spans.",
		"DMR answer-token F1/ROUGE and optional judge correctness are not comparable to labelled retrieval relevance.",
		"External DMR results are not a Cortex reproduction unless a separate versioned reproduction report says otherwise.",
	}
	if !reflect.DeepEqual(evidence.Limitations, want) {
		t.Fatalf("limitations:\n got: %#v\nwant: %#v", evidence.Limitations, want)
	}
}

func TestAdaptBaselineEvidenceRejectsUnstableMappingInputs(t *testing.T) {
	tests := []struct {
		name       string
		index      int
		result     common.QuestionResult
		evaluation EvaluationMode
	}{
		{name: "negative conversation index", index: -1, result: common.QuestionResult{ID: "conv--1", Query: "question"}, evaluation: EvaluationCombinedF1Rouge},
		{name: "mismatched legacy ID", index: 2, result: common.QuestionResult{ID: "conv-3", Query: "question"}, evaluation: EvaluationCombinedF1Rouge},
		{name: "missing query", index: 2, result: common.QuestionResult{ID: "conv-2"}, evaluation: EvaluationCombinedF1Rouge},
		{name: "unknown evaluation", index: 2, result: common.QuestionResult{ID: "conv-2", Query: "question"}, evaluation: EvaluationMode("retrieval-relevance")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := AdaptBaselineEvidence(tt.index, tt.result, tt.evaluation); err == nil {
				t.Fatal("expected mapping validation error")
			}
		})
	}
}

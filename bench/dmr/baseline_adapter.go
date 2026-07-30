package dmr

import (
	"fmt"

	"github.com/lleontor705/cortex/bench/common"
)

const (
	// DMRBaselineAdapterSchemaVersion identifies the adapter metadata contract.
	DMRBaselineAdapterSchemaVersion = "1.0"

	// ReportKindExternalBenchmarkAdapter distinguishes adapted external benchmark
	// evidence from a versioned Cortex reproduction.
	ReportKindExternalBenchmarkAdapter = "external-benchmark-adapter"
)

// EvaluationMode records the legacy DMR evaluator used for a result. Neither
// mode is equivalent to labelled retrieval relevance.
type EvaluationMode string

const (
	EvaluationCombinedF1Rouge          EvaluationMode = "combined-f1-rouge"
	EvaluationCombinedF1RougeWithJudge EvaluationMode = "combined-f1-rouge-with-judge"
)

// DatasetAttribution preserves the upstream dataset source and licence.
type DatasetAttribution struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	License     string `json:"license"`
	Attribution string `json:"attribution"`
}

// BaselineEvidence adapts one legacy DMR result into explicit evidence
// metadata without changing its score or inventing retrieval relevance labels.
type BaselineEvidence struct {
	SchemaVersion                    string                `json:"schema_version"`
	Benchmark                        string                `json:"benchmark"`
	Dataset                          DatasetAttribution    `json:"dataset"`
	QueryID                          string                `json:"query_id"`
	QueryClass                       string                `json:"query_class"`
	QuerySourceField                 string                `json:"query_source_field"`
	EvaluationMode                   EvaluationMode        `json:"evaluation_mode"`
	ScoreSemantics                   string                `json:"score_semantics"`
	LegacyResult                     common.QuestionResult `json:"legacy_result"`
	ReportKind                       string                `json:"report_kind"`
	CortexReproduction               bool                  `json:"cortex_reproduction"`
	RetrievalLabelsComplete          bool                  `json:"retrieval_labels_complete"`
	ReleaseRetrievalEvidenceEligible bool                  `json:"release_retrieval_evidence_eligible"`
	Limitations                      []string              `json:"limitations"`
}

// AdaptBaselineEvidence maps a legacy conversation result to deterministic
// query metadata. It deliberately leaves retrieval-label fields incomplete:
// MSC-Self-Instruct supplies answers, not stable Cortex relevance IDs/spans.
func AdaptBaselineEvidence(conversationIndex int, result common.QuestionResult, evaluation EvaluationMode) (BaselineEvidence, error) {
	if conversationIndex < 0 {
		return BaselineEvidence{}, fmt.Errorf("conversation index must be non-negative")
	}
	legacyID := fmt.Sprintf("conv-%d", conversationIndex)
	if result.ID != legacyID {
		return BaselineEvidence{}, fmt.Errorf("legacy result ID %q does not match conversation %q", result.ID, legacyID)
	}
	if result.Query == "" {
		return BaselineEvidence{}, fmt.Errorf("legacy result query is required")
	}
	if evaluation != EvaluationCombinedF1Rouge && evaluation != EvaluationCombinedF1RougeWithJudge {
		return BaselineEvidence{}, fmt.Errorf("unsupported DMR evaluation mode %q", evaluation)
	}

	return BaselineEvidence{
		SchemaVersion: DMRBaselineAdapterSchemaVersion,
		Benchmark:     "DMR",
		Dataset: DatasetAttribution{
			Name:        "MSC-Self-Instruct",
			Source:      "https://huggingface.co/datasets/MemGPT/MSC-Self-Instruct",
			License:     "Apache-2.0",
			Attribution: "MemGPT MSC-Self-Instruct (arXiv:2310.08560)",
		},
		QueryID:                          fmt.Sprintf("msc-self-instruct/conversation-%06d/self-instruct-b", conversationIndex),
		QueryClass:                       "deep-retrieval",
		QuerySourceField:                 "self_instruct.B",
		EvaluationMode:                   evaluation,
		ScoreSemantics:                   scoreSemantics(evaluation),
		LegacyResult:                     result,
		ReportKind:                       ReportKindExternalBenchmarkAdapter,
		CortexReproduction:               false,
		RetrievalLabelsComplete:          false,
		ReleaseRetrievalEvidenceEligible: false,
		Limitations: []string{
			"MSC-Self-Instruct does not provide Cortex stable relevant episode/fact IDs or evidence spans.",
			"DMR answer-token F1/ROUGE and optional judge correctness are not comparable to labelled retrieval relevance.",
			"External DMR results are not a Cortex reproduction unless a separate versioned reproduction report says otherwise.",
		},
	}, nil
}

func scoreSemantics(evaluation EvaluationMode) string {
	if evaluation == EvaluationCombinedF1RougeWithJudge {
		return "mean of answer-token F1 and ROUGE-L; correctness may be overridden by the configured judge"
	}
	return "mean of answer-token F1 and ROUGE-L"
}

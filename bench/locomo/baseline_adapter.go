package locomo

import (
	"fmt"
	"sort"

	"github.com/lleontor705/cortex/bench/common"
)

const (
	// EvidenceOriginExternal identifies evidence produced by an upstream suite
	// rather than by a Cortex reproduction protocol.
	EvidenceOriginExternal = "external"
	// LegacyAnswerF1Metric names LOCOMO's answer-token score without implying
	// that it measures stable-ID or evidence-span retrieval relevance.
	LegacyAnswerF1Metric = "legacy_answer_token_f1"
)

// DatasetProvenance retains the upstream dataset identity and usage terms.
// Callers must supply these values from the exact dataset artifact they used.
type DatasetProvenance struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	SourceURL   string `json:"source_url"`
	License     string `json:"license"`
	SplitPolicy string `json:"split_policy"`
}

// BaselineAdapterInput binds a legacy LOCOMO result to its immutable inputs.
type BaselineAdapterInput struct {
	Dataset         DatasetProvenance
	ProtocolVersion string
	ReportID        string
	Build           common.BuildMetadata
	Hardware        common.HardwareMetadata
	Conversations   []Conversation
	LegacyResult    common.BenchmarkResult
}

// QueryEvidence maps a LOCOMO question to its upstream evidence locators.
// The Cortex relevance fields intentionally remain nil until a separately
// labelled corpus supplies stable IDs or immutable byte spans.
type QueryEvidence struct {
	QueryID                  string                `json:"query_id"`
	SampleID                 string                `json:"sample_id"`
	Question                 string                `json:"question"`
	Category                 string                `json:"category"`
	ExternalEvidenceLocators []string              `json:"external_evidence_locators"`
	RelevantStableIDs        []string              `json:"relevant_stable_ids,omitempty"`
	RelevantEvidenceSpans    []common.EvidenceSpan `json:"relevant_evidence_spans,omitempty"`
}

// BaselineEvidence keeps legacy answer evaluation usable while making its
// external origin and release-evidence limitations machine-readable.
type BaselineEvidence struct {
	Dataset             DatasetProvenance     `json:"dataset"`
	EvidenceOrigin      string                `json:"evidence_origin"`
	CortexReproduction  bool                  `json:"cortex_reproduction"`
	ReleaseComparable   bool                  `json:"release_comparable"`
	BlockingLimitations []string              `json:"blocking_limitations"`
	Queries             []QueryEvidence       `json:"queries"`
	Report              common.EvidenceReport `json:"report"`
}

// AdaptBaselineEvidence converts an already-computed legacy LOCOMO result into
// the common evidence-report shape. It performs no retrieval, scoring, judge,
// network, or filesystem work and therefore cannot change runner behavior.
func AdaptBaselineEvidence(input BaselineAdapterInput) (BaselineEvidence, error) {
	if input.Dataset.Name == "" || input.Dataset.Version == "" {
		return BaselineEvidence{}, fmt.Errorf("locomo baseline adapter: dataset name and version are required")
	}
	if input.Dataset.SourceURL == "" || input.Dataset.License == "" || input.Dataset.SplitPolicy == "" {
		return BaselineEvidence{}, fmt.Errorf("locomo baseline adapter: dataset source URL, licence, and split policy are required")
	}

	type mappedQuestion struct {
		queryID  string
		sampleID string
		qa       QA
	}
	questions := make([]mappedQuestion, 0)
	queryIDs := make(map[string]struct{})
	for _, conversation := range input.Conversations {
		if conversation.SampleID == "" {
			return BaselineEvidence{}, fmt.Errorf("locomo baseline adapter: sample_id is required")
		}
		for index, qa := range conversation.QA {
			queryID := fmt.Sprintf("locomo/%s/qa-%04d", conversation.SampleID, index+1)
			if _, duplicate := queryIDs[queryID]; duplicate {
				return BaselineEvidence{}, fmt.Errorf("locomo baseline adapter: duplicate stable query ID %q", queryID)
			}
			queryIDs[queryID] = struct{}{}
			questions = append(questions, mappedQuestion{
				queryID:  queryID,
				sampleID: conversation.SampleID,
				qa:       qa,
			})
		}
	}
	if len(questions) == 0 {
		return BaselineEvidence{}, fmt.Errorf("locomo baseline adapter: at least one question is required")
	}
	if len(input.LegacyResult.Details) != len(questions) {
		return BaselineEvidence{}, fmt.Errorf("locomo baseline adapter: legacy detail count %d does not match dataset question count %d", len(input.LegacyResult.Details), len(questions))
	}

	const profileID = "locomo-legacy-answer-evaluation"
	const profileVersion = "1.0.0"
	queryEvidence := make([]QueryEvidence, 0, len(questions))
	queryReports := make([]common.QueryReport, 0, len(questions))
	type categoryAggregate struct {
		sum   float64
		count int
	}
	aggregates := make(map[string]categoryAggregate)
	for index, question := range questions {
		detail := input.LegacyResult.Details[index]
		if detail.Query != question.qa.Question || detail.Expected != question.qa.AnswerString() {
			return BaselineEvidence{}, fmt.Errorf("locomo baseline adapter: legacy detail %d does not match dataset query and answer", index)
		}
		category := categoryNames[question.qa.Category]
		if category == "" {
			category = fmt.Sprintf("category-%d", question.qa.Category)
		}
		queryEvidence = append(queryEvidence, QueryEvidence{
			QueryID:                  question.queryID,
			SampleID:                 question.sampleID,
			Question:                 question.qa.Question,
			Category:                 category,
			ExternalEvidenceLocators: append([]string(nil), question.qa.Evidence...),
		})
		queryReports = append(queryReports, common.QueryReport{
			QueryID:         question.queryID,
			ProfileID:       profileID,
			ProfileVersion:  profileVersion,
			QueryClass:      category,
			Metrics:         map[string]float64{LegacyAnswerF1Metric: detail.Score},
			CurrentOutput:   []common.RankedOutput{},
			CandidateOutput: []common.RankedOutput{},
		})
		aggregate := aggregates[category]
		aggregate.sum += detail.Score
		aggregate.count++
		aggregates[category] = aggregate
	}

	categories := make([]string, 0, len(aggregates))
	for category := range aggregates {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	profiles := make([]common.ProfileReport, 0, len(categories))
	for _, category := range categories {
		aggregate := aggregates[category]
		profiles = append(profiles, common.ProfileReport{
			ProfileID:      profileID,
			ProfileVersion: profileVersion,
			QueryClass:     category,
			Metrics: map[string]float64{
				LegacyAnswerF1Metric: aggregate.sum / float64(aggregate.count),
			},
			Latency:    common.LatencyReport{Unit: "milliseconds"},
			Throughput: common.ThroughputReport{Unit: "queries_per_second"},
		})
	}

	limitations := []string{
		"External LOCOMO evidence has not been reproduced by the Cortex baseline protocol.",
		"LOCOMO dialogue evidence locators are upstream annotations, not Cortex stable relevance IDs or immutable evidence spans.",
		"Legacy answer-token F1 and optional judge correctness measure answer quality, not labelled retrieval relevance.",
		"Missing temporal, isolation, lifecycle, privacy, hard-negative, and no-answer labels block Cortex retrieval release claims.",
		"Zero-valued latency, throughput, and resource fields are unmeasured placeholders required by the common report schema, not performance results.",
	}
	report := common.EvidenceReport{
		SchemaVersion:   "retrieval-evidence-report/v1",
		ReportID:        input.ReportID,
		CorpusVersion:   input.Dataset.Version,
		ProtocolVersion: input.ProtocolVersion,
		Build:           input.Build,
		Hardware:        input.Hardware,
		MetricDefinitions: []common.MetricDefinition{{
			Name: LegacyAnswerF1Metric, Unit: "ratio", Direction: "higher_is_better",
			Description: "Legacy LOCOMO token-level overlap between retrieved answer text and the reference answer; it evaluates answer overlap only.",
		}},
		Profiles: profiles,
		Queries:  queryReports,
		Resources: common.ResourceReport{
			CPUUnit: "cpu_seconds", PeakRSSUnit: "bytes", StorageUnit: "bytes", IndexUnit: "bytes",
		},
		Uncertainty: common.UncertaintyReport{
			Method: "not estimated by legacy adapter", ConfidenceLevel: 0.95, SampleSize: len(questions),
			Notes: "The adapter preserves one supplied legacy result and does not infer uncertainty or reproducibility.",
		},
		Limitations: append([]string(nil), limitations...),
	}
	if err := report.Validate(); err != nil {
		return BaselineEvidence{}, fmt.Errorf("locomo baseline adapter: invalid evidence report: %w", err)
	}

	return BaselineEvidence{
		Dataset:             input.Dataset,
		EvidenceOrigin:      EvidenceOriginExternal,
		CortexReproduction:  false,
		ReleaseComparable:   false,
		BlockingLimitations: append([]string(nil), limitations...),
		Queries:             queryEvidence,
		Report:              report,
	}, nil
}

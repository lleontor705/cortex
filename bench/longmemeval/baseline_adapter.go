package longmemeval

import (
	"fmt"
	"strings"
)

// EvidenceOrigin identifies whether evidence was produced by Cortex or comes
// from an external benchmark source.
type EvidenceOrigin string

const (
	// EvidenceOriginExternal marks evidence that Cortex has not reproduced.
	EvidenceOriginExternal EvidenceOrigin = "external"
)

// DatasetProvenance identifies the exact upstream dataset distribution and
// retains its source and licensing notice without interpreting either.
type DatasetProvenance struct {
	DatasetSourceID string
	SourceURL       string
	LicenseNotice   string
}

// AbilityCategory retains a LongMemEval ability code and gives it a stable,
// human-readable name for evidence reports.
type AbilityCategory struct {
	Code string
	Name string
}

// TemporalLocator retains temporal metadata present on an upstream chat turn.
// SessionID and Timestamp are locators, not Cortex memory IDs or relevance
// labels.
type TemporalLocator struct {
	SessionID int
	Timestamp string
}

// RetrievalLabelStatus describes whether the upstream sample provides the
// stable relevance labels required for Cortex retrieval release evidence.
type RetrievalLabelStatus struct {
	Available               bool
	RelevantStableIDs       []string
	EvidenceSpans           []string
	ReleaseEvidenceEligible bool
}

// LegacyMetricComparability prevents answer scoring from being interpreted as
// labelled retrieval relevance.
type LegacyMetricComparability struct {
	AnswerTokenF1ComparableToLabelledRetrieval bool
	JudgeComparableToLabelledRetrieval         bool
}

// BaselineEvidence is a reporting-only view of one LongMemEval question. It
// does not execute retrieval or alter the legacy runner's F1/judge behavior.
type BaselineEvidence struct {
	DatasetSourceID    string
	QuerySourceID      string
	SourceURL          string
	LicenseNotice      string
	Ability            AbilityCategory
	TemporalMetadata   []TemporalLocator
	IsAbstention       bool
	EvidenceOrigin     EvidenceOrigin
	CortexReproduction bool
	RetrievalLabels    RetrievalLabelStatus
	LegacyMetrics      LegacyMetricComparability
	Limitations        []string
}

// AdaptBaselineEvidence converts upstream metadata into an evidence report.
// It deliberately leaves retrieval IDs and spans absent because LongMemEval's
// answer-oriented labels do not establish Cortex stable-ID relevance.
func AdaptBaselineEvidence(provenance DatasetProvenance, question Question) (BaselineEvidence, error) {
	if strings.TrimSpace(provenance.DatasetSourceID) == "" {
		return BaselineEvidence{}, fmt.Errorf("longmemeval evidence: dataset source ID is required")
	}
	if strings.TrimSpace(question.ID) == "" {
		return BaselineEvidence{}, fmt.Errorf("longmemeval evidence: query source ID is required")
	}
	if strings.TrimSpace(provenance.SourceURL) == "" {
		return BaselineEvidence{}, fmt.Errorf("longmemeval evidence: source URL is required")
	}
	if strings.TrimSpace(provenance.LicenseNotice) == "" {
		return BaselineEvidence{}, fmt.Errorf("longmemeval evidence: license notice is required")
	}

	ability, ok := longMemEvalAbility(question.Category)
	if !ok {
		return BaselineEvidence{}, fmt.Errorf("longmemeval evidence: unsupported ability category %q", question.Category)
	}

	temporal := make([]TemporalLocator, 0, len(question.ChatHistory))
	for _, turn := range question.ChatHistory {
		if strings.TrimSpace(turn.Timestamp) == "" {
			continue
		}
		temporal = append(temporal, TemporalLocator{
			SessionID: turn.SessionID,
			Timestamp: turn.Timestamp,
		})
	}

	return BaselineEvidence{
		DatasetSourceID:    provenance.DatasetSourceID,
		QuerySourceID:      question.ID,
		SourceURL:          provenance.SourceURL,
		LicenseNotice:      provenance.LicenseNotice,
		Ability:            ability,
		TemporalMetadata:   temporal,
		IsAbstention:       ability.Code == "ABS",
		EvidenceOrigin:     EvidenceOriginExternal,
		CortexReproduction: false,
		RetrievalLabels: RetrievalLabelStatus{
			Available:               false,
			RelevantStableIDs:       nil,
			EvidenceSpans:           nil,
			ReleaseEvidenceEligible: false,
		},
		LegacyMetrics: LegacyMetricComparability{
			AnswerTokenF1ComparableToLabelledRetrieval: false,
			JudgeComparableToLabelledRetrieval:         false,
		},
		Limitations: []string{
			"LongMemEval is external evidence and not a Cortex reproduction.",
			"The upstream sample does not provide Cortex stable relevance IDs or evidence spans.",
			"Legacy answer-token F1 and judge outputs are not labelled retrieval relevance metrics.",
		},
	}, nil
}

func longMemEvalAbility(category string) (AbilityCategory, bool) {
	code := strings.ToUpper(strings.TrimSpace(category))
	names := map[string]string{
		"IE":  "information_extraction",
		"MR":  "multi_session_reasoning",
		"TR":  "temporal_reasoning",
		"KU":  "knowledge_updates",
		"ABS": "abstention",
	}
	name, ok := names[code]
	return AbilityCategory{Code: code, Name: name}, ok
}

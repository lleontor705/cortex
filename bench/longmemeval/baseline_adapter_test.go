package longmemeval

import (
	"reflect"
	"strings"
	"testing"
)

func TestAdaptBaselineEvidencePreservesSourceMetadata(t *testing.T) {
	provenance := DatasetProvenance{
		DatasetSourceID: "longmemeval-release-2024-10",
		SourceURL:       "https://github.com/xiaowu0162/LongMemEval",
		LicenseNotice:   "Use and redistribution remain governed by the upstream dataset distribution.",
	}
	question := Question{
		ID:       "q-temporal-17",
		Question: "What happened after the appointment?",
		Answer:   "The follow-up call.",
		Category: "TR",
		ChatHistory: []ChatTurn{
			{Role: "user", Content: "I have an appointment.", SessionID: 4, Timestamp: "2024-01-02T09:00:00Z"},
			{Role: "assistant", Content: "Noted.", SessionID: 4, Timestamp: ""},
			{Role: "user", Content: "The follow-up call is done.", SessionID: 5, Timestamp: "2024-01-03T10:30:00Z"},
		},
	}

	evidence, err := AdaptBaselineEvidence(provenance, question)
	if err != nil {
		t.Fatalf("AdaptBaselineEvidence() error = %v", err)
	}

	if evidence.DatasetSourceID != provenance.DatasetSourceID || evidence.QuerySourceID != question.ID {
		t.Fatalf("source IDs = %q/%q, want %q/%q", evidence.DatasetSourceID, evidence.QuerySourceID, provenance.DatasetSourceID, question.ID)
	}
	if evidence.SourceURL != provenance.SourceURL || evidence.LicenseNotice != provenance.LicenseNotice {
		t.Fatalf("source attribution was not retained: %#v", evidence)
	}
	if evidence.Ability.Code != "TR" || evidence.Ability.Name != "temporal_reasoning" {
		t.Fatalf("ability = %#v, want TR/temporal_reasoning", evidence.Ability)
	}
	wantTemporal := []TemporalLocator{
		{SessionID: 4, Timestamp: "2024-01-02T09:00:00Z"},
		{SessionID: 5, Timestamp: "2024-01-03T10:30:00Z"},
	}
	if !reflect.DeepEqual(evidence.TemporalMetadata, wantTemporal) {
		t.Fatalf("temporal metadata = %#v, want %#v", evidence.TemporalMetadata, wantTemporal)
	}
	if evidence.IsAbstention {
		t.Fatal("temporal reasoning question must not be marked as abstention")
	}
}

func TestAdaptBaselineEvidenceRetainsAbilityAndAbstentionCategories(t *testing.T) {
	provenance := validDatasetProvenance()
	tests := []struct {
		code       string
		name       string
		abstention bool
	}{
		{code: "IE", name: "information_extraction"},
		{code: "MR", name: "multi_session_reasoning"},
		{code: "TR", name: "temporal_reasoning"},
		{code: "KU", name: "knowledge_updates"},
		{code: "ABS", name: "abstention", abstention: true},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			evidence, err := AdaptBaselineEvidence(provenance, Question{ID: "query-" + tt.code, Category: tt.code})
			if err != nil {
				t.Fatalf("AdaptBaselineEvidence() error = %v", err)
			}
			if evidence.Ability.Code != tt.code || evidence.Ability.Name != tt.name {
				t.Fatalf("ability = %#v, want %s/%s", evidence.Ability, tt.code, tt.name)
			}
			if evidence.IsAbstention != tt.abstention {
				t.Fatalf("IsAbstention = %t, want %t", evidence.IsAbstention, tt.abstention)
			}
		})
	}
}

func TestAdaptBaselineEvidenceDisclosesComparabilityLimits(t *testing.T) {
	evidence, err := AdaptBaselineEvidence(validDatasetProvenance(), Question{ID: "q-abs-1", Category: "ABS"})
	if err != nil {
		t.Fatalf("AdaptBaselineEvidence() error = %v", err)
	}

	if evidence.EvidenceOrigin != EvidenceOriginExternal || evidence.CortexReproduction {
		t.Fatalf("origin/reproduction = %q/%t, want external/false", evidence.EvidenceOrigin, evidence.CortexReproduction)
	}
	if evidence.RetrievalLabels.Available || evidence.RetrievalLabels.ReleaseEvidenceEligible {
		t.Fatalf("unlabelled retrieval evidence must not be release eligible: %#v", evidence.RetrievalLabels)
	}
	if evidence.RetrievalLabels.RelevantStableIDs != nil || evidence.RetrievalLabels.EvidenceSpans != nil {
		t.Fatalf("adapter fabricated retrieval labels: %#v", evidence.RetrievalLabels)
	}
	if evidence.LegacyMetrics.AnswerTokenF1ComparableToLabelledRetrieval || evidence.LegacyMetrics.JudgeComparableToLabelledRetrieval {
		t.Fatalf("legacy answer metrics must not claim labelled-retrieval comparability: %#v", evidence.LegacyMetrics)
	}
	joined := strings.Join(evidence.Limitations, " ")
	for _, disclosure := range []string{"external", "not a Cortex reproduction", "stable relevance IDs or evidence spans", "answer-token F1", "judge"} {
		if !strings.Contains(joined, disclosure) {
			t.Errorf("limitations %q do not disclose %q", joined, disclosure)
		}
	}
}

func TestAdaptBaselineEvidenceRejectsMissingSourceAuthority(t *testing.T) {
	tests := []struct {
		name       string
		provenance DatasetProvenance
		question   Question
		want       string
	}{
		{name: "dataset source ID", provenance: DatasetProvenance{SourceURL: "https://example.test", LicenseNotice: "upstream"}, question: Question{ID: "q1", Category: "IE"}, want: "dataset source ID"},
		{name: "query source ID", provenance: validDatasetProvenance(), question: Question{Category: "IE"}, want: "query source ID"},
		{name: "source URL", provenance: DatasetProvenance{DatasetSourceID: "dataset", LicenseNotice: "upstream"}, question: Question{ID: "q1", Category: "IE"}, want: "source URL"},
		{name: "license notice", provenance: DatasetProvenance{DatasetSourceID: "dataset", SourceURL: "https://example.test"}, question: Question{ID: "q1", Category: "IE"}, want: "license notice"},
		{name: "ability", provenance: validDatasetProvenance(), question: Question{ID: "q1", Category: "invented"}, want: "ability category"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := AdaptBaselineEvidence(tt.provenance, tt.question)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("AdaptBaselineEvidence() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func validDatasetProvenance() DatasetProvenance {
	return DatasetProvenance{
		DatasetSourceID: "longmemeval-release-2024-10",
		SourceURL:       "https://github.com/xiaowu0162/LongMemEval",
		LicenseNotice:   "Use and redistribution remain governed by the upstream dataset distribution.",
	}
}

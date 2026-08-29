package retrieval

import (
	"math"
	"testing"
)

func TestAgentScoreNormalizeSignalPolicies(t *testing.T) {
	t.Parallel()

	lexical := 3.0
	dense := 0.75
	maxSim := 1.2
	ppr := 0.25
	summary := 0.5
	inputs := []AgentScore{
		{Signal: AgentScoreLexical, Raw: &lexical, SourceKind: "memory", PublicHandle: "obs-a"},
		{Signal: AgentScoreDense, Raw: &dense, SourceKind: "memory", PublicHandle: "obs-b"},
		{Signal: AgentScoreMaxSim, Raw: &maxSim, SourceKind: "memory", PublicHandle: "obs-c"},
		{Signal: AgentScorePPR, Raw: &ppr, SourceKind: "graph", PublicHandle: "node-a"},
		{Signal: AgentScoreSummary, Raw: &summary, SourceKind: "summary", PublicHandle: "summary-a"},
	}

	got, err := NormalizeAgentScores(inputs)
	if err != nil {
		t.Fatalf("NormalizeAgentScores() error = %v", err)
	}

	want := map[AgentScoreSignal]float64{
		AgentScoreLexical: 0.75,
		AgentScoreDense:   0.75,
		AgentScoreMaxSim:  1,
		AgentScorePPR:     0.25,
		AgentScoreSummary: 0.5,
	}
	if len(got) != len(want) {
		t.Fatalf("NormalizeAgentScores() len = %d, want %d", len(got), len(want))
	}
	for _, score := range got {
		if score.Normalized < 0 || score.Normalized > 1 {
			t.Fatalf("normalized %s score = %v, want [0,1]", score.Signal, score.Normalized)
		}
		if score.Normalized != want[score.Signal] {
			t.Errorf("normalized %s score = %v, want %v", score.Signal, score.Normalized, want[score.Signal])
		}
	}
}

func TestAgentScoreRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	negative := -0.01
	nan := math.NaN()
	positiveInfinity := math.Inf(1)
	finite := 0.5
	tests := []struct {
		name  string
		score AgentScore
	}{
		{name: "missing", score: AgentScore{Signal: AgentScoreDense}},
		{name: "negative", score: AgentScore{Signal: AgentScoreDense, Raw: &negative}},
		{name: "nan", score: AgentScore{Signal: AgentScoreDense, Raw: &nan}},
		{name: "infinity", score: AgentScore{Signal: AgentScoreDense, Raw: &positiveInfinity}},
		{name: "unknown signal", score: AgentScore{Signal: "other", Raw: &finite}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NormalizeAgentScores([]AgentScore{tt.score}); err == nil {
				t.Fatal("NormalizeAgentScores() error = nil, want rejection")
			}
		})
	}
}

func TestAgentScoreDeterministicTies(t *testing.T) {
	t.Parallel()

	raw := 0.8
	inputs := []AgentScore{
		{Signal: AgentScoreDense, Raw: &raw, SourceKind: "summary", PublicHandle: "summary-b"},
		{Signal: AgentScoreDense, Raw: &raw, SourceKind: "memory", PublicHandle: "obs-b"},
		{Signal: AgentScoreDense, Raw: &raw, SourceKind: "memory", PublicHandle: "obs-a"},
	}

	got, err := NormalizeAgentScores(inputs)
	if err != nil {
		t.Fatalf("NormalizeAgentScores() error = %v", err)
	}
	want := []string{"obs-a", "obs-b", "summary-b"}
	for i := range want {
		if got[i].PublicHandle != want[i] {
			t.Errorf("result[%d] handle = %q, want %q", i, got[i].PublicHandle, want[i])
		}
	}
}

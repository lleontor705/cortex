package common

import (
	"math"
	"testing"
)

func TestF1Score(t *testing.T) {
	tests := []struct {
		pred, ref string
		want      float64
	}{
		{"the cat sat", "the cat sat", 1.0},
		{"the cat sat", "the dog sat", 0.667},
		{"", "", 1.0},
		{"hello", "", 0.0},
		{"", "hello", 0.0},
		{"a b c d", "a b e f", 0.5},
	}

	for _, tt := range tests {
		got := F1Score(tt.pred, tt.ref)
		if math.Abs(got-tt.want) > 0.01 {
			t.Errorf("F1(%q, %q) = %.3f, want %.3f", tt.pred, tt.ref, got, tt.want)
		}
	}
}

func TestRougeL(t *testing.T) {
	tests := []struct {
		pred, ref string
		wantMin   float64
	}{
		{"the cat sat on the mat", "the cat sat on the mat", 1.0},
		{"the cat sat", "the dog sat on mat", 0.3},
		{"", "", 1.0},
		{"hello", "", 0.0},
	}

	for _, tt := range tests {
		got := RougeL(tt.pred, tt.ref)
		if got < tt.wantMin-0.01 {
			t.Errorf("RougeL(%q, %q) = %.3f, want >= %.3f", tt.pred, tt.ref, got, tt.wantMin)
		}
	}
}

func TestAggregate(t *testing.T) {
	results := []QuestionResult{
		{ID: "1", Type: "single-hop", Score: 0.9, Correct: true},
		{ID: "2", Type: "single-hop", Score: 0.8, Correct: true},
		{ID: "3", Type: "multi-hop", Score: 0.3, Correct: false},
	}

	agg := Aggregate(results)
	if agg.Total != 3 {
		t.Fatalf("total = %d", agg.Total)
	}
	if agg.Correct != 2 {
		t.Fatalf("correct = %d", agg.Correct)
	}
	if math.Abs(agg.Overall-0.667) > 0.01 {
		t.Fatalf("overall = %.3f", agg.Overall)
	}
	if _, ok := agg.ByType["single-hop"]; !ok {
		t.Fatal("missing single-hop type")
	}
}

func TestRecallAtK(t *testing.T) {
	tests := []struct {
		name      string
		retrieved []string
		relevant  []string
		k         int
		want      float64
	}{
		{name: "all relevant in cutoff", retrieved: []string{"a", "b", "x"}, relevant: []string{"a", "b"}, k: 2, want: 1},
		{name: "partial recall", retrieved: []string{"a", "x", "b"}, relevant: []string{"a", "b"}, k: 2, want: 0.5},
		{name: "duplicate retrieval does not double count", retrieved: []string{"a", "a", "b"}, relevant: []string{"a", "b"}, k: 2, want: 0.5},
		{name: "duplicate labels do not change denominator", retrieved: []string{"a"}, relevant: []string{"a", "a"}, k: 1, want: 1},
		{name: "empty relevant is deterministic", retrieved: []string{"a"}, relevant: nil, k: 1, want: 0},
		{name: "empty retrieval", retrieved: nil, relevant: []string{"a"}, k: 5, want: 0},
		{name: "non-positive cutoff", retrieved: []string{"a"}, relevant: []string{"a"}, k: 0, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RecallAtK(tt.retrieved, tt.relevant, tt.k); math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("RecallAtK() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMRR(t *testing.T) {
	tests := []struct {
		name      string
		retrieved []string
		relevant  []string
		want      float64
	}{
		{name: "first result relevant", retrieved: []string{"a", "b"}, relevant: []string{"a"}, want: 1},
		{name: "later result relevant", retrieved: []string{"x", "a"}, relevant: []string{"a"}, want: 0.5},
		{name: "duplicate result consumes rank without gaining credit", retrieved: []string{"x", "x", "a"}, relevant: []string{"a"}, want: 1.0 / 3.0},
		{name: "no relevant result", retrieved: []string{"x"}, relevant: []string{"a"}, want: 0},
		{name: "empty labels", retrieved: []string{"a"}, relevant: nil, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MRR(tt.retrieved, tt.relevant); math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("MRR() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNDCGAtK(t *testing.T) {
	tests := []struct {
		name      string
		retrieved []string
		relevance map[string]float64
		k         int
		want      float64
	}{
		{name: "ideal graded order", retrieved: []string{"high", "low"}, relevance: map[string]float64{"high": 3, "low": 1}, k: 2, want: 1},
		{name: "reversed graded order", retrieved: []string{"low", "high"}, relevance: map[string]float64{"high": 3, "low": 1}, k: 2, want: (1 + 7/math.Log2(3)) / (7 + 1/math.Log2(3))},
		{name: "duplicate result consumes rank without gaining twice", retrieved: []string{"high", "high", "low"}, relevance: map[string]float64{"high": 3, "low": 1}, k: 2, want: 7 / (7 + 1/math.Log2(3))},
		{name: "requested cutoff is not reduced to relevant count", retrieved: []string{"x", "y", "high"}, relevance: map[string]float64{"high": 3}, k: 3, want: 0.5},
		{name: "non-positive grades are irrelevant", retrieved: []string{"zero", "negative"}, relevance: map[string]float64{"zero": 0, "negative": -1}, k: 2, want: 0},
		{name: "empty inputs", retrieved: nil, relevance: nil, k: 5, want: 0},
		{name: "non-positive cutoff", retrieved: []string{"high"}, relevance: map[string]float64{"high": 3}, k: -1, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NDCGAtK(tt.retrieved, tt.relevance, tt.k); math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("NDCGAtK() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvidenceRecall(t *testing.T) {
	span := func(id string, start, end int) EvidenceRef {
		return EvidenceRef{Span: &EvidenceSpan{EpisodeID: id, StartByte: start, EndByte: end}}
	}
	tests := []struct {
		name      string
		retrieved []EvidenceRef
		relevant  []EvidenceRef
		want      float64
	}{
		{name: "matches stable episode fact and exact span", retrieved: []EvidenceRef{{EpisodeID: "episode-1"}, {FactID: "fact-1"}, span("episode-2", 4, 12)}, relevant: []EvidenceRef{{EpisodeID: "episode-1"}, {FactID: "fact-1"}, span("episode-2", 4, 12)}, want: 1},
		{name: "different span is not evidence", retrieved: []EvidenceRef{span("episode-1", 4, 11)}, relevant: []EvidenceRef{span("episode-1", 4, 12)}, want: 0},
		{name: "ID-only evidence does not satisfy span label", retrieved: []EvidenceRef{{EpisodeID: "episode-1"}}, relevant: []EvidenceRef{span("episode-1", 4, 12)}, want: 0},
		{name: "duplicates do not double count", retrieved: []EvidenceRef{{FactID: "fact-1"}, {FactID: "fact-1"}}, relevant: []EvidenceRef{{FactID: "fact-1"}, {FactID: "fact-2"}}, want: 0.5},
		{name: "empty labels", retrieved: []EvidenceRef{{FactID: "fact-1"}}, relevant: nil, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EvidenceRecall(tt.retrieved, tt.relevant); math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("EvidenceRecall() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecallNoAnswerAbstention(t *testing.T) {
	tests := []struct {
		name      string
		noAnswer  bool
		retrieved []string
		abstained bool
		want      bool
	}{
		{name: "correct no-answer abstention", noAnswer: true, abstained: true, want: true},
		{name: "answer on no-answer query", noAnswer: true, retrieved: []string{"x"}, want: false},
		{name: "silent empty result is not abstention", noAnswer: true, want: false},
		{name: "correct answered query", retrieved: []string{"a"}, want: true},
		{name: "abstention on answerable query", abstained: true, want: false},
		{name: "empty answer on answerable query", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NoAnswerCorrect(tt.noAnswer, tt.retrieved, tt.abstained); got != tt.want {
				t.Fatalf("NoAnswerCorrect() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsolationViolationCount(t *testing.T) {
	tests := []struct {
		name      string
		retrieved []string
		excluded  []string
		want      int
	}{
		{name: "no leakage", retrieved: []string{"a"}, excluded: []string{"b"}, want: 0},
		{name: "each leaked result remains blocking", retrieved: []string{"a", "b", "b"}, excluded: []string{"b"}, want: 2},
		{name: "duplicate excluded labels are a set", retrieved: []string{"b"}, excluded: []string{"b", "b"}, want: 1},
		{name: "empty inputs", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsolationViolationCount(tt.retrieved, tt.excluded); got != tt.want {
				t.Fatalf("IsolationViolationCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFilterEligibilityExact(t *testing.T) {
	tests := []struct {
		name     string
		returned []string
		eligible []string
		want     bool
	}{
		{name: "same exact set", returned: []string{"b", "a"}, eligible: []string{"a", "b"}, want: true},
		{name: "duplicate IDs preserve set equality", returned: []string{"a", "a"}, eligible: []string{"a"}, want: true},
		{name: "missing eligible ID", returned: []string{"a"}, eligible: []string{"a", "b"}, want: false},
		{name: "ineligible returned ID", returned: []string{"a", "x"}, eligible: []string{"a"}, want: false},
		{name: "both empty", want: true},
		{name: "expected empty but returned non-empty", returned: []string{"x"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FilterEligibilityExact(tt.returned, tt.eligible); got != tt.want {
				t.Fatalf("FilterEligibilityExact() = %v, want %v", got, tt.want)
			}
		})
	}
}

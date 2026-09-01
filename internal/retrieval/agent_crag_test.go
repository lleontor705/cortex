package retrieval

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAgentCRAGEvaluatesNormalizedScoresDeterministically(t *testing.T) {
	cfg := DefaultCRAGConfig()
	for _, tc := range []struct {
		name   string
		scores []float64
		want   ConfidenceGrade
	}{
		{"empty", nil, ConfidenceGradeLow}, {"below floor", []float64{.001}, ConfidenceGradeLow}, {"low", []float64{.29, .1}, ConfidenceGradeLow},
		{"medium", []float64{.3, .2}, ConfidenceGradeMedium}, {"high", []float64{.4, .65}, ConfidenceGradeHigh}, {"permuted high", []float64{.65, .4}, ConfidenceGradeHigh},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvaluateAgentCRAG(tc.scores, cfg)
			if err != nil || got != tc.want {
				t.Fatalf("got=%q want=%q err=%v", got, tc.want, err)
			}
		})
	}
	if got, err := EvaluateAgentCRAG([]float64{.3}, CRAGConfig{}); err != nil || got != ConfidenceGradeMedium {
		t.Fatalf("zero config got=%q err=%v", got, err)
	}
	for _, score := range []float64{-.01, 1.01, math.NaN(), math.Inf(1)} {
		if _, err := EvaluateAgentCRAG([]float64{score}, cfg); !errors.Is(err, ErrInvalidAgentCRAGScore) {
			t.Fatalf("score=%v err=%v", score, err)
		}
	}
}

func TestAgentCRAGRefinementIsDeterministicBoundedAndServerControlled(t *testing.T) {
	parts := make([]string, 40)
	for i := range parts {
		parts[i] = fmt.Sprintf("token-%02d-%s", i, strings.Repeat("x", 20))
	}
	query := strings.Join(parts, " ")
	first, second := RefineAgentCRAGQuery(query), RefineAgentCRAGQuery(query)
	if first != second || first == query || utf8.RuneCountInString(first) > AgentCRAGMaxQueryRunes {
		t.Fatalf("first=%q second=%q runes=%d", first, second, utf8.RuneCountInString(first))
	}
	tokens := make(map[string]bool)
	for _, token := range strings.Fields(strings.ToLower(first)) {
		tokens[token] = true
	}
	for _, term := range []string{"project-context", "implementation", "symbols", "decisions", "files", "context"} {
		if !tokens[term] {
			t.Fatalf("refinement omitted %q: %q", term, first)
		}
	}
}

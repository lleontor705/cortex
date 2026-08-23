package retrieval

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/graph"
)

func TestClassifyQueryComplexity(t *testing.T) {
	tests := []struct {
		query    string
		wantTier QueryTier
	}{
		{"func OpenStore", TierDirectFactual},
		{"#1024", TierDirectFactual},
		{"main.go", TierDirectFactual},
		{"cortex_meta", TierDirectFactual},
		{"What is the impact if we change the session schema?", TierMultiHopGraph},
		{"¿Por qué cambiamos la persistencia y qué archivos afecta?", TierMultiHopGraph},
		{"Show the dependency cycle between components", TierMultiHopGraph},
		{"Explain the general architecture and communities of modules", TierArchitecturalGlobal},
		{"Overview of system structure and components", TierArchitecturalGlobal},
		{"How does token validation work across microservices?", TierSemanticHybrid},
		{"Database migration strategy and best practices", TierSemanticHybrid},
	}

	for _, tt := range tests {
		got := ClassifyQueryComplexity(tt.query)
		if got != tt.wantTier {
			t.Errorf("ClassifyQueryComplexity(%q) = %v, want %v", tt.query, got, tt.wantTier)
		}
	}
}

func TestEvaluateCRAG(t *testing.T) {
	highResults := []*domain.SearchResult{
		{Rank: 0.85, Observation: domain.Observation{ID: 1, Title: "Match"}},
		{Rank: 0.001, Observation: domain.Observation{ID: 2, Title: "Noise"}}, // below floor
	}

	evalHigh := EvaluateCRAG(highResults, DefaultCRAGConfig())
	if evalHigh.Grade != ConfidenceGradeHigh {
		t.Errorf("expected high confidence, got %v", evalHigh.Grade)
	}
	if evalHigh.NeedsRefinement {
		t.Error("expected needs_refinement = false for high confidence")
	}
	if len(evalHigh.FilteredResults) != 1 {
		t.Errorf("expected noise candidate to be stripped, got %d results", len(evalHigh.FilteredResults))
	}

	lowResults := []*domain.SearchResult{
		{Rank: 0.15, Observation: domain.Observation{ID: 3, Title: "Weak Match"}},
	}
	evalLow := EvaluateCRAG(lowResults, DefaultCRAGConfig())
	if evalLow.Grade != ConfidenceGradeLow {
		t.Errorf("expected low confidence, got %v", evalLow.Grade)
	}
	if !evalLow.NeedsRefinement {
		t.Error("expected needs_refinement = true for low confidence")
	}
}

func TestExecuteAdaptiveSearch(t *testing.T) {
	ctx := context.Background()

	mockLexical := func(ctx context.Context, q domain.SearchOptions) ([]*domain.SearchResult, error) {
		return []*domain.SearchResult{
			{Rank: 0.75, Observation: domain.Observation{ID: 10, Title: "Auth Service"}},
		}, nil
	}

	opts := AdaptiveSearchOptions{
		Mode: "auto",
		GraphNodes: []graph.GraphAnalyticsNode{
			{ID: "10", Label: "Auth"},
			{ID: "20", Label: "Session"},
		},
		GraphEdges: []graph.GraphAnalyticsEdge{
			{Source: "10", Target: "20", Weight: 1.0},
		},
	}

	res, err := ExecuteAdaptiveSearch(ctx, "What affects the Auth Service and why?", opts, mockLexical, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.Tier != TierMultiHopGraph {
		t.Errorf("expected TierMultiHopGraph for dependency question, got %v", res.Tier)
	}
	if res.Confidence != ConfidenceGradeHigh {
		t.Errorf("expected High confidence, got %v", res.Confidence)
	}
	if len(res.Results) == 0 {
		t.Fatal("expected at least 1 result")
	}
}

func BenchmarkClassifyQueryComplexity(b *testing.B) {
	queries := []string{
		"func OpenStore",
		"How does authentication and session management work in multi-tenant mode?",
		"What is the blast radius and why is the session schema affected?",
		"Overview of system architecture and main components",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ClassifyQueryComplexity(queries[i%len(queries)])
	}
}

func BenchmarkEvaluateCRAG(b *testing.B) {
	results := []*domain.SearchResult{
		{Rank: 0.85}, {Rank: 0.70}, {Rank: 0.55}, {Rank: 0.40}, {Rank: 0.20},
	}

	cfg := DefaultCRAGConfig()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EvaluateCRAG(results, cfg)
	}
}

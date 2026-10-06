package retrieval

import (
	"context"
	"math"
	"reflect"
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

func TestExecuteAdaptiveSearch_HippoRAG2_BipartiteBoost(t *testing.T) {
	ctx := context.Background()

	// Candidate 1: "Auth Gateway" mentioning AuthService (high initial score)
	// Candidate 2: "JWT Validation Policy" mentioning TokenValidator (low initial score)
	mockLexical := func(ctx context.Context, q domain.SearchOptions) ([]*domain.SearchResult, error) {
		return []*domain.SearchResult{
			{
				Rank: 0.80,
				Observation: domain.Observation{
					ID:      100,
					Title:   "Auth Gateway",
					Content: "Dispatches requests to AuthService.",
				},
			},
			{
				Rank: 0.10,
				Observation: domain.Observation{
					ID:      200,
					Title:   "JWT Validation Policy",
					Content: "Enforces signature checks in TokenValidator.",
				},
			},
		}, nil
	}

	opts := AdaptiveSearchOptions{
		Mode: "multi_hop",
		GraphNodes: []graph.GraphAnalyticsNode{
			{ID: "sym:AuthService", Label: "AuthService", Kind: graph.NodeKindSymbol},
			{ID: "sym:TokenValidator", Label: "TokenValidator", Kind: graph.NodeKindSymbol},
		},
		GraphEdges: []graph.GraphAnalyticsEdge{
			{Source: "sym:AuthService", Target: "sym:TokenValidator", Type: "calls", Weight: 2.0},
		},
	}

	res, err := ExecuteAdaptiveSearch(ctx, "Why does the auth workflow depend on token validation?", opts, mockLexical, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.Tier != TierMultiHopGraph {
		t.Errorf("expected TierMultiHopGraph, got %v", res.Tier)
	}

	var found200 *domain.SearchResult
	for _, r := range res.Results {
		if r.ID == 200 {
			found200 = r
			break
		}
	}

	if found200 == nil {
		t.Fatal("expected candidate 200 to survive")
	}

	// Due to HippoRAG 2 bipartite PPR propagation (obs:100 -> sym:AuthService -> sym:TokenValidator -> obs:200),
	// candidate 200's rank must be boosted above its original 0.10!
	if found200.Rank <= 0.10 {
		t.Errorf("expected candidate 200 to receive HippoRAG 2 topological boost > 0.10, got %f", found200.Rank)
	}
}

// TestExecuteAdaptiveSearch_MultiHopNoteReorderingChangesFinalRanking pins the
// Chain-of-Note reading stage in the multi-hop fusion path (REQ-LME-001): the
// lexical pool ranks candidate 1 first, but its passage is irrelevant while
// candidate 2 is the note-best match. Dropping the note-ranked list from
// multi-hop fusion reverts the final order to lexical order and must fail here.
func TestExecuteAdaptiveSearch_MultiHopNoteReorderingChangesFinalRanking(t *testing.T) {
	ctx := context.Background()
	query := "Why does token validation affect the session cache?"

	mockLexical := func(ctx context.Context, q domain.SearchOptions) ([]*domain.SearchResult, error) {
		return []*domain.SearchResult{
			{
				Rank: 0.90,
				Observation: domain.Observation{
					ID:      1,
					Title:   "Backup Runbook",
					Content: "Quarterly billing invoices for hardware vendors.",
				},
			},
			{
				Rank: 0.50,
				Observation: domain.Observation{
					ID:      2,
					Title:   "Token Validation Flow",
					Content: "Token validation directly affects the session cache during login.",
				},
			},
			{
				Rank: 0.20,
				Observation: domain.Observation{
					ID:      3,
					Title:   "Cache Policy",
					Content: "Session cache rotation policy.",
				},
			},
		}, nil
	}

	res, err := ExecuteAdaptiveSearch(ctx, query, AdaptiveSearchOptions{
		Mode:  "multi_hop",
		Limit: 10,
	}, mockLexical, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Tier != TierMultiHopGraph {
		t.Fatalf("expected TierMultiHopGraph, got %v", res.Tier)
	}

	gotIDs := make([]int64, 0, len(res.Results))
	for _, r := range res.Results {
		gotIDs = append(gotIDs, r.ID)
	}

	// Lexical order alone yields [1 2 3]: the note list must lift the
	// note-ranked candidate 2 above the irrelevant lexical leader and keep the
	// mid-relevance candidate 3 ahead of candidate 1's note position.
	want := []int64{2, 1, 3}
	if !reflect.DeepEqual(gotIDs, want) {
		t.Errorf("note-ranked multi-hop fusion order = %v, want %v", gotIDs, want)
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

func TestAdaptiveFusionFeatures(t *testing.T) {
	peaked := []float64{0.9, 0.05, 0.03, 0.01, 0.01}
	peakedFeats := ComputeFusionFeatures(peaked)
	if !peakedFeats.Available {
		t.Fatal("expected peaked features to be available")
	}
	if peakedFeats.Skewness < 1.0 {
		t.Errorf("expected skewness >= 1.0 for peaked distribution, got %f", peakedFeats.Skewness)
	}
	if peakedFeats.Top1Margin < 0.5 {
		t.Errorf("expected top-1 margin >= 0.5 for peaked distribution, got %f", peakedFeats.Top1Margin)
	}

	flat := []float64{0.2, 0.19, 0.19, 0.18, 0.18, 0.17}
	flatFeats := ComputeFusionFeatures(flat)
	if !flatFeats.Available {
		t.Fatal("expected flat features to be available")
	}
	if flatFeats.Skewness > 0.5 {
		t.Errorf("expected skewness <= 0.5 for flat distribution, got %f", flatFeats.Skewness)
	}
	if flatFeats.Top1Margin > 0.2 {
		t.Errorf("expected top-1 margin <= 0.2 for flat distribution, got %f", flatFeats.Top1Margin)
	}

	for name, scores := range map[string][]float64{
		"empty":    {},
		"single":   {0.5},
		"with NaN": {0.5, math.NaN()},
		"with Inf": {0.5, math.Inf(1)},
		"nil":      nil,
	} {
		if f := ComputeFusionFeatures(scores); f.Available {
			t.Errorf("%s scores: expected unavailable features", name)
		}
	}

	original := []float64{0.01, 0.9, 0.05, 0.03}
	ComputeFusionFeatures(original)
	want := []float64{0.01, 0.9, 0.05, 0.03}
	for i := range want {
		if original[i] != want[i] {
			t.Fatalf("caller slice mutated at index %d: got %v, want %v", i, original, want)
		}
	}
}

func TestAdaptiveSkewRoutePeakedRoutesCheap(t *testing.T) {
	features := ComputeFusionFeatures([]float64{0.9, 0.05, 0.03, 0.01, 0.01})
	if !features.Available {
		t.Fatal("expected features to be available")
	}

	cases := []struct {
		query string
		want  QueryTier
	}{
		{"Explain the general architecture and communities of modules", TierSemanticHybrid},
		{"What is the impact if we change the session schema?", TierSemanticHybrid},
		{"How does token validation work across microservices?", TierSemanticHybrid},
		{"func OpenStore", TierDirectFactual},
	}
	for _, tt := range cases {
		base := ClassifyQueryComplexity(tt.query)
		if base == TierDirectFactual && tt.want == TierSemanticHybrid {
			t.Fatalf("test case %q must start above the direct tier", tt.query)
		}
		got := ClassifyQueryWithFusion(tt.query, features)
		if got != tt.want {
			t.Errorf("peaked ClassifyQueryWithFusion(%q) = %v (base %v), want %v", tt.query, got, base, tt.want)
		}
		if costRank(got) > costRank(TierSemanticHybrid) {
			t.Errorf("peaked distribution must skip expensive tiers, got %v", got)
		}
	}
}

func TestAdaptiveSkewRouteFlatEscalates(t *testing.T) {
	features := ComputeFusionFeatures([]float64{0.2, 0.19, 0.19, 0.18, 0.18, 0.17})
	if !features.Available {
		t.Fatal("expected features to be available")
	}

	cases := []struct {
		query string
		want  QueryTier
	}{
		{"func OpenStore", TierSemanticHybrid},
		{"How does token validation work across microservices?", TierMultiHopGraph},
		{"What is the impact if we change the session schema?", TierArchitecturalGlobal},
		{"Explain the general architecture and communities of modules", TierArchitecturalGlobal},
	}
	for _, tt := range cases {
		base := ClassifyQueryComplexity(tt.query)
		got := ClassifyQueryWithFusion(tt.query, features)
		if got != tt.want {
			t.Errorf("flat ClassifyQueryWithFusion(%q) = %v (base %v), want %v", tt.query, got, base, tt.want)
		}
		// Escalation is capped at the top tier, so a top-tier base stays put.
		if base != TierArchitecturalGlobal && costRank(got) <= costRank(base) {
			t.Errorf("flat distribution must escalate above base tier, got %v (base %v)", got, base)
		}
	}
}

func TestAdaptiveSkewRouteFallback(t *testing.T) {
	queries := []string{
		"func OpenStore",
		"How does token validation work across microservices?",
		"What is the impact if we change the session schema?",
		"Explain the general architecture and communities of modules",
	}

	for _, q := range queries {
		base := ClassifyQueryComplexity(q)
		if got := ClassifyQueryWithFusion(q, FusionRouteFeatures{}); got != base {
			t.Errorf("unavailable features: got %v, want heuristic %v for %q", got, base, q)
		}
		if got := ClassifyQueryWithFusion(q, ComputeFusionFeatures([]float64{0.5, math.NaN()})); got != base {
			t.Errorf("non-finite scores: got %v, want heuristic %v for %q", got, base, q)
		}
	}

	ambiguous := FusionRouteFeatures{Skewness: 0.7, Top1Margin: 0.6, Available: true}
	for _, q := range queries {
		base := ClassifyQueryComplexity(q)
		if got := ClassifyQueryWithFusion(q, ambiguous); got != base {
			t.Errorf("ambiguous features: got %v, want heuristic %v for %q", got, base, q)
		}
	}
}

func TestAdaptiveSkewRouteExecuteAdaptiveSearch(t *testing.T) {
	ctx := context.Background()
	mockLexical := func(ctx context.Context, q domain.SearchOptions) ([]*domain.SearchResult, error) {
		return []*domain.SearchResult{
			{Rank: 0.7, Observation: domain.Observation{ID: 1, Title: "Auth Service"}},
		}, nil
	}
	query := "How does token validation work across microservices?"
	baseTier := ClassifyQueryComplexity(query)
	if baseTier != TierSemanticHybrid {
		t.Fatalf("test requires a semantic base tier, got %v", baseTier)
	}

	flatRes, err := ExecuteAdaptiveSearch(ctx, query, AdaptiveSearchOptions{
		Mode:         "auto",
		FusionScores: []float64{0.2, 0.19, 0.19, 0.18, 0.18, 0.17},
	}, mockLexical, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if costRank(flatRes.Tier) <= costRank(baseTier) {
		t.Errorf("flat fusion scores must escalate above %v, got %v", baseTier, flatRes.Tier)
	}

	res, err := ExecuteAdaptiveSearch(ctx, query, AdaptiveSearchOptions{
		Mode:         "auto",
		FusionScores: []float64{0.9, 0.05, 0.03, 0.01, 0.01},
	}, mockLexical, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Tier != TierSemanticHybrid {
		t.Errorf("peaked fusion scores must route to %v, got %v", TierSemanticHybrid, res.Tier)
	}

	heuristicRes, err := ExecuteAdaptiveSearch(ctx, query, AdaptiveSearchOptions{Mode: "auto"}, mockLexical, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if heuristicRes.Tier != baseTier {
		t.Errorf("missing fusion scores must fall back to heuristic %v, got %v", baseTier, heuristicRes.Tier)
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

// TestExecuteAdaptiveSearch_SemanticHybridDenseFallback pins the FTS-starved
// semantic path: an empty lexical leg must answer from the dense pool at
// similarity rank instead of returning an empty context, while a non-empty
// lexical leg must keep the vector leg untouched (no extra vector search).
func TestExecuteAdaptiveSearch_SemanticHybridDenseFallback(t *testing.T) {
	ctx := context.Background()
	query := "How does token validation work across microservices?"
	if base := ClassifyQueryComplexity(query); base != TierSemanticHybrid {
		t.Fatalf("test requires a semantic base tier, got %v", base)
	}

	vectorCalls := 0
	mockVector := func(ctx context.Context, q domain.VectorQuery) ([]*domain.VectorSearchResult, error) {
		vectorCalls++
		return []*domain.VectorSearchResult{
			{
				Observation: domain.Observation{
					ID:      501,
					Title:   "Quarterly shipping notice",
					Content: "Pallets left the warehouse dock.",
				},
				Similarity: 0.90,
			},
			{
				Observation: domain.Observation{
					ID:      502,
					Title:   "Token validation fallback",
					Content: "Token validation across microservices retries once.",
				},
				Similarity: 0.80,
			},
			{
				Observation: domain.Observation{
					ID:      503,
					Title:   "Zebra fjord quilts",
					Content: "Zebra fjord quilts.",
				},
				Similarity: 0.70,
			},
		}, nil
	}
	emptyLexical := func(ctx context.Context, q domain.SearchOptions) ([]*domain.SearchResult, error) {
		return nil, nil
	}

	starved, err := ExecuteAdaptiveSearch(ctx, query, AdaptiveSearchOptions{
		Mode:        "auto",
		Limit:       2,
		QueryVector: []float32{0.1, 0.2},
	}, emptyLexical, mockVector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if starved.Tier != TierSemanticHybrid {
		t.Fatalf("expected TierSemanticHybrid, got %v", starved.Tier)
	}
	if vectorCalls != 1 {
		t.Fatalf("expected exactly one dense fallback search, got %d", vectorCalls)
	}
	gotIDs := make([]int64, 0, len(starved.Results))
	for _, r := range starved.Results {
		gotIDs = append(gotIDs, r.ID)
	}
	// Similarity rank leads with 501; 502 is the only answer-bearing passage
	// (it keeps the gate open) and sits behind it at its dense position.
	if want := []int64{501, 502}; !reflect.DeepEqual(gotIDs, want) {
		t.Errorf("dense fallback order = %v, want %v (similarity rank, truncated to limit)", gotIDs, want)
	}

	populatedLexical := func(ctx context.Context, q domain.SearchOptions) ([]*domain.SearchResult, error) {
		return []*domain.SearchResult{
			{Rank: 0.75, Observation: domain.Observation{ID: 10, Title: "lexical hit"}},
		}, nil
	}
	served, err := ExecuteAdaptiveSearch(ctx, query, AdaptiveSearchOptions{
		Mode:        "auto",
		Limit:       10,
		QueryVector: []float32{0.1, 0.2},
	}, populatedLexical, mockVector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vectorCalls != 1 {
		t.Errorf("lexical hit must suppress the dense fallback, vector searches = %d", vectorCalls)
	}
	if len(served.Results) != 1 || served.Results[0].ID != 10 {
		t.Errorf("lexical-served results = %+v, want the single lexical hit", served.Results)
	}
}

// TestExecuteAdaptiveSearch_SemanticHybridWithholdsUnreadableDenseContext
// pins the answer-bearing gate of the dense fallback: a dense pool the reading
// stage scores as irrelevant to the query must NOT replace the empty lexical
// context, because misleading evidence loses judge-correct answers that an
// empty context already earned.
func TestExecuteAdaptiveSearch_SemanticHybridWithholdsUnreadableDenseContext(t *testing.T) {
	ctx := context.Background()
	query := "How does token validation work across microservices?"
	if base := ClassifyQueryComplexity(query); base != TierSemanticHybrid {
		t.Fatalf("test requires a semantic base tier, got %v", base)
	}

	vectorCalls := 0
	mockVector := func(ctx context.Context, q domain.VectorQuery) ([]*domain.VectorSearchResult, error) {
		vectorCalls++
		return []*domain.VectorSearchResult{
			{Observation: domain.Observation{ID: 701, Title: "Zebra fjord quilts", Content: "Zebra fjord quilts."}, Similarity: 0.90},
			{Observation: domain.Observation{ID: 702, Title: "Quartz epoxy molds", Content: "Quartz epoxy molds."}, Similarity: 0.85},
		}, nil
	}
	emptyLexical := func(ctx context.Context, q domain.SearchOptions) ([]*domain.SearchResult, error) {
		return nil, nil
	}

	res, err := ExecuteAdaptiveSearch(ctx, query, AdaptiveSearchOptions{
		Mode:        "auto",
		Limit:       10,
		QueryVector: []float32{0.1, 0.2},
	}, emptyLexical, mockVector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vectorCalls != 1 {
		t.Fatalf("expected the dense pool to be read once, got %d searches", vectorCalls)
	}
	if len(res.Results) != 0 {
		t.Errorf("unreadable dense context must stay unserved, got %d results", len(res.Results))
	}
}

// TestExecuteAdaptiveSearch_MultiHopNoteReadsDensePool pins the Chain-of-Note
// reading input of the multi-hop tier: with an FTS-starved lexical leg the
// dense-derived pool must be read too, so a vector-only passage whose text
// matches the query is lifted from dense rank 7 into the final top-5. Without
// the dense reading input the output is raw dense order and this fails.
func TestExecuteAdaptiveSearch_MultiHopNoteReadsDensePool(t *testing.T) {
	ctx := context.Background()
	query := "When did Melanie run the charity race for the shelter?"
	const answerID int64 = 300

	distractor := "Zebra fjord quilts."
	mockVector := func(ctx context.Context, q domain.VectorQuery) ([]*domain.VectorSearchResult, error) {
		hits := make([]*domain.VectorSearchResult, 0, 8)
		for i := 0; i < 6; i++ {
			hits = append(hits, &domain.VectorSearchResult{
				Observation: domain.Observation{ID: int64(201 + i), Title: distractor, Content: distractor},
				Similarity:  0.90 - float64(i)*0.05,
			})
		}
		hits = append(hits,
			&domain.VectorSearchResult{
				Observation: domain.Observation{
					ID:      answerID,
					Title:   "Shelter race result",
					Content: "Melanie ran the charity race for the animal shelter on October 14, 2023.",
				},
				Similarity: 0.60,
			},
			&domain.VectorSearchResult{
				Observation: domain.Observation{ID: 208, Title: distractor, Content: distractor},
				Similarity:  0.55,
			},
		)
		return hits, nil
	}
	starvedLexical := func(ctx context.Context, q domain.SearchOptions) ([]*domain.SearchResult, error) {
		return nil, nil
	}

	res, err := ExecuteAdaptiveSearch(ctx, query, AdaptiveSearchOptions{
		Mode:        "multi_hop",
		Limit:       10,
		QueryVector: []float32{0.1, 0.2},
	}, starvedLexical, mockVector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Tier != TierMultiHopGraph {
		t.Fatalf("expected TierMultiHopGraph, got %v", res.Tier)
	}
	for position, r := range res.Results {
		if r.ID == answerID {
			if position > 4 {
				t.Errorf("note-read dense passage ranked %d (dense rank 7), want a top-5 lift", position+1)
			}
			return
		}
	}
	t.Errorf("answer candidate %d missing from results %v", answerID, res.Results)
}

// TestSpeculativeQueryMarkers pins the classifier feeding both dense-context
// gates: prediction wording must be caught case-insensitively while factual
// phrasing stays untouched.
func TestSpeculativeQueryMarkers(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"Would the shelter race be rescheduled for November?", true},
		{"MIGHT the parser ship this sprint?", true},
		{"Is the migration likely to finish before noon?", true},
		{"The release will probably slip a week, what then?", true},
		{"When did Melanie run the charity race for the shelter?", false},
		{"unlikely", false},
		{"What is the impact if we change the session schema?", false},
	}

	for _, tt := range tests {
		if got := speculativeQuery(tt.query); got != tt.want {
			t.Errorf("speculativeQuery(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

// TestSemanticDenseFallbackSkipsSpeculativeQueries pins the fallback gate: a
// prediction query has no passage that answers it, so the dense leg must not
// be called at all and the tier keeps the empty context the judge scores
// above similarity-retrieved noise.
func TestSemanticDenseFallbackSkipsSpeculativeQueries(t *testing.T) {
	ctx := context.Background()
	query := "Would token validation work across microservices without retries?"
	if base := ClassifyQueryComplexity(query); base != TierSemanticHybrid {
		t.Fatalf("test requires a semantic base tier, got %v", base)
	}

	vectorCalls := 0
	mockVector := func(ctx context.Context, q domain.VectorQuery) ([]*domain.VectorSearchResult, error) {
		vectorCalls++
		return []*domain.VectorSearchResult{
			{
				Observation: domain.Observation{
					ID:      511,
					Title:   "Token validation fallback",
					Content: "Token validation across microservices retries once.",
				},
				Similarity: 0.90,
			},
		}, nil
	}
	emptyLexical := func(ctx context.Context, q domain.SearchOptions) ([]*domain.SearchResult, error) {
		return nil, nil
	}

	res, err := ExecuteAdaptiveSearch(ctx, query, AdaptiveSearchOptions{
		Mode:        "auto",
		Limit:       2,
		QueryVector: []float32{0.1, 0.2},
	}, emptyLexical, mockVector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Tier != TierSemanticHybrid {
		t.Fatalf("expected TierSemanticHybrid, got %v", res.Tier)
	}
	if vectorCalls != 0 {
		t.Errorf("speculative query issued %d dense fallback searches, want 0", vectorCalls)
	}
	if len(res.Results) != 0 {
		t.Errorf("speculative query served dense context %+v, want none", res.Results)
	}
}

// TestExecuteAdaptiveSearch_MultiHopSpeculativeQueryKeepsDensePoolOutOfReading
// pins the reading-pool gate against the lift proven for factual queries: the
// dense leg still runs for fusion, but the Chain-of-Note stage reads the
// recognized candidates only, so a dense-only passage keeps its raw similarity
// position instead of earning a top-5 note lift.
func TestExecuteAdaptiveSearch_MultiHopSpeculativeQueryKeepsDensePoolOutOfReading(t *testing.T) {
	ctx := context.Background()
	query := "Would Melanie run the charity race for the shelter this year?"
	const answerID int64 = 300

	distractor := "Zebra fjord quilts."
	vectorCalls := 0
	mockVector := func(ctx context.Context, q domain.VectorQuery) ([]*domain.VectorSearchResult, error) {
		vectorCalls++
		hits := make([]*domain.VectorSearchResult, 0, 8)
		for i := 0; i < 6; i++ {
			hits = append(hits, &domain.VectorSearchResult{
				Observation: domain.Observation{ID: int64(201 + i), Title: distractor, Content: distractor},
				Similarity:  0.90 - float64(i)*0.05,
			})
		}
		hits = append(hits,
			&domain.VectorSearchResult{
				Observation: domain.Observation{
					ID:      answerID,
					Title:   "Shelter race result",
					Content: "Melanie ran the charity race for the animal shelter on October 14, 2023.",
				},
				Similarity: 0.60,
			},
			&domain.VectorSearchResult{
				Observation: domain.Observation{ID: 208, Title: distractor, Content: distractor},
				Similarity:  0.55,
			},
		)
		return hits, nil
	}
	starvedLexical := func(ctx context.Context, q domain.SearchOptions) ([]*domain.SearchResult, error) {
		return nil, nil
	}

	res, err := ExecuteAdaptiveSearch(ctx, query, AdaptiveSearchOptions{
		Mode:        "multi_hop",
		Limit:       10,
		QueryVector: []float32{0.1, 0.2},
	}, starvedLexical, mockVector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Tier != TierMultiHopGraph {
		t.Fatalf("expected TierMultiHopGraph, got %v", res.Tier)
	}
	if vectorCalls != 1 {
		t.Errorf("speculative query must keep the dense fusion leg, vector searches = %d", vectorCalls)
	}
	for position, r := range res.Results {
		if r.ID == answerID {
			if position < 6 {
				t.Errorf("dense-only passage ranked %d, want the raw dense rank 7 with no note lift", position+1)
			}
			return
		}
	}
	t.Errorf("answer candidate %d missing from results %v", answerID, res.Results)
}

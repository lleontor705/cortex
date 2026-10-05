package graph

import (
	"testing"
)

// TestPPRRunAppliesDenseSeedWeights pins that dense retrieval scores fuse into
// the PPR seed vector: with identical lexical evidence, the passage with the
// higher dense score must dominate, and raising a passage's dense score must
// raise its propagated score.
func TestPPRRunAppliesDenseSeedWeights(t *testing.T) {
	nodes := []GraphAnalyticsNode{
		{ID: "sym:AuthService", Label: "AuthService", Kind: NodeKindSymbol},
		{ID: "sym:TokenValidator", Label: "TokenValidator", Kind: NodeKindSymbol},
	}
	edges := []GraphAnalyticsEdge{
		{Source: "sym:AuthService", Target: "sym:TokenValidator", Type: "calls", Weight: 2.0},
	}
	nodes, edges = LinkPassages(nodes, edges, []PassageRef{
		{ID: 101, Label: "Auth Handler", Concepts: []string{"sym:AuthService"}},
		{ID: 102, Label: "JWT Spec", Concepts: []string{"sym:TokenValidator"}},
	}, 1.5)

	opts := DefaultPPROptions()
	opts.Directed = false

	baseSeeds := []PassageSeed{
		{PassageID: 101, LexicalScore: 0.5, DenseScore: 0.9},
		{PassageID: 102, LexicalScore: 0.5, DenseScore: 0.1},
	}

	out := RunHippoRAG2(HippoRAG2Request{
		Nodes:        nodes,
		Edges:        edges,
		PassageSeeds: baseSeeds,
		Options:      opts,
	})

	if !out.Applied {
		t.Fatal("expected PPR to apply on a linked, seeded graph")
	}
	if out.PassageScores[101] <= out.PassageScores[102] {
		t.Errorf("dense-heavy seed must outscore dense-light seed at equal lexical evidence: 101=%f 102=%f",
			out.PassageScores[101], out.PassageScores[102])
	}
	if out.ConceptScores["AuthService"] <= 0 {
		t.Errorf("expected positive score for triple/concept node AuthService, got %f", out.ConceptScores["AuthService"])
	}

	stronger := make([]PassageSeed, len(baseSeeds))
	copy(stronger, baseSeeds)
	stronger[1].DenseScore = 0.9

	out2 := RunHippoRAG2(HippoRAG2Request{
		Nodes:        nodes,
		Edges:        edges,
		PassageSeeds: stronger,
		Options:      opts,
	})

	if !out2.Applied {
		t.Fatal("expected PPR to apply for the second run")
	}
	if out2.PassageScores[102] <= out.PassageScores[102] {
		t.Errorf("raising dense score must raise the passage's PPR score: dense 0.1 -> %f, dense 0.9 -> %f",
			out.PassageScores[102], out2.PassageScores[102])
	}
}

// TestPPRHippoRAG2PassageAliasPropagatesMultiHop pins that passage: nodes are
// seeded through observation seeds and classified into the passage score
// family, and that activation reaches an unseeded passage across concept hops.
func TestPPRHippoRAG2PassageAliasPropagatesMultiHop(t *testing.T) {
	nodes := []GraphAnalyticsNode{
		{ID: PassageNodeID(42), Label: "Auth Handler", Kind: NodeKindPassage},
		{ID: PassageNodeID(7), Label: "JWT Spec", Kind: NodeKindPassage},
		{ID: "sym:AuthService", Label: "AuthService", Kind: NodeKindSymbol},
		{ID: "sym:TokenValidator", Label: "TokenValidator", Kind: NodeKindSymbol},
	}
	edges := []GraphAnalyticsEdge{
		{Source: PassageNodeID(42), Target: "sym:AuthService", Type: EdgeTypeMentions, Weight: 2.0},
		{Source: "sym:AuthService", Target: "sym:TokenValidator", Type: "calls", Weight: 1.5},
		{Source: PassageNodeID(7), Target: "sym:TokenValidator", Type: EdgeTypeMentions, Weight: 2.0},
	}

	opts := DefaultPPROptions()
	opts.Directed = false

	passageScores, conceptScores := HippoRAG2Propagate(nodes, edges, map[int64]float64{42: 0.8}, nil, opts)

	if passageScores[42] <= 0 {
		t.Fatalf("seeded passage 42 must receive score through the passage: alias, got %f", passageScores[42])
	}
	if passageScores[7] <= 0 {
		t.Errorf("unseeded passage 7 must receive multi-hop activation, got %f", passageScores[7])
	}
	if passageScores[42] <= passageScores[7] {
		t.Errorf("seeded passage must outscore propagated passage: 42=%f 7=%f", passageScores[42], passageScores[7])
	}
	if conceptScores["AuthService"] <= 0 {
		t.Errorf("concept scores must include AuthService, got %v", conceptScores)
	}
}

// TestPPREmptyAndSparseGraphFallsBack pins the fallback contract: an empty
// graph, an edge-less graph, or seeds that miss the graph entirely must return
// Applied=false with no scores and no error, leaving callers on the existing
// dense-plus-lexical fusion path.
func TestPPREmptyAndSparseGraphFallsBack(t *testing.T) {
	nodes := []GraphAnalyticsNode{
		{ID: "sym:AuthService", Label: "AuthService", Kind: NodeKindSymbol},
		{ID: "sym:TokenValidator", Label: "TokenValidator", Kind: NodeKindSymbol},
	}

	cases := []struct {
		name    string
		req     HippoRAG2Request
		wantWhy string
	}{
		{
			name: "empty graph",
			req: HippoRAG2Request{
				PassageSeeds: []PassageSeed{{PassageID: 1, DenseScore: 0.9, LexicalScore: 0.4}},
			},
			wantWhy: "no nodes",
		},
		{
			name: "sparse graph without edges",
			req: HippoRAG2Request{
				Nodes:        nodes,
				PassageSeeds: []PassageSeed{{PassageID: 1, DenseScore: 0.9}},
			},
			wantWhy: "no edges",
		},
		{
			name: "seeds miss the graph",
			req: HippoRAG2Request{
				Nodes: nodes,
				Edges: []GraphAnalyticsEdge{
					{Source: "sym:AuthService", Target: "sym:TokenValidator", Type: "calls", Weight: 2.0},
				},
				PassageSeeds: []PassageSeed{{PassageID: 999, DenseScore: 0.9}},
				ConceptSeeds: map[string]float64{"sym:UnknownConcept": 0.5},
			},
			wantWhy: "no seed reaches a node",
		},
		{
			name:    "zero-value request",
			req:     HippoRAG2Request{},
			wantWhy: "empty request",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := RunHippoRAG2(tc.req)
			if out.Applied {
				t.Errorf("%s: expected Applied=false so callers keep dense+lexical fusion", tc.wantWhy)
			}
			if len(out.PassageScores) != 0 || len(out.ConceptScores) != 0 {
				t.Errorf("%s: fallback must return empty scores, got passage=%d concept=%d",
					tc.wantWhy, len(out.PassageScores), len(out.ConceptScores))
			}
		})
	}
}

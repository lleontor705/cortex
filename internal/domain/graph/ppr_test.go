package graph

import (
	"testing"
)

func TestComputePersonalizedPageRank(t *testing.T) {
	// Linear Graph: A <-> B <-> C <-> D
	nodes := []GraphAnalyticsNode{
		{ID: "A", Label: "Node A"},
		{ID: "B", Label: "Node B"},
		{ID: "C", Label: "Node C"},
		{ID: "D", Label: "Node D"},
	}

	edges := []GraphAnalyticsEdge{
		{Source: "A", Target: "B", Weight: 1.0},
		{Source: "B", Target: "C", Weight: 1.0},
		{Source: "C", Target: "D", Weight: 1.0},
	}

	// Seed activation solely on node "A"
	seeds := map[string]float64{
		"A": 1.0,
	}

	scores := ComputePersonalizedPageRank(nodes, edges, seeds, DefaultPPROptions())

	if len(scores) != 4 {
		t.Fatalf("expected 4 node scores, got %d", len(scores))
	}

	// In HippoRAG, activation decays with distance from seed "A"
	// Score(A) > Score(B) > Score(C) > Score(D)
	if scores["A"] <= scores["B"] {
		t.Errorf("expected Score(A) > Score(B), got A=%f, B=%f", scores["A"], scores["B"])
	}
	if scores["B"] <= scores["C"] {
		t.Errorf("expected Score(B) > Score(C), got B=%f, C=%f", scores["B"], scores["C"])
	}
	if scores["C"] <= scores["D"] {
		t.Errorf("expected Score(C) > Score(D), got C=%f, D=%f", scores["C"], scores["D"])
	}

	// Verify probability distribution sums to 1.0 (+/- epsilon)
	var sum float64
	for _, s := range scores {
		sum += s
	}
	if sum < 0.99 || sum > 1.01 {
		t.Errorf("expected total probability sum ~1.0, got %f", sum)
	}
}

func TestHippoRAGPropagateTopK(t *testing.T) {
	nodes := []GraphAnalyticsNode{
		{ID: "core", Label: "Core Service"},
		{ID: "auth", Label: "Auth Module"},
		{ID: "session", Label: "Session Store"},
		{ID: "isolated", Label: "Isolated Util"},
	}

	edges := []GraphAnalyticsEdge{
		{Source: "core", Target: "auth", Weight: 2.0},
		{Source: "auth", Target: "session", Weight: 1.5},
	}

	seeds := map[string]float64{
		"core": 1.0,
	}

	topResults := HippoRAGPropagate(nodes, edges, seeds, 2)
	if len(topResults) != 2 {
		t.Fatalf("expected 2 top results, got %d", len(topResults))
	}

	if topResults[0].NodeID != "core" {
		t.Errorf("expected #1 to be 'core', got %s", topResults[0].NodeID)
	}
	if topResults[1].NodeID != "auth" {
		t.Errorf("expected #2 to be 'auth', got %s", topResults[1].NodeID)
	}
}

func BenchmarkComputePersonalizedPageRank(b *testing.B) {
	// Create a representative 100-node graph with 300 edges
	nodes := make([]GraphAnalyticsNode, 100)
	for i := 0; i < 100; i++ {
		nodes[i] = GraphAnalyticsNode{ID: string(rune('A' + (i % 26)))}
	}
	var edges []GraphAnalyticsEdge
	for i := 0; i < 99; i++ {
		edges = append(edges, GraphAnalyticsEdge{
			Source: nodes[i].ID,
			Target: nodes[i+1].ID,
			Weight: 1.0,
		})
	}
	seeds := map[string]float64{nodes[0].ID: 1.0}
	opts := DefaultPPROptions()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ComputePersonalizedPageRank(nodes, edges, seeds, opts)
	}
}

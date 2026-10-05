package graph

import (
	"testing"
)

// TestGraphPassagesLinkToConceptNodes pins the first-class passage node
// construction contract: LinkPassages materializes passage nodes with the
// canonical passage: ID prefix, links them to concept/triple nodes with
// mention edges, never mutates the base graph, and is idempotent.
func TestGraphPassagesLinkToConceptNodes(t *testing.T) {
	baseNodes := []GraphAnalyticsNode{
		{ID: "sym:AuthService", Label: "AuthService", Kind: NodeKindSymbol},
		{ID: "sym:TokenValidator", Label: "TokenValidator", Kind: NodeKindSymbol},
	}
	baseEdges := []GraphAnalyticsEdge{
		{Source: "sym:AuthService", Target: "sym:TokenValidator", Type: "calls", Weight: 2.0},
	}

	if PassageNodeID(42) != "passage:42" {
		t.Fatalf("PassageNodeID(42) = %q, want %q", PassageNodeID(42), "passage:42")
	}

	passages := []PassageRef{
		{ID: 42, Label: "Auth Gateway", Concepts: []string{"sym:AuthService"}},
		{ID: 7, Label: "JWT Policy", Concepts: []string{"sym:TokenValidator", "sym:AuthService"}},
	}

	nodes, edges := LinkPassages(baseNodes, baseEdges, passages, 1.5)

	if len(baseNodes) != 2 || len(baseEdges) != 1 {
		t.Fatalf("LinkPassages mutated its inputs: baseNodes=%d baseEdges=%d", len(baseNodes), len(baseEdges))
	}

	byID := make(map[string]GraphAnalyticsNode, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}

	p42, ok := byID[PassageNodeID(42)]
	if !ok {
		t.Fatal("passage 42 must exist as a first-class node")
	}
	if p42.Kind != NodeKindPassage {
		t.Errorf("passage node kind = %q, want %q", p42.Kind, NodeKindPassage)
	}
	if p42.Label != "Auth Gateway" {
		t.Errorf("passage node label = %q, want %q", p42.Label, "Auth Gateway")
	}
	if _, ok := byID[PassageNodeID(7)]; !ok {
		t.Fatal("passage 7 must exist as a first-class node")
	}

	type edgeKey struct{ src, dst, typ string }
	mentionWeights := make(map[edgeKey]float64)
	for _, e := range edges {
		if e.Type == EdgeTypeMentions {
			mentionWeights[edgeKey{e.Source, e.Target, e.Type}] = e.Weight
		}
	}
	for _, want := range []edgeKey{
		{PassageNodeID(42), "sym:AuthService", EdgeTypeMentions},
		{PassageNodeID(7), "sym:TokenValidator", EdgeTypeMentions},
		{PassageNodeID(7), "sym:AuthService", EdgeTypeMentions},
	} {
		w, ok := mentionWeights[want]
		if !ok {
			t.Errorf("missing mention edge %s -> %s", want.src, want.dst)
			continue
		}
		if w != 1.5 {
			t.Errorf("mention edge %s -> %s weight = %f, want 1.5", want.src, want.dst, w)
		}
	}

	// Idempotent: relinking the same passages must not duplicate nodes or edges.
	nodes2, edges2 := LinkPassages(nodes, edges, passages, 0)
	if len(nodes2) != len(nodes) {
		t.Errorf("relinking duplicated nodes: %d -> %d", len(nodes), len(nodes2))
	}
	if len(edges2) != len(edges) {
		t.Errorf("relinking duplicated edges: %d -> %d", len(edges), len(edges2))
	}

	// Non-positive mention weights fall back to DefaultWeight.
	_, weighted := LinkPassages(baseNodes, baseEdges, []PassageRef{
		{ID: 9, Label: "Fallback", Concepts: []string{"sym:AuthService"}},
	}, 0)
	found := false
	for _, e := range weighted {
		if e.Source == PassageNodeID(9) && e.Target == "sym:AuthService" {
			found = true
			if e.Weight != DefaultWeight {
				t.Errorf("mention weight = %f, want DefaultWeight %f", e.Weight, DefaultWeight)
			}
		}
	}
	if !found {
		t.Error("missing mention edge for passage 9")
	}
}

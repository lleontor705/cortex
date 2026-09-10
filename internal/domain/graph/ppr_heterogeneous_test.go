package graph

import (
	"testing"
)

func TestHippoRAG2Propagate_BipartitePassageSymbol(t *testing.T) {
	// Heterogeneous Graph:
	// Observation 101 ("Auth Handler") mentions Symbol "AuthService"
	// Symbol "AuthService" calls Symbol "TokenValidator"
	// Observation 102 ("JWT Spec") mentions Symbol "TokenValidator"
	// Observation 999 ("Database Migrations") mentions Symbol "MigrationRunner" (unrelated)
	nodes := []GraphAnalyticsNode{
		{ID: "obs:101", Label: "Auth Handler", Kind: NodeKindObservation},
		{ID: "obs:102", Label: "JWT Spec", Kind: NodeKindObservation},
		{ID: "obs:999", Label: "DB Migrations", Kind: NodeKindObservation},
		{ID: "sym:AuthService", Label: "AuthService", Kind: NodeKindSymbol},
		{ID: "sym:TokenValidator", Label: "TokenValidator", Kind: NodeKindSymbol},
		{ID: "sym:MigrationRunner", Label: "MigrationRunner", Kind: NodeKindSymbol},
	}

	edges := []GraphAnalyticsEdge{
		// Observation to Symbol
		{Source: "obs:101", Target: "sym:AuthService", Type: EdgeTypeMentions, Weight: 2.0},
		{Source: "obs:102", Target: "sym:TokenValidator", Type: EdgeTypeMentions, Weight: 2.0},
		{Source: "obs:999", Target: "sym:MigrationRunner", Type: EdgeTypeMentions, Weight: 2.0},

		// Symbol to Symbol dependency
		{Source: "sym:AuthService", Target: "sym:TokenValidator", Type: "calls", Weight: 1.5},
	}

	// Activate ONLY Observation 101
	obsSeeds := map[int64]float64{
		101: 1.0,
	}

	opts := DefaultPPROptions()
	opts.Directed = false // Associative reasoning flows bidirectionally between passages and symbols

	obsScores, symScores := HippoRAG2Propagate(nodes, edges, obsSeeds, nil, opts)

	if obsScores[101] <= 0 {
		t.Fatalf("expected positive score for seed observation 101, got %f", obsScores[101])
	}

	// Due to multi-hop propagation:
	// obs:101 -> sym:AuthService -> sym:TokenValidator -> obs:102
	// Therefore, obs:102 must receive associative activation!
	if obsScores[102] <= 0 {
		t.Errorf("expected positive score for multi-hop observation 102, got %f", obsScores[102])
	}

	if obsScores[101] <= obsScores[102] {
		t.Errorf("expected Score(obs:101) > Score(obs:102), got 101=%f, 102=%f", obsScores[101], obsScores[102])
	}

	// Unrelated observation 999 should have 0 score (or lower score than connected observation 102)
	if obsScores[999] >= obsScores[102] {
		t.Errorf("expected unrelated observation 999 to score less than connected observation 102, got 999=%f, 102=%f", obsScores[999], obsScores[102])
	}

	// Check that mentioned symbols also got scored
	if symScores["AuthService"] <= symScores["TokenValidator"] {
		t.Errorf("expected Score(AuthService) > Score(TokenValidator), got AuthService=%f, TokenValidator=%f",
			symScores["AuthService"], symScores["TokenValidator"])
	}
}

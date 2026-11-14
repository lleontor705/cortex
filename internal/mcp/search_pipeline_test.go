package mcp

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func pipelineResults(ids []int64, ranks []float64) []*domain.SearchResult {
	out := make([]*domain.SearchResult, len(ids))
	for i, id := range ids {
		out[i] = &domain.SearchResult{Observation: domain.Observation{ID: id, Title: "t"}, Rank: ranks[i]}
	}
	return out
}

func pipelineIDs(results []*domain.SearchResult) []int64 {
	ids := make([]int64, len(results))
	for i, r := range results {
		ids[i] = r.ID
	}
	return ids
}

func pipelineSameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestApplyGraphStage_NoGraphOrNoEdges(t *testing.T) {
	ctx := context.Background()

	nilGraph := &Stores{}
	results := pipelineResults([]int64{1, 2, 3}, []float64{0.9, 0.5, 0.1})
	before := pipelineIDs(results)
	if got := applyGraphStage(ctx, nilGraph, results); !pipelineSameIDs(pipelineIDs(got), before) {
		t.Fatalf("nil graph: order changed to %v, want %v", pipelineIDs(got), before)
	}

	stores := setupTestStores(t)
	createSession(t, stores, "s-pipe", "demo")
	o1 := saveObs(t, stores, "A", "demo", "s-pipe")
	o2 := saveObs(t, stores, "B", "demo", "s-pipe")
	o3 := saveObs(t, stores, "C", "demo", "s-pipe")
	results = pipelineResults([]int64{o1.ID, o2.ID, o3.ID}, []float64{0.9, 0.5, 0.1})
	before = pipelineIDs(results)
	if got := applyGraphStage(ctx, stores, results); !pipelineSameIDs(pipelineIDs(got), before) {
		t.Fatalf("no edges: order changed to %v, want %v", pipelineIDs(got), before)
	}
}

func TestApplyGraphStage_ReordersByGraph(t *testing.T) {
	ctx := context.Background()
	stores := setupTestStores(t)
	createSession(t, stores, "s-pipe", "demo")
	o1 := saveObs(t, stores, "One", "demo", "s-pipe")
	o2 := saveObs(t, stores, "Two", "demo", "s-pipe")
	o3 := saveObs(t, stores, "Three", "demo", "s-pipe")
	o4 := saveObs(t, stores, "Four", "demo", "s-pipe")
	for _, e := range []*domain.Edge{
		{FromObsID: o4.ID, ToObsID: o3.ID, RelationType: "references", Weight: 1},
		{FromObsID: o2.ID, ToObsID: o3.ID, RelationType: "references", Weight: 1},
		{FromObsID: o1.ID, ToObsID: o3.ID, RelationType: "references", Weight: 1},
	} {
		if err := stores.Graph.CreateEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	// Mixed-scale ranks: the graph stage must not depend on them.
	results := pipelineResults([]int64{o1.ID, o2.ID, o3.ID, o4.ID}, []float64{0.03, 0.02, 0.9, 0.01})
	incoming := make(map[int64]*domain.SearchResult, len(results))
	for _, r := range results {
		incoming[r.ID] = r
	}
	got := applyGraphStage(ctx, stores, results)

	if len(got) != 4 {
		t.Fatalf("got %d results, want 4", len(got))
	}
	pos := -1
	for i, r := range got {
		if incoming[r.ID] != r {
			t.Fatalf("result %d is not one of the input pointers", r.ID)
		}
		if r.ID == o3.ID {
			pos = i
		}
	}
	if pos < 0 || pos >= 2 {
		t.Fatalf("node 3 at position %d, want < 2 (order %v)", pos, pipelineIDs(got))
	}
	for _, r := range results {
		if r.Rank != incoming[r.ID].Rank {
			t.Fatalf("Rank mutated for %d", r.ID)
		}
	}
	if results[2].Rank != 0.9 || results[0].Rank != 0.03 {
		t.Fatalf("input Rank values changed: %v", []float64{results[0].Rank, results[2].Rank})
	}
}

func TestApplyGraphStage_Deterministic(t *testing.T) {
	ctx := context.Background()
	stores := setupTestStores(t)
	createSession(t, stores, "s-pipe", "demo")
	o1 := saveObs(t, stores, "One", "demo", "s-pipe")
	o2 := saveObs(t, stores, "Two", "demo", "s-pipe")
	o3 := saveObs(t, stores, "Three", "demo", "s-pipe")
	if err := stores.Graph.CreateEdge(ctx, &domain.Edge{FromObsID: o1.ID, ToObsID: o3.ID, RelationType: "references", Weight: 1}); err != nil {
		t.Fatal(err)
	}

	first := applyGraphStage(ctx, stores, pipelineResults([]int64{o1.ID, o2.ID, o3.ID}, []float64{0.2, 0.2, 0.2}))
	second := applyGraphStage(ctx, stores, pipelineResults([]int64{o1.ID, o2.ID, o3.ID}, []float64{0.2, 0.2, 0.2}))
	if !pipelineSameIDs(pipelineIDs(first), pipelineIDs(second)) {
		t.Fatalf("non-deterministic order: %v vs %v", pipelineIDs(first), pipelineIDs(second))
	}
}

func TestGateSearchResults_KeepsLowRankResults(t *testing.T) {
	results := pipelineResults([]int64{1, 2, 3}, []float64{0.02, 0.00002, 0.0000001})
	eval := gateSearchResults(results)
	// With MinScoreFloor disabled, none of the non-negative ranks may be filtered out.
	if len(eval.FilteredResults) != 3 {
		t.Fatalf("FilteredResults len = %d, want 3", len(eval.FilteredResults))
	}
	if eval.Grade == "" {
		t.Fatal("expected a confidence grade")
	}
	if len(results) != 3 {
		t.Fatalf("input slice modified: len %d", len(results))
	}
}

package mcp

import (
	"context"
	"fmt"
	"sort"

	"github.com/lleontor705/cortex/v2/internal/domain"
	graphdomain "github.com/lleontor705/cortex/v2/internal/domain/graph"
	"github.com/lleontor705/cortex/v2/internal/retrieval"
)

// graphRRFConstant mirrors the position-only RRF constant (k=60) used by retrieval fusion.
const graphRRFConstant = 60.0

// applyGraphStage re-orders results with Personalized PageRank over the observation graph.
// Seeds are positional (1/(k+position)), never raw Rank values, because Rank is on mixed
// scales after lexical/vector fusion. The PPR order is fused with the incoming order by
// position-only RRF, and the slice is returned sorted by that fused score (ties keep the
// incoming order). It returns the input unchanged when the graph has no edges.
func applyGraphStage(ctx context.Context, stores *Stores, results []*domain.SearchResult) []*domain.SearchResult {
	if stores == nil || stores.Graph == nil || len(results) == 0 {
		return results
	}

	var graphNodes []graphdomain.GraphAnalyticsNode
	var graphEdges []graphdomain.GraphAnalyticsEdge
	seeds := make(map[string]float64, len(results))
	seenNodes := make(map[int64]bool)

	for i, r := range results {
		seeds[fmt.Sprintf("%d", r.ID)] = 1.0 / (graphRRFConstant + float64(i+1))
		if !seenNodes[r.ID] {
			seenNodes[r.ID] = true
			graphNodes = append(graphNodes, graphdomain.GraphAnalyticsNode{
				ID:    fmt.Sprintf("%d", r.ID),
				Label: r.Title,
			})
		}

		edges, _ := stores.Graph.GetEdgesForObservation(ctx, r.ID)
		for _, e := range edges {
			graphEdges = append(graphEdges, graphdomain.GraphAnalyticsEdge{
				Source: fmt.Sprintf("%d", e.FromObsID),
				Target: fmt.Sprintf("%d", e.ToObsID),
				Weight: e.Weight,
			})
			if !seenNodes[e.FromObsID] {
				seenNodes[e.FromObsID] = true
				graphNodes = append(graphNodes, graphdomain.GraphAnalyticsNode{
					ID: fmt.Sprintf("%d", e.FromObsID),
				})
			}
			if !seenNodes[e.ToObsID] {
				seenNodes[e.ToObsID] = true
				graphNodes = append(graphNodes, graphdomain.GraphAnalyticsNode{
					ID: fmt.Sprintf("%d", e.ToObsID),
				})
			}
		}
	}

	if len(graphEdges) == 0 {
		return results
	}

	pprScores := graphdomain.ComputePersonalizedPageRank(graphNodes, graphEdges, seeds, graphdomain.DefaultPPROptions())

	// Positions (0-based) of incoming results, ordered by PPR score descending,
	// ties broken by incoming position ascending.
	pprOrder := make([]int, len(results))
	for i := range pprOrder {
		pprOrder[i] = i
	}
	sort.SliceStable(pprOrder, func(a, b int) bool {
		sa := pprScores[fmt.Sprintf("%d", results[pprOrder[a]].ID)]
		sb := pprScores[fmt.Sprintf("%d", results[pprOrder[b]].ID)]
		return sa > sb
	})
	pprPos := make([]int, len(results))
	for p, idx := range pprOrder {
		pprPos[idx] = p
	}

	// Position-only RRF fusion of incoming order and PPR order.
	fused := make([]float64, len(results))
	for i := range results {
		fused[i] = 1.0/(graphRRFConstant+float64(i+1)) + 1.0/(graphRRFConstant+float64(pprPos[i]+1))
	}

	idx := make([]int, len(results))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return fused[idx[a]] > fused[idx[b]]
	})

	out := make([]*domain.SearchResult, len(results))
	for p, i := range idx {
		out[p] = results[i]
	}
	return out
}

// gateSearchResults grades retrieval confidence without dropping results. The
// grade keeps the DefaultCRAGConfig thresholds so the remote-escalation decision
// that reads Grade/NeedsRefinement is unchanged; only the absolute score floor
// is disabled, because Rank is not on a single scale at this point and the
// floor silently removed old but relevant memories. Callers keep their full
// results slice and read only Grade, NeedsRefinement, and Confidence.
func gateSearchResults(results []*domain.SearchResult) retrieval.CRAGEvaluation {
	cfg := retrieval.DefaultCRAGConfig()
	cfg.MinScoreFloor = 0
	return retrieval.EvaluateCRAG(results, cfg)
}

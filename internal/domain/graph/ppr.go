// Package graph provides graph analytics, clustering, and structural code
// intelligence algorithms inspired by Graphify, ported natively to Go with zero-CGO.
package graph

import (
	"math"
	"sort"
)

// PPROptions configures the Personalized PageRank (HippoRAG) algorithm.
type PPROptions struct {
	// DampingFactor is the teleportation probability factor (typically 0.85).
	DampingFactor float64
	// MaxIterations is the maximum number of power iteration loops (typically 20).
	MaxIterations int
	// Tolerance is the convergence threshold (typically 1e-6).
	Tolerance float64
	// Directed determines if edges should be treated as strictly directed (Source -> Target).
	Directed bool
}

// DefaultPPROptions returns standard HippoRAG parameters.
func DefaultPPROptions() PPROptions {
	return PPROptions{
		DampingFactor: 0.85,
		MaxIterations: 20,
		Tolerance:     1e-6,
		Directed:      true,
	}
}

// ScoredNode represents a graph node scored by HippoRAG / Personalized PageRank.
type ScoredNode struct {
	NodeID string  `json:"node_id"`
	Score  float64 `json:"score"`
}

// ComputePersonalizedPageRank calculates the Personalized PageRank (PPR) distribution
// across the graph given an initial seed preference vector (HippoRAG activation).
//
// In HippoRAG, the seeds represent the initial lexical/vector retrieval hits, and
// the PageRank power iteration propagates activation through structural associations
// in memory and code graphs in O(E * iterations) time without any external LLM calls.
func ComputePersonalizedPageRank(
	nodes []GraphAnalyticsNode,
	edges []GraphAnalyticsEdge,
	seeds map[string]float64,
	opts PPROptions,
) map[string]float64 {
	if len(nodes) == 0 {
		return map[string]float64{}
	}

	if opts.DampingFactor <= 0 || opts.DampingFactor >= 1 {
		opts.DampingFactor = 0.85
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = 20
	}
	if opts.Tolerance <= 0 {
		opts.Tolerance = 1e-6
	}

	// 1. Build node index and normalize seed distribution
	nodeIndices := make(map[string]int, len(nodes))
	for i, n := range nodes {
		nodeIndices[n.ID] = i
	}

	n := len(nodes)
	seedVector := make([]float64, n)
	var seedSum float64

	for nodeID, weight := range seeds {
		if idx, ok := nodeIndices[nodeID]; ok && weight > 0 {
			seedVector[idx] = weight
			seedSum += weight
		}
	}

	// If no valid seeds were found, fall back to uniform teleportation
	if seedSum <= 0 {
		for i := 0; i < n; i++ {
			seedVector[i] = 1.0 / float64(n)
		}
	} else {
		for i := 0; i < n; i++ {
			seedVector[i] /= seedSum
		}
	}

	// 2. Build adjacency list and out-degree weights
	type edgeTarget struct {
		targetIdx int
		weight    float64
	}

	adj := make([][]edgeTarget, n)
	outWeightSum := make([]float64, n)

	for _, e := range edges {
		srcIdx, srcOk := nodeIndices[e.Source]
		tgtIdx, tgtOk := nodeIndices[e.Target]
		if !srcOk || !tgtOk {
			continue
		}
		w := e.Weight
		if w <= 0 {
			w = 1.0
		}
		adj[srcIdx] = append(adj[srcIdx], edgeTarget{targetIdx: tgtIdx, weight: w})
		outWeightSum[srcIdx] += w

		if !opts.Directed {
			adj[tgtIdx] = append(adj[tgtIdx], edgeTarget{targetIdx: srcIdx, weight: w})
			outWeightSum[tgtIdx] += w
		}
	}

	// 3. Initialize rank vector p(0) = seedVector
	p := make([]float64, n)
	copy(p, seedVector)

	alpha := opts.DampingFactor
	nextP := make([]float64, n)

	// 4. Power Iteration
	for iter := 0; iter < opts.MaxIterations; iter++ {
		// Initialize nextP with the restart / teleportation component: (1 - alpha) * seedVector
		for i := 0; i < n; i++ {
			nextP[i] = (1.0 - alpha) * seedVector[i]
		}

		// Distribute probability mass along graph edges
		var danglingSum float64
		for u := 0; u < n; u++ {
			if outWeightSum[u] > 0 {
				contrib := (alpha * p[u]) / outWeightSum[u]
				for _, edge := range adj[u] {
					nextP[edge.targetIdx] += contrib * edge.weight
				}
			} else {
				// Dangling node with no outgoing edges
				danglingSum += p[u]
			}
		}

		// Re-distribute dangling node mass to seed distribution
		if danglingSum > 0 {
			danglingContrib := alpha * danglingSum
			for i := 0; i < n; i++ {
				nextP[i] += danglingContrib * seedVector[i]
			}
		}

		// Check for convergence (L1 norm difference)
		var diff float64
		for i := 0; i < n; i++ {
			diff += math.Abs(nextP[i] - p[i])
			p[i] = nextP[i]
		}

		if diff < opts.Tolerance {
			break
		}
	}

	// 5. Build result map
	result := make(map[string]float64, n)
	for i, node := range nodes {
		result[node.ID] = p[i]
	}

	return result
}

// HippoRAGPropagate applies Personalized PageRank on graph nodes and returns top-K ranked nodes.
func HippoRAGPropagate(
	nodes []GraphAnalyticsNode,
	edges []GraphAnalyticsEdge,
	seeds map[string]float64,
	topK int,
) []ScoredNode {
	scores := ComputePersonalizedPageRank(nodes, edges, seeds, DefaultPPROptions())

	scored := make([]ScoredNode, 0, len(scores))
	for nodeID, score := range scores {
		scored = append(scored, ScoredNode{
			NodeID: nodeID,
			Score:  score,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].NodeID < scored[j].NodeID
		}
		return scored[i].Score > scored[j].Score
	})

	if topK > 0 && len(scored) > topK {
		scored = scored[:topK]
	}

	return scored
}

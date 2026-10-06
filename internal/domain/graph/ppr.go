// Package graph provides graph analytics, clustering, and structural code
// intelligence algorithms inspired by Graphify, ported natively to Go with zero-CGO.
package graph

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
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

// PassageSeed pairs one passage's dense retrieval similarity with its lexical
// hit score; both fuse into the PPR seed weight for that passage.
type PassageSeed struct {
	PassageID    int64
	DenseScore   float64
	LexicalScore float64
}

// HippoRAG2Request bundles one HippoRAG 2 retrieval pass: the heterogeneous
// knowledge graph, per-passage dense-plus-lexical evidence, and
// triple/concept match scores. A zero Options selects DefaultPPROptions.
type HippoRAG2Request struct {
	Nodes        []GraphAnalyticsNode
	Edges        []GraphAnalyticsEdge
	PassageSeeds []PassageSeed
	ConceptSeeds map[string]float64
	Options      PPROptions
}

// HippoRAG2Outcome reports the passage and concept score families from one
// PPR pass. Applied is false when the graph is empty, too sparse, or misses
// every seed; callers must then keep the dense-plus-lexical fusion path.
type HippoRAG2Outcome struct {
	PassageScores map[int64]float64
	ConceptScores map[string]float64
	Applied       bool
}

// RunHippoRAG2 fuses dense and lexical passage evidence into PPR seed weights
// and propagates activation across the knowledge graph. It never errors: a
// sparse or unreachable graph yields Applied=false with empty scores so the
// caller falls back to dense-plus-lexical fusion.
func RunHippoRAG2(req HippoRAG2Request) HippoRAG2Outcome {
	fallback := func() HippoRAG2Outcome {
		return HippoRAG2Outcome{
			PassageScores: map[int64]float64{},
			ConceptScores: map[string]float64{},
			Applied:       false,
		}
	}

	// A graph needs at least two nodes and one edge before propagation can
	// carry associative evidence beyond teleportation.
	if len(req.Nodes) < 2 || len(req.Edges) < 1 {
		return fallback()
	}

	passageSeeds := fusePassageSeeds(req.PassageSeeds)
	unifiedSeeds := unifyPPRSeeds(passageSeeds, req.ConceptSeeds)
	if !seedReachesGraph(req.Nodes, unifiedSeeds) {
		return fallback()
	}

	opts := req.Options
	if opts == (PPROptions{}) {
		opts = DefaultPPROptions()
	}

	passageScores, conceptScores := HippoRAG2Propagate(req.Nodes, req.Edges, passageSeeds, req.ConceptSeeds, opts)
	return HippoRAG2Outcome{
		PassageScores: passageScores,
		ConceptScores: conceptScores,
		Applied:       true,
	}
}

// fusePassageSeeds folds dense and lexical evidence into one seed weight per
// passage: dense similarity contributes exactly as much as lexical confidence,
// so vector hits seed propagation even without a lexical match.
func fusePassageSeeds(seeds []PassageSeed) map[int64]float64 {
	weights := make(map[int64]float64, len(seeds))
	for _, s := range seeds {
		dense := math.Max(s.DenseScore, 0)
		lexical := math.Max(s.LexicalScore, 0)
		weight := dense + lexical
		if weight <= 0 {
			continue
		}
		weights[s.PassageID] += weight
	}
	return weights
}

// seedReachesGraph reports whether at least one canonical seed ID aliases a
// node in the graph; otherwise PPR would degrade to uniform teleportation.
func seedReachesGraph(nodes []GraphAnalyticsNode, unifiedSeeds map[string]float64) bool {
	if len(unifiedSeeds) == 0 {
		return false
	}
	present := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		present[n.ID] = struct{}{}
	}
	for seedID := range unifiedSeeds {
		if _, ok := present[seedID]; ok {
			return true
		}
	}
	return false
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

// HippoRAG2Propagate executes Personalized PageRank over a heterogeneous bipartite/knowledge graph
// containing both observation/passage nodes (e.g., "obs:<id>" or "<id>") and symbol/entity nodes (e.g., "sym:<name>").
//
// Seeds can be provided as observation IDs (with initial lexical/vector scores) and/or symbol names.
// The algorithm runs PPR over the joint graph, allowing activation to flow from observations
// through the symbols they mention to other related symbols and back to dependent observations.
func HippoRAG2Propagate(
	nodes []GraphAnalyticsNode,
	edges []GraphAnalyticsEdge,
	observationSeeds map[int64]float64,
	symbolSeeds map[string]float64,
	opts PPROptions,
) (obsScores map[int64]float64, symScores map[string]float64) {
	allScores := ComputePersonalizedPageRank(nodes, edges, unifyPPRSeeds(observationSeeds, symbolSeeds), opts)
	return classifyPPRScores(allScores)
}

// unifyPPRSeeds registers every observation seed under its obs:, numeric, and
// passage: aliases, and every symbol seed under its bare and sym: forms, so
// seed weights reach nodes regardless of which canonical spelling a graph
// builder used.
func unifyPPRSeeds(observationSeeds map[int64]float64, symbolSeeds map[string]float64) map[string]float64 {
	unifiedSeeds := make(map[string]float64, len(observationSeeds)+len(symbolSeeds))

	for obsID, score := range observationSeeds {
		if score <= 0 {
			continue
		}
		id := strconv.FormatInt(obsID, 10)
		unifiedSeeds[fmt.Sprintf("obs:%s", id)] = score
		unifiedSeeds[id] = score
		unifiedSeeds[passageNodePrefix+id] = score
	}

	for sym, score := range symbolSeeds {
		if score <= 0 {
			continue
		}
		unifiedSeeds[sym] = score
		if !strings.HasPrefix(sym, "sym:") {
			unifiedSeeds["sym:"+sym] = score
		}
	}

	return unifiedSeeds
}

// classifyPPRScores splits a raw PPR distribution into passage-family scores
// (obs:, passage:, and bare numeric nodes) and concept/symbol scores.
func classifyPPRScores(allScores map[string]float64) (obsScores map[int64]float64, symScores map[string]float64) {
	obsScores = make(map[int64]float64)
	symScores = make(map[string]float64)

	for nodeID, score := range allScores {
		if strings.HasPrefix(nodeID, "obs:") {
			if id, err := strconv.ParseInt(strings.TrimPrefix(nodeID, "obs:"), 10, 64); err == nil {
				obsScores[id] = score
				continue
			}
		}
		if strings.HasPrefix(nodeID, passageNodePrefix) {
			if id, err := strconv.ParseInt(strings.TrimPrefix(nodeID, passageNodePrefix), 10, 64); err == nil {
				obsScores[id] = score
				continue
			}
		}
		if id, err := strconv.ParseInt(nodeID, 10, 64); err == nil {
			// Numeric ID can be an observation
			obsScores[id] = score
			continue
		}
		// Otherwise treated as symbol
		cleanSym := strings.TrimPrefix(nodeID, "sym:")
		symScores[cleanSym] = score
		symScores[nodeID] = score
	}

	return obsScores, symScores
}

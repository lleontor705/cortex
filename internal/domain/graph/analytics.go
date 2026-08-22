// Package graph provides graph analytics, clustering, and structural code
// intelligence algorithms inspired by Graphify, ported natively to Go with zero-CGO.
package graph

import (
	"sort"
	"strings"
)

// GraphAnalyticsNode represents a generic node for graph analysis.
type GraphAnalyticsNode struct {
	ID         string         `json:"id"`
	Label      string         `json:"label"`
	Kind       string         `json:"kind"`
	Subtype    string         `json:"subtype,omitempty"`
	SourceFile string         `json:"source_file,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// GraphAnalyticsEdge represents an edge for graph analysis.
type GraphAnalyticsEdge struct {
	ID         string  `json:"id"`
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Type       string  `json:"type"`
	Weight     float64 `json:"weight"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning,omitempty"`
}

// Community represents a functional cluster of tightly-coupled nodes.
type Community struct {
	ID            int      `json:"id"`
	Label         string   `json:"label"`
	HubNodeID     string   `json:"hub_node_id"`
	Members       []string `json:"members"`
	Size          int      `json:"size"`
	CohesionScore float64  `json:"cohesion_score"`
}

// GodNode represents an architectural bottleneck with disproportionate connectivity.
type GodNode struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Degree     int    `json:"degree"`
	InDegree   int    `json:"in_degree"`
	OutDegree  int    `json:"out_degree"`
	SourceFile string `json:"source_file,omitempty"`
}

// SurprisingConnection represents a non-obvious bridge across distinct domains or peripheral-to-hub coupling.
type SurprisingConnection struct {
	SourceNode   string   `json:"source_node"`
	TargetNode   string   `json:"target_node"`
	RelationType string   `json:"relation_type"`
	Score        int      `json:"score"`
	Reasons      []string `json:"reasons"`
}

// DependencyCycle represents a circular dependency chain.
type DependencyCycle struct {
	Length int      `json:"length"`
	Nodes  []string `json:"nodes"`
}

// BlastRadiusResult represents the impacted symbols and files when a node changes.
type BlastRadiusResult struct {
	RootNode       string   `json:"root_node"`
	DirectImpact   []string `json:"direct_impact"`
	TotalImpacted  []string `json:"total_impacted"`
	ImpactedFiles  []string `json:"impacted_files"`
	BlastRadiusPct float64  `json:"blast_radius_pct"`
}

// GraphAnalyticsReport is the aggregated health and structural intelligence report.
type GraphAnalyticsReport struct {
	TotalNodes            int                    `json:"total_nodes"`
	TotalEdges            int                    `json:"total_edges"`
	Density               float64                `json:"density"`
	Communities           []Community            `json:"communities"`
	GodNodes              []GodNode              `json:"god_nodes"`
	SurprisingConnections []SurprisingConnection `json:"surprising_connections"`
	Cycles                []DependencyCycle      `json:"cycles"`
}

// Noise labels to ignore in god node rankings
var builtinNoiseLabels = map[string]bool{
	"string": true, "int": true, "bool": true, "error": true, "any": true,
	"Context": true, "context.Context": true, "fmt": true, "errors": true,
	"map": true, "slice": true, "nil": true, "struct": true,
}

// AnalyzeGraph performs full structural analysis and community detection.
func AnalyzeGraph(nodes []GraphAnalyticsNode, edges []GraphAnalyticsEdge) *GraphAnalyticsReport {
	report := &GraphAnalyticsReport{
		TotalNodes:            len(nodes),
		TotalEdges:            len(edges),
		Communities:           DetectCommunities(nodes, edges),
		GodNodes:              FindGodNodes(nodes, edges, 10),
		SurprisingConnections: FindSurprisingConnections(nodes, edges, 5),
		Cycles:                FindCycles(nodes, edges),
	}

	if len(nodes) > 1 {
		maxPossibleEdges := float64(len(nodes) * (len(nodes) - 1))
		report.Density = float64(len(edges)) / maxPossibleEdges
	}

	return report
}

// DetectCommunities partitions nodes into clusters using connected components + Hub labeling.
func DetectCommunities(nodes []GraphAnalyticsNode, edges []GraphAnalyticsEdge) []Community {
	nodeMap := make(map[string]GraphAnalyticsNode, len(nodes))
	degrees := make(map[string]int, len(nodes))
	adj := make(map[string][]string, len(nodes))

	for _, n := range nodes {
		nodeMap[n.ID] = n
	}
	for _, e := range edges {
		degrees[e.Source]++
		degrees[e.Target]++
		adj[e.Source] = append(adj[e.Source], e.Target)
		adj[e.Target] = append(adj[e.Target], e.Source)
	}

	visited := make(map[string]bool)
	var communities []Community
	cid := 1

	// Deterministic sorting of nodes
	sortedNodeIDs := make([]string, 0, len(nodes))
	for _, n := range nodes {
		sortedNodeIDs = append(sortedNodeIDs, n.ID)
	}
	sort.Strings(sortedNodeIDs)

	for _, startID := range sortedNodeIDs {
		if visited[startID] {
			continue
		}

		var members []string
		queue := []string{startID}
		visited[startID] = true

		hubID := startID
		maxDeg := degrees[startID]

		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			members = append(members, curr)

			if deg := degrees[curr]; deg > maxDeg {
				maxDeg = deg
				hubID = curr
			}

			for _, neighbor := range adj[curr] {
				if !visited[neighbor] {
					visited[neighbor] = true
					queue = append(queue, neighbor)
				}
			}
		}

		hubLabel := nodeMap[hubID].Label
		if hubLabel == "" {
			hubLabel = hubID
		}

		// Calculate cohesion score (internal edges / possible internal edges)
		internalEdges := 0
		memberSet := make(map[string]bool, len(members))
		for _, m := range members {
			memberSet[m] = true
		}
		for _, e := range edges {
			if memberSet[e.Source] && memberSet[e.Target] {
				internalEdges++
			}
		}

		cohesion := 1.0
		if len(members) > 1 {
			possible := float64(len(members) * (len(members) - 1) / 2)
			cohesion = float64(internalEdges) / possible
		}

		communities = append(communities, Community{
			ID:            cid,
			Label:         hubLabel,
			HubNodeID:     hubID,
			Members:       members,
			Size:          len(members),
			CohesionScore: cohesion,
		})
		cid++
	}

	// Sort communities by size descending
	sort.Slice(communities, func(i, j int) bool {
		return communities[i].Size > communities[j].Size
	})

	return communities
}

// FindGodNodes identifies the most central, highly connected architectural entities.
func FindGodNodes(nodes []GraphAnalyticsNode, edges []GraphAnalyticsEdge, topN int) []GodNode {
	if topN <= 0 {
		topN = 10
	}

	inDeg := make(map[string]int)
	outDeg := make(map[string]int)
	nodeMap := make(map[string]GraphAnalyticsNode)

	for _, n := range nodes {
		nodeMap[n.ID] = n
	}
	for _, e := range edges {
		outDeg[e.Source]++
		inDeg[e.Target]++
	}

	var candidates []GodNode
	for _, n := range nodes {
		if builtinNoiseLabels[n.Label] || strings.HasPrefix(n.Label, ".") {
			continue
		}
		total := inDeg[n.ID] + outDeg[n.ID]
		if total > 0 {
			candidates = append(candidates, GodNode{
				ID:         n.ID,
				Label:      n.Label,
				Degree:     total,
				InDegree:   inDeg[n.ID],
				OutDegree:  outDeg[n.ID],
				SourceFile: n.SourceFile,
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Degree == candidates[j].Degree {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].Degree > candidates[j].Degree
	})

	if len(candidates) > topN {
		return candidates[:topN]
	}
	return candidates
}

// FindSurprisingConnections scores and identifies unexpected cross-domain or peripheral-to-hub coupling.
func FindSurprisingConnections(nodes []GraphAnalyticsNode, edges []GraphAnalyticsEdge, topN int) []SurprisingConnection {
	if topN <= 0 {
		topN = 5
	}

	nodeMap := make(map[string]GraphAnalyticsNode)
	degrees := make(map[string]int)
	for _, n := range nodes {
		nodeMap[n.ID] = n
	}
	for _, e := range edges {
		degrees[e.Source]++
		degrees[e.Target]++
	}

	var results []SurprisingConnection
	for _, e := range edges {
		if e.Type == "imports" || e.Type == "defines" {
			continue
		}

		u := nodeMap[e.Source]
		v := nodeMap[e.Target]

		score := 0
		var reasons []string

		// 1. Cross file/package connection
		if u.SourceFile != "" && v.SourceFile != "" && u.SourceFile != v.SourceFile {
			score += 2
			reasons = append(reasons, "Cruza límites de archivos/módulos distintos")
		}

		// 2. Peripheral to God Node
		degU := degrees[e.Source]
		degV := degrees[e.Target]
		if (degU <= 2 && degV >= 6) || (degV <= 2 && degU >= 6) {
			score += 3
			reasons = append(reasons, "Nodo periférico acoplado directamente a un hub central")
		}

		// 3. Contradiction or supersedes relation
		if e.Type == "contradicts" || e.Type == "supersedes" {
			score += 3
			reasons = append(reasons, "Relación lógica crítica ("+e.Type+")")
		}

		if score > 0 {
			results = append(results, SurprisingConnection{
				SourceNode:   e.Source,
				TargetNode:   e.Target,
				RelationType: e.Type,
				Score:        score,
				Reasons:      reasons,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > topN {
		return results[:topN]
	}
	return results
}

// FindCycles detects circular dependencies using Tarjan's strongly connected components algorithm.
func FindCycles(nodes []GraphAnalyticsNode, edges []GraphAnalyticsEdge) []DependencyCycle {
	adj := make(map[string][]string)
	for _, e := range edges {
		if e.Type == "imports" || e.Type == "calls" || e.Type == "uses" {
			adj[e.Source] = append(adj[e.Source], e.Target)
		}
	}

	var cycles []DependencyCycle
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	var path []string

	var dfs func(u string)
	dfs = func(u string) {
		visited[u] = true
		recStack[u] = true
		path = append(path, u)

		for _, v := range adj[u] {
			if !visited[v] {
				dfs(v)
			} else if recStack[v] {
				// Found a cycle
				var cycleNodes []string
				cycleStart := false
				for _, p := range path {
					if p == v {
						cycleStart = true
					}
					if cycleStart {
						cycleNodes = append(cycleNodes, p)
					}
				}
				cycleNodes = append(cycleNodes, v)
				if len(cycleNodes) > 1 {
					cycles = append(cycles, DependencyCycle{
						Length: len(cycleNodes) - 1,
						Nodes:  cycleNodes,
					})
				}
			}
		}

		path = path[:len(path)-1]
		recStack[u] = false
	}

	for _, n := range nodes {
		if !visited[n.ID] {
			dfs(n.ID)
		}
	}

	return cycles
}

// CalculateBlastRadius computes all downstream and upstream nodes impacted by changing a symbol.
func CalculateBlastRadius(rootNodeID string, nodes []GraphAnalyticsNode, edges []GraphAnalyticsEdge, maxHops int) *BlastRadiusResult {
	if maxHops <= 0 {
		maxHops = 3
	}

	nodeMap := make(map[string]GraphAnalyticsNode)
	reverseAdj := make(map[string][]string) // incoming callers / dependents

	for _, n := range nodes {
		nodeMap[n.ID] = n
	}
	for _, e := range edges {
		reverseAdj[e.Target] = append(reverseAdj[e.Target], e.Source)
	}

	visited := make(map[string]bool)
	var directImpact []string
	var totalImpacted []string
	impactedFileSet := make(map[string]bool)

	visited[rootNodeID] = true
	queue := []struct {
		id  string
		hop int
	}{{id: rootNodeID, hop: 0}}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		if curr.id != rootNodeID {
			totalImpacted = append(totalImpacted, curr.id)
			if curr.hop == 1 {
				directImpact = append(directImpact, curr.id)
			}
			if f := nodeMap[curr.id].SourceFile; f != "" {
				impactedFileSet[f] = true
			}
		}

		if curr.hop < maxHops {
			for _, caller := range reverseAdj[curr.id] {
				if !visited[caller] {
					visited[caller] = true
					queue = append(queue, struct {
						id  string
						hop int
					}{id: caller, hop: curr.hop + 1})
				}
			}
		}
	}

	var impactedFiles []string
	for f := range impactedFileSet {
		impactedFiles = append(impactedFiles, f)
	}
	sort.Strings(impactedFiles)

	pct := 0.0
	if len(nodes) > 0 {
		pct = (float64(len(totalImpacted)) / float64(len(nodes))) * 100.0
	}

	return &BlastRadiusResult{
		RootNode:       rootNodeID,
		DirectImpact:   directImpact,
		TotalImpacted:  totalImpacted,
		ImpactedFiles:  impactedFiles,
		BlastRadiusPct: pct,
	}
}

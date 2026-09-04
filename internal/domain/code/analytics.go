package code

import (
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Noise symbols to exclude from god nodes (matches Graphify standards).
var builtinNoise = map[string]bool{
	"str": true, "int": true, "float": true, "bool": true, "string": true,
	"byte": true, "error": true, "nil": true, "true": true, "false": true,
	"object": true, "any": true, "context": true, "fmt": true, "time": true,
	"os": true, "io": true, "log": true, "sync": true, "math": true,
	"test": true, "mock": true, "json": true, "http": true,
}

// ComputeAnalytics runs structural graph diagnostics on a project's CodeGraph.
func ComputeAnalytics(graph *CodeGraph) *AnalyticsReport {
	report := &AnalyticsReport{
		Project:        graph.Project,
		TotalSymbols:   len(graph.Symbols),
		TotalRelations: len(graph.Relations),
		GeneratedAt:    time.Now().UTC(),
	}

	if len(graph.Symbols) == 0 {
		return report
	}

	// 1. Map symbols and calculate file count
	symbolMap := make(map[string]Symbol, len(graph.Symbols))
	fileSet := make(map[string]bool)
	for _, s := range graph.Symbols {
		symbolMap[s.ID] = s
		if s.FilePath != "" {
			fileSet[s.FilePath] = true
		}
	}
	report.TotalFiles = len(fileSet)

	// 2. Degree Centrality (InDegree, OutDegree, Total)
	inDegree := make(map[string]int)
	outDegree := make(map[string]int)
	adjList := make(map[string][]string) // For cycle detection

	for _, r := range graph.Relations {
		outDegree[r.SourceID]++
		inDegree[r.TargetID]++
		adjList[r.SourceID] = append(adjList[r.SourceID], r.TargetID)
	}

	// 3. Identify God Nodes (Core Architectural Hubs)
	godNodes := make([]GodNode, 0)
	for _, s := range graph.Symbols {
		lowerName := strings.ToLower(s.Name)
		if builtinNoise[lowerName] {
			continue
		}
		in := inDegree[s.ID]
		out := outDegree[s.ID]
		total := in + out
		if total < 2 {
			continue
		}

		// Score combines in-degree (dependents/fan-in) and out-degree (fan-out)
		score := float64(in)*1.5 + float64(out)*1.0
		godNodes = append(godNodes, GodNode{
			ID:        s.ID,
			Name:      s.Name,
			Kind:      s.Kind,
			FilePath:  s.FilePath,
			Degree:    total,
			InDegree:  in,
			OutDegree: out,
			Score:     math.Round(score*100) / 100,
		})
	}

	sort.Slice(godNodes, func(i, j int) bool {
		if godNodes[i].Score == godNodes[j].Score {
			return godNodes[i].Name < godNodes[j].Name
		}
		return godNodes[i].Score > godNodes[j].Score
	})

	if len(godNodes) > 10 {
		report.GodNodes = godNodes[:10]
	} else {
		report.GodNodes = godNodes
	}

	// 4. Detect Import / Call Cycles (Tarjan's algorithm / DFS)
	report.ImportCycles = detectCycles(graph.Symbols, adjList, symbolMap)

	// 5. Package / Module Community Cohesion Scoring
	report.Communities, report.AverageCohesion = calculateCommunities(graph.Symbols, graph.Relations)

	return report
}

// detectCycles searches for circular dependencies in the code graph.
func detectCycles(symbols []Symbol, adj map[string][]string, symMap map[string]Symbol) []ImportCycle {
	visited := make(map[string]int) // 0: unvisited, 1: visiting (in stack), 2: visited
	var cycles []ImportCycle
	path := make([]string, 0)

	var dfs func(u string)
	dfs = func(u string) {
		visited[u] = 1
		path = append(path, u)

		for _, v := range adj[u] {
			if len(cycles) >= 10 {
				return // Cap at 10 cycles
			}
			if visited[v] == 1 {
				// Found cycle: reconstruct cycle path
				cycleNodes := make([]string, 0)
				fileSet := make(map[string]bool)
				startIndex := -1
				for i, node := range path {
					if node == v {
						startIndex = i
						break
					}
				}
				if startIndex >= 0 {
					for i := startIndex; i < len(path); i++ {
						nodeID := path[i]
						sym := symMap[nodeID]
						name := sym.Name
						if name == "" {
							name = nodeID
						}
						cycleNodes = append(cycleNodes, name)
						if sym.FilePath != "" {
							fileSet[sym.FilePath] = true
						}
					}
					cycleNodes = append(cycleNodes, symMap[v].Name) // Complete loop

					files := make([]string, 0, len(fileSet))
					for f := range fileSet {
						files = append(files, f)
					}
					sort.Strings(files)

					if len(files) > 1 { // Only record cross-file or meaningful cycles
						cycles = append(cycles, ImportCycle{
							ID:        strings.Join(cycleNodes, " -> "),
							Files:     files,
							CyclePath: cycleNodes,
						})
					}
				}
			} else if visited[v] == 0 {
				dfs(v)
			}
		}

		path = path[:len(path)-1]
		visited[u] = 2
	}

	for _, s := range symbols {
		if visited[s.ID] == 0 {
			dfs(s.ID)
		}
	}

	return cycles
}

// calculateCommunities groups symbols by package/module and calculates cohesion.
func calculateCommunities(symbols []Symbol, relations []Relation) ([]CommunityCohesion, float64) {
	pkgGroups := make(map[string][]string)
	for _, s := range symbols {
		pkg := s.PackageName
		if pkg == "" && s.FilePath != "" {
			parts := strings.Split(s.FilePath, "/")
			if len(parts) > 1 {
				pkg = parts[len(parts)-2]
			} else {
				pkg = "root"
			}
		}
		if pkg == "" {
			pkg = "default"
		}
		pkgGroups[pkg] = append(pkgGroups[pkg], s.ID)
	}

	// Calculate internal edges per package
	nodePkg := make(map[string]string)
	for pkg, ids := range pkgGroups {
		for _, id := range ids {
			nodePkg[id] = pkg
		}
	}

	internalEdges := make(map[string]int)
	for _, r := range relations {
		srcPkg := nodePkg[r.SourceID]
		tgtPkg := nodePkg[r.TargetID]
		if srcPkg != "" && srcPkg == tgtPkg {
			internalEdges[srcPkg]++
		}
	}

	var communities []CommunityCohesion
	cid := 0
	totalScore := 0.0

	for pkg, members := range pkgGroups {
		n := len(members)
		if n < 2 {
			continue
		}
		possible := n * (n - 1) / 2
		actual := internalEdges[pkg]
		score := 0.0
		if possible > 0 {
			score = float64(actual) / float64(possible)
		}
		if score > 1.0 {
			score = 1.0
		}
		score = math.Round(score*1000) / 1000

		communities = append(communities, CommunityCohesion{
			CommunityID:   cid,
			Label:         pkg,
			Members:       members,
			InternalEdges: actual,
			PossibleEdges: possible,
			CohesionScore: score,
		})
		totalScore += score
		cid++
	}

	sort.Slice(communities, func(i, j int) bool {
		return len(communities[i].Members) > len(communities[j].Members)
	})

	avg := 0.0
	if len(communities) > 0 {
		avg = math.Round((totalScore/float64(len(communities)))*1000) / 1000
	}

	return communities, avg
}

// CalculateCodeBlastRadius computes callers and impacted files when a symbol or file is modified.
func CalculateCodeBlastRadius(graph *CodeGraph, target string, maxHops int) *CodeBlastRadius {
	if graph == nil || len(graph.Symbols) == 0 {
		return &CodeBlastRadius{
			Target:         target,
			TargetKind:     "unknown",
			DirectCallers:  []string{},
			ImpactedFiles:  []string{},
			TotalImpacted:  []string{},
			BlastRadiusPct: 0,
		}
	}
	if maxHops <= 0 {
		maxHops = 3
	}

	normTarget := strings.TrimSpace(target)
	normTargetSlash := filepath.ToSlash(normTarget)

	symMap := make(map[string]Symbol, len(graph.Symbols))
	fileMap := make(map[string][]string) // slashFilePath -> symbolIDs
	nameMap := make(map[string][]string) // name -> symbolIDs

	for _, s := range graph.Symbols {
		symMap[s.ID] = s
		fSlash := filepath.ToSlash(s.FilePath)
		if fSlash != "" {
			fileMap[fSlash] = append(fileMap[fSlash], s.ID)
		}
		nameMap[s.Name] = append(nameMap[s.Name], s.ID)
	}

	targetKind := "symbol"
	var rootIDs []string

	// Check if target is a file path
	if ids, ok := fileMap[normTargetSlash]; ok {
		targetKind = "file"
		rootIDs = append(rootIDs, ids...)
	} else {
		// Check suffix match on file path (e.g. "service.go" matching "internal/domain/service.go")
		for fSlash, ids := range fileMap {
			if strings.HasSuffix(fSlash, "/"+normTargetSlash) || fSlash == normTargetSlash {
				targetKind = "file"
				rootIDs = append(rootIDs, ids...)
			}
		}
	}

	// If not a file, check if target is an exact symbol ID
	if len(rootIDs) == 0 {
		if _, ok := symMap[normTarget]; ok {
			targetKind = "symbol"
			rootIDs = append(rootIDs, normTarget)
		}
	}

	// Check by symbol Name
	if len(rootIDs) == 0 {
		if ids, ok := nameMap[normTarget]; ok {
			targetKind = "symbol"
			rootIDs = append(rootIDs, ids...)
		}
	}

	// If still empty, try case-insensitive symbol match
	if len(rootIDs) == 0 {
		for _, s := range graph.Symbols {
			if strings.EqualFold(s.Name, normTarget) || strings.EqualFold(s.ID, normTarget) {
				rootIDs = append(rootIDs, s.ID)
			}
		}
	}

	if len(rootIDs) == 0 {
		return &CodeBlastRadius{
			Target:         target,
			TargetKind:     "unknown",
			DirectCallers:  []string{},
			ImpactedFiles:  []string{},
			TotalImpacted:  []string{},
			BlastRadiusPct: 0,
		}
	}

	return computeBlastFromRoots(graph, target, targetKind, nil, rootIDs, maxHops, symMap)
}

// CalculateDiffBlastRadius computes the aggregate impact of a list of changed files.
func CalculateDiffBlastRadius(graph *CodeGraph, modifiedFiles []string, maxHops int) *CodeBlastRadius {
	if graph == nil || len(graph.Symbols) == 0 || len(modifiedFiles) == 0 {
		return &CodeBlastRadius{
			Target:         "git_diff",
			TargetKind:     "git_diff",
			ChangedFiles:   modifiedFiles,
			DirectCallers:  []string{},
			ImpactedFiles:  []string{},
			TotalImpacted:  []string{},
			BlastRadiusPct: 0,
		}
	}
	if maxHops <= 0 {
		maxHops = 3
	}

	symMap := make(map[string]Symbol, len(graph.Symbols))
	fileMap := make(map[string][]string)

	for _, s := range graph.Symbols {
		symMap[s.ID] = s
		fSlash := filepath.ToSlash(s.FilePath)
		if fSlash != "" {
			fileMap[fSlash] = append(fileMap[fSlash], s.ID)
		}
	}

	var rootIDs []string
	matchedFilesSet := make(map[string]bool)

	for _, rawFile := range modifiedFiles {
		fSlash := filepath.ToSlash(strings.TrimSpace(rawFile))
		if ids, ok := fileMap[fSlash]; ok {
			rootIDs = append(rootIDs, ids...)
			matchedFilesSet[fSlash] = true
			continue
		}
		// Match suffix
		for indexedFile, ids := range fileMap {
			if strings.HasSuffix(indexedFile, "/"+fSlash) || strings.HasSuffix(fSlash, "/"+indexedFile) {
				rootIDs = append(rootIDs, ids...)
				matchedFilesSet[indexedFile] = true
			}
		}
	}

	var matchedFiles []string
	for f := range matchedFilesSet {
		matchedFiles = append(matchedFiles, f)
	}
	sort.Strings(matchedFiles)

	return computeBlastFromRoots(graph, "git_diff", "git_diff", modifiedFiles, rootIDs, maxHops, symMap)
}

func computeBlastFromRoots(
	graph *CodeGraph,
	target string,
	targetKind string,
	changedFiles []string,
	rootIDs []string,
	maxHops int,
	symMap map[string]Symbol,
) *CodeBlastRadius {
	// Reverse adjacency: caller/source -> callee/target.
	// When target is modified, caller/source is impacted!
	reverseAdj := make(map[string][]string)
	forwardAdj := make(map[string][]string) // For cycle detection

	for _, r := range graph.Relations {
		// r.SourceID calls or depends on r.TargetID
		reverseAdj[r.TargetID] = append(reverseAdj[r.TargetID], r.SourceID)
		forwardAdj[r.SourceID] = append(forwardAdj[r.SourceID], r.TargetID)
	}

	visited := make(map[string]bool)
	isRoot := make(map[string]bool)
	for _, id := range rootIDs {
		visited[id] = true
		isRoot[id] = true
	}

	queue := make([]struct {
		id  string
		hop int
	}, 0, len(rootIDs))

	for _, id := range rootIDs {
		queue = append(queue, struct {
			id  string
			hop int
		}{id: id, hop: 0})
	}

	var directCallers []string
	var totalImpacted []string
	impactedFileSet := make(map[string]bool)
	directCallerSet := make(map[string]bool)
	totalImpactedSet := make(map[string]bool)

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		if !isRoot[curr.id] {
			if !totalImpactedSet[curr.id] {
				totalImpactedSet[curr.id] = true
				totalImpacted = append(totalImpacted, curr.id)
			}
			if curr.hop == 1 && !directCallerSet[curr.id] {
				directCallerSet[curr.id] = true
				directCallers = append(directCallers, curr.id)
			}
			if s, ok := symMap[curr.id]; ok && s.FilePath != "" {
				impactedFileSet[s.FilePath] = true
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
	sort.Strings(directCallers)
	sort.Strings(totalImpacted)

	pct := 0.0
	if len(graph.Symbols) > 0 {
		pct = math.Round((float64(len(totalImpacted))/float64(len(graph.Symbols)))*10000) / 100.0
	}

	// Check if any root or impacted symbols are in cycles
	allCycles := detectCycles(graph.Symbols, forwardAdj, symMap)
	var affectedCycles []ImportCycle
	for _, c := range allCycles {
		hasCycleImpact := false
		for _, f := range c.Files {
			if impactedFileSet[f] {
				hasCycleImpact = true
				break
			}
			for _, id := range rootIDs {
				if sym, ok := symMap[id]; ok && sym.FilePath == f {
					hasCycleImpact = true
					break
				}
			}
		}
		if hasCycleImpact {
			affectedCycles = append(affectedCycles, c)
		}
	}

	return &CodeBlastRadius{
		Target:         target,
		TargetKind:     targetKind,
		RootNode:       target,
		ChangedFiles:   changedFiles,
		DirectCallers:  directCallers,
		ImpactedFiles:  impactedFiles,
		TotalImpacted:  totalImpacted,
		BlastRadiusPct: pct,
		AffectedCycles: affectedCycles,
	}
}

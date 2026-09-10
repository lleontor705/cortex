package code

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// ImpactedTestFunction represents a test function identified in the code graph.
type ImpactedTestFunction struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	FilePath   string `json:"file_path"`
	LineNumber int    `json:"line_number"`
	Hops       int    `json:"hops"`   // 1 = direct caller, 2+ = transitive
	Direct     bool   `json:"direct"` // true if directly calling target
}

// TestImpactResult summarizes test impact analysis.
type TestImpactResult struct {
	Target              string                 `json:"target"`
	TargetKind          string                 `json:"target_kind"` // "symbol", "file", "git_diff"
	ImpactedTestFiles   []string               `json:"impacted_test_files"`
	ImpactedTestFuncs   []ImpactedTestFunction `json:"impacted_test_functions"`
	RecommendedCommands []string               `json:"recommended_commands"`
}

// IsTestFilePath checks if a file path is a known test file convention across languages.
func IsTestFilePath(path string) bool {
	f := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(f)

	// Go
	if strings.HasSuffix(base, "_test.go") {
		return true
	}
	// Python
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
		return true
	}
	if strings.HasSuffix(base, "_test.py") {
		return true
	}
	// JS / TS
	if strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".test.tsx") ||
		strings.HasSuffix(base, ".spec.ts") || strings.HasSuffix(base, ".spec.tsx") ||
		strings.HasSuffix(base, ".test.js") || strings.HasSuffix(base, ".test.jsx") ||
		strings.HasSuffix(base, ".spec.js") || strings.HasSuffix(base, ".spec.jsx") {
		return true
	}
	// Java / Kotlin / C#
	if strings.HasSuffix(base, "test.java") || strings.HasSuffix(base, "tests.java") ||
		strings.HasSuffix(base, "test.kt") || strings.HasSuffix(base, "tests.kt") ||
		strings.HasSuffix(base, "test.cs") || strings.HasSuffix(base, "tests.cs") {
		return true
	}
	// Rust
	if strings.Contains(f, "/tests/") && strings.HasSuffix(base, ".rs") {
		return true
	}
	return false
}

// FindImpactedTests traverses the reverse dependency graph to identify all test
// suites and functions that exercise the target symbol, file, or git diff.
func FindImpactedTests(graph *CodeGraph, target string, maxHops int) *TestImpactResult {
	result := &TestImpactResult{
		Target:              target,
		TargetKind:          "unknown",
		ImpactedTestFiles:   []string{},
		ImpactedTestFuncs:   []ImpactedTestFunction{},
		RecommendedCommands: []string{},
	}

	if graph == nil || len(graph.Symbols) == 0 {
		return result
	}
	if maxHops <= 0 {
		maxHops = 3
	}

	normTarget := strings.TrimSpace(target)
	normTargetSlash := filepath.ToSlash(normTarget)

	symMap := make(map[string]Symbol, len(graph.Symbols))
	fileMap := make(map[string][]string)
	nameMap := make(map[string][]string)

	for _, s := range graph.Symbols {
		symMap[s.ID] = s
		fSlash := filepath.ToSlash(s.FilePath)
		if fSlash != "" {
			fileMap[fSlash] = append(fileMap[fSlash], s.ID)
		}
		nameMap[s.Name] = append(nameMap[s.Name], s.ID)
	}

	var rootIDs []string
	targetKind := "symbol"

	// Match as file
	if ids, ok := fileMap[normTargetSlash]; ok {
		targetKind = "file"
		rootIDs = append(rootIDs, ids...)
	} else {
		for fSlash, ids := range fileMap {
			if strings.HasSuffix(fSlash, "/"+normTargetSlash) || fSlash == normTargetSlash {
				targetKind = "file"
				rootIDs = append(rootIDs, ids...)
			}
		}
	}

	// Match as symbol ID or name
	if len(rootIDs) == 0 {
		if _, ok := symMap[normTarget]; ok {
			targetKind = "symbol"
			rootIDs = append(rootIDs, normTarget)
		} else if ids, ok := nameMap[normTarget]; ok {
			targetKind = "symbol"
			rootIDs = append(rootIDs, ids...)
		} else {
			for _, s := range graph.Symbols {
				if strings.EqualFold(s.Name, normTarget) || strings.EqualFold(s.ID, normTarget) {
					rootIDs = append(rootIDs, s.ID)
				}
			}
		}
	}

	result.TargetKind = targetKind
	if len(rootIDs) == 0 {
		return result
	}

	// Reverse adjacency: callee -> callers
	reverseAdj := make(map[string][]string)
	for _, r := range graph.Relations {
		reverseAdj[r.TargetID] = append(reverseAdj[r.TargetID], r.SourceID)
	}

	visited := make(map[string]bool)
	for _, id := range rootIDs {
		visited[id] = true
	}

	type queueItem struct {
		id  string
		hop int
	}
	queue := make([]queueItem, 0, len(rootIDs))
	for _, id := range rootIDs {
		queue = append(queue, queueItem{id: id, hop: 0})
	}

	impactedFilesSet := make(map[string]bool)
	var impactedTestFuncs []ImpactedTestFunction
	seenFuncs := make(map[string]bool)

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		sym, hasSym := symMap[curr.id]
		if hasSym && IsTestFilePath(sym.FilePath) {
			impactedFilesSet[sym.FilePath] = true
			if !seenFuncs[sym.ID] && isCallableTestSymbol(sym) {
				seenFuncs[sym.ID] = true
				impactedTestFuncs = append(impactedTestFuncs, ImpactedTestFunction{
					ID:         sym.ID,
					Name:       sym.Name,
					FilePath:   sym.FilePath,
					LineNumber: sym.LineNumber,
					Hops:       curr.hop,
					Direct:     curr.hop == 1,
				})
			}
		}

		if curr.hop >= maxHops {
			continue
		}

		for _, callerID := range reverseAdj[curr.id] {
			if !visited[callerID] {
				visited[callerID] = true
				queue = append(queue, queueItem{id: callerID, hop: curr.hop + 1})
			}
		}
	}

	// Sort files and functions
	var testFiles []string
	for f := range impactedFilesSet {
		testFiles = append(testFiles, f)
	}
	sort.Strings(testFiles)

	sort.Slice(impactedTestFuncs, func(i, j int) bool {
		if impactedTestFuncs[i].Direct != impactedTestFuncs[j].Direct {
			return impactedTestFuncs[i].Direct // Direct first
		}
		if impactedTestFuncs[i].FilePath != impactedTestFuncs[j].FilePath {
			return impactedTestFuncs[i].FilePath < impactedTestFuncs[j].FilePath
		}
		return impactedTestFuncs[i].LineNumber < impactedTestFuncs[j].LineNumber
	})

	result.ImpactedTestFiles = testFiles
	result.ImpactedTestFuncs = impactedTestFuncs
	result.RecommendedCommands = BuildTestRunCommands(testFiles, impactedTestFuncs)

	return result
}

// isCallableTestSymbol checks if the symbol is an actual test case.
func isCallableTestSymbol(sym Symbol) bool {
	if sym.Kind != KindFunc && sym.Kind != KindMethod {
		return false
	}
	name := sym.Name
	if strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "test_") ||
		strings.HasSuffix(name, "Test") || strings.HasSuffix(name, "Tests") {
		return true
	}
	return false
}

// BuildTestRunCommands generates actionable, minimal test commands per language.
func BuildTestRunCommands(testFiles []string, testFuncs []ImpactedTestFunction) []string {
	if len(testFiles) == 0 && len(testFuncs) == 0 {
		return nil
	}

	commands := make([]string, 0)

	// Group by language / directory
	goDirs := make(map[string][]string) // dir -> funcNames
	tsFiles := make([]string, 0)
	pyFiles := make([]string, 0)

	for _, f := range testFiles {
		fSlash := filepath.ToSlash(f)
		if strings.HasSuffix(fSlash, "_test.go") {
			dir := filepath.ToSlash(filepath.Dir(fSlash))
			if !strings.HasPrefix(dir, "./") && !strings.HasPrefix(dir, "/") {
				dir = "./" + dir
			}
			if _, ok := goDirs[dir]; !ok {
				goDirs[dir] = []string{}
			}
		} else if strings.HasSuffix(fSlash, ".test.ts") || strings.HasSuffix(fSlash, ".spec.ts") ||
			strings.HasSuffix(fSlash, ".test.js") || strings.HasSuffix(fSlash, ".spec.js") {
			tsFiles = append(tsFiles, fSlash)
		} else if strings.HasPrefix(filepath.Base(fSlash), "test_") && strings.HasSuffix(fSlash, ".py") {
			pyFiles = append(pyFiles, fSlash)
		}
	}

	// Also associate functions with directories
	for _, tf := range testFuncs {
		fSlash := filepath.ToSlash(tf.FilePath)
		if strings.HasSuffix(fSlash, "_test.go") {
			dir := filepath.ToSlash(filepath.Dir(fSlash))
			if !strings.HasPrefix(dir, "./") && !strings.HasPrefix(dir, "/") {
				dir = "./" + dir
			}
			if strings.HasPrefix(tf.Name, "Test") {
				goDirs[dir] = append(goDirs[dir], tf.Name)
			}
		}
	}

	// Go commands
	sortedGoDirs := make([]string, 0, len(goDirs))
	for dir := range goDirs {
		sortedGoDirs = append(sortedGoDirs, dir)
	}
	sort.Strings(sortedGoDirs)

	for _, dir := range sortedGoDirs {
		funcs := goDirs[dir]
		uniqueFuncs := uniqueStrings(funcs)
		if len(uniqueFuncs) > 0 {
			pattern := fmt.Sprintf("^(%s)$", strings.Join(uniqueFuncs, "|"))
			commands = append(commands, fmt.Sprintf("go test -v -count=1 -run '%s' %s", pattern, dir))
		} else {
			commands = append(commands, fmt.Sprintf("go test -v -count=1 %s", dir))
		}
	}

	// TS/JS commands
	for _, f := range tsFiles {
		commands = append(commands, fmt.Sprintf("npm test -- %s", f))
	}

	// Python commands
	for _, f := range pyFiles {
		commands = append(commands, fmt.Sprintf("pytest %s", f))
	}

	return commands
}

func uniqueStrings(slice []string) []string {
	seen := make(map[string]bool, len(slice))
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if !seen[s] && s != "" {
			seen[s] = true
			result = append(result, s)
		}
	}
	sort.Strings(result)
	return result
}

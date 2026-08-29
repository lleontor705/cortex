package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/lleontor705/cortex/v2/internal/domain"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
	domaingraph "github.com/lleontor705/cortex/v2/internal/domain/graph"
)

const (
	maxAgentCommunitySummaries  = 4
	maxAgentCommunityRunes      = 1200
	maxAgentSummaryCacheEntries = 256
)

var sharedAgentSummaryCache = newAgentSummaryCache()

type agentSummaryCache struct {
	mu             sync.RWMutex
	entries        map[string]agentSummaryEntry
	builds         uint64
	beforeReadLock func()
}

type agentSummaryEntry struct {
	fingerprint [sha256.Size]byte
	evidence    []agentdomain.Evidence
}

func newAgentSummaryCache() *agentSummaryCache {
	return &agentSummaryCache{entries: make(map[string]agentSummaryEntry)}
}

func (c *agentSummaryCache) architecturalEvidence(ctx context.Context, scope agentdomain.Scope, projectID string, memory *domain.GraphSubgraph, ast *code.CodeGraph, usable bool) ([]agentdomain.Evidence, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if c == nil || !usable || !agentCorpusMatchesScope(scope, projectID, memory, ast) || memory.Truncated || (len(memory.Nodes) == 0 && len(ast.Symbols) == 0) {
		return nil, false, nil
	}
	fingerprint := agentCorpusFingerprint(memory, ast)
	key := strings.Join([]string{scope.TenantID, scope.WorkspaceID, scope.Project, projectID}, "\x00")
	if c.beforeReadLock != nil {
		c.beforeReadLock()
	}
	c.mu.RLock()
	if err := ctx.Err(); err != nil {
		c.mu.RUnlock()
		return nil, false, err
	}
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if ok && entry.fingerprint == fingerprint {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		return append([]agentdomain.Evidence(nil), entry.evidence...), true, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if entry, ok = c.entries[key]; ok && entry.fingerprint == fingerprint {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		return append([]agentdomain.Evidence(nil), entry.evidence...), true, nil
	}
	evidence := buildAgentCommunityEvidence(memory, ast)
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if len(evidence) == 0 {
		return nil, false, nil
	}
	if _, exists := c.entries[key]; !exists && len(c.entries) >= maxAgentSummaryCacheEntries {
		oldest := ""
		for candidate := range c.entries {
			if oldest == "" || candidate < oldest {
				oldest = candidate
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[key] = agentSummaryEntry{fingerprint: fingerprint, evidence: append([]agentdomain.Evidence(nil), evidence...)}
	c.builds++
	return evidence, false, nil
}

func agentCorpusMatchesScope(scope agentdomain.Scope, projectID string, memory *domain.GraphSubgraph, ast *code.CodeGraph) bool {
	if scope.TenantID == "" || scope.WorkspaceID == "" || scope.Project == "" || projectID == "" || memory == nil || ast == nil || ast.Project != projectID {
		return false
	}
	for _, node := range memory.Nodes {
		if node.Project != projectID {
			return false
		}
	}
	for _, symbol := range ast.Symbols {
		if symbol.Project != projectID {
			return false
		}
	}
	return true
}

func authorizedAgentCorpus(memory *domain.GraphSubgraph, ast *code.CodeGraph) ([]domain.GraphNode, []domain.GraphLink, []code.Symbol, []code.Relation) {
	nodes := append([]domain.GraphNode(nil), memory.Nodes...)
	symbols := append([]code.Symbol(nil), ast.Symbols...)
	memoryIDs := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		memoryIDs[node.ID] = true
	}
	symbolIDs := make(map[string]bool, len(symbols))
	for _, symbol := range symbols {
		symbolIDs[symbol.ID] = true
	}
	edges := make([]domain.GraphLink, 0, len(memory.Edges))
	for _, edge := range memory.Edges {
		if memoryIDs[edge.Source] && memoryIDs[edge.Target] {
			edges = append(edges, edge)
		}
	}
	relations := make([]code.Relation, 0, len(ast.Relations))
	for _, relation := range ast.Relations {
		if symbolIDs[relation.SourceID] && symbolIDs[relation.TargetID] {
			relations = append(relations, relation)
		}
	}
	return nodes, edges, symbols, relations
}

func agentCorpusFingerprint(memory *domain.GraphSubgraph, ast *code.CodeGraph) [sha256.Size]byte {
	nodes, edges, symbols, relations := authorizedAgentCorpus(memory, ast)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.ID < b.ID
	})
	sort.Slice(symbols, func(i, j int) bool { return symbols[i].ID < symbols[j].ID })
	sort.Slice(relations, func(i, j int) bool {
		a, b := relations[i], relations[j]
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		if a.TargetID != b.TargetID {
			return a.TargetID < b.TargetID
		}
		if a.Relation != b.Relation {
			return a.Relation < b.Relation
		}
		return a.ID < b.ID
	})
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "project\x00%s\n", ast.Project)
	for _, n := range nodes {
		_, _ = fmt.Fprintf(h, "n\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\n", n.ID, n.Kind, n.Subtype, n.Label, n.Project, n.Hop)
	}
	for _, e := range edges {
		_, _ = fmt.Fprintf(h, "e\x00%s\x00%s\x00%s\x00%s\x00%.17g\x00%.17g\x00%s\x00%s\n", e.ID, e.Source, e.Target, e.Type, e.Weight, e.Confidence, e.AssertionKind, e.AssertionStatus)
	}
	for _, s := range symbols {
		_, _ = fmt.Fprintf(h, "s\x00%s\x00%s\x00%s\x00%d\x00%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\n", s.ID, s.Project, s.FilePath, s.LineNumber, s.EndLine, s.Kind, s.Name, s.PackageName, s.Signature, s.DocSummary, s.FileHash)
	}
	for _, r := range relations {
		_, _ = fmt.Fprintf(h, "r\x00%d\x00%s\x00%s\x00%s\x00%s\x00%.17g\x00%s\n", r.ID, r.Project, r.SourceID, r.TargetID, r.Relation, r.Confidence, r.Reasoning)
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func buildAgentCommunityEvidence(memory *domain.GraphSubgraph, ast *code.CodeGraph) []agentdomain.Evidence {
	memoryNodes, memoryEdges, symbols, relations := authorizedAgentCorpus(memory, ast)
	nodes := make([]domaingraph.GraphAnalyticsNode, 0, len(memoryNodes)+len(symbols))
	edges := make([]domaingraph.GraphAnalyticsEdge, 0, len(memoryEdges)+len(relations))
	for _, n := range memoryNodes {
		nodes = append(nodes, domaingraph.GraphAnalyticsNode{ID: n.ID, Label: n.Label, Kind: n.Kind, Subtype: n.Subtype})
	}
	for _, s := range symbols {
		nodes = append(nodes, domaingraph.GraphAnalyticsNode{ID: "code:" + s.ID, Label: s.Name, Kind: "code", Subtype: s.Kind, SourceFile: s.FilePath})
	}
	for _, e := range memoryEdges {
		edges = append(edges, domaingraph.GraphAnalyticsEdge{ID: e.ID, Source: e.Source, Target: e.Target, Type: e.Type, Weight: e.Weight, Confidence: e.Confidence})
	}
	for _, r := range relations {
		edges = append(edges, domaingraph.GraphAnalyticsEdge{ID: fmt.Sprintf("code:%d", r.ID), Source: "code:" + r.SourceID, Target: "code:" + r.TargetID, Type: r.Relation, Weight: r.Confidence, Confidence: r.Confidence, Reasoning: r.Reasoning})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Source != edges[j].Source {
			return edges[i].Source < edges[j].Source
		}
		if edges[i].Target != edges[j].Target {
			return edges[i].Target < edges[j].Target
		}
		if edges[i].Type != edges[j].Type {
			return edges[i].Type < edges[j].Type
		}
		return edges[i].ID < edges[j].ID
	})
	summaries := append([]domaingraph.CommunitySummary(nil), domaingraph.AnalyzeGraph(nodes, edges).CommunitySummaries...)
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].CommunityID < summaries[j].CommunityID })
	if len(summaries) > maxAgentCommunitySummaries {
		summaries = summaries[:maxAgentCommunitySummaries]
	}
	evidence := make([]agentdomain.Evidence, 0, len(summaries))
	for i, summary := range summaries {
		content := summary.SummaryMarkdown
		runes := []rune(content)
		if len(runes) > maxAgentCommunityRunes {
			content = string(runes[:maxAgentCommunityRunes]) + "…"
		}
		evidence = append(evidence, agentdomain.Evidence{Kind: agentdomain.EvidenceMemory, Title: fmt.Sprintf("Architecture community %d: %s", summary.CommunityID, summary.Label), Content: content, Score: 1 - float64(i)*0.01})
	}
	return evidence
}

func mergeAgentArchitecturalEvidence(summaries, sources []agentdomain.Evidence, limit int) []agentdomain.Evidence {
	if limit <= 0 {
		return nil
	}
	if len(summaries) == 0 {
		if len(sources) > limit {
			return append([]agentdomain.Evidence(nil), sources[:limit]...)
		}
		return append([]agentdomain.Evidence(nil), sources...)
	}
	out := make([]agentdomain.Evidence, 0, limit)
	out = append(out, summaries[0])
	used := make(map[int]bool)
	for _, kind := range []agentdomain.EvidenceKind{agentdomain.EvidenceMemory, agentdomain.EvidenceCode} {
		for i, source := range sources {
			if len(out) >= limit {
				break
			}
			if !used[i] && source.Kind == kind {
				out = append(out, source)
				used[i] = true
				break
			}
		}
	}
	for _, summary := range summaries[1:] {
		if len(out) >= limit {
			break
		}
		out = append(out, summary)
	}
	for i, source := range sources {
		if len(out) >= limit {
			break
		}
		if !used[i] {
			out = append(out, source)
		}
	}
	return out
}

func countAgentCommunityEvidence(evidence []agentdomain.Evidence) int {
	count := 0
	for _, item := range evidence {
		if strings.HasPrefix(item.Title, "Architecture community ") {
			count++
		}
	}
	return count
}

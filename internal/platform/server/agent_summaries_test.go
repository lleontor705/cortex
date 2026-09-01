package server

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
	postgresstore "github.com/lleontor705/cortex/v2/internal/store/postgres"
)

func summaryCorpus(project string) (*domain.GraphSubgraph, *code.CodeGraph) {
	memory := &domain.GraphSubgraph{Nodes: []domain.GraphNode{
		{ID: "observation:a", Kind: "observation", Label: "Decision A", Project: project},
		{ID: "observation:b", Kind: "observation", Label: "Decision B", Project: project},
	}, Edges: []domain.GraphLink{{ID: "edge:a", Source: "observation:a", Target: "observation:b", Type: "references", Weight: 1, Confidence: 1}}}
	ast := &code.CodeGraph{Project: project, Symbols: []code.Symbol{
		{ID: "symbol:a", Project: project, FilePath: "app/a.go", Kind: "function", Name: "A"},
		{ID: "symbol:b", Project: project, FilePath: "app/b.go", Kind: "function", Name: "B"},
	}, Relations: []code.Relation{{ID: 1, Project: project, SourceID: "symbol:a", TargetID: "symbol:b", Relation: "calls", Confidence: 1}}}
	return memory, ast
}

func TestAgentSummaryCachesOnlyCombinedAuthorizedCorpus(t *testing.T) {
	cache := newAgentSummaryCache()
	scope := agentdomain.Scope{TenantID: "tenant-a", WorkspaceID: "workspace-a", Project: "cortex"}
	project := "11111111-1111-1111-1111-111111111111"
	memory, ast := summaryCorpus(project)
	first, hit, err := cache.architecturalEvidence(t.Context(), scope, project, memory, ast, true)
	if err != nil || hit || len(first) == 0 || cache.builds != 1 {
		t.Fatalf("first=%#v hit=%v builds=%d err=%v", first, hit, cache.builds, err)
	}
	permutedMemory := &domain.GraphSubgraph{Nodes: []domain.GraphNode{memory.Nodes[1], memory.Nodes[0]}, Edges: append([]domain.GraphLink(nil), memory.Edges...)}
	permutedAST := &code.CodeGraph{Project: project, Symbols: []code.Symbol{ast.Symbols[1], ast.Symbols[0]}, Relations: append([]code.Relation(nil), ast.Relations...)}
	second, hit, err := cache.architecturalEvidence(t.Context(), scope, project, permutedMemory, permutedAST, true)
	if err != nil || !hit || !reflect.DeepEqual(first, second) || cache.builds != 1 {
		t.Fatalf("permutation hit=%v builds=%d err=%v", hit, cache.builds, err)
	}
	memory.Nodes[0].Label = "Decision drift"
	if _, hit, err = cache.architecturalEvidence(t.Context(), scope, project, memory, ast, true); err != nil || hit || cache.builds != 2 {
		t.Fatalf("memory drift hit=%v builds=%d err=%v", hit, cache.builds, err)
	}
	ast.Symbols[0].FileHash = "ast-drift"
	if _, hit, err = cache.architecturalEvidence(t.Context(), scope, project, memory, ast, true); err != nil || hit || cache.builds != 3 {
		t.Fatalf("AST drift hit=%v builds=%d err=%v", hit, cache.builds, err)
	}
	foreign := agentdomain.Scope{TenantID: "tenant-b", WorkspaceID: "workspace-a", Project: "cortex"}
	if _, hit, err = cache.architecturalEvidence(t.Context(), foreign, project, memory, ast, true); err != nil || hit || cache.builds != 4 || len(cache.entries) != 2 {
		t.Fatalf("scope hit=%v builds=%d entries=%d err=%v", hit, cache.builds, len(cache.entries), err)
	}
}

func TestAgentSummaryNeverServesStaleOrMismatchedCorpus(t *testing.T) {
	cache := newAgentSummaryCache()
	scope := agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex"}
	project := "11111111-1111-1111-1111-111111111111"
	memory, ast := summaryCorpus(project)
	if _, _, err := cache.architecturalEvidence(t.Context(), scope, project, memory, ast, true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		scope  agentdomain.Scope
		memory *domain.GraphSubgraph
		ast    *code.CodeGraph
		usable bool
	}{
		{"degraded", scope, memory, ast, false}, {"missing memory", scope, nil, ast, true}, {"missing AST", scope, memory, nil, true},
		{"truncated", scope, &domain.GraphSubgraph{Nodes: memory.Nodes, Truncated: true}, ast, true}, {"missing scope", agentdomain.Scope{}, memory, ast, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, hit, err := cache.architecturalEvidence(t.Context(), tc.scope, project, tc.memory, tc.ast, tc.usable)
			if err != nil || hit || len(got) != 0 {
				t.Fatalf("got=%#v hit=%v err=%v", got, hit, err)
			}
		})
	}
	mismatchMemory, mismatchAST := summaryCorpus(project)
	mismatchMemory.Nodes[0].Project = "foreign"
	got, hit, err := cache.architecturalEvidence(t.Context(), scope, project, mismatchMemory, mismatchAST, true)
	if err != nil || hit || len(got) != 0 {
		t.Fatalf("mismatch got=%#v hit=%v err=%v", got, hit, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, hit, err = cache.architecturalEvidence(ctx, scope, project, memory, ast, true)
	if !errors.Is(err, context.Canceled) || hit || len(got) != 0 {
		t.Fatalf("cancel got=%#v hit=%v err=%v", got, hit, err)
	}
}

func TestAgentSummaryCancellationWhileWaitingForCacheFailsClosed(t *testing.T) {
	cache := newAgentSummaryCache()
	project := "11111111-1111-1111-1111-111111111111"
	memory, ast := summaryCorpus(project)
	scope := agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex"}
	started := make(chan struct{})
	cache.beforeReadLock = func() { close(started) }
	cache.mu.Lock()
	ctx, cancel := context.WithCancel(t.Context())
	type outcome struct {
		evidence []agentdomain.Evidence
		hit      bool
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		evidence, hit, err := cache.architecturalEvidence(ctx, scope, project, memory, ast, true)
		done <- outcome{evidence: evidence, hit: hit, err: err}
	}()
	<-started
	cancel()
	cache.mu.Unlock()
	result := <-done
	if !errors.Is(result.err, context.Canceled) || result.hit || len(result.evidence) != 0 {
		t.Fatalf("evidence=%#v hit=%v err=%v", result.evidence, result.hit, result.err)
	}
	if cache.builds != 0 || len(cache.entries) != 0 {
		t.Fatalf("cancelled waiter mutated cache: builds=%d entries=%d", cache.builds, len(cache.entries))
	}
}

func TestAgentSummaryMergesLightRAGSummariesWithUnderlyingSources(t *testing.T) {
	cache := newAgentSummaryCache()
	project := "11111111-1111-1111-1111-111111111111"
	memory, ast := summaryCorpus(project)
	summaries, _, err := cache.architecturalEvidence(t.Context(), agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex"}, project, memory, ast, true)
	if err != nil || len(summaries) == 0 {
		t.Fatalf("summaries=%#v err=%v", summaries, err)
	}
	sources := []agentdomain.Evidence{{Kind: agentdomain.EvidenceMemory, Title: "Decision A", Content: "source memory", Score: .9}, {Kind: agentdomain.EvidenceCode, Title: "A", Path: "app/a.go", Content: "source code", Score: .8}}
	merged := mergeAgentArchitecturalEvidence(summaries, sources, 3)
	if len(merged) != 3 || merged[0].Title == "Decision A" || merged[1].Title != "Decision A" || merged[2].Kind != agentdomain.EvidenceCode {
		t.Fatalf("summary/source reservation failed: %#v", merged)
	}
}

func TestAgentSummaryCacheIsBoundedAcrossScopes(t *testing.T) {
	cache := newAgentSummaryCache()
	project := "11111111-1111-1111-1111-111111111111"
	memory, ast := summaryCorpus(project)
	for i := 0; i < maxAgentSummaryCacheEntries+8; i++ {
		scope := agentdomain.Scope{TenantID: fmt.Sprintf("tenant-%03d", i), WorkspaceID: "workspace", Project: "cortex"}
		if _, _, err := cache.architecturalEvidence(t.Context(), scope, project, memory, ast, true); err != nil {
			t.Fatal(err)
		}
	}
	if len(cache.entries) != maxAgentSummaryCacheEntries {
		t.Fatalf("entries=%d, want bounded %d", len(cache.entries), maxAgentSummaryCacheEntries)
	}
}

func TestAgentArchitecturalRetrievalUsesCommunitySummaryAndUnderlyingSources(t *testing.T) {
	ops := graphFixture()
	cache := newAgentSummaryCache()
	retriever := scopedAgentRetriever{ops: ops, summaries: cache}
	ctx := context.WithValue(context.Background(), agentProjectIDKey{}, recordingAgentProjectID)
	scope := agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex"}
	retrieve := func() agentdomain.RetrievalResult {
		t.Helper()
		result, err := retriever.RetrieveScoped(ctx, scope, "Explain the general architecture and communities of modules", 8)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := retrieve()
	summaryCount := countAgentCommunityEvidence(first.Evidence)
	if summaryCount == 0 || len(first.Evidence) <= summaryCount {
		t.Fatalf("architectural evidence omitted summary or underlying sources: %#v", first.Evidence)
	}
	assertSemanticStage(t, first.Trace, "community_summary", agentStageOK)
	if cache.builds != 1 {
		t.Fatalf("builds=%d, want 1", cache.builds)
	}
	second := retrieve()
	if cache.builds != 1 || !reflect.DeepEqual(first.Evidence, second.Evidence) {
		t.Fatalf("cache miss or nondeterminism: builds=%d", cache.builds)
	}
	ops.snapshot.Nodes[0].Label += " drift"
	memoryDrift := retrieve()
	if cache.builds != 2 || reflect.DeepEqual(second.Evidence, memoryDrift.Evidence) {
		t.Fatalf("memory drift did not recompute: builds=%d", cache.builds)
	}
	ops.codeSnapshot.Symbols[0].Name += "Drift"
	astDrift := retrieve()
	if cache.builds != 3 || reflect.DeepEqual(memoryDrift.Evidence, astDrift.Evidence) {
		t.Fatalf("AST drift did not recompute: builds=%d", cache.builds)
	}
	ops.memoryErr = postgresstore.ErrCodeIndexUnavailable
	unavailable := retrieve()
	if countAgentCommunityEvidence(unavailable.Evidence) != 0 || cache.builds != 3 {
		t.Fatalf("stale summary served while unavailable: %#v builds=%d", unavailable.Evidence, cache.builds)
	}
	assertSemanticStage(t, unavailable.Trace, "community_summary", agentStageDegraded)
}

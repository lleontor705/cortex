package server

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"unicode/utf8"

	"github.com/lleontor705/cortex/v2/internal/domain"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
	"github.com/lleontor705/cortex/v2/internal/retrieval"
)

type cragSearchCall struct {
	projectID, projectLabel, query string
	options                        domain.SearchOptions
}
type cragAgentOperations struct {
	results   [][]*domain.SearchResult
	calls     []cragSearchCall
	searchErr error
	onSearch  func(int)
}

func (o *cragAgentOperations) SearchAgentObservations(_ context.Context, projectID, projectLabel, query string, options domain.SearchOptions) ([]*domain.SearchResult, error) {
	o.calls = append(o.calls, cragSearchCall{projectID, projectLabel, query, options})
	if o.onSearch != nil {
		o.onSearch(len(o.calls))
	}
	if o.searchErr != nil {
		return nil, o.searchErr
	}
	index := len(o.calls) - 1
	if index >= len(o.results) {
		index = len(o.results) - 1
	}
	if index < 0 {
		return nil, nil
	}
	return o.results[index], nil
}
func (*cragAgentOperations) GetAgentObservationByID(context.Context, string, string, int64) (*domain.Observation, error) {
	return nil, domain.ErrNotFound
}
func (*cragAgentOperations) ListCodeSymbols(context.Context, code.SymbolFilter) ([]code.Symbol, error) {
	return nil, nil
}
func (*cragAgentOperations) GetCodeGraph(_ context.Context, project string) (*code.CodeGraph, error) {
	return &code.CodeGraph{Project: project}, nil
}
func cragResult(rank float64) []*domain.SearchResult {
	return []*domain.SearchResult{{Observation: domain.Observation{PublicID: "20000000-a000-0000-0000-000000000001", Title: "authorized", Content: "project-context auth implementation symbols decisions files context", Project: "cortex"}, Rank: rank}}
}
func cragContext() context.Context {
	return context.WithValue(context.Background(), agentProjectIDKey{}, recordingAgentProjectID)
}

func TestAgentCRAGRetriesLowConfidenceExactlyOnceWithoutScopeDrift(t *testing.T) {
	ops := &cragAgentOperations{results: [][]*domain.SearchResult{cragResult(.1), cragResult(2)}}
	retriever := scopedAgentRetriever{ops: ops}
	scope := agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex"}
	result, err := retriever.RetrieveScoped(cragContext(), scope, "auth", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops.calls) != 2 || len(result.Evidence) == 0 {
		t.Fatalf("calls=%d evidence=%#v", len(ops.calls), result.Evidence)
	}
	assertSemanticStage(t, result.Trace, "crag", agentStageOK)
	first, second := ops.calls[0], ops.calls[1]
	if first.projectID != second.projectID || first.projectLabel != second.projectLabel || first.options.Project != second.options.Project || first.options.Limit != second.options.Limit {
		t.Fatalf("scope/budget drift: first=%#v second=%#v", first, second)
	}
	if second.options.Query != second.query || first.query == second.query || second.query != retrieval.RefineAgentCRAGQuery(first.query) || utf8.RuneCountInString(second.query) > retrieval.AgentCRAGMaxQueryRunes {
		t.Fatalf("unsafe refinement: first=%q second=%q opts=%q", first.query, second.query, second.options.Query)
	}
}

func TestAgentCRAGMediumAndHighDoNotRetry(t *testing.T) {
	for _, tc := range []struct {
		name string
		rank float64
	}{{"medium", 1}, {"high", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			ops := &cragAgentOperations{results: [][]*domain.SearchResult{cragResult(tc.rank)}}
			result, err := (scopedAgentRetriever{ops: ops}).RetrieveScoped(cragContext(), agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex"}, "auth", 8)
			if err != nil || len(ops.calls) != 1 || len(result.Evidence) == 0 {
				t.Fatalf("calls=%d evidence=%#v err=%v", len(ops.calls), result.Evidence, err)
			}
			assertSemanticStage(t, result.Trace, "crag", agentStageOK)
		})
	}
}

func TestAgentCRAGSecondLowAbstainsWithoutStaleEvidence(t *testing.T) {
	ops := &cragAgentOperations{results: [][]*domain.SearchResult{cragResult(.1), nil}}
	result, err := (scopedAgentRetriever{ops: ops}).RetrieveScoped(cragContext(), agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex"}, "auth", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops.calls) != 2 || len(result.Evidence) != 0 {
		t.Fatalf("calls=%d stale=%#v", len(ops.calls), result.Evidence)
	}
	assertSemanticStage(t, result.Trace, "crag", agentStageDegraded)
	count := 0
	for _, reason := range result.Trace.Degraded {
		if reason == agentCRAGInsufficientConfidence {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("CRAG degradation count=%d trace=%#v", count, result.Trace.Degraded)
	}
}

func TestAgentCRAGAuthorizationAndCancellationFailClosed(t *testing.T) {
	denied := errors.New("authorization denied")
	deniedOps := &cragAgentOperations{searchErr: denied}
	if _, err := (scopedAgentRetriever{ops: deniedOps}).RetrieveScoped(cragContext(), agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex"}, "auth", 8); !errors.Is(err, denied) || len(deniedOps.calls) != 1 {
		t.Fatalf("err=%v calls=%d", err, len(deniedOps.calls))
	}
	for _, cancelAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("cancel during pass %d", cancelAt), func(t *testing.T) {
			ctx, cancel := context.WithCancel(cragContext())
			cancelOps := &cragAgentOperations{results: [][]*domain.SearchResult{cragResult(.1), cragResult(2)}, onSearch: func(call int) {
				if call == cancelAt {
					cancel()
				}
			}}
			if _, err := (scopedAgentRetriever{ops: cancelOps}).RetrieveScoped(ctx, agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex"}, "auth", 8); !errors.Is(err, context.Canceled) || len(cancelOps.calls) != cancelAt {
				t.Fatalf("err=%v calls=%d", err, len(cancelOps.calls))
			}
		})
	}
}

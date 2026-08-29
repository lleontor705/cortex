package server

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/domain"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
	postgresstore "github.com/lleontor705/cortex/v2/internal/store/postgres"
)

const (
	graphSeedPublicID     = "30000000-a000-0000-0000-000000000001"
	graphNeighborPublicID = "30000000-a000-0000-0000-000000000002"
)

type graphAgentOperations struct {
	recordingAgentOperations
	snapshot, snapshotResult *domain.GraphSubgraph
	codeSnapshot             *code.CodeGraph
	memoryErr, codeErr       error
	seedSymbols              []code.Symbol
	hops, nodes, edges       int
	codeGraphCalls           int
	hydrated                 map[int64]*domain.Observation
}

func (o *graphAgentOperations) SearchAgentObservations(_ context.Context, projectID, projectLabel, _ string, _ domain.SearchOptions) ([]*domain.SearchResult, error) {
	o.searchProjectID, o.searchProject = projectID, projectLabel
	return []*domain.SearchResult{{Observation: domain.Observation{ID: 1, PublicID: graphSeedPublicID, Project: projectLabel, Title: "Seed", Content: "seed remains useful"}, Rank: .9}}, nil
}

func (o *graphAgentOperations) GetAgentGraphSnapshot(ctx context.Context, _, _ string, _ []string, hops, nodes, edges int) (*domain.GraphSubgraph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o.hops, o.nodes, o.edges = hops, nodes, edges
	if o.snapshotResult != nil {
		return o.snapshotResult, o.memoryErr
	}
	return o.snapshot, o.memoryErr
}

func (o *graphAgentOperations) GetAgentCodeGraphSnapshot(ctx context.Context, project, _ string, hops, nodes, edges int) (*code.CodeGraph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o.hops, o.nodes, o.edges = hops, nodes, edges
	if o.codeSnapshot == nil {
		return &code.CodeGraph{Project: project}, o.codeErr
	}
	return o.codeSnapshot, o.codeErr
}

func (o *graphAgentOperations) GetAgentObservationByID(_ context.Context, _, _ string, id int64) (*domain.Observation, error) {
	return o.hydrated[id], nil
}
func (o *graphAgentOperations) ListCodeSymbols(_ context.Context, filter code.SymbolFilter) ([]code.Symbol, error) {
	if o.codeErr != nil {
		return nil, o.codeErr
	}
	if o.seedSymbols != nil {
		return o.seedSymbols, nil
	}
	return []code.Symbol{{ID: "func:Seed", Project: filter.Project, Name: "SeedSymbol", Kind: "func", FilePath: "seed.go", LineNumber: 1}}, nil
}
func (o *graphAgentOperations) GetCodeGraph(context.Context, string) (*code.CodeGraph, error) {
	o.codeGraphCalls++
	return nil, errors.New("full graph forbidden")
}

func graphFixture() *graphAgentOperations {
	return &graphAgentOperations{
		snapshot: &domain.GraphSubgraph{Nodes: []domain.GraphNode{
			{ID: "observation:" + graphSeedPublicID, Kind: "observation", Project: recordingAgentProjectID, Metadata: map[string]any{"observation_id": int64(1)}},
			{ID: "observation:" + graphNeighborPublicID, Kind: "observation", Project: recordingAgentProjectID, Metadata: map[string]any{"observation_id": int64(2)}},
		}, Edges: []domain.GraphLink{
			{Source: "observation:" + graphSeedPublicID, Target: "observation:" + graphNeighborPublicID, Type: "references", Weight: 1},
			{Source: "observation:" + graphSeedPublicID, Target: "observation:foreign", Type: "references", Weight: 99},
		}},
		codeSnapshot: &code.CodeGraph{Project: recordingAgentProjectID, Symbols: []code.Symbol{
			{ID: "func:Seed", Project: recordingAgentProjectID, Name: "SeedSymbol", Kind: "func", FilePath: "seed.go", LineNumber: 1, Metadata: map[string]any{"agent_seed": true}},
			{ID: "func:Neighbor", Project: recordingAgentProjectID, Name: "NeighborSymbol", Kind: "func", FilePath: "neighbor.go", LineNumber: 2},
		}, Relations: []code.Relation{{SourceID: "func:Seed", TargetID: "func:Neighbor", Relation: code.RelationCalls, Confidence: 1}}},
		hydrated: map[int64]*domain.Observation{
			1: {ID: 1, PublicID: graphSeedPublicID, Project: "cortex", Title: "Seed", Content: "seed remains useful"},
			2: {ID: 2, PublicID: graphNeighborPublicID, Project: "cortex", Title: "Memory neighbor", Content: "bounded visible neighbor"},
		},
	}
}

func TestScopedAgentRetrieverMultiHopUsesBoundedSnapshots(t *testing.T) {
	ops := graphFixture()
	ctx := context.WithValue(context.Background(), agentProjectIDKey{}, recordingAgentProjectID)
	result, err := (scopedAgentRetriever{ops: ops}).RetrieveScoped(ctx, agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex"}, "What is the impact if we change SeedSymbol?", 8)
	if err != nil {
		t.Fatal(err)
	}
	if ops.codeGraphCalls != 0 || ops.hops != agentGraphMaxHops || ops.nodes != agentGraphMaxNodes || ops.edges != agentGraphMaxEdges {
		t.Fatalf("full=%d budgets=%d/%d/%d", ops.codeGraphCalls, ops.hops, ops.nodes, ops.edges)
	}
	assertEvidenceTitles(t, result.Evidence, "Seed", "Memory neighbor", "NeighborSymbol")
	assertEvidenceTitlesAbsent(t, result.Evidence, "ForeignSymbol")
	assertSemanticStage(t, result.Trace, "graph_ppr", agentStageOK)
}

type delegatedGraphOperations struct {
	Operations
	memoryCalls, codeCalls int
}

func (o *delegatedGraphOperations) GetAgentGraphSnapshot(context.Context, string, string, []string, int, int, int) (*domain.GraphSubgraph, error) {
	o.memoryCalls++
	return &domain.GraphSubgraph{}, nil
}
func (o *delegatedGraphOperations) GetAgentCodeGraphSnapshot(_ context.Context, project, _ string, _, _, _ int) (*code.CodeGraph, error) {
	o.codeCalls++
	return &code.CodeGraph{Project: project}, nil
}

func TestRequestOperationsAgentGraphDelegatesAuthenticatedBoundary(t *testing.T) {
	inner := &delegatedGraphOperations{Operations: &fakeOperations{}}
	ctx := withOperations(context.Background(), inner)
	if _, err := (requestOperations{}).GetAgentGraphSnapshot(ctx, recordingAgentProjectID, "cortex", nil, 2, 8, 16); err != nil {
		t.Fatal(err)
	}
	if _, err := (requestOperations{}).GetAgentCodeGraphSnapshot(ctx, recordingAgentProjectID, "Seed", 2, 8, 16); err != nil {
		t.Fatal(err)
	}
	if inner.memoryCalls != 1 || inner.codeCalls != 1 {
		t.Fatalf("delegation calls=%d/%d", inner.memoryCalls, inner.codeCalls)
	}
}

func TestScopedAgentRetrieverMultiHopFailsClosedOnAuthorizationAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
	}{
		{name: "authorization", ctx: context.Background(), err: errors.New(authz.DenyProject)},
		{name: "cancellation", ctx: func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops := graphFixture()
			ops.memoryErr = tc.err
			ctx := context.WithValue(tc.ctx, agentProjectIDKey{}, recordingAgentProjectID)
			_, err := (scopedAgentRetriever{ops: ops}).RetrieveScoped(ctx, agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex"}, "What dependency impact exists?", 4)
			if err == nil {
				t.Fatal("security/cancellation error degraded instead of failing closed")
			}
		})
	}
}

func TestScopedAgentRetrieverMultiHopAvailabilityDegrades(t *testing.T) {
	ops := graphFixture()
	ops.codeErr = postgresstore.ErrCodeIndexUnavailable
	ctx := context.WithValue(context.Background(), agentProjectIDKey{}, recordingAgentProjectID)
	result, err := (scopedAgentRetriever{ops: ops}).RetrieveScoped(ctx, agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex"}, "What dependency impact exists?", 4)
	if err != nil {
		t.Fatal(err)
	}
	assertEvidenceTitles(t, result.Evidence, "Seed", "Memory neighbor")
	assertSemanticStage(t, result.Trace, "graph_ppr", agentStageDegraded)
}

func TestAgentGraphHybridSeedsNormalizeOpposingScales(t *testing.T) {
	lexical := []*domain.SearchResult{{Observation: domain.Observation{PublicID: graphSeedPublicID}, Rank: 1000}}
	dense := []*domain.VectorSearchResult{{Observation: domain.Observation{PublicID: graphNeighborPublicID}, Similarity: .2}}
	seeds, err := agentHybridGraphSeeds(lexical, dense, []code.Symbol{{ID: "func:Seed", Project: recordingAgentProjectID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"observation:" + graphSeedPublicID, "observation:" + graphNeighborPublicID, "code:func:Seed"} {
		if seeds[id] <= 0 {
			t.Fatalf("hybrid seed %q missing: %#v", id, seeds)
		}
	}
	if seeds["observation:"+graphSeedPublicID] > 2*seeds["observation:"+graphNeighborPublicID] {
		t.Fatalf("native lexical scale dominated normalized dense: %#v", seeds)
	}
}

func TestScopedAgentRetrieverMultiHopCanonicalAcrossPermutations(t *testing.T) {
	left, right := graphFixture(), graphFixture()
	right.snapshot.Nodes[0], right.snapshot.Nodes[1] = right.snapshot.Nodes[1], right.snapshot.Nodes[0]
	right.codeSnapshot.Symbols[0], right.codeSnapshot.Symbols[1] = right.codeSnapshot.Symbols[1], right.codeSnapshot.Symbols[0]
	ctx := context.WithValue(context.Background(), agentProjectIDKey{}, recordingAgentProjectID)
	query := "What dependency impact exists?"
	a, err := (scopedAgentRetriever{ops: left}).RetrieveScoped(ctx, agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex"}, query, 8)
	if err != nil {
		t.Fatal(err)
	}
	b, err := (scopedAgentRetriever{ops: right}).RetrieveScoped(ctx, agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex"}, query, 8)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(evidenceTitles(a.Evidence), evidenceTitles(b.Evidence)) {
		t.Fatalf("permutation changed order: %v vs %v", evidenceTitles(a.Evidence), evidenceTitles(b.Evidence))
	}
}

func TestScopedAgentRetrieverMultiHopKeepsDisconnectedSeed(t *testing.T) {
	ops := graphFixture()
	ops.snapshot.Edges = nil
	ops.codeSnapshot = &code.CodeGraph{Project: recordingAgentProjectID}
	ctx := context.WithValue(context.Background(), agentProjectIDKey{}, recordingAgentProjectID)
	result, err := (scopedAgentRetriever{ops: ops}).RetrieveScoped(ctx, agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex"}, "What dependency impact exists?", 3)
	if err != nil {
		t.Fatal(err)
	}
	assertEvidenceTitles(t, result.Evidence, "Seed")
}

func evidenceTitles(evidence []agentdomain.Evidence) []string {
	result := make([]string, len(evidence))
	for i := range evidence {
		result[i] = evidence[i].Title
	}
	return result
}
func assertEvidenceTitles(t *testing.T, evidence []agentdomain.Evidence, titles ...string) {
	t.Helper()
	found := map[string]bool{}
	for _, item := range evidence {
		found[item.Title] = true
	}
	for _, title := range titles {
		if !found[title] {
			t.Fatalf("evidence %#v omitted %q", evidence, title)
		}
	}
}
func assertEvidenceTitlesAbsent(t *testing.T, evidence []agentdomain.Evidence, titles ...string) {
	t.Helper()
	for _, item := range evidence {
		for _, title := range titles {
			if item.Title == title {
				t.Fatalf("evidence leaked %q", title)
			}
		}
	}
}

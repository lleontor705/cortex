package server

import (
	"context"
	"errors"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
)

type failingSemanticVectorIndex struct {
	agentRecordingVectorIndex
	err error
}

func (v *failingSemanticVectorIndex) Search(_ context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	v.query = q
	return nil, v.err
}

func TestScopedAgentRetrieverSemanticHybridUsesImmutableScope(t *testing.T) {
	ops := &recordingAgentOperations{}
	vectors := &agentRecordingVectorIndex{}
	ctx := context.WithValue(context.Background(), agentProjectIDKey{}, recordingAgentProjectID)

	result, err := (scopedAgentRetriever{ops: ops, vectors: vectors, embeddings: fixedAgentEmbedding{}}).RetrieveScoped(ctx, agentdomain.Scope{
		TenantID: "tenant-a", WorkspaceID: "workspace-a", Project: "duplicate-label",
	}, "How is scoped authorization applied to project requests?", 5)
	if err != nil {
		t.Fatalf("RetrieveScoped() = %v", err)
	}
	if result.Trace.Tier != agentdomain.RetrievalTierSemanticHybrid {
		t.Fatalf("tier = %q", result.Trace.Tier)
	}
	if ops.searchProjectID != recordingAgentProjectID || ops.searchProject != "duplicate-label" {
		t.Fatalf("lexical scope id=%q label=%q", ops.searchProjectID, ops.searchProject)
	}
	filters := vectors.query.Filters
	if filters["tenant_id"] != "tenant-a" || filters["workspace_id"] != "workspace-a" || filters["project_id"] != recordingAgentProjectID {
		t.Fatalf("dense filters = %#v", filters)
	}
	if ops.vectorLookupProjectID != recordingAgentProjectID || ops.vectorLookupProjectLabel != "duplicate-label" {
		t.Fatalf("hydration identity=%q label=%q", ops.vectorLookupProjectID, ops.vectorLookupProjectLabel)
	}
	if len(result.Evidence) == 0 || result.Evidence[0].Title != "Vector" || result.Evidence[0].Score < 0 || result.Evidence[0].Score > 1 {
		t.Fatalf("evidence = %#v", result.Evidence)
	}
	assertSemanticStage(t, result.Trace, "lexical", "ok")
	assertSemanticStage(t, result.Trace, "dense", "ok")
	assertSemanticStage(t, result.Trace, "rrf_maxsim", "ok")
}

func TestScopedAgentRetrieverSemanticVectorFailureDegradesLexicalOnly(t *testing.T) {
	ops := &recordingAgentOperations{}
	vectors := &failingSemanticVectorIndex{err: errors.New("vector unavailable")}
	ctx := context.WithValue(context.Background(), agentProjectIDKey{}, recordingAgentProjectID)

	result, err := (scopedAgentRetriever{ops: ops, vectors: vectors, embeddings: fixedAgentEmbedding{}}).RetrieveScoped(ctx, agentdomain.Scope{
		TenantID: "tenant-a", WorkspaceID: "workspace-a", Project: "cortex",
	}, "How is authorization applied to project requests?", 5)
	if err != nil {
		t.Fatalf("RetrieveScoped() = %v", err)
	}
	if len(result.Evidence) == 0 || result.Evidence[0].Title != "Decision" || result.Evidence[0].Kind != agentdomain.EvidenceMemory {
		t.Fatalf("lexical fallback = %#v", result.Evidence)
	}
	assertSemanticStage(t, result.Trace, "dense", "degraded")
}

func TestScopedAgentRetrieverSemanticRejectsInvalidScopeBeforeDependencies(t *testing.T) {
	ops := &recordingAgentOperations{}
	vectors := &agentRecordingVectorIndex{}
	_, err := (scopedAgentRetriever{ops: ops, vectors: vectors, embeddings: fixedAgentEmbedding{}}).RetrieveScoped(
		context.Background(), agentdomain.Scope{TenantID: "", WorkspaceID: "workspace", Project: "cortex"}, "conceptual question here", 5,
	)
	if err == nil {
		t.Fatal("RetrieveScoped() error = nil")
	}
	if ops.searchProjectID != "" || vectors.query.Vector != nil {
		t.Fatalf("invalid scope reached dependencies: ops=%#v vector=%#v", ops, vectors.query)
	}
}

func TestSemanticAgentEvidenceNormalizesSignalsBeforeRRFAndMaxSim(t *testing.T) {
	lexical := []*domain.SearchResult{{Observation: domain.Observation{
		ID: 900, PublicID: "00000000-0000-0000-0000-000000000002", Title: "Lexical scale", Content: "unrelated material",
	}, Rank: 99}}
	dense := []*domain.VectorSearchResult{{Observation: domain.Observation{
		ID: 1, PublicID: "00000000-0000-0000-0000-000000000001", Title: "Dense semantic", Content: "scoped authorization",
	}, Similarity: .25}}

	evidence, err := semanticAgentEvidence("scoped authorization", lexical, dense, 5)
	if err != nil {
		t.Fatalf("semanticAgentEvidence() = %v", err)
	}
	if len(evidence) != 2 || evidence[0].Title != "Dense semantic" {
		t.Fatalf("normalized ranking = %#v", evidence)
	}
	for _, item := range evidence {
		if item.Score < 0 || item.Score > 1 {
			t.Fatalf("composed score escaped [0,1]: %#v", evidence)
		}
	}
}

func TestSemanticAgentEvidenceEqualFinalScoreTiesByPublicID(t *testing.T) {
	publicA := "00000000-0000-0000-0000-000000000001"
	publicB := "00000000-0000-0000-0000-000000000002"
	lexical := []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1, PublicID: publicB, Title: "Public B", Content: "same tokens"}, Rank: 10},
		{Observation: domain.Observation{ID: 999, PublicID: publicA, Title: "Public A", Content: "same tokens"}, Rank: 1},
	}
	dense := []*domain.VectorSearchResult{
		{Observation: domain.Observation{ID: 999, PublicID: publicA, Title: "Public A", Content: "same tokens"}, Similarity: .9},
		{Observation: domain.Observation{ID: 1, PublicID: publicB, Title: "Public B", Content: "same tokens"}, Similarity: .2},
	}

	evidence, err := semanticAgentEvidence("same tokens", lexical, dense, 5)
	if err != nil {
		t.Fatalf("semanticAgentEvidence() = %v", err)
	}
	if len(evidence) != 2 || evidence[0].Title != "Public A" || evidence[0].Score != evidence[1].Score {
		t.Fatalf("public tie order = %#v", evidence)
	}
}

func assertSemanticStage(t *testing.T, trace agentdomain.RetrievalTrace, name, status string) {
	t.Helper()
	for _, stage := range trace.Stages {
		if stage.Name == name && stage.Status == status {
			return
		}
	}
	t.Fatalf("stage %s=%s absent from %#v", name, status, trace.Stages)
}

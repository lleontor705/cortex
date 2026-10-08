package server

import (
	"context"
	"errors"
	"sync"
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

// countingAgentEmbedding records provider round-trips per query text for the
// ret-103 query-embedding-cache wiring tests.
type countingAgentEmbedding struct {
	mu    sync.Mutex
	calls map[string]int
	err   error
}

func (f *countingAgentEmbedding) Embed(_ context.Context, text string) ([]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[text]++
	if f.err != nil {
		return nil, f.err
	}
	return []float32{0.5, 0.5}, nil
}
func (f *countingAgentEmbedding) Dimensions() int { return 2 }
func (f *countingAgentEmbedding) Model() string   { return "ret-103-fake-model" }

func (f *countingAgentEmbedding) callsFor(t *testing.T, text string) int {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[text]
}

// TestAgentRetrieverQueryEmbeddingCacheSkipsProviderRoundTrip verifies the
// agent retriever serves a repeated query embedding from the shared cache
// without a second provider round-trip.
func TestAgentRetrieverQueryEmbeddingCacheSkipsProviderRoundTrip(t *testing.T) {
	ops := &recordingAgentOperations{}
	vectors := &agentRecordingVectorIndex{}
	embed := &countingAgentEmbedding{calls: map[string]int{}}
	ctx := context.WithValue(context.Background(), agentProjectIDKey{}, recordingAgentProjectID)
	retriever := scopedAgentRetriever{ops: ops, vectors: vectors, embeddings: embed}
	scope := agentdomain.Scope{TenantID: "tenant-a", WorkspaceID: "workspace-a", Project: "cortex"}
	// Unique query: avoids cache-key collisions with other tests sharing the
	// process-wide hybridQueryEmbeddingCache.
	query := "ret103 unique cache probe hybrid round trip"

	for i := 0; i < 2; i++ {
		if _, err := retriever.RetrieveScoped(ctx, scope, query, 5); err != nil {
			t.Fatalf("RetrieveScoped() run %d = %v", i+1, err)
		}
	}
	if got := embed.callsFor(t, query); got != 1 {
		t.Fatalf("provider round-trips = %d, want exactly 1 (second embed must hit the cache)", got)
	}
}

// TestAgentRetrieverQueryEmbeddingCacheNeverCachesProviderErrors verifies a
// provider failure is retried (never cached) on the next retrieval.
func TestAgentRetrieverQueryEmbeddingCacheNeverCachesProviderErrors(t *testing.T) {
	ops := &recordingAgentOperations{}
	vectors := &agentRecordingVectorIndex{}
	embed := &countingAgentEmbedding{calls: map[string]int{}, err: errors.New("provider unavailable")}
	ctx := context.WithValue(context.Background(), agentProjectIDKey{}, recordingAgentProjectID)
	retriever := scopedAgentRetriever{ops: ops, vectors: vectors, embeddings: embed}
	scope := agentdomain.Scope{TenantID: "tenant-a", WorkspaceID: "workspace-a", Project: "cortex"}
	query := "ret103 unique failing provider probe"

	for i := 0; i < 2; i++ {
		if _, err := retriever.RetrieveScoped(ctx, scope, query, 5); err != nil {
			t.Fatalf("RetrieveScoped() must degrade lexical-only on embed failure, run %d = %v", i+1, err)
		}
	}
	if got := embed.callsFor(t, query); got != 2 {
		t.Fatalf("provider round-trips = %d, want 2 (errors must not be cached)", got)
	}
}

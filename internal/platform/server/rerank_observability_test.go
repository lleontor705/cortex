package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/retrieval"
)

// rerankFakeReranker is a deterministic scriptable reranker: it records the
// candidate IDs it was handed and re-orders them (reverse) so engagement is
// observable in the response ordering.
type rerankFakeReranker struct {
	seenIDs [][]int64
	err     error
}

func (f *rerankFakeReranker) Rerank(_ string, docs []*domain.SearchResult) ([]*domain.SearchResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	ids := make([]int64, 0, len(docs))
	for _, doc := range docs {
		ids = append(ids, doc.ID)
	}
	f.seenIDs = append(f.seenIDs, ids)
	out := make([]*domain.SearchResult, len(docs))
	copy(out, docs)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// recordingSearchOps shadows SearchObservations to capture the limit the
// hybrid handler requested (candidate-pool widening proof).
type recordingSearchOps struct {
	*fakeOperations
	lastLimit int
	results   []*domain.SearchResult
}

func (f *recordingSearchOps) SearchObservations(_ context.Context, _ string, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
	f.lastLimit = opts.Limit
	return f.results, nil
}

type rerankVectorCandidates struct {
	recordingVectorIndex
	count int
}

func (v *rerankVectorCandidates) Search(context.Context, domain.VectorQuery) ([]domain.VectorCandidate, error) {
	out := make([]domain.VectorCandidate, v.count)
	for i := range out {
		out[i] = domain.VectorCandidate{ID: int64(i + 100), Score: 0.9, Provenance: "rerank-test"}
	}
	return out, nil
}

func rerankTestHandler(t *testing.T, reranker retrieval.Reranker, ops Operations, provider, model string) (http.Handler, *rerankFakeReranker) {
	fake, _ := reranker.(*rerankFakeReranker)
	t.Helper()
	cfg := config.Config{
		HTTP:   config.HTTPConfig{Token: "test-token"},
		Server: config.ServerConfig{WorkspaceID: "workspace-verified"},
		Search: config.SearchConfig{DefaultLimit: 10, MaxLimit: 20, RerankProvider: provider, RerankModel: model},
	}
	auth := requestAuthenticator{
		verifier: verifierFunc(func(_ context.Context, secret, _ string) (domain.Principal, error) {
			if secret != cfg.HTTP.Token {
				return domain.Principal{}, errors.New("unknown credential")
			}
			return domain.Principal{Subject: "user-1", OrgID: "tenant-verified"}, nil
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) { return ops, nil }),
	}
	handler, _ := newHTTPHandlerWithHybridSearch(cfg, requestOperations{}, func(context.Context) error { return nil }, auth.middleware, hybridSearchDependencies{
		vectors:    &rerankVectorCandidates{count: 2},
		embeddings: &serverFixedEmbedding{},
		reranker:   reranker,
	})
	return handler, fake
}

func doHybridSearch(handler http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/search/hybrid?q=probe&project=demo", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestSearchHybridRerankAppliedHeadersAndOrder(t *testing.T) {
	ops := &recordingSearchOps{fakeOperations: newFakeOperations()}
	// Lexical list of three results; the fake reranker reverses order so the
	// applied engagement is provable in the response body ordering.
	ops.results = []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1, PublicID: "obs-1", Title: "one"}},
		{Observation: domain.Observation{ID: 2, PublicID: "obs-2", Title: "two"}},
		{Observation: domain.Observation{ID: 3, PublicID: "obs-3", Title: "three"}},
	}
	reranker := &rerankFakeReranker{}
	handler, _ := rerankTestHandler(t, reranker, ops, "openai-compatible", "qwen3-reranker")

	rec := doHybridSearch(handler)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("X-Rerank-Provider"); got != "openai-compatible" {
		t.Fatalf("X-Rerank-Provider = %q, want openai-compatible", got)
	}
	if got := rec.Header().Get("X-Rerank-Model"); got != "qwen3-reranker" {
		t.Fatalf("X-Rerank-Model = %q, want qwen3-reranker", got)
	}
	if got := rec.Header().Get("X-Rerank-Applied"); got != "true" {
		t.Fatalf("X-Rerank-Applied = %q, want true", got)
	}
	if got := rec.Header().Get("X-Rerank-Candidates"); got != "3" {
		t.Fatalf("X-Rerank-Candidates = %q, want 3", got)
	}
	if len(reranker.seenIDs) != 1 {
		t.Fatalf("reranker calls = %d, want 1", len(reranker.seenIDs))
	}
	got := rec.Body.String()
	first := strings.Index(got, `"id":"obs-3"`)
	last := strings.Index(got, `"id":"obs-1"`)
	if first == -1 || last == -1 || first > last {
		t.Fatalf("response order not reranked (want obs-3 before obs-1): %s", got)
	}
}

func TestSearchHybridRerankFailsOpenAndReportsFalse(t *testing.T) {
	ops := &recordingSearchOps{fakeOperations: newFakeOperations()}
	ops.results = []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1, PublicID: "obs-1", Title: "one"}},
		{Observation: domain.Observation{ID: 2, PublicID: "obs-2", Title: "two"}},
	}
	reranker := &rerankFakeReranker{err: errors.New("rerank provider unreachable")}
	handler, _ := rerankTestHandler(t, reranker, ops, "openai-compatible", "qwen3-reranker")

	rec := doHybridSearch(handler)
	if rec.Code != http.StatusOK {
		t.Fatalf("rerank failure must fail open, status = %d", rec.Code)
	}
	if got := rec.Header().Get("X-Rerank-Applied"); got != "false" {
		t.Fatalf("X-Rerank-Applied = %q, want false after rerank error", got)
	}
	if got := rec.Header().Get("X-Rerank-Candidates"); got != "" {
		t.Fatalf("X-Rerank-Candidates = %q, want absent when not applied", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "obs-1") || !strings.Contains(body, "obs-2") {
		t.Fatalf("pre-rerank fused results must still be served: %s", body)
	}
	if strings.Index(body, `"id":"obs-1"`) > strings.Index(body, `"id":"obs-2"`) {
		t.Fatalf("pre-rerank fused order must be preserved on fail-open: %s", body)
	}
}

func TestSearchHybridWithoutRerankerReportsAppliedFalse(t *testing.T) {
	ops := &recordingSearchOps{fakeOperations: newFakeOperations()}
	ops.results = []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1, PublicID: "obs-1", Title: "one"}},
	}
	handler, _ := rerankTestHandler(t, nil, ops, "none", "")

	rec := doHybridSearch(handler)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("X-Rerank-Provider"); got != "none" {
		t.Fatalf("X-Rerank-Provider = %q, want none", got)
	}
	if got := rec.Header().Get("X-Rerank-Applied"); got != "false" {
		t.Fatalf("X-Rerank-Applied = %q, want false without a composed reranker", got)
	}
}

func TestRerankCandidatePoolNeverBelowLimit(t *testing.T) {
	for _, tc := range []struct{ limit, want int }{
		{1, 4},
		{10, 40},
		{20, 64},
		{100, 100},
	} {
		if got := rerankCandidatePool(tc.limit); got != tc.want {
			t.Fatalf("rerankCandidatePool(%d) = %d, want %d", tc.limit, got, tc.want)
		}
	}
}

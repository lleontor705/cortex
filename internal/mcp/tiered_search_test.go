package mcp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/retrieval"
)

type mockRemoteSearcher struct {
	called    bool
	callCount int
	lastQuery string
	lastOpts  domain.SearchOptions
	results   []*domain.SearchResult
	err       error
}

func (m *mockRemoteSearcher) SearchHybrid(ctx context.Context, query string, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
	m.called = true
	m.callCount++
	m.lastQuery = query
	m.lastOpts = opts
	return m.results, m.err
}

func TestHandleSearch_CRAGEscalation_EmptyLocal(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")

	remoteMock := &mockRemoteSearcher{
		results: []*domain.SearchResult{
			{
				Observation: domain.Observation{
					PublicID:  "remote-obs-uuid-1",
					Title:     "Remote Architecture Decision",
					Content:   "Architecture decision stored on remote server",
					Type:      "architecture",
					Project:   "demo",
					Scope:     "project",
					CreatedAt: time.Now(),
				},
				Rank: 0.92,
			},
		},
	}
	stores.RemoteSearch = remoteMock

	handler := handleSearch(stores)
	result := callTool(t, handler, map[string]interface{}{
		"query":   "nonexistent local query",
		"project": "demo",
	})

	text := resultText(result)
	if !remoteMock.called {
		t.Fatal("expected remote search escalation when local results are empty")
	}
	if !strings.Contains(text, "Remote Architecture Decision") {
		t.Errorf("expected remote result in output, got %q", text)
	}
	if !strings.Contains(text, "strategy=remote") {
		t.Errorf("expected score_breakdown to indicate remote strategy, got %q", text)
	}
}

func TestHandleSearch_CRAGEscalation_ForcedScopes(t *testing.T) {
	// Tests that "team" and "global" scopes force escalation to RemoteSearch
	for _, forcedScope := range []string{"team", "global"} {
		t.Run("Scope_"+forcedScope, func(t *testing.T) {
			stores := setupTestStores(t)
			createSession(t, stores, "s1", "demo")
			saveObs(t, stores, "Local note", "demo", "s1")

			remoteMock := &mockRemoteSearcher{
				results: []*domain.SearchResult{
					{
						Observation: domain.Observation{
							ID:        100,
							Title:     "Team Shared Protocol",
							Content:   "Shared across all engineers",
							Type:      "decision",
							Project:   "demo",
							Scope:     forcedScope,
							CreatedAt: time.Now(),
						},
						Rank: 0.85,
					},
				},
			}
			stores.RemoteSearch = remoteMock

			handler := handleSearch(stores)
			result := callTool(t, handler, map[string]interface{}{
				"query":   "note",
				"project": "demo",
				"scope":   forcedScope,
			})

			if !remoteMock.called {
				t.Fatalf("expected remote search escalation for scope %q", forcedScope)
			}
			if remoteMock.lastOpts.Scope != forcedScope {
				t.Errorf("expected remoteOpts.Scope = %q, got %q", forcedScope, remoteMock.lastOpts.Scope)
			}
			text := resultText(result)
			if !strings.Contains(text, "Team Shared Protocol") {
				t.Errorf("expected team protocol in output, got %q", text)
			}
		})
	}
}

func TestHandleSearch_ScopeLocal_NeverEscalates(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")

	remoteMock := &mockRemoteSearcher{
		results: []*domain.SearchResult{
			{
				Observation: domain.Observation{
					Title:   "Should Not Be Returned",
					Content: "Remote content",
				},
			},
		},
	}
	stores.RemoteSearch = remoteMock

	handler := handleSearch(stores)
	result := callTool(t, handler, map[string]interface{}{
		"query": "nonexistent",
		"scope": "local",
	})

	if remoteMock.called {
		t.Fatal("remote search should NOT be called when scope is 'local'")
	}
	text := resultText(result)
	if !strings.Contains(text, "No memories found") {
		t.Errorf("expected no memories found, got %q", text)
	}
}

func TestHandleSearch_RemoteFailure_OfflineResilience(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	saveObs(t, stores, "Important local discovery", "demo", "s1")

	// Remote searcher returns network failure
	remoteMock := &mockRemoteSearcher{
		err: errors.New("connection refused: server offline"),
	}
	stores.RemoteSearch = remoteMock

	handler := handleSearch(stores)
	result := callTool(t, handler, map[string]interface{}{
		"query":   "discovery",
		"project": "demo",
		"scope":   "team", // Forces escalation, but remote is down
	})

	if !remoteMock.called {
		t.Fatal("expected remote search to be attempted")
	}

	// Should not crash and should return local search result (or graceful no memories if scope was filtered)
	// Because scope="team" was requested and local has no scope="team", no memories found is returned gracefully.
	if result.IsError {
		t.Fatalf("tool handler returned error when remote failed: %+v", result)
	}
}

func TestHandleSearch_FusionAndDeduplication(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	saveObs(t, stores, "Shared Architecture Pattern", "demo", "s1")

	// Remote returns same observation title + a new unique one
	remoteMock := &mockRemoteSearcher{
		results: []*domain.SearchResult{
			{
				Observation: domain.Observation{
					PublicID:  "remote-pattern-uuid",
					Title:     "Shared Architecture Pattern",
					Content:   "Updated content from server",
					Type:      "architecture",
					Project:   "demo",
					Scope:     "project",
					CreatedAt: time.Now(),
				},
				Rank: 0.90,
			},
			{
				Observation: domain.Observation{
					PublicID:  "remote-unique-uuid",
					Title:     "Unique Remote Knowledge",
					Content:   "Only on server",
					Type:      "pattern",
					Project:   "demo",
					Scope:     "project",
					CreatedAt: time.Now(),
				},
				Rank: 0.85,
			},
		},
	}
	stores.RemoteSearch = remoteMock

	handler := handleSearch(stores)
	result := callTool(t, handler, map[string]interface{}{
		"query":   "Pattern",
		"project": "demo",
		"scope":   "team", // Forces escalation
	})

	text := resultText(result)
	if !strings.Contains(text, "Shared Architecture Pattern") {
		t.Errorf("expected Shared Architecture Pattern in output, got %q", text)
	}
	if !strings.Contains(text, "Unique Remote Knowledge") {
		t.Errorf("expected Unique Remote Knowledge in output, got %q", text)
	}
	if !strings.Contains(text, "strategy=remote") {
		t.Errorf("expected strategy=remote in breakdown, got %q", text)
	}
}

// --- Rerank wiring gate (REQ-RNK-002) ---

// rerankCandidates builds a fixture where RRF and late-interaction disagree:
// the top RRF candidate shares no query token, while lower-RRF candidates
// repeat the whole query, so a visible reorder proves rerank ran pre-limit.
func rerankCandidates() ([]*domain.SearchResult, []*domain.VectorSearchResult) {
	fts := []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1, Title: "Release Checklist", Content: "Deploy notes for the web console release.", Type: "note", Project: "demo"}, Rank: 0.5},
		{Observation: domain.Observation{ID: 2, Title: "Tenant Isolation Deep Dive", Content: "Postgres row level security enforces tenant isolation policy per workspace.", Type: "decision", Project: "demo"}, Rank: 0.4},
		{Observation: domain.Observation{ID: 3, Title: "Rotation Runbook", Content: "Rotate signing keys on a schedule.", Type: "note", Project: "demo"}, Rank: 0.45},
		{Observation: domain.Observation{ID: 4, Title: "Security Policy Notes", Content: "Tenant isolation policy is enforced by the gateway.", Type: "decision", Project: "demo"}, Rank: 0.3},
	}
	vec := []*domain.VectorSearchResult{
		{Observation: domain.Observation{ID: 1, Title: "Release Checklist", Content: "Deploy notes for the web console release.", Type: "note", Project: "demo"}, Similarity: 0.9},
		{Observation: domain.Observation{ID: 5, Title: "Isolation Architecture", Content: "tenant isolation policy decisions live here", Type: "decision", Project: "demo"}, Similarity: 0.8},
		{Observation: domain.Observation{ID: 2, Title: "Tenant Isolation Deep Dive", Content: "Postgres row level security enforces tenant isolation policy per workspace.", Type: "decision", Project: "demo"}, Similarity: 0.7},
	}
	return fts, vec
}

func resultIDs(results []*domain.SearchResult) []int64 {
	ids := make([]int64, len(results))
	for i, r := range results {
		ids[i] = r.ID
	}
	return ids
}

func plainFuse(opts retrieval.FuseOptions) []*domain.SearchResult {
	fts, vec := rerankCandidates()
	return retrieval.FuseResultsWithOptions(fts, vec, opts)
}

func gatedFuse(query string, opts retrieval.FuseOptions) []*domain.SearchResult {
	fts, vec := rerankCandidates()
	return fuseWithConfiguredRerank(query, fts, vec, opts)
}

// isolateUserConfig points config.Load at a throwaway home because product
// code resolves the user config exclusively from HOME/USERPROFILE: a real
// ~/.cortex/cortex.yaml that pins search.rerank_provider would shadow the
// CORTEX_RERANK_* env overlay, which config.Load only applies when the file
// value is empty.
func isolateUserConfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func withInjectedReranker(t *testing.T, reranker retrieval.Reranker) {
	t.Helper()
	rerankGateMu.Lock()
	rerankGateValue = reranker
	rerankGateReady = true
	rerankGateMu.Unlock()
	t.Cleanup(resetRerankGate)
}

type failingReranker struct{}

func (failingReranker) Rerank(string, []*domain.SearchResult) ([]*domain.SearchResult, error) {
	return nil, errors.New("rerank endpoint unavailable")
}

type setBreakingReranker struct{}

func (setBreakingReranker) Rerank(_ string, docs []*domain.SearchResult) ([]*domain.SearchResult, error) {
	return docs[:1], nil
}

// TestFuseWithConfiguredRerank_DefaultIsByteIdentical pins the disabled gate:
// with the default search.rerank_provider the helper must return exactly what
// the pre-change fusion call returns, candidate for candidate and rank for rank.
func TestFuseWithConfiguredRerank_DefaultIsByteIdentical(t *testing.T) {
	isolateUserConfig(t)

	if cfg, err := config.Load(""); err == nil && cfg.Search.RerankProvider != "" &&
		!strings.EqualFold(cfg.Search.RerankProvider, "none") {
		t.Skipf("search.rerank_provider=%q in this environment; byte-identity pin requires the default", cfg.Search.RerankProvider)
	}

	t.Setenv("CORTEX_RERANK_PROVIDER", "")
	resetRerankGate()
	t.Cleanup(resetRerankGate)

	if reranker := configuredReranker(); reranker != nil {
		t.Fatalf("default config constructed %T; the disabled default must build zero rerank machinery", reranker)
	}

	opts := retrieval.FuseOptions{Limit: 2}
	got := gatedFuse("tenant isolation policy", opts)
	want := plainFuse(opts)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("disabled rerank changed fused output:\n got = %v\nwant = %v", resultIDs(got), resultIDs(want))
	}
	if len(got) != opts.Limit {
		t.Fatalf("len(got) = %d, want %d", len(got), opts.Limit)
	}
}

// TestFuseWithConfiguredRerank_LateInteractionOrdersBeforeLimit proves the
// enabled path: ordering visibly changes AND the winner comes from outside the
// disabled top-limit, so rerank ran on the whole fused set before truncation.
func TestFuseWithConfiguredRerank_LateInteractionOrdersBeforeLimit(t *testing.T) {
	isolateUserConfig(t)
	t.Setenv("CORTEX_RERANK_PROVIDER", "late-interaction")
	resetRerankGate()
	t.Cleanup(resetRerankGate)

	if reranker := configuredReranker(); reranker == nil {
		t.Fatal("late-interaction provider constructed no reranker")
	}

	opts := retrieval.FuseOptions{Limit: 2}
	got := gatedFuse("tenant isolation policy", opts)
	plain := plainFuse(opts)

	if len(got) != opts.Limit {
		t.Fatalf("len(got) = %d, want %d: the limit still applies after rerank", len(got), opts.Limit)
	}
	if reflect.DeepEqual(resultIDs(got), resultIDs(plain)) {
		t.Fatalf("rerank ordering did not apply: got %v, same as un-reranked fusion", resultIDs(got))
	}
	for _, r := range got {
		if r.ID == 1 {
			t.Fatalf("query-irrelevant candidate survived rerank: got %v", resultIDs(got))
		}
	}
	plainIDs := map[int64]bool{}
	for _, r := range plain {
		plainIDs[r.ID] = true
	}
	crossed := false
	for _, r := range got {
		if !plainIDs[r.ID] {
			crossed = true
		}
	}
	if !crossed {
		t.Fatalf("rerank ran post-limit: %v stays inside the un-reranked top-%d %v", resultIDs(got), opts.Limit, resultIDs(plain))
	}
}

// TestFuseWithConfiguredRerank_FailureKeepsFusionOrder exercises the degrade
// policy: a rerank error or a set-breaking provider response must fall back to
// the un-reranked fused order, still truncated to the limit, never fail.
func TestFuseWithConfiguredRerank_FailureKeepsFusionOrder(t *testing.T) {
	opts := retrieval.FuseOptions{Limit: 2}
	want := plainFuse(opts)

	for _, tc := range []struct {
		name     string
		reranker retrieval.Reranker
	}{
		{"endpoint error", failingReranker{}},
		{"dropped candidates", setBreakingReranker{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withInjectedReranker(t, tc.reranker)
			got := gatedFuse("tenant isolation policy", opts)
			if !reflect.DeepEqual(resultIDs(got), resultIDs(want)) {
				t.Fatalf("degraded output = %v, want un-reranked fusion %v", resultIDs(got), resultIDs(want))
			}
		})
	}
}

// TestFuseWithConfiguredRerank_OpenAICompatibleRefusedLocally pins local
// fail-closed behavior: the server-only preset cannot be constructed here, so
// rerank degrades to off instead of erroring the query.
func TestFuseWithConfiguredRerank_OpenAICompatibleRefusedLocally(t *testing.T) {
	isolateUserConfig(t)
	t.Setenv("CORTEX_RERANK_PROVIDER", "openai-compatible")
	t.Setenv("CORTEX_RERANK_BASE_URL", "https://rerank.example.com/v1")
	resetRerankGate()
	t.Cleanup(resetRerankGate)

	if reranker := configuredReranker(); reranker != nil {
		t.Fatalf("local composition constructed %T; openai-compatible must be refused fail-closed", reranker)
	}

	opts := retrieval.FuseOptions{Limit: 2}
	got := gatedFuse("tenant isolation policy", opts)
	want := plainFuse(opts)
	if !reflect.DeepEqual(resultIDs(got), resultIDs(want)) {
		t.Fatalf("refused rerank changed fused output: got %v, want %v", resultIDs(got), resultIDs(want))
	}
}

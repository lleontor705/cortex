package retrieval

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/embedding"
)

func mkRerankDoc(id int64, title, topicKey, content string, rank float64) *domain.SearchResult {
	return &domain.SearchResult{
		Rank: rank,
		Observation: domain.Observation{
			ID:       id,
			Title:    title,
			TopicKey: topicKey,
			Content:  content,
		},
	}
}

// fakeClock replaces wall time so pacer gaps and 429 backoff are observable
// without ever sleeping in tests.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
}

func (c *fakeClock) recordedSleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

// rerankCapture records what the stub provider actually received.
type rerankCapture struct {
	mu          sync.Mutex
	method      string
	path        string
	auth        string
	contentType string
	bodies      []rerankRequest
}

func (c *rerankCapture) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.method = r.Method
	c.path = r.URL.Path
	c.auth = r.Header.Get("Authorization")
	c.contentType = r.Header.Get("Content-Type")
	var body rerankRequest
	_ = json.NewDecoder(r.Body).Decode(&body)
	c.bodies = append(c.bodies, body)
}

func respondRerank(w http.ResponseWriter, results []rerankResult) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rerankResponse{Results: results})
}

type rerankHarness struct {
	reranker Reranker
	clock    *fakeClock
	capture  *rerankCapture
}

func newRerankHarness(t *testing.T, handler http.HandlerFunc) *rerankHarness {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	policy := embedding.OutboundPolicy{AllowLoopback: true, AllowInsecureLoopbackHTTP: true}
	if err := policy.ApproveDestination(server.URL); err != nil {
		t.Fatalf("approve destination: %v", err)
	}
	t.Setenv("CORTEX_RERANK_API_KEY", "test-rerank-key")

	r, err := NewSecureReranker(RerankConfig{
		Provider: "openai-compatible",
		Model:    "qwen3-Reranker-8B",
		BaseURL:  server.URL,
	}, policy)
	if err != nil {
		t.Fatalf("NewSecureReranker: %v", err)
	}
	hr, ok := r.(*httpReranker)
	if !ok {
		t.Fatalf("secure factory returned %T, want *httpReranker", r)
	}
	clock := &fakeClock{now: time.Unix(1700000000, 0)}
	hr.pacer.now = clock.Now
	hr.pacer.sleep = clock.Sleep
	hr.sleep = clock.Sleep
	return &rerankHarness{reranker: r, clock: clock, capture: &rerankCapture{}}
}

func assertSameSet(t *testing.T, in, out []*domain.SearchResult) {
	t.Helper()
	if len(out) != len(in) {
		t.Fatalf("output length = %d, want %d (input set must be preserved)", len(out), len(in))
	}
	remaining := make(map[*domain.SearchResult]int, len(in))
	for _, d := range in {
		remaining[d]++
	}
	for _, d := range out {
		remaining[d]--
	}
	for ptr, n := range remaining {
		if n != 0 {
			t.Fatalf("input pointer %p (id=%d) multiplicity changed by %d", ptr, ptr.ID, n)
		}
	}
}

// --- factories ---

func TestNewRerankerLocalPresets(t *testing.T) {
	for _, provider := range []string{"", "none", "NONE"} {
		r, err := NewReranker(RerankConfig{Provider: provider})
		if r != nil || err != nil {
			t.Fatalf("provider %q: got (%v, %v), want (nil, nil) — disabled must build zero machinery", provider, r, err)
		}
	}
	r, err := NewReranker(RerankConfig{Provider: "late-interaction"})
	if err != nil || r == nil {
		t.Fatalf("late-interaction: got (%v, %v), want a local reranker", r, err)
	}
	if _, err := NewReranker(RerankConfig{Provider: "openai-compatible", BaseURL: "https://rerank.example.com/v1"}); err == nil {
		t.Fatal("local factory constructed the HTTP reranker; openai-compatible must be refused fail-closed")
	}
	if _, err := NewReranker(RerankConfig{Provider: "cohere"}); err == nil {
		t.Fatal("unknown provider accepted; construction must hard-fail")
	}
}

func TestNewSecureRerankerRequiresApprovedDestinationAndKey(t *testing.T) {
	policy := embedding.OutboundPolicy{AllowLoopback: true, AllowInsecureLoopbackHTTP: true}
	if err := policy.ApproveDestination("https://rerank.example.com/v1"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	t.Setenv("CORTEX_RERANK_API_KEY", "")
	if _, err := NewSecureReranker(RerankConfig{Provider: "openai-compatible", BaseURL: "https://rerank.example.com/v1"}, policy); err == nil || !strings.Contains(err.Error(), "CORTEX_RERANK_API_KEY") {
		t.Fatalf("missing key error = %v, want CORTEX_RERANK_API_KEY construction failure", err)
	}

	t.Setenv("CORTEX_RERANK_API_KEY", "k")
	unapproved := embedding.OutboundPolicy{AllowLoopback: true, AllowInsecureLoopbackHTTP: true}
	if _, err := NewSecureReranker(RerankConfig{Provider: "openai-compatible", BaseURL: "https://rerank.example.com/v1"}, unapproved); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("unapproved destination error = %v, want rejection", err)
	}

	r, err := NewSecureReranker(RerankConfig{Provider: "none"}, unapproved)
	if r != nil || err != nil {
		t.Fatalf("none preset: got (%v, %v), want (nil, nil)", r, err)
	}
	if _, err := NewSecureReranker(RerankConfig{Provider: "cohere"}, unapproved); err == nil {
		t.Fatal("unknown provider accepted on the secure path")
	}
}

// --- local default ---

func TestLateInteractionRerankerMatchesBlend(t *testing.T) {
	query := "postgresql tenant isolation"
	makeSet := func() []*domain.SearchResult {
		return []*domain.SearchResult{
			mkRerankDoc(1, "SQLite Single Binary", "storage", "Zero CGO local database.", 0.5),
			mkRerankDoc(2, "PostgreSQL Multi-Tenant Isolation", "security", "Row Level Security with tenant isolation and crypto tokens.", 0.4),
			mkRerankDoc(3, "Deployment Notes", "ops", "Unrelated release checklist for the web console.", 0.45),
			mkRerankDoc(4, "Tenant Isolation Deep Dive", "security", "Postgres policies, roles, and per-tenant scoping.", 0.3),
		}
	}
	direct := ReRankWithLateInteraction(query, makeSet())
	got, err := DefaultLateInteractionReranker{}.Rerank(query, makeSet())
	if err != nil {
		t.Fatalf("Rerank error: %v", err)
	}
	if len(direct) != len(got) {
		t.Fatalf("length = %d, want %d", len(got), len(direct))
	}
	for i := range direct {
		if got[i].ID != direct[i].ID {
			t.Fatalf("position %d: id = %d, want %d (must equal the 60/40 blend)", i, got[i].ID, direct[i].ID)
		}
		if got[i].Rank != direct[i].Rank {
			t.Fatalf("position %d: rank = %f, want %f (blend score must be unchanged)", i, got[i].Rank, direct[i].Rank)
		}
	}
}

func TestLateInteractionRerankerDeterministicTies(t *testing.T) {
	docs := []*domain.SearchResult{
		mkRerankDoc(1, "Twin", "same", "identical body text", 0.5),
		mkRerankDoc(2, "Twin", "same", "identical body text", 0.5),
		mkRerankDoc(3, "Twin", "same", "identical body text", 0.5),
	}
	got, err := DefaultLateInteractionReranker{}.Rerank("identical body text", docs)
	if err != nil {
		t.Fatalf("Rerank error: %v", err)
	}
	assertSameSet(t, docs, got)
	for i, wantID := range []int64{3, 2, 1} {
		if got[i].ID != wantID {
			t.Fatalf("position %d: id = %d, want %d (ties must break by ID DESC)", i, got[i].ID, wantID)
		}
	}
}

// --- HTTP implementation ---

func TestHTTPRerankHappyPath(t *testing.T) {
	var h *rerankHarness
	h = newRerankHarness(t, func(w http.ResponseWriter, r *http.Request) {
		h.capture.record(r)
		respondRerank(w, []rerankResult{
			{Index: 1, RelevanceScore: 0.9},
			{Index: 0, RelevanceScore: 0.2},
		})
	})
	docs := []*domain.SearchResult{
		mkRerankDoc(10, "First", "alpha", "body one", 0.7),
		mkRerankDoc(11, "Second", "beta", "body two", 0.6),
	}
	got, err := h.reranker.Rerank("which body", docs)
	if err != nil {
		t.Fatalf("Rerank error: %v", err)
	}
	assertSameSet(t, docs, got)
	if got[0].ID != 11 || got[0].Rank != 0.9 || got[1].ID != 10 || got[1].Rank != 0.2 {
		t.Fatalf("order/rank = (%d,%f),(%d,%f), want (11,0.9),(10,0.2)", got[0].ID, got[0].Rank, got[1].ID, got[1].Rank)
	}

	h.capture.mu.Lock()
	defer h.capture.mu.Unlock()
	if h.capture.method != http.MethodPost || h.capture.path != "/v1/rerank" {
		t.Fatalf("request = %s %s, want POST /v1/rerank", h.capture.method, h.capture.path)
	}
	if h.capture.auth != "Bearer test-rerank-key" {
		t.Fatalf("authorization = %q, want bearer key from CORTEX_RERANK_API_KEY", h.capture.auth)
	}
	if !strings.HasPrefix(h.capture.contentType, "application/json") {
		t.Fatalf("content-type = %q", h.capture.contentType)
	}
	if len(h.capture.bodies) != 1 {
		t.Fatalf("requests = %d, want 1", len(h.capture.bodies))
	}
	body := h.capture.bodies[0]
	if body.Model != "qwen3-Reranker-8B" || body.Query != "which body" || body.TopN != 2 {
		t.Fatalf("body = %+v, want model/query/top_n", body)
	}
	if len(body.Documents) != 2 || body.Documents[0] != "First alpha body one" || body.Documents[1] != "Second beta body two" {
		t.Fatalf("documents = %v", body.Documents)
	}
}

func TestHTTPRerankBatchesAtMost32Documents(t *testing.T) {
	var h *rerankHarness
	h = newRerankHarness(t, func(w http.ResponseWriter, r *http.Request) {
		h.capture.record(r)
		h.capture.mu.Lock()
		n := len(h.capture.bodies[len(h.capture.bodies)-1].Documents)
		h.capture.mu.Unlock()
		results := make([]rerankResult, 0, n)
		for i := 0; i < n; i++ {
			results = append(results, rerankResult{Index: i, RelevanceScore: 0})
		}
		respondRerank(w, results)
	})

	docs := make([]*domain.SearchResult, 64)
	for i := range docs {
		docs[i] = mkRerankDoc(int64(i+1), "Doc", "batch", "shared content", 0.5)
	}
	got, err := h.reranker.Rerank("shared content", docs)
	if err != nil {
		t.Fatalf("Rerank error: %v", err)
	}
	assertSameSet(t, docs, got)

	h.capture.mu.Lock()
	defer h.capture.mu.Unlock()
	if len(h.capture.bodies) != 2 {
		t.Fatalf("requests = %d, want 2 (64 docs must split at 32)", len(h.capture.bodies))
	}
	for i, body := range h.capture.bodies {
		if len(body.Documents) != 32 {
			t.Fatalf("request %d carried %d documents, want 32", i, len(body.Documents))
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i].ID >= got[i-1].ID {
			t.Fatalf("all-zero scores must order by ID DESC, got %d after %d", got[i].ID, got[i-1].ID)
		}
	}
}

func TestHTTPRerankThrottlesToSixtyPerMinute(t *testing.T) {
	var h *rerankHarness
	h = newRerankHarness(t, func(w http.ResponseWriter, r *http.Request) {
		h.capture.record(r)
		respondRerank(w, []rerankResult{{Index: 0, RelevanceScore: 0.5}})
	})
	if gap := time.Minute / rerankRequestsPerMin; gap != time.Second {
		t.Fatalf("min gap = %v, want 1s for RPM 60", gap)
	}
	for round := 0; round < 2; round++ {
		docs := []*domain.SearchResult{mkRerankDoc(int64(round+1), "Solo", "t", "only doc", 0.5)}
		if _, err := h.reranker.Rerank("only doc", docs); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	sleeps := h.clock.recordedSleeps()
	if len(sleeps) != 1 || sleeps[0] != time.Second {
		t.Fatalf("pacer sleeps = %v, want exactly one 1s gap between requests", sleeps)
	}
}

func TestHTTPRerank429BackoffAndGiveUp(t *testing.T) {
	t.Run("backs off then succeeds", func(t *testing.T) {
		var requests int
		var mu sync.Mutex
		var h *rerankHarness
		h = newRerankHarness(t, func(w http.ResponseWriter, r *http.Request) {
			h.capture.record(r)
			mu.Lock()
			requests++
			n := requests
			mu.Unlock()
			if n <= 2 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			respondRerank(w, []rerankResult{{Index: 0, RelevanceScore: 0.8}})
		})
		docs := []*domain.SearchResult{mkRerankDoc(1, "Solo", "t", "only doc", 0.5)}
		got, err := h.reranker.Rerank("only doc", docs)
		if err != nil {
			t.Fatalf("Rerank error: %v", err)
		}
		assertSameSet(t, docs, got)
		mu.Lock()
		gotRequests := requests
		mu.Unlock()
		if gotRequests != 3 {
			t.Fatalf("requests = %d, want 3 (2 throttled + 1 success)", gotRequests)
		}
		want := []time.Duration{time.Second, 2 * time.Second}
		sleeps := h.clock.recordedSleeps()
		if len(sleeps) != len(want) {
			t.Fatalf("backoff sleeps = %v, want %v", sleeps, want)
		}
		for i := range want {
			if sleeps[i] != want[i] {
				t.Fatalf("backoff sleep %d = %v, want %v (1s<<attempt)", i, sleeps[i], want[i])
			}
		}
	})

	t.Run("gives up after three retries", func(t *testing.T) {
		var requests int
		var mu sync.Mutex
		h := newRerankHarness(t, func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			requests++
			mu.Unlock()
			w.WriteHeader(http.StatusTooManyRequests)
		})
		docs := []*domain.SearchResult{mkRerankDoc(1, "Solo", "t", "only doc", 0.7)}
		got, err := h.reranker.Rerank("only doc", docs)
		if err == nil || !strings.Contains(err.Error(), "rate limited") {
			t.Fatalf("error = %v, want rate-limited give-up", err)
		}
		if got != nil {
			t.Fatalf("output = %v, want nil on error (discard output)", got)
		}
		if docs[0].Rank != 0.7 {
			t.Fatalf("input rank mutated to %f on failure", docs[0].Rank)
		}
		mu.Lock()
		gotRequests := requests
		mu.Unlock()
		if gotRequests != 4 {
			t.Fatalf("requests = %d, want 4 (initial + 3 retries)", gotRequests)
		}
		want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
		sleeps := h.clock.recordedSleeps()
		if len(sleeps) != len(want) {
			t.Fatalf("backoff sleeps = %v, want %v", sleeps, want)
		}
		for i := range want {
			if sleeps[i] != want[i] {
				t.Fatalf("backoff sleep %d = %v, want %v", i, sleeps[i], want[i])
			}
		}
	})
}

func TestHTTPRerankDeterministicTies(t *testing.T) {
	var h *rerankHarness
	h = newRerankHarness(t, func(w http.ResponseWriter, r *http.Request) {
		h.capture.record(r)
		respondRerank(w, []rerankResult{
			{Index: 0, RelevanceScore: 0.5},
			{Index: 1, RelevanceScore: 0.5},
			{Index: 2, RelevanceScore: 0.5},
		})
	})
	docs := []*domain.SearchResult{
		mkRerankDoc(7, "A", "t", "same", 0.5),
		mkRerankDoc(8, "B", "t", "same", 0.5),
		mkRerankDoc(9, "C", "t", "same", 0.5),
	}
	got, err := h.reranker.Rerank("same", docs)
	if err != nil {
		t.Fatalf("Rerank error: %v", err)
	}
	assertSameSet(t, docs, got)
	for i, wantID := range []int64{9, 8, 7} {
		if got[i].ID != wantID {
			t.Fatalf("position %d: id = %d, want %d (ties break by ID DESC)", i, got[i].ID, wantID)
		}
	}
}

func TestHTTPRerankRejectsMalformedIndexes(t *testing.T) {
	cases := []struct {
		name    string
		results []rerankResult
	}{
		{"out of range", []rerankResult{{Index: 5, RelevanceScore: 0.9}, {Index: 0, RelevanceScore: 0.1}}},
		{"duplicate index", []rerankResult{{Index: 0, RelevanceScore: 0.9}, {Index: 0, RelevanceScore: 0.1}}},
		{"incomplete coverage", []rerankResult{{Index: 0, RelevanceScore: 0.9}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRerankHarness(t, func(w http.ResponseWriter, _ *http.Request) {
				respondRerank(w, tc.results)
			})
			docs := []*domain.SearchResult{
				mkRerankDoc(1, "First", "t", "one", 0.7),
				mkRerankDoc(2, "Second", "t", "two", 0.6),
			}
			got, err := h.reranker.Rerank("query", docs)
			if err == nil {
				t.Fatalf("malformed batch accepted, output = %v", got)
			}
			if got != nil {
				t.Fatalf("output = %v, want nil on error (discard output)", got)
			}
			if docs[0].Rank != 0.7 || docs[1].Rank != 0.6 {
				t.Fatalf("input mutated on rejection: ranks = %f, %f", docs[0].Rank, docs[1].Rank)
			}
		})
	}
}

// --- configuration (REQ-CFG-001) ---

func neutralizeRerankEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"CORTEX_RERANK_PROVIDER",
		"CORTEX_RERANK_MODEL",
		"CORTEX_RERANK_BASE_URL",
		"CORTEX_RERANK_API_KEY",
		"CORTEX_EMBEDDING_PROVIDER",
		"CORTEX_EMBEDDING_MODEL",
		"CORTEX_EMBEDDING_BASE_URL",
	} {
		t.Setenv(key, "")
	}
}

func writeRerankConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cortex.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestRerankConfigDefaultsToDisabled(t *testing.T) {
	neutralizeRerankEnv(t)
	cfg, err := config.Load(writeRerankConfig(t, "database:\n  in_memory: true\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Search.RerankProvider != "none" {
		t.Fatalf("rerank_provider = %q, want none", cfg.Search.RerankProvider)
	}
	if got := config.DefaultConfig().Search.RerankProvider; got != "none" {
		t.Fatalf("default rerank_provider = %q, want none", got)
	}
	r, err := NewReranker(RerankConfig{Provider: cfg.Search.RerankProvider})
	if r != nil || err != nil {
		t.Fatalf("disabled preset built (%v, %v), want zero machinery", r, err)
	}
}

func TestRerankConfigEnvMirror(t *testing.T) {
	neutralizeRerankEnv(t)
	t.Setenv("CORTEX_RERANK_PROVIDER", "openai-compatible")
	t.Setenv("CORTEX_RERANK_MODEL", "qwen3-Reranker-8B")
	t.Setenv("CORTEX_RERANK_BASE_URL", "https://rerank.example.com/v1")

	cfg, err := config.Load(writeRerankConfig(t, "database:\n  in_memory: true\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Search.RerankProvider != "openai-compatible" ||
		cfg.Search.RerankModel != "qwen3-Reranker-8B" ||
		cfg.Search.RerankBaseURL != "https://rerank.example.com/v1" {
		t.Fatalf("env mirror = provider %q model %q base_url %q",
			cfg.Search.RerankProvider, cfg.Search.RerankModel, cfg.Search.RerankBaseURL)
	}
}

func TestRerankConfigFileWinsOverEnv(t *testing.T) {
	neutralizeRerankEnv(t)
	t.Setenv("CORTEX_RERANK_PROVIDER", "openai-compatible")
	t.Setenv("CORTEX_RERANK_BASE_URL", "https://rerank.example.com/v1")
	t.Setenv("CORTEX_RERANK_MODEL", "qwen3-Reranker-8B")

	path := writeRerankConfig(t, "database:\n  in_memory: true\nsearch:\n  rerank_provider: late-interaction\n")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Search.RerankProvider != "late-interaction" {
		t.Fatalf("rerank_provider = %q, want file value late-interaction to win over env", cfg.Search.RerankProvider)
	}
	if cfg.Search.RerankModel != "qwen3-Reranker-8B" {
		t.Fatalf("rerank_model = %q, want env fallback for unset keys", cfg.Search.RerankModel)
	}
}

func TestRerankConfigRejectsUnknownProvider(t *testing.T) {
	neutralizeRerankEnv(t)
	path := writeRerankConfig(t, "database:\n  in_memory: true\nsearch:\n  rerank_provider: cohere\n")
	if _, err := config.Load(path); err == nil || !strings.Contains(err.Error(), "rerank_provider") {
		t.Fatalf("unknown provider error = %v, want rerank_provider hard-fail", err)
	}
}

func TestRerankConfigRemoteDestinationPolicy(t *testing.T) {
	neutralizeRerankEnv(t)

	missing := writeRerankConfig(t, "database:\n  in_memory: true\nsearch:\n  rerank_provider: openai-compatible\n")
	if _, err := config.Load(missing); err == nil || !strings.Contains(err.Error(), "rerank_base_url") {
		t.Fatalf("missing base_url error = %v, want rerank_base_url rejection", err)
	}

	insecure := writeRerankConfig(t, "database:\n  in_memory: true\nsearch:\n  rerank_provider: openai-compatible\n  rerank_base_url: http://10.0.0.9/v1\n")
	if _, err := config.Load(insecure); err == nil || !strings.Contains(err.Error(), "rerank_base_url") {
		t.Fatalf("insecure destination error = %v, want rerank_base_url transport rejection", err)
	}

	approved := writeRerankConfig(t, "database:\n  in_memory: true\nsearch:\n  rerank_provider: openai-compatible\n  rerank_base_url: https://rerank.example.com/v1\n")
	cfg, err := config.Load(approved)
	if err != nil {
		t.Fatalf("approved destination rejected: %v", err)
	}
	if cfg.Search.RerankBaseURL != "https://rerank.example.com/v1" {
		t.Fatalf("rerank_base_url = %q", cfg.Search.RerankBaseURL)
	}
}

func TestResolveRerankAPIKeyEnvOnly(t *testing.T) {
	t.Setenv("CORTEX_RERANK_API_KEY", "  secret-key  ")
	if got := config.ResolveRerankAPIKey(); got != "secret-key" {
		t.Fatalf("ResolveRerankAPIKey = %q, want trimmed env value", got)
	}
	t.Setenv("CORTEX_RERANK_API_KEY", "")
	if got := config.ResolveRerankAPIKey(); got != "" {
		t.Fatalf("ResolveRerankAPIKey = %q, want empty when unset", got)
	}
}

func TestRerankEndpointVersionInclusiveBase(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://api.nan.builders/v1", "https://api.nan.builders/v1/rerank"},
		{"https://api.nan.builders/v1/", "https://api.nan.builders/v1/rerank"},
		{"https://api.nan.builders/v2", "https://api.nan.builders/v2/rerank"},
		{"https://api.nan.builders", "https://api.nan.builders/v1/rerank"},
		{"https://api.nan.builders/", "https://api.nan.builders/v1/rerank"},
		{"https://host.example/rerank-api", "https://host.example/rerank-api/v1/rerank"},
	} {
		if got := rerankEndpoint(tc.base); got != tc.want {
			t.Errorf("rerankEndpoint(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

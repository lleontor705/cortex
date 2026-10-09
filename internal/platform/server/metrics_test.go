package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

// parseExposition is a minimal v0.0.4 text parser: it validates the structural
// contract (HELP/TYPE before the sample, one sample per declared metric, all
// values numeric) and returns name → value.
func parseExposition(t *testing.T, body string) map[string]float64 {
	t.Helper()
	samples := map[string]float64{}
	declared := map[string]string{} // name -> type
	scanner := bufio.NewScanner(strings.NewReader(body))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# HELP "):
			rest := strings.TrimPrefix(line, "# HELP ")
			name, _, ok := strings.Cut(rest, " ")
			if !ok || name == "" {
				t.Fatalf("line %d: malformed HELP: %q", lineNo, line)
			}
		case strings.HasPrefix(line, "# TYPE "):
			rest := strings.TrimPrefix(line, "# TYPE ")
			name, kind, ok := strings.Cut(rest, " ")
			if !ok || name == "" || (kind != "counter" && kind != "gauge") {
				t.Fatalf("line %d: malformed TYPE: %q", lineNo, line)
			}
			declared[name] = kind
		case strings.HasPrefix(line, "#"):
			t.Fatalf("line %d: unknown comment form: %q", lineNo, line)
		default:
			name, value, ok := strings.Cut(line, " ")
			if !ok {
				t.Fatalf("line %d: sample without value: %q", lineNo, line)
			}
			v, err := strconv.ParseFloat(value, 64)
			if err != nil {
				t.Fatalf("line %d: non-numeric sample value %q: %v", lineNo, value, err)
			}
			if _, dup := samples[name]; dup {
				t.Fatalf("line %d: duplicate sample %q", lineNo, name)
			}
			if _, ok := declared[name]; !ok {
				t.Fatalf("line %d: sample %q has no TYPE declaration", lineNo, name)
			}
			samples[name] = v
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("exposition contained no samples")
	}
	return samples
}

func TestMetricsRegistryRenderIsParsableV004(t *testing.T) {
	r := &metricsRegistry{}
	requests := r.counter("cortex_test_requests_total", "Test requests.")
	requests.Add(7)
	gaugeOnly := &serverMetrics{registry: r}

	var out strings.Builder
	gaugeOnly.render(&out)
	samples := parseExposition(t, out.String())

	if got := samples["cortex_test_requests_total"]; got != 7 {
		t.Fatalf("cortex_test_requests_total = %v, want 7", got)
	}
	if _, ok := samples["cortex_process_uptime_seconds"]; !ok {
		t.Fatal("exposition missing the process uptime gauge")
	}
	if !strings.Contains(out.String(), "# TYPE cortex_test_requests_total counter") {
		t.Fatal("exposition missing counter TYPE declaration")
	}
	if !strings.Contains(out.String(), "# TYPE cortex_process_uptime_seconds gauge") {
		t.Fatal("exposition missing gauge TYPE declaration")
	}
}

func TestMetricsRegistryConcurrentIncrementIsAtomic(t *testing.T) {
	r := &metricsRegistry{}
	c := r.counter("cortex_test_concurrent_total", "Concurrent increments.")
	const goroutines, per = 16, 100
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < per; j++ {
				c.Inc()
			}
		}()
	}
	wg.Wait()
	if got := c.Value(); got != goroutines*per {
		t.Fatalf("counter = %d, want %d (lost updates)", got, goroutines*per)
	}
	if same := r.counter("cortex_test_concurrent_total", "Concurrent increments."); same != c {
		t.Fatal("registration must be idempotent per metric name")
	}
}

// stubEmbedding classifies outcomes for the cache-metrics seam test.
type stubEmbedding struct {
	calls int
	err   error
	mu    sync.Mutex
}

func (s *stubEmbedding) Embed(context.Context, string) ([]float32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return []float32{0.1, 0.2}, nil
}
func (s *stubEmbedding) Dimensions() int { return 2 }
func (s *stubEmbedding) Model() string   { return "stub" }

func TestQueryEmbeddingCacheIncrementsMetricsSeams(t *testing.T) {
	hitsBefore := prometheusMetrics.queryEmbeddingCacheHits.Value()
	missesBefore := prometheusMetrics.queryEmbeddingCacheMisses.Value()
	requestsBefore := prometheusMetrics.embeddingProviderRequests.Value()
	errorsBefore := prometheusMetrics.embeddingProviderErrors.Value()

	svc := &stubEmbedding{}
	cache := newQueryEmbeddingCache(8, 1_000_000_000_000) // effectively unbounded TTL
	model := "stub"
	ctx := context.Background()
	// First call: a miss (one provider request). Second call: a hit.
	for i := 0; i < 2; i++ {
		if _, err := cache.embed(ctx, svc, model, "query"); err != nil {
			t.Fatalf("embed %d: %v", i, err)
		}
	}
	if svc.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (second query must be a cache hit)", svc.calls)
	}
	if got := prometheusMetrics.queryEmbeddingCacheHits.Value() - hitsBefore; got != 1 {
		t.Fatalf("cache hits delta = %d, want 1", got)
	}
	if got := prometheusMetrics.queryEmbeddingCacheMisses.Value() - missesBefore; got != 1 {
		t.Fatalf("cache misses delta = %d, want 1", got)
	}
	if got := prometheusMetrics.embeddingProviderRequests.Value() - requestsBefore; got != 1 {
		t.Fatalf("provider requests delta = %d, want 1", got)
	}
	if got := prometheusMetrics.embeddingProviderErrors.Value() - errorsBefore; got != 0 {
		t.Fatalf("provider errors delta = %d, want 0", got)
	}

	// A provider failure on a fresh key classifies as one provider request
	// AND one provider error.
	failingCache := newQueryEmbeddingCache(8, 1_000_000_000_000)
	errSvc := &stubEmbedding{err: errors.New("provider down")}
	requestsBeforeErr := prometheusMetrics.embeddingProviderRequests.Value()
	if _, err := failingCache.embed(ctx, errSvc, model, "query"); err == nil {
		t.Fatal("expected the failing provider round-trip to surface its error")
	}
	if got := prometheusMetrics.embeddingProviderRequests.Value() - requestsBeforeErr; got != 1 {
		t.Fatalf("provider requests delta = %d, want 1", got)
	}
	if got := prometheusMetrics.embeddingProviderErrors.Value() - errorsBefore; got != 1 {
		t.Fatalf("provider errors delta = %d, want 1", got)
	}
}

func TestMetricsEndpointAdminGated(t *testing.T) {
	ops := newFakeOperations()
	cfg := config.Config{HTTP: config.HTTPConfig{Token: "test-token"}}
	h, _ := newVerifiedHTTPHandler(cfg, ops, func(context.Context) error { return nil })

	t.Run("unauthenticated request rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
			t.Fatalf("unauthenticated /metrics status = %d, want 401/403", rec.Code)
		}
	})
	t.Run("authenticated admin scrape returns valid exposition", func(t *testing.T) {
		prometheusMetrics.queryEmbeddingCacheHits.Add(3)
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/metrics status = %d, body=%s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Fatalf("content type = %q, want text/plain exposition", ct)
		}
		samples := parseExposition(t, rec.Body.String())
		if got := samples["cortex_query_embedding_cache_hits_total"]; got < 3 {
			t.Fatalf("exposed cache hits = %v, want >= 3", got)
		}
		for _, name := range []string{
			"cortex_query_embedding_cache_misses_total", "cortex_rerank_applied_total",
			"cortex_rerank_gate_fail_total", "cortex_embedding_provider_requests_total",
			"cortex_embedding_provider_errors_total", "cortex_worker_embedding_batches_total",
			"cortex_worker_embeddings_total", "cortex_worker_errors_total",
			"cortex_process_uptime_seconds",
		} {
			if _, ok := samples[name]; !ok {
				t.Fatalf("exposition missing %q", name)
			}
		}
	})
	t.Run("admin denial enforced by the operations gate", func(t *testing.T) {
		ops.authorizeAdminErr = errors.New("not an admin")
		defer func() { ops.authorizeAdminErr = nil }()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Fatalf("denied admin /metrics status = %d, want non-200", rec.Code)
		}
	})
}

func TestRerankGateMetricsSeams(t *testing.T) {
	appliedBefore := prometheusMetrics.rerankApplied.Value()
	gateFailsBefore := prometheusMetrics.rerankGateFails.Value()

	ops := newFakeOperations()
	ops.observations[1] = &domain.Observation{ID: 1, PublicID: "00000000-0000-0000-0000-000000000001", Title: "Rerank metric", Content: "content", Project: "demo", Scope: "project"}
	// WorkspaceID must be set: the hybrid handler returns lexical-only
	// results (never reaching the rerank seam) without a workspace.
	cfg := config.Config{HTTP: config.HTTPConfig{Token: "test-token"}, Server: config.ServerConfig{WorkspaceID: "workspace-metrics"}, Search: config.SearchConfig{DefaultLimit: 10, MaxLimit: 20}}
	auth := requestAuthenticator{
		verifier: verifierFunc(func(_ context.Context, secret, _ string) (domain.Principal, error) {
			if secret != cfg.HTTP.Token {
				return domain.Principal{}, errors.New("unknown credential")
			}
			return domain.Principal{Subject: "user-1", OrgID: "tenant"}, nil
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) { return ops, nil }),
	}
	handler, _ := newHTTPHandlerWithHybridSearch(cfg, requestOperations{}, func(context.Context) error { return nil }, auth.middleware, hybridSearchDependencies{
		vectors: &recordingVectorIndex{}, embeddings: &serverFixedEmbedding{},
		reranker: failingReranker{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/search/hybrid?q=metric&project=demo&mode=semantic", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("hybrid status = %d (rerank must fail open), body=%s", rec.Code, rec.Body.String())
	}
	if got := prometheusMetrics.rerankGateFails.Value() - gateFailsBefore; got != 1 {
		t.Fatalf("rerank gate-fail delta = %d, want 1", got)
	}
	if got := prometheusMetrics.rerankApplied.Value() - appliedBefore; got != 0 {
		t.Fatalf("rerank applied delta = %d, want 0", got)
	}
}

// failingReranker always fails so the fail-open seam is exercised.
type failingReranker struct{}

func (failingReranker) Rerank(_ string, docs []*domain.SearchResult) ([]*domain.SearchResult, error) {
	return nil, fmt.Errorf("injected rerank failure")
}

func TestWorkerDrainIncrementsMetricsSeams(t *testing.T) {
	batchesBefore := prometheusMetrics.workerEmbeddingBatches.Value()
	embeddedBefore := prometheusMetrics.workerEmbeddings.Value()

	source := &fakeEmbeddingSource{batch: workerSourceBatch()}
	worker := &backgroundEmbeddingWorker{
		source:     source,
		embeddings: workerTestEmbedder{},
		vectors:    &workerTestVectorIndex{},
		interval:   0,
	}
	worker.drainBatch(context.Background())

	if got := prometheusMetrics.workerEmbeddingBatches.Value() - batchesBefore; got != 1 {
		t.Fatalf("worker batches delta = %d, want 1", got)
	}
	if got := prometheusMetrics.workerEmbeddings.Value() - embeddedBefore; got <= 0 {
		t.Fatalf("worker embeddings delta = %d, want > 0", got)
	}

	errBefore := prometheusMetrics.workerErrors.Value()
	failing := &backgroundEmbeddingWorker{
		source:     &fakeEmbeddingSource{listErr: errors.New("list down")},
		embeddings: workerTestEmbedder{},
		vectors:    &workerTestVectorIndex{},
		interval:   0,
	}
	failing.drainBatch(context.Background())
	if got := prometheusMetrics.workerErrors.Value() - errBefore; got != 1 {
		t.Fatalf("worker errors delta = %d, want 1", got)
	}
}

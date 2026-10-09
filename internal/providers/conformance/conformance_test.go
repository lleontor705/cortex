package conformance

// Scenario matrix for the provider conformance suite. Every scenario runs
// the REAL Cortex client against the fake OpenAI-compatible provider over
// loopback — no network, no live provider, fully deterministic. The live
// probe (TestLive) activates ONLY behind CORTEX_CONFORMANCE_LIVE_EMBEDDINGS
// and skips otherwise, never failing the offline suite.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/embedding"
	"github.com/lleontor705/cortex/v2/internal/retrieval"
)

func newEmbeddingClient(t *testing.T, p *Provider, model string) embedding.Service {
	t.Helper()
	t.Setenv("CORTEX_EMBEDDING_API_KEY", "conformance-key")
	svc, err := embedding.NewSecure(embedding.Config{
		Provider: "openai-compatible",
		APIKey:   "conformance-key",
		Model:    model,
		BaseURL:  p.BaseURL(),
	}, p.OutboundPolicy())
	if err != nil {
		t.Fatalf("embedding client construction: %v", err)
	}
	if svc == nil {
		t.Fatal("embedding client construction returned nil service")
	}
	return svc
}

func newRerankClient(t *testing.T, p *Provider, model string) retrieval.Reranker {
	t.Helper()
	t.Setenv("CORTEX_RERANK_API_KEY", "conformance-key")
	r, err := retrieval.NewSecureReranker(retrieval.RerankConfig{
		Provider: "openai-compatible",
		Model:    model,
		BaseURL:  p.BaseURL(),
	}, p.OutboundPolicy())
	if err != nil {
		t.Fatalf("rerank client construction: %v", err)
	}
	if r == nil {
		t.Fatal("rerank client construction returned nil reranker")
	}
	return r
}

func sampleDocs() []*domain.SearchResult {
	return []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1, Title: "first", Content: "alpha body"}, Rank: 0},
		{Observation: domain.Observation{ID: 2, Title: "second", Content: "beta body"}, Rank: 0},
	}
}

// --- embeddings contract --------------------------------------------------------

func TestEmbeddingsRoundTripAndDimCaching(t *testing.T) {
	p := NewProvider(Behavior{EmbedDims: 8})
	defer p.Close()

	svc := newEmbeddingClient(t, p, p.behavior.KnownModel)
	vec, err := svc.Embed(context.Background(), "conformance probe")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vec) != 8 {
		t.Fatalf("dims = %d, want 8 (from response)", len(vec))
	}
	if svc.Dimensions() != 8 {
		t.Fatalf("Dimensions() = %d, want cached 8", svc.Dimensions())
	}
	if p.LastEmbedModel() != p.behavior.KnownModel {
		t.Fatalf("client sent model %q", p.LastEmbedModel())
	}
}

// CONTRACT: the client must NOT send a `dimensions` parameter — gateways
// disagree on honoring it (Nan ignores it) — and must derive dims from the
// first response instead.
func TestEmbeddingsDimensionsParamNeverSent(t *testing.T) {
	p := NewProvider(Behavior{EmbedDims: 8, HonorDimensionsParam: true})
	defer p.Close()

	svc := newEmbeddingClient(t, p, p.behavior.KnownModel)
	if _, err := svc.Embed(context.Background(), "probe"); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if p.SawDimensionsParam() {
		t.Fatal("client sent a dimensions parameter; contract says it must not rely on gateway dimension support")
	}
}

// QUIRK REGRESSION (nan.builders): an unknown model id answers HTTP 401
// auth_error. The client must classify model-not-found, NOT auth-failure.
func TestEmbeddingsUnknownModel401ClassifiesModelNotFound(t *testing.T) {
	p := NewProvider(Behavior{})
	defer p.Close()

	svc := newEmbeddingClient(t, p, "ghost-model")
	_, err := svc.Embed(context.Background(), "probe")
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
	ae, ok := embedding.ClassifyError(err)
	if !ok {
		t.Fatalf("error is not a typed APIError: %v", err)
	}
	if got := ae.Class(); got != embedding.ClassModelNotFound {
		t.Fatalf("class = %q, want %q (Nan quirk: 401 auth_error for unknown model is model-not-found). err=%v", got, embedding.ClassModelNotFound, err)
	}
}

func TestEmbeddingsPlain401IsAuthFailure(t *testing.T) {
	p := NewProvider(Behavior{
		UnknownModelStatus: 401,
		UnknownModelBody:   `{"error":{"message":"invalid api key","type":"auth_error"}}`,
	})
	defer p.Close()

	svc := newEmbeddingClient(t, p, "whatever") // any model: 401 is key-driven here
	_, err := svc.Embed(context.Background(), "probe")
	ae, ok := embedding.ClassifyError(err)
	if !ok {
		t.Fatalf("error is not typed: %v", err)
	}
	if ae.Class() != embedding.ClassAuth {
		t.Fatalf("class = %q, want auth-failure", ae.Class())
	}
}

// CONTRACT: 429 is fail-closed for embeddings — the budget paces requests
// client-side, but a provider-side 429 surfaces as a typed rate-limited
// error with NO silent retry (embedding volume is too high to double-spend).
func TestEmbeddings429FailClosedNoRetry(t *testing.T) {
	p := NewProvider(Behavior{EmbeddingsStatus: 429, EmbeddingsBody: `{"error":{"message":"slow down"}}`})
	defer p.Close()

	svc := newEmbeddingClient(t, p, p.behavior.KnownModel)
	_, err := svc.Embed(context.Background(), "probe")
	ae, ok := embedding.ClassifyError(err)
	if !ok {
		t.Fatalf("error is not typed: %v", err)
	}
	if ae.Class() != embedding.ClassRateLimited {
		t.Fatalf("class = %q, want rate-limited", ae.Class())
	}
	if got := p.EmbeddingsCalls(); got != 1 {
		t.Fatalf("embedding calls = %d, want 1 (no retry)", got)
	}
}

// CONTRACT: 5xx is a typed provider-server-error; no retry inside the client.
func TestEmbeddings5xxTyped(t *testing.T) {
	p := NewProvider(Behavior{EmbeddingsStatus: 503, EmbeddingsBody: `{"error":{"message":"overloaded"}}`})
	defer p.Close()

	svc := newEmbeddingClient(t, p, p.behavior.KnownModel)
	_, err := svc.Embed(context.Background(), "probe")
	ae, ok := embedding.ClassifyError(err)
	if !ok {
		t.Fatalf("error is not typed: %v", err)
	}
	if ae.Class() != embedding.ClassServer {
		t.Fatalf("class = %q, want provider-server-error", ae.Class())
	}
	if got := p.EmbeddingsCalls(); got != 1 {
		t.Fatalf("embedding calls = %d, want 1", got)
	}
}

// --- fail-closed construction ----------------------------------------------------

func TestMissingBaseURLFailsClosed(t *testing.T) {
	// Rerank: openai-compatible without base_url must refuse construction.
	t.Setenv("CORTEX_RERANK_API_KEY", "k")
	if _, err := retrieval.NewSecureReranker(retrieval.RerankConfig{
		Provider: "openai-compatible", Model: "m", BaseURL: "",
	}, embedding.OutboundPolicy{AllowLoopback: true, AllowInsecureLoopbackHTTP: true, Timeout: loopbackTimeout}); err == nil {
		t.Fatal("rerank with empty base_url must fail closed")
	}

	// Embedding: secure construction with empty base_url must refuse.
	if _, err := embedding.NewSecure(embedding.Config{
		Provider: "openai-compatible", APIKey: "k", Model: "m",
	}, embedding.OutboundPolicy{AllowLoopback: true, AllowInsecureLoopbackHTTP: true, Timeout: loopbackTimeout}); err == nil {
		t.Fatal("embedding openai-compatible with empty base_url must fail closed")
	}

	// Embedding: the local (non-secure) factory refuses openai-compatible
	// outright — remote-only construction goes through the policy gate.
	if svc := embedding.New(embedding.Config{Provider: "openai-compatible", APIKey: "k", Model: "m", BaseURL: "http://127.0.0.1:1/v1"}); svc != nil {
		t.Fatal("embedding.New must refuse openai-compatible (fail-closed remote-only)")
	}
}

// --- rerank contract ---------------------------------------------------------------

func TestRerankHappyPathOrdersByRelevance(t *testing.T) {
	p := NewProvider(Behavior{})
	defer p.Close()

	r := newRerankClient(t, p, p.behavior.KnownModel)
	out, err := r.Rerank("probe query", sampleDocs())
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("output set changed: %d docs", len(out))
	}
	// Fake scores index 1 higher: output order must be doc 2 then doc 1,
	// with the provider score stored in Rank.
	if out[0].ID != 2 || out[1].ID != 1 {
		t.Fatalf("order = [%d,%d], want [2,1]", out[0].ID, out[1].ID)
	}
	if out[0].Rank != 0.97 {
		t.Fatalf("rank = %v, want provider score 0.97", out[0].Rank)
	}
}

func TestRerankUnknownModel401QuirkClassifiesModelNotFound(t *testing.T) {
	p := NewProvider(Behavior{})
	defer p.Close()

	r := newRerankClient(t, p, "ghost-reranker")
	_, err := r.Rerank("probe", sampleDocs())
	ae, ok := embedding.ClassifyError(err)
	if !ok {
		t.Fatalf("rerank error is not typed: %v", err)
	}
	if ae.Class() != embedding.ClassModelNotFound {
		t.Fatalf("class = %q, want model-not-found; err=%v", ae.Class(), err)
	}
	if got := p.RerankAttempts(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (401 must not retry)", got)
	}
}

// CONTRACT: 429 backs off 1s/2s/4s (Nan RPM 60 enforced client-side) and
// eventually succeeds when the provider recovers.
func TestRerank429BacksOffThenSucceeds(t *testing.T) {
	p := NewProvider(Behavior{Rerank429BeforeSuccess: 1})
	defer p.Close()

	r := newRerankClient(t, p, p.behavior.KnownModel)
	out, err := r.Rerank("probe", sampleDocs())
	if err != nil {
		t.Fatalf("rerank after 429 backoff: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("output set changed")
	}
	if got := p.RerankAttempts(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

// CONTRACT: 429 exhaustion fails after the documented retry budget (3).
func TestRerank429ExhaustionFails(t *testing.T) {
	p := NewProvider(Behavior{Rerank429BeforeSuccess: 1000}) // always 429
	defer p.Close()

	r := newRerankClient(t, p, p.behavior.KnownModel)
	if _, err := r.Rerank("probe", sampleDocs()); err == nil {
		t.Fatal("expected error after 429 exhaustion")
	}
	// Initial attempt + rerankMaxRetries429 (3) retries = 4 total.
	if got := p.RerankAttempts(); got != 4 {
		t.Fatalf("attempts = %d, want 4", got)
	}
}

// CONTRACT: 5xx fails immediately — no retry, no fail-open inside the client
// (the rerank contract is: non-nil error ⇒ discard the returned slice).
func TestRerank5xxNoRetry(t *testing.T) {
	p := NewProvider(Behavior{RerankStatus: 500, RerankBody: `{"error":{"message":"internal"}}`})
	defer p.Close()

	r := newRerankClient(t, p, p.behavior.KnownModel)
	out, err := r.Rerank("probe", sampleDocs())
	if err == nil {
		t.Fatal("expected error on 5xx")
	}
	if out != nil {
		t.Fatal("non-nil error must discard the returned slice")
	}
	if got := p.RerankAttempts(); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

// --- chat completions surface (harness contract) ---------------------------------

func TestChatCompletionsUnknownModelQuirk(t *testing.T) {
	p := NewProvider(Behavior{})
	defer p.Close()

	status, body := postJSON(t, p.URL+"/v1/chat/completions", map[string]any{
		"model":    "ghost",
		"messages": []map[string]string{{"role": "user", "content": "ping"}},
	}, "conformance-key")
	if status != 401 {
		t.Fatalf("chat unknown-model status = %d, want 401", status)
	}
	if !strings.Contains(body, "not found") || !strings.Contains(body, "auth_error") {
		t.Fatalf("quirk body must be a Nan-style auth_error naming the model: %s", body)
	}
}

// --- live probe (opt-in only) -------------------------------------------------------

// TestLiveEmbeddingsDimensionsHonor probes the LIVE configured provider at
// TEST TIME ONLY: it sends one request with and one without a `dimensions`
// parameter and reports whether they are honored. Skipped unless
// CORTEX_CONFORMANCE_LIVE_EMBEDDINGS=1 — the offline suite must never fail
// because a developer has no provider credentials.
func TestLiveEmbeddingsDimensionsHonor(t *testing.T) {
	if os.Getenv("CORTEX_CONFORMANCE_LIVE_EMBEDDINGS") != "1" {
		t.Skip("live provider probe disabled (set CORTEX_CONFORMANCE_LIVE_EMBEDDINGS=1 with CORTEX_EMBEDDING_* settings to enable)")
	}
	base := strings.TrimRight(os.Getenv("CORTEX_EMBEDDING_BASE_URL"), "/")
	model := os.Getenv("CORTEX_EMBEDDING_MODEL")
	key := os.Getenv("CORTEX_EMBEDDING_API_KEY")
	if base == "" || model == "" || key == "" {
		t.Skip("CORTEX_EMBEDDING_BASE_URL / MODEL / API_KEY required for the live probe")
	}
	liveDimensionsProbe(t, base, model, key)
}

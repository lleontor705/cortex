// Package conformance implements the per-provider conformance suite (P5):
// a fake OpenAI-compatible provider (chat completions, embeddings, rerank)
// plus a deterministic scenario matrix run against the REAL Cortex clients
// (internal/embedding, internal/retrieval), codifying known provider quirks
// as regression tests before contract drift reaches production.
//
// KNOWN QUIRKS codified here:
//
//   - nan.builders answers an UNKNOWN model id with HTTP 401 auth_error
//     (first-party OpenAI uses 404 model_not_found). The clients must
//     surface a typed error classifying as model-not-found — NOT
//     auth-failure — whenever the bounded body names an unknown model.
//   - Nan advertises RPM 60 / batch 32: both enforced client-side, and 429s
//     back off with the documented 1s/2s/4s schedule before failing.
//   - The embeddings client never sends a `dimensions` parameter (gateways
//     disagree on honoring it); the client derives dims from the FIRST
//     response and caches them.
//
// The suite is fully offline (httptest loopback). A LIVE provider probe runs
// only when CORTEX_CONFORMANCE_LIVE_EMBEDDINGS=1 plus the CORTEX_EMBEDDING_*
// settings are present — otherwise the live test skips, never fails.
package conformance

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/embedding"
)

// Behavior configures the fake provider's canned responses.
type Behavior struct {
	// KnownModel is the model id the fake accepts; anything else triggers
	// the unknown-model quirk response (default: 401 auth_error).
	KnownModel string
	// EmbedDims is the embedding dimension returned on success.
	EmbedDims int
	// HonorDimensionsParam: when true, a request carrying "dimensions"
	// receives exactly that many dims (OpenAI first-party behavior). When
	// false the parameter is IGNORED (the Nan behavior the client must not
	// rely on).
	HonorDimensionsParam bool
	// UnknownModelStatus/Body default to 401 with a Nan-style auth_error
	// body naming the requested model.
	UnknownModelStatus int
	UnknownModelBody   string
	// Override statuses/bodies for the embedding endpoints (0 = 200).
	EmbeddingsStatus int
	EmbeddingsBody   string
	RerankStatus     int
	RerankBody       string
	ChatStatus       int
	ChatBody         string
	// Rerank429BeforeSuccess makes the rerank endpoint answer that many 429s
	// before a 200 (retry/backoff simulation).
	Rerank429BeforeSuccess int
}

// Provider is an in-process OpenAI-compatible fake with call observability.
type Provider struct {
	*httptest.Server
	behavior Behavior

	mu               sync.Mutex
	rerankAttempts   int
	embeddingsCalls  int
	chatCalls        int
	sawDimensionsReq bool
	lastEmbedModel   string
}

// NewProvider starts the fake provider loopback server.
func NewProvider(behavior Behavior) *Provider {
	if behavior.EmbedDims <= 0 {
		behavior.EmbedDims = 8
	}
	if behavior.KnownModel == "" {
		behavior.KnownModel = "qwen3-embedding-8B"
	}
	if behavior.UnknownModelStatus == 0 {
		behavior.UnknownModelStatus = http.StatusUnauthorized
	}
	if behavior.UnknownModelBody == "" {
		behavior.UnknownModelBody = `{"error":{"message":"model '%s' not found","type":"auth_error"}}`
	}
	p := &Provider{behavior: behavior}
	p.Server = httptest.NewServer(http.HandlerFunc(p.serve))
	return p
}

// Observability -------------------------------------------------------------

// RerankAttempts reports how many rerank HTTP requests the client made.
func (p *Provider) RerankAttempts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rerankAttempts
}

// EmbeddingsCalls reports how many embeddings HTTP requests were made.
func (p *Provider) EmbeddingsCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.embeddingsCalls
}

// SawDimensionsParam reports whether any embeddings request carried a
// "dimensions" field.
func (p *Provider) SawDimensionsParam() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sawDimensionsReq
}

// LastEmbedModel reports the model id of the last embeddings request.
func (p *Provider) LastEmbedModel() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastEmbedModel
}

// Routing -----------------------------------------------------------------

func (p *Provider) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/embeddings"):
		p.serveEmbeddings(w, r)
	case strings.HasSuffix(r.URL.Path, "/rerank"):
		p.serveRerank(w, r)
	case strings.HasSuffix(r.URL.Path, "/chat/completions"):
		p.serveChat(w, r)
	default:
		http.NotFound(w, r)
	}
}

type fakeRequest struct {
	Model      string `json:"model"`
	Input      any    `json:"input"`
	Dimensions *int   `json:"dimensions"`
	Query      string `json:"query"`
	Documents  any    `json:"documents"`
}

func (p *Provider) decode(r *http.Request) fakeRequest {
	var req fakeRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	return req
}

func (p *Provider) isUnknownModel(model string) bool {
	return model != "" && model != p.behavior.KnownModel
}

func (p *Provider) writeError(w http.ResponseWriter, status int, bodyTemplate string, model string) {
	body := bodyTemplate
	if strings.Contains(body, "%s") {
		body = strings.ReplaceAll(body, "%s", model)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (p *Provider) serveEmbeddings(w http.ResponseWriter, r *http.Request) {
	req := p.decode(r)
	p.mu.Lock()
	p.embeddingsCalls++
	if req.Dimensions != nil {
		p.sawDimensionsReq = true
	}
	p.lastEmbedModel = req.Model
	p.mu.Unlock()

	if p.isUnknownModel(req.Model) {
		p.writeError(w, p.behavior.UnknownModelStatus, p.behavior.UnknownModelBody, req.Model)
		return
	}
	if p.behavior.EmbeddingsStatus != 0 && p.behavior.EmbeddingsStatus != http.StatusOK {
		if p.behavior.EmbeddingsBody != "" {
			p.writeError(w, p.behavior.EmbeddingsStatus, p.behavior.EmbeddingsBody, req.Model)
			return
		}
		w.WriteHeader(p.behavior.EmbeddingsStatus)
		return
	}
	if p.behavior.EmbeddingsStatus != 0 {
		// Non-default success status with body passthrough.
		w.WriteHeader(p.behavior.EmbeddingsStatus)
		_, _ = w.Write([]byte(p.behavior.EmbeddingsBody))
		return
	}

	dims := p.behavior.EmbedDims
	if p.behavior.HonorDimensionsParam && req.Dimensions != nil && *req.Dimensions > 0 {
		dims = *req.Dimensions
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": []map[string]any{{"embedding": make([]float32, dims)}},
	})
}

func (p *Provider) serveRerank(w http.ResponseWriter, r *http.Request) {
	req := p.decode(r)
	p.mu.Lock()
	p.rerankAttempts++
	attempts := p.rerankAttempts
	p.mu.Unlock()

	if p.isUnknownModel(req.Model) {
		p.writeError(w, p.behavior.UnknownModelStatus, p.behavior.UnknownModelBody, req.Model)
		return
	}
	if p.behavior.RerankStatus != 0 && p.behavior.RerankStatus != http.StatusOK {
		if p.behavior.RerankBody != "" {
			p.writeError(w, p.behavior.RerankStatus, p.behavior.RerankBody, req.Model)
			return
		}
		w.WriteHeader(p.behavior.RerankStatus)
		return
	}
	if p.behavior.Rerank429BeforeSuccess > 0 && attempts <= p.behavior.Rerank429BeforeSuccess {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"results": []map[string]any{
			{"index": 1, "relevance_score": 0.97},
			{"index": 0, "relevance_score": 0.42},
		},
	})
}

func (p *Provider) serveChat(w http.ResponseWriter, r *http.Request) {
	req := p.decode(r)
	p.mu.Lock()
	p.chatCalls++
	p.mu.Unlock()

	if p.isUnknownModel(req.Model) {
		p.writeError(w, p.behavior.UnknownModelStatus, p.behavior.UnknownModelBody, req.Model)
		return
	}
	if p.behavior.ChatStatus != 0 && p.behavior.ChatStatus != http.StatusOK {
		if p.behavior.ChatBody != "" {
			p.writeError(w, p.behavior.ChatStatus, p.behavior.ChatBody, req.Model)
			return
		}
		w.WriteHeader(p.behavior.ChatStatus)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{{
			"message": map[string]any{"role": "assistant", "content": "pong"},
		}},
	})
}

// Clients -------------------------------------------------------------------

// OutboundPolicy builds a loopback-allowing outbound policy scoped to the
// fake provider's host/port (the same constraints production enforces).
func (p *Provider) OutboundPolicy() embedding.OutboundPolicy {
	u := p.URL // http://127.0.0.1:port
	host := strings.TrimPrefix(strings.TrimSuffix(u, "/"), "http://")
	hostOnly, port, _ := strings.Cut(host, ":")
	portNum, _ := strconv.Atoi(port)
	return embedding.OutboundPolicy{
		AllowedHosts:              []string{hostOnly},
		AllowedPorts:              []int{portNum},
		AllowLoopback:             true,
		AllowInsecureLoopbackHTTP: true,
		Timeout:                   loopbackTimeout,
		MaxRedirects:              2,
	}
}

// BaseURL returns the version-prefixed base clients expect.
func (p *Provider) BaseURL() string { return p.URL + "/v1" }

// loopbackTimeout keeps failing scenarios fast.
const loopbackTimeout = 3 * time.Second

// postJSON is a raw helper for harness-contract tests and the live probe.
func postJSON(t *testing.T, url string, payload any, key string) (status int, body string) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := (&http.Client{Timeout: loopbackTimeout}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, string(b)
}

// liveDimensionsProbe compares the live provider's response with and without
// a `dimensions` parameter. Test-time only; never used by production code.
func liveDimensionsProbe(t *testing.T, base, model, key string) {
	t.Helper()
	without := map[string]any{"model": model, "input": "probe"}
	with := map[string]any{"model": model, "input": "probe", "dimensions": 4}

	st1, b1 := postJSON(t, base+"/embeddings", without, key)
	if st1 != 200 {
		t.Fatalf("live probe (no dimensions): status %d body %s", st1, b1)
	}
	d1 := firstEmbeddingDims(t, b1)

	st2, b2 := postJSON(t, base+"/embeddings", with, key)
	if st2 != 200 {
		t.Logf("live probe (dimensions=4): provider rejected the parameter with %d (%s) — treat as NOT honored", st2, b2)
		return
	}
	d2 := firstEmbeddingDims(t, b2)

	switch {
	case d2 == 4 && d1 != 4:
		t.Logf("live provider HONORS the dimensions parameter (no-param=%d, with-param=%d)", d1, d2)
	case d2 == d1:
		t.Logf("live provider IGNORES the dimensions parameter (dims=%d both) — client contract (never send dimensions) is correct", d1)
	default:
		t.Logf("live provider returned unexpected dims: no-param=%d with-param=4→%d", d1, d2)
	}
}

func firstEmbeddingDims(t *testing.T, body string) int {
	t.Helper()
	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil || len(parsed.Data) == 0 {
		t.Fatalf("unparsable embeddings response: %q", body)
	}
	return len(parsed.Data[0].Embedding)
}

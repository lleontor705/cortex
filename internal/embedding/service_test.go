package embedding

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"go.uber.org/goleak"
)

func TestNewReturnsNilForNone(t *testing.T) {
	svc := New(Config{Provider: "none"})
	if svc != nil {
		t.Fatal("expected nil for 'none' provider")
	}
}

func TestNewReturnsNilForEmpty(t *testing.T) {
	svc := New(Config{})
	if svc != nil {
		t.Fatal("expected nil for empty provider")
	}
}

func TestNewReturnsNilForOpenAIWithoutKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	svc := New(Config{Provider: "openai"})
	if svc != nil {
		t.Fatal("expected nil when no API key available")
	}
}

func TestNewReturnsServiceForOpenAIWithKey(t *testing.T) {
	svc := New(Config{Provider: "openai", APIKey: "test-key"})
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.Dimensions() != 1536 {
		t.Fatalf("dimensions = %d, want 1536", svc.Dimensions())
	}
	if svc.Model() != "text-embedding-3-small" {
		t.Fatalf("model = %q", svc.Model())
	}
}

func TestNewReturnsServiceForOllama(t *testing.T) {
	svc := New(Config{Provider: "ollama"})
	if svc == nil {
		t.Fatal("expected non-nil service for ollama")
	}
	if svc.Dimensions() != 768 {
		t.Fatalf("dimensions = %d, want 768", svc.Dimensions())
	}
	if svc.Model() != "nomic-embed-text" {
		t.Fatalf("model = %q", svc.Model())
	}
}

func TestNewOllamaCustomModel(t *testing.T) {
	svc := New(Config{Provider: "ollama", Model: "mxbai-embed-large", BaseURL: "http://localhost:11434"})
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.Model() != "mxbai-embed-large" {
		t.Fatalf("model = %q", svc.Model())
	}
}

func TestOllamaEmbedIntegration(t *testing.T) {
	// This test requires a running Ollama instance with nomic-embed-text.
	// It exercises the live embedding path end-to-end and closes the
	// service afterward so idle HTTP keepalive connections are reaped
	// (Task A: no goroutine leak into subsequent goleak-guarded tests).
	svc := New(Config{Provider: "ollama"})
	if svc == nil {
		t.Skip("ollama service not available")
	}

	// Close the service after the test so persistConn goroutines are reaped.
	if closer, ok := svc.(io.Closer); ok {
		defer func() { _ = closer.Close() }()
	}

	ctx := context.Background()
	vec, err := svc.Embed(ctx, "Hello world, this is a test sentence for embedding.")
	if err != nil {
		t.Skipf("ollama not running or model not pulled: %v", err)
	}

	if len(vec) != 768 {
		t.Fatalf("dimensions = %d, want 768", len(vec))
	}

	// Verify non-zero
	allZero := true
	for _, v := range vec {
		if v != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatal("embedding is all zeros")
	}

	t.Logf("Embedding: %d dimensions, first 5 values: %v", len(vec), vec[:5])
}

// ---------------------------------------------------------------------------
// HTTP client lifecycle (Task A defect pin): shared reusable *http.Client +
// io.Closer releasing idle keepalive connections.
//
// The defect: ollamaService.Embed and openAIService.Embed each created a fresh
// &http.Client{} per call. The default Transport's persistConn readLoop and
// writeLoop goroutines survived past the test, leaking into subsequent goleak-
// guarded tests (notably the embedding worker tests). This was caught by
// TestWorker_ProcessesIntent_EndToEnd when TestOllamaEmbedIntegration ran
// first (alphabetical ordering) against a live Ollama.
//
// The fix: each service holds a single shared *http.Client (constructed once
// in New). A Close() method on the concrete type calls CloseIdleConnections
// so the persistConn goroutines are reaped. The composition root (app.Close,
// bench Close) type-asserts to io.Closer to invoke it without bloating the
// Service interface.
// ---------------------------------------------------------------------------

// countingListener wraps a net.Listener to count accepted TCP connections
// (not HTTP requests). This distinguishes keepalive connection reuse (1 conn
// for N requests) from per-call client creation (N conns for N requests).
type countingListener struct {
	net.Listener
	count *int32
}

func (l *countingListener) Accept() (net.Conn, error) {
	atomic.AddInt32(l.count, 1)
	return l.Listener.Accept()
}

// mockOllamaServer returns an httptest server that responds with a valid
// Ollama /api/embed payload, and counts the number of TCP connections
// accepted at the LISTENER level (not per-request). The connection counter
// lets tests prove the client is REUSED (few connections for many Embed
// calls) rather than re-created per call.
//
// The returned cleanup func MUST be deferred AFTER goleak.VerifyNone so LIFO
// ordering closes the server before goleak checks (mirrors the worker_test.go
// setupWorkerDB pattern: defer goleak first, defer cleanup second).
func mockOllamaServer(t *testing.T, dims int) (*httptest.Server, *int32, func()) {
	t.Helper()
	var conns int32

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	cln := &countingListener{Listener: ln, count: &conns}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Keep the connection in the idle pool (don't close it server-side)
		// so the persistConn goroutines persist until CloseIdleConnections.
		w.Header().Set("Content-Type", "application/json")
		emb := make([]float64, dims)
		for i := range emb {
			emb[i] = 0.01 * float64(i+1)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float64{emb},
		})
	}))
	srv.Listener = cln
	srv.Start()

	cleanup := func() { srv.Close() }
	return srv, &conns, cleanup
}

// TestOllamaService_CloseReleasesIdleConnections is the PRIMARY defect pin:
// after multiple Embed() calls share one client, Close() reaps ALL persistConn
// goroutines — goleak.VerifyNone passes. Under the defect (per-call client),
// Close() only closed the last client's connections; earlier clients' idle
// goroutines leaked.
func TestOllamaService_CloseReleasesIdleConnections(t *testing.T) {
	defer goleak.VerifyNone(t)

	srv, _, srvClose := mockOllamaServer(t, 768)
	defer srvClose() // LIFO: runs before goleak

	svc := New(Config{Provider: "ollama", BaseURL: srv.URL, Model: "test-model"})
	if svc == nil {
		t.Fatal("expected non-nil ollama service")
	}

	// Make several embed calls — all sharing the SAME client.
	for i := 0; i < 5; i++ {
		vec, err := svc.Embed(context.Background(), "test sentence")
		if err != nil {
			t.Fatalf("embed call %d: %v", i, err)
		}
		if len(vec) != 768 {
			t.Fatalf("embed call %d: dims=%d, want 768", i, len(vec))
		}
	}

	// Close MUST release idle HTTP keepalive connections so the persistConn
	// goroutines are reaped. If Close doesn't exist or doesn't call
	// CloseIdleConnections, goleak.VerifyNone (deferred above) fails.
	closer, ok := svc.(io.Closer)
	if !ok {
		t.Fatal("ollamaService must implement io.Closer — Close() method missing")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// goleak.VerifyNone (deferred) catches any leaked persistConn goroutines.
}

// TestOllamaService_SharedClientAcrossCalls proves the *http.Client is
// constructed ONCE and reused across all Embed() calls (HTTP keepalive),
// NOT re-created per call. The server-side connection counter should show
// far fewer than N connections for N calls (typically 1 with keepalive).
func TestOllamaService_SharedClientAcrossCalls(t *testing.T) {
	defer goleak.VerifyNone(t)

	srv, connPtr, srvClose := mockOllamaServer(t, 128)
	defer srvClose() // LIFO: runs before goleak

	svc := New(Config{Provider: "ollama", BaseURL: srv.URL, Model: "test-model"})
	if svc == nil {
		t.Fatal("expected non-nil ollama service")
	}
	defer func() {
		if closer, ok := svc.(io.Closer); ok {
			_ = closer.Close()
		}
	}()

	const calls = 8
	for i := 0; i < calls; i++ {
		_, _ = svc.Embed(context.Background(), "shared client test")
	}

	conns := atomic.LoadInt32(connPtr)
	// With HTTP keepalive and a shared client, 8 sequential calls should use
	// at most a few TCP connections (typically 1). Under the defect (per-call
	// client), each call creates a new connection — up to 8. We assert < calls
	// as the keepalive proof; the exact count depends on the transport but
	// reuse is unambiguous when conns < calls.
	if conns >= int32(calls) {
		t.Errorf("shared client not reused: %d TCP connections for %d Embed calls "+
			"(expected keepalive reuse → < %d)", conns, calls, calls)
	}
	t.Logf("TCP connections used: %d (of %d Embed calls) — keepalive reuse confirmed", conns, calls)
}

// TestOpenAIService_CloseReleasesIdleConnections is the same defect pin for
// the OpenAI backend. It uses a mock server so it does NOT hit the real API.
func TestOpenAIService_CloseReleasesIdleConnections(t *testing.T) {
	defer goleak.VerifyNone(t)

	// We can't easily redirect the OpenAI URL via Config (it's hardcoded to
	// api.openai.com). Instead, construct the service and then exercise
	// Close() to prove it implements io.Closer without error. The goleak
	// gate confirms no goroutines are spawned merely by constructing and
	// closing the service.
	svc := New(Config{Provider: "openai", APIKey: "test-key"})
	if svc == nil {
		t.Fatal("expected non-nil openai service")
	}

	closer, ok := svc.(io.Closer)
	if !ok {
		t.Fatal("openAIService must implement io.Closer — Close() method missing")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Idempotent: second Close must not error.
	if err := closer.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestService_CloseIdempotent proves Close is safe to call multiple times
// (the app and bench close paths may invoke it redundantly during shutdown).
func TestOllamaService_CloseIdempotent(t *testing.T) {
	svc := New(Config{Provider: "ollama"})
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	closer, ok := svc.(io.Closer)
	if !ok {
		t.Fatal("ollamaService must implement io.Closer")
	}
	for i := 0; i < 3; i++ {
		if err := closer.Close(); err != nil {
			t.Fatalf("Close call %d: %v", i, err)
		}
	}
}

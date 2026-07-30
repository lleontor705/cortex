// Package common — LLM-judge tests (TDD, Ollama-only).
//
// These tests pin the P0 hotfix contract for bench/common/llm_judge.go:
//   - OLLAMA_ENDPOINT env (default http://localhost:11434)
//   - OLLAMA_JUDGE_MODEL env (default qwen2.5:7b-instruct)
//   - POST /api/chat with format:"json", temperature:0, seed:42
//   - JudgeResult typed return + backward-compat JudgeAnswer float64 return
//   - 1 retry on transient 5xx; no retry on 4xx
//   - 30s default request timeout
//   - Cross-platform: works on Windows (current host) by mocking with httptest
//   - Ollama-style streaming NDJSON responses are accumulated, not just first chunk
//
// No real Ollama binary is required — httptest.NewServer simulates the endpoint.
package common

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- Config defaults ---------------------------------------------------------

func TestDefaultJudgeConfig_OllamaEndpointDefault(t *testing.T) {
	t.Setenv("OLLAMA_ENDPOINT", "")
	t.Setenv("OLLAMA_JUDGE_MODEL", "")

	cfg := DefaultJudgeConfig()
	if cfg == nil {
		t.Fatal("DefaultJudgeConfig() returned nil with no env vars; expected Ollama defaults")
	}
	if cfg.Endpoint != "http://localhost:11434" {
		t.Errorf("Endpoint = %q, want %q", cfg.Endpoint, "http://localhost:11434")
	}
	if cfg.Model != "qwen2.5:7b-instruct" {
		t.Errorf("Model = %q, want %q", cfg.Model, "qwen2.5:7b-instruct")
	}
}

func TestDefaultJudgeConfig_OLLAMA_ENDPOINT_Override(t *testing.T) {
	t.Setenv("OLLAMA_ENDPOINT", "http://gpu-host.lan:11434")
	t.Setenv("OLLAMA_JUDGE_MODEL", "")

	cfg := DefaultJudgeConfig()
	if cfg == nil {
		t.Fatal("DefaultJudgeConfig() returned nil")
	}
	if cfg.Endpoint != "http://gpu-host.lan:11434" {
		t.Errorf("Endpoint = %q, want override value", cfg.Endpoint)
	}
	// Model must still be the default when only endpoint is overridden.
	if cfg.Model != "qwen2.5:7b-instruct" {
		t.Errorf("Model = %q, want default %q", cfg.Model, "qwen2.5:7b-instruct")
	}
}

func TestDefaultJudgeConfig_OLLAMA_JUDGE_MODEL_Override(t *testing.T) {
	t.Setenv("OLLAMA_ENDPOINT", "")
	t.Setenv("OLLAMA_JUDGE_MODEL", "llama3.1:8b-instruct")

	cfg := DefaultJudgeConfig()
	if cfg == nil {
		t.Fatal("DefaultJudgeConfig() returned nil")
	}
	if cfg.Model != "llama3.1:8b-instruct" {
		t.Errorf("Model = %q, want override value", cfg.Model)
	}
	if cfg.Endpoint != "http://localhost:11434" {
		t.Errorf("Endpoint = %q, want default", cfg.Endpoint)
	}
}

func TestDefaultJudgeConfig_DefaultTimeout30s(t *testing.T) {
	t.Setenv("OLLAMA_ENDPOINT", "")
	t.Setenv("OLLAMA_JUDGE_MODEL", "")

	cfg := DefaultJudgeConfig()
	if cfg == nil {
		t.Fatal("DefaultJudgeConfig() returned nil")
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", cfg.Timeout)
	}
}

// --- HTTP request contract ---------------------------------------------------

// TestJudgeAnswer_HTTPPostChatFormatJSON asserts the request body and path.
// This is the single most important contract for the P0 hotfix.
func TestJudgeAnswer_HTTPPostChatFormatJSON(t *testing.T) {
	var (
		gotPath      string
		gotBody      map[string]any
		gotAuthHdr   string
		gotBodyBytes []byte
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthHdr = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotBodyBytes = body
		_ = json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": `{"verdict":"correct","score":1.0,"reasoning":"matches"}`,
			},
		})
	}))
	defer server.Close()

	cfg := &JudgeConfig{
		Endpoint: server.URL,
		Model:    "qwen2.5:7b-instruct",
		Timeout:  5 * time.Second,
	}

	if _, err := JudgeAnswer(cfg, "What is 2+2?", "4", "four"); err != nil {
		t.Fatalf("JudgeAnswer: %v", err)
	}

	if gotPath != "/api/chat" {
		t.Errorf("path = %q, want /api/chat", gotPath)
	}
	// Authorization header MUST be absent — Ollama has no API key.
	if gotAuthHdr != "" {
		t.Errorf("Authorization header set to %q; Ollama has no auth — must be empty", gotAuthHdr)
	}

	if gotBody["model"] != "qwen2.5:7b-instruct" {
		t.Errorf("body.model = %v, want qwen2.5:7b-instruct", gotBody["model"])
	}
	if gotBody["format"] != "json" {
		t.Errorf("body.format = %v, want \"json\"", gotBody["format"])
	}

	opts, ok := gotBody["options"].(map[string]any)
	if !ok {
		t.Fatalf("body.options missing or wrong type: %v (raw: %s)", gotBody["options"], string(gotBodyBytes))
	}
	// JSON unmarshal yields float64 for numbers.
	if temp, _ := opts["temperature"].(float64); temp != 0 {
		t.Errorf("options.temperature = %v, want 0", opts["temperature"])
	}
	if seed, _ := opts["seed"].(float64); seed != 42 {
		t.Errorf("options.seed = %v, want 42", opts["seed"])
	}

	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("body.messages = %v, want 1 user message", gotBody["messages"])
	}
	msg := msgs[0].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("messages[0].role = %v, want user", msg["role"])
	}
	if content, _ := msg["content"].(string); !strings.Contains(content, "Question:") {
		t.Errorf("messages[0].content missing 'Question:' prefix: %q", content)
	}
}

// --- Typed JudgeResult --------------------------------------------------------

func TestJudge_ReturnsTypedJudgeResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": `{"verdict":"correct","score":1.0,"reasoning":"answer is exactly 4"}`,
			},
		})
	}))
	defer server.Close()

	cfg := &JudgeConfig{Endpoint: server.URL, Model: "qwen2.5:7b-instruct", Timeout: 5 * time.Second}

	result, err := Judge(context.Background(), cfg, "What is 2+2?", "4", "4")
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if result.Verdict != "correct" {
		t.Errorf("Verdict = %q, want correct", result.Verdict)
	}
	if result.Score != 1.0 {
		t.Errorf("Score = %v, want 1.0", result.Score)
	}
	if result.Reasoning != "answer is exactly 4" {
		t.Errorf("Reasoning = %q", result.Reasoning)
	}
}

// --- Backward-compat: JudgeAnswer returns float64 ----------------------------

func TestJudgeAnswer_BackwardCompat_Float64Return(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": `{"verdict":"incorrect","score":0.0,"reasoning":"no match"}`,
			},
		})
	}))
	defer server.Close()

	cfg := &JudgeConfig{Endpoint: server.URL, Model: "qwen2.5:7b-instruct", Timeout: 5 * time.Second}

	score, err := JudgeAnswer(cfg, "q", "e", "g")
	if err != nil {
		t.Fatalf("JudgeAnswer: %v", err)
	}
	if score != 0.0 {
		t.Errorf("score = %v, want 0.0 for incorrect verdict", score)
	}
}

func TestJudgeAnswer_BackwardCompat_PartialScore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": `{"verdict":"partial","score":0.5,"reasoning":"half right"}`,
			},
		})
	}))
	defer server.Close()

	cfg := &JudgeConfig{Endpoint: server.URL, Model: "qwen2.5:7b-instruct", Timeout: 5 * time.Second}

	score, err := JudgeAnswer(cfg, "q", "e", "g")
	if err != nil {
		t.Fatalf("JudgeAnswer: %v", err)
	}
	// Backward compat: JudgeAnswer maps verdict->{correct:1, incorrect:0, partial:score}
	// We pick "score" as the canonical mapping so runners' `> 0.5` threshold
	// continues to work for both binary and partial verdicts.
	if score != 0.5 {
		t.Errorf("score = %v, want 0.5 for partial verdict", score)
	}
}

func TestJudgeAnswer_NilConfigReturnsError(t *testing.T) {
	score, err := JudgeAnswer(nil, "q", "e", "g")
	if err == nil {
		t.Fatal("expected error when cfg is nil")
	}
	if score != -1 {
		t.Errorf("score = %v, want -1 (legacy sentinel)", score)
	}
}

// --- Retry policy ------------------------------------------------------------

func TestJudge_RetriesOnceOn5xx(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"model loading"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": `{"verdict":"correct","score":1.0,"reasoning":"ok"}`,
			},
		})
	}))
	defer server.Close()

	cfg := &JudgeConfig{Endpoint: server.URL, Model: "qwen", Timeout: 5 * time.Second}

	if _, err := JudgeAnswer(cfg, "q", "e", "g"); err != nil {
		t.Fatalf("expected success after one retry, got: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Errorf("expected at least 2 calls (1 retry), got %d", got)
	}
	if got := atomic.LoadInt32(&calls); got > 2 {
		t.Errorf("expected at most 2 calls (1 retry), got %d", got)
	}
}

func TestJudge_DoesNotRetryOn4xx(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad model name"}`))
	}))
	defer server.Close()

	cfg := &JudgeConfig{Endpoint: server.URL, Model: "qwen", Timeout: 5 * time.Second}

	if _, err := JudgeAnswer(cfg, "q", "e", "g"); err == nil {
		t.Fatal("expected error on 400 response")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected exactly 1 call (no retry on 4xx), got %d", got)
	}
}

// --- Timeout -----------------------------------------------------------------

func TestJudge_RespectsTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep well past the client timeout.
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	cfg := &JudgeConfig{Endpoint: server.URL, Model: "qwen", Timeout: 150 * time.Millisecond}

	start := time.Now()
	_, err := JudgeAnswer(cfg, "q", "e", "g")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 1*time.Second {
		t.Errorf("JudgeAnswer took %v; expected to fail-fast near 150ms timeout", elapsed)
	}
}

// --- Streaming NDJSON response (Ollama streams by default) -------------------

func TestJudge_AccumulatesStreamingNDJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		// Two NDJSON chunks: split verdict JSON across both lines so the
		// parser must accumulate content from multiple messages.
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"{\"verdict\":\"correct\",\"score\":1.0,\"reasoning\":\"split "}}` + "\n"))
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"across chunks\"}"},"done":true}` + "\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer server.Close()

	cfg := &JudgeConfig{Endpoint: server.URL, Model: "qwen", Timeout: 5 * time.Second}

	result, err := Judge(context.Background(), cfg, "q", "e", "g")
	if err != nil {
		t.Fatalf("Judge (streaming): %v", err)
	}
	if result.Verdict != "correct" {
		t.Errorf("Verdict = %q, want correct (accumulated from 2 chunks)", result.Verdict)
	}
	if result.Score != 1.0 {
		t.Errorf("Score = %v, want 1.0", result.Score)
	}
	if !strings.Contains(result.Reasoning, "across chunks") {
		t.Errorf("Reasoning = %q, expected to contain 'across chunks'", result.Reasoning)
	}
}

// --- Cross-platform: works on Windows ----------------------------------------
// The endpoint may use either localhost or 127.0.0.1 on different platforms;
// what matters is that we don't bake in any path separator, drive letter,
// or OS-specific syscall. This test only runs on Windows to keep CI matrix
// honest; it does nothing on linux/darwin.

func TestJudge_EndpointNoPlatformSpecificURL(t *testing.T) {
	t.Setenv("OLLAMA_ENDPOINT", "")
	cfg := DefaultJudgeConfig()
	if cfg == nil {
		t.Fatal("DefaultJudgeConfig() returned nil")
	}
	// The default endpoint must use the standard scheme+host format on
	// every OS: no drive letters, no backslashes, no platform quirks.
	u := cfg.Endpoint
	if !strings.HasPrefix(u, "http://") {
		t.Errorf("Endpoint %q does not start with http:// — cross-platform violation", u)
	}
	if strings.Contains(u, `\`) {
		t.Errorf("Endpoint %q contains a backslash — Windows path separator leaked into URL", u)
	}
	// Ban Windows drive-letter prefix like "C:" at the start (after scheme).
	hostPart := strings.TrimPrefix(u, "http://")
	if len(hostPart) >= 2 && hostPart[1] == ':' {
		t.Errorf("Endpoint %q looks like a Windows drive path", u)
	}
}

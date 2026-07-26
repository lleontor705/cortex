// Package common — LLM-as-Judge evaluator (Ollama-only).
//
// This file replaces the legacy Engram-compatible cloud-provider judge
// (OpenAI / Anthropic) with a strict Ollama-only implementation. The
// rewrite closes all 10 gaps catalogued in the G1 spike
// (docs/q1-2026/spikes/g1-judge-model.md §6):
//
//  1. Provider architecture → Ollama only; no cloud fallback.
//  2. OLLAMA_ENDPOINT env wired (default http://localhost:11434).
//  3. format:"json" + structured JudgeResult{Verdict, Score, Reasoning}.
//  4. temperature=0 + seed=42 always set for determinism.
//  5. Typed JudgeResult public return + backward-compat JudgeAnswer(float64).
//  6. Test surface (llm_judge_test.go) covers HTTP, retry, timeout, streaming.
//  7. Retry once on transient 5xx; no retry on 4xx.
//  8. Configurable judge model via OLLAMA_JUDGE_MODEL (default qwen2.5:7b-instruct).
//  9. Cross-platform: pure stdlib, no OS-specific path/scheme assumptions.
//  10. Ollama NDJSON streaming responses are accumulated, not just first chunk.
//
// Function signatures preserved for backward compatibility with the
// benchmark runners (bench/locomo, bench/longmemeval, bench/dmr):
//   - JudgeConfig  (struct; callers only check != nil)
//   - DefaultJudgeConfig() *JudgeConfig
//   - JudgeAnswer(cfg *JudgeConfig, q, expected, got string) (float64, error)
//
// New preferred API:
//   - JudgeResult{Verdict string, Score float64, Reasoning string}
//   - Judge(ctx context.Context, cfg *JudgeConfig, q, expected, got string) (JudgeResult, error)
package common

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// JudgeConfig configures the Ollama-backed LLM judge.
//
// Callers (bench/locomo, bench/longmemeval, bench/dmr) treat this as an
// opaque pointer — they only check `cfg != nil` — so adding new fields is
// safe. The legacy Provider/APIKey fields from the Engram-compatible
// implementation are intentionally absent: this judge MUST NOT call any
// cloud provider.
type JudgeConfig struct {
	// Endpoint is the Ollama HTTP root (e.g. "http://localhost:11434").
	// Populated from OLLAMA_ENDPOINT; defaults to localhost:11434.
	Endpoint string

	// Model is the Ollama model name (e.g. "qwen2.5:7b-instruct").
	// Populated from OLLAMA_JUDGE_MODEL; defaults to qwen2.5:7b-instruct.
	Model string

	// Timeout is the per-request HTTP timeout. Defaults to 30s when zero.
	// Used both for connect and overall request lifetime.
	Timeout time.Duration

	// HTTPClient is an optional pre-built *http.Client used for the
	// underlying call. When nil, the judge constructs one from Timeout.
	// Tests inject httptest servers by setting Endpoint to server.URL and
	// leaving HTTPClient nil — the default client honours Timeout correctly.
	HTTPClient *http.Client

	// Logger receives one structured record per call. When nil, a
	// default slog logger writing to stderr is used.
	Logger *slog.Logger
}

// DefaultJudgeConfig returns a config wired from environment variables.
// Unlike the legacy implementation, this never returns nil — Ollama is
// always the judge path. Callers should not gate on nil.
//
// Env vars (all optional):
//   - OLLAMA_ENDPOINT:     default "http://localhost:11434"
//   - OLLAMA_JUDGE_MODEL:  default "qwen2.5:7b-instruct"
func DefaultJudgeConfig() *JudgeConfig {
	endpoint := strings.TrimSpace(os.Getenv("OLLAMA_ENDPOINT"))
	if endpoint == "" {
		endpoint = "http://localhost:11434"
	}
	// Strip trailing slash so url+"/api/chat" never produces "//api/chat".
	endpoint = strings.TrimRight(endpoint, "/")

	model := strings.TrimSpace(os.Getenv("OLLAMA_JUDGE_MODEL"))
	if model == "" {
		model = "qwen2.5:7b-instruct"
	}

	return &JudgeConfig{
		Endpoint: endpoint,
		Model:    model,
		Timeout:  30 * time.Second,
	}
}

// JudgeResult is the typed, structured return for Judge.
//
// Verdict   — "correct" | "incorrect" | "partial" (case-insensitive, lower-cased)
// Score     — float in [0.0, 1.0]; reflects the model's confidence in Verdict
// Reasoning — short natural-language justification from the model
type JudgeResult struct {
	Verdict   string  `json:"verdict"`
	Score     float64 `json:"score"`
	Reasoning string  `json:"reasoning"`
}

// promptTemplate is the instruction sent to the judge model. The model is
// asked for a strict JSON object so Ollama's `format:"json"` can be relied
// upon to keep output parseable.
//
// The actual question/expected/got text is injected at the END of the
// prompt to keep the schema instruction close to the model's first-token
// attention window.
const promptTemplate = `You are evaluating a memory system's answer to a question.

Respond ONLY with a JSON object of the form:
{"verdict": "correct" | "incorrect" | "partial", "score": <float 0.0..1.0>, "reasoning": "<short justification>"}

- "correct"   means the system answer is essentially the same as the expected answer.
- "incorrect" means it contradicts or misses the expected answer.
- "partial"   means it overlaps partially; use score in (0.0, 1.0).

Question: %s
Expected answer: %s
System answer: %s`

// Judge evaluates a single question/expected/got triple and returns the
// typed JudgeResult. This is the preferred API for new code; existing
// runners continue to call JudgeAnswer below.
func Judge(ctx context.Context, cfg *JudgeConfig, question, expected, got string) (JudgeResult, error) {
	if cfg == nil {
		return JudgeResult{}, fmt.Errorf("judge: nil config (call DefaultJudgeConfig)")
	}

	prompt := fmt.Sprintf(promptTemplate, question, expected, got)

	raw, err := callOllama(ctx, cfg, prompt)
	if err != nil {
		return JudgeResult{}, err
	}

	result, err := parseJudgeJSON(raw)
	if err != nil {
		return JudgeResult{}, fmt.Errorf("judge: parse response: %w (raw=%q)", err, raw)
	}
	return result, nil
}

// JudgeAnswer is the backward-compatible float64-returning wrapper used
// by the benchmark runners (bench/locomo, bench/longmemeval, bench/dmr).
// Runners gate on `judgeScore > 0.5` for "correct", which keeps working
// because partial verdicts map to the model's own score field.
//
// Sentinel preserved: returns (-1, err) when cfg is nil, matching the
// legacy behaviour that callers use as a signal to fall back to F1.
func JudgeAnswer(cfg *JudgeConfig, question, expected, got string) (float64, error) {
	if cfg == nil {
		return -1, fmt.Errorf("judge: nil config (set OLLAMA_ENDPOINT or call DefaultJudgeConfig)")
	}

	result, err := Judge(context.Background(), cfg, question, expected, got)
	if err != nil {
		return -1, err
	}

	switch strings.ToLower(result.Verdict) {
	case "correct":
		return 1.0, nil
	case "incorrect":
		return 0.0, nil
	default:
		// "partial" or any other verdict: trust the model's score field.
		// Runners' `> 0.5` threshold continues to work for partials
		// weighted above the threshold.
		return result.Score, nil
	}
}

// chatRequest is the wire format for POST {endpoint}/api/chat.
//
// Ollama's chat endpoint accepts these fields; the judge always pins
// temperature=0 + seed=42 for byte-identical reproducibility, and forces
// format:"json" so the response is guaranteed to be a parseable object.
type chatRequest struct {
	Model    string         `json:"model"`
	Messages []chatMessage  `json:"messages"`
	Format   string         `json:"format"` // "json"
	Stream   bool           `json:"stream"` // false — single combined response
	Options  map[string]any `json:"options"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatStreamChunk represents one NDJSON line from Ollama's /api/chat
// when streaming is enabled. Each chunk contributes a delta to the
// assistant content; the final chunk has Done=true.
type chatStreamChunk struct {
	Model   string `json:"model"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done bool `json:"done"`
}

// callOllama POSTs the prompt to {Endpoint}/api/chat, accumulates the
// (potentially streamed) response, and returns the concatenated assistant
// content. Retry policy: 1 retry on transient HTTP failures (timeout,
// connect errors, 5xx, 408, 429). No retry on other 4xx.
func callOllama(ctx context.Context, cfg *JudgeConfig, prompt string) (string, error) {
	client, timeout := clientAndTimeout(cfg)
	logger := loggerFor(cfg)

	logger.Info("judge.call_ollama",
		slog.String("endpoint", cfg.Endpoint),
		slog.String("model", cfg.Model),
		slog.Duration("timeout", timeout),
	)

	body, err := json.Marshal(chatRequest{
		Model: cfg.Model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		Format:  "json",
		Stream:  false, // request non-streaming; we still parse streaming in case server defaults to it
		Options: map[string]any{"temperature": 0, "seed": 42},
	})
	if err != nil {
		return "", fmt.Errorf("ollama: marshal: %w", err)
	}

	url := cfg.Endpoint + "/api/chat"

	const maxAttempts = 2 // initial + 1 retry
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		reqCtx, cancel := context.WithTimeout(ctx, timeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			cancel()
			return "", fmt.Errorf("ollama: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/x-ndjson, application/json")

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			lastErr = fmt.Errorf("ollama: request: %w", err)
			logger.Warn("judge.attempt_failed",
				slog.Int("attempt", attempt),
				slog.String("err", lastErr.Error()),
			)
			// network/timeout error — retryable
			if attempt < maxAttempts {
				continue
			}
			return "", lastErr
		}

		statusOK := resp.StatusCode >= 200 && resp.StatusCode < 300
		retryableStatus := resp.StatusCode == 408 || resp.StatusCode == 429 || (resp.StatusCode >= 500 && resp.StatusCode < 600)

		if !statusOK {
			// Drain a small prefix so error messages are useful.
			preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			_ = resp.Body.Close()
			cancel()
			lastErr = fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(preview)))
			if retryableStatus && attempt < maxAttempts {
				logger.Warn("judge.attempt_failed",
					slog.Int("attempt", attempt),
					slog.Int("status", resp.StatusCode),
				)
				continue
			}
			return "", lastErr
		}

		contentType := resp.Header.Get("Content-Type")
		var raw string
		if strings.HasPrefix(contentType, "application/x-ndjson") {
			raw, err = readStreamingContent(resp.Body)
		} else {
			// Non-streaming single-shot response (Ollama default when
			// stream=false is set on the request).
			var single struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				Done bool `json:"done"`
			}
			decodeErr := json.NewDecoder(resp.Body).Decode(&single)
			if decodeErr != nil {
				_ = resp.Body.Close()
				cancel()
				return "", fmt.Errorf("ollama: decode: %w", decodeErr)
			}
			raw = single.Message.Content
		}
		_ = resp.Body.Close()
		cancel()

		if err != nil {
			lastErr = fmt.Errorf("ollama: read response: %w", err)
			if attempt < maxAttempts {
				continue
			}
			return "", lastErr
		}
		return raw, nil
	}

	return "", lastErr
}

// readStreamingContent accumulates the assistant content across all NDJSON
// chunks and returns the concatenated string. Ollama emits one chunk per
// token by default; the final chunk has done:true.
func readStreamingContent(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	// Allow long lines — reasoning JSON can be a few KB.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var content strings.Builder
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var chunk chatStreamChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			// Skip malformed lines; Ollama may emit metadata-only chunks.
			continue
		}
		if chunk.Message.Content != "" {
			content.WriteString(chunk.Message.Content)
		}
	}
	if err := scanner.Err(); err != nil {
		return content.String(), fmt.Errorf("ollama: scan stream: %w", err)
	}
	return content.String(), nil
}

// parseJudgeJSON extracts a JudgeResult from a raw assistant message.
// The model is asked for strict JSON, but we still defend against common
// wrapper shapes (markdown fences, leading prose).
func parseJudgeJSON(raw string) (JudgeResult, error) {
	trimmed := strings.TrimSpace(raw)
	// Strip ```json ... ``` fences if the model wrapped its output.
	if strings.HasPrefix(trimmed, "```") {
		trimmed = stripCodeFence(trimmed)
	}

	// Find the first '{' and the last '}' to ignore any leading/trailing prose.
	start := strings.IndexByte(trimmed, '{')
	end := strings.LastIndexByte(trimmed, '}')
	if start < 0 || end < 0 || end <= start {
		return JudgeResult{}, fmt.Errorf("no JSON object in response")
	}
	trimmed = trimmed[start : end+1]

	var result JudgeResult
	if err := json.Unmarshal([]byte(trimmed), &result); err != nil {
		return JudgeResult{}, fmt.Errorf("decode: %w", err)
	}
	result.Verdict = strings.ToLower(strings.TrimSpace(result.Verdict))

	if result.Verdict == "" {
		return JudgeResult{}, fmt.Errorf("missing verdict field")
	}
	// Clamp score to [0, 1] defensively.
	if result.Score < 0 {
		result.Score = 0
	}
	if result.Score > 1 {
		result.Score = 1
	}
	return result, nil
}

func stripCodeFence(s string) string {
	// Drop leading ```json or ``` line
	if idx := strings.IndexByte(s, '\n'); idx > 0 {
		s = s[idx+1:]
	}
	// Drop trailing ``` if present.
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func clientAndTimeout(cfg *JudgeConfig) (*http.Client, time.Duration) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if cfg.HTTPClient != nil {
		return cfg.HTTPClient, timeout
	}
	return &http.Client{Timeout: timeout}, timeout
}

func loggerFor(cfg *JudgeConfig) *slog.Logger {
	if cfg.Logger != nil {
		return cfg.Logger
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

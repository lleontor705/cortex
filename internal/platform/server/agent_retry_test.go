package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
)

// scriptedChatTransport serves a fixed sequence of scripted outcomes, one per
// provider attempt: either a transport error or an HTTP status + body. It is
// a pure http.RoundTripper seam, so no network is involved.
type scriptedChatTransport struct {
	mu     sync.Mutex
	script []scriptedChatStep
	calls  int
}

type scriptedChatStep struct {
	err    error
	status int
	body   string
}

func (s *scriptedChatTransport) RoundTrip(*http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	call := s.calls
	s.calls++
	if call >= len(s.script) {
		return nil, errors.New("script exhausted")
	}
	step := s.script[call]
	if step.err != nil {
		return nil, step.err
	}
	return &http.Response{
		StatusCode: step.status,
		Body:       io.NopCloser(strings.NewReader(step.body)),
		Header:     make(http.Header),
	}, nil
}

func newRetryTestProvider(transport http.RoundTripper, sleeps *[]time.Duration) *configuredChatProvider {
	p := &configuredChatProvider{
		cfg:      config.ServerLLMConfig{Provider: "generic", BaseURL: "https://provider.test/v1", Model: "test-model"},
		baseURL:  "https://provider.test/v1",
		client:   &http.Client{Transport: transport},
		sem:      make(chan struct{}, 1),
		maxBody:  1 << 20,
		maxError: 4 << 10,
	}
	p.maxRetries = agentChatMaxRetries
	p.sleep = func(d time.Duration) {
		if sleeps != nil {
			*sleeps = append(*sleeps, d)
		}
	}
	return p
}

func chatSuccessBody() string {
	return `{"choices":[{"message":{"content":"{\"claims\":[{\"text\":\"ok\",\"citation_handles\":[\"src_test_001\"]}]}"}}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`
}

func TestChatProviderRetriesTimeoutsThenSucceeds(t *testing.T) {
	transport := &scriptedChatTransport{script: []scriptedChatStep{
		{err: errors.New("context deadline exceeded")},
		{err: context.DeadlineExceeded},
		{status: http.StatusOK, body: chatSuccessBody()},
	}}
	sleeps := []time.Duration{}
	p := newRetryTestProvider(transport, &sleeps)
	result, err := p.Complete(context.Background(), agentdomain.CompletionRequest{SystemPrompt: "policy", UserPrompt: "evidence"})
	if err != nil {
		t.Fatalf("Complete after two timeouts = %v, want success", err)
	}
	if len(result.Claims) != 1 || result.Claims[0].Text != "ok" {
		t.Fatalf("claims = %#v, want one 'ok' claim", result.Claims)
	}
	if transport.calls != 3 {
		t.Fatalf("provider calls = %d, want 3 (initial + 2 retries)", transport.calls)
	}
	if len(sleeps) != 2 || sleeps[0] <= 0 || sleeps[1] <= sleeps[0] {
		t.Fatalf("backoff sleeps = %v, want two increasing positive waits (1s/2s + jitter)", sleeps)
	}
}

func TestChatProviderRetries429ThenSucceeds(t *testing.T) {
	transport := &scriptedChatTransport{script: []scriptedChatStep{
		{status: http.StatusTooManyRequests, body: `{"error":"rate limited"}`},
		{status: http.StatusOK, body: chatSuccessBody()},
	}}
	sleeps := []time.Duration{}
	p := newRetryTestProvider(transport, &sleeps)
	result, err := p.Complete(context.Background(), agentdomain.CompletionRequest{SystemPrompt: "policy", UserPrompt: "evidence"})
	if err != nil {
		t.Fatalf("Complete after 429 = %v, want success", err)
	}
	if len(result.Claims) != 1 {
		t.Fatalf("claims = %#v, want one claim", result.Claims)
	}
	if transport.calls != 2 || len(sleeps) != 1 {
		t.Fatalf("calls=%d sleeps=%d, want one 429 retried once", transport.calls, len(sleeps))
	}
}

func TestChatProviderDoesNotRetryClientErrors(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		transport := &scriptedChatTransport{script: []scriptedChatStep{
			{status: status, body: `{"error":"client mistake"}`},
		}}
		sleeps := []time.Duration{}
		p := newRetryTestProvider(transport, &sleeps)
		_, err := p.Complete(context.Background(), agentdomain.CompletionRequest{SystemPrompt: "policy", UserPrompt: "evidence"})
		if err == nil {
			t.Fatalf("status %d must fail", status)
		}
		if transport.calls != 1 {
			t.Fatalf("status %d retried: calls = %d, want exactly one attempt", status, transport.calls)
		}
		if len(sleeps) != 0 {
			t.Fatalf("status %d slept between attempts: %v", status, sleeps)
		}
	}
}

func TestChatProviderRetryExhaustionPreservesFailClosedError(t *testing.T) {
	transport := &scriptedChatTransport{script: []scriptedChatStep{
		{status: http.StatusServiceUnavailable, body: `{}`},
		{status: http.StatusServiceUnavailable, body: `{}`},
		{status: http.StatusServiceUnavailable, body: `{}`},
	}}
	sleeps := []time.Duration{}
	p := newRetryTestProvider(transport, &sleeps)
	_, err := p.Complete(context.Background(), agentdomain.CompletionRequest{SystemPrompt: "policy", UserPrompt: "evidence"})
	if err == nil || !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("exhausted retries error = %v, want sanitized 'status 503' failure", err)
	}
	if transport.calls != 1+agentChatMaxRetries {
		t.Fatalf("calls = %d, want %d", transport.calls, 1+agentChatMaxRetries)
	}
	if len(sleeps) != agentChatMaxRetries {
		t.Fatalf("sleeps = %v, want one per retry", sleeps)
	}
}

func TestChatProviderRetryStopsWhenContextCancelledDuringBackoff(t *testing.T) {
	transport := &scriptedChatTransport{script: []scriptedChatStep{
		{status: http.StatusInternalServerError, body: `{}`},
		{status: http.StatusInternalServerError, body: `{}`},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := newRetryTestProvider(transport, nil)
	p.sleep = func(time.Duration) { cancel() }
	_, err := p.Complete(ctx, agentdomain.CompletionRequest{SystemPrompt: "policy", UserPrompt: "evidence"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled after cancellation during backoff", err)
	}
	if transport.calls != 1 {
		t.Fatalf("calls = %d, want no attempt after cancellation", transport.calls)
	}
}

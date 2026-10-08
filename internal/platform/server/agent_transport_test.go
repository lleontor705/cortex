package server

import (
	"net/http"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
)

func TestConfiguredChatProviderIdleConnTimeout(t *testing.T) {
	provider, err := newConfiguredChatProvider(config.ServerLLMConfig{
		Provider: "generic", BaseURL: "https://provider.test/v1", Model: "admin-model",
		MaxConcurrent: 1, MaxRedirects: 1, MaxResponseBodyBytes: 4096, MaxErrorBodyBytes: 1024,
	})
	if err != nil {
		t.Fatalf("newConfiguredChatProvider: %v", err)
	}
	transport, ok := provider.(*configuredChatProvider).client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("chat client transport = %T, want *http.Transport", provider.(*configuredChatProvider).client.Transport)
	}
	if transport.IdleConnTimeout != agentProviderIdleConnTimeout {
		t.Fatalf("chat provider IdleConnTimeout = %s, want %s: idle pooled connections are killed by the provider path and stall requests ~20s (issue #129)", transport.IdleConnTimeout, agentProviderIdleConnTimeout)
	}
	if transport.DisableKeepAlives {
		t.Fatal("chat provider must not disable keep-alives: Connection: close stops request-cancellation from propagating upstream on streaming responses")
	}
}

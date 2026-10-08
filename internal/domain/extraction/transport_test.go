package extraction

import (
	"net/http"
	"testing"
	"time"
)

func TestServerModeExtractionIdleConnTimeout(t *testing.T) {
	policy := DefaultOutboundPolicy()
	if err := policy.ApproveDestination("https://provider.test/v1"); err != nil {
		t.Fatalf("ApproveDestination: %v", err)
	}
	service := NewServiceWithPolicy(Config{Timeout: 45 * time.Second}, policy)
	transport, ok := service.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("extraction client transport = %T, want *http.Transport", service.httpClient.Transport)
	}
	if transport.IdleConnTimeout != 15*time.Second {
		t.Fatalf("extraction IdleConnTimeout = %s, want 15s: idle pooled connections are killed by the provider path and stall requests ~20s (issue #129)", transport.IdleConnTimeout)
	}
}

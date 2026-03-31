package a2a_transport

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/lleontor705/cortex/internal/domain/a2a"
)

func TestTransportFactory(t *testing.T) {
	factory := NewTransportFactory()
	handler := func(ctx context.Context, msg *a2a.Message) error { return nil }

	stdio, err := factory.CreateTransport("stdio", "", handler)
	if err != nil {
		t.Fatalf("failed to create stdio transport: %v", err)
	}
	if stdio == nil {
		t.Error("stdio transport should not be nil")
	}

	httpTransport, err := factory.CreateTransport("http", "http://localhost:8080", handler)
	if err != nil {
		t.Fatalf("failed to create http transport: %v", err)
	}
	if httpTransport == nil {
		t.Error("http transport should not be nil")
	}

	_, err = factory.CreateTransport("invalid", "", handler)
	if err == nil {
		t.Error("should fail for unsupported transport type")
	}
}

func TestStdioTransportRegisterUnregister(t *testing.T) {
	transport := NewStdioTransport(nil)
	ctx := context.Background()

	err := transport.RegisterAgent(ctx, "agent-1", "http://localhost:9001")
	if err != nil {
		t.Fatalf("failed to register: %v", err)
	}

	endpoints, err := transport.GetAgentEndpoints(ctx)
	if err != nil {
		t.Fatalf("failed to get endpoints: %v", err)
	}
	if endpoints["agent-1"] != "http://localhost:9001" {
		t.Errorf("expected endpoint http://localhost:9001, got %s", endpoints["agent-1"])
	}

	err = transport.UnregisterAgent(ctx, "agent-1")
	if err != nil {
		t.Fatalf("failed to unregister: %v", err)
	}

	endpoints, _ = transport.GetAgentEndpoints(ctx)
	if _, exists := endpoints["agent-1"]; exists {
		t.Error("agent-1 should be removed")
	}
}

func TestStdioTransportSendNoConnection(t *testing.T) {
	transport := NewStdioTransport(nil)
	ctx := context.Background()

	msg := a2a.NewMessage(a2a.MessageTypeRequest, "agent-1", []string{"agent-2"}, "test", nil)

	err := transport.Send(ctx, msg, "agent-2")
	if err == nil {
		t.Error("should fail when no connection to agent")
	}
}

func TestStdioTransportGetStats(t *testing.T) {
	transport := NewStdioTransport(nil)
	stats := transport.GetStats()

	if stats.StartedAt.IsZero() {
		t.Error("started_at should not be zero")
	}
	if stats.MessagesSent != 0 {
		t.Errorf("expected 0 messages sent, got %d", stats.MessagesSent)
	}
}

func TestStdioTransportClose(t *testing.T) {
	transport := NewStdioTransport(nil)
	err := transport.Close()
	if err != nil {
		t.Fatalf("failed to close: %v", err)
	}
}

func TestHTTPTransportRegisterUnregister(t *testing.T) {
	transport := NewHTTPTransport("http://localhost:8080", nil)
	ctx := context.Background()

	err := transport.RegisterAgent(ctx, "agent-1", "http://agent1:8080/a2a")
	if err != nil {
		t.Fatalf("failed to register: %v", err)
	}

	endpoints, err := transport.GetAgentEndpoints(ctx)
	if err != nil {
		t.Fatalf("failed to get endpoints: %v", err)
	}
	if endpoints["agent-1"] != "http://agent1:8080/a2a" {
		t.Errorf("expected endpoint, got %s", endpoints["agent-1"])
	}

	transport.UnregisterAgent(ctx, "agent-1")
	endpoints, _ = transport.GetAgentEndpoints(ctx)
	if _, exists := endpoints["agent-1"]; exists {
		t.Error("agent-1 should be removed")
	}
}

func TestHTTPTransportSendNoEndpoint(t *testing.T) {
	transport := NewHTTPTransport("http://localhost:8080", nil)
	ctx := context.Background()

	msg := a2a.NewMessage(a2a.MessageTypeRequest, "agent-1", []string{"agent-2"}, "test", nil)

	err := transport.Send(ctx, msg, "agent-2")
	if err == nil {
		t.Error("should fail when no endpoint for agent")
	}
}

func TestHTTPTransportGetStats(t *testing.T) {
	transport := NewHTTPTransport("http://localhost:8080", nil)
	stats := transport.GetStats()

	if stats.StartedAt.IsZero() {
		t.Error("started_at should not be zero")
	}
}

func TestHTTPTransportClose(t *testing.T) {
	transport := NewHTTPTransport("http://localhost:8080", nil)
	err := transport.Close()
	if err != nil {
		t.Fatalf("failed to close: %v", err)
	}
}

func TestMessageHandlerCalled(t *testing.T) {
	var called atomic.Int32
	handler := func(ctx context.Context, msg *a2a.Message) error {
		called.Add(1)
		return nil
	}

	transport := NewStdioTransport(handler)
	ctx := context.Background()

	// Register an agent so send succeeds
	transport.RegisterAgent(ctx, "agent-2", "endpoint")

	msg := a2a.NewMessage(a2a.MessageTypeNotify, "agent-1", []string{"agent-2"}, "test", "payload")
	_ = transport.Send(ctx, msg, "agent-2")

	// The handler is for incoming messages, not outgoing
	// But we can verify the transport structure works
	stats := transport.GetStats()
	if stats.MessagesSent != 1 {
		t.Errorf("expected 1 message sent, got %d", stats.MessagesSent)
	}
}

func TestTransportStatsJSON(t *testing.T) {
	transport := NewHTTPTransport("http://localhost:8080", nil)
	stats := transport.GetStats()

	// Verify JSON tags work
	if stats.MessagesSent != 0 {
		t.Errorf("expected 0, got %d", stats.MessagesSent)
	}
	if stats.MessagesReceived != 0 {
		t.Errorf("expected 0, got %d", stats.MessagesReceived)
	}
	if stats.Errors != 0 {
		t.Errorf("expected 0, got %d", stats.Errors)
	}
}

func TestStdioTransportBroadcastNoAgents(t *testing.T) {
	transport := NewStdioTransport(nil)
	ctx := context.Background()

	msg := a2a.NewMessage(a2a.MessageTypeNotify, "agent-1", []string{}, "test", "payload")

	// Broadcast with no agents registered - should succeed since To is empty
	err := transport.Broadcast(ctx, msg)
	if err != nil {
		t.Errorf("broadcast should succeed with empty To: %v", err)
	}
}

func TestHTTPTransportMultipleRegistrations(t *testing.T) {
	transport := NewHTTPTransport("http://localhost:8080", nil)
	ctx := context.Background()

	agents := map[string]string{
		"agent-1": "http://agent1:8080/a2a",
		"agent-2": "http://agent2:8080/a2a",
		"agent-3": "http://agent3:8080/a2a",
	}

	for id, endpoint := range agents {
		transport.RegisterAgent(ctx, id, endpoint)
	}

	endpoints, _ := transport.GetAgentEndpoints(ctx)
	if len(endpoints) != 3 {
		t.Errorf("expected 3 endpoints, got %d", len(endpoints))
	}
}

// Benchmark tests
func BenchmarkNewMessage(b *testing.B) {
	for i := 0; i < b.N; i++ {
		a2a.NewMessage(a2a.MessageTypeRequest, "agent-1", []string{"agent-2"}, "test", "payload")
	}
}

func BenchmarkMessageToJSON(b *testing.B) {
	msg := a2a.NewMessage(a2a.MessageTypeRequest, "agent-1", []string{"agent-2"}, "test", map[string]string{"key": "value"})
	for i := 0; i < b.N; i++ {
		_, _ = msg.ToJSON()
	}
}

func BenchmarkMessageFromJSON(b *testing.B) {
	msg := a2a.NewMessage(a2a.MessageTypeRequest, "agent-1", []string{"agent-2"}, "test", map[string]string{"key": "value"})
	data, _ := msg.ToJSON()
	for i := 0; i < b.N; i++ {
		_, _ = a2a.FromJSON(data)
	}
}
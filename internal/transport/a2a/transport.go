// Package a2a_transport provides A2A protocol transport implementations
//
// This package implements different transport mechanisms for A2A protocol
// communication, including stdio (for MCP) and HTTP transports.
package a2a_transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/lleontor705/cortex/internal/domain/a2a"
)

// Transport defines the interface for A2A message transport
type Transport interface {
	Send(ctx context.Context, message *a2a.Message, to ...string) error
	Broadcast(ctx context.Context, message *a2a.Message) error
	Listen(ctx context.Context, handler MessageHandler) error
	RegisterAgent(ctx context.Context, agentID string, endpoint string) error
	UnregisterAgent(ctx context.Context, agentID string) error
	GetAgentEndpoints(ctx context.Context) (map[string]string, error)
	Close() error
	GetStats() TransportStats
}

// MessageHandler handles incoming A2A messages
type MessageHandler func(ctx context.Context, message *a2a.Message) error

// TransportStats provides transport statistics
type TransportStats struct {
	MessagesSent      int64         `json:"messages_sent"`
	MessagesReceived  int64         `json:"messages_received"`
	Errors            int64         `json:"errors"`
	ActiveConnections int           `json:"active_connections"`
	Uptime            time.Duration `json:"uptime"`
	StartedAt         time.Time     `json:"started_at"`
}

// StdioTransport implements A2A over stdio (MCP style)
type StdioTransport struct {
	agents      map[string]string
	mu          sync.RWMutex
	handler     MessageHandler
	stats       TransportStats
	closed      chan struct{}
}

// NewStdioTransport creates a new stdio transport
func NewStdioTransport(handler MessageHandler) *StdioTransport {
	return &StdioTransport{
		agents: make(map[string]string),
		handler: handler,
		stats:  TransportStats{StartedAt: time.Now()},
		closed: make(chan struct{}),
	}
}

// Send sends a message to specific agents over stdio
func (t *StdioTransport) Send(ctx context.Context, message *a2a.Message, to ...string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, agentID := range to {
		if _, exists := t.agents[agentID]; !exists {
			return fmt.Errorf("no connection to agent %s", agentID)
		}
	}

	data, err := message.ToJSON()
	if err != nil {
		t.stats.Errors++
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Write to stdout
	_, err = os.Stdout.Write(append(data, '\n'))
	if err != nil {
		t.stats.Errors++
		return fmt.Errorf("failed to write message: %w", err)
	}

	t.stats.MessagesSent++
	return nil
}

// Broadcast broadcasts a message to all connected agents over stdio
func (t *StdioTransport) Broadcast(ctx context.Context, message *a2a.Message) error {
	message.To = []string{}
	return t.Send(ctx, message)
}

// Listen starts listening for incoming messages over stdio
func (t *StdioTransport) Listen(ctx context.Context, handler MessageHandler) error {
	t.handler = handler
	reader := bufio.NewReader(os.Stdin)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.closed:
				return
			default:
				line, err := reader.ReadBytes('\n')
				if err != nil {
					if err != io.EOF {
						t.stats.Errors++
					}
					return
				}

				message, err := a2a.FromJSON(line)
				if err != nil {
					t.stats.Errors++
					continue
				}

				t.stats.MessagesReceived++
				go func() {
					if err := t.handler(ctx, message); err != nil {
						t.stats.Errors++
					}
				}()
			}
		}
	}()

	return nil
}

// RegisterAgent registers an agent endpoint
func (t *StdioTransport) RegisterAgent(ctx context.Context, agentID string, endpoint string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.agents[agentID] = endpoint
	return nil
}

// UnregisterAgent removes an agent endpoint
func (t *StdioTransport) UnregisterAgent(ctx context.Context, agentID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.agents, agentID)
	return nil
}

// GetAgentEndpoints returns all registered agent endpoints
func (t *StdioTransport) GetAgentEndpoints(ctx context.Context) (map[string]string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	endpoints := make(map[string]string)
	for k, v := range t.agents {
		endpoints[k] = v
	}
	return endpoints, nil
}

// Close closes the stdio transport
func (t *StdioTransport) Close() error {
	close(t.closed)
	return nil
}

// GetStats returns transport statistics
func (t *StdioTransport) GetStats() TransportStats {
	return t.stats
}

// HTTPTransport implements A2A over HTTP
type HTTPTransport struct {
	baseURL string
	agents  map[string]string
	mu      sync.RWMutex
	handler MessageHandler
	stats   TransportStats
	client  *http.Client
	server  *http.Server
	closed  chan struct{}
}

// NewHTTPTransport creates a new HTTP transport
func NewHTTPTransport(baseURL string, handler MessageHandler) *HTTPTransport {
	return &HTTPTransport{
		baseURL: baseURL,
		agents:  make(map[string]string),
		handler: handler,
		stats:   TransportStats{StartedAt: time.Now()},
		client:  &http.Client{Timeout: 30 * time.Second},
		closed:  make(chan struct{}),
	}
}

// Send sends a message to specific agents via HTTP
func (t *HTTPTransport) Send(ctx context.Context, message *a2a.Message, to ...string) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var errs []error
	for _, agentID := range to {
		endpoint, exists := t.agents[agentID]
		if !exists {
			errs = append(errs, fmt.Errorf("no endpoint for agent %s", agentID))
			continue
		}
		if err := t.sendToEndpoint(ctx, message, endpoint); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		t.stats.Errors++
		return fmt.Errorf("some messages failed: %v", errs)
	}
	t.stats.MessagesSent++
	return nil
}

// Broadcast broadcasts a message to all agents via HTTP
func (t *HTTPTransport) Broadcast(ctx context.Context, message *a2a.Message) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var errs []error
	for agentID, endpoint := range t.agents {
		if err := t.sendToEndpoint(ctx, message, endpoint); err != nil {
			errs = append(errs, fmt.Errorf("agent %s: %w", agentID, err))
		}
	}

	if len(errs) > 0 {
		t.stats.Errors++
		return fmt.Errorf("some broadcast messages failed: %v", errs)
	}
	t.stats.MessagesSent++
	return nil
}

func (t *HTTPTransport) sendToEndpoint(ctx context.Context, message *a2a.Message, endpoint string) error {
	data, err := message.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Message-ID", message.ID)
	req.Header.Set("A2A-Message-Type", string(message.Type))
	req.Header.Set("A2A-From", message.From)

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP error: %s", resp.Status)
	}
	return nil
}

// Listen starts listening for incoming HTTP messages
func (t *HTTPTransport) Listen(ctx context.Context, handler MessageHandler) error {
	t.handler = handler

	mux := http.NewServeMux()
	mux.HandleFunc("/a2a/message", t.handleMessage)
	mux.HandleFunc("/a2a/health", t.handleHealth)

	t.server = &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	go func() {
		if err := t.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Server stopped
		}
	}()

	return nil
}

func (t *HTTPTransport) handleMessage(w http.ResponseWriter, r *http.Request) {
	t.stats.MessagesReceived++

	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		t.stats.Errors++
		return
	}

	message, err := a2a.FromJSON(data)
	if err != nil {
		http.Error(w, "Failed to parse message", http.StatusBadRequest)
		t.stats.Errors++
		return
	}

	go func() {
		if err := t.handler(r.Context(), message); err != nil {
			t.stats.Errors++
		}
	}()

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"received","message_id":"%s"}`, message.ID)
}

func (t *HTTPTransport) handleHealth(w http.ResponseWriter, r *http.Request) {
	stats := t.GetStats()
	statsJSON, _ := json.Marshal(stats)
	w.Header().Set("Content-Type", "application/json")
	w.Write(statsJSON)
}

// RegisterAgent registers an agent endpoint
func (t *HTTPTransport) RegisterAgent(ctx context.Context, agentID string, endpoint string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.agents[agentID] = endpoint
	return nil
}

// UnregisterAgent removes an agent endpoint
func (t *HTTPTransport) UnregisterAgent(ctx context.Context, agentID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.agents, agentID)
	return nil
}

// GetAgentEndpoints returns all registered agent endpoints
func (t *HTTPTransport) GetAgentEndpoints(ctx context.Context) (map[string]string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	endpoints := make(map[string]string)
	for k, v := range t.agents {
		endpoints[k] = v
	}
	return endpoints, nil
}

// Close closes the HTTP transport
func (t *HTTPTransport) Close() error {
	close(t.closed)
	if t.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		t.server.Shutdown(ctx)
	}
	return nil
}

// GetStats returns transport statistics
func (t *HTTPTransport) GetStats() TransportStats {
	t.mu.RLock()
	defer t.mu.RUnlock()
	stats := t.stats
	if t.server != nil {
		stats.ActiveConnections = 1
	}
	return stats
}

// TransportFactory creates transports based on configuration
type TransportFactory struct{}

// NewTransportFactory creates a new transport factory
func NewTransportFactory() *TransportFactory {
	return &TransportFactory{}
}

// CreateTransport creates a transport based on the specified type
func (f *TransportFactory) CreateTransport(transportType, baseURL string, handler MessageHandler) (Transport, error) {
	switch transportType {
	case "stdio":
		return NewStdioTransport(handler), nil
	case "http":
		return NewHTTPTransport(baseURL, handler), nil
	default:
		return nil, fmt.Errorf("unsupported transport type: %s", transportType)
	}
}